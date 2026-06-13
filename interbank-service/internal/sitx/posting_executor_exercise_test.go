package sitx_test

import (
	"context"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
)

// --- builders for the exercise pseudo-account wire shape (after SpecPostingToInternal) ---

// optAcctMonas builds an OPTION pseudo-account MONAS leg (the strike credit/debit
// at the pseudo-account). accountID is the negotiationId; rn is the negotiation
// routing.
func optAcctMonas(rn int64, negID, currency string, amount int64, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   contractsitx.AccountTypeOption,
		AccountID:     negID,
		AssetType:     contractsitx.AssetTypeMonas,
		AssetID:       currency,
		Amount:        decimalStr(amount),
		Direction:     dir,
	}
}

// optAcctStock builds an OPTION pseudo-account STOCK leg (the underlying moving
// in/out of the pseudo-account).
func optAcctStock(rn int64, negID, ticker string, amount int64, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   contractsitx.AccountTypeOption,
		AccountID:     negID,
		AssetType:     contractsitx.AssetTypeStock,
		AssetID:       ticker,
		Amount:        decimalStr(amount),
		Direction:     dir,
	}
}

// personStock builds a PERSON STOCK leg (the buyer's underlying arrival).
func personStock(rn int64, participant, ticker string, amount int64, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   contractsitx.AccountTypePerson,
		AccountID:     participant,
		AssetType:     contractsitx.AssetTypeStock,
		AssetID:       ticker,
		Amount:        decimalStr(amount),
		Direction:     dir,
	}
}

// acctMonas builds an ACCOUNT MONAS leg with a concrete 18-digit-style number.
func acctMonas(rn int64, num, currency string, amount int64, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   contractsitx.AccountTypeAccount,
		AccountID:     num,
		AssetType:     contractsitx.AssetTypeMonas,
		AssetID:       currency,
		Amount:        decimalStr(amount),
		Direction:     dir,
	}
}

// sellerLookupResp is a found seller-side contract lookup response.
func sellerLookupResp(sellerID, ticker, strike string, qty int64, currency, settlement, status string) *stockpb.LookupPeerOptionContractResponse {
	return &stockpb.LookupPeerOptionContractResponse{
		Found:          true,
		SellerId:       sellerID,
		Ticker:         ticker,
		StrikePrice:    strike,
		Quantity:       qty,
		Currency:       currency,
		SettlementDate: settlement,
		Status:         status,
	}
}

// exerciseSellerPostings is the seller-bank view of the 4-leg exercise: it owns
// the OPTION pseudo-account legs (2,3) by contract; the buyer's MONAS DEBIT (leg 1)
// and PERSON STOCK CREDIT (leg 4) are on routing 111 (the buyer's bank). Strike =
// 50 x 10 = 500. negotiationId = {111,"neg-1"}. ownRouting (seller) = 222.
func exerciseSellerPostings() []contractsitx.InternalPosting {
	return []contractsitx.InternalPosting{
		acctMonas(111, "111000117810858011", "RSD", 500, contractsitx.DirectionDebit), // 1 buyer pays strike
		optAcctMonas(111, "neg-1", "RSD", 500, contractsitx.DirectionCredit),          // 2 pseudo gets strike
		optAcctStock(111, "neg-1", "WMT", 10, contractsitx.DirectionDebit),            // 3 pseudo releases shares
		personStock(111, "client-1", "WMT", 10, contractsitx.DirectionCredit),         // 4 buyer gets shares
	}
}

