package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// ---------------------------------------------------------------------------
// Interface covering only handler-called OTC surface
// ---------------------------------------------------------------------------

type otcSvcFacade interface {
	ListOffers(filter service.OTCFilter) ([]model.Holding, int64, error)
	BuyOffer(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error)
}

// ---------------------------------------------------------------------------
// Mock
// ---------------------------------------------------------------------------

type mockOTCSvc struct {
	listFn func(filter service.OTCFilter) ([]model.Holding, int64, error)
	buyFn  func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error)
}

func (m *mockOTCSvc) ListOffers(filter service.OTCFilter) ([]model.Holding, int64, error) {
	if m.listFn != nil {
		return m.listFn(filter)
	}
	return nil, 0, nil
}

func (m *mockOTCSvc) BuyOffer(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
	if m.buyFn != nil {
		return m.buyFn(offerID, buyerID, buyerSystemType, quantity, buyerAccountID)
	}
	return &service.OTCBuyResult{
		ID:           1,
		OfferID:      offerID,
		Quantity:     quantity,
		PricePerUnit: decimal.NewFromFloat(10),
		TotalPrice:   decimal.NewFromFloat(10).Mul(decimal.NewFromInt(quantity)),
		Commission:   decimal.NewFromFloat(0.5),
	}, nil
}

// ---------------------------------------------------------------------------
// Testable OTCHandler variant that accepts the interface
// ---------------------------------------------------------------------------

type testOTCHandler struct {
	pb.UnimplementedOTCGRPCServiceServer
	otcSvc otcSvcFacade
}

func newOTCHandlerForTest(svc otcSvcFacade) *testOTCHandler {
	return &testOTCHandler{otcSvc: svc}
}

