package sitx_test

import (
	"context"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
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
	reserveCalls    int
	releaseCalls    int
	lastReserveTxID string
	lastReleaseTxID string
	// money-leg validation (forged-strike defense). validateOK defaults to true
	// (nil) so existing tests keep voting YES; set validateDeny to force a NO.
	validateCalls int
	lastValidate  *stockpb.ValidatePeerOptionMoneyLegRequest
	validateDeny  bool
	validateErr   error
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

// TestPostingExecutor_AccountInactive_Unacceptable verifies that an inactive
// destination account on our routing yields UNACCEPTABLE_ASSET on a CREDIT.
func TestPostingExecutor_AccountInactive_Unacceptable(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "inactive"}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)
	postings := []contractsitx.Posting{
		{RoutingNumber: 222, AccountID: "222000001", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 111, AccountID: "111000001", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionCredit},
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
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111000001", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionCredit},
		{RoutingNumber: 222, AccountID: "222000001", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionDebit},
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
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111-A", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "222-B", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
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
	optDesc := `{"ticker":"AAPL","amount":1}`
	postings := []contractsitx.Posting{
		// Money leg balances out.
		{RoutingNumber: 111, AccountID: "111-pay", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "222-pay", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
		// Option leg: SELLER on routing 222, BUYER (credit) on routing 111.
		{RoutingNumber: 222, AccountID: "client-7", AssetID: optDesc, Amount: decimal.NewFromInt(1), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 111, AccountID: "client-3", AssetID: optDesc, Amount: decimal.NewFromInt(1), Direction: contractsitx.DirectionCredit},
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
	optDesc := `{"ticker":"GOOG","amount":2}`
	postings := []contractsitx.Posting{
		// Option DEBIT on our routing 111 = WE are the seller.
		{RoutingNumber: 111, AccountID: "client-9", AssetID: optDesc, Amount: decimal.NewFromInt(2), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "client-10", AssetID: optDesc, Amount: decimal.NewFromInt(2), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-OC")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestPostingExecutor_OptionItem_ExerciseDoesNotReserve is the regression test
// for the exercise over-reservation bug: an EXERCISE-intent DEBIT option leg
// must NOT reserve seller shares at vote time (the shares were already held at
// accept and are consumed at COMMIT). Reserving again orphaned a second hold
// that nothing released, permanently locking those shares.
func TestPostingExecutor_OptionItem_ExerciseDoesNotReserve(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	hc := &stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}}
	exec.SetHoldingChecker(hc)
	// Option DEBIT on our routing 111 = WE are the seller; intent=exercise.
	optDesc := `{"ticker":"MSFT","amount":3,"intent":"exercise"}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-1", AssetID: optDesc, Amount: decimal.NewFromInt(3), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "client-2", AssetID: optDesc, Amount: decimal.NewFromInt(3), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-EX")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES on exercise leg, got %+v", res.Vote)
	}
	if hc.reserveCalls != 0 {
		t.Errorf("exercise must NOT reserve seller shares at vote; got %d reserve calls", hc.reserveCalls)
	}
	if len(res.OptionItems) != 1 {
		t.Errorf("expected the exercise option leg still emitted as an OptionItem, got %d", len(res.OptionItems))
	}
}

// TestPostingExecutor_OptionItem_HoldingChecker_OK verifies the YES path
// when the seller has sufficient holdings.
func TestPostingExecutor_OptionItem_HoldingChecker_OK(t *testing.T) {
	stub := &stubAccountClient{}
	exec := sitx.NewPostingExecutor(stub, 111)
	exec.SetHoldingChecker(&stubHoldingChecker{resp: &stockpb.CheckSellerCanDeliverResponse{Ok: true}})
	optDesc := `{"ticker":"MSFT","amount":3}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-1", AssetID: optDesc, Amount: decimal.NewFromInt(3), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "client-2", AssetID: optDesc, Amount: decimal.NewFromInt(3), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-OO")
	if res.Vote.Type != contractsitx.VoteYes {
		t.Fatalf("expected YES, got %+v", res.Vote)
	}
	if len(res.OptionItems) != 1 {
		t.Errorf("expected 1 option item, got %d", len(res.OptionItems))
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
	optDesc := `{"ticker":"AAPL","amount":4}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-7", AssetID: optDesc, Amount: decimal.NewFromInt(4), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "client-8", AssetID: optDesc, Amount: decimal.NewFromInt(4), Direction: contractsitx.DirectionCredit},
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
	od := `{"ticker":"MA","amount":2,"strikePrice":"250","currency":"RSD","negotiationId":{"routingNumber":222,"id":"neg-1"},"intent":"exercise"}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 222, AccountID: "client-1", AssetID: od, Amount: decimal.NewFromInt(2), Direction: contractsitx.DirectionDebit},     // seller option leg (own routing)
		{RoutingNumber: 222, AccountID: "client-1", AssetID: "RSD", Amount: decimal.NewFromInt(1), Direction: contractsitx.DirectionCredit}, // forged strike = 1 (should be 500)
		{RoutingNumber: 111, AccountID: "111-BUY", AssetID: "RSD", Amount: decimal.NewFromInt(1), Direction: contractsitx.DirectionDebit},
	}
	res := exec.Reserve(context.Background(), postings, "111", "idem-FORGE")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO vote on forged-strike exercise, got %+v", res.Vote)
	}
	if chk.validateCalls != 1 {
		t.Fatalf("expected exactly 1 money-leg validation, got %d", chk.validateCalls)
	}
	// The validator must have been handed the SELLER's paired money (the forged 1)
	// and the exercise intent + negotiation identity, so it can compare to stored terms.
	if got := chk.lastValidate.GetMoneyAmount(); got != "1" {
		t.Errorf("validator money_amount = %q, want 1 (the forged seller credit)", got)
	}
	if chk.lastValidate.GetIntent() != "exercise" {
		t.Errorf("validator intent = %q, want exercise", chk.lastValidate.GetIntent())
	}
	if chk.lastValidate.GetNegotiationId() != "neg-1" || chk.lastValidate.GetDirection() != "DEBIT" {
		t.Errorf("validator neg/dir = %q/%q, want neg-1/DEBIT", chk.lastValidate.GetNegotiationId(), chk.lastValidate.GetDirection())
	}
}

