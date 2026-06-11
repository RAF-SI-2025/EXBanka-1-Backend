package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
	"github.com/exbanka/interbank-service/internal/sitx"
	"github.com/shopspring/decimal"
)

// PeerOptionRecorder is the subset of stockpb.PeerOTCServiceClient
// that this handler depends on, to record option contracts at
// COMMIT_TX time and release vote-time seller-share holds at ROLLBACK_TX.
// Decoupled for testability — production wiring uses the real gRPC client;
// tests can supply a stub.
type PeerOptionRecorder interface {
	RecordOptionContract(ctx context.Context, in *stockpb.RecordOptionContractRequest, opts ...grpc.CallOption) (*stockpb.RecordOptionContractResponse, error)
	ReleaseSellerSharesForNewTx(ctx context.Context, in *stockpb.ReleaseSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReleaseSellerSharesResponse, error)
}

// PeerTxGRPCHandler implements transactionpb.PeerTxServiceServer.
// Phase 3 Task 6: real NEW_TX / COMMIT_TX / ROLLBACK_TX implementations.
// Phase 3 Task 10 wires InitiateOutboundTx by injecting the outbound
// repository, HTTP client, peer-bank lookup, and our routing number.
type PeerTxGRPCHandler struct {
	transactionpb.UnimplementedPeerTxServiceServer
	idemRepo       *repository.PeerIdempotenceRepository
	executor       *sitx.PostingExecutor
	client         sitx.AccountClient
	outRepo        *repository.OutboundPeerTxRepository
	httpClient     *sitx.PeerHTTPClient
	peerLookup     PeerLookupFunc
	ownRouting     int64
	optionRecorder PeerOptionRecorder // optional; nil disables option-leg materialisation

	// Receiver-side 202-async (Task 3): HandleNewTx returns the YES/NO vote
	// synchronously if the background reserve worker finishes within
	// receiveSyncDeadline; otherwise it returns HTTP 202 (pending) and the
	// worker keeps running. inflight de-dups concurrent retransmits to a
	// single worker per (peer:idem).
	receiveSyncDeadline time.Duration
	// workerTimeout bounds the background reserve worker's context so a hung
	// account-service (slow/unreachable) can't leak the goroutine and keep the
	// inflight entry forever. MUST be larger than receiveSyncDeadline. Set in
	// NewPeerTxGRPCHandler; tests override it directly (in-package).
	workerTimeout time.Duration
	mu            sync.Mutex
	inflight      map[string]chan struct{} // peer:idem -> done signal (closed when worker finishes)
}

// PeerLookupFunc resolves a peer-bank-code to a PeerHTTPTarget for outbound
// dispatch. Injected so the handler doesn't depend on the peer-bank
// repository directly.
type PeerLookupFunc func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error)

func NewPeerTxGRPCHandler(
	idemRepo *repository.PeerIdempotenceRepository,
	executor *sitx.PostingExecutor,
	accountClient sitx.AccountClient,
	outRepo *repository.OutboundPeerTxRepository,
	httpClient *sitx.PeerHTTPClient,
	peerLookup PeerLookupFunc,
	ownRouting int64,
	receiveSyncDeadline time.Duration,
) *PeerTxGRPCHandler {
	return &PeerTxGRPCHandler{
		idemRepo:            idemRepo,
		executor:            executor,
		client:              accountClient,
		outRepo:             outRepo,
		httpClient:          httpClient,
		peerLookup:          peerLookup,
		ownRouting:          ownRouting,
		receiveSyncDeadline: receiveSyncDeadline,
		workerTimeout:       2 * time.Minute,
		inflight:            make(map[string]chan struct{}),
	}
}

// SetOptionRecorder wires the cross-bank option-contract recorder.
// Optional — left nil, the handler falls back to logging that an
// option leg was committed but not materialised.
func (h *PeerTxGRPCHandler) SetOptionRecorder(r PeerOptionRecorder) {
	h.optionRecorder = r
}

// HandleNewTx validates the inbound NEW_TX envelope, then runs the cheap
// balance check + reservation in a background worker (Task 3, receiver-side
// 202-async). If the worker finishes within receiveSyncDeadline it returns the
// YES/NO vote synchronously (fast path, identical to the pre-async behaviour);
// otherwise it returns HTTP 202 (pending) and leaves the worker running. The
// final vote is cached in peer_idempotence_records (status="done") so a later
// retransmit returns the same result without re-executing. A retransmit that
// arrives while a `pending` row exists re-kicks the worker (restart recovery)
// and returns pending again.
func (h *PeerTxGRPCHandler) HandleNewTx(ctx context.Context, req *transactionpb.SiTxNewTxRequest) (*transactionpb.SiTxVoteResponse, error) {
	idem := req.GetIdempotenceKey().GetLocallyGeneratedKey()
	peerCode := req.GetPeerBankCode()
	if idem == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing idempotence_key or peer_bank_code")
	}

	existing, found, err := h.idemRepo.Lookup(peerCode, idem)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "idem lookup: %v", err)
	}
	if found && existing.Status == "done" {
		var cached transactionpb.SiTxVoteResponse
		if json.Unmarshal([]byte(existing.ResponsePayloadJSON), &cached) == nil {
			return &cached, nil
		}
		// corrupt payload — fall through to re-execute
	}

	postings := protoToPostings(req.GetPostings())

	// Transaction metadata + correlation identity carried on the NEW_TX
	// envelope, persisted on the idempotence record (Task 10/11). The
	// initiator's transactionId is what COMMIT_TX / ROLLBACK_TX correlate to.
	meta := txMeta{
		Message:         req.GetMessage(),
		PaymentCode:     req.GetPaymentCode(),
		PaymentPurpose:  req.GetPaymentPurpose(),
		CallNumber:      req.GetCallNumber(),
		TxRoutingNumber: req.GetTransactionId().GetRoutingNumber(),
		TxForeignID:     req.GetTransactionId().GetId(),
	}

	if found && existing.Status == "pending" {
		h.startWorker(peerCode, idem, postings, meta) // re-kick if not in-flight (restart recovery)
		return &transactionpb.SiTxVoteResponse{Pending: true}, nil
	}

	sig := h.startWorker(peerCode, idem, postings, meta)
	select {
	case <-sig:
		// Only return the cached vote when the worker actually wrote a done
		// record. If the worker aborted on an infra timeout (runReserve returns
		// nil without caching), the row is still `pending` — re-reading it would
		// unmarshal the placeholder "{}" payload into an empty/zero vote. Guard
		// on Status=="done" so this path returns Pending instead of a bogus vote.
		if rec, ok, lerr := h.idemRepo.Lookup(peerCode, idem); lerr == nil && ok && rec.Status == "done" {
			var cached transactionpb.SiTxVoteResponse
			if json.Unmarshal([]byte(rec.ResponsePayloadJSON), &cached) == nil {
				return &cached, nil
			}
		}
		return &transactionpb.SiTxVoteResponse{Pending: true}, nil
	case <-time.After(h.receiveSyncDeadline):
		_ = h.idemRepo.UpsertPending(&model.PeerIdempotenceRecord{
			PeerBankCode: peerCode, LocallyGeneratedKey: idem,
			TxRoutingNumber: meta.TxRoutingNumber, TxForeignID: meta.TxForeignID,
			Message: meta.Message, PaymentCode: meta.PaymentCode,
			PaymentPurpose: meta.PaymentPurpose, CallNumber: meta.CallNumber,
		})
		return &transactionpb.SiTxVoteResponse{Pending: true}, nil
	}
}

