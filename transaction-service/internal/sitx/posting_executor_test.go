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

func TestPostingExecutor_UnacceptableAsset_DebitOnOurRouting(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111-A", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "222-B", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-3")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) == 0 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("expected UNACCEPTABLE_ASSET, got %+v", res.Vote.NoVotes)
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
