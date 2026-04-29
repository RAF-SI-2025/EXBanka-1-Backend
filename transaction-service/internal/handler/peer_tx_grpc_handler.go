package handler

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
)

// PeerTxGRPCHandler implements transactionpb.PeerTxServiceServer.
// Phase 3 Task 6: real NEW_TX / COMMIT_TX / ROLLBACK_TX implementations.
// Phase 3 Task 10 wires InitiateOutboundTx by injecting the outbound
// repository, HTTP client, peer-bank lookup, and our routing number.
type PeerTxGRPCHandler struct {
	transactionpb.UnimplementedPeerTxServiceServer
	idemRepo   *repository.PeerIdempotenceRepository
	executor   *sitx.PostingExecutor
	client     sitx.AccountClient
	outRepo    *repository.OutboundPeerTxRepository
	httpClient *sitx.PeerHTTPClient
	peerLookup PeerLookupFunc
	ownRouting int64
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
		return cacheAndReturn(h.idemRepo, peerCode, idem, "", voteToProto(vote))
	}

	// Execute reservations.
	res := h.executor.Reserve(ctx, postings, peerCode, idem)
	if res.Vote.Type == contractsitx.VoteNo {
		return cacheAndReturn(h.idemRepo, peerCode, idem, "", voteToProto(res.Vote))
	}

	txID := uuid.NewString()
	resp := &transactionpb.SiTxVoteResponse{Type: contractsitx.VoteYes, TransactionId: txID}
	return cacheAndReturn(h.idemRepo, peerCode, idem, txID, resp)
}

// cacheAndReturn inserts the idempotence record and returns the response.
// Per SI-TX, the idempotence record MUST be committed before the
// response is sent — this function call ordering achieves that as long
// as the caller propagates the returned response to the client only
// after this call returns.
func cacheAndReturn(repo *repository.PeerIdempotenceRepository, peerCode, idem, txID string, resp *transactionpb.SiTxVoteResponse) (*transactionpb.SiTxVoteResponse, error) {
	payload, _ := json.Marshal(resp)
	rec := &model.PeerIdempotenceRecord{
		PeerBankCode:        peerCode,
		LocallyGeneratedKey: idem,
		TransactionID:       txID,
		ResponsePayloadJSON: string(payload),
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
	// commits whatever was reserved under that key.
	key := peerCode + ":" + idem
	if _, cerr := h.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{ReservationKey: key}); cerr != nil {
		return nil, status.Errorf(codes.Internal, "commit: %v", cerr)
	}
	return &transactionpb.SiTxAckResponse{}, nil
}

func (h *PeerTxGRPCHandler) HandleRollbackTx(ctx context.Context, req *transactionpb.SiTxRollbackRequest) (*transactionpb.SiTxAckResponse, error) {
	idem := req.GetIdempotenceKey().GetLocallyGeneratedKey()
	peerCode := req.GetPeerBankCode()
	if idem == "" || peerCode == "" {
		return nil, status.Error(codes.InvalidArgument, "missing idempotence_key or peer_bank_code")
	}
	_, found, err := h.idemRepo.Lookup(peerCode, idem)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lookup: %v", err)
	}
	if !found {
		// No record → nothing to roll back. Idempotent.
		return &transactionpb.SiTxAckResponse{}, nil
	}
	key := peerCode + ":" + idem
	if _, rerr := h.client.ReleaseIncoming(ctx, &accountpb.ReleaseIncomingRequest{ReservationKey: key}); rerr != nil {
		return nil, status.Errorf(codes.Internal, "release: %v", rerr)
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
	// it back on rollback.
	if _, err := h.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   req.GetFromAccountNumber(),
		Amount:          "-" + req.GetAmount(),
		UpdateAvailable: true,
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