// runReserve performs the cheap balance check + reservation and caches the
// result as a done idempotence record. Returns the proto vote. Safe to call
// from a background goroutine (uses the passed ctx) and idempotent on
// (peerCode, idem) — re-running after a crash re-reserves harmlessly because
// account-service reservations are keyed by peer:idem.
func (h *PeerTxGRPCHandler) runReserve(ctx context.Context, peerCode, idem string, postings []contractsitx.InternalPosting, meta txMeta) *transactionpb.SiTxVoteResponse {
	if vote := sitx.BuildPrelimVote(postings); vote.Type == contractsitx.VoteNo {
		resp := voteToProto(vote)
		_, _ = cacheAndReturn(h.idemRepo, peerCode, idem, "", nil, nil, meta, resp)
		return resp
	}
	res := h.executor.Reserve(ctx, postings, peerCode, idem)
	if ctx.Err() != nil {
		// Aborted by the worker timeout/cancel — the reserve was NOT a
		// deterministic protocol reject, it's an infra timeout. Do NOT cache a
		// done record (that would falsely freeze a NO vote and block re-kick).
		// Leave the pending row as-is so a later retransmit re-kicks a fresh
		// worker (idempotent on peer:idem).
		return nil
	}
	if res.Vote.Type == contractsitx.VoteNo {
		resp := voteToProto(res.Vote)
		_, _ = cacheAndReturn(h.idemRepo, peerCode, idem, "", nil, nil, meta, resp)
		return resp
	}
	txID := uuid.NewString()
	resp := &transactionpb.SiTxVoteResponse{Type: contractsitx.VoteYes, TransactionId: txID}
	_, _ = cacheAndReturn(h.idemRepo, peerCode, idem, txID, res.DebitedItems, res.OptionItems, meta, resp)
	return resp
}

// startWorker ensures a single background worker runs for (peerCode, idem) and
// returns a signal channel CLOSED when the worker finishes (after the done
// record is committed). Concurrent callers for the same key share one worker.
func (h *PeerTxGRPCHandler) startWorker(peerCode, idem string, postings []contractsitx.InternalPosting, meta txMeta) chan struct{} {
	key := peerCode + ":" + idem
	h.mu.Lock()
	if sig, ok := h.inflight[key]; ok {
		h.mu.Unlock()
		return sig
	}
	sig := make(chan struct{})
	h.inflight[key] = sig
	h.mu.Unlock()
	go func() {
		// Bounded background context: the worker must outlive the gRPC request
		// that returned 202, but a hung account-service must NOT leak the
		// goroutine forever. On timeout runReserve writes no done record (see
		// there), so the pending row stays pending and the next retransmit
		// re-kicks a fresh worker (idempotent on peer:idem). A process restart
		// likewise abandons it; the next retransmit re-kicks.
		ctx, cancel := context.WithTimeout(context.Background(), h.workerTimeout)
		defer cancel()
		h.runReserve(ctx, peerCode, idem, postings, meta)
		h.mu.Lock()
		delete(h.inflight, key)
		h.mu.Unlock()
		close(sig)
	}()
	return sig
}

// txMeta carries the NEW_TX envelope metadata + initiator transactionId
// threaded onto the idempotence record at NEW_TX time.
type txMeta struct {
	Message         string
	PaymentCode     string
	PaymentPurpose  string
	CallNumber      string
	TxRoutingNumber int64
	TxForeignID     string
}

// cacheAndReturn inserts the idempotence record and returns the response.
// Per SI-TX, the idempotence record MUST be committed before the
// response is sent — this function call ordering achieves that as long
// as the caller propagates the returned response to the client only
// after this call returns.
//
// debits is the list of immediate-debits performed during reservation;
// persisted as JSON so HandleRollbackTx can credit each entry back if
// the IB later sends ROLLBACK_TX. Pass nil (or empty slice) when there
// were no DEBIT postings on this bank's routing. options carries the
// option-asset legs to materialise at COMMIT_TX time.
func cacheAndReturn(repo *repository.PeerIdempotenceRepository, peerCode, idem, txID string, debits []sitx.DebitedItem, options []sitx.OptionItem, meta txMeta, resp *transactionpb.SiTxVoteResponse) (*transactionpb.SiTxVoteResponse, error) {
	payload, _ := json.Marshal(resp)
	debitsJSON := "[]"
	if len(debits) > 0 {
		if b, err := json.Marshal(debits); err == nil {
			debitsJSON = string(b)
		}
	}
	optionsJSON := "[]"
	if len(options) > 0 {
		if b, err := json.Marshal(options); err == nil {
			optionsJSON = string(b)
		}
	}
	rec := &model.PeerIdempotenceRecord{
		PeerBankCode:        peerCode,
		LocallyGeneratedKey: idem,
		TransactionID:       txID,
		ResponsePayloadJSON: string(payload),
		DebitsJSON:          debitsJSON,
		OptionsJSON:         optionsJSON,
		Message:             meta.Message,
		PaymentCode:         meta.PaymentCode,
		PaymentPurpose:      meta.PaymentPurpose,
		CallNumber:          meta.CallNumber,
		TxRoutingNumber:     meta.TxRoutingNumber,
		TxForeignID:         meta.TxForeignID,
	}
	if err := repo.UpsertDone(rec); err != nil {
		return nil, status.Errorf(codes.Internal, "idem insert: %v", err)
	}
	return resp, nil
}

