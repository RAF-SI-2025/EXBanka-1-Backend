package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const eodhDefaultBaseURL = "https://eodhd.com"

type EODHDExchange struct {
	Name         string `json:"Name"`
	Code         string `json:"Code"`
	OperatingMIC string `json:"OperatingMIC"`
	Country      string `json:"Country"`
	Currency     string `json:"Currency"`
	CountryISO2  string `json:"CountryISO2"`
	CountryISO3  string `json:"CountryISO3"`
}

type EODHDExchangeDetails struct {
	Name         string            `json:"Name"`
	Code         string            `json:"Code"`
	Timezone     string            `json:"Timezone"`
	IsOpen       bool              `json:"isOpen"`
	TradingHours EODHDTradingHours `json:"TradingHours"`
	Holidays     []EODHDHoliday    `json:"Holidays"`
}

type EODHDTradingHours struct {
	OpenHour  string `json:"OpenHour"`
	CloseHour string `json:"CloseHour"`
}

type EODHDHoliday struct {
	Holiday string `json:"Holiday"`
	Date    string `json:"Date"`
}

type EODHDClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewEODHDClient(apiKey string) *EODHDClient {
	return &EODHDClient{
		apiKey:  apiKey,
		baseURL: eodhDefaultBaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *EODHDClient) FetchExchanges() ([]EODHDExchange, error) {
	url := fmt.Sprintf("%s/api/exchanges-list/?api_token=%s&fmt=json", c.baseURL, c.apiKey)
	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch exchanges: %w", err)
	}
	var exchanges []EODHDExchange
	if err := json.Unmarshal(body, &exchanges); err != nil {
		return nil, fmt.Errorf("parse exchanges: %w", err)
	}
	return exchanges, nil
}

func (c *EODHDClient) FetchExchangeDetails(code string) (*EODHDExchangeDetails, error) {
	url := fmt.Sprintf("%s/api/exchange-details/%s?api_token=%s&fmt=json", c.baseURL, code, c.apiKey)
	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch exchange details for %s: %w", code, err)
	}
	var details EODHDExchangeDetails
	if err := json.Unmarshal(body, &details); err != nil {
		return nil, fmt.Errorf("parse exchange details for %s: %w", code, err)
	}
	return &details, nil
}

func (c *EODHDClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("EODHD API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
