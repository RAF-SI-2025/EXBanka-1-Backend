package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// SimulatorSource implements Source by talking to Market-Simulator.
type SimulatorSource struct {
	client *SimulatorClient

	// Maps our local ticker → simulator's internal stock ID, populated
	// by FetchStocks so FetchOptions can resolve the right stock_id query.
	stockIDMu       sync.RWMutex
	stockIDByTicker map[string]uint64
}

// NewSimulatorSource creates a SimulatorSource backed by the given client.
// The client must have EnsureRegistered() called before any requests are made.
func NewSimulatorSource(client *SimulatorClient) *SimulatorSource {
	return &SimulatorSource{
		client:          client,
		stockIDByTicker: make(map[string]uint64),
	}
}

// Name returns the short identifier for this source.
func (s *SimulatorSource) Name() string { return "simulator" }

// --- HTTP helpers ---

func (s *SimulatorSource) getJSON(ctx context.Context, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.client.URL(path), nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("simulator: GET %s status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseDec(str string) decimal.Decimal {
	d, _ := decimal.NewFromString(str)
	return d
}

// --- wire types (Market-Simulator JSON shapes) ---

type msExchange struct {
	ID      uint64 `json:"id"`
	Acronym string `json:"acronym"`
	MICCode string `json:"mic_code"`
	Name    string `json:"name"`
}

type msStock struct {
	ID     uint64 `json:"id"`
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
}

type msListing struct {
	ID         uint64 `json:"id"`
	ExchangeID uint64 `json:"exchange_id"`
	Price      string `json:"price"`
}

type msFutures struct {
	ID             uint64 `json:"id"`
	Ticker         string `json:"ticker"`
	Name           string `json:"name"`
	ContractSize   int64  `json:"contract_size"`
	SettlementDate string `json:"settlement_date"`
}

type msForex struct {
	ID            uint64 `json:"id"`
	Ticker        string `json:"ticker"`
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
	Rate          string `json:"rate"`
}

type msOption struct {
	ID                uint64 `json:"id"`
	Ticker            string `json:"ticker"`
	Name              string `json:"name"`
	StockID           uint64 `json:"stock_id"`
	OptionType        string `json:"option_type"`
	StrikePrice       string `json:"strike_price"`
	ImpliedVolatility string `json:"implied_volatility"`
	Premium           string `json:"premium"`
	SettlementDate    string `json:"settlement_date"`
}

// pickLowestExchangeID sorts listings by exchange_id and returns the first.
// Returns zero-value and false when the slice is empty.
func pickLowestExchangeID(listings []msListing) (msListing, bool) {
	if len(listings) == 0 {
		return msListing{}, false
	}
	sort.Slice(listings, func(i, j int) bool {
		return listings[i].ExchangeID < listings[j].ExchangeID
	})
	return listings[0], true
}

// --- Source interface implementation ---

// FetchExchanges returns all exchanges from Market-Simulator.
// The Market-Simulator does not provide Polity/Currency/TimeZone/OpenTime/CloseTime,
// so sane defaults are applied; callers may override them after import.
func (s *SimulatorSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
	var parsed struct {
		Data []msExchange `json:"data"`
	}
	if err := s.getJSON(ctx, "/api/market/exchanges?per_page=200", &parsed); err != nil {
		return nil, err
	}
	out := make([]model.StockExchange, 0, len(parsed.Data))
	for _, e := range parsed.Data {
		out = append(out, model.StockExchange{
			Acronym:   e.Acronym,
			MICCode:   e.MICCode,
			Name:      e.Name,
			Polity:    "Unknown",
			Currency:  "USD",
			TimeZone:  "America/New_York",
			OpenTime:  "09:30",
			CloseTime: "16:00",
		})
	}
	return out, nil
}

// FetchStocks returns all stocks from Market-Simulator, each pinned to the
// listing with the lowest exchange_id. The simulator's internal stock IDs are
// cached for use by FetchOptions.
func (s *SimulatorSource) FetchStocks(ctx context.Context) ([]StockWithListing, error) {
	var parsed struct {
		Data []msStock `json:"data"`
	}
	if err := s.getJSON(ctx, "/api/market/stocks?per_page=200", &parsed); err != nil {
		return nil, err
	}

	out := make([]StockWithListing, 0, len(parsed.Data))
	for _, st := range parsed.Data {
		var listingsResp struct {
			Data []msListing `json:"data"`
		}
		if err := s.getJSON(ctx, "/api/market/stocks/"+st.Ticker+"/listings", &listingsResp); err != nil {
			// Skip stocks we can't resolve a listing for.
			continue
		}
		chosen, ok := pickLowestExchangeID(listingsResp.Data)
		if !ok {
			continue
		}
		price := parseDec(chosen.Price)

		out = append(out, StockWithListing{
			Stock: model.Stock{
				Ticker: st.Ticker,
				Name:   st.Name,
				Price:  price,
				High:   price,
				Low:    price,
			},
			ExchangeID:  chosen.ExchangeID,
			Price:       price,
			High:        price,
			Low:         price,
			LastRefresh: time.Now(),
		})

		// Cache the simulator's internal stock ID so FetchOptions can look it up.
		s.stockIDMu.Lock()
		s.stockIDByTicker[st.Ticker] = st.ID
		s.stockIDMu.Unlock()
	}
	return out, nil
}

// FetchFutures returns all futures contracts from Market-Simulator, each pinned
// to the listing with the lowest exchange_id.
func (s *SimulatorSource) FetchFutures(ctx context.Context) ([]FuturesWithListing, error) {
	var parsed struct {
		Data []msFutures `json:"data"`
	}
	if err := s.getJSON(ctx, "/api/market/futures?per_page=200", &parsed); err != nil {
		return nil, err
	}

	out := make([]FuturesWithListing, 0, len(parsed.Data))
	for _, f := range parsed.Data {
		var listingsResp struct {
			Data []msListing `json:"data"`
		}
		if err := s.getJSON(ctx, "/api/market/futures/"+f.Ticker+"/listings", &listingsResp); err != nil {
			continue
		}
		chosen, ok := pickLowestExchangeID(listingsResp.Data)
		if !ok {
			continue
		}
		price := parseDec(chosen.Price)

		settleDate, _ := time.Parse(time.RFC3339, f.SettlementDate)
		contractSize := f.ContractSize
		if contractSize == 0 {
			contractSize = 1
		}

		out = append(out, FuturesWithListing{
			Futures: model.FuturesContract{
				Ticker:         f.Ticker,
				Name:           f.Name,
				ContractSize:   contractSize,
				ContractUnit:   "contract",
				Price:          price,
				High:           price,
				Low:            price,
				SettlementDate: settleDate,
			},
			ExchangeID:  chosen.ExchangeID,
			Price:       price,
			High:        price,
			Low:         price,
			LastRefresh: time.Now(),
		})
	}
	return out, nil
}

// FetchForex returns all forex pairs from Market-Simulator, each pinned to
// the listing with the lowest exchange_id.
func (s *SimulatorSource) FetchForex(ctx context.Context) ([]ForexWithListing, error) {
	var parsed struct {
		Data []msForex `json:"data"`
	}
	if err := s.getJSON(ctx, "/api/market/forex?per_page=200", &parsed); err != nil {
		return nil, err
	}

	out := make([]ForexWithListing, 0, len(parsed.Data))
	for _, fx := range parsed.Data {
		var listingsResp struct {
			Data []msListing `json:"data"`
		}
		if err := s.getJSON(ctx, "/api/market/forex/"+fx.Ticker+"/listings", &listingsResp); err != nil {
			continue
		}
		chosen, ok := pickLowestExchangeID(listingsResp.Data)
		if !ok {
			continue
		}
		price := parseDec(chosen.Price)
		rate := parseDec(fx.Rate)

		out = append(out, ForexWithListing{
			Forex: model.ForexPair{
				Ticker:        fx.Ticker,
				Name:          fx.BaseCurrency + "/" + fx.QuoteCurrency,
				BaseCurrency:  fx.BaseCurrency,
				QuoteCurrency: fx.QuoteCurrency,
				ExchangeRate:  rate,
				Liquidity:     "medium",
				High:          price,
				Low:           price,
			},
			ExchangeID:  chosen.ExchangeID,
			Price:       price,
			High:        price,
			Low:         price,
			LastRefresh: time.Now(),
		})
	}
	return out, nil
}

// FetchOptions returns all option contracts for the given stock from Market-Simulator.
// FetchStocks must be called first to populate the internal ticker→simulatorID cache.
// If the ticker is not cached, nil is returned (no error).
func (s *SimulatorSource) FetchOptions(ctx context.Context, stock *model.Stock) ([]model.Option, error) {
	s.stockIDMu.RLock()
	simulatorID, ok := s.stockIDByTicker[stock.Ticker]
	s.stockIDMu.RUnlock()
	if !ok {
		// No mapping cached — FetchStocks wasn't called or stock not in simulator.
		return nil, nil
	}
	return s.fetchOptionsBySimulatorID(ctx, stock, simulatorID)
}

// FetchOptionsByTicker is a SimulatorSource-specific helper (not on the Source
// interface) that resolves options using the cached simulator stock_id for the
// given ticker string. Useful in tests where constructing a full model.Stock
// is inconvenient.
func (s *SimulatorSource) FetchOptionsByTicker(ctx context.Context, ticker string) ([]model.Option, error) {
	s.stockIDMu.RLock()
	simulatorID, ok := s.stockIDByTicker[ticker]
	s.stockIDMu.RUnlock()
	if !ok {
		return nil, nil
	}
	// Use a minimal stock stub — StockID on options is set to stock.ID which
	// will be 0 here (no DB ID), which is fine for the test assertion.
	stub := &model.Stock{Ticker: ticker}
	return s.fetchOptionsBySimulatorID(ctx, stub, simulatorID)
}

func (s *SimulatorSource) fetchOptionsBySimulatorID(ctx context.Context, stock *model.Stock, simulatorID uint64) ([]model.Option, error) {
	var parsed struct {
		Data []msOption `json:"data"`
	}
	path := fmt.Sprintf("/api/market/options?stock_id=%d&per_page=200", simulatorID)
	if err := s.getJSON(ctx, path, &parsed); err != nil {
		return nil, err
	}

	out := make([]model.Option, 0, len(parsed.Data))
	for _, o := range parsed.Data {
		settleDate, _ := time.Parse(time.RFC3339, o.SettlementDate)
		out = append(out, model.Option{
			Ticker:            o.Ticker,
			Name:              o.Name,
			StockID:           stock.ID, // use our local stock.ID, not simulator's
			OptionType:        o.OptionType,
			StrikePrice:       parseDec(o.StrikePrice),
			ImpliedVolatility: parseDec(o.ImpliedVolatility),
			Premium:           parseDec(o.Premium),
			SettlementDate:    settleDate,
		})
	}
	return out, nil
}

// RefreshPrices is a no-op for SimulatorSource. The sync service drives price
// refresh by calling FetchStocks/FetchFutures/FetchForex on a 3-second ticker
// and writing results back to the DB.
func (s *SimulatorSource) RefreshPrices(_ context.Context) error {
	return nil
}
