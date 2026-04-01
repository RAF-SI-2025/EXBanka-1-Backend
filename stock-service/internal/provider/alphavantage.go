package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const alphaVantageBaseURL = "https://www.alphavantage.co/query"

type AlphaVantageClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewAlphaVantageClient(apiKey string) *AlphaVantageClient {
	return &AlphaVantageClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// QuoteData represents parsed price data from the GLOBAL_QUOTE endpoint.
type QuoteData struct {
	Price  decimal.Decimal
	High   decimal.Decimal
	Low    decimal.Decimal
	Change decimal.Decimal
	Volume int64
}

// OverviewData represents parsed company overview from the OVERVIEW endpoint.
type OverviewData struct {
	Name              string
	Exchange          string
	OutstandingShares int64
	DividendYield     decimal.Decimal
}

// FetchQuote retrieves real-time price data for a stock ticker.
func (c *AlphaVantageClient) FetchQuote(ticker string) (*QuoteData, error) {
	url := fmt.Sprintf("%s?function=GLOBAL_QUOTE&symbol=%s&apikey=%s",
		alphaVantageBaseURL, ticker, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch quote for %s: %w", ticker, err)
	}

	var raw struct {
		GlobalQuote struct {
			Price  string `json:"05. price"`
			High   string `json:"03. high"`
			Low    string `json:"04. low"`
			Change string `json:"09. change"`
			Volume string `json:"06. volume"`
		} `json:"Global Quote"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse quote for %s: %w", ticker, err)
	}

	q := raw.GlobalQuote
	price, _ := decimal.NewFromString(q.Price)
	high, _ := decimal.NewFromString(q.High)
	low, _ := decimal.NewFromString(q.Low)
	change, _ := decimal.NewFromString(q.Change)
	vol, _ := strconv.ParseInt(strings.TrimSpace(q.Volume), 10, 64)

	return &QuoteData{
		Price:  price,
		High:   high,
		Low:    low,
		Change: change,
		Volume: vol,
	}, nil
}

// FetchOverview retrieves company overview data for a stock ticker.
func (c *AlphaVantageClient) FetchOverview(ticker string) (*OverviewData, error) {
	url := fmt.Sprintf("%s?function=OVERVIEW&symbol=%s&apikey=%s",
		alphaVantageBaseURL, ticker, c.apiKey)

	body, err := c.doGet(url)
	if err != nil {
		return nil, fmt.Errorf("fetch overview for %s: %w", ticker, err)
	}

	var raw struct {
		Name              string `json:"Name"`
		Exchange          string `json:"Exchange"`
		SharesOutstanding string `json:"SharesOutstanding"`
		DividendYield     string `json:"DividendYield"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse overview for %s: %w", ticker, err)
	}

	shares, _ := strconv.ParseInt(raw.SharesOutstanding, 10, 64)
	divYield, _ := decimal.NewFromString(raw.DividendYield)

	return &OverviewData{
		Name:              raw.Name,
		Exchange:          raw.Exchange,
		OutstandingShares: shares,
		DividendYield:     divYield,
	}, nil
}

// FetchStockData fetches both quote and overview, merging into a partial Stock model.
func (c *AlphaVantageClient) FetchStockData(ticker string) (*model.Stock, error) {
	quote, err := c.FetchQuote(ticker)
	if err != nil {
		return nil, err
	}
	overview, err := c.FetchOverview(ticker)
	if err != nil {
		return nil, err
	}

	return &model.Stock{
		Ticker:            ticker,
		Name:              overview.Name,
		OutstandingShares: overview.OutstandingShares,
		DividendYield:     overview.DividendYield,
		Price:             quote.Price,
		High:              quote.High,
		Low:               quote.Low,
		Change:            quote.Change,
		Volume:            quote.Volume,
		LastRefresh:       time.Now(),
	}, nil
}

func (c *AlphaVantageClient) doGet(url string) ([]byte, error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
