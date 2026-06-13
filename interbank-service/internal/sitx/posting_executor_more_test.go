package sitx_test

import (
	"context"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
)

// stubAccountClientList covers ListAccountsByClient for the participant-id
// resolver path inside resolveAccountForPosting.
type stubAccountClientList struct {
	stubAccountClient
	listFn func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error)
}

func (s *stubAccountClientList) ListAccountsByClient(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in, opts...)
	}
	return &accountpb.ListAccountsResponse{}, nil
}

// stubHoldingChecker satisfies SellerHoldingChecker.
type stubHoldingChecker struct {
	resp *stockpb.CheckSellerCanDeliverResponse
	err  error
	// reserve/release tracking (Celina-5 vote-time share hold)
	reserveCalls      int
	releaseCalls      int
	lastReserveTxID   string
	lastReleaseTxID   string
	lastReserveTicker string
	lastReserveQty    int64
	// money-leg validation (forged-strike defense). validateOK defaults to true
	// (nil) so existing tests keep voting YES; set validateDeny to force a NO.
	validateCalls int
	lastValidate  *stockpb.ValidatePeerOptionMoneyLegRequest
	validateDeny  bool
	validateErr   error
	// OPTION pseudo-account exercise-leg ownership lookup (design §3.3.1). When
	// lookupResp is nil the checker reports found=false (this bank doesn't hold
	// the seller side). lookupErr forces a transport error.
	lookupCalls int
	lastLookup  *stockpb.LookupPeerOptionContractRequest
	lookupResp  *stockpb.LookupPeerOptionContractResponse
	lookupErr   error
}

func (s *stubHoldingChecker) LookupPeerOptionContract(ctx context.Context, in *stockpb.LookupPeerOptionContractRequest, opts ...grpc.CallOption) (*stockpb.LookupPeerOptionContractResponse, error) {
	s.lookupCalls++
	s.lastLookup = in
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	if s.lookupResp != nil {
		return s.lookupResp, nil
	}
	return &stockpb.LookupPeerOptionContractResponse{Found: false}, nil
}

func (s *stubHoldingChecker) CheckSellerCanDeliver(ctx context.Context, in *stockpb.CheckSellerCanDeliverRequest, opts ...grpc.CallOption) (*stockpb.CheckSellerCanDeliverResponse, error) {
	return s.resp, s.err
}

// ReserveSellerSharesForNewTx mirrors the configured CheckSellerCanDeliver
// verdict (ok/err) so existing tests that set `resp`/`err` keep their meaning
// now that the executor reserves instead of checks.
func (s *stubHoldingChecker) ReserveSellerSharesForNewTx(ctx context.Context, in *stockpb.ReserveSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReserveSellerSharesResponse, error) {
	s.reserveCalls++
	s.lastReserveTxID = in.GetCrossbankTxId()
	s.lastReserveTicker = in.GetTicker()
	s.lastReserveQty = in.GetQuantity()
	if s.err != nil {
		return nil, s.err
	}
	ok := s.resp == nil || s.resp.GetOk()
	return &stockpb.ReserveSellerSharesResponse{Ok: ok}, nil
}

func (s *stubHoldingChecker) ReleaseSellerSharesForNewTx(ctx context.Context, in *stockpb.ReleaseSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReleaseSellerSharesResponse, error) {
	s.releaseCalls++
	s.lastReleaseTxID = in.GetCrossbankTxId()
	return &stockpb.ReleaseSellerSharesResponse{}, nil
}

func (s *stubHoldingChecker) ValidatePeerOptionMoneyLeg(ctx context.Context, in *stockpb.ValidatePeerOptionMoneyLegRequest, opts ...grpc.CallOption) (*stockpb.ValidatePeerOptionMoneyLegResponse, error) {
	s.validateCalls++
	s.lastValidate = in
	if s.validateErr != nil {
		return nil, s.validateErr
	}
	return &stockpb.ValidatePeerOptionMoneyLegResponse{Ok: !s.validateDeny}, nil
}