func (h *PeerTxGRPCHandler) HandleCommitTx(ctx context.Context, req *transactionpb.SiTxCommitRequest) (*transactionpb.SiTxAckResponse, error) {
	// The COMMIT carries its OWN (per-message-unique) idempotence key, which is
	// DIFFERENT from the NEW_TX's. We correlate to the original NEW_TX by
	// transactionId (== the NEW_TX's locally_generated_key, L), NOT by this
	// message's own key.
	peerCode := req.GetPeerBankCode()
	txForeignID := req.GetTransactionId().GetId()
	if txForeignID == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing transaction_id or peer_bank_code")
	}
	// Correlate to the NEW_TX by the INITIATOR's transactionId (tx_foreign_id
	// column), NOT by locally_generated_key — SI-TX §2.8.2 treats transactionId
	// as independent of the idempotence key, so a spec-conformant peer may pick a
	// transactionId.id different from its NEW_TX key.
	rec, found, err := h.idemRepo.LookupByTransactionID(peerCode, txForeignID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "no NEW_TX record for this transaction_id")
	}
	if rec.TransactionID == "" {
		// Previous NEW_TX was a NO vote — nothing to commit.
		return nil, status.Error(codes.FailedPrecondition, "previous NEW_TX vote was NO; cannot COMMIT")
	}
	// Retransmit dedup: a re-delivered COMMIT (fresh idem each time) is a no-op
	// once we've already committed this NEW_TX. The underlying account-service
	// calls are themselves idempotent, so this is a fast-path short-circuit.
	if rec.CommittedAt != nil {
		return &transactionpb.SiTxAckResponse{}, nil
	}
	// All reservation keys are derived from the ORIGINAL NEW_TX key (L), which
	// is rec.LocallyGeneratedKey — never from the COMMIT message's own key.
	key := peerCode + ":" + rec.LocallyGeneratedKey
	// CommitIncoming is idempotent on the reservation key. Account-service
	// commits whatever was reserved under that key, writing the initiator's
	// Message as the ledger entry description. NotFound is benign here — it
	// means this bank had no CREDIT postings on the original NEW_TX (e.g., the
	// OTC accept flow can land on a peer that only had DEBIT postings, which
	// were already finalised at vote-YES time and need no commit step).
	if _, cerr := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
		ReservationKey: key,
		IdempotencyKey: "sitx-commit-" + key,
		Memo:           rec.Message,
	}); cerr != nil {
		if status.Code(cerr) != codes.NotFound {
			return nil, status.Errorf(codes.Internal, "commit: %v", cerr)
		}
	}
	// Settle DEBIT-side outgoing holds (reserve-then-settle): the money now
	// actually leaves the debited accounts. Each hold is keyed by its
	// per-posting idempotency tag, persisted in DebitsJSON at NEW_TX time.
	// SettleOutgoing is idempotent; NotFound is benign (no DEBIT legs on this
	// bank, or the hold was already settled on a prior COMMIT replay).
	var debits []sitx.DebitedItem
	if rec.DebitsJSON != "" && rec.DebitsJSON != "[]" {
		if jerr := json.Unmarshal([]byte(rec.DebitsJSON), &debits); jerr != nil {
			return nil, status.Errorf(codes.Internal, "decode debits: %v", jerr)
		}
	}
	for _, d := range debits {
		if _, serr := h.client.SettleOutgoing(ctx, &accountpb.SettleOutgoingRequest{
			ReservationKey: d.IdempotencyTag,
			IdempotencyKey: "sitx-settle-out-" + d.IdempotencyTag,
		}); serr != nil {
			if status.Code(serr) != codes.NotFound {
				return nil, status.Errorf(codes.Internal, "settle %s: %v", d.IdempotencyTag, serr)
			}
		}
	}
	// Materialise any option-asset legs into peer_option_contracts via
	// stock-service. The list was captured at NEW_TX time and persisted
	// in OptionsJSON so we don't depend on the original postings list,
	// which is no longer available at commit time.
	if err := h.materialiseOptions(ctx, rec.OptionsJSON, key); err != nil {
		return nil, status.Errorf(codes.Internal, "record options: %v", err)
	}
	// Stamp committed_at so a retransmitted COMMIT short-circuits next time.
	// All side effects above are idempotent, so a crash before this stamp just
	// replays them on the next COMMIT delivery.
	if _, merr := h.idemRepo.MarkCommitted(rec.ID); merr != nil {
		return nil, status.Errorf(codes.Internal, "mark committed: %v", merr)
	}
	return &transactionpb.SiTxAckResponse{}, nil
}

// materialiseOptions decodes the persisted OptionsJSON list and
// asks stock-service to record each as a peer_option_contracts row.
// crossbankTxID is "<peerCode>:<idem>" so both banks key contracts
// the same way (idempotency is on (crossbank_tx_id, posting_index)).
//
// OPTION-asset legs are always accept-phase: the executor supplies
// OptionIntentAccept unconditionally. The intent field is no longer
// carried on the wire — it was removed from OptionDescription as part
// of the SI-TX wire-conformance reshape. Exercise flows use a different
// account shape handled by a later task, not the OPTION-asset path.
//
// No-op when optionRecorder is nil or list is empty/`[]`.
// optionsJSONHasDebitLeg reports whether the persisted NEW_TX OptionsJSON
// contains a DEBIT option leg — i.e. this bank held the seller and placed a
// vote-time share hold that ROLLBACK_TX must release.
func optionsJSONHasDebitLeg(optionsJSON string) bool {
	if optionsJSON == "" || optionsJSON == "[]" {
		return false
	}
	var items []sitx.OptionItem
	if err := json.Unmarshal([]byte(optionsJSON), &items); err != nil {
		return false
	}
	for _, it := range items {
		// Only an ACCEPT debit leg placed a vote-time seller-share HOLD that
		// ROLLBACK_TX must release. Exercise items never place a vote-time share
		// hold (the shares were reserved at ACCEPT and are only CONSUMED at the
		// exercise COMMIT), so an exercise_seller DEBIT item must NOT trigger an
		// accept-style share release — its MONAS reservation is released via the
		// normal ReservationKeys/ReleaseIncoming path. Empty Kind == accept
		// (back-compat with items persisted before the discriminator).
		if it.Direction == contractsitx.DirectionDebit &&
			(it.Kind == "" || it.Kind == sitx.OptionKindAccept) {
			return true
		}
	}
	return false
}

