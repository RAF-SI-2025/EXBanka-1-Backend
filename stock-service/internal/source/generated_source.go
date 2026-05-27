package source

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// oscillationMultipliers defines a deterministic 4-phase cycle applied to
// stock and futures prices. Each phase lasts one wallclock minute, so the
// full cycle is 4 minutes and repeats forever. This replaces a random walk
// so demo / test environments produce predictable, visible price movement
// that order triggers (limit, stop, alerts) can be verified against.
//
// Phase index = floor(unixSeconds / 60) mod 4.
var oscillationMultipliers = [4]float64{0.90, 1.00, 1.10, 1.00}

// DecFromFloat is exported so tests can build expected decimals.
func DecFromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// GeneratedSource produces deterministic local data for dev/demo use.
// It holds mutable current prices so RefreshPrices can update them to the
// current oscillation phase.
type GeneratedSource struct {
	mu  sync.RWMutex
	now time.Time
	// clock is the wallclock used to compute the oscillation phase. It is
	// overridable for deterministic tests.
	clock func() time.Time
	// baseStockPx / baseFuturesPx are the immutable seed prices captured at
	// construction. Current prices are always derived from these times an
	// oscillation multiplier, so the cycle never drifts.
	baseStockPx   map[string]decimal.Decimal
	baseFuturesPx map[string]decimal.Decimal
	stockPx       map[string]decimal.Decimal
	futuresPx     map[string]decimal.Decimal
	forexPx       map[string]decimal.Decimal
	// exchangeByAcronym resolves exchange acronyms to the IDs currently in
	// the database. When nil (unit tests without a DB), the fallback hash
	// `exchangeForTicker` is used, which assumes fresh-DB IDs 1..20.
	exchangeByAcronym ExchangeByAcronym
}

// NewGeneratedSource constructs a GeneratedSource seeded from the static
// `generatedStocks` / `generatedFutures` tables. Two calls to
// NewGeneratedSource at the same wallclock minute produce identical
// FetchStocks output.
func NewGeneratedSource() *GeneratedSource {
	g := &GeneratedSource{
		now:           time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC),
		clock:         func() time.Time { return time.Now().UTC() },
		baseStockPx:   make(map[string]decimal.Decimal, len(generatedStocks)),
		baseFuturesPx: make(map[string]decimal.Decimal, len(generatedFutures)),
		stockPx:       make(map[string]decimal.Decimal, len(generatedStocks)),
		futuresPx:     make(map[string]decimal.Decimal, len(generatedFutures)),
		forexPx:       make(map[string]decimal.Decimal, len(forexSeedPrices)),
	}
	for _, s := range generatedStocks {
		base := dec(s.Price)
		g.baseStockPx[s.Ticker] = base
		g.stockPx[s.Ticker] = base
	}
	for _, f := range generatedFutures {
		base := dec(f.Price)
		g.baseFuturesPx[f.Ticker] = base
		g.futuresPx[f.Ticker] = base
	}
	for pair, p := range forexSeedPrices {
		g.forexPx[pair] = dec(p)
	}
	// Apply the current phase so the first FetchStocks call after
	// construction reflects the oscillating price, not the raw seed.
	g.applyOscillationLocked()
	return g
}

// withClock overrides the clock used for phase computation. Test-only.
func (g *GeneratedSource) withClock(fn func() time.Time) *GeneratedSource {
	g.mu.Lock()
	g.clock = fn
	g.applyOscillationLocked()
	g.mu.Unlock()
	return g
}

// oscillationPhase returns the current phase index in [0,4) for the given
// instant. Each phase covers one wallclock minute.
func oscillationPhase(t time.Time) int {
	return int((t.Unix() / 60) % int64(len(oscillationMultipliers)))
}

// applyOscillationLocked recomputes stockPx and futuresPx from the immutable
// base seeds times the current phase multiplier. Must be called with g.mu
// held for writing.
func (g *GeneratedSource) applyOscillationLocked() {
	mult := decimal.NewFromFloat(oscillationMultipliers[oscillationPhase(g.clock())])
	for k, base := range g.baseStockPx {
		g.stockPx[k] = base.Mul(mult)
	}
	for k, base := range g.baseFuturesPx {
		g.futuresPx[k] = base.Mul(mult)
	}
}

// WithExchangeResolver attaches the exchange-lookup function used to translate
// acronyms (e.g. "NYSE") into the DB IDs the caller needs for foreign-key
// references. Must be called before the source is first used for seeding if
// the underlying DB may not have sequential 1..20 exchange IDs — in practice,
// always after a wipe+reseed.
func (g *GeneratedSource) WithExchangeResolver(fn ExchangeByAcronym) *GeneratedSource {
	g.exchangeByAcronym = fn
	return g
}

func (g *GeneratedSource) Name() string { return "generated" }

// exchangeForTicker picks an exchange index 1..20 deterministically from the
// ticker using FNV-32a hash. This returns a positional index, not a DB ID —
// after a wipe+reseed the sequence advances and those two diverge. Use
// resolveExchangeID, which goes through the injected resolver, for anything
// that needs a valid foreign key.
func exchangeForTicker(ticker string) uint64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(ticker))
	return uint64(h.Sum32()%20) + 1
}

