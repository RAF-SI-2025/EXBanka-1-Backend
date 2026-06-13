package handler_test

import (
	"context"
	"encoding/json"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
)

// constResolver always returns the same seller account number.
type constResolver struct{ number string }

func (c constResolver) ResolveSellerAccountNumber(_ context.Context, _ *model.OTCNegotiation, _ string) string {
	return c.number
}

// fakeSellerAccountResolver returns a fixed account number for the seller's
// nominated account (the bound account on the local listing). Empty number ⇒
// "no nomination available" (the documented first-active fallback path).
type fakeSellerAccountResolver struct {
	number    string
	gotNeg    *model.OTCNegotiation
	gotCcy    string
	callCount int
}

func (f *fakeSellerAccountResolver) ResolveSellerAccountNumber(_ context.Context, neg *model.OTCNegotiation, premiumCurrency string) string {
	f.callCount++
	f.gotNeg = neg
	f.gotCcy = premiumCurrency
	return f.number
}

// TestAcceptNegotiation_SellerCreditPinsNominatedAccount verifies sub-case 1:
// when WE host the seller and compose the accept postings, the seller's
// premium-CREDIT leg (posting 1) carries the seller's NOMINATED account number
// as an ACCOUNT-typed posting (spec §2.6 TxAccount.ACCOUNT{num}) — NOT the
// participant id resolved loosely to "first active account in the currency".
// The OPTION legs keep the seller PARTICIPANT id (they become the contract's
// seller_id used for exercise + /me listing).
func TestAcceptNegotiation_SellerCreditPinsNominatedAccount(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	const sellerNominated = "111000000000000777"
	resolver := &fakeSellerAccountResolver{number: sellerNominated}
	h = h.WithSellerAccountResolver(resolver)

	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 100,
		PricePerStock:      decimal.RequireFromString("150.00"),
		Currency:           "USD",
		Premium:            decimal.RequireFromString("10.00"),
		PremiumCurrency:    "USD",
		SettlementDate:     "2026-12-31",
		BuyerAccountNumber: "111000000000000999",
		LastModifiedBy:     contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	seedRepo := repository.NewOTCNegotiationRepository(db)
	if err := seedRepo.UpsertRemoteNeg(buildRemoteNegForTest(
		222, "neg-nominated", offer, string(offerJSON),
		222, "client-7", 111, "client-9",
	)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-1", Status: "initiated"}
	if _, err := h.AcceptNegotiation(ctx, &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-nominated"},
	}); err != nil {
		t.Fatalf("accept: %v", err)
	}

	if resolver.callCount == 0 {
		t.Fatalf("expected SellerAccountResolver to be consulted")
	}
	if resolver.gotCcy != "USD" {
		t.Errorf("resolver got premium currency %q, want USD", resolver.gotCcy)
	}

	postings := peerTx.gotReq.GetPostings()
	if len(postings) != 4 {
		t.Fatalf("expected 4 postings, got %d", len(postings))
	}
	// Posting 1: seller CREDIT premium — must carry the NOMINATED ACCOUNT NUMBER.
	if postings[1].GetDirection() != "CREDIT" {
		t.Fatalf("posting 1 direction %q, want CREDIT", postings[1].GetDirection())
	}
	if postings[1].GetAccountId() != sellerNominated {
		t.Errorf("posting 1 (seller credit) account_id = %q, want nominated %q", postings[1].GetAccountId(), sellerNominated)
	}
	if postings[1].GetAccountType() != "ACCOUNT" {
		t.Errorf("posting 1 account_type = %q, want ACCOUNT (concrete account-number leg)", postings[1].GetAccountType())
	}
	// Posting 2: seller DEBIT option — must KEEP the seller PARTICIPANT id.
	if postings[2].GetAccountId() != "client-9" || postings[2].GetAccountType() != "PERSON" {
		t.Errorf("posting 2 (seller option leg) must keep participant id PERSON, got id=%q type=%q",
			postings[2].GetAccountId(), postings[2].GetAccountType())
	}
}