// TestPostingExecutor_NoSuchAccount_PostingIndex is the Task 9 regression:
// a 2-posting TX whose 2nd posting (index 1) credits a non-existent account on
// OUR routing must vote NO with NO_SUCH_ACCOUNT and the offending posting's
// 0-based index (1). The 1st posting is a DEBIT on the peer's routing (skipped
// locally), so the failure is unambiguously index 1.
func TestPostingExecutor_NoSuchAccount_PostingIndex(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return nil, errors.New("not found")
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(222, "222000001", "RSD", 100, contractsitx.DirectionDebit), // index 0, peer routing → skipped
		money(111, "111-nope", "RSD", 100, contractsitx.DirectionCredit), // index 1, our routing, missing account
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-NSA")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) != 1 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Fatalf("expected NO_SUCH_ACCOUNT, got %+v", res.Vote.NoVotes)
	}
	if res.Vote.NoVotes[0].Posting == nil {
		t.Fatalf("expected posting index set on NO_SUCH_ACCOUNT, got nil")
	}
	if *res.Vote.NoVotes[0].Posting != 1 {
		t.Errorf("expected posting index 1, got %d", *res.Vote.NoVotes[0].Posting)
	}
}

// TestPostingExecutor_AccountInactive_Unacceptable verifies that an inactive
// destination account on our routing yields UNACCEPTABLE_ASSET on a CREDIT.
func TestPostingExecutor_AccountInactive_Unacceptable(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "inactive"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(222, "222000001", "RSD", 50, contractsitx.DirectionDebit),
		money(111, "111000001", "RSD", 50, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-X")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if len(res.Vote.NoVotes) == 0 || res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("expected UNACCEPTABLE_ASSET, got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_ReserveFails_VotesNo verifies that a reservation gRPC
// failure surfaces as UNACCEPTABLE_ASSET.
func TestPostingExecutor_ReserveFails_VotesNo(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
			return nil, errors.New("reserve boom")
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111000001", "RSD", 50, contractsitx.DirectionCredit),
		money(222, "222000001", "RSD", 50, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-Y")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_DebitFails_InsufficientAsset verifies that a failing
// ReserveOutgoing (insufficient available balance) on a DEBIT-on-our-routing
// surfaces INSUFFICIENT_ASSET.
func TestPostingExecutor_DebitFails_InsufficientAsset(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		reserveOutFn: func(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error) {
			return nil, errors.New("insufficient available balance")
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionDebit),
		money(222, "222-B", "RSD", 100, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-Z")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_OptionItem_NoChecker_AlwaysIncluded verifies that option
// postings (assetId starts with `{`) are emitted as OptionItems even when
// holdingChecker is nil. Buyer/Seller maps populated from CREDIT/DEBIT
// directions in the same TX.
func TestPostingExecutor_OptionItem_NoChecker_AlwaysIncluded(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-3"},"stock":{"ticker":"AAPL"},"pricePerUnit":{"amount":10,"currency":"RSD"},"settlementDate":"2026-12-31","amount":1}`
	postings := []contractsitx.InternalPosting{
		// Money leg balances out.
		money(111, "111-pay", "RSD", 100, contractsitx.DirectionDebit),
		money(222, "222-pay", "RSD", 100, contractsitx.DirectionCredit),
		// Option leg: SELLER on routing 222, BUYER (credit) on routing 111.
		option(222, "client-7", optDesc, 1, contractsitx.DirectionDebit),
		option(111, "client-3", optDesc, 1, contractsitx.DirectionCredit),
	}
	// Override the get-account stub so the money DEBIT on our routing succeeds.
	stub.getAccountFn = func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
	}
	stub.updateFn = func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
		return &accountpb.AccountResponse{}, nil
	}
	stub.reserveFn = func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
		return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey}, nil
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-O")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if len(res.OptionItems) != 1 {
		t.Fatalf("expected 1 option item, got %d", len(res.OptionItems))
	}
	if res.OptionItems[0].Direction != contractsitx.DirectionCredit {
		t.Errorf("expected CREDIT direction (option on our routing 111 is buyer), got %q", res.OptionItems[0].Direction)
	}
	if res.OptionItems[0].Buyer.RoutingNumber != 111 || res.OptionItems[0].Buyer.ID != "client-3" {
		t.Errorf("buyer not populated: %+v", res.OptionItems[0].Buyer)
	}
	if res.OptionItems[0].Seller.RoutingNumber != 222 || res.OptionItems[0].Seller.ID != "client-7" {
		t.Errorf("seller not populated: %+v", res.OptionItems[0].Seller)
	}
}

// TestPostingExecutor_OptionItem_HoldingChecker_RejectsOnInsufficient
// verifies that when this bank is the seller and the holding checker reports
// not-OK, the executor votes NO INSUFFICIENT_ASSET.
func TestPostingExecutor_OptionItem_HoldingChecker_RejectsOnInsufficient(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetHoldingChecker(&stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: false}})
	optDesc := `{"stock":{"ticker":"GOOG"},"amount":2}`
	postings := []contractsitx.InternalPosting{
		// Option DEBIT on our routing 111 = WE are the seller.
		option(111, "client-9", optDesc, 2, contractsitx.DirectionDebit),
		option(222, "client-10", optDesc, 2, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-OC")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_OptionAssetDebitLeg_ReservesSellerShares verifies that a
// DEBIT OPTION-asset leg on our routing (we are the seller) with a valid nested
// OptionDescription DOES call ReserveSellerSharesForNewTx. The executor always
// treats OPTION-asset postings as accept-phase: the intent is supplied internally
// and is no longer carried on the wire, so any well-formed OptionDescription
// (with stock.ticker populated) must trigger a seller-share hold at vote time.
func TestPostingExecutor_OptionAssetDebitLeg_ReservesSellerShares(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	hc := &stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}}
	exec.SetHoldingChecker(hc)
	// Nested OptionDescription — matches the wire format after the reshape.
	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-1"},"stock":{"ticker":"MSFT"},"pricePerUnit":{"amount":50,"currency":"RSD"},"settlementDate":"2026-12-31","amount":3}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-1", optDesc, 3, contractsitx.DirectionDebit),
		option(222, "client-2", optDesc, 3, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-EX")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if hc.reserveCalls != 1 {
		t.Errorf("expected exactly 1 ReserveSellerSharesForNewTx call; got %d", hc.reserveCalls)
	}
	if hc.lastReserveTxID != "222:idem-EX" {
		t.Errorf("reserve keyed on %q, want 222:idem-EX", hc.lastReserveTxID)
	}
	// Verify the reserve was for the correct ticker and quantity.
	if len(res.OptionItems) != 1 {
		t.Errorf("expected 1 option item, got %d", len(res.OptionItems))
	}
}

