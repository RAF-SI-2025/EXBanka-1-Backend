package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func TestCounterNegotiation_ValidationErrors(t *testing.T) {
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(&service.OTCNegotiationService{})
	ctx := context.Background()

	// Invalid caller owner type.
	_, err := h.CounterNegotiation(ctx, &stockpb.CounterNegotiationRequest{CallerOwnerType: "nope"})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Non-bank caller with id 0.
	_, err = h.CounterNegotiation(ctx, &stockpb.CounterNegotiationRequest{CallerOwnerType: "client", CallerOwnerId: 0})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	base := func() *stockpb.CounterNegotiationRequest {
		return &stockpb.CounterNegotiationRequest{
			NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7,
			Quantity: "10", StrikePrice: "150", Premium: "20", SettlementDate: "2030-01-01",
		}
	}

	// Bad quantity.
	r := base()
	r.Quantity = "abc"
	_, err = h.CounterNegotiation(ctx, r)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Bad strike.
	r = base()
	r.StrikePrice = "abc"
	_, err = h.CounterNegotiation(ctx, r)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Bad premium.
	r = base()
	r.Premium = "abc"
	_, err = h.CounterNegotiation(ctx, r)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	// Bad settlement date.
	r = base()
	r.SettlementDate = "not-a-date"
	_, err = h.CounterNegotiation(ctx, r)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestAcceptNegotiationChain_FundValidationErrors(t *testing.T) {
	ctx := context.Background()

	// fund support not wired → FailedPrecondition.
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(&service.OTCNegotiationService{})
	_, err := h.AcceptNegotiationChain(ctx, &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7, OnBehalfOfFundId: 5,
	})
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)

	// Wire a fund repo + seed a fund managed by employee 42 with RSD account 1.
	db := testutil.SetupTestDB(t, &model.InvestmentFund{})
	if err := db.Create(&model.InvestmentFund{
		ID: 9, Name: "F", ManagerEmployeeID: 42, RSDAccountID: 1, Active: true,
		FundType: model.FundTypeOpen, FundStatus: model.FundStatusOpen,
	}).Error; err != nil {
		t.Fatalf("seed fund: %v", err)
	}
	hf := NewOTCOptionsHandler(nil, nil).
		WithNegotiations(&service.OTCNegotiationService{}).
		WithFundRepo(repository.NewFundRepository(db))

	// Unknown fund → NotFound.
	_, err = hf.AcceptNegotiationChain(ctx, &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7, OnBehalfOfFundId: 999, ActingEmployeeId: 42,
	})
	testutil.RequireGRPCCode(t, err, codes.NotFound)

	// Missing acting_employee_id → PermissionDenied.
	_, err = hf.AcceptNegotiationChain(ctx, &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7, OnBehalfOfFundId: 9,
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)

	// Non-manager employee → PermissionDenied.
	_, err = hf.AcceptNegotiationChain(ctx, &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7, OnBehalfOfFundId: 9, ActingEmployeeId: 7,
	})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)

	// Acceptor account != fund RSD account → InvalidArgument.
	_, err = hf.AcceptNegotiationChain(ctx, &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId: 1, CallerOwnerType: "client", CallerOwnerId: 7, OnBehalfOfFundId: 9,
		ActingEmployeeId: 42, AcceptorAccountId: 999,
	})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}
