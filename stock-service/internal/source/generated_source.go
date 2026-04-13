package source

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

const (
	generatedSeed          = int64(0x1EB0081A) // arbitrary stable seed; not cryptographic
	generatedRandomWalkPct = 0.005             // ±0.5% per refresh tick
)

// DecFromFloat is exported so tests can build expected decimals.
func DecFromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// GeneratedSource produces deterministic local data for dev/demo use.
// It holds mutable current prices so RefreshPrices can drift them.
type GeneratedSource struct {
	mu        sync.RWMutex
	now       time.Time
	rng       *rand.Rand
	stockPx   map[string]decimal.Decimal
	futuresPx map[string]decimal.Decimal
	forexPx   map[string]decimal.Decimal
}

// NewGeneratedSource constructs a GeneratedSource with a deterministic seed.
// Two calls to NewGeneratedSource will produce identical FetchStocks output.
func NewGeneratedSource() *GeneratedSource {
	g := &GeneratedSource{
		now:       time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC),
		rng:       rand.New(rand.NewSource(generatedSeed)), //nolint:gosec // not cryptographic
		stockPx:   make(map[string]decimal.Decimal, len(generatedStocks)),
		futuresPx: make(map[string]decimal.Decimal, len(generatedFutures)),
		forexPx:   make(map[string]decimal.Decimal, len(forexSeedPrices)),
	}
	for _, s := range generatedStocks {
		g.stockPx[s.Ticker] = dec(s.Price)
	}
	for _, f := range generatedFutures {
		g.futuresPx[f.Ticker] = dec(f.Price)
	}
	for pair, p := range forexSeedPrices {
		g.forexPx[pair] = dec(p)
	}
	return g
}

func (g *GeneratedSource) Name() string { return "generated" }

// exchangeForTicker picks an exchange index 1..20 deterministically from the
// ticker using FNV-32a hash. Stable across runs.
func exchangeForTicker(ticker string) uint64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ticker))
	return uint64(h.Sum32()%20) + 1
}

// exchangeDefaults maps an exchange acronym to its region metadata.
// Used to populate the not-null fields Polity, Currency, TimeZone, OpenTime, CloseTime.
var exchangeDefaults = map[string]struct {
	Polity    string
	Currency  string
	TimeZone  string
	OpenTime  string
	CloseTime string
}{
	"NYSE":     {"United States", "USD", "America/New_York", "09:30", "16:00"},
	"NASDAQ":   {"United States", "USD", "America/New_York", "09:30", "16:00"},
	"LSE":      {"United Kingdom", "GBP", "Europe/London", "08:00", "16:30"},
	"TSE":      {"Japan", "JPY", "Asia/Tokyo", "09:00", "15:30"},
	"HKEX":     {"Hong Kong", "HKD", "Asia/Hong_Kong", "09:30", "16:00"},
	"SSE":      {"China", "CNY", "Asia/Shanghai", "09:30", "15:00"},
	"EURONEXT": {"Netherlands", "EUR", "Europe/Amsterdam", "09:00", "17:30"},
	"TSX":      {"Canada", "CAD", "America/Toronto", "09:30", "16:00"},
	"BSE":      {"India", "INR", "Asia/Kolkata", "09:15", "15:30"},
	"ASX":      {"Australia", "AUD", "Australia/Sydney", "10:00", "16:00"},
	"JSE":      {"South Africa", "ZAR", "Africa/Johannesburg", "09:00", "17:00"},
	"BMV":      {"Mexico", "MXN", "America/Mexico_City", "08:30", "15:00"},
	"BVMF":     {"Brazil", "BRL", "America/Sao_Paulo", "10:00", "17:55"},
	"KRX":      {"South Korea", "KRW", "Asia/Seoul", "09:00", "15:30"},
	"BME":      {"Spain", "EUR", "Europe/Madrid", "09:00", "17:30"},
	"SIX":      {"Switzerland", "CHF", "Europe/Zurich", "09:00", "17:30"},
	"OMX":      {"Sweden", "SEK", "Europe/Stockholm", "09:00", "17:30"},
	"WSE":      {"Poland", "PLN", "Europe/Warsaw", "09:00", "17:00"},
	"BVC":      {"Colombia", "COP", "America/Bogota", "09:30", "16:00"},
	"MOEX":     {"Russia", "RUB", "Europe/Moscow", "09:50", "18:50"},
}

// futuresContractUnit maps a futures ticker to its contract unit string.
var futuresContractUnit = map[string]string{
	"CL":  "barrel",
	"GC":  "troy ounce",
	"SI":  "troy ounce",
	"NG":  "MMBtu",
	"HG":  "pound",
	"ZC":  "bushel",
	"ZS":  "bushel",
	"ZW":  "bushel",
	"CC":  "metric ton",
	"KC":  "pound",
	"SB":  "pound",
	"CT":  "pound",
	"ES":  "index points",
	"NQ":  "index points",
	"YM":  "index points",
	"RTY": "index points",
	"ZB":  "USD",
	"ZN":  "USD",
	"6E":  "EUR",
	"6J":  "JPY",
}