// TestAcceptNegotiation_SellerCreditFallsBackWhenUnresolved verifies the
// documented fallback: when the seller's nominated account can't be resolved
// (resolver returns "" — free-form negotiation, no parent listing, or unbound
// account), the seller-credit leg keeps the participant id (PERSON), preserving
// the prior first-active behaviour. Conservation is unchanged.
func TestAcceptNegotiation_SellerCreditFallsBackWhenUnresolved(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	resolver := &fakeSellerAccountResolver{number: ""} // no nomination available
	h = h.WithSellerAccountResolver(resolver)

	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 100,
		PricePerStock:   decimal.RequireFromString("150.00"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("10.00"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	seedRepo := repository.NewOTCNegotiationRepository(db)
	if err := seedRepo.UpsertRemoteNeg(buildRemoteNegForTest(
		222, "neg-fallback", offer, string(offerJSON),
		222, "client-7", 111, "client-9",
	)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-1", Status: "initiated"}
	if _, err := h.AcceptNegotiation(ctx, &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-fallback"},
	}); err != nil {
		t.Fatalf("accept: %v", err)
	}

	postings := peerTx.gotReq.GetPostings()
	// Posting 1: seller CREDIT premium — keeps participant id (PERSON) when no
	// nomination is resolvable.
	if postings[1].GetAccountId() != "client-9" || postings[1].GetAccountType() != "PERSON" {
		t.Errorf("posting 1 fallback must keep participant id PERSON, got id=%q type=%q",
			postings[1].GetAccountId(), postings[1].GetAccountType())
	}
}

// TestRecordOptionContract_PersistsNominatedSellerAccount verifies sub-case 2's
// producer side: at accept-COMMIT, the seller bank stores the seller's nominated
// account number on the SELLER-side (DEBIT) remote contract row, so the later
// exercise strike credit (via LookupPeerOptionContract) targets the bound
// account.
func TestRecordOptionContract_PersistsNominatedSellerAccount(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)
	const nominated = "111000000000000777"
	h = h.WithSellerAccountResolver(constResolver{number: nominated})

	// The originating negotiation mirror exists on the seller's bank (created by
	// the inbound bid). RecordOptionContract correlates to it by native id to
	// resolve the seller's nominated account.
	negOffer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 5,
		PricePerStock:   decimal.NewFromInt(100),
		Currency:        "USD",
		Premium:         decimal.NewFromInt(10),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	negJSON, _ := json.Marshal(negOffer)
	seedRepo := repository.NewOTCNegotiationRepository(db)
	if err := seedRepo.UpsertRemoteNeg(buildRemoteNegForTest(
		222, "neg-commit", negOffer, string(negJSON),
		222, "client-7", 111, "client-9",
	)); err != nil {
		t.Fatalf("seed neg: %v", err)
	}

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "neg-commit"},
		Stock:          contractsitx.StockDescription{Ticker: "AAPL"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(100)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         5,
	}
	optJSON, _ := json.Marshal(optDesc)

	resp, err := h.RecordOptionContract(ctx, &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "222:k-commit",
		PostingIndex:          2,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-7"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-9"},
		Direction:             contractsitx.DirectionDebit,
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	var row model.OptionContract
	if err := db.First(&row, resp.GetContractId()).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.RemoteSellerAccountNumber == nil || *row.RemoteSellerAccountNumber != nominated {
		t.Errorf("RemoteSellerAccountNumber = %v, want %q", row.RemoteSellerAccountNumber, nominated)
	}
}

// TestRecordOptionContract_CreditDirection_NoSellerAccountStored verifies the
// BUYER-side (CREDIT) remote contract does NOT store a seller account number
// (the seller's bank, not the buyer's, owns that nomination).
func TestRecordOptionContract_CreditDirection_NoSellerAccountStored(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t)
	ctx := context.Background()

	reserver := &fakeReserver{}
	h.SetHoldingReserver(reserver)
	h = h.WithSellerAccountResolver(constResolver{number: "111000000000000777"})

	optDesc := contractsitx.OptionDescription{
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "neg-credit"},
		Stock:          contractsitx.StockDescription{Ticker: "MSFT"},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: decimal.NewFromInt(50)}, Currency: "USD"},
		SettlementDate: "2026-12-31",
		Amount:         1,
	}
	optJSON, _ := json.Marshal(optDesc)

	resp, err := h.RecordOptionContract(ctx, &stockpb.RecordOptionContractRequest{
		CrossbankTxId:         "222:k-credit",
		PostingIndex:          3,
		BuyerId:               &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-1"},
		SellerId:              &stockpb.PeerForeignBankId{RoutingNumber: 222, Id: "client-9"},
		Direction:             contractsitx.DirectionCredit,
		OptionDescriptionJson: string(optJSON),
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	var row model.OptionContract
	if err := db.First(&row, resp.GetContractId()).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.RemoteSellerAccountNumber != nil && *row.RemoteSellerAccountNumber != "" {
		t.Errorf("CREDIT-side row must not store a seller account number, got %q", *row.RemoteSellerAccountNumber)
	}
}
