package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/model"
)

func TestComputeOrderState_PendingDeclinedCancelled(t *testing.T) {
	cases := []struct {
		status, want string
	}{
		{"pending", "pending"},
		{"declined", "declined"},
		{"cancelled", "cancelled"},
	}
	for _, tc := range cases {
		got := computeOrderState(&model.Order{Status: tc.status}, false)
		if got != tc.want {
			t.Errorf("status=%q: want %q, got %q", tc.status, tc.want, got)
		}
	}
}

func TestComputeOrderState_ApprovedFilledFillingApproved(t *testing.T) {
	// approved + IsDone -> filled
	if got := computeOrderState(&model.Order{Status: "approved", IsDone: true}, true); got != "filled" {
		t.Errorf("approved+done: want filled, got %q", got)
	}
	// approved + not done + has fills -> filling
	if got := computeOrderState(&model.Order{Status: "approved"}, true); got != "filling" {
		t.Errorf("approved+filling: want filling, got %q", got)
	}
	// approved + not done + no fills -> approved
	if got := computeOrderState(&model.Order{Status: "approved"}, false); got != "approved" {
		t.Errorf("approved+empty: want approved, got %q", got)
	}
}

func TestComputeOrderState_UnknownFallback(t *testing.T) {
	got := computeOrderState(&model.Order{Status: "weird"}, false)
	if got != "unknown" {
		t.Errorf("unknown status: want 'unknown', got %q", got)
	}
}

func TestToOrderProto_PopulatesAllFields(t *testing.T) {
	hid := uint64(7)
	limit := decimal.NewFromFloat(101.5)
	stop := decimal.NewFromFloat(95.0)
	o := &model.Order{
		ID:                10,
		UserID:            42,
		ListingID:         100,
		HoldingID:         &hid,
		SecurityType:      "stock",
		Ticker:            "AAPL",
		Direction:         "buy",
		OrderType:         "limit",
		Quantity:          5,
		ContractSize:      1,
		PricePerUnit:      decimal.NewFromFloat(180),
		ApproximatePrice:  decimal.NewFromFloat(900),
		Commission:        decimal.NewFromFloat(2.5),
		LimitValue:        &limit,
		StopValue:         &stop,
		Status:            "approved",
		ApprovedBy:        "supervisor",
		IsDone:            false,
		RemainingPortions: 3, // 5 - 3 = 2 filled
		AfterHours:        true,
		AllOrNone:         false,
		Margin:            true,
		AccountID:         55,
		ActingEmployeeID:  17,
		LastModification:  time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		CreatedAt:         time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC),
	}
	p := toOrderProto(o)
	if p.Id != 10 {
		t.Errorf("Id: %d", p.Id)
	}
	if p.HoldingId != 7 {
		t.Errorf("HoldingId: %d", p.HoldingId)
	}
	if p.FilledQuantity != 2 {
		t.Errorf("FilledQuantity want 2 got %d", p.FilledQuantity)
	}
	// has fills 3<5, not done -> "filling"
	if p.State != "filling" {
		t.Errorf("State: want filling, got %q", p.State)
	}
	if p.LimitValue == nil || *p.LimitValue != "101.5000" {
		t.Errorf("LimitValue formatting unexpected: %v", p.LimitValue)
	}
	if p.StopValue == nil || *p.StopValue != "95.0000" {
		t.Errorf("StopValue formatting unexpected: %v", p.StopValue)
	}
	if p.Commission != "2.50" {
		t.Errorf("Commission want 2.50 got %s", p.Commission)
	}
}