// TestReserve_OptionPseudoAccount_OwnedContract_VotesYes: the seller bank (222)
// owns the seller-side contract for neg-1. It must credit the seller's money
// account (ReserveIncoming) for the strike, emit an "exercise_seller" OptionItem
// for the STOCK pseudo leg, and NOT place a new share reservation.
func TestReserve_OptionPseudoAccount_OwnedContract_VotesYes(t *testing.T) {
	var reserveIncomingAcct string
	var reserveIncomingAmt string
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			reserveIncomingAcct = in.AccountNumber
			reserveIncomingAmt = in.Amount
			return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey, BalanceAfter: "1000"}, nil
		},
	}
	// Seller's money account resolves via ListAccountsByClient("client-3").
	listStub := &stubAccountClientList{
		stubAccountClient: *stub,
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "222000999", CurrencyCode: "RSD", Status: "active"},
			}}, nil
		},
	}
	hc := &stubHoldingChecker{
		lookupResp: sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2999-12-31T00:00:00+02:00", "active"),
	}
	exec := sitx.NewPostingExecutor(listStub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-1")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	// Strike credited to the resolved seller money account.
	if reserveIncomingAcct != "222000999" {
		t.Errorf("strike credited to %q, want 222000999", reserveIncomingAcct)
	}
	if reserveIncomingAmt != "500" {
		t.Errorf("strike amount %q, want 500", reserveIncomingAmt)
	}
	// Exactly one ReservationKey (the seller money credit) tracked for commit/rollback.
	if len(res.ReservationKeys) != 1 {
		t.Errorf("expected 1 reservation key (seller money credit), got %d: %v", len(res.ReservationKeys), res.ReservationKeys)
	}
	// One exercise_seller OptionItem, carrying the negotiationId + DEBIT direction.
	if len(res.OptionItems) != 1 {
		t.Fatalf("expected 1 OptionItem, got %d", len(res.OptionItems))
	}
	it := res.OptionItems[0]
	if it.Kind != sitx.OptionKindExerciseSeller {
		t.Errorf("kind = %q, want %q", it.Kind, sitx.OptionKindExerciseSeller)
	}
	if it.Direction != contractsitx.DirectionDebit {
		t.Errorf("direction = %q, want DEBIT", it.Direction)
	}
	// No NEW share reservation must be placed at exercise (shares were reserved at accept).
	if hc.reserveCalls != 0 {
		t.Errorf("expected 0 ReserveSellerSharesForNewTx calls at exercise, got %d", hc.reserveCalls)
	}
}

// TestReserve_OptionPseudoAccount_StrikeCreditsNominatedAccount: when the stored
// seller-side contract carries a NOMINATED seller account number, the seller
// bank credits the strike to THAT exact account (ACCOUNT pass-through) — NOT the
// seller's first active <currency> account resolved from the participant id. This
// is sub-case 2: the strike credit honours the seller's bound account.
func TestReserve_OptionPseudoAccount_StrikeCreditsNominatedAccount(t *testing.T) {
	const nominated = "222000000000000111"
	var reserveIncomingAcct string
	listFnCalls := 0
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			reserveIncomingAcct = in.AccountNumber
			return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey, BalanceAfter: "1000"}, nil
		},
	}
	// If first-active resolution is (wrongly) used, this list would be consulted.
	listStub := &stubAccountClientList{
		stubAccountClient: *stub,
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			listFnCalls++
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "222000999", CurrencyCode: "RSD", Status: "active"}, // the WRONG (first-active) account
			}}, nil
		},
	}
	look := sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2999-12-31T00:00:00+02:00", "active")
	look.SellerAccountNumber = nominated
	hc := &stubHoldingChecker{lookupResp: look}
	exec := sitx.NewPostingExecutor(listStub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-nom")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if reserveIncomingAcct != nominated {
		t.Errorf("strike credited to %q, want nominated %q (not first-active)", reserveIncomingAcct, nominated)
	}
	if listFnCalls != 0 {
		t.Errorf("expected NO first-active list lookup when a nominated account is present, got %d calls", listFnCalls)
	}
}

// TestReserve_OptionPseudoAccount_StrikeFallsBackWhenNoNomination: with no stored
// nominated account, the strike credit falls back to first-active participant
// resolution (the documented fallback) — unchanged from prior behaviour.
func TestReserve_OptionPseudoAccount_StrikeFallsBackWhenNoNomination(t *testing.T) {
	var reserveIncomingAcct string
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			reserveIncomingAcct = in.AccountNumber
			return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey, BalanceAfter: "1000"}, nil
		},
	}
	listStub := &stubAccountClientList{
		stubAccountClient: *stub,
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "222000999", CurrencyCode: "RSD", Status: "active"},
			}}, nil
		},
	}
	// look WITHOUT SellerAccountNumber (empty) → fallback.
	hc := &stubHoldingChecker{lookupResp: sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2999-12-31T00:00:00+02:00", "active")}
	exec := sitx.NewPostingExecutor(listStub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-fb")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if reserveIncomingAcct != "222000999" {
		t.Errorf("strike fallback credited to %q, want first-active 222000999", reserveIncomingAcct)
	}
}

