package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	gogrpc "google.golang.org/grpc"

	pb "github.com/exbanka/contract/accountpb"
)

// stubAccountServiceClient captures the request passed to each reservation
// RPC so the wrapper's field mapping can be verified. Only the
// reservation-lifecycle methods are exercised here — the other methods
// return zero values and are only present to satisfy the interface.
type stubAccountServiceClient struct {
	pb.AccountServiceClient

	reserveReq        *pb.ReserveFundsRequest
	reserveResp       *pb.ReserveFundsResponse
	reserveErr        error
	releaseReq        *pb.ReleaseReservationRequest
	partialSettleReq  *pb.PartialSettleReservationRequest
	getReservationReq *pb.GetReservationRequest
	updateBalanceReq  *pb.UpdateBalanceRequest
}

func (s *stubAccountServiceClient) ReserveFunds(_ context.Context, in *pb.ReserveFundsRequest, _ ...gogrpc.CallOption) (*pb.ReserveFundsResponse, error) {
	s.reserveReq = in
	if s.reserveErr != nil {
		return nil, s.reserveErr
	}
	return s.reserveResp, nil
}

func (s *stubAccountServiceClient) ReleaseReservation(_ context.Context, in *pb.ReleaseReservationRequest, _ ...gogrpc.CallOption) (*pb.ReleaseReservationResponse, error) {
	s.releaseReq = in
	return &pb.ReleaseReservationResponse{ReleasedAmount: "0", ReservedBalance: "0"}, nil
}

func (s *stubAccountServiceClient) PartialSettleReservation(_ context.Context, in *pb.PartialSettleReservationRequest, _ ...gogrpc.CallOption) (*pb.PartialSettleReservationResponse, error) {
	s.partialSettleReq = in
	return &pb.PartialSettleReservationResponse{SettledAmount: in.Amount}, nil
}

func (s *stubAccountServiceClient) GetReservation(_ context.Context, in *pb.GetReservationRequest, _ ...gogrpc.CallOption) (*pb.GetReservationResponse, error) {
	s.getReservationReq = in
	return &pb.GetReservationResponse{Exists: true, Status: "active"}, nil
}

func (s *stubAccountServiceClient) UpdateBalance(_ context.Context, in *pb.UpdateBalanceRequest, _ ...gogrpc.CallOption) (*pb.AccountResponse, error) {
	s.updateBalanceReq = in
	return &pb.AccountResponse{AccountNumber: in.AccountNumber}, nil
}

func TestAccountClient_ReserveFunds_MapsFieldsAndReturnsResponse(t *testing.T) {
	stub := &stubAccountServiceClient{
		reserveResp: &pb.ReserveFundsResponse{ReservationId: 42, ReservedBalance: "100.0000", AvailableBalance: "900.0000"},
	}
	c := NewAccountClient(stub)

	resp, err := c.ReserveFunds(context.Background(), 7, 123, decimal.NewFromFloat(100.0), "RSD", "idem-key-rf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stub.reserveReq == nil {
		t.Fatal("ReserveFunds was not invoked on the stub")
	}
	if stub.reserveReq.AccountId != 7 {
		t.Errorf("AccountId: got %d, want 7", stub.reserveReq.AccountId)
	}
	if stub.reserveReq.OrderId != 123 {
		t.Errorf("OrderId: got %d, want 123", stub.reserveReq.OrderId)
	}
	if stub.reserveReq.CurrencyCode != "RSD" {
		t.Errorf("CurrencyCode: got %q, want %q", stub.reserveReq.CurrencyCode, "RSD")
	}
	if stub.reserveReq.Amount != "100" {
		t.Errorf("Amount: got %q, want %q", stub.reserveReq.Amount, "100")
	}
	if resp.ReservationId != 42 {
		t.Errorf("response ReservationId: got %d, want 42", resp.ReservationId)
	}
}

func TestAccountClient_ReserveFunds_PropagatesErrors(t *testing.T) {
	sentinel := errors.New("insufficient funds")
	stub := &stubAccountServiceClient{reserveErr: sentinel}
	c := NewAccountClient(stub)

	_, err := c.ReserveFunds(context.Background(), 1, 2, decimal.NewFromInt(10), "RSD", "idem-key-rf-err")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to be propagated, got %v", err)
	}
}

