package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/service"
)

type ExchangeGRPCHandler struct {
	pb.UnimplementedExchangeServiceServer
	svc *service.ExchangeService
}

func NewExchangeGRPCHandler(svc *service.ExchangeService) *ExchangeGRPCHandler {
	return &ExchangeGRPCHandler{svc: svc}
}

func (h *ExchangeGRPCHandler) ListRates(ctx context.Context, _ *pb.ListRatesRequest) (*pb.ListRatesResponse, error) {
	rates, err := h.svc.ListRates()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list rates: %v", err)
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "rate not found for %s/%s", req.GetFromCurrency(), req.GetToCurrency())
		}
		return nil, status.Errorf(codes.Internal, "rate lookup failed: %v", err)
	}
	return rateToProto(rate), nil
}

func (h *ExchangeGRPCHandler) Calculate(ctx context.Context, req *pb.CalculateRequest) (*pb.CalculateResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil || amount.IsNegative() || amount.IsZero() {
		return nil, status.Errorf(codes.InvalidArgument, "amount must be a positive number, got %q", req.GetAmount())
	}
	if err := validateCurrency(req.GetFromCurrency()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "from_currency: %v", err)
	}
	if err := validateCurrency(req.GetToCurrency()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "to_currency: %v", err)
	}

	net, commRate, effRate, err := h.svc.Calculate(ctx, req.GetFromCurrency(), req.GetToCurrency(), amount)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "calculate: exchange rate not found: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "calculate: %v", err)
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
		return nil, status.Errorf(codes.InvalidArgument, "amount must be a positive number, got %q", req.GetAmount())
	}
	converted, effRate, err := h.svc.Convert(ctx, req.GetFromCurrency(), req.GetToCurrency(), amount)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "convert: exchange rate not found: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "convert: %v", err)
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
		UpdatedAt:    r.UpdatedAt.String(),
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
