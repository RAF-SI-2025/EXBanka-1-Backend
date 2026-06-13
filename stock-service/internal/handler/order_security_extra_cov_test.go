package handler

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func TestOrderHandler_CreateOrder_BadLimitAndStop(t *testing.T) {
	h := newOrderHandlerForTest(&mockOrderSvc{}, &mockExecEngine{})
	ctx := context.Background()

	bad := "abc"
	_, err := h.CreateOrder(ctx, &pb.CreateOrderRequest{UserId: 1, SystemType: "client", Direction: "buy", OrderType: "limit", LimitValue: &bad})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)

	_, err = h.CreateOrder(ctx, &pb.CreateOrderRequest{UserId: 1, SystemType: "client", Direction: "buy", OrderType: "stop", StopValue: &bad})
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestOrderHandler_CreateOrder_ForwardsOptionalFields(t *testing.T) {
	var captured service.CreateOrderRequest
	svc := &mockOrderSvc{
		createFn: func(_ context.Context, req service.CreateOrderRequest) (*model.Order, error) {
			captured = req
			ot, oid := model.OwnerFromLegacy(req.UserID, req.SystemType)
			return &model.Order{ID: 7, OwnerType: ot, OwnerID: oid, Status: "pending"}, nil
		},
	}
	h := newOrderHandlerForTest(svc, &mockExecEngine{})

	limit := "12.5"
	stop := "11.0"
	base := uint64(555)
	_, err := h.CreateOrder(context.Background(), &pb.CreateOrderRequest{
		UserId: 1, SystemType: "client", Direction: "buy", OrderType: "limit",
		Quantity: 3, HoldingId: 88, LimitValue: &limit, StopValue: &stop, BaseAccountId: &base,
	})
	testutil.RequireNoGRPCError(t, err)
	if captured.LimitValue == nil || captured.LimitValue.String() != "12.5" {
		t.Fatalf("limit not forwarded: %+v", captured.LimitValue)
	}
	if captured.StopValue == nil || captured.StopValue.String() != "11" {
		t.Fatalf("stop not forwarded: %+v", captured.StopValue)
	}
	if captured.HoldingID == nil || *captured.HoldingID != 88 {
		t.Fatalf("holding id not forwarded: %+v", captured.HoldingID)
	}
	if captured.BaseAccountID == nil || *captured.BaseAccountID != 555 {
		t.Fatalf("base account not forwarded: %+v", captured.BaseAccountID)
	}
}

func TestSecurityHandler_ListFutures_PriceVolumeFilters(t *testing.T) {
	var captured repository.FuturesFilter
	sec := &mockSecuritySvc{
		listFuturesFn: func(filter repository.FuturesFilter) ([]model.FuturesContract, int64, error) {
			captured = filter
			return []model.FuturesContract{{ID: 1, Ticker: "ESF24"}}, 1, nil
		},
	}
	h := newSecurityHandlerForTest(sec, &mockListingSvc{}, &mockCandleSvc{}, &mockListingRepo{})

	resp, err := h.ListFutures(context.Background(), &pb.ListFuturesRequest{
		MinPrice: "10.5", MaxPrice: "99.9", MinVolume: 100, MaxVolume: 5000,
	})
	testutil.RequireNoGRPCError(t, err)
	if resp.GetTotalCount() != 1 {
		t.Fatalf("want total 1, got %d", resp.GetTotalCount())
	}
	if captured.MinPrice == nil || captured.MaxPrice == nil {
		t.Fatalf("price filters not populated: %+v / %+v", captured.MinPrice, captured.MaxPrice)
	}
	if captured.MinVolume == nil || *captured.MinVolume != 100 {
		t.Fatalf("min volume not populated: %+v", captured.MinVolume)
	}
	if captured.MaxVolume == nil || *captured.MaxVolume != 5000 {
		t.Fatalf("max volume not populated: %+v", captured.MaxVolume)
	}
}