func TestAccountClient_ReleaseReservation_PassesOrderID(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	if _, err := c.ReleaseReservation(context.Background(), 555, "idem-key-rr"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.releaseReq == nil || stub.releaseReq.OrderId != 555 {
		t.Errorf("ReleaseReservation: stub saw req %+v, want OrderId=555", stub.releaseReq)
	}
}

func TestAccountClient_PartialSettleReservation_MapsFields(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	if _, err := c.PartialSettleReservation(context.Background(), 10, 20, decimal.NewFromFloat(12.5), "fill", "idem-key-ps"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := stub.partialSettleReq
	if req == nil {
		t.Fatal("PartialSettleReservation was not invoked")
	}
	if req.OrderId != 10 || req.OrderTransactionId != 20 {
		t.Errorf("order ids: got OrderId=%d OrderTransactionId=%d, want 10/20", req.OrderId, req.OrderTransactionId)
	}
	if req.Amount != "12.5" {
		t.Errorf("Amount: got %q, want %q", req.Amount, "12.5")
	}
	if req.Memo != "fill" {
		t.Errorf("Memo: got %q, want %q", req.Memo, "fill")
	}
}

func TestAccountClient_GetReservation_PassesOrderID(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	resp, err := c.GetReservation(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.getReservationReq == nil || stub.getReservationReq.OrderId != 999 {
		t.Errorf("GetReservation: stub saw req %+v, want OrderId=999", stub.getReservationReq)
	}
	if !resp.Exists || resp.Status != "active" {
		t.Errorf("passthrough response lost: %+v", resp)
	}
}

func TestAccountClient_CreditAccount_UsesAbsoluteAmountAndUpdatesAvailable(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	// Pass a negative amount to verify the wrapper coerces to absolute so
	// callers can't accidentally debit via CreditAccount.
	if _, err := c.CreditAccount(context.Background(), "265000-42", decimal.NewFromFloat(-25.5), "compensation", "idem-credit-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := stub.updateBalanceReq
	if req == nil {
		t.Fatal("UpdateBalance was not invoked")
	}
	if req.AccountNumber != "265000-42" {
		t.Errorf("AccountNumber: got %q, want %q", req.AccountNumber, "265000-42")
	}
	if req.Amount != "25.5000" {
		t.Errorf("Amount: got %q, want %q (absolute, fixed 4dp)", req.Amount, "25.5000")
	}
	if !req.UpdateAvailable {
		t.Error("UpdateAvailable should always be true for CreditAccount")
	}
	if req.Memo != "compensation" {
		t.Errorf("Memo: got %q, want %q", req.Memo, "compensation")
	}
	if req.IdempotencyKey != "idem-credit-1" {
		t.Errorf("IdempotencyKey: got %q, want %q", req.IdempotencyKey, "idem-credit-1")
	}
}

func TestAccountClient_DebitAccount_ForcesNegativeAmountAndUpdatesAvailable(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	if _, err := c.DebitAccount(context.Background(), "265000-42", decimal.NewFromFloat(25.5), "compensation", "idem-debit-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req := stub.updateBalanceReq
	if req == nil {
		t.Fatal("UpdateBalance was not invoked")
	}
	if req.AccountNumber != "265000-42" {
		t.Errorf("AccountNumber: got %q, want %q", req.AccountNumber, "265000-42")
	}
	if req.Amount != "-25.5000" {
		t.Errorf("Amount: got %q, want %q (negative, fixed 4dp)", req.Amount, "-25.5000")
	}
	if !req.UpdateAvailable {
		t.Error("UpdateAvailable should always be true for DebitAccount")
	}
	if req.Memo != "compensation" {
		t.Errorf("Memo: got %q, want %q", req.Memo, "compensation")
	}
	if req.IdempotencyKey != "idem-debit-1" {
		t.Errorf("IdempotencyKey: got %q, want %q", req.IdempotencyKey, "idem-debit-1")
	}
}

func TestAccountClient_DebitAccount_RejectsNonPositiveAmount(t *testing.T) {
	stub := &stubAccountServiceClient{}
	c := NewAccountClient(stub)

	if _, err := c.DebitAccount(context.Background(), "265000-42", decimal.Zero, "bad", ""); err == nil {
		t.Error("expected error for zero amount")
	}
	if _, err := c.DebitAccount(context.Background(), "265000-42", decimal.NewFromFloat(-1), "bad", ""); err == nil {
		t.Error("expected error for negative amount")
	}
	if stub.updateBalanceReq != nil {
		t.Error("UpdateBalance must not be invoked for invalid amount")
	}
}