func (h *PeerTxGRPCHandler) materialiseOptions(ctx context.Context, optionsJSON, crossbankTxID string) error {
	if h.optionRecorder == nil || optionsJSON == "" || optionsJSON == "[]" {
		return nil
	}
	var items []sitx.OptionItem
	if err := json.Unmarshal([]byte(optionsJSON), &items); err != nil {
		return err
	}
	for _, it := range items {
		req := &stockpb.RecordOptionContractRequest{
			CrossbankTxId:         crossbankTxID,
			PostingIndex:          int32(it.PostingIndex),
			OptionDescriptionJson: it.OptionDescriptionJSON,
			BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: it.Buyer.RoutingNumber, Id: it.Buyer.ID},
			SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: it.Seller.RoutingNumber, Id: it.Seller.ID},
			Direction:             it.Direction,
		}
		// Branch on the item Kind (set by the executor from the transaction SHAPE):
		//   - accept (empty/"accept"): forms the cross-bank contract; Intent=accept,
		//     parties taken from the paired OPTION-asset legs.
		//   - exercise_seller/exercise_buyer: drives recordOptionExercise via
		//     Intent=exercise with the item's Direction (DEBIT seller / CREDIT buyer).
		//     recordOptionExercise looks the contract up by negotiationId+direction and
		//     reads the parties/terms from the stored row, so BuyerId/SellerId here are
		//     only the non-nil placeholders the request validation requires.
		switch it.Kind {
		case sitx.OptionKindExerciseSeller, sitx.OptionKindExerciseBuyer:
			req.Intent = contractsitx.OptionIntentExercise
		default: // "" or OptionKindAccept
			req.Intent = contractsitx.OptionIntentAccept
		}
		if _, err := h.optionRecorder.RecordOptionContract(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (h *PeerTxGRPCHandler) HandleRollbackTx(ctx context.Context, req *transactionpb.SiTxRollbackRequest) (*transactionpb.SiTxAckResponse, error) {
	// ROLLBACK carries its OWN (per-message-unique) idempotence key; we correlate
	// to the original NEW_TX by transactionId (== the NEW_TX's L).
	peerCode := req.GetPeerBankCode()
	txForeignID := req.GetTransactionId().GetId()
	if txForeignID == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing transaction_id or peer_bank_code")
	}
	// Correlate by the INITIATOR's transactionId (tx_foreign_id column), spec-
	// independent of the NEW_TX idempotence key — see HandleCommitTx.
	rec, found, err := h.idemRepo.LookupByTransactionID(peerCode, txForeignID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if !found {
		// No record → nothing to roll back. Idempotent.
		return &transactionpb.SiTxAckResponse{}, nil
	}
	// A committed TX cannot be rolled back. If the NEW_TX already COMMITTED
	// (committed_at set), a late/duplicate ROLLBACK must be a safe no-op — never
	// release settled funds. This guards the spec's "ili u celosti, ili ne
	// uopšte" atomicity against a peer that (incorrectly or due to a race) sends
	// both COMMIT_TX and ROLLBACK_TX for the same transactionId.
	if rec.CommittedAt != nil {
		return &transactionpb.SiTxAckResponse{}, nil
	}
	// Retransmit dedup: a re-delivered ROLLBACK is a no-op once already done.
	if rec.RolledBackAt != nil {
		return &transactionpb.SiTxAckResponse{}, nil
	}
	// All reservation keys are derived from the ORIGINAL NEW_TX key (L =
	// rec.LocallyGeneratedKey), never from the ROLLBACK message's own key.
	key := peerCode + ":" + rec.LocallyGeneratedKey
	// Release CREDIT-side reservations (one reservation key per NEW_TX).
	// NotFound is benign — the original NEW_TX may have had no CREDIT
	// postings on this bank (DEBIT-only legs need credit-back below,
	// not a reservation release).
	if _, rerr := h.client.ReleaseIncoming(ctx, &accountpb.ReleaseIncomingRequest{
		ReservationKey: key,
		IdempotencyKey: "sitx-release-" + key,
	}); rerr != nil {
		if status.Code(rerr) != codes.NotFound {
			return nil, status.Errorf(codes.Internal, "release: %v", rerr)
		}
	}
	// Release any vote-time seller-share hold this bank placed at NEW_TX (a
	// DEBIT option leg on our routing). The hold is keyed on the SI-TX identity
	// (key = "<peerCode>:<idem>"), so one release covers the TX. Idempotent +
	// no-op when absent. OptionsJSON was persisted at NEW_TX so we don't need
	// the original postings list.
	if h.optionRecorder != nil && optionsJSONHasDebitLeg(rec.OptionsJSON) {
		if _, rerr := h.optionRecorder.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{CrossbankTxId: key}); rerr != nil {
			return nil, status.Errorf(codes.Internal, "release shares: %v", rerr)
		}
	}
	// Release DEBIT-side outgoing holds placed during NEW_TX (reserve-then-
	// settle): the money never left, so releasing returns AvailableBalance
	// with no Balance movement. Each hold is keyed by its own per-posting
	// idempotency tag so retries are safe. NotFound is benign (no DEBIT legs
	// on this bank, or already released by the timeout cron).
	var debits []sitx.DebitedItem
	if rec.DebitsJSON != "" && rec.DebitsJSON != "[]" {
		if jerr := json.Unmarshal([]byte(rec.DebitsJSON), &debits); jerr != nil {
			return nil, status.Errorf(codes.Internal, "decode debits: %v", jerr)
		}
	}
	for _, d := range debits {
		if _, rerr := h.client.ReleaseOutgoing(ctx, &accountpb.ReleaseOutgoingRequest{
			ReservationKey: d.IdempotencyTag,
			IdempotencyKey: "sitx-release-out-" + d.IdempotencyTag,
		}); rerr != nil {
			if status.Code(rerr) != codes.NotFound {
				return nil, status.Errorf(codes.Internal, "release %s: %v", d.IdempotencyTag, rerr)
			}
		}
	}
	// Stamp rolled_back_at so a retransmitted ROLLBACK short-circuits next time.
	if _, merr := h.idemRepo.MarkRolledBack(rec.ID); merr != nil {
		return nil, status.Errorf(codes.Internal, "mark rolled_back: %v", merr)
	}
	return &transactionpb.SiTxAckResponse{}, nil
}

