package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userpb "github.com/exbanka/contract/userpb"
)

// stubActuaryServiceClient captures requests made through the ActuaryClient
// wrapper so the tests can verify field mapping / sign handling without
// running a real user-service.
type stubActuaryServiceClient struct {
	userpb.ActuaryServiceClient

	getReq    *userpb.GetActuaryInfoRequest
	getResp   *userpb.ActuaryInfo
	getErr    error
	updateReq *userpb.UpdateUsedLimitRequest
	updateErr error
}

func (s *stubActuaryServiceClient) GetActuaryInfo(_ context.Context, in *userpb.GetActuaryInfoRequest, _ ...gogrpc.CallOption) (*userpb.ActuaryInfo, error) {
	s.getReq = in
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getResp, nil
}

func (s *stubActuaryServiceClient) UpdateUsedLimit(_ context.Context, in *userpb.UpdateUsedLimitRequest, _ ...gogrpc.CallOption) (*userpb.ActuaryInfo, error) {
	s.updateReq = in
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return &userpb.ActuaryInfo{}, nil
}

func TestActuaryLimitInfo_Remaining_Positive(t *testing.T) {
	info := ActuaryLimitInfo{Limit: decimal.NewFromInt(100), UsedLimit: decimal.NewFromInt(30)}
	if got := info.Remaining(); !got.Equal(decimal.NewFromInt(70)) {
		t.Errorf("got %s want 70", got)
	}
}

func TestActuaryLimitInfo_Remaining_NegativeClampedToZero(t *testing.T) {
	info := ActuaryLimitInfo{Limit: decimal.NewFromInt(50), UsedLimit: decimal.NewFromInt(75)}
	if got := info.Remaining(); !got.IsZero() {
		t.Errorf("got %s want 0", got)
	}
}

func TestActuaryLimitInfo_Remaining_Zero(t *testing.T) {
	info := ActuaryLimitInfo{Limit: decimal.NewFromInt(50), UsedLimit: decimal.NewFromInt(50)}
	if got := info.Remaining(); !got.IsZero() {
		t.Errorf("got %s want 0", got)
	}
}

func TestActuaryClient_GetActuaryLimit_Success(t *testing.T) {
	stub := &stubActuaryServiceClient{
		getResp: &userpb.ActuaryInfo{
			Id:           7,
			EmployeeId:   42,
			Limit:        "1000",
			UsedLimit:    "250.5",
			NeedApproval: true,
		},
	}
	c := NewActuaryClient(stub)
	got, err := c.GetActuaryLimit(context.Background(), 42)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if stub.getReq.EmployeeId != 42 {
		t.Errorf("emp_id got %d", stub.getReq.EmployeeId)
	}
	if got.ID != 7 || got.EmployeeID != 42 {
		t.Errorf("ids: %+v", got)
	}
	if !got.Limit.Equal(decimal.NewFromInt(1000)) {
		t.Errorf("limit: %s", got.Limit)
	}
	if !got.UsedLimit.Equal(decimal.NewFromFloat(250.5)) {
		t.Errorf("used: %s", got.UsedLimit)
	}
	if !got.NeedApproval {
		t.Error("expected NeedApproval=true")
	}
}

func TestActuaryClient_GetActuaryLimit_NotFoundCodes(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.FailedPrecondition, codes.InvalidArgument} {
		stub := &stubActuaryServiceClient{getErr: status.Error(code, "no actuary")}
		c := NewActuaryClient(stub)
		_, err := c.GetActuaryLimit(context.Background(), 1)
		if !errors.Is(err, ErrActuaryNotFound) {
			t.Errorf("code %s: expected ErrActuaryNotFound, got %v", code, err)
		}
	}
}

func TestActuaryClient_GetActuaryLimit_OtherErrorPropagates(t *testing.T) {
	stub := &stubActuaryServiceClient{getErr: status.Error(codes.Internal, "boom")}
	c := NewActuaryClient(stub)
	_, err := c.GetActuaryLimit(context.Background(), 1)
	if err == nil || errors.Is(err, ErrActuaryNotFound) {
		t.Errorf("expected non-ErrActuaryNotFound error, got %v", err)
	}
}

