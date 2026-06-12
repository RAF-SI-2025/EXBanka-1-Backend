package sitx_test

import (
	"context"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
)

// TestReserveOutgoingDebit_FXFallback: a CHF premium DEBITED from a buyer who
// holds only an EUR account now FX-converts (CHF→EUR) and places the outgoing
// hold of the converted amount on the EUR account instead of voting
// NO_SUCH_ACCOUNT — the buyer-side FX fix, symmetric with the seller-side credit.
func TestReserveOutgoingDebit_FXFallback(t *testing.T) {
	stub := sellerEUROnly()
	var outAmount, outCcy, outAcct string
	stub.stubAccountClient.reserveOutFn = func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
		outAmount, outCcy, outAcct = in.GetAmount(), in.GetCurrency(), in.GetAccountNumber()
		return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
	}
	conv := &stubConverter{out: "37"}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetConverter(conv)

	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionDebit), // our routing, buyer debit
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-fxdebit")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES (FX bridges the debit), got %+v", res.Vote)
	}
	if conv.calls != 1 || conv.lastFrom != "CHF" || conv.lastTo != "EUR" || conv.lastAmount != "40" {
		t.Fatalf("expected one CHF→EUR convert of 40, got calls=%d %s→%s amt=%s", conv.calls, conv.lastFrom, conv.lastTo, conv.lastAmount)
	}
	if outAmount != "37" || outCcy != "EUR" || outAcct != "111000000000000011" {
		t.Fatalf("expected reserve-outgoing 37 EUR from the EUR account, got %s %s acct=%s", outAmount, outCcy, outAcct)
	}
}

// TestReserveOutgoingDebit_NoConverter_FailsClosed: without a converter wired the
// pre-FX behaviour is preserved exactly — a buyer with no account in the leg
// currency still votes NO with NO_SUCH_ACCOUNT (the fix is purely additive).
func TestReserveOutgoingDebit_NoConverter_FailsClosed(t *testing.T) {
	stub := sellerEUROnly()
	exec := sitx.NewPostingExecutor(stub, 111) // no SetConverter
	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-nofxdebit")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO without a converter, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) != 1 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Fatalf("expected NO_SUCH_ACCOUNT, got %+v", res.Vote.NoVotes)
	}
}

// TestFXReserve_RejectsNonPositiveConversion: a buggy/hostile exchange returning
// a non-positive converted amount ("0") must vote NO rather than reserve a
// garbage amount and silently under-settle the leg — both credit and debit sides.
func TestFXReserve_RejectsNonPositiveConversion(t *testing.T) {
	conv := &stubConverter{out: "0"}

	exec := sitx.NewPostingExecutor(sellerEUROnly(), 111)
	exec.SetConverter(conv)
	resC := exec.Reserve(context.Background(), []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionCredit),
	}, "222", "idem-zero-credit")
	if resC.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("zero conversion (credit) must vote NO, got %+v", resC.Vote)
	}

	exec2 := sitx.NewPostingExecutor(sellerEUROnly(), 111)
	exec2.SetConverter(conv)
	resD := exec2.Reserve(context.Background(), []contractsitx.InternalPosting{
		money(111, "client-1", "CHF", 40, contractsitx.DirectionDebit),
	}, "222", "idem-zero-debit")
	if resD.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("zero conversion (debit) must vote NO, got %+v", resD.Vote)
	}
}

// TestReserve_RejectsMultipleMonasCreditsSameBank: two money CREDITs to the same
// bank in one TX would collide under the shared reservation key (the 2nd silently
// skipped → money lost at commit). The audit #4 guard rejects the shape up front.
func TestReserve_RejectsMultipleMonasCreditsSameBank(t *testing.T) {
	exec := sitx.NewPostingExecutor(sellerEUROnly(), 111)
	postings := []contractsitx.InternalPosting{
		money(111, "client-1", "EUR", 40, contractsitx.DirectionCredit),
		money(111, "client-2", "EUR", 50, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-2credits")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("two money credits to the same bank must vote NO (shared-key collision), got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) != 1 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Fatalf("expected UNACCEPTABLE_ASSET, got %+v", res.Vote.NoVotes)
	}
}