// InitiateOutboundTx is the sender-side entry point: gateway → here →
// peer bank. Persists an outbound row, debits the sender immediately,
// then attempts a best-effort NEW_TX → COMMIT_TX dispatch. Failures
// leave the row in `pending` for OutboundReplayCron to resume.
func (h *PeerTxGRPCHandler) InitiateOutboundTx(ctx context.Context, req *transactionpb.SiTxInitiateRequest) (*transactionpb.SiTxInitiateResponse, error) {
	if h.outRepo == nil || h.httpClient == nil || h.peerLookup == nil {
		return nil, status.Error(codes.Unimplemented, "outbound deps not wired")
	}
	if len(req.GetToAccountNumber()) < 3 {
		return nil, status.Error(codes.InvalidArgument, "to_account_number too short")
	}
	peerCode := req.GetToAccountNumber()[:3]
	target, err := h.peerLookup(ctx, peerCode)
	if err != nil || target == nil {
		return nil, status.Errorf(codes.NotFound, "peer bank %s not registered", peerCode)
	}

	idem := uuid.NewString()
	amt, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	// Internal (flat, magnitude-string) form, persisted for the replay cron's
	// local reverse/commit dispatch. The wire envelope below carries the SPEC
	// (signed) form built from these via InternalPostingToSpec.
	internalPostings := []contractsitx.InternalPosting{
		{RoutingNumber: h.ownRouting, AccountType: "ACCOUNT", AccountID: req.GetFromAccountNumber(), AssetType: "MONAS", AssetID: req.GetCurrency(), Amount: amt.Abs().String(), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: target.RoutingNumber, AccountType: "ACCOUNT", AccountID: req.GetToAccountNumber(), AssetType: "MONAS", AssetID: req.GetCurrency(), Amount: amt.Abs().String(), Direction: contractsitx.DirectionCredit},
	}
	postingsJSON, _ := json.Marshal(internalPostings)
	row := &model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   peerCode,
		// Cross-bank client money sends are dispatched from /api/v3/me/payments
		// (a payment to another person at another bank); transfers are
		// intra-bank/same-client only.
		TxKind:       "payment",
		PostingsJSON: string(postingsJSON),
		Status:       "pending",
	}
	if err := h.outRepo.Create(row); err != nil {
		return nil, status.Errorf(codes.Internal, "outbound row: %v", err)
	}

	// Reserve-then-settle: HOLD the sender's money now (AvailableBalance -=
	// amount, Balance untouched), settle it at COMMIT and release it on a NO
	// vote / rollback / timeout. The hold can't be spent elsewhere while the
	// peer decides, but the money hasn't left yet — money-safe and time-safe
	// (account-service's timeout cron releases the hold if the peer never
	// answers). Reservation key "peer-out:<idem>"; idempotent on the key.
	outKey := "peer-out:" + idem
	if _, err := h.client.ReserveOutgoing(ctx, &accountpb.ReserveOutgoingRequest{
		AccountNumber:  req.GetFromAccountNumber(),
		Amount:         req.GetAmount(),
		Currency:       req.GetCurrency(),
		ReservationKey: outKey,
		IdempotencyKey: "peer-out-reserve-" + idem,
	}); err != nil {
		// The hold never landed, so this TX can never settle. Terminate the row
		// (rolled_back) before returning so OutboundReplayCron doesn't resume it
		// and commit a transfer whose money was never held. No reversal needed —
		// nothing was applied.
		_ = h.outRepo.MarkRolledBack(idem, "reserve failed: "+err.Error())
		if status.Code(err) == codes.FailedPrecondition {
			return nil, status.Errorf(codes.FailedPrecondition, "reserve: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "reserve: %v", err)
	}

	// Build the SPEC wire postings (signed amounts) from the internal form.
	wirePostings, convErr := internalToSpecPostings(internalPostings)
	if convErr != nil {
		_ = h.outRepo.MarkRolledBack(idem, "build wire postings: "+convErr.Error())
		// Reverse the hold we just placed before bailing — nothing was sent.
		_, _ = h.client.ReleaseOutgoing(ctx, &accountpb.ReleaseOutgoingRequest{
			ReservationKey: outKey,
			IdempotencyKey: "peer-out-release-" + idem,
		})
		return nil, status.Errorf(codes.Internal, "build wire postings: %v", convErr)
	}

	// transactionId pins the ORIGINAL NEW_TX identity; COMMIT/ROLLBACK below
	// correlate back to it while carrying their OWN fresh idempotence keys.
	txID := contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: idem}

	// Best-effort dispatch. On any error, the row stays pending and the
	// replay cron picks it up on next tick. NEW_TX idempotence key == idem (L);
	// transactionId.id also == L so the receiver keys its record under L.
	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message: contractsitx.Transaction{
			Postings:      wirePostings,
			TransactionID: txID,
			Message:       "Cross-bank payment",
		},
	}
	if vote, err := h.httpClient.PostNewTx(ctx, target, envelope); err != nil {
		_ = h.outRepo.MarkAttempt(idem, err.Error())
	} else if vote.Vote == contractsitx.VoteYes {
		// PIVOT into the forward-only commit phase BEFORE the peer commits or the
		// hold settles. If anything below fails the row stays `committing` and
		// OutboundReplayCron drives it to committed — it is NEVER compensated.
		// Without the pivot, a settle failure after the peer already credited the
		// recipient could be max-attempts-compensated (hold released → sender
		// keeps the money) while the recipient is credited — i.e. money created.
		if cerr := h.outRepo.MarkCommitting(idem); cerr != nil {
			return &transactionpb.SiTxInitiateResponse{TransactionId: idem, PollUrl: "/api/v3/me/payments/" + idem, Status: "pending"}, nil
		}
		// COMMIT carries its OWN unique idempotence key (M), correlating to the
		// NEW_TX (L) via Message.TransactionID.
		commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
			IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: uuid.NewString()},
			MessageType:    contractsitx.MessageTypeCommitTx,
			Message:        contractsitx.CommitTransaction{TransactionID: txID},
		}
		if err := h.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
			_ = h.outRepo.MarkAttempt(idem, "commit: "+err.Error())
		} else {
			// Peer committed → settle the hold (Balance -= amount, money leaves).
			// If settle fails, keep the row `committing` so OutboundReplayCron
			// retries; SettleOutgoing is idempotent on the key.
			if _, serr := h.client.SettleOutgoing(ctx, &accountpb.SettleOutgoingRequest{
				ReservationKey: outKey,
				IdempotencyKey: "peer-out-settle-" + idem,
			}); serr != nil && status.Code(serr) != codes.NotFound {
				_ = h.outRepo.MarkAttempt(idem, "settle: "+serr.Error())
			} else {
				_ = h.outRepo.MarkCommitted(idem)
			}
		}
	} else {
		reason := "peer voted NO"
		if len(vote.Reasons) > 0 {
			reason = "peer voted NO: " + vote.Reasons[0].Reason
		}
		// Atomicity (Celina 5: "ili u celosti, ili ne uopšte"): release the
		// sender's hold on a NO vote (no Balance ever moved — only the hold is
		// lifted). If the release fails, keep the row pending (MarkAttempt) so
		// OutboundReplayCron retries the reversal — marking it rolled_back here
		// would strand the held money in a terminal row nothing revisits. The
		// release key is idempotent, so the retry nets out.
		if _, rErr := h.client.ReleaseOutgoing(ctx, &accountpb.ReleaseOutgoingRequest{
			ReservationKey: outKey,
			IdempotencyKey: "peer-out-release-" + idem,
		}); rErr != nil && status.Code(rErr) != codes.NotFound {
			_ = h.outRepo.MarkAttempt(idem, reason+" (release failed, will retry: "+rErr.Error()+")")
		} else {
			_ = h.outRepo.MarkRolledBack(idem, reason)
			// Release any reservation the peer placed before its NO vote.
			h.rollbackPeer(ctx, target, txID)
		}
	}

	return &transactionpb.SiTxInitiateResponse{
		TransactionId: idem,
		PollUrl:       "/api/v3/me/payments/" + idem,
		Status:        "pending",
	}, nil
}

