package nbs

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// RateProvider is an interface so tests can mock the NBS API.
type RateProvider interface {
	FetchRates() (map[string][2]decimal.Decimal, error)
}

// nbsURL is the NBS exchange rate XML endpoint.
// Format: GET request returns XML with KursnaListaModWorker structure.
const nbsURL = "https://nbs.rs/kursnaListaModworker/na498.exchange.rate.worker/exchangeRateListXml"

// ExchangeRateItem maps one currency row from NBS XML.
type ExchangeRateItem struct {
	CurrencyCode string `xml:"currencyCode"`
	BuyingRate   string `xml:"buyingRate"`
	SellingRate  string `xml:"sellingRate"`
	MiddleRate   string `xml:"middleRate"`
	Unit         int    `xml:"unit"`
}

// ExchangeRateList is the root NBS XML element.
type ExchangeRateList struct {
	Items []ExchangeRateItem `xml:"item"`
	Date  string             `xml:"date"`
}

// Client fetches exchange rates from NBS.
type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// FetchRates returns a map of currency_code -> [buyRate, sellRate] per 1 unit in RSD.
func (c *Client) FetchRates() (map[string][2]decimal.Decimal, error) {
	resp, err := c.httpClient.Get(nbsURL)
	if err != nil {
		return nil, fmt.Errorf("NBS request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading NBS response: %w", err)
	}

	return ParseRatesXML(body)
}

// ParseRatesXML parses NBS XML response. Exported for testing.
func ParseRatesXML(data []byte) (map[string][2]decimal.Decimal, error) {
	var list ExchangeRateList
	// NBS uses comma as decimal separator in some locales — normalize to dot
	normalized := strings.ReplaceAll(string(data), ",", ".")
	if err := xml.Unmarshal([]byte(normalized), &list); err != nil {
		return nil, fmt.Errorf("parsing NBS XML: %w", err)
	}

	rates := make(map[string][2]decimal.Decimal)
	for _, item := range list.Items {
		buy, errB := decimal.NewFromString(item.BuyingRate)
		sell, errS := decimal.NewFromString(item.SellingRate)
		if errB != nil || errS != nil || buy.IsZero() || sell.IsZero() {
			continue // skip malformed items
		}
		unit := decimal.NewFromInt(int64(item.Unit))
		if unit.IsZero() {
			unit = decimal.NewFromInt(1)
		}
		rates[item.CurrencyCode] = [2]decimal.Decimal{
			buy.Div(unit),
			sell.Div(unit),
		}
	}
	return rates, nil
}
