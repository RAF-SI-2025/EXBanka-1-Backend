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
		_ = json.NewEncoder(w).Encode(resp)
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

func TestFetchRatesFromRSD_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	_, err := p.FetchRatesFromRSD()
	assert.Error(t, err)
}

func TestFetchRatesFromRSD_NonSuccessResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result":    "error",
			"base_code": "RSD",
			"rates":     map[string]float64{"EUR": 0.00851},
		})
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	_, err := p.FetchRatesFromRSD()
	assert.Error(t, err)
}

func TestFetchRatesFromRSD_NoSupportedCurrencies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result":    "success",
			"base_code": "RSD",
			"rates":     map[string]float64{"BGN": 0.0166},
		})
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	_, err := p.FetchRatesFromRSD()
	assert.Error(t, err, "must error when no supported currencies are present")
}

func TestFetchRatesFromRSD_ZeroRateSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result":    "success",
			"base_code": "RSD",
			"rates": map[string]float64{
				"EUR": 0.0,    // zero — must be skipped
				"USD": 0.0093, // included
			},
		})
	}))
	defer srv.Close()

	p := provider.NewExchangeRateAPIClient("", srv.URL+"/v6/latest/")
	rates, err := p.FetchRatesFromRSD()
	require.NoError(t, err)
	_, hasEUR := rates["EUR"]
	assert.False(t, hasEUR, "zero rate should be skipped")
	_, hasUSD := rates["USD"]
	assert.True(t, hasUSD)
}

func TestFetchRatesFromRSD_HttpClientError(t *testing.T) {
	// Point to a baseURL that will fail to dial.
	p := provider.NewExchangeRateAPIClient("", "http://127.0.0.1:1/v6/latest/")
	_, err := p.FetchRatesFromRSD()
	assert.Error(t, err, "transport error must propagate")
}

func TestNewExchangeRateAPIClient_DefaultsBaseURLWhenEmpty(t *testing.T) {
	// Constructing with an empty baseURL must not panic and must give back a
	// usable client. We don't issue a real network call here.
	c := provider.NewExchangeRateAPIClient("", "")
	assert.NotNil(t, c)
}
