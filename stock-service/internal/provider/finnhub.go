package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const finnhubDefaultBaseURL = "https://finnhub.io"

type FinnhubForexSymbol struct {
	Symbol        string `json:"symbol"`
	DisplaySymbol string `json:"displaySymbol"`
	Description   string `json:"description"`
}

type FinnhubClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewFinnhubClient(apiKey string) *FinnhubClient {
	return &FinnhubClient{
		apiKey:  apiKey,
		baseURL: finnhubDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *FinnhubClient) FetchForexSymbols() ([]FinnhubForexSymbol, error) {
	url := fmt.Sprintf("%s/api/v1/forex/symbol?exchange=oanda&token=%s", c.baseURL, c.apiKey)
	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch forex symbols: %w", err)
	}
	var symbols []FinnhubForexSymbol
	if err := json.Unmarshal(body, &symbols); err != nil {
		return nil, fmt.Errorf("parse forex symbols: %w", err)
	}
	return symbols, nil
}

func (c *FinnhubClient) FetchForexRates(base string) (map[string]float64, error) {
	url := fmt.Sprintf("%s/api/v1/forex/rates?base=%s&token=%s", c.baseURL, base, c.apiKey)
	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch forex rates for %s: %w", base, err)
	}
	var raw struct {
		Base  string             `json:"base"`
		Quote map[string]float64 `json:"quote"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse forex rates for %s: %w", base, err)
	}
	return raw.Quote, nil
}

func (c *FinnhubClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Finnhub API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