// InitiateOutboundTxWithPostings is the OTC-friendly variant of
// InitiateOutboundTx — accepts a pre-composed posting list (typically
// 4 postings for OTC accept: premium money + 1× OptionDescription both
// directions) instead of building from {fromAccount, toAccount, amount}.
//
// Reuses the same outbound_peer_txs / PeerHTTPClient / OutboundReplayCron
// flow as the simple-transfer InitiateOutboundTx; the only difference is
// the postings come from the caller and the tx_kind column reflects the
// originating intent (transfer / otc-accept / otc-exercise).
func (h *PeerTxGRPCHandler) InitiateOutboundTxWithPostings(ctx context.Context, req *transactionpb.SiTxInitiateWithPostingsRequest) (*transactionpb.SiTxInitiateResponse, error) {
	if h.outRepo == nil || h.httpClient == nil || h.peerLookup == nil {
		return nil, status.Error(codes.Unimplemented, "outbound deps not wired")
	}
	if len(req.GetPostings()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "postings required")
	}
	target, err := h.peerLookup(ctx, req.GetPeerBankCode())
	if err != nil || target == nil {
		return nil, status.Errorf(codes.NotFound, "peer bank %s not registered", req.GetPeerBankCode())
	}

	idem := uuid.NewString()
	// The incoming proto postings are already in the executor's internal form
	// (flat, magnitude-string Amount + explicit account/asset type tags).
	postings := protoToPostings(req.GetPostings())
	postingsJSON, _ := json.Marshal(postings)

	txKind := req.GetTxKind()
	if txKind == "" {
		txKind = "transfer"
	}
	row := &model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   req.GetPeerBankCode(),
		TxKind:         txKind,
		PostingsJSON:   string(postingsJSON),
		Status:         "pending",
	}
	if err := h.outRepo.Create(row); err != nil {
		return nil, status.Errorf(codes.Internal, "outbound row: %v", err)
	}

	// Apply this bank's own postings locally first, while we still hold
	// a clear vote-time view of the world. Unlike InitiateOutboundTx
	// (which pre-debits a single sender account), OTC-style multi-leg
	// postings have both DEBIT and CREDIT legs on each routing — both
	// peer banks need to see their own legs land. We reuse the same
	// posting executor the receiver runs, with a peer code distinct
	// from any inbound NEW_TX (own routing as a string).
	ownPeerCode := strconv.FormatInt(h.ownRouting, 10)
	localKey := ownPeerCode + ":" + idem
	localResult := h.executor.Reserve(ctx, postings, ownPeerCode, idem)
	if localResult.Vote.Type == contractsitx.VoteNo {
		reason := "local reserve failed"
		if len(localResult.Vote.NoVotes) > 0 {
			reason = "local reserve failed: " + localResult.Vote.NoVotes[0].Reason
		}
		_ = h.outRepo.MarkRolledBack(idem, reason)
		return nil, status.Error(codes.FailedPrecondition, reason)
	}

	// Build SPEC wire postings (signed amounts) from the internal form.
	wirePostings, convErr := internalToSpecPostings(postings)
	if convErr != nil {
		_ = h.outRepo.MarkRolledBack(idem, "build wire postings: "+convErr.Error())
		// Reverse the local legs we just reserved before bailing.
		_ = h.executor.ReverseLocal(ctx, postings, ownPeerCode, idem)
		return nil, status.Errorf(codes.Internal, "build wire postings: %v", convErr)
	}
	// transactionId pins the original NEW_TX identity (L). COMMIT/ROLLBACK
	// correlate to it while carrying their own fresh idempotence keys.
	txID := contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: idem}

	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message: contractsitx.Transaction{
			Postings:      wirePostings,
			TransactionID: txID,
			Message:       "Cross-bank OTC " + txKind,
		},
	}
	// finalStatus mirrors the row's terminal state so the caller can tell a settled
	// accept from a rolled-back one. Previously the response was always "pending",
	// which made the stock-service inbound accept consume the listing + report
	// "accepted" even on a peer NO vote (the user's "listing deleted, no contract").
	// "committed"/"committing" = will settle; "rolled_back" = aborted; "pending" =
	// peer unreachable / will retry via OutboundReplayCron.
	finalStatus := "pending"
	if vote, err := h.httpClient.PostNewTx(ctx, target, envelope); err != nil {
		_ = h.outRepo.MarkAttempt(idem, err.Error())
	} else if vote.Vote == contractsitx.VoteYes {
		// PIVOT: the peer voted YES → it has reserved and will honor a COMMIT.
		// Durably cross into the `committing` phase BEFORE any local settle, so
		// any failure below leaves the row forward-only (OutboundReplayCron drives
		// it to committed via CommitOutboundLocal — it is NEVER compensated/rolled
		// back, which would strand settled money or desync from a committed peer).
		if cerr := h.outRepo.MarkCommitting(idem); cerr != nil {
			// Couldn't pivot (concurrent resolution / terminal) — leave it for the
			// cron; do not attempt the commit inline. The peer voted YES so this is
			// committing-forward, not a failure: report it as such so the caller
			// proceeds (the cron drives it to committed).
			return &transactionpb.SiTxInitiateResponse{TransactionId: idem, PollUrl: "/api/v3/me/otc/transactions/" + idem + "/status", Status: "committing"}, nil
		}
		// Finalise ALL local legs, then tell the peer to commit, then mark
		// committed — but ONLY if every step succeeds. Any failure leaves the row
		// `committing` (via MarkAttempt, which preserves status) so the cron
		// retries the full idempotent sequence and drives it forward.
		localOK := true
		// 1. Finalise local CREDIT-leg reservations. NotFound is benign.
		if _, cerr := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
			ReservationKey: localKey,
			IdempotencyKey: "sitx-localcommit-" + localKey,
		}); cerr != nil && status.Code(cerr) != codes.NotFound {
			_ = h.outRepo.MarkAttempt(idem, "local commit: "+cerr.Error())
			localOK = false
		}
		// 2. Settle local DEBIT-side outgoing holds (e.g. buyer premium money).
		if localOK {
			if serr := h.executor.SettleLocal(ctx, postings, ownPeerCode, idem); serr != nil {
				_ = h.outRepo.MarkAttempt(idem, "local settle: "+serr.Error())
				localOK = false
			}
		}
		// 3. Materialise sender-side option contract rows (seller DEBIT on accept;
		// buyer CREDIT transition + share credit on exercise). crossbankTxID is the
		// local key "<ownRouting>:<idem>", consistent with the wire UUID.
		if localOK {
			if merr := h.materialiseOptions(ctx, optionItemsJSON(localResult.OptionItems), localKey); merr != nil {
				_ = h.outRepo.MarkAttempt(idem, "local option-record: "+merr.Error())
				localOK = false
			}
		}
		// 4. Tell the peer to commit.
		if localOK {
			// COMMIT carries its OWN unique idempotence key (M), correlating to the
			// NEW_TX (L) via Message.TransactionID.
			commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
				IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: uuid.NewString()},
				MessageType:    contractsitx.MessageTypeCommitTx,
				Message:        contractsitx.CommitTransaction{TransactionID: txID},
			}
			if err := h.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
				_ = h.outRepo.MarkAttempt(idem, "commit: "+err.Error())
				localOK = false
			}
		}
		if localOK {
			_ = h.outRepo.MarkCommitted(idem)
			finalStatus = "committed"
		} else {
			// Local finalisation hit a snag after the YES-vote pivot; the row stays
			// `committing` and the cron drives it forward. Report committing (not
			// pending) so the caller treats the accept as settling, not failed.
			finalStatus = "committing"
		}
	} else {
		reason := "peer voted NO"
		if len(vote.Reasons) > 0 {
			reason = "peer voted NO: " + vote.Reasons[0].Reason
		}
		// Peer voted NO → reverse our local legs via the executor: release the
		// CREDIT-side reservation AND release each DEBIT-side outgoing HOLD
		// (reserve-then-settle — the money never left, so this is a hold release,
		// NOT a credit-back) plus any vote-time seller-share hold. ReverseLocal
		// reuses the same per-posting keys, so it's idempotent and matches the
		// OutboundReplayCron reversal path. If it fails, keep the row pending
		// (MarkAttempt) so the cron retries rather than stranding held funds.
		if rerr := h.executor.ReverseLocal(ctx, postings, ownPeerCode, idem); rerr != nil {
			_ = h.outRepo.MarkAttempt(idem, reason+" (local reversal failed, will retry: "+rerr.Error()+")")
			// Reversal pending; the cron finishes it. Leave finalStatus "pending".
		} else {
			_ = h.outRepo.MarkRolledBack(idem, reason)
			finalStatus = "rolled_back"
			// Release any reservation the peer placed before its NO vote.
			h.rollbackPeer(ctx, target, txID)
		}
	}

	// OTC variant: postings come from a cross-bank OTC accept/exercise, so the
	// poll URL points at the OTC transaction-status endpoint (resolves this
	// idem via PeerTxService.GetTxStatus), not the payment endpoint. Status is the
	// row's terminal state so the caller can fail the accept on "rolled_back".
	return &transactionpb.SiTxInitiateResponse{
		TransactionId: idem,
		PollUrl:       "/api/v3/me/otc/transactions/" + idem + "/status",
		Status:        finalStatus,
	}, nil
}

