package handler_test

import (
	"context"
	"encoding/json"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestInbound_AcceptNegotiation_SettlementRolledBack_RevertsAndErrors is the
// settlement-rollback safety regression. When the SI-TX settlement rolls back
// (a bank voted NO — e.g. the seller has no account in the premium currency and
// no FX is possible), InitiateOutboundTxWithPostings now reports status
// "rolled_back". The inbound accept must treat that as failure: revert the
// acceptance claim (accepted → ongoing) and return an error — NOT report
// "accepted" / consume the listing. Previously the response was always "pending",
// so the accept reported success with no contract (the user's "listing deleted,
// no contract, money error").
func TestInbound_AcceptNegotiation_SettlementRolledBack_RevertsAndErrors(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting 111
	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-rb", Status: "rolled_back"}

	// WE (111) last proposed → peer (222) accepting is the legit counterparty, so
	// the accept reaches the dispatch (only the SETTLEMENT fails, not the guards).
	offer := contractsitx.OtcOffer{
		Ticker: "AAPL", Amount: 10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-12-31",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 111, ID: "client-9"},
	}
	offerJSON, _ := json.Marshal(offer)
	repo := repository.NewOTCNegotiationRepository(db)
	row := buildRemoteNegForTest(222, "neg-rb", offer, string(offerJSON),
		222, "client-7", 111, "client-9")
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  "222",
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-rb"},
	})
	if err == nil {
		t.Fatal("a rolled-back settlement must fail the accept (no contract formed)")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("want FailedPrecondition on a rolled-back settlement, got %v", status.Code(err))
	}
	// The dispatch WAS attempted (the settlement is what failed, not a guard).
	if peerTx.gotReq == nil {
		t.Error("expected a settlement dispatch before the rollback")
	}
	// The claim must be reverted so the chain can be re-accepted (not stuck accepted).
	after, gerr := repo.GetRemoteNegByRoutingAndNative(222, "neg-rb")
	if gerr != nil {
		t.Fatalf("re-read: %v", gerr)
	}
	if after.Status != "ongoing" {
		t.Errorf("status after rolled-back accept: got %q, want ongoing (claim reverted)", after.Status)
	}
}
