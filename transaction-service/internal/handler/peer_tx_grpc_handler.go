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
// InitiateOutboundTx remains Unimplemented until Tasks 7-10 wire the
// sender-side outbound HTTP client + replay cron.
type PeerTxGRPCHandler struct {
	transactionpb.UnimplementedPeerTxServiceServer
	idemRepo *repository.PeerIdempotenceRepository
	executor *sitx.PostingExecutor
	client   sitx.AccountClient
}

func NewPeerTxGRPCHandler(
	idemRepo *repository.PeerIdempotenceRepository,
	executor *sitx.PostingExecutor,
	accountClient sitx.AccountClient,
) *PeerTxGRPCHandler {
	return &PeerTxGRPCHandler{idemRepo: idemRepo, executor: executor, client: accountClient}
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

// InitiateOutboundTx is implemented in Tasks 7-10 (sender-side scaffolding).
// Phase 3 Task 6 leaves the inherited UnimplementedPeerTxServiceServer
// stub in place so callers receive codes.Unimplemented until then.

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
