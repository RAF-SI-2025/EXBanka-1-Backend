package handler

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
)

// PeerOptionRecorder is the subset of stockpb.PeerOTCServiceClient
// that this handler depends on, to record option contracts at
// COMMIT_TX time. Decoupled for testability — production wiring uses
// the real gRPC client; tests can supply a stub.
type PeerOptionRecorder interface {
	RecordOptionContract(ctx context.Context, in *stockpb.RecordOptionContractRequest, opts ...grpc.CallOption) (*stockpb.RecordOptionContractResponse, error)
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
) *PeerTxGRPCHandler {
	return &PeerTxGRPCHandler{
		idemRepo:   idemRepo,
		executor:   executor,
		client:     accountClient,
		outRepo:    outRepo,
		httpClient: httpClient,
		peerLookup: peerLookup,
		ownRouting: ownRouting,
	}
}

// SetOptionRecorder wires the cross-bank option-contract recorder.
// Optional — left nil, the handler falls back to logging that an
// option leg was committed but not materialised.
func (h *PeerTxGRPCHandler) SetOptionRecorder(r PeerOptionRecorder) {
	h.optionRecorder = r
}

// HandleNewTx validates the inbound NEW_TX envelope, runs the cheap
// balance check via vote_builder, and on YES executes the credit-side
// reservations via posting_executor. The response is cached in
// peer_idempotence_records so replays return the same vote without
// re-executing.
func (h *PeerTxGRPCHandler) HandleNewTx(ctx context.Context, req *transactionpb.SiTxNewTxRequest) (*transactionpb.SiTxVoteResponse, error) {
	idem := req.GetIdempotenceKey().GetLocallyGeneratedKey()
	peerCode := req.GetPeerBankCode()
	if idem == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing idempotence_key or peer_bank_code")
	}

	// Replay-cache hit?
	if existing, found, err := h.idemRepo.Lookup(peerCode, idem); err != nil {
		return nil, status.Errorf(codes.Internal, "idem lookup: %v", err)
	} else if found {
		var cached transactionpb.SiTxVoteResponse
		if jerr := json.Unmarshal([]byte(existing.ResponsePayloadJSON), &cached); jerr == nil {
			return &cached, nil
		}
		// Cached payload corrupt — log and fall through to re-execute. The
		// idem record's tx_id still anchors the response if we got here.
	}

	postings := protoToPostings(req.GetPostings())

	// Cheap balance check first — avoids hitting account-service for
	// trivially-rejectable envelopes.
	if vote := sitx.BuildPrelimVote(postings); vote.Type == contractsitx.VoteNo {
		return cacheAndReturn(h.idemRepo, peerCode, idem, "", nil, nil, voteToProto(vote))
	}

	// Execute reservations.
	res := h.executor.Reserve(ctx, postings, peerCode, idem)
	if res.Vote.Type == contractsitx.VoteNo {
		return cacheAndReturn(h.idemRepo, peerCode, idem, "", nil, nil, voteToProto(res.Vote))
	}

	txID := uuid.NewString()
	resp := &transactionpb.SiTxVoteResponse{Type: contractsitx.VoteYes, TransactionId: txID}
	return cacheAndReturn(h.idemRepo, peerCode, idem, txID, res.DebitedItems, res.OptionItems, resp)
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
func cacheAndReturn(repo *repository.PeerIdempotenceRepository, peerCode, idem, txID string, debits []sitx.DebitedItem, options []sitx.OptionItem, resp *transactionpb.SiTxVoteResponse) (*transactionpb.SiTxVoteResponse, error) {
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
	}
	if err := repo.Insert(rec); err != nil {
		return nil, status.Errorf(codes.Internal, "idem insert: %v", err)
	}
	return resp, nil
}