// ReverseOutboundLocal undoes the local balance effects applied at
// initiation for an outbound row that terminally fails without committing
// (peer NO vote or max retries exceeded in OutboundReplayCron). It is wired
// into the cron as its LocalReversalFunc so the cron — which has no account
// client of its own — can credit the sender back.
//
// Dispatch is keyed on tx_kind, which determines the reservation-key scheme the
// initiation path used, so the reversal idempotency keys match and the reversal
// is safe to interleave with the inline NO-vote rollback:
//   - "payment" (InitiateOutboundTx): a single sender hold was placed with
//     reservation key "peer-out:<idem>"; release it (the money never left —
//     only the hold is lifted). No Balance movement.
//   - "transfer" / "otc-*" (InitiateOutboundTxWithPostings): reservations +
//     per-posting outgoing holds were applied via the posting executor with
//     peerCode = our own routing; reverse them via the executor's matching keys.
//
// NB: "transfer" is the WithPostings default (per-posting), NOT the single-hold
// payment path — InitiateOutboundTx sets "payment". Routing "payment" through
// the executor branch (the old bug) left the real "peer-out:<idem>" hold pending.
func (h *PeerTxGRPCHandler) ReverseOutboundLocal(ctx context.Context, row *model.OutboundPeerTx) error {
	var postings []contractsitx.InternalPosting
	if err := json.Unmarshal([]byte(row.PostingsJSON), &postings); err != nil {
		return err
	}
	idem := row.IdempotenceKey
	switch row.TxKind {
	case "", "payment":
		if _, err := h.client.ReleaseOutgoing(ctx, &accountpb.ReleaseOutgoingRequest{
			ReservationKey: "peer-out:" + idem,
			IdempotencyKey: "peer-out-release-" + idem,
		}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		return nil
	default:
		ownPeerCode := strconv.FormatInt(h.ownRouting, 10)
		return h.executor.ReverseLocal(ctx, postings, ownPeerCode, idem)
	}
}

// CommitOutboundLocal finalises this bank's local legs applied at initiation
// time. Wired into OutboundReplayCron as its LocalCommitFunc so the cron —
// which has no account client of its own — can complete the local side of a
// cross-bank commit after a crash between the inline reserve and the inline
// commit/settle calls.
//
//   - "payment" rows (InitiateOutboundTx): settle the single sender HOLD keyed
//     "peer-out:<idem>" (the money leaves). No CREDIT leg.
//   - "transfer" / "otc-*" rows (InitiateOutboundTxWithPostings): commit local
//     CREDIT-leg reservations AND settle per-posting DEBIT-side outgoing holds
//     via the executor.
//
// Idempotency: every step reuses the inline path's keys, so this is safe to
// call multiple times. NotFound at account-service is benign — means no
// matching leg landed on this bank. NB: "payment" (not "transfer") is the
// single-hold path — InitiateOutboundTx sets tx_kind="payment".
func (h *PeerTxGRPCHandler) CommitOutboundLocal(ctx context.Context, row *model.OutboundPeerTx) error {
	if row.TxKind == "" || row.TxKind == "payment" {
		if _, err := h.client.SettleOutgoing(ctx, &accountpb.SettleOutgoingRequest{
			ReservationKey: "peer-out:" + row.IdempotenceKey,
			IdempotencyKey: "peer-out-settle-" + row.IdempotenceKey,
		}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		return nil
	}
	ownPeerCode := strconv.FormatInt(h.ownRouting, 10)
	localKey := ownPeerCode + ":" + row.IdempotenceKey
	if _, err := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
		ReservationKey: localKey,
		IdempotencyKey: "sitx-localcommit-" + localKey,
	}); err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	var postings []contractsitx.InternalPosting
	if err := json.Unmarshal([]byte(row.PostingsJSON), &postings); err != nil {
		return err
	}
	if serr := h.executor.SettleLocal(ctx, postings, ownPeerCode, row.IdempotenceKey); serr != nil {
		return serr
	}
	// Re-materialise this bank's option legs (sender-side contract: a DEBIT
	// seller row on accept, or the CREDIT buyer transition+share-credit on
	// exercise). The inline path does this too, but if it crashed/failed after
	// settling money the cron must redo it — option materialisation is otherwise
	// absent from the recovery path. Idempotent on (crossbank_tx_id, posting_index)
	// and on the exercise contract-status guard. crossbankTxID is the local key
	// "<ownRouting>:<idem>", matching the inline materialise.
	return h.materialiseOptions(ctx, optionItemsJSON(h.executor.ExtractOwnOptionItems(postings)), localKey)
}