func TestActuaryClient_GetActuaryLimit_NonStatusErrorPropagates(t *testing.T) {
	sentinel := errors.New("net dial fail")
	stub := &stubActuaryServiceClient{getErr: sentinel}
	c := NewActuaryClient(stub)
	_, err := c.GetActuaryLimit(context.Background(), 1)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel: got %v", err)
	}
}

func TestActuaryClient_IncrementUsedLimit_PositiveAmount(t *testing.T) {
	stub := &stubActuaryServiceClient{}
	c := NewActuaryClient(stub)
	if err := c.IncrementUsedLimit(context.Background(), 99, decimal.NewFromInt(123)); err != nil {
		t.Fatalf("err: %v", err)
	}
	req := stub.updateReq
	if req == nil {
		t.Fatal("UpdateUsedLimit not called")
	}
	if req.Id != 99 || req.Amount != "123" || req.Currency != "RSD" {
		t.Errorf("req mismatch: %+v", req)
	}
}

func TestActuaryClient_IncrementUsedLimit_RejectsNonPositive(t *testing.T) {
	stub := &stubActuaryServiceClient{}
	c := NewActuaryClient(stub)
	if err := c.IncrementUsedLimit(context.Background(), 1, decimal.Zero); err == nil {
		t.Error("expected error for zero amount")
	}
	if err := c.IncrementUsedLimit(context.Background(), 1, decimal.NewFromInt(-1)); err == nil {
		t.Error("expected error for negative amount")
	}
	if stub.updateReq != nil {
		t.Error("UpdateUsedLimit must not be called")
	}
}

func TestActuaryClient_IncrementUsedLimit_PropagatesErr(t *testing.T) {
	sentinel := errors.New("svc down")
	stub := &stubActuaryServiceClient{updateErr: sentinel}
	c := NewActuaryClient(stub)
	err := c.IncrementUsedLimit(context.Background(), 1, decimal.NewFromInt(1))
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel: got %v", err)
	}
}

func TestActuaryClient_DecrementUsedLimit_PositiveBecomesNegative(t *testing.T) {
	stub := &stubActuaryServiceClient{}
	c := NewActuaryClient(stub)
	if err := c.DecrementUsedLimit(context.Background(), 99, decimal.NewFromInt(50)); err != nil {
		t.Fatalf("err: %v", err)
	}
	req := stub.updateReq
	if req == nil {
		t.Fatal("UpdateUsedLimit not called")
	}
	if req.Amount != "-50" {
		t.Errorf("got amount %q want -50", req.Amount)
	}
}

func TestActuaryClient_DecrementUsedLimit_RejectsNonPositive(t *testing.T) {
	stub := &stubActuaryServiceClient{}
	c := NewActuaryClient(stub)
	if err := c.DecrementUsedLimit(context.Background(), 1, decimal.Zero); err == nil {
		t.Error("expected error for zero amount")
	}
	if err := c.DecrementUsedLimit(context.Background(), 1, decimal.NewFromInt(-1)); err == nil {
		t.Error("expected error for negative amount")
	}
	if stub.updateReq != nil {
		t.Error("UpdateUsedLimit must not be called")
	}
}

func TestActuaryClient_Stub_ReturnsUnderlying(t *testing.T) {
	stub := &stubActuaryServiceClient{}
	c := NewActuaryClient(stub)
	got := c.Stub()
	if got == nil {
		t.Fatal("Stub() returned nil")
	}
	if _, ok := got.(*stubActuaryServiceClient); !ok {
		t.Errorf("Stub() returned wrong type %T", got)
	}
}

func TestAccountClient_Stub_ReturnsUnderlying(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)
	got := c.Stub()
	if got == nil {
		t.Fatal("Stub() returned nil")
	}
	if _, ok := got.(*stubAccountServiceClient); !ok {
		t.Errorf("Stub() returned wrong type %T", got)
	}
}