func (h *PeerTxGRPCHandler) HandleCommitTx(ctx context.Context, req *transactionpb.SiTxCommitRequest) (*transactionpb.SiTxAckResponse, error) {
	idem := req.GetIdempotenceKey().GetLocallyGeneratedKey()
	peerCode := req.GetPeerBankCode()
	if idem == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing idempotence_key or peer_bank_code")
	}
	rec, found, err := h.idemRepo.Lookup(peerCode, idem)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "no NEW_TX record for this idempotence key")
	}
	if rec.TransactionID == "" {
		// Previous NEW_TX was a NO vote — nothing to commit.
		return nil, status.Error(codes.FailedPrecondition, "previous NEW_TX vote was NO; cannot COMMIT")
	}
	// CommitIncoming is idempotent on the reservation key. We don't
	// have the per-posting list at commit time (it was discarded after
	// NEW_TX); the reservation key is "<peerCode>:<idem>". Account-service
	// commits whatever was reserved under that key. NotFound is benign
	// here — it means this bank had no CREDIT postings on the original
	// NEW_TX (e.g., the OTC accept flow can land on a peer that only
	// had DEBIT postings, which were already finalised at vote-YES time
	// and need no commit step).
	key := peerCode + ":" + idem
	if _, cerr := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
		ReservationKey: key,
		IdempotencyKey: "sitx-commit-" + key,
	}); cerr != nil {
		if status.Code(cerr) != codes.NotFound {
			return nil, status.Errorf(codes.Internal, "commit: %v", cerr)
		}
	}
	// Materialise any option-asset legs into peer_option_contracts via
	// stock-service. The list was captured at NEW_TX time and persisted
	// in OptionsJSON so we don't depend on the original postings list,
	// which is no longer available at commit time.
	if err := h.materialiseOptions(ctx, rec.OptionsJSON, key); err != nil {
		return nil, status.Errorf(codes.Internal, "record options: %v", err)
	}
	return &transactionpb.SiTxAckResponse{}, nil
}

