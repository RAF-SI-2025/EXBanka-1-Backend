package handler_test

import (
	"context"
	"testing"
	"time"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestInitiateOptionExercise_Expired_Rejected guards the cross-bank
// exercise-after-expiry gap found in the live two-stack adversarial sweep
// (2026-06-05): the buyer's bank let an exercise on an EXPIRED contract proceed
// — it claimed the contract (active -> exercising) and dispatched the SI-TX. The
// seller's bank correctly voted NO (optionExpired) so NO money moved, but the
// buyer-side contract was left stuck in "exercising" (the NO vote is a valid
// protocol outcome, not a transport error, so the claim was never reverted).
//
// The LOCAL exercise path rejects an expired contract up front
// (settlement_date <= today). InitiateOptionExercise must do the same: reject
// with FailedPrecondition BEFORE claiming, leaving the contract "active".
func TestInitiateOptionExercise_Expired_Rejected(t *testing.T) {
	h, db, _, _ := newPeerOtcHandler(t) // ownRouting = 111

	// Buyer-side (CREDIT) remote contract hosted by us (buyer routing 111),
	// seller on peer 222, with a PAST settlement date.
	past := time.Now().UTC().AddDate(-1, 0, 0).Format("2006-01-02")
	row := seedRemoteContractRow(
		222, "tx-expired", 3, contractsitx.DirectionCredit,
		111, "neg-exp",
		111, "client-7", 222, "client-9",
		"AAPL", 1, decimal.NewFromInt(100), "USD", past, "active",
	)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: row.ID,
		BuyerAccountNumber:   "111000000000000001",
	})
	if err == nil {
		t.Fatal("expected expired exercise to be rejected, got nil")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
	// The contract must NOT have been claimed (stays active, not "exercising").
	var got model.OptionContract
	if e := db.First(&got, row.ID).Error; e != nil {
		t.Fatalf("reload: %v", e)
	}
	if got.Status != "active" {
		t.Errorf("status after rejected expired exercise: got %q, want active (no stuck exercising)", got.Status)
	}
}

// TestInitiateOptionExercise_SettlementRolledBack_RevertsClaim: a cross-bank
// exercise whose SI-TX settlement ROLLS BACK (a peer NO vote — a valid protocol
// outcome, not a transport error) must NOT leave the contract stuck in
// "exercising". The claim is reverted to "active" so the buyer can retry.
// Regression for "contract status \"exercising\" is not exercisable".
func TestInitiateOptionExercise_SettlementRolledBack_RevertsClaim(t *testing.T) {
	h, db, peerTx, _ := newPeerOtcHandler(t) // ownRouting = 111
	peerTx.resp = &transactionpb.SiTxInitiateResponse{TransactionId: "tx-rb", Status: "rolled_back"}

	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	row := seedRemoteContractRow(
		222, "tx-exrb", 3, contractsitx.DirectionCredit,
		111, "neg-exrb",
		111, "client-7", 222, "client-9",
		"AAPL", 1, decimal.NewFromInt(100), "USD", future, "active",
	)
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := h.InitiateOptionExercise(context.Background(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: row.ID,
		BuyerAccountNumber:   "111000000000000001",
	})
	if err == nil {
		t.Fatal("a rolled-back exercise settlement must fail the exercise")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
	var got model.OptionContract
	if e := db.First(&got, row.ID).Error; e != nil {
		t.Fatalf("reload: %v", e)
	}
	if got.Status != "active" {
		t.Errorf("status after rolled-back exercise: got %q, want active (claim reverted, not stuck exercising)", got.Status)
	}
}
