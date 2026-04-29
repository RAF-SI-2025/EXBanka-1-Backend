package sitx

import (
	"context"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"google.golang.org/grpc"
)

// AccountClient is the subset of accountpb.AccountServiceClient that
// posting_executor depends on, plus UpdateBalance for sender-side debits
// in InitiateOutboundTx (Phase 3 Task 6/9). Decoupled for testability —
// the real accountpb.AccountServiceClient satisfies this interface, and
// test stubs can implement it directly without grpc.ClientConn.
type AccountClient interface {
	GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error)
	CommitIncoming(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error)
	ReleaseIncoming(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error)
	UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
}

// ReserveResult is the outcome of executing the reserve phase of a NEW_TX.
type ReserveResult struct {
	Vote            contractsitx.TransactionVote
	ReservationKeys []string // populated on YES; one per credit posting on our routing
}

// PostingExecutor walks an accepted NEW_TX's postings and applies the
// receiver-side reservations via account-service. ownRouting is this
// bank's routing number — postings with a different routing are not
// executed locally (they're the responsibility of the other bank).
type PostingExecutor struct {
	client     AccountClient
	ownRouting int64
}

func NewPostingExecutor(client AccountClient, ownRouting int64) *PostingExecutor {
	return &PostingExecutor{client: client, ownRouting: ownRouting}
}

// Reserve runs the receive-side reserve phase of a NEW_TX. It walks the
// postings, reserving each credit-posting on our routingNumber via
// account-service.ReserveIncoming. On any per-posting failure it returns
// a NO vote with the matching SI-TX reason and the failing posting index.
func (e *PostingExecutor) Reserve(ctx context.Context, postings []contractsitx.Posting, peerBankCode, locallyGeneratedKey string) ReserveResult {
	keys := []string{}
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber != e.ownRouting {
			continue
		}
		if p.Direction != contractsitx.DirectionCredit {
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
		acct, err := e.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: p.AccountID})
		if err != nil || acct == nil {
			return noVote(contractsitx.NoVoteReasonNoSuchAccount, i)
		}
		if acct.Status != "active" {
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
		if acct.CurrencyCode != p.AssetID {
			return noVote(contractsitx.NoVoteReasonNoSuchAsset, i)
		}
		key := peerBankCode + ":" + locallyGeneratedKey
		if _, err := e.client.ReserveIncoming(ctx, &accountpb.ReserveIncomingRequest{
			AccountNumber:  p.AccountID,
			Amount:         p.Amount.String(),
			Currency:       p.AssetID,
			ReservationKey: key,
			IdempotencyKey: "sitx-reserve-" + key,
		}); err != nil {
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
		keys = append(keys, key)
	}
	return ReserveResult{
		Vote:            contractsitx.TransactionVote{Type: contractsitx.VoteYes},
		ReservationKeys: keys,
	}
}

func noVote(reason string, postingIdx int) ReserveResult {
	return ReserveResult{
		Vote: contractsitx.TransactionVote{
			Type:    contractsitx.VoteNo,
			NoVotes: []contractsitx.NoVote{{Reason: reason, Posting: ptr(postingIdx)}},
		},
	}
}

func ptr(i int) *int { return &i }
