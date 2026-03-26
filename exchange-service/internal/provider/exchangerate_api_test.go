package provider_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/exchange-service/internal/provider"
)

func TestFetchRatesFromRSD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"result":    "success",
			"base_code": "RSD",
			"rates": map[string]float64{
				"EUR": 0.00851,
				"USD": 0.00926,
				"GBP": 0.00723,
				"CHF": 0.00840,
				"JPY": 1.42000,
				"CAD": 0.01280,
				"AUD": 0.01430,
				"RSD": 1.00000, // base — should be ignored
				"BGN": 0.01665, // unsupported — should be ignored
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	rates, err := p.FetchRatesFromRSD()

	require.NoError(t, err)
	assert.Len(t, rates, len(provider.SupportedCurrencies))
	eurRate, ok := rates["EUR"]
	require.True(t, ok)
	f, _ := eurRate.Float64()
	assert.InDelta(t, 0.00851, f, 0.0001)
	_, hasRSD := rates["RSD"]
	assert.False(t, hasRSD, "RSD should not appear in rates (it is the base)")
}

func TestFetchRatesFromRSD_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	_, err := p.FetchRatesFromRSD()
	assert.Error(t, err)
}
