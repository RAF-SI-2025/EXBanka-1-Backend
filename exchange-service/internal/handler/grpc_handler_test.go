package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/exchange-service/internal/model"
	"github.com/exbanka/exchange-service/internal/service"
)

// ---------------------------------------------------------------------------
// Hand-written mock
// ---------------------------------------------------------------------------

type mockExchangeFacade struct {
	listRatesFn func() ([]model.ExchangeRate, error)
	getRateFn   func(from, to string) (*model.ExchangeRate, error)
	calculateFn func(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error)
	convertFn   func(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error)
}

func (m *mockExchangeFacade) ListRates() ([]model.ExchangeRate, error) {
	return m.listRatesFn()
}

func (m *mockExchangeFacade) GetRate(from, to string) (*model.ExchangeRate, error) {
	return m.getRateFn(from, to)
}

func (m *mockExchangeFacade) Calculate(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
	return m.calculateFn(ctx, from, to, amount)
}

func (m *mockExchangeFacade) Convert(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	return m.convertFn(ctx, from, to, amount)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sampleRate(from, to string) model.ExchangeRate {
	return model.ExchangeRate{
		ID:           1,
		FromCurrency: from,
		ToCurrency:   to,
		BuyRate:      decimal.NewFromFloat(117.0),
		SellRate:     decimal.NewFromFloat(119.0),
		UpdatedAt:    time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Sentinel passthrough sanity check
// ---------------------------------------------------------------------------

func TestSentinels_PassthroughCodes(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		code     codes.Code
	}{
		{"RateNotFound", service.ErrRateNotFound, codes.NotFound},
		{"InvalidAmount", service.ErrInvalidAmount, codes.InvalidArgument},
		{"UnsupportedCurrency", service.ErrUnsupportedCurrency, codes.InvalidArgument},
		{"RateLookupFailed", service.ErrRateLookupFailed, codes.Internal},
		{"SameCurrency", service.ErrSameCurrency, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Op: %w", tc.sentinel)
			require.True(t, errors.Is(wrapped, tc.sentinel))
			assert.Equal(t, tc.code, status.Code(wrapped))
		})
	}
}

// ---------------------------------------------------------------------------
// ListRates
// ---------------------------------------------------------------------------

func TestListRates_Success(t *testing.T) {
	rates := []model.ExchangeRate{sampleRate("EUR", "RSD"), sampleRate("RSD", "EUR")}
	mock := &mockExchangeFacade{
		listRatesFn: func() ([]model.ExchangeRate, error) { return rates, nil },
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.ListRates(context.Background(), &pb.ListRatesRequest{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Rates, 2)
	assert.Equal(t, "EUR", resp.Rates[0].FromCurrency)
	assert.Equal(t, "RSD", resp.Rates[0].ToCurrency)
}

func TestListRates_Error(t *testing.T) {
	mock := &mockExchangeFacade{
		listRatesFn: func() ([]model.ExchangeRate, error) {
			return nil, fmt.Errorf("ListRates db: %w", service.ErrRateLookupFailed)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.ListRates(context.Background(), &pb.ListRatesRequest{})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// GetRate
// ---------------------------------------------------------------------------

func TestGetRate_Success(t *testing.T) {
	rate := sampleRate("EUR", "RSD")
	mock := &mockExchangeFacade{
		getRateFn: func(from, to string) (*model.ExchangeRate, error) {
			assert.Equal(t, "EUR", from)
			assert.Equal(t, "RSD", to)
			return &rate, nil
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.GetRate(context.Background(), &pb.GetRateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "EUR", resp.FromCurrency)
	assert.Equal(t, "RSD", resp.ToCurrency)
}

func TestGetRate_NotFound(t *testing.T) {
	mock := &mockExchangeFacade{
		getRateFn: func(from, to string) (*model.ExchangeRate, error) {
			return nil, fmt.Errorf("GetRate(%s/%s): %w", from, to, service.ErrRateNotFound)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.GetRate(context.Background(), &pb.GetRateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "USD",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestGetRate_ServiceError(t *testing.T) {
	mock := &mockExchangeFacade{
		getRateFn: func(from, to string) (*model.ExchangeRate, error) {
			return nil, fmt.Errorf("GetRate db: %w", service.ErrRateLookupFailed)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.GetRate(context.Background(), &pb.GetRateRequest{
		FromCurrency: "USD",
		ToCurrency:   "RSD",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// Calculate
// ---------------------------------------------------------------------------

func TestCalculate_Success(t *testing.T) {
	net := decimal.NewFromFloat(11781.00)
	commRate := decimal.NewFromFloat(0.005)
	effRate := decimal.NewFromFloat(118.0)

	mock := &mockExchangeFacade{
		calculateFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
			assert.Equal(t, "EUR", from)
			assert.Equal(t, "RSD", to)
			assert.True(t, decimal.NewFromFloat(100).Equal(amount))
			return net, commRate, effRate, nil
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "100",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "EUR", resp.FromCurrency)
	assert.Equal(t, "RSD", resp.ToCurrency)
	assert.NotEmpty(t, resp.ConvertedAmount)
	assert.NotEmpty(t, resp.EffectiveRate)
}

func TestCalculate_InvalidAmount_EmptyString(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrInvalidAmount))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCalculate_InvalidAmount_Negative(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "-50",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrInvalidAmount))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCalculate_UnsupportedFromCurrency(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "XYZ",
		ToCurrency:   "RSD",
		Amount:       "100",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrUnsupportedCurrency))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCalculate_UnsupportedToCurrency(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "XYZ",
		Amount:       "100",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrUnsupportedCurrency))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCalculate_NotFound(t *testing.T) {
	mock := &mockExchangeFacade{
		calculateFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
			return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("Calculate(%s/%s): %w", from, to, service.ErrRateNotFound)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "100",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCalculate_ServiceError(t *testing.T) {
	mock := &mockExchangeFacade{
		calculateFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, decimal.Decimal, error) {
			return decimal.Zero, decimal.Zero, decimal.Zero, fmt.Errorf("Calculate db: %w", service.ErrRateLookupFailed)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Calculate(context.Background(), &pb.CalculateRequest{
		FromCurrency: "USD",
		ToCurrency:   "EUR",
		Amount:       "200",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// Convert
// ---------------------------------------------------------------------------

func TestConvert_Success(t *testing.T) {
	converted := decimal.NewFromFloat(11800.00)
	effRate := decimal.NewFromFloat(118.0)

	mock := &mockExchangeFacade{
		convertFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
			assert.Equal(t, "EUR", from)
			assert.Equal(t, "RSD", to)
			assert.True(t, decimal.NewFromFloat(100).Equal(amount))
			return converted, effRate, nil
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Convert(context.Background(), &pb.ConvertRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "100",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.ConvertedAmount)
	assert.NotEmpty(t, resp.EffectiveRate)
}

func TestConvert_InvalidAmount(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Convert(context.Background(), &pb.ConvertRequest{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		Amount:       "0",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrInvalidAmount))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestConvert_UnsupportedCurrency(t *testing.T) {
	mock := &mockExchangeFacade{}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Convert(context.Background(), &pb.ConvertRequest{
		FromCurrency: "XYZ",
		ToCurrency:   "RSD",
		Amount:       "100",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrUnsupportedCurrency))
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestConvert_NotFound(t *testing.T) {
	mock := &mockExchangeFacade{
		convertFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
			return decimal.Zero, decimal.Zero, fmt.Errorf("Convert(%s/%s): %w", from, to, service.ErrRateNotFound)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Convert(context.Background(), &pb.ConvertRequest{
		FromCurrency: "USD",
		ToCurrency:   "RSD",
		Amount:       "500",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestConvert_ServiceError(t *testing.T) {
	mock := &mockExchangeFacade{
		convertFn: func(_ context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
			return decimal.Zero, decimal.Zero, fmt.Errorf("Convert db: %w", service.ErrRateLookupFailed)
		},
	}
	h := newExchangeGRPCHandlerForTest(mock)

	resp, err := h.Convert(context.Background(), &pb.ConvertRequest{
		FromCurrency: "USD",
		ToCurrency:   "EUR",
		Amount:       "100",
	})

	require.Nil(t, resp)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
