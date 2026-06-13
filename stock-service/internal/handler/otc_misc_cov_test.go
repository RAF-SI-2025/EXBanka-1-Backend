package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func TestMintedContractToProto(t *testing.T) {
	if got := mintedContractToProto(nil); got != nil {
		t.Fatal("nil contract should map to nil")
	}
	buyer := uint64(7)
	seller := uint64(8)
	offer := uint64(3)
	c := &model.OptionContract{
		ID: 5, OfferID: &offer,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyer,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &seller,
		Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "RSD", StrikeCurrency: "RSD",
		SettlementDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		BuyerAccountID: 100, SellerAccountID: 200, Status: "active",
		PremiumPaidAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	out := mintedContractToProto(c)
	if out.GetId() != 5 || out.GetOfferId() != 3 {
		t.Fatalf("id/offer mismatch: %+v", out)
	}
	if out.GetBuyerOwnerId() != 7 || out.GetSellerOwnerId() != 8 {
		t.Fatalf("owner ids mismatch: buyer=%d seller=%d", out.GetBuyerOwnerId(), out.GetSellerOwnerId())
	}
	if out.GetTicker() != "AAPL" || out.GetStatus() != "active" {
		t.Fatalf("ticker/status mismatch: %+v", out)
	}
}

func TestExerciseContract_FundValidationErrors(t *testing.T) {
	fx := newOTCOptionsHandlerFixture(t)
	ctx := context.Background()

	// fund support not wired → FailedPrecondition.
	_, err := fx.h.ExerciseContract(ctx, &stockpb.ExerciseContractRequest{
		ContractId: 1, OnBehalfOfFundId: 5, ActorUserId: 1, ActorSystemType: "employee",
	})
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)

	// Wire a real fund repo + seed a fund managed by employee 42.
	if err := fx.db.AutoMigrate(&model.InvestmentFund{}); err != nil {
		t.Fatalf("migrate funds: %v", err)
	}
	if err := fx.db.Create(&model.InvestmentFund{
		ID: 9, Name: "F", ManagerEmployeeID: 42, RSDAccountID: 1, Active: true,
		FundType: model.FundTypeOpen, FundStatus: model.FundStatusOpen,
	}).Error; err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	hf := fx.h.WithFundRepo(repository.NewFundRepository(fx.db))

	// Unknown fund → NotFound.
	_, err = hf.ExerciseContract(ctx, &stockpb.ExerciseContractRequest{
		ContractId: 1, OnBehalfOfFundId: 999, ActorUserId: 42, ActorSystemType: "employee",
	})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Non-employee actor on a fund exercise → PermissionDenied.
	_, err = hf.ExerciseContract(ctx, &stockpb.ExerciseContractRequest{
		ContractId: 1, OnBehalfOfFundId: 9, ActorUserId: 42, ActorSystemType: "client",
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)

	// Employee who is not the manager → PermissionDenied.
	_, err = hf.ExerciseContract(ctx, &stockpb.ExerciseContractRequest{
		ContractId: 1, OnBehalfOfFundId: 9, ActorUserId: 7, ActorSystemType: "employee",
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}