func TestToOrderProto_NilHoldingAndPriceFields(t *testing.T) {
	o := &model.Order{
		ID:                1,
		Quantity:          10,
		RemainingPortions: 10,
		Status:            "pending",
		PricePerUnit:      decimal.Zero,
		ApproximatePrice:  decimal.Zero,
		Commission:        decimal.Zero,
		LastModification:  time.Now(),
		CreatedAt:         time.Now(),
	}
	p := toOrderProto(o)
	if p.HoldingId != 0 {
		t.Errorf("expected HoldingId 0, got %d", p.HoldingId)
	}
	if p.LimitValue != nil {
		t.Errorf("expected LimitValue nil, got %v", *p.LimitValue)
	}
	if p.StopValue != nil {
		t.Errorf("expected StopValue nil, got %v", *p.StopValue)
	}
	if p.State != "pending" {
		t.Errorf("expected pending, got %q", p.State)
	}
}

func TestToOrderDetailProto_RefinesStateFromTxnCount(t *testing.T) {
	o := &model.Order{
		Quantity:          10,
		RemainingPortions: 10, // toOrderProto would say "approved"
		Status:            "approved",
		IsDone:            false,
		PricePerUnit:      decimal.Zero,
		ApproximatePrice:  decimal.Zero,
		Commission:        decimal.Zero,
		LastModification:  time.Now(),
		CreatedAt:         time.Now(),
	}
	txns := []model.OrderTransaction{
		{ID: 1, Quantity: 1, PricePerUnit: decimal.NewFromInt(100), TotalPrice: decimal.NewFromInt(100), ExecutedAt: time.Now()},
	}
	d := toOrderDetailProto(o, txns)
	if d.Order.State != "filling" {
		t.Errorf("expected state filling (txn count > 0), got %q", d.Order.State)
	}
	if len(d.Transactions) != 1 {
		t.Fatalf("expected 1 txn, got %d", len(d.Transactions))
	}
	if d.Transactions[0].Id != 1 {
		t.Errorf("txn id mismatch")
	}
}

func TestToOrderListResponse(t *testing.T) {
	orders := []model.Order{
		{ID: 1, Quantity: 5, RemainingPortions: 5, Status: "pending", PricePerUnit: decimal.Zero, ApproximatePrice: decimal.Zero, Commission: decimal.Zero, LastModification: time.Now(), CreatedAt: time.Now()},
		{ID: 2, Quantity: 5, RemainingPortions: 5, Status: "pending", PricePerUnit: decimal.Zero, ApproximatePrice: decimal.Zero, Commission: decimal.Zero, LastModification: time.Now(), CreatedAt: time.Now()},
	}
	resp := toOrderListResponse(orders, 42)
	if resp.TotalCount != 42 {
		t.Errorf("TotalCount: %d", resp.TotalCount)
	}
	if len(resp.Orders) != 2 {
		t.Fatalf("Orders len: %d", len(resp.Orders))
	}
	if resp.Orders[1].Id != 2 {
		t.Errorf("Orders[1].Id: %d", resp.Orders[1].Id)
	}
}

func TestMapOrderError_AllArms(t *testing.T) {
	cases := []struct {
		msg      string
		wantCode codes.Code
	}{
		{"order not found", codes.NotFound},
		{"listing not found", codes.NotFound},
		{"order does not belong to user", codes.PermissionDenied},
		{"order is not pending", codes.FailedPrecondition},
		{"order is already completed", codes.FailedPrecondition},
		{"order is already declined/cancelled", codes.FailedPrecondition},
		{"cannot approve: settlement date has passed", codes.FailedPrecondition},
		{"limit_value required for limit/stop_limit orders", codes.InvalidArgument},
		{"stop_value required for stop/stop_limit orders", codes.InvalidArgument},
		{"some unexpected db failure", codes.Internal},
	}
	for _, tc := range cases {
		err := mapOrderError(errors.New(tc.msg))
		if status.Code(err) != tc.wantCode {
			t.Errorf("msg=%q: want %v, got %v", tc.msg, tc.wantCode, status.Code(err))
		}
	}
}

func TestMapOrderError_PassesThroughGRPCStatus(t *testing.T) {
	original := status.Error(codes.ResourceExhausted, "rate limited")
	err := mapOrderError(original)
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted preserved, got %v", status.Code(err))
	}
}
