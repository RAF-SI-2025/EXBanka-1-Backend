package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

// exchangeRateAPIResponse is the JSON envelope returned by open.er-api.com.
type exchangeRateAPIResponse struct {
	Result   string             `json:"result"`
	BaseCode string             `json:"base_code"`
	Rates    map[string]float64 `json:"rates"`
}

// ExchangeRateAPIClient fetches rates from open.er-api.com (free tier, no key needed).
// If apiKey is non-empty, the v6 authenticated endpoint is used instead.
type ExchangeRateAPIClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string // override for tests; production default is "https://open.er-api.com/v6/latest/"
}

func NewExchangeRateAPIClient(apiKey, baseURL string) *ExchangeRateAPIClient {
	if baseURL == "" {
		baseURL = "https://open.er-api.com/v6/latest/"
	}
	return &ExchangeRateAPIClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		apiKey:     apiKey,
		baseURL:    baseURL,
	}
}

// FetchRatesFromRSD returns mid-market rates relative to RSD.
// It calls the provider once with RSD as the base currency.
// The response map contains only supported currencies (SupportedCurrencies).
func (c *ExchangeRateAPIClient) FetchRatesFromRSD() (map[string]decimal.Decimal, error) {
	// Use baseURL for all requests so tests can inject an httptest.Server.
	// When an apiKey is set and baseURL is the free-tier default, switch to the
	// authenticated paid-tier endpoint; a custom baseURL is always honoured as-is.
	const freeBase = "https://open.er-api.com/v6/latest/"
	url := c.baseURL + "RSD"
	if c.apiKey != "" && c.baseURL == freeBase {
		url = fmt.Sprintf("https://v6.exchangerate-api.com/v6/%s/latest/RSD", c.apiKey)
	}

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("exchange rate API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchange rate API returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading exchange rate API response: %w", err)
	}

	var apiResp exchangeRateAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing exchange rate API response: %w", err)
	}
	if apiResp.Result != "success" {
		return nil, fmt.Errorf("exchange rate API returned result=%q", apiResp.Result)
	}

	rates := make(map[string]decimal.Decimal, len(SupportedCurrencies))
	for _, code := range SupportedCurrencies {
		raw, ok := apiResp.Rates[code]
		if !ok || raw <= 0 {
			continue
		}
		rates[code] = decimal.NewFromFloat(raw)
	}
	if len(rates) == 0 {
		return nil, fmt.Errorf("exchange rate API: no supported currencies in response")
	}
	return rates, nil
}