// TestPostingExecutor_ExerciseHonestStrike_Yes verifies the happy path: when the
// seller's paired money CREDIT matches stored terms the validator approves and
// the vote is YES, and the validator received the correctly-summed seller money.
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
	od := `{"ticker":"MA","amount":2,"strikePrice":"250","currency":"RSD","negotiationId":{"routingNumber":222,"id":"neg-2"},"intent":"exercise"}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 222, AccountID: "client-1", AssetID: "RSD", Amount: decimal.NewFromInt(500), Direction: contractsitx.DirectionCredit}, // honest strike 250*2
		{RoutingNumber: 222, AccountID: "client-1", AssetID: od, Amount: decimal.NewFromInt(2), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 111, AccountID: "111-BUY", AssetID: "RSD", Amount: decimal.NewFromInt(500), Direction: contractsitx.DirectionDebit},
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
	optDesc := `{"ticker":"AAPL","amount":4}`
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-7", AssetID: optDesc, Amount: decimal.NewFromInt(4), Direction: contractsitx.DirectionDebit},
		{RoutingNumber: 222, AccountID: "client-8", AssetID: optDesc, Amount: decimal.NewFromInt(4), Direction: contractsitx.DirectionCredit},
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
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-42", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
		{RoutingNumber: 222, AccountID: "222-X", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
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
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "client-42", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionCredit},
		{RoutingNumber: 222, AccountID: "222-X", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionDebit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-NM")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonNoSuchAccount {
		t.Errorf("got %+v", res.Vote.NoVotes)
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
	postings := []contractsitx.Posting{
		{RoutingNumber: 111, AccountID: "111-A", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: "FOO"},
		{RoutingNumber: 222, AccountID: "222-B", AssetID: "RSD", Amount: decimal.NewFromInt(50), Direction: contractsitx.DirectionCredit},
	}
	res := exec.Reserve(context.Background(), postings, "222", "idem-D")
	if res.Vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", res.Vote)
	}
	if res.Vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnacceptableAsset {
		t.Errorf("got %+v", res.Vote.NoVotes)
	}
}

// TestVoteBuilder_BalancedPostings_YES verifies BuildPrelimVote returns YES
// when net per assetId is zero.
func TestVoteBuilder_BalancedPostings_YES(t *testing.T) {
	v := sitx.BuildPrelimVote([]contractsitx.Posting{
		{AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
		{AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
	})
	if v.Type != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", v)
	}
}
