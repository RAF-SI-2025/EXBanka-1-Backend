package sitx_test

import (
	"context"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
)

// stubConverter satisfies sitx.Converter. convertFn lets a test assert the
// (from,to,amount) and return a converted magnitude string.
type stubConverter struct {
	calls      int
	lastFrom   string
	lastTo     string
	lastAmount string
	out        string
	err        error
}

func (s *stubConverter) Convert(ctx context.Context, in *exchangepb.ConvertRequest, opts ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	s.calls++
	s.lastFrom = in.GetFromCurrency()
	s.lastTo = in.GetToCurrency()
	s.lastAmount = in.GetAmount()
	if s.err != nil {
		return nil, s.err
	}
	return &exchangepb.ConvertResponse{ConvertedAmount: s.out}, nil
}

// sellerEUROnly is an account client whose participant "client-1" holds ONE active
// EUR account and nothing else — the seller-lacks-the-premium-currency shape that
// reproduced the live NO_SUCH_ACCOUNT roll-back.
func sellerEUROnly() *stubAccountClientList {
	base := &stubAccountClient{
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			return &accountpb.ReserveIncomingResponse{}, nil
		},
	}
	return &stubAccountClientList{
		stubAccountClient: *base,
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "111000000000000011", CurrencyCode: "EUR", Status: "active"},
			}}, nil
		},
	}
}

// TestReserveIncomingCredit_FXFallback: a CHF premium credited to a seller who holds
// only an EUR account now FX-converts (CHF→EUR) and reserves the converted amount in
// the EUR account instead of voting NO_SUCH_ACCOUNT — the seller-side FX fix.
func TestReserveIncomingCredit_FXFallback(t *testing.T) {
	stub := sellerEUROnly()
	var reservedAmount, reservedCcy, reservedAcct string
	stub.stubAccountClient.reserveFn = func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
		reservedAmount, reservedCcy, reservedAcct = in.GetAmount(), in.GetCurrency(), in.GetAccountNumber()
		return &accountpb.ReserveIncomingResponse{}, nil
	}
	conv := &stubConverter{out: "37"}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetConverter(conv)

	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionCredit), // our routing, seller credit
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-fx")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES (FX bridges the credit), got %+v", res.Vote)
	}
	if conv.calls != 1 || conv.lastFrom != "CHF" || conv.lastTo != "EUR" || conv.lastAmount != "40" {
		t.Fatalf("expected one CHF→EUR convert of 40, got calls=%d %s→%s amt=%s", conv.calls, conv.lastFrom, conv.lastTo, conv.lastAmount)
	}
	if reservedAmount != "37" || reservedCcy != "EUR" || reservedAcct != "111000000000000011" {
		t.Fatalf("expected reserve 37 EUR into the EUR account, got %s %s acct=%s", reservedAmount, reservedCcy, reservedAcct)
	}
}

// TestReserveIncomingCredit_NoConverter_FailsClosed: without a converter wired, the
// pre-FX behaviour is preserved exactly — a participant with no account in the leg
// currency still votes NO with NO_SUCH_ACCOUNT (the fix is purely additive).
func TestReserveIncomingCredit_NoConverter_FailsClosed(t *testing.T) {
	stub := sellerEUROnly()
	exec := sitx.NewPostingExecutor(stub, 111) // no SetConverter
	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-nofx")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO without a converter, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) != 1 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Fatalf("expected NO_SUCH_ACCOUNT, got %+v", res.Vote.NoVotes)
	}
}

// TestReserveIncomingCredit_SameCurrency_NoFX: a credit in a currency the seller DOES
// hold reserves as-is and never calls the converter (FX is only the fallback path).
func TestReserveIncomingCredit_SameCurrency_NoFX(t *testing.T) {
	eur := &accountpb.AccountResponse{AccountNumber: "111000000000000011", CurrencyCode: "EUR", Status: "active"}
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				if in.GetAccountNumber() == eur.AccountNumber {
					return eur, nil
				}
				return nil, errors.New("not found")
			},
			reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
				if in.GetCurrency() != "EUR" || in.GetAmount() != "40" {
					return nil, errors.New("unexpected reserve")
				}
				return &accountpb.ReserveIncomingResponse{}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{eur}}, nil
		},
	}
	conv := &stubConverter{out: "999"}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetConverter(conv)
	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "EUR", 40, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-same")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if conv.calls != 0 {
		t.Fatalf("expected no FX call for same-currency credit, got %d", conv.calls)
	}
}