// TestPostingExecutor_OptionItem_HoldingChecker_OK verifies the YES path
// when the seller has sufficient holdings, and that ReserveSellerSharesForNewTx
// is called with the correct Ticker and Quantity extracted from the nested wire JSON.
func TestPostingExecutor_OptionItem_HoldingChecker_OK(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	hc := &stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}}
	exec.SetHoldingChecker(hc)
	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-4"},"stock":{"ticker":"MSFT"},"pricePerUnit":{"amount":75,"currency":"RSD"},"settlementDate":"2026-12-31","amount":3}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-1", optDesc, 3, contractsitx.DirectionDebit),
		option(222, "client-2", optDesc, 3, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-OO")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if len(res.OptionItems) != 1 {
		t.Errorf("expected 1 option item, got %d", len(res.OptionItems))
	}
	// Verify the ticker extraction: ReserveSellerSharesForNewTx must have been
	// called with Ticker="MSFT" and Quantity=3 — proving the nested stock.ticker
	// field is correctly parsed from the wire JSON.
	if hc.reserveCalls != 1 {
		t.Fatalf("expected exactly 1 ReserveSellerSharesForNewTx call; got %d", hc.reserveCalls)
	}
	if hc.lastReserveTxID != "222:idem-OO" {
		t.Errorf("reserve keyed on %q, want 222:idem-OO", hc.lastReserveTxID)
	}
	if hc.lastReserveTicker != "MSFT" {
		t.Errorf("reserve Ticker = %q, want MSFT", hc.lastReserveTicker)
	}
	if hc.lastReserveQty != 3 {
		t.Errorf("reserve Quantity = %d, want 3", hc.lastReserveQty)
	}
}

