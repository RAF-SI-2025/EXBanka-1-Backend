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
// UpdateBalance on a DEBIT-on-our-routing surfaces INSUFFICIENT_ASSET.
func TestPostingExecutor_DebitFails_InsufficientAsset(t *testing.T) {
	stub := &stubAccountClient{
		getAccountFn: func(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD", Status: "active"}, nil
		},
		updateFn: func(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error) {
			return nil, errors.New("insufficient funds")
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
