package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const alpacaDefaultBaseURL = "https://api.alpaca.markets"

type AlpacaAsset struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"`
	Status   string `json:"status"`
	Tradable bool   `json:"tradable"`
}

type AlpacaClient struct {
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient *http.Client
}

func NewAlpacaClient(apiKey, apiSecret string) *AlpacaClient {
	return &AlpacaClient{
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   alpacaDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *AlpacaClient) FetchAssets() ([]AlpacaAsset, error) {
	url := fmt.Sprintf("%s/v2/assets?status=active&asset_class=us_equity", c.baseURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("APCA-API-KEY-ID", c.apiKey)
	req.Header.Set("APCA-API-SECRET-KEY", c.apiSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch assets: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Alpaca API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var all []AlpacaAsset
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("parse assets: %w", err)
	}

	tradable := make([]AlpacaAsset, 0, len(all))
	for _, a := range all {
		if a.Tradable {
			tradable = append(tradable, a)
		}
	}
	return tradable, nil
}