func (h *testOTCHandler) ListOffers(ctx context.Context, req *pb.ListOTCOffersRequest) (*pb.ListOTCOffersResponse, error) {
	filter := service.OTCFilter{
		SecurityType: req.SecurityType,
		Ticker:       req.Ticker,
		Page:         int(req.Page),
		PageSize:     int(req.PageSize),
	}
	holdings, total, err := h.otcSvc.ListOffers(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	offers := make([]*pb.OTCOffer, len(holdings))
	for i, hld := range holdings {
		offers[i] = &pb.OTCOffer{
			Id:           hld.ID,
			SellerId:     hld.UserID,
			SellerName:   hld.UserFirstName + " " + hld.UserLastName,
			SecurityType: hld.SecurityType,
			Ticker:       hld.Ticker,
			Name:         hld.Name,
			Quantity:     hld.PublicQuantity,
			PricePerUnit: hld.AveragePrice.StringFixed(2),
			CreatedAt:    hld.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return &pb.ListOTCOffersResponse{Offers: offers, TotalCount: total}, nil
}

func (h *testOTCHandler) BuyOffer(ctx context.Context, req *pb.BuyOTCOfferRequest) (*pb.OTCTransaction, error) {
	effectiveBuyerID, effectiveSystemType, rerr := resolveOrderOwner(req.BuyerId, req.SystemType, req.ActingEmployeeId, req.OnBehalfOfClientId)
	if rerr != nil {
		return nil, rerr
	}
	result, err := h.otcSvc.BuyOffer(
		req.OfferId,
		effectiveBuyerID,
		effectiveSystemType,
		req.Quantity,
		req.AccountId,
	)
	if err != nil {
		return nil, mapOTCError(err)
	}
	return &pb.OTCTransaction{
		Id:           result.ID,
		OfferId:      result.OfferID,
		Quantity:     result.Quantity,
		PricePerUnit: result.PricePerUnit.StringFixed(2),
		TotalPrice:   result.TotalPrice.StringFixed(2),
		Commission:   result.Commission.StringFixed(2),
	}, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestOTCHandler_ListOffers_Success(t *testing.T) {
	now := time.Now()
	svc := &mockOTCSvc{
		listFn: func(filter service.OTCFilter) ([]model.Holding, int64, error) {
			return []model.Holding{
				{
					ID:             1,
					UserID:         10,
					UserFirstName:  "Alice",
					UserLastName:   "Smith",
					SecurityType:   "stock",
					Ticker:         "AAPL",
					Name:           "Apple Inc.",
					PublicQuantity: 50,
					AveragePrice:   decimal.NewFromFloat(150),
					CreatedAt:      now,
				},
			}, 1, nil
		},
	}
	h := newOTCHandlerForTest(svc)
	resp, err := h.ListOffers(context.Background(), &pb.ListOTCOffersRequest{
		SecurityType: "stock",
		Page:         1,
		PageSize:     10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Errorf("expected TotalCount=1, got %d", resp.TotalCount)
	}
	if len(resp.Offers) != 1 {
		t.Fatalf("expected 1 offer, got %d", len(resp.Offers))
	}
	offer := resp.Offers[0]
	if offer.Ticker != "AAPL" {
		t.Errorf("expected ticker AAPL, got %q", offer.Ticker)
	}
	if offer.SellerName != "Alice Smith" {
		t.Errorf("expected SellerName 'Alice Smith', got %q", offer.SellerName)
	}
	if offer.PricePerUnit != "150.00" {
		t.Errorf("expected PricePerUnit '150.00', got %q", offer.PricePerUnit)
	}
}

func TestOTCHandler_ListOffers_Empty(t *testing.T) {
	svc := &mockOTCSvc{
		listFn: func(filter service.OTCFilter) ([]model.Holding, int64, error) {
			return nil, 0, nil
		},
	}
	h := newOTCHandlerForTest(svc)
	resp, err := h.ListOffers(context.Background(), &pb.ListOTCOffersRequest{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TotalCount != 0 {
		t.Errorf("expected TotalCount=0, got %d", resp.TotalCount)
	}
	if len(resp.Offers) != 0 {
		t.Errorf("expected 0 offers, got %d", len(resp.Offers))
	}
}

func TestOTCHandler_ListOffers_Error(t *testing.T) {
	svc := &mockOTCSvc{
		listFn: func(filter service.OTCFilter) ([]model.Holding, int64, error) {
			return nil, 0, errors.New("db failure")
		},
	}
	h := newOTCHandlerForTest(svc)
	_, err := h.ListOffers(context.Background(), &pb.ListOTCOffersRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestOTCHandler_BuyOffer_Success(t *testing.T) {
	svc := &mockOTCSvc{
		buyFn: func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
			return &service.OTCBuyResult{
				ID:           55,
				OfferID:      offerID,
				Quantity:     quantity,
				PricePerUnit: decimal.NewFromFloat(200),
				TotalPrice:   decimal.NewFromFloat(200).Mul(decimal.NewFromInt(quantity)),
				Commission:   decimal.NewFromFloat(1),
			}, nil
		},
	}
	h := newOTCHandlerForTest(svc)
	resp, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:    7,
		BuyerId:    42,
		SystemType: "client",
		Quantity:   3,
		AccountId:  100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Id != 55 {
		t.Errorf("expected Id=55, got %d", resp.Id)
	}
	if resp.OfferId != 7 {
		t.Errorf("expected OfferId=7, got %d", resp.OfferId)
	}
	if resp.Quantity != 3 {
		t.Errorf("expected Quantity=3, got %d", resp.Quantity)
	}
	if resp.PricePerUnit != "200.00" {
		t.Errorf("expected PricePerUnit='200.00', got %q", resp.PricePerUnit)
	}
	if resp.Commission != "1.00" {
		t.Errorf("expected Commission='1.00', got %q", resp.Commission)
	}
}

func TestOTCHandler_BuyOffer_NotFound(t *testing.T) {
	svc := &mockOTCSvc{
		buyFn: func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
			return nil, errors.New("OTC offer not found")
		},
	}
	h := newOTCHandlerForTest(svc)
	_, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:    99,
		BuyerId:    1,
		SystemType: "client",
		Quantity:   1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

func TestOTCHandler_BuyOffer_OwnOffer(t *testing.T) {
	svc := &mockOTCSvc{
		buyFn: func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
			return nil, errors.New("cannot buy your own OTC offer")
		},
	}
	h := newOTCHandlerForTest(svc)
	_, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:    1,
		BuyerId:    10,
		SystemType: "client",
		Quantity:   2,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", status.Code(err))
	}
}

func TestOTCHandler_BuyOffer_InsufficientQuantity(t *testing.T) {
	svc := &mockOTCSvc{
		buyFn: func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
			return nil, errors.New("insufficient public quantity for OTC purchase")
		},
	}
	h := newOTCHandlerForTest(svc)
	_, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:    1,
		BuyerId:    5,
		SystemType: "client",
		Quantity:   1000,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", status.Code(err))
	}
}

func TestOTCHandler_BuyOffer_EmployeeOnBehalfOfClient(t *testing.T) {
	var capturedBuyerID uint64
	var capturedSystemType string
	svc := &mockOTCSvc{
		buyFn: func(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error) {
			capturedBuyerID = buyerID
			capturedSystemType = buyerSystemType
			return &service.OTCBuyResult{
				ID:           1,
				OfferID:      offerID,
				Quantity:     quantity,
				PricePerUnit: decimal.NewFromFloat(10),
				TotalPrice:   decimal.NewFromFloat(10),
				Commission:   decimal.Zero,
			}, nil
		},
	}
	h := newOTCHandlerForTest(svc)
	_, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:            1,
		BuyerId:            999, // junk — should be ignored
		SystemType:         "employee",
		ActingEmployeeId:   7,
		OnBehalfOfClientId: 42,
		Quantity:           1,
		AccountId:          100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedBuyerID != 42 {
		t.Errorf("expected buyerID=42 (client), got %d", capturedBuyerID)
	}
	if capturedSystemType != "client" {
		t.Errorf("expected systemType=client, got %q", capturedSystemType)
	}
}

func TestOTCHandler_BuyOffer_MissingClientForActingEmployee(t *testing.T) {
	h := newOTCHandlerForTest(&mockOTCSvc{})
	_, err := h.BuyOffer(context.Background(), &pb.BuyOTCOfferRequest{
		OfferId:          1,
		BuyerId:          1,
		SystemType:       "employee",
		ActingEmployeeId: 7,
		// OnBehalfOfClientId = 0 intentionally missing
		Quantity: 1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}
