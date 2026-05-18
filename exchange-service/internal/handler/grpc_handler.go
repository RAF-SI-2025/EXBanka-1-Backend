package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/service"
)

// exchangeFacade is the narrow interface the handler requires from the service.
// Using an interface here allows test injection without a live DB.
type exchangeFacade interface {
	ListRates() ([]model.ExchangeRate, error)
	GetRate(from, to string) (*model.ExchangeRate, error)
	Calculate(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error)
	Convert(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error)
}

type ExchangeGRPCHandler struct {
	pb.UnimplementedExchangeServiceServer
	svc exchangeFacade
}

func NewExchangeGRPCHandler(svc *service.ExchangeService) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
}

// newExchangeGRPCHandlerForTest constructs a handler from any exchangeFacade
// implementation. Only for use in tests.
func newExchangeGRPCHandlerForTest(svc exchangeFacade) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
}

func (h *ExchangeGRPCHandler) ListRates(ctx context.Context, _ *pb.ListRatesRequest) (*pb.ListRatesResponse, error) {
	rates, err := h.svc.ListRates()
	if err != nil {
		return nil, err
	}
	pbRates := make([]*pb.RateResponse, 0, len(rates))
	for i := range rates {
		pbRates = append(pbRates, rateToProto(&rates[i]))
	}
	return &pb.ListRatesResponse{Rates: pbRates}, nil
}

func (h *ExchangeGRPCHandler) GetRate(ctx context.Context, req *pb.GetRateRequest) (*pb.RateResponse, error) {
	rate, err := h.svc.GetRate(req.GetFromCurrency(), req.GetToCurrency())
	if err != nil {
		return nil, err
	}
	return rateToProto(rate), nil
}

func (h *ExchangeGRPCHandler) Calculate(ctx context.Context, req *pb.CalculateRequest) (*pb.CalculateResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil || amount.IsNegative() || amount.IsZero() {
		return nil, fmt.Errorf("Calculate(amount=%q): %w", req.GetAmount(), service.ErrInvalidAmount)
	}
	if err := validateCurrency(req.GetFromCurrency()); err != nil {
		return nil, fmt.Errorf("Calculate(from=%s): %v: %w", req.GetFromCurrency(), err, service.ErrUnsupportedCurrency)
	}
	if err := validateCurrency(req.GetToCurrency()); err != nil {
		return nil, fmt.Errorf("Calculate(to=%s): %v: %w", req.GetToCurrency(), err, service.ErrUnsupportedCurrency)
	}

	net, commRate, effRate, err := h.svc.Calculate(ctx, req.GetFromCurrency(), req.GetToCurrency(), amount)
	if err != nil {
		return nil, err
	}
	return &pb.CalculateResponse{
		FromCurrency:    req.GetFromCurrency(),
		ToCurrency:      req.GetToCurrency(),
		InputAmount:     amount.StringFixed(4),
		ConvertedAmount: net.StringFixed(4),
		CommissionRate:  commRate.String(),
		EffectiveRate:   effRate.StringFixed(6),
	}, nil
}

func (h *ExchangeGRPCHandler) Convert(ctx context.Context, req *pb.ConvertRequest) (*pb.ConvertResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil || amount.IsNegative() || amount.IsZero() {
		return nil, fmt.Errorf("Convert(amount=%q): %w", req.GetAmount(), service.ErrInvalidAmount)
	}
	if err := validateCurrency(req.GetFromCurrency()); err != nil {
		return nil, fmt.Errorf("Convert(from=%s): %v: %w", req.GetFromCurrency(), err, service.ErrUnsupportedCurrency)
	}
	if err := validateCurrency(req.GetToCurrency()); err != nil {
		return nil, fmt.Errorf("Convert(to=%s): %v: %w", req.GetToCurrency(), err, service.ErrUnsupportedCurrency)
	}
	converted, effRate, err := h.svc.Convert(ctx, req.GetFromCurrency(), req.GetToCurrency(), amount)
	if err != nil {
		return nil, err
	}
	return &pb.ConvertResponse{
		ConvertedAmount: converted.StringFixed(4),
		EffectiveRate:   effRate.StringFixed(6),
	}, nil
}

func rateToProto(r *model.ExchangeRate) *pb.RateResponse {
	return &pb.RateResponse{
		FromCurrency: r.FromCurrency,
		ToCurrency:   r.ToCurrency,
		BuyRate:      r.BuyRate.StringFixed(6),
		SellRate:     r.SellRate.StringFixed(6),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// validateCurrency ensures the code is one of the supported currencies or RSD.
func validateCurrency(code string) error {
	valid := map[string]bool{
		"RSD": true, "EUR": true, "CHF": true, "USD": true,
		"GBP": true, "JPY": true, "CAD": true, "AUD": true,
	}
	if !valid[code] {
		return fmt.Errorf("%q is not a supported currency (supported: RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD)", code)
	}
	return nil
}
