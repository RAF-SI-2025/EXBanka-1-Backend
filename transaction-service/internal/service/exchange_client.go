package service

import (
	"context"
	"fmt"
	"log"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	exchangepb "github.com/exbanka/contract/exchangepb"
)

// ExchangeClientIface abstracts currency conversion for TransferService.
// The production implementation calls the exchange-service gRPC.
type ExchangeClientIface interface {
	// ConvertViaRSD returns (convertedAmount, effectiveRate, error).
	// No commission is applied — the fee service handles that separately.
	ConvertViaRSD(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error)
}

// GRPCExchangeClient wraps exchangepb.ExchangeServiceClient to satisfy ExchangeClientIface.
type GRPCExchangeClient struct {
	client exchangepb.ExchangeServiceClient
}

func NewGRPCExchangeClient(client exchangepb.ExchangeServiceClient) *GRPCExchangeClient {
	return &GRPCExchangeClient{client: client}
}

func (g *GRPCExchangeClient) ConvertViaRSD(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	resp, err := g.client.Convert(ctx, &exchangepb.ConvertRequest{
		FromCurrency: from,
		ToCurrency:   to,
		Amount:       amount.StringFixed(4),
	})
	if err != nil {
		code := status.Code(err)
		if code == codes.NotFound {
			return decimal.Zero, decimal.Zero, fmt.Errorf("exchange rate not available for %s→%s: %w", from, to, err)
		}
		return decimal.Zero, decimal.Zero, fmt.Errorf("exchange service call failed: %w", err)
	}
	converted, err := decimal.NewFromString(resp.GetConvertedAmount())
	if err != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("invalid converted_amount from exchange service: %w", err)
	}
	effRate, err := decimal.NewFromString(resp.GetEffectiveRate())
	if err != nil {
		// Non-fatal: effective rate is stored on the transfer record for display
		// purposes only. Log and zero it rather than failing the conversion.
		log.Printf("WARN: GRPCExchangeClient: invalid effective_rate %q from exchange-service: %v", resp.GetEffectiveRate(), err)
		effRate = decimal.Zero
	}
	return converted, effRate, nil
}
