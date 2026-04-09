package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

type futuresSeedEntry struct {
	Ticker          string  `json:"ticker"`
	Name            string  `json:"name"`
	ContractSize    int64   `json:"contract_size"`
	ContractUnit    string  `json:"contract_unit"`
	SettlementDate  string  `json:"settlement_date"`
	ExchangeAcronym string  `json:"exchange_acronym"`
	Price           float64 `json:"price"`
	High            float64 `json:"high"`
	Low             float64 `json:"low"`
	Volume          int64   `json:"volume"`
}

// FuturesSeedRow holds a partial FuturesContract plus the exchange acronym
// (to be resolved to an ExchangeID by the caller).
type FuturesSeedRow struct {
	Contract        model.FuturesContract
	ExchangeAcronym string
}

// LoadFuturesFromJSON reads futures seed data from a JSON file.
// Returns partial FuturesContract models (ExchangeID must be resolved by caller).
func LoadFuturesFromJSON(path string) ([]FuturesSeedRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read futures seed file: %w", err)
	}

	var entries []futuresSeedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse futures seed json: %w", err)
	}

	rows := make([]FuturesSeedRow, 0, len(entries))
	for _, e := range entries {
		settlement, err := time.Parse("2006-01-02", e.SettlementDate)
		if err != nil {
			return nil, fmt.Errorf("parse settlement date for %s: %w", e.Ticker, err)
		}
		rows = append(rows, FuturesSeedRow{
			Contract: model.FuturesContract{
				Ticker:         e.Ticker,
				Name:           e.Name,
				ContractSize:   e.ContractSize,
				ContractUnit:   e.ContractUnit,
				SettlementDate: settlement,
				Price:          decimal.NewFromFloat(e.Price),
				High:           decimal.NewFromFloat(e.High),
				Low:            decimal.NewFromFloat(e.Low),
				Change:         decimal.NewFromFloat(e.High - e.Low),
				Volume:         e.Volume,
				LastRefresh:    time.Now(),
			},
			ExchangeAcronym: e.ExchangeAcronym,
		})
	}
	return rows, nil
}

// DefaultStockTickers is the list of well-known tickers to sync from AlphaVantage.
// We sync these on startup and periodically. Additional tickers can be added later.
var DefaultStockTickers = []string{
	"AAPL", "MSFT", "GOOGL", "AMZN", "TSLA",
	"META", "NVDA", "JPM", "V", "JNJ",
	"WMT", "PG", "MA", "UNH", "HD",
	"DIS", "NFLX", "ADBE", "CRM", "PYPL",
}