// TestPostingExecutor_OptionItem_ReservesSharesAtVote is the regression test
// for the spec deviation: at NEW_TX (vote) the executor must RESERVE the
// seller's shares (a real hold keyed on crossbank_tx_id = "<peer>:<idem>"),
// not merely check them — so they can't be sold before COMMIT.
func TestPostingExecutor_OptionItem_ReservesSharesAtVote(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	chk := &stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}}
	exec.SetHoldingChecker(chk)
	optDesc := `{"stock":{"ticker":"AAPL"},"amount":4}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-7", optDesc, 4, contractsitx.DirectionDebit),
		option(222, "client-8", optDesc, 4, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-RS")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if chk.reserveCalls != 1 {
		t.Fatalf("expected exactly 1 share RESERVE call at vote, got %d", chk.reserveCalls)
	}
	if chk.lastReserveTxID != "222:idem-RS" {
		t.Errorf("reserve keyed on %q, want 222:idem-RS", chk.lastReserveTxID)
	}
}

// TestPostingExecutor_ExerciseForgedStrike_NoVote is the forged-strike theft
// regression: a seller-side (DEBIT) exercise option leg whose paired money
// CREDIT to the seller does NOT match the receiver's stored terms must produce
// a NO vote (the validator denies), so no shares are delivered for too little
// money. The option leg is ordered first so the deny short-circuits before the
// money leg is even reserved.
func TestPostingExecutor_ExerciseForgedStrike_NoVote(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 222)
	chk := &stubHoldingChecker{validateDeny: true} // receiver's terms don't match → deny
	exec.SetHoldingChecker(chk)
	od := `{"negotiationId":{"routingNumber":222,"id":"neg-1"},"stock":{"ticker":"MA"},"pricePerUnit":{"amount":250,"currency":"RSD"},"settlementDate":"","amount":2}`
	postings := []contractsitx.InternalPosting{
		option(222, "client-1", od, 2, contractsitx.DirectionDebit),    // seller option leg (own routing)
		money(222, "client-1", "RSD", 1, contractsitx.DirectionCredit), // forged strike = 1 (should be 500)
		money(111, "111-BUY", "RSD", 1, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-FORGE")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO vote on forged-strike exercise, got %+v", res.Vote)
	}
	if chk.validateCalls != 1 {
		t.Fatalf("expected exactly 1 money-leg validation, got %d", chk.validateCalls)
	}
	// The validator must have been handed the SELLER's paired money (the forged 1)
	// and negotiation identity, so it can compare to stored terms.
	// Intent is now always OptionIntentAccept — exercise is signalled by TX shape.
	if got := chk.lastValidate.GetMoneyAmount(); got != "1" {
		t.Errorf("validator money_amount = %q, want 1 (the forged seller credit)", got)
	}
	if chk.lastValidate.GetIntent() != contractsitx.OptionIntentAccept {
		t.Errorf("validator intent = %q, want %q", chk.lastValidate.GetIntent(), contractsitx.OptionIntentAccept)
	}
	if chk.lastValidate.GetNegotiationId() != "neg-1" || chk.lastValidate.GetDirection() != "DEBIT" {
		t.Errorf("validator neg/dir = %q/%q, want neg-1/DEBIT", chk.lastValidate.GetNegotiationId(), chk.lastValidate.GetDirection())
	}
}

// TestPostingExecutor_ExerciseBuyerOvercharge_NoVote is the mirror of the
// forged-strike test for the BUYER's bank: a CREDIT (buyer) option leg whose
// paired money DEBIT (the buyer's strike payment) exceeds the receiver's stored
// terms must produce a NO vote, so a malicious peer cannot overcharge the buyer.
// Intent is no longer wire-carried; the executor always passes OptionIntentAccept
// to the validator regardless of the transaction shape.
func TestPostingExecutor_ExerciseBuyerOvercharge_NoVote(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111) // buyer's bank
	chk := &stubHoldingChecker{validateDeny: true}
	exec.SetHoldingChecker(chk)
	od := `{"negotiationId":{"routingNumber":222,"id":"neg-9"},"stock":{"ticker":"MA"},"pricePerUnit":{"amount":250,"currency":"RSD"},"settlementDate":"","amount":2}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-1", od, 2, contractsitx.DirectionCredit),           // buyer option leg (own routing)
		money(111, "111-BUYER-ACCT", "RSD", 9000, contractsitx.DirectionDebit), // forged-high strike 9000 (should be 500)
		money(222, "client-1", "RSD", 9000, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-OVER")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO vote on buyer-overcharge exercise, got %+v", res.Vote)
	}
	if chk.validateCalls != 1 {
		t.Fatalf("expected exactly 1 money-leg validation, got %d", chk.validateCalls)
	}
	if chk.lastValidate.GetDirection() != "CREDIT" {
		t.Errorf("validator direction = %q, want CREDIT (buyer side)", chk.lastValidate.GetDirection())
	}
	if got := chk.lastValidate.GetMoneyAmount(); got != "9000" {
		t.Errorf("validator money_amount = %q, want 9000 (the forged buyer debit)", got)
	}
	// Verify the validator received the correct Ticker and NegotiationId from the wire.
	if chk.lastValidate.GetTicker() != "MA" {
		t.Errorf("validator ticker = %q, want MA", chk.lastValidate.GetTicker())
	}
	if chk.lastValidate.GetNegotiationId() != "neg-9" {
		t.Errorf("validator negotiation_id = %q, want neg-9", chk.lastValidate.GetNegotiationId())
	}
}

