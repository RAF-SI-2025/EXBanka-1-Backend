// api-gateway/internal/handler/exchange_handler_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	exchangepb "github.com/exbanka/contract/exchangepb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func exchangeRouter(h *handler.ExchangeHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/rates", h.ListExchangeRates)
	r.GET("/rates/:from/:to", h.GetExchangeRate)
	r.POST("/calculate", h.CalculateExchange)
	return r
}

func TestExchange_ListRates(t *testing.T) {
	ex := &stubExchangeClient{
		listFn: func(_ *exchangepb.ListRatesRequest) (*exchangepb.ListRatesResponse, error) {
			return &exchangepb.ListRatesResponse{
				Rates: []*exchangepb.RateResponse{{FromCurrency: "EUR", ToCurrency: "RSD", BuyRate: "117.0", SellRate: "118.0"}},
			}, nil
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	req := httptest.NewRequest("GET", "/rates", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"from_currency":"EUR"`)
}

func TestExchange_ListRates_GRPCError(t *testing.T) {
	ex := &stubExchangeClient{
		listFn: func(_ *exchangepb.ListRatesRequest) (*exchangepb.ListRatesResponse, error) {
			return nil, status.Error(codes.Internal, "x")
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	req := httptest.NewRequest("GET", "/rates", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestExchange_GetRate_UpperCases(t *testing.T) {
	ex := &stubExchangeClient{
		getFn: func(req *exchangepb.GetRateRequest) (*exchangepb.RateResponse, error) {
			require.Equal(t, "EUR", req.FromCurrency)
			require.Equal(t, "RSD", req.ToCurrency)
			return &exchangepb.RateResponse{FromCurrency: "EUR", ToCurrency: "RSD"}, nil
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	req := httptest.NewRequest("GET", "/rates/eur/rsd", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestExchange_GetRate_NotFound(t *testing.T) {
	ex := &stubExchangeClient{
		getFn: func(_ *exchangepb.GetRateRequest) (*exchangepb.RateResponse, error) {
			return nil, status.Error(codes.NotFound, "no rate")
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	req := httptest.NewRequest("GET", "/rates/aaa/bbb", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestExchange_Calculate_Success(t *testing.T) {
	ex := &stubExchangeClient{
		calculateFn: func(req *exchangepb.CalculateRequest) (*exchangepb.CalculateResponse, error) {
			require.Equal(t, "EUR", req.FromCurrency)
			require.Equal(t, "USD", req.ToCurrency)
			return &exchangepb.CalculateResponse{
				FromCurrency:    req.FromCurrency,
				ToCurrency:      req.ToCurrency,
				InputAmount:     "100",
				ConvertedAmount: "115",
				CommissionRate:  "0.005",
				EffectiveRate:   "1.15",
			}, nil
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	body := `{"fromCurrency":"EUR","toCurrency":"USD","amount":"100"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"converted_amount":"115"`)
}

func TestExchange_Calculate_BadJSON(t *testing.T) {
	h := handler.NewExchangeHandler(&stubExchangeClient{})
	r := exchangeRouter(h)
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExchange_Calculate_NegativeAmount(t *testing.T) {
	h := handler.NewExchangeHandler(&stubExchangeClient{})
	r := exchangeRouter(h)
	body := `{"fromCurrency":"EUR","toCurrency":"USD","amount":"-100"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "amount must be a positive number")
}

func TestExchange_Calculate_BadAmountFormat(t *testing.T) {
	h := handler.NewExchangeHandler(&stubExchangeClient{})
	r := exchangeRouter(h)
	body := `{"fromCurrency":"EUR","toCurrency":"USD","amount":"abc"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestExchange_Calculate_UnsupportedFrom(t *testing.T) {
	h := handler.NewExchangeHandler(&stubExchangeClient{})
	r := exchangeRouter(h)
	body := `{"fromCurrency":"XYZ","toCurrency":"USD","amount":"100"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unsupported fromCurrency")
}

func TestExchange_Calculate_UnsupportedTo(t *testing.T) {
	h := handler.NewExchangeHandler(&stubExchangeClient{})
	r := exchangeRouter(h)
	body := `{"fromCurrency":"EUR","toCurrency":"XYZ","amount":"100"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unsupported toCurrency")
}

func TestExchange_Calculate_GRPCError(t *testing.T) {
	ex := &stubExchangeClient{
		calculateFn: func(_ *exchangepb.CalculateRequest) (*exchangepb.CalculateResponse, error) {
			return nil, status.Error(codes.Internal, "down")
		},
	}
	h := handler.NewExchangeHandler(ex)
	r := exchangeRouter(h)
	body := `{"fromCurrency":"EUR","toCurrency":"USD","amount":"100"}`
	req := httptest.NewRequest("POST", "/calculate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
