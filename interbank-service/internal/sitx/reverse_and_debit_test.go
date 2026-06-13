package sitx_test

import (
	"context"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestReverseLocal_ReleasesOwnDebitMoneyHold verifies ReverseLocal releases the
// outgoing HOLD for each money DEBIT leg on our routing (via ReleaseOutgoing
// keyed by the per-posting tag), and also releases the incoming reservation.
func TestReverseLocal_ReleasesOwnDebitMoneyHold(t *testing.T) {
	releasedIncoming := false
	var releasedOutKeys []string
	stub := &stubAccountClient{
		releaseFn: func(_ context.Context, in *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
			releasedIncoming = true
			if in.ReservationKey != "222:idem-RV2" {
				t.Errorf("incoming release key = %q, want 222:idem-RV2", in.ReservationKey)
			}
			return &accountpb.ReleaseIncomingResponse{}, nil
		},
		releaseOutFn: func(_ context.Context, in *accountpb.ReleaseOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
			releasedOutKeys = append(releasedOutKeys, in.ReservationKey)
			return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionDebit), // idx 0 → release outgoing hold
		money(222, "222-B", "RSD", 100, contractsitx.DirectionCredit),
	}
	if err := exec.ReverseLocal(context.Background(), postings, "222", "idem-RV2"); err != nil {
		t.Fatalf("ReverseLocal: %v", err)
	}
	if !releasedIncoming {
		t.Errorf("expected ReleaseIncoming to be called")
	}
	if len(releasedOutKeys) != 1 || releasedOutKeys[0] != "222:idem-RV2:0" {
		t.Errorf("expected outgoing hold release on 222:idem-RV2:0, got %v", releasedOutKeys)
	}
}

// TestReverseLocal_ReleaseIncomingErrorPropagates verifies a non-NotFound error
// from ReleaseIncoming aborts the reversal (so the caller can retry), while a
// NotFound is swallowed as benign.
func TestReverseLocal_ReleaseIncomingErrorPropagates(t *testing.T) {
	boom := &stubAccountClient{
		releaseFn: func(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
			return nil, status.Error(codes.Unavailable, "account-service down")
		},
	}
	exec := sitx.NewPostingExecutor(boom, 111)
	postings := []contractsitx.InternalPosting{money(111, "111-A", "RSD", 100, contractsitx.DirectionCredit)}
	if err := exec.ReverseLocal(context.Background(), postings, "222", "idem-RVE"); err == nil {
		t.Errorf("expected non-NotFound ReleaseIncoming error to propagate")
	}

	benign := &stubAccountClient{
		releaseFn: func(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
			return nil, status.Error(codes.NotFound, "no reservation")
		},
	}
	exec2 := sitx.NewPostingExecutor(benign, 111)
	if err := exec2.ReverseLocal(context.Background(), postings, "222", "idem-RVN"); err != nil {
		t.Errorf("NotFound on ReleaseIncoming should be benign, got %v", err)
	}
}

// TestReserveOutgoingDebit_InsufficientAsset verifies a ReserveOutgoing failure
// on a same-currency DEBIT leg (no FX) maps to an INSUFFICIENT_ASSET NO vote.
func TestReserveOutgoingDebit_InsufficientAsset(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(_ context.Context, in *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveOutFn: func(_ context.Context, _ *accountpb.ReserveOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
			return nil, errors.New("hold exceeds available balance")
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 1000, contractsitx.DirectionDebit),
		money(222, "222-B", "RSD", 1000, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-INS")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("expected INSUFFICIENT_ASSET, got %+v", res.Vote.NoVotes)
	}
}

// TestReserveOutgoingDebit_InactiveAccount verifies a DEBIT against an inactive
// account on our routing votes NO with UNACCEPTABLE_ASSET.
func TestReserveOutgoingDebit_InactiveAccount(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(_ context.Context, in *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "inactive"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionDebit),
		money(222, "222-B", "RSD", 100, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-INACT")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("expected UNACCEPTABLE_ASSET, got %+v", res.Vote.NoVotes)
	}
}