// TestPostingExecutor_ExerciseHonestStrike_Yes verifies the happy path: when the
// seller's paired money CREDIT matches stored terms the validator approves and
// the vote is YES, and the validator received the correctly-summed seller money,
// correct Ticker, and correct NegotiationId from the nested wire JSON.
// Intent is no longer wire-carried; the executor always passes OptionIntentAccept
// to the validator regardless of the transaction shape.
func TestPostingExecutor_ExerciseHonestStrike_Yes(t *testing.T) {
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
			},
			reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
				return &accountpb.ReserveIncomingResponse{}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{AccountNumber: "222000000000000001", CurrencyCode: "RSD", Status: "active"}}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	chk := &stubHoldingChecker{} // validateDeny false → approve
	exec.SetHoldingChecker(chk)
	od := `{"negotiationId":{"routingNumber":222,"id":"neg-2"},"stock":{"ticker":"MA"},"pricePerUnit":{"amount":250,"currency":"RSD"},"settlementDate":"","amount":2}`
	postings := []contractsitx.InternalPosting{
		money(222, "client-1", "RSD", 500, contractsitx.DirectionCredit), // honest strike 250*2
		option(222, "client-1", od, 2, contractsitx.DirectionDebit),
		money(111, "111-BUY", "RSD", 500, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-HONEST")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES on honest-strike exercise, got %+v", res.Vote)
	}
	if chk.validateCalls != 1 {
		t.Fatalf("expected 1 validation call, got %d", chk.validateCalls)
	}
	if got := chk.lastValidate.GetMoneyAmount(); got != "500" {
		t.Errorf("validator money_amount = %q, want 500 (summed seller credit)", got)
	}
	// Verify the validator received the correct Ticker and NegotiationId from the wire.
	if chk.lastValidate.GetTicker() != "MA" {
		t.Errorf("validator ticker = %q, want MA", chk.lastValidate.GetTicker())
	}
	if chk.lastValidate.GetNegotiationId() != "neg-2" {
		t.Errorf("validator negotiation_id = %q, want neg-2", chk.lastValidate.GetNegotiationId())
	}
}

// TestPostingExecutor_ReverseLocal_ReleasesShareHold verifies a rollback of a
// sender-local OTC apply releases the vote-time share hold (keyed on the SI-TX
// identity), so a rolled-back trade doesn't strand the seller's shares.
func TestPostingExecutor_ReverseLocal_ReleasesShareHold(t *testing.T) {
	stub := &stubAccountClient{
		releaseFn: func(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
			return &accountpb.ReleaseIncomingResponse{}, nil // no CREDIT legs on our routing here; benign
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	chk := &stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}}
	exec.SetHoldingChecker(chk)
	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-5"},"stock":{"ticker":"AAPL"},"pricePerUnit":{"amount":20,"currency":"RSD"},"settlementDate":"2026-12-31","amount":4}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-7", optDesc, 4, contractsitx.DirectionDebit),
		option(222, "client-8", optDesc, 4, contractsitx.DirectionCredit),
	}
	if err := exec.ReverseLocal(context.Background(), postings, "222", "idem-RV"); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if chk.releaseCalls != 1 {
		t.Fatalf("expected 1 share RELEASE on reverse, got %d", chk.releaseCalls)
	}
	if chk.lastReleaseTxID != "222:idem-RV" {
		t.Errorf("release keyed on %q, want 222:idem-RV", chk.lastReleaseTxID)
	}
}

// TestPostingExecutor_ResolveClientAccountID_HappyPath verifies the
// "client-<n>" resolver looks up the matching active account by currency.
func TestPostingExecutor_ResolveClientAccountID_HappyPath(t *testing.T) {
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
			},
			reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
				if in.AccountNumber != "111-resolved-001" {
					t.Errorf("expected resolved account number, got %q", in.AccountNumber)
				}
				return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "111-resolved-001", CurrencyCode: "RSD", Status: "active"},
			}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "client-42", "RSD", 100, contractsitx.DirectionCredit),
		money(222, "222-X", "RSD", 100, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-R")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
}