// TestReserve_BuyerStockArrival_VotesYes: the buyer bank (111) is the sender. It
// processes its MONAS DEBIT (leg 1, generic ReserveOutgoing) and its PERSON STOCK
// CREDIT (leg 4) → emits an "exercise_buyer" OptionItem carrying the negotiationId
// (read from the OPTION pseudo-account legs in the same TX). It must SKIP the
// OPTION pseudo-account legs (it holds the buyer side; lookup found=false).
func TestReserve_BuyerStockArrival_VotesYes(t *testing.T) {
	var reserveOutAcct string
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveOutFn: func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
			reserveOutAcct = in.AccountNumber
			return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
		},
	}
	// Buyer bank: lookup found=false (it holds the buyer/CREDIT side only).
	hc := &stubHoldingChecker{lookupResp: &stockpb.LookupPeerOptionContractResponse{Found: false}}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetHoldingChecker(hc)

	postings := exerciseSellerPostings() // same wire; here ownRouting=111 (buyer)
	res := exec.Reserve(context.Background(), postings, "111", "k-ex-1")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if reserveOutAcct != "111000117810858011" {
		t.Errorf("strike debited from %q, want 111000117810858011", reserveOutAcct)
	}
	// One exercise_buyer OptionItem with CREDIT direction, carrying negotiationId neg-1.
	if len(res.OptionItems) != 1 {
		t.Fatalf("expected 1 OptionItem, got %d", len(res.OptionItems))
	}
	it := res.OptionItems[0]
	if it.Kind != sitx.OptionKindExerciseBuyer {
		t.Errorf("kind = %q, want %q", it.Kind, sitx.OptionKindExerciseBuyer)
	}
	if it.Direction != contractsitx.DirectionCredit {
		t.Errorf("direction = %q, want CREDIT", it.Direction)
	}
	// The reconstructed OptionDescription JSON must carry the negotiationId so
	// recordOptionExercise's CREDIT branch can look up the buyer-side contract.
	if it.OptionDescriptionJSON == "" {
		t.Errorf("expected reconstructed OptionDescriptionJSON carrying negotiationId")
	}
}

// TestReserve_OptionPseudoAccount_NotFound_VotesNo: this bank is the expected
// seller bank (it processes the pseudo-account leg on its routing) but holds NO
// contract row → OPTION_NEGOTIATION_NOT_FOUND.
func TestReserve_OptionPseudoAccount_NotFound_VotesNo(t *testing.T) {
	stub := &stubAccountClient{}
	// negotiation routing == ownRouting (222) so this bank IS the expected owner,
	// yet lookup reports found=false.
	postings := []contractsitx.InternalPosting{
		acctMonas(111, "111000117810858011", "RSD", 500, contractsitx.DirectionDebit),
		optAcctMonas(222, "neg-x", "RSD", 500, contractsitx.DirectionCredit),
		optAcctStock(222, "neg-x", "WMT", 10, contractsitx.DirectionDebit),
		personStock(111, "client-1", "WMT", 10, contractsitx.DirectionCredit),
	}
	hc := &stubHoldingChecker{lookupResp: &stockpb.LookupPeerOptionContractResponse{Found: false}}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), postings, "111", "k-ex-2")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonOptionNegotiationNotFound {
		t.Errorf("reason = %q, want %q", res.Vote.NoVotes[0].Reason, contractsitx.NoVoteReasonOptionNegotiationNotFound)
	}
}

// TestReserve_OptionPseudoAccount_Expired_VotesNo: a found contract past its
// settlementDate → OPTION_USED_OR_EXPIRED.
func TestReserve_OptionPseudoAccount_Expired_VotesNo(t *testing.T) {
	stub := &stubAccountClient{}
	hc := &stubHoldingChecker{
		lookupResp: sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2000-01-01T00:00:00+02:00", "active"),
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-3")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonOptionUsedOrExpired {
		t.Errorf("reason = %q, want %q", res.Vote.NoVotes[0].Reason, contractsitx.NoVoteReasonOptionUsedOrExpired)
	}
}