// FetchExchanges returns the 20 generated exchanges with all required fields populated.
func (g *GeneratedSource) FetchExchanges(_ context.Context) ([]model.StockExchange, error) {
	out := make([]model.StockExchange, 0, len(generatedExchanges))
	for _, src := range generatedExchanges {
		ex := src
		if d, ok := exchangeDefaults[ex.Acronym]; ok {
			ex.Polity = d.Polity
			ex.Currency = d.Currency
			ex.TimeZone = d.TimeZone
			ex.OpenTime = d.OpenTime
			ex.CloseTime = d.CloseTime
		} else {
			// Blanket fallback — should not happen with a complete defaults map.
			ex.Polity = "Unknown"
			ex.Currency = "USD"
			ex.TimeZone = "UTC"
			ex.OpenTime = "09:00"
			ex.CloseTime = "17:00"
		}
		out = append(out, ex)
	}
	return out, nil
}

// FetchStocks returns the 20 generated stocks with current prices.
func (g *GeneratedSource) FetchStocks(_ context.Context) ([]StockWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]StockWithListing, 0, len(generatedStocks))
	for _, s := range generatedStocks {
		price := g.stockPx[s.Ticker]
		out = append(out, StockWithListing{
			Stock: model.Stock{
				Ticker: s.Ticker,
				Name:   s.Name,
				Price:  price,
			},
			ExchangeID:  exchangeForTicker(s.Ticker),
			Price:       price,
			High:        price,
			Low:         price,
			LastRefresh: g.now,
		})
	}
	return out, nil
}

// FetchFutures returns the 20 generated futures contracts with current prices.
func (g *GeneratedSource) FetchFutures(_ context.Context) ([]FuturesWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]FuturesWithListing, 0, len(generatedFutures))
	for _, f := range generatedFutures {
		price := g.futuresPx[f.Ticker]
		unit, ok := futuresContractUnit[f.Ticker]
		if !ok {
			unit = "contract"
		}
		out = append(out, FuturesWithListing{
			Futures: model.FuturesContract{
				Ticker:         f.Ticker,
				Name:           f.Name,
				ContractSize:   f.ContractSize,
				ContractUnit:   unit,
				Price:          price,
				SettlementDate: futuresSettlementDate(g.now, f.DaysToExpiry),
			},
			ExchangeID:  exchangeForTicker(f.Ticker),
			Price:       price,
			High:        price,
			Low:         price,
			LastRefresh: g.now,
		})
	}
	return out, nil
}

// FetchForex returns 56 forex pairs (8 currencies × 7 counterparties) with current rates.
func (g *GeneratedSource) FetchForex(_ context.Context) ([]ForexWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]ForexWithListing, 0, 56)
	// Iterate in a deterministic order over supportedCurrencies.
	for _, base := range supportedCurrencies {
		for _, quote := range supportedCurrencies {
			if base == quote {
				continue
			}
			pair := base + "/" + quote
			price, ok := g.forexPx[pair]
			if !ok {
				continue
			}
			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}
			out = append(out, ForexWithListing{
				Forex: model.ForexPair{
					Ticker:        pair,
					Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
					BaseCurrency:  base,
					QuoteCurrency: quote,
					ExchangeRate:  price, // ForexPair uses ExchangeRate, not Price
					Liquidity:     liquidity,
					ExchangeID:    exchangeForTicker(pair),
					LastRefresh:   g.now,
				},
				ExchangeID:  exchangeForTicker(pair),
				Price:       price,
				High:        price,
				Low:         price,
				LastRefresh: g.now,
			})
		}
	}
	return out, nil
}

// FetchOptions generates option contracts for the given stock.
func (g *GeneratedSource) FetchOptions(_ context.Context, stock *model.Stock) ([]model.Option, error) {
	return GenerateOptionsForStock(stock), nil
}

// RefreshPrices applies a ±0.5% random walk to all current prices.
// The walk uses the instance's RNG so it is per-instance (not global).
func (g *GeneratedSource) RefreshPrices(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	walk := func(p decimal.Decimal) decimal.Decimal {
		delta := (g.rng.Float64()*2 - 1) * generatedRandomWalkPct //nolint:gosec // not cryptographic
		return p.Mul(decimal.NewFromFloat(1 + delta))
	}
	for k, v := range g.stockPx {
		g.stockPx[k] = walk(v)
	}
	for k, v := range g.futuresPx {
		g.futuresPx[k] = walk(v)
	}
	for k, v := range g.forexPx {
		g.forexPx[k] = walk(v)
	}
	return nil
}