// TestPostingExecutor_ResolveClientAccountID_NoMatch verifies that no matching
// active account for the requested currency yields NO_SUCH_ACCOUNT.
func TestPostingExecutor_ResolveClientAccountID_NoMatch(t *testing.T) {
	stub := &stubAccountClientList{
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "111-eur", CurrencyCode: "EUR", Status: "active"},
			}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "client-42", "RSD", 50, contractsitx.DirectionCredit),
		money(222, "222-X", "RSD", 50, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-NM")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_ResolveBankAccountID_HappyPath verifies that a BANK
// participant id ("employee-<N>" or "bank") on a credit leg resolves to the
// bank's own active account in the requested currency (via the bank sentinel
// owner id). Without this, a cross-bank OTC accept where THIS bank holds the
// bank SELLER could not resolve the seller-credit posting and voted NO with
// NO_SUCH_ACCOUNT — stranding the bank↔bank accept SI-TX in "committing".
func TestPostingExecutor_ResolveBankAccountID_HappyPath(t *testing.T) {
	const bankOwnerID uint64 = 1_000_000_000
	var listedClientID uint64
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "USD", Status: "active"}, nil
			},
			reserveFn: func(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
				if in.AccountNumber != "111-BANK-USD-01" {
					t.Errorf("expected resolved bank account number, got %q", in.AccountNumber)
				}
				return &accountpb.ReserveIncomingResponse{ReservationKey: in.ReservationKey}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			listedClientID = in.ClientId
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "111-BANK-USD-01", CurrencyCode: "USD", Status: "active", AccountType: "bank", OwnerId: bankOwnerID},
			}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "employee-1", "USD", 5, contractsitx.DirectionCredit), // bank seller credit
		money(222, "222-X", "USD", 5, contractsitx.DirectionDebit),       // buyer debit elsewhere
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-bank")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES (bank participant resolves to bank account), got %+v", res.Vote)
	}
	if listedClientID != bankOwnerID {
		t.Errorf("expected bank participant resolved via bank sentinel owner id %d, listed %d", bankOwnerID, listedClientID)
	}
}