// materialiseOptions decodes the persisted OptionsJSON list and
// asks stock-service to record each as a peer_option_contracts row.
// crossbankTxID is "<peerCode>:<idem>" so both banks key contracts
// the same way (idempotency is on (crossbank_tx_id, posting_index)).
// No-op when optionRecorder is nil or list is empty/`[]`.
func (h *PeerTxGRPCHandler) materialiseOptions(ctx context.Context, optionsJSON, crossbankTxID string) error {
	if h.optionRecorder == nil || optionsJSON == "" || optionsJSON == "[]" {
		return nil
	}
	var items []sitx.OptionItem
	if err := json.Unmarshal([]byte(optionsJSON), &items); err != nil {
		return err
	}
	for _, it := range items {
		_, err := h.optionRecorder.RecordOptionContract(ctx, &stockpb.RecordOptionContractRequest{
			CrossbankTxId:         crossbankTxID,
			PostingIndex:          int32(it.PostingIndex),
			OptionDescriptionJson: it.OptionDescriptionJSON,
			BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: it.Buyer.RoutingNumber, Id: it.Buyer.ID},
			SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: it.Seller.RoutingNumber, Id: it.Seller.ID},
			Direction:             it.Direction,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *PeerTxGRPCHandler) HandleRollbackTx(ctx context.Context, req *transactionpb.SiTxRollbackRequest) (*transactionpb.SiTxAckResponse, error) {
	idem := req.GetIdempotenceKey().GetLocallyGeneratedKey()
	peerCode := req.GetPeerBankCode()
	if idem == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing idempotence_key or peer_bank_code")
	}
	rec, found, err := h.idemRepo.Lookup(peerCode, idem)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if !found {
		// No record → nothing to roll back. Idempotent.
		return &transactionpb.SiTxAckResponse{}, nil
	}
	key := peerCode + ":" + idem
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
	// Credit-back DEBIT-side immediate-debits performed during NEW_TX.
	// Each entry is keyed by its own idempotency tag so retries are safe.
	var debits []sitx.DebitedItem
	if rec.DebitsJSON != "" && rec.DebitsJSON != "[]" {
		if jerr := json.Unmarshal([]byte(rec.DebitsJSON), &debits); jerr != nil {
			return nil, status.Errorf(codes.Internal, "decode debits: %v", jerr)
		}
	}
	for _, d := range debits {
		if _, cerr := h.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   d.AccountNumber,
			Amount:          d.Amount,
			UpdateAvailable: true,
			IdempotencyKey:  "sitx-creditback-" + d.IdempotencyTag,
		}); cerr != nil {
			return nil, status.Errorf(codes.Internal, "creditback %s: %v", d.IdempotencyTag, cerr)
		}
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
	postings := []contractsitx.Posting{
		{RoutingNumber: h.ownRouting, AccountID: req.GetFromAccountNumber(), AssetID: req.GetCurrency(), Amount: amt, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: target.RoutingNumber, AccountID: req.GetToAccountNumber(), AssetID: req.GetCurrency(), Amount: amt, Direction: contractsitx.DirectionCredit},
	}
	postingsJSON, _ := json.Marshal(postings)
	row := &model.OutboundPeerTx{
		IdempotenceKey: idem,
		PeerBankCode:   peerCode,
		TxKind:         "transfer",
		PostingsJSON:   string(postingsJSON),
		Status:         "pending",
	}
	if err := h.outRepo.Create(row); err != nil {
		return nil, status.Errorf(codes.Internal, "outbound row: %v", err)
	}

	// Sender-debit-immediate: take the money from the sender now; credit
	// it back on rollback. Idempotency key keeps the debit safe under
	// retry/replay (account-service rejects requests without one).
	if _, err := h.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   req.GetFromAccountNumber(),
		Amount:          "-" + req.GetAmount(),
		UpdateAvailable: true,
		IdempotencyKey:  "peer-out-debit-" + idem,
	}); err != nil {
		return nil, status.Errorf(codes.Internal, "debit: %v", err)
	}

	// Best-effort dispatch. On any error, the row stays pending and the
	// replay cron picks it up on next tick.
	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message:        contractsitx.Transaction{Postings: postings},
	}
	if vote, err := h.httpClient.PostNewTx(ctx, target, envelope); err != nil {
		_ = h.outRepo.MarkAttempt(idem, err.Error())
	} else if vote.Type == contractsitx.VoteYes {
		commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
			IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
			MessageType:    contractsitx.MessageTypeCommitTx,
			Message:        contractsitx.CommitTransaction{TransactionID: idem},
		}
		if err := h.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
			_ = h.outRepo.MarkAttempt(idem, "commit: "+err.Error())
		} else {
			_ = h.outRepo.MarkCommitted(idem)
		}
	} else {
		reason := "peer voted NO"
		if len(vote.NoVotes) > 0 {
			reason = "peer voted NO: " + vote.NoVotes[0].Reason
		}
		// Atomicity (Celina 5: "ili u celosti, ili ne uopšte"): credit the
		// sender back on a NO vote, since we already debited them above.
		if _, cbErr := h.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   req.GetFromAccountNumber(),
			Amount:          req.GetAmount(),
			UpdateAvailable: true,
			IdempotencyKey:  "peer-out-creditback-" + idem,
		}); cbErr != nil {
			reason = reason + " (creditback failed: " + cbErr.Error() + ")"
		}
		_ = h.outRepo.MarkRolledBack(idem, reason)
	}

	return &transactionpb.SiTxInitiateResponse{
		TransactionId: idem,
		PollUrl:       "/api/v3/me/transfers/" + idem,
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
	postings := make([]contractsitx.Posting, 0, len(req.GetPostings()))
	for _, p := range req.GetPostings() {
		amt, _ := decimal.NewFromString(p.GetAmount())
		postings = append(postings, contractsitx.Posting{
			RoutingNumber: p.GetRoutingNumber(),
			AccountID:     p.GetAccountId(),
			AssetID:       p.GetAssetId(),
			Amount:        amt,
			Direction:     p.GetDirection(),
		})
	}
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

	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message:        contractsitx.Transaction{Postings: postings},
	}
	if vote, err := h.httpClient.PostNewTx(ctx, target, envelope); err != nil {
		_ = h.outRepo.MarkAttempt(idem, err.Error())
	} else if vote.Type == contractsitx.VoteYes {
		// Peer voted YES → finalise our local CREDIT-leg reservations
		// before sending COMMIT_TX. NotFound is benign (no CREDIT legs
		// landed locally on this bank).
		if _, cerr := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
			ReservationKey: localKey,
			IdempotencyKey: "sitx-localcommit-" + localKey,
		}); cerr != nil && status.Code(cerr) != codes.NotFound {
			_ = h.outRepo.MarkAttempt(idem, "local commit: "+cerr.Error())
		}
		commitEnvelope := contractsitx.Message[contractsitx.CommitTransaction]{
			IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: h.ownRouting, LocallyGeneratedKey: idem},
			MessageType:    contractsitx.MessageTypeCommitTx,
			Message:        contractsitx.CommitTransaction{TransactionID: idem},
		}
		if err := h.httpClient.PostCommitTx(ctx, target, commitEnvelope); err != nil {
			_ = h.outRepo.MarkAttempt(idem, "commit: "+err.Error())
		} else {
			// Materialise sender-side option contract rows now that
			// both banks have committed. Receiver-side rows are
			// written by the peer's HandleCommitTx; sender-side rows
			// are written here by us, since our local Reserve was
			// run with peerCode=ownRouting and stays in our
			// idempotence-record cache for free, but the option list
			// is not pulled from idem store on this path — we use
			// the localResult directly. crossbankTxID is consistently
			// "<ownRouting>:<idem>" so the sender's row is keyed by
			// the same UUID that flows on the wire.
			if merr := h.materialiseOptions(ctx, optionItemsJSON(localResult.OptionItems), localKey); merr != nil {
				_ = h.outRepo.MarkAttempt(idem, "local option-record: "+merr.Error())
			}
			_ = h.outRepo.MarkCommitted(idem)
		}
	} else {
		reason := "peer voted NO"
		if len(vote.NoVotes) > 0 {
			reason = "peer voted NO: " + vote.NoVotes[0].Reason
		}
		// Peer voted NO → release our local reservation + credit-back
		// any DEBIT legs we already finalised locally.
		if _, rerr := h.client.ReleaseIncoming(ctx, &accountpb.ReleaseIncomingRequest{
			ReservationKey: localKey,
			IdempotencyKey: "sitx-localrelease-" + localKey,
		}); rerr != nil && status.Code(rerr) != codes.NotFound {
			reason = reason + " (local release failed: " + rerr.Error() + ")"
		}
		for _, d := range localResult.DebitedItems {
			if _, cerr := h.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   d.AccountNumber,
				Amount:          d.Amount,
				UpdateAvailable: true,
				IdempotencyKey:  "sitx-localcreditback-" + d.IdempotencyTag,
			}); cerr != nil {
				reason = reason + " (local creditback " + d.IdempotencyTag + " failed: " + cerr.Error() + ")"
			}
		}
		_ = h.outRepo.MarkRolledBack(idem, reason)
	}

	return &transactionpb.SiTxInitiateResponse{
		TransactionId: idem,
		PollUrl:       "/api/v3/me/transfers/" + idem,
		Status:        "pending",
	}, nil
}

func protoToPostings(in []*transactionpb.SiTxPosting) []contractsitx.Posting {
	out := make([]contractsitx.Posting, len(in))
	for i, p := range in {
		amt, _ := decimal.NewFromString(p.GetAmount())
		out[i] = contractsitx.Posting{
			RoutingNumber: p.GetRoutingNumber(),
			AccountID:     p.GetAccountId(),
			AssetID:       p.GetAssetId(),
			Amount:        amt,
			Direction:     p.GetDirection(),
		}
	}
	return out
}

func voteToProto(v contractsitx.TransactionVote) *transactionpb.SiTxVoteResponse {
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