// hashVolume returns a deterministic int64 in [minVol, maxVol] derived from
// the given seed string. Same seed → same value across process restarts, so
// the generated source produces reproducible Volume fields for
// stocks/futures/forex.
func hashVolume(seed string, minVol, maxVol int64) int64 {
	if maxVol <= minVol {
		return minVol
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	rangeSize := uint64(maxVol - minVol + 1)
	return minVol + int64(h.Sum64()%rangeSize)
}

// generatedExchangeAcronyms lists the 20 generated exchanges in the same order
// as generatedExchanges so exchangeForTicker can map its hash result to a
// stable acronym, which the resolver translates to a real DB ID.
var generatedExchangeAcronyms = func() []string {
	out := make([]string, len(generatedExchanges))
	for i, ex := range generatedExchanges {
		out[i] = ex.Acronym
	}
	return out
}()

// acronymForTicker returns the generated exchange acronym for a given ticker.
func acronymForTicker(ticker string) string {
	idx := (exchangeForTicker(ticker) - 1) % uint64(len(generatedExchangeAcronyms))
	return generatedExchangeAcronyms[idx]
}

// resolveExchangeID returns the real DB exchange ID for a ticker. When a
// resolver is wired (production path), it looks up by acronym. Without one
// (unit tests), it falls back to the positional hash, which only works on a
// fresh DB where IDs happen to equal positions.
func (g *GeneratedSource) resolveExchangeID(ticker string) uint64 {
	if g.exchangeByAcronym == nil {
		return exchangeForTicker(ticker)
	}
	id, err := g.exchangeByAcronym(acronymForTicker(ticker))
	if err != nil {
		// Fallback keeps the seed path forward-progressing; syncStocks logs
		// the subsequent FK failure if the ID is stale.
		return exchangeForTicker(ticker)
	}
	return id
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
		// Normalize to the supported-currency set so a buy order on this
		// exchange survives the exchange-service.Convert call at fill time.
		// Unsupported codes (HKD, CNY, INR, ZAR, MXN, BRL, KRW, SEK, PLN,
		// COP, RUB — all present in exchangeDefaults above) collapse to USD.
		ex.Currency = NormalizeExchangeCurrency(ex.Currency)
		out = append(out, ex)
	}
	return out, nil
}

// FetchStocks returns the 20 generated stocks with current prices.
//
// Change is reported as (current price − base seed price), so the derived
// ChangePercent surfaces the oscillation as ±10% at peak/trough phases and
// 0% at base phases. High/Low span [base, peak] (or [trough, base]) so the
// daily range column matches the swing direction.
func (g *GeneratedSource) FetchStocks(_ context.Context) ([]StockWithListing, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]StockWithListing, 0, len(generatedStocks))
	for _, s := range generatedStocks {
		base := g.baseStockPx[s.Ticker]
		price := g.stockPx[s.Ticker]
		change := price.Sub(base)
		high := decimal.Max(price, base)
		low := decimal.Min(price, base)
		exchangeID := g.resolveExchangeID(s.Ticker)
		volume := hashVolume("stock:"+s.Ticker, 100_000, 50_000_000)
		out = append(out, StockWithListing{
			Stock: model.Stock{
				Ticker:      s.Ticker,
				Name:        s.Name,
				Price:       price,
				High:        high,
				Low:         low,
				Change:      change,
				Volume:      volume,
				LastRefresh: g.now,
				ExchangeID:  exchangeID,
			},
			ExchangeID:  exchangeID,
			Price:       price,
			High:        high,
			Low:         low,
			Volume:      volume,
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
		base := g.baseFuturesPx[f.Ticker]
		price := g.futuresPx[f.Ticker]
		change := price.Sub(base)
		high := decimal.Max(price, base)
		low := decimal.Min(price, base)
		unit, ok := futuresContractUnit[f.Ticker]
		if !ok {
			unit = "contract"
		}
		exchangeID := g.resolveExchangeID(f.Ticker)
		volume := hashVolume("futures:"+f.Ticker, 1_000, 500_000)
		out = append(out, FuturesWithListing{
			Futures: model.FuturesContract{
				Ticker:         f.Ticker,
				Name:           f.Name,
				ContractSize:   f.ContractSize,
				ContractUnit:   unit,
				Price:          price,
				High:           high,
				Low:            low,
				Change:         change,
				Volume:         volume,
				LastRefresh:    g.now,
				SettlementDate: futuresSettlementDate(g.now, f.DaysToExpiry),
				ExchangeID:     exchangeID,
			},
			ExchangeID:  exchangeID,
			Price:       price,
			High:        high,
			Low:         low,
			Volume:      volume,
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
			exchangeID := g.resolveExchangeID(pair)
			volume := hashVolume("forex:"+pair, 100_000_000, 10_000_000_000)
			out = append(out, ForexWithListing{
				Forex: model.ForexPair{
					Ticker:        pair,
					Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
					BaseCurrency:  base,
					QuoteCurrency: quote,
					ExchangeRate:  price, // ForexPair uses ExchangeRate, not Price
					High:          price,
					Low:           price,
					Liquidity:     liquidity,
					Volume:        volume,
					ExchangeID:    exchangeID,
					LastRefresh:   g.now,
				},
				ExchangeID:  exchangeID,
				Price:       price,
				High:        price,
				Low:         price,
				Volume:      volume,
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

// RefreshPrices recomputes stock and futures prices to match the current
// 4-minute oscillation phase. Forex rates are intentionally left untouched
// because cross-currency conversion (exchange-service.Convert) is used to
// price buy/sell fills and fee math — swinging forex ±10% would distort
// balances and break parity with the exchange-service.
func (g *GeneratedSource) RefreshPrices(_ context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.applyOscillationLocked()
	return nil
}
