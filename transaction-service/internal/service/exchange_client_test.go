package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	exchangepb "github.com/exbanka/contract/exchangepb"
)

// stubExchangeServiceClient satisfies exchangepb.ExchangeServiceClient just
// enough for the GRPCExchangeClient wrapper tests.
type stubExchangeServiceClient struct {
	convertResp *exchangepb.ConvertResponse
	convertErr  error
}

func (s *stubExchangeServiceClient) ListRates(_ context.Context, _ *exchangepb.ListRatesRequest, _ ...grpc.CallOption) (*exchangepb.ListRatesResponse, error) {
	return nil, nil
}
func (s *stubExchangeServiceClient) GetRate(_ context.Context, _ *exchangepb.GetRateRequest, _ ...grpc.CallOption) (*exchangepb.RateResponse, error) {
	return nil, nil
}
func (s *stubExchangeServiceClient) Calculate(_ context.Context, _ *exchangepb.CalculateRequest, _ ...grpc.CallOption) (*exchangepb.CalculateResponse, error) {
	return nil, nil
}
func (s *stubExchangeServiceClient) Convert(_ context.Context, _ *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	return s.convertResp, s.convertErr
}

// TestGRPCExchangeClient_HappyPath verifies the happy path parses the
// ConvertResponse into typed decimals.
func TestGRPCExchangeClient_HappyPath(t *testing.T) {
	stub := &stubExchangeServiceClient{
		convertResp: &exchangepb.ConvertResponse{
			ConvertedAmount: "82.50",
			EffectiveRate:   "0.825",
		},
	}
	c := NewGRPCExchangeClient(stub)
	conv, rate, err := c.ConvertViaRSD(context.Background(), "RSD", "EUR", decimal.NewFromInt(100))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !conv.Equal(decimal.NewFromFloat(82.5)) {
		t.Errorf("converted: %s", conv.String())
	}
	if !rate.Equal(decimal.NewFromFloat(0.825)) {
		t.Errorf("rate: %s", rate.String())
	}
}

// TestGRPCExchangeClient_NotFoundMapped verifies the NotFound gRPC code maps
// to a "rate not available" error.
func TestGRPCExchangeClient_NotFoundMapped(t *testing.T) {
	stub := &stubExchangeServiceClient{convertErr: status.Error(codes.NotFound, "no rate")}
	c := NewGRPCExchangeClient(stub)
	_, _, err := c.ConvertViaRSD(context.Background(), "RSD", "JPY", decimal.NewFromInt(100))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errContains(err, "exchange rate not available") {
		t.Errorf("expected 'exchange rate not available', got %v", err)
	}
}

// TestGRPCExchangeClient_InternalErrorMapped verifies non-NotFound errors
// surface as a generic call-failed error.
func TestGRPCExchangeClient_InternalErrorMapped(t *testing.T) {
	stub := &stubExchangeServiceClient{convertErr: errors.New("backend down")}
	c := NewGRPCExchangeClient(stub)
	_, _, err := c.ConvertViaRSD(context.Background(), "RSD", "USD", decimal.NewFromInt(100))
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errContains(err, "exchange service call failed") {
		t.Errorf("expected 'exchange service call failed', got %v", err)
	}
}

// TestGRPCExchangeClient_BadConvertedAmount verifies an unparseable
// converted_amount surfaces as a decode error.
func TestGRPCExchangeClient_BadConvertedAmount(t *testing.T) {
	stub := &stubExchangeServiceClient{
		convertResp: &exchangepb.ConvertResponse{ConvertedAmount: "garbage", EffectiveRate: "1"},
	}
	c := NewGRPCExchangeClient(stub)
	_, _, err := c.ConvertViaRSD(context.Background(), "RSD", "EUR", decimal.NewFromInt(100))
	if err == nil {
		t.Fatalf("expected error on bad converted_amount")
	}
}

// TestGRPCExchangeClient_BadEffectiveRate_NonFatal verifies that a bad
// effective_rate is non-fatal (logged, zeroed).
func TestGRPCExchangeClient_BadEffectiveRate_NonFatal(t *testing.T) {
	stub := &stubExchangeServiceClient{
		convertResp: &exchangepb.ConvertResponse{ConvertedAmount: "100", EffectiveRate: "not-a-number"},
	}
	c := NewGRPCExchangeClient(stub)
	conv, rate, err := c.ConvertViaRSD(context.Background(), "RSD", "EUR", decimal.NewFromInt(100))
	if err != nil {
		t.Fatalf("expected non-fatal: %v", err)
	}
	if !conv.Equal(decimal.NewFromInt(100)) {
		t.Errorf("conv: %s", conv.String())
	}
	if !rate.Equal(decimal.Zero) {
		t.Errorf("rate should be zeroed on parse failure, got %s", rate.String())
	}
}

func errContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