// TestReserve_OptionPseudoAccount_UsedStatus_VotesNo: a found contract already
// "exercised" → OPTION_USED_OR_EXPIRED.
func TestReserve_OptionPseudoAccount_UsedStatus_VotesNo(t *testing.T) {
	stub := &stubAccountClient{}
	hc := &stubHoldingChecker{
		lookupResp: sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2999-12-31T00:00:00+02:00", "exercised"),
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(hc)

	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-4")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonOptionUsedOrExpired {
		t.Errorf("reason = %q, want %q", res.Vote.NoVotes[0].Reason, contractsitx.NoVoteReasonOptionUsedOrExpired)
	}
}

// TestReserve_OptionPseudoAccount_WrongAmount_VotesNo: the pseudo MONAS leg
// amount != StrikePrice*Quantity → OPTION_AMOUNT_INCORRECT.
func TestReserve_OptionPseudoAccount_WrongAmount_VotesNo(t *testing.T) {
	stub := &stubAccountClient{}
	hc := &stubHoldingChecker{
		// Stored strike 50 x 10 = 500, but the wire leg carries 999.
		lookupResp: sellerLookupResp("client-3", "WMT", "50", 10, "RSD", "2999-12-31T00:00:00+02:00", "active"),
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(hc)

	postings := []contractsitx.InternalPosting{
		acctMonas(111, "111000117810858011", "RSD", 999, contractsitx.DirectionDebit),
		optAcctMonas(111, "neg-1", "RSD", 999, contractsitx.DirectionCredit),
		optAcctStock(111, "neg-1", "WMT", 10, contractsitx.DirectionDebit),
		personStock(111, "client-1", "WMT", 10, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "k-ex-5")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonOptionAmountIncorrect {
		t.Errorf("reason = %q, want %q", res.Vote.NoVotes[0].Reason, contractsitx.NoVoteReasonOptionAmountIncorrect)
	}
}

// TestReserve_OptionPseudoAccount_RoutingMatchesButNotOurContract_Skips: the
// ownership rule is "do I hold the seller side?", not routing-prefix. A pseudo-
// account leg whose negotiation routing == ownRouting but for which we hold NO
// seller contract... is the NotFound case (covered above). The complementary case
// here: ownRouting == 111 (buyer bank) sees pseudo legs on routing 111 (matching
// its own routing) but must SKIP them because it holds the buyer side
// (lookup found=false) — proven by NOT voting NO and NOT touching money for them.
func TestReserve_OptionPseudoAccount_RoutingMatchesButBuyerSide_Skips(t *testing.T) {
	reserveIncomingCalled := false
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			reserveIncomingCalled = true
			return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey}, nil
		},
		reserveOutFn: func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
			return &accountpb.ReserveOutgoingResponse{ReservationKey: in.ReservationKey}, nil
		},
	}
	hc := &stubHoldingChecker{lookupResp: &stockpb.LookupPeerOptionContractResponse{Found: false}}
	exec := sitx.NewPostingExecutor(stub, 111) // buyer bank
	exec.SetHoldingChecker(hc)

	// Pseudo-account legs carry routing 111 (== ownRouting) here, yet the buyer
	// bank must NOT settle them (no seller contract). It still handles leg 1 (its
	// MONAS DEBIT) and leg 4 (its PERSON STOCK CREDIT).
	res := exec.Reserve(context.Background(), exerciseSellerPostings(), "111", "k-ex-6")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	// The pseudo-account MONAS CREDIT must NOT have been credited (it's not ours).
	if reserveIncomingCalled {
		t.Errorf("buyer bank must NOT ReserveIncoming the seller's strike pseudo-leg")
	}
	// Only the buyer-stock exercise item, no seller item.
	if len(res.OptionItems) != 1 || res.OptionItems[0].Kind != sitx.OptionKindExerciseBuyer {
		t.Errorf("expected exactly 1 exercise_buyer item, got %+v", res.OptionItems)
	}
}