// rollbackPeer sends a best-effort, idempotent ROLLBACK_TX to the peer for a
// transaction this bank is abandoning inline (peer voted NO). It releases any
// reservation the peer placed before its NO (a multi-posting OTC NEW_TX can
// reserve earlier legs — e.g. incoming premium or seller shares — before a
// later posting fails). The peer's HandleRollbackTx is idempotent (release by
// key, no-op when absent), so this is safe even when the peer reserved nothing.
// Failure is logged, not fatal — the OutboundReplayCron/PeerTxReconciler and
// the peer's own timeout sweeper are backstops.
func (h *PeerTxGRPCHandler) rollbackPeer(ctx context.Context, target *sitx.PeerHTTPTarget, txID contractsitx.ForeignBankId) {
	if h.httpClient == nil || target == nil {
		return
	}
	// ROLLBACK carries its OWN fresh idempotence key and correlates to the
	// original NEW_TX via Message.TransactionID (the spec's per-message unique
	// key rule). The receiver looks the record up by TransactionID, not by this
	// message's own key.
	env := contractsitx.Message[contractsitx.RollbackTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: uuid.NewString()},
		MessageType:    contractsitx.MessageTypeRollbackTx,
		Message:        contractsitx.RollbackTransaction{TransactionID: txID},
	}
	if err := h.httpClient.PostRollbackTx(ctx, target, env); err != nil {
		log.Printf("inline rollback: ROLLBACK_TX to %s for %s failed (peer may retain reservation until its sweeper): %v", target.BankCode, txID.ID, err)
	}
}

// internalToSpecPostings converts the flat internal postings to the signed
// SPEC wire postings via the shared contract mapping (so the sign↔direction
// inversion is never hand-rolled).
func internalToSpecPostings(in []contractsitx.InternalPosting) ([]contractsitx.Posting, error) {
	out := make([]contractsitx.Posting, 0, len(in))
	for _, ip := range in {
		sp, err := contractsitx.InternalPostingToSpec(ip)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, nil
}

// GetTxStatus implements the Celina-5 CHECK_STATUS mechanism. A peer bank
// can query the state of a cross-bank transaction by its transactionId
// (which equals our IdempotenceKey / UUID). We check both sender-side
// (outbound_peer_txs) and receiver-side (peer_idempotence_records) tables
// and return a unified status so both parties can resume from where they
// stopped.
func (h *PeerTxGRPCHandler) GetTxStatus(ctx context.Context, req *transactionpb.GetTxStatusRequest) (*transactionpb.GetTxStatusResponse, error) {
	txID := req.GetTransactionId()
	callerCode := req.GetCallerPeerBankCode()
	if txID == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id required")
	}

	// 1. Check sender-side: did WE initiate this outbound TX?
	if h.outRepo != nil {
		row, err := h.outRepo.GetByIdempotenceKey(txID)
		if err == nil && row != nil {
			lastActionAt := row.UpdatedAt.UTC().Format(time.RFC3339)
			return &transactionpb.GetTxStatusResponse{
				State:        senderState(row.Status),
				OurRole:      "sender",
				LastActionAt: lastActionAt,
				LastError:    row.LastError,
			}, nil
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.Internal, "outbound lookup: %v", err)
		}
	}

	// 2. Check receiver-side: did WE receive a NEW_TX from this peer for
	// this transaction? We correlate by the initiator's transactionId, which we
	// persist in the tx_foreign_id column at NEW_TX time (LookupByTransactionID).
	// This is independent of how the peer chose its NEW_TX idempotence key.
	if callerCode != "" {
		rec, found, err := h.idemRepo.LookupByTransactionID(callerCode, txID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "idem lookup: %v", err)
		}
		if found {
			lastActionAt := rec.CreatedAt.UTC().Format(time.RFC3339)
			if rec.CommittedAt != nil {
				lastActionAt = rec.CommittedAt.UTC().Format(time.RFC3339)
			} else if rec.RolledBackAt != nil {
				lastActionAt = rec.RolledBackAt.UTC().Format(time.RFC3339)
			}
			return &transactionpb.GetTxStatusResponse{
				State:        receiverState(rec),
				OurRole:      "receiver",
				LastActionAt: lastActionAt,
				LastError:    "",
			}, nil
		}
	}

	// 3. Unknown — we have no record of this transaction.
	return &transactionpb.GetTxStatusResponse{
		State:        "unknown",
		OurRole:      "",
		LastActionAt: "",
		LastError:    "",
	}, nil
}

// receiverState maps a receiver-side idempotence record to the public
// CHECK_STATUS vocabulary. It must NEVER report "committed" before COMMIT_TX
// actually landed: a peer that polls our status and hears "committed" will
// settle its own side, so a premature "committed" on a transaction we may yet
// roll back would desync the two ledgers (this was the always-"committed" bug).
//
//   - RolledBackAt set        → "rolled_back" (we released our reservations).
//   - CommittedAt set         → "committed"   (COMMIT_TX applied; money moved).
//   - still preparing (async) → "prepared".
//   - vote cached as NO       → "rolled_back" (the tx will not commit here).
//   - otherwise (voted YES, holding reservations, awaiting COMMIT/ROLLBACK)
//     → "prepared".
func receiverState(rec *model.PeerIdempotenceRecord) string {
	if rec.RolledBackAt != nil {
		return "rolled_back"
	}
	if rec.CommittedAt != nil {
		return "committed"
	}
	if rec.Status == "pending" {
		return "prepared" // 202-async reserve still running; no vote/commit yet
	}
	var cached struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(rec.ResponsePayloadJSON), &cached); err == nil && cached.Type == contractsitx.VoteNo {
		return "rolled_back"
	}
	return "prepared"
}

// senderState maps the internal outbound_peer_tx status to the public
// CHECK_STATUS vocabulary.
func senderState(s string) string {
	switch s {
	case "committed":
		return "committed"
	case "rolled_back":
		return "rolled_back"
	case "failed":
		return "dead_letter"
	default:
		// "pending" | "committing" | anything unexpected → "prepared"
		return "prepared"
	}
}

func protoToPostings(in []*transactionpb.SiTxPosting) []contractsitx.InternalPosting {
	out := make([]contractsitx.InternalPosting, len(in))
	for i, p := range in {
		out[i] = contractsitx.InternalPosting{
			RoutingNumber: p.GetRoutingNumber(),
			AccountID:     p.GetAccountId(),
			AssetID:       p.GetAssetId(),
			Amount:        p.GetAmount(),
			Direction:     p.GetDirection(),
			AccountType:   p.GetAccountType(),
			AssetType:     p.GetAssetType(),
		}
	}
	return out
}

func voteToProto(v sitx.Vote) *transactionpb.SiTxVoteResponse {
	out := &transactionpb.SiTxVoteResponse{Type: v.Type}
	for _, nv := range v.NoVotes {
		entry := &transactionpb.SiTxNoVote{Reason: nv.Reason}
		if nv.Posting != nil {
			entry.PostingIndex = int32(*nv.Posting)
			entry.PostingIndexSet = true
		}
		out.NoVotes = append(out.NoVotes, entry)
	}
	return out
}

// optionItemsJSON serialises an OptionItem slice for the
// materialiseOptions helper. Empty slice → "[]" so the helper's
// fast-path skip works for non-OTC TXs.
func optionItemsJSON(items []sitx.OptionItem) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}