// TestPostingExecutor_UnknownDirection_Unacceptable verifies the default branch.
func TestPostingExecutor_UnknownDirection_Unacceptable(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.InternalPosting{
		{RoutingNumber: 111, AccountType: "ACCOUNT", AccountID: "111-A", AssetType: "MONAS", AssetID: "RSD", Amount: "50", Direction: "FOO"},
		money(222, "222-B", "RSD", 50, contractsitx.DirectionCredit),
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-D")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// optionPseudo builds an OPTION pseudo-account posting (exercise form): the
// AccountType is OPTION, AccountID is the negotiationId, RoutingNumber is the
// negotiation's routing, and the asset (MONAS strike or STOCK underlying) leaves
// the pseudo-account.
func optionPseudo(rn int64, negID, assetType, assetID string, amount int64, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   contractsitx.AccountTypeOption,
		AccountID:     negID,
		AssetType:     assetType,
		AssetID:       assetID,
		Amount:        decimalStr(amount),
		Direction:     dir,
	}
}

// TestPostingExecutor_PseudoLeg_LookupError_FailsClosed verifies a transient
// LookupPeerOptionContract error on an OPTION pseudo-account leg fails closed:
// the executor votes NO OPTION_NEGOTIATION_NOT_FOUND so money never moves on an
// exercise it couldn't verify ownership of.
func TestPostingExecutor_PseudoLeg_LookupError_FailsClosed(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(&stubHoldingChecker{lookupErr: errors.New("stock-service down")})
	postings := []contractsitx.InternalPosting{
		optionPseudo(222, "neg-7", contractsitx.AssetTypeMonas, "RSD", 500, contractsitx.DirectionDebit),
	}
	// peerBankCode != ownRouting → receiver path, so a lookup failure is a hard NO.
	res := exec.Reserve(context.Background(), postings, "111", "idem-LE")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonOptionNegotiationNotFound {
		t.Errorf("expected OPTION_NEGOTIATION_NOT_FOUND, got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_PseudoLeg_NilChecker_Skipped verifies that with no holding
// checker wired the executor cannot prove ownership of an OPTION pseudo-account
// leg, so it SKIPS the leg (does not vote NO) — matching the current code at the
// nil-checker guard. With only the (skipped) pseudo leg present the vote is YES.
func TestPostingExecutor_PseudoLeg_NilChecker_Skipped(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 222) // no SetHoldingChecker → nil checker
	postings := []contractsitx.InternalPosting{
		optionPseudo(222, "neg-8", contractsitx.AssetTypeStock, "MSFT", 3, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-NC")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES (leg skipped), got %+v", res.Vote)
	}
	if len(res.OptionItems) != 0 {
		t.Errorf("expected no option items for a skipped leg, got %d", len(res.OptionItems))
	}
}

// lookupFound is a holding-checker preset that returns a found seller contract
// with the given terms for the pseudo-MONAS-credit gate tests.
func lookupFound(strike string, qty int64) *stubHoldingChecker {
	return &stubHoldingChecker{lookupResp: &stockpb.LookupPeerOptionContractResponse{
		Found:       true,
		SellerId:    "client-1",
		StrikePrice: strike,
		Quantity:    qty,
		Status:      "active",
	}}
}

// TestPostingExecutor_PseudoMonas_InactiveSeller_Unacceptable verifies that on a
// pseudo-MONAS strike credit, an inactive seller money account yields
// UNACCEPTABLE_ASSET (the active-gate, shared with the generic credit path).
func TestPostingExecutor_PseudoMonas_InactiveSeller_Unacceptable(t *testing.T) {
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "inactive"}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			// Resolver needs an active match; GetAccountByNumber then reports inactive.
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{AccountNumber: "222-acct", CurrencyCode: "RSD", Status: "active"}}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(lookupFound("250", 2))
	postings := []contractsitx.InternalPosting{
		optionPseudo(222, "neg-9", contractsitx.AssetTypeMonas, "RSD", 500, contractsitx.DirectionDebit), // 250*2
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-IS")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("expected UNACCEPTABLE_ASSET, got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_PseudoMonas_CurrencyMismatch_NoSuchAsset verifies that a
// resolved seller account in the wrong currency yields NO_SUCH_ASSET (the
// currency-gate, shared with the generic credit path).
func TestPostingExecutor_PseudoMonas_CurrencyMismatch_NoSuchAsset(t *testing.T) {
	stub := &stubAccountClientList{
		stubAccountClient: stubAccountClient{
			getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
				return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "EUR", Status: "active"}, nil
			},
		},
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			// Return an explicit account number so resolveAccountForPosting succeeds;
			// GetAccountByNumber then reports the (mismatched) EUR currency.
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{AccountNumber: "222-acct", CurrencyCode: "RSD", Status: "active"}}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(lookupFound("250", 2))
	postings := []contractsitx.InternalPosting{
		optionPseudo(222, "neg-10", contractsitx.AssetTypeMonas, "RSD", 500, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-CM")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAsset {
		t.Errorf("expected NO_SUCH_ASSET, got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_PseudoMonas_UnresolvableSeller_NoSuchAccount verifies that
// when the seller's "client-<n>" id resolves to no active account in the
// currency, the leg yields NO_SUCH_ACCOUNT (the resolve-gate, shared with the
// generic credit path).
func TestPostingExecutor_PseudoMonas_UnresolvableSeller_NoSuchAccount(t *testing.T) {
	stub := &stubAccountClientList{
		listFn: func(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
			// No RSD account → resolveAccountForPosting returns an error.
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{{AccountNumber: "222-eur", CurrencyCode: "EUR", Status: "active"}}}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 222)
	exec.SetHoldingChecker(lookupFound("250", 2))
	postings := []contractsitx.InternalPosting{
		optionPseudo(222, "neg-11", contractsitx.AssetTypeMonas, "RSD", 500, contractsitx.DirectionDebit),
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-UR")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Errorf("expected NO_SUCH_ACCOUNT, got %+v", res.Vote.NoVotes)
	}
}

// TestVoteBuilder_BalancedPostings_YES verifies BuildPrelimVote returns YES
// when net per assetId is zero.
func TestVoteBuilder_BalancedPostings_YES(t *testing.T) {
	v := sitx.BuildPrelimVote([]contractsitx.InternalPosting{
		{AssetType: "MONAS", AssetID: "RSD", Amount: "100", Direction: contractsitx.DirectionDebit},
		{AssetType: "MONAS", AssetID: "RSD", Amount: "100", Direction: contractsitx.DirectionCredit},
	})
	if v.Type != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", v)
	}
}
