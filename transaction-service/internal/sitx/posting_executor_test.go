package sitx_test

import (
	"context"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
)

type stubAccountClient struct {
	getAccountFn func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	reserveFn    func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error)
	commitFn     func(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error)
	releaseFn    func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error)
	updateFn     func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	reserveOutFn func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error)
	settleOutFn  func(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error)
	releaseOutFn func(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error)
}

func (s *stubAccountClient) GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getAccountFn != nil {
		return s.getAccountFn(ctx, in, opts...)
	}
	return nil, errors.New("not stubbed")
}
func (s *stubAccountClient) ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	if s.reserveFn != nil {
		return s.reserveFn(ctx, in, opts...)
	}
	return nil, errors.New("not stubbed")
}
func (s *stubAccountClient) CommitIncoming(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	if s.commitFn != nil {
		return s.commitFn(ctx, in, opts...)
	}
	return nil, errors.New("not stubbed")
}
func (s *stubAccountClient) ReleaseIncoming(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	if s.releaseFn != nil {
		return s.releaseFn(ctx, in, opts...)
	}
	return nil, errors.New("not stubbed")
}
func (s *stubAccountClient) UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, opts...)
	}
	return nil, errors.New("not stubbed")
}
func (s *stubAccountClient) ListAccountsByClient(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return &accountpb.ListAccountsResponse{}, nil
}
func (s *stubAccountClient) ReserveOutgoing(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
	if s.reserveOutFn != nil {
		return s.reserveOutFn(ctx, in, opts...)
	}
	return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
}
func (s *stubAccountClient) SettleOutgoing(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
	if s.settleOutFn != nil {
		return s.settleOutFn(ctx, in, opts...)
	}
	return &accountpb.SettleOutgoingResponse{}, nil
}
func (s *stubAccountClient) ReleaseOutgoing(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
	if s.releaseOutFn != nil {
		return s.releaseOutFn(ctx, in, opts...)
	}
	return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
}

func TestPostingExecutor_HappyPath_ReservesCredit(t *testing.T) {
	called := false
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			called = true
			if in.ReservationKey != "222:idem-1" {
				t.Errorf("reservation_key: %q", in.ReservationKey)
			}
			return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey, BalanceAfter: "100.00"}, nil
		},
	}

	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 222, AccountID: "222000001", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 111, AccountID: "111000001", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-1")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", res.Vote)
	}
	if !called {
		t.Errorf("ReserveIncoming was not called")
	}
}

func TestPostingExecutor_NoSuchAccount(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return nil, errors.New("not found")
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111-bogus", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
		{RoutingNumber: 222, AccountID: "222000001", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-2")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) == 0 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Errorf("expected NO_SUCH_ACCOUNT, got %+v", res.Vote.NoVotes)
	}
}

func TestPostingExecutor_DebitOnOurRouting_ReservesOutgoing(t *testing.T) {
	// Per SI-TX reserve-then-settle, a DEBIT posting on our routing means our
	// account is losing the asset — but at NEW_TX we only place a HOLD via
	// ReserveOutgoing (AvailableBalance dips, Balance untouched). The money
	// actually leaves at COMMIT_TX (SettleOutgoing) and the hold is released
	// at ROLLBACK_TX. The reservation key is the per-posting tag.
	reservedAmount := ""
	reservedKey := ""
	reservedCurrency := ""
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveOutFn: func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
			reservedAmount = in.Amount
			reservedKey = in.ReservationKey
			reservedCurrency = in.Currency
			return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
		},
		updateFn: func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			t.Errorf("UpdateBalance must NOT be called for a DEBIT leg under reserve-then-settle; got amount=%q", in.Amount)
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111-A", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "222-B", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-3")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES on DEBIT posting, got %+v", res.Vote)
	}
	if reservedAmount != "100" {
		t.Errorf("expected ReserveOutgoing(100), got %q", reservedAmount)
	}
	if reservedCurrency != "RSD" {
		t.Errorf("expected reserve currency RSD, got %q", reservedCurrency)
	}
	// Tag format "<peer>:<idem>:<postingIndex>" — DEBIT leg is index 0.
	if reservedKey != "222:idem-3:0" {
		t.Errorf("expected reservation key 222:idem-3:0, got %q", reservedKey)
	}
	if len(res.DebitedItems) != 1 {
		t.Fatalf("expected 1 debited item, got %d", len(res.DebitedItems))
	}
	if res.DebitedItems[0].Amount != "100" {
		t.Errorf("debited item amount: %q", res.DebitedItems[0].Amount)
	}
	if res.DebitedItems[0].IdempotencyTag != "222:idem-3:0" {
		t.Errorf("debited item tag: %q", res.DebitedItems[0].IdempotencyTag)
	}
}

func TestPostingExecutor_NoSuchAsset_CurrencyMismatch(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 222, AccountID: "222-A", AssetID: "EUR", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 111, AccountID: "111-B", AssetID: "EUR", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-4")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) == 0 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAsset {
		t.Errorf("expected NO_SUCH_ASSET, got %+v", res.Vote.NoVotes)
	}
}
