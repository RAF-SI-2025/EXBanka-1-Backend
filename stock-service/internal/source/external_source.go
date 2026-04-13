package source

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
)

// ExchangeByAcronym resolves an exchange acronym (e.g. "NYSE", "FOREX") to its
// database ID. ExternalSource receives this as a constructor argument so the
// source package does not import repository.
type ExchangeByAcronym func(acronym string) (uint64, error)

// SupportedCurrencies matches the 8 currencies supported by exchange-service.
// Kept in sync with service.SupportedCurrencies.
var SupportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

// ExternalSource wraps the existing external provider clients. It produces
// the same data as the pre-refactor sync path — this implementation is a
// straight port of the bodies of SecuritySyncService.syncExchanges/syncStocks/
// seedForexPairs/seedFutures/generateAllOptions.
//
// Each FetchX method returns a slice for the caller to upsert; it does NOT
// write to any repository itself.
type ExternalSource struct {
	alpaca      *provider.AlpacaClient
	finnhub     *provider.FinnhubClient
	eodhd       *provider.EODHDClient
	av          *provider.AlphaVantageClient
	csvPath     string
	futuresPath string
	// exchangeByAcronym resolves exchange acronyms to IDs; may be nil in tests.
	exchangeByAcronym ExchangeByAcronym
}

// NewExternalSource constructs an ExternalSource. exchangeByAcronym may be nil
// (FetchStocks/FetchFutures/FetchForex will return empty slices in that case).
func NewExternalSource(
	alpaca *provider.AlpacaClient,
	finnhub *provider.FinnhubClient,
	eodhd *provider.EODHDClient,
	av *provider.AlphaVantageClient,
	csvPath string,
	futuresPath string,
) *ExternalSource {
	return &ExternalSource{
		alpaca:      alpaca,
		finnhub:     finnhub,
		eodhd:       eodhd,
		av:          av,
		csvPath:     csvPath,
		futuresPath: futuresPath,
	}
}

// WithExchangeResolver attaches the exchange-lookup function used by
// FetchStocks, FetchFutures, and FetchForex to resolve acronyms to IDs.
func (s *ExternalSource) WithExchangeResolver(fn ExchangeByAcronym) *ExternalSource {
	s.exchangeByAcronym = fn
	return s
}

func (s *ExternalSource) Name() string { return "external" }

// FetchExchanges loads exchanges from the CSV file and, if an EODHD client is
// present, enriches with additional MICs from the EODHD exchange list.
// This is a port of SecuritySyncService.syncExchanges — it returns the merged
// list; the caller is responsible for upserting into the DB.
func (s *ExternalSource) FetchExchanges(ctx context.Context) ([]model.StockExchange, error) {
	csvExchanges, csvErr := provider.LoadExchangesFromCSVFile(s.csvPath)
	csvByMIC := make(map[string]*model.StockExchange)
	if csvErr == nil {
		for i := range csvExchanges {
			csvByMIC[csvExchanges[i].MICCode] = &csvExchanges[i]
		}
	} else {
		log.Printf("WARN: failed to load exchanges from CSV: %v", csvErr)
	}

	// Start with CSV exchanges (they have correct acronyms and trading hours).
	result := make([]model.StockExchange, 0, len(csvExchanges))
	if csvErr == nil {
		result = append(result, csvExchanges...)
	}

	// Merge additional MICs from EODHD (without overwriting existing CSV entries).
	if s.eodhd != nil {
		eohdExchanges, err := s.eodhd.FetchExchanges()
		if err != nil {
			log.Printf("WARN: EODHD FetchExchanges failed: %v", err)
		} else {
			for _, ex := range eohdExchanges {
				mics := strings.Split(ex.OperatingMIC, ",")
				for _, mic := range mics {
					mic = strings.TrimSpace(mic)
					if mic == "" {
						continue
					}
					// Skip MICs already seeded from CSV.
					if _, ok := csvByMIC[mic]; ok {
						continue
					}
					result = append(result, model.StockExchange{
						Name:     ex.Name,
						Acronym:  ex.Code,
						MICCode:  mic,
						Polity:   ex.Country,
						Currency: ex.Currency,
					})
				}
			}
		}
	} else {
		log.Println("WARN: no EODHD API key — using CSV exchanges only")
	}

	if csvErr != nil && len(result) == 0 {
		return nil, fmt.Errorf("fetch exchanges: %w", csvErr)
	}
	return result, nil
}

// FetchStocks is a port of SecuritySyncService.syncStocks.
// It fetches stock tickers from Alpaca (fallback: DefaultStockTickers) and
// enriches each with quote+overview data from AlphaVantage.
// Exchange IDs are resolved via the ExchangeByAcronym resolver.
// Returns a slice of StockWithListing for the caller to upsert.
func (s *ExternalSource) FetchStocks(ctx context.Context) ([]StockWithListing, error) {
	if s.exchangeByAcronym == nil {
		log.Println("WARN: ExternalSource.FetchStocks: no exchange resolver — returning empty")
		return nil, nil
	}

	if s.av == nil {
		log.Println("WARN: no AlphaVantage API key — returning default stocks")
		return s.fetchDefaultStocks()
	}

	tickers := s.getStockTickers()

	var results []StockWithListing
	for _, ticker := range tickers {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		stockData, err := s.av.FetchStockData(ticker)
		if err != nil {
			log.Printf("WARN: failed to fetch stock %s: %v", ticker, err)
			continue
		}

		exchangeAcronym := "NYSE"
		overview, err := s.av.FetchOverview(ticker)
		if err == nil && overview.Exchange != "" {
			exchangeAcronym = overview.Exchange
		}

		exchangeID, err := s.exchangeByAcronym(exchangeAcronym)
		if err != nil {
			log.Printf("WARN: exchange %s not found for stock %s, skipping", exchangeAcronym, ticker)
			continue
		}
		stockData.ExchangeID = exchangeID

		results = append(results, StockWithListing{
			Stock:       *stockData,
			ExchangeID:  exchangeID,
			Price:       stockData.Price,
			High:        stockData.High,
			Low:         stockData.Low,
			LastRefresh: stockData.LastRefresh,
		})

		// Rate limit: AlphaVantage free tier allows 5 calls/min.
		time.Sleep(12 * time.Second)
	}

	log.Printf("fetched %d stocks from external providers", len(results))
	return results, nil
}

func (s *ExternalSource) getStockTickers() []string {
	if s.alpaca != nil {
		assets, err := s.alpaca.FetchAssets()
		if err != nil {
			log.Printf("WARN: Alpaca FetchAssets failed: %v — using default tickers", err)
		} else if len(assets) > 0 {
			tickers := make([]string, len(assets))
			for i, a := range assets {
				tickers[i] = a.Symbol
			}
			log.Printf("fetched %d stock tickers from Alpaca", len(tickers))
			return tickers
		}
	} else {
		log.Println("WARN: no Alpaca API key — using default stock tickers")
	}
	return provider.DefaultStockTickers
}

func (s *ExternalSource) fetchDefaultStocks() ([]StockWithListing, error) {
	if s.exchangeByAcronym == nil {
		return nil, nil
	}

	nyseID, _ := s.exchangeByAcronym("NYSE")
	nasdaqID, _ := s.exchangeByAcronym("NASDAQ")

	defaults := []model.Stock{
		{Ticker: "AAPL", Name: "Apple Inc.", OutstandingShares: 15000000000, DividendYield: decimal.NewFromFloat(0.005), Price: decimal.NewFromFloat(165.00), High: decimal.NewFromFloat(167.50), Low: decimal.NewFromFloat(163.20), Change: decimal.NewFromFloat(-2.30), Volume: 50000},
		{Ticker: "MSFT", Name: "Microsoft Corporation", OutstandingShares: 7400000000, DividendYield: decimal.NewFromFloat(0.008), Price: decimal.NewFromFloat(420.00), High: decimal.NewFromFloat(425.00), Low: decimal.NewFromFloat(418.00), Change: decimal.NewFromFloat(2.00), Volume: 35000},
		{Ticker: "GOOGL", Name: "Alphabet Inc.", OutstandingShares: 5900000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(175.00), High: decimal.NewFromFloat(177.00), Low: decimal.NewFromFloat(173.50), Change: decimal.NewFromFloat(1.50), Volume: 28000},
		{Ticker: "AMZN", Name: "Amazon.com Inc.", OutstandingShares: 10300000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(185.00), High: decimal.NewFromFloat(187.00), Low: decimal.NewFromFloat(183.00), Change: decimal.NewFromFloat(-1.00), Volume: 40000},
		{Ticker: "TSLA", Name: "Tesla Inc.", OutstandingShares: 3200000000, DividendYield: decimal.Zero, Price: decimal.NewFromFloat(175.00), High: decimal.NewFromFloat(180.00), Low: decimal.NewFromFloat(170.00), Change: decimal.NewFromFloat(5.00), Volume: 60000},
		{Ticker: "META", Name: "Meta Platforms Inc.", OutstandingShares: 2570000000, DividendYield: decimal.NewFromFloat(0.004), Price: decimal.NewFromFloat(500.00), High: decimal.NewFromFloat(505.00), Low: decimal.NewFromFloat(495.00), Change: decimal.NewFromFloat(3.00), Volume: 25000},
		{Ticker: "NVDA", Name: "NVIDIA Corporation", OutstandingShares: 24500000000, DividendYield: decimal.NewFromFloat(0.0003), Price: decimal.NewFromFloat(950.00), High: decimal.NewFromFloat(960.00), Low: decimal.NewFromFloat(940.00), Change: decimal.NewFromFloat(10.00), Volume: 45000},
		{Ticker: "JPM", Name: "JPMorgan Chase & Co.", OutstandingShares: 2870000000, DividendYield: decimal.NewFromFloat(0.022), Price: decimal.NewFromFloat(200.00), High: decimal.NewFromFloat(202.00), Low: decimal.NewFromFloat(198.00), Change: decimal.NewFromFloat(1.00), Volume: 15000},
	}

	nasdaqTickers := map[string]bool{"AAPL": true, "MSFT": true, "GOOGL": true, "AMZN": true, "TSLA": true, "META": true, "NVDA": true}

	now := time.Now()
	var results []StockWithListing
	for i := range defaults {
		defaults[i].LastRefresh = now
		exchangeID := nyseID
		if nasdaqTickers[defaults[i].Ticker] && nasdaqID != 0 {
			exchangeID = nasdaqID
		}
		defaults[i].ExchangeID = exchangeID

		results = append(results, StockWithListing{
			Stock:       defaults[i],
			ExchangeID:  exchangeID,
			Price:       defaults[i].Price,
			High:        defaults[i].High,
			Low:         defaults[i].Low,
			LastRefresh: now,
		})
	}

	log.Printf("returning %d default stocks", len(results))
	return results, nil
}

// FetchFutures is a port of SecuritySyncService.seedFutures.
// It reads futures contracts from the JSON seed file and resolves exchange IDs.
// Returns a slice of FuturesWithListing for the caller to upsert.
func (s *ExternalSource) FetchFutures(ctx context.Context) ([]FuturesWithListing, error) {
	rows, err := provider.LoadFuturesFromJSON(s.futuresPath)
	if err != nil {
		return nil, fmt.Errorf("fetch futures: %w", err)
	}

	if s.exchangeByAcronym == nil {
		log.Println("WARN: ExternalSource.FetchFutures: no exchange resolver — skipping exchange ID resolution")
	}

	var results []FuturesWithListing
	for _, row := range rows {
		var exchangeID uint64
		if s.exchangeByAcronym != nil {
			id, err := s.exchangeByAcronym(row.ExchangeAcronym)
			if err != nil {
				log.Printf("WARN: exchange %s not found for futures %s, skipping",
					row.ExchangeAcronym, row.Contract.Ticker)
				continue
			}
			exchangeID = id
			row.Contract.ExchangeID = exchangeID
		}

		results = append(results, FuturesWithListing{
			Futures:     row.Contract,
			ExchangeID:  exchangeID,
			Price:       row.Contract.Price,
			High:        row.Contract.High,
			Low:         row.Contract.Low,
			LastRefresh: row.Contract.LastRefresh,
		})
	}

	log.Printf("fetched %d futures contracts from JSON seed", len(results))
	return results, nil
}

// FetchForex is a port of SecuritySyncService.seedForexPairs.
// It fetches forex pairs from Finnhub (fallback: hardcoded pairs for SupportedCurrencies).
// Returns a slice of ForexWithListing for the caller to upsert.
func (s *ExternalSource) FetchForex(ctx context.Context) ([]ForexWithListing, error) {
	if s.exchangeByAcronym == nil {
		log.Println("WARN: ExternalSource.FetchForex: no exchange resolver — returning empty")
		return nil, nil
	}

	forexExchangeID, err := s.exchangeByAcronym("FOREX")
	if err != nil {
		log.Println("WARN: no FOREX exchange found — forex pairs will not have exchange association")
		return nil, nil
	}

	if s.finnhub != nil {
		results, ok := s.fetchForexFromFinnhub(forexExchangeID)
		if ok {
			return results, nil
		}
	} else {
		log.Println("WARN: no Finnhub API key — seeding hardcoded forex pairs")
	}

	return s.fetchHardcodedForexPairs(forexExchangeID), nil
}

func (s *ExternalSource) fetchForexFromFinnhub(forexExchangeID uint64) ([]ForexWithListing, bool) {
	symbols, err := s.finnhub.FetchForexSymbols()
	if err != nil {
		log.Printf("WARN: Finnhub FetchForexSymbols failed: %v — falling back to hardcoded", err)
		return nil, false
	}

	rates := make(map[string]map[string]float64)
	for _, base := range SupportedCurrencies {
		r, err := s.finnhub.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: Finnhub FetchForexRates(%s) failed: %v", base, err)
			continue
		}
		rates[base] = r
	}

	supported := make(map[string]bool)
	for _, c := range SupportedCurrencies {
		supported[c] = true
	}

	var results []ForexWithListing
	for _, sym := range symbols {
		parts := strings.SplitN(sym.DisplaySymbol, "/", 2)
		if len(parts) != 2 {
			continue
		}
		base, quote := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if !supported[base] || !supported[quote] {
			continue
		}

		rate := float64(0)
		if baseRates, ok := rates[base]; ok {
			if r, ok := baseRates[quote]; ok {
				rate = r
			}
		}

		liquidity := "medium"
		if isMajorPair(base, quote) {
			liquidity = "high"
		} else if isExoticPair(base, quote) {
			liquidity = "low"
		}

		exchangeRate := decimal.NewFromFloat(rate)
		fp := model.ForexPair{
			Ticker:        fmt.Sprintf("%s/%s", base, quote),
			Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
			BaseCurrency:  base,
			QuoteCurrency: quote,
			ExchangeRate:  exchangeRate,
			Liquidity:     liquidity,
			ExchangeID:    forexExchangeID,
			LastRefresh:   time.Now(),
		}

		results = append(results, ForexWithListing{
			Forex:       fp,
			ExchangeID:  forexExchangeID,
			Price:       exchangeRate,
			LastRefresh: fp.LastRefresh,
		})
	}

	if len(results) == 0 {
		log.Println("WARN: no supported forex pairs found from Finnhub — falling back")
		return nil, false
	}

	log.Printf("fetched %d forex pairs from Finnhub", len(results))
	return results, true
}

func (s *ExternalSource) fetchHardcodedForexPairs(forexExchangeID uint64) []ForexWithListing {
	var results []ForexWithListing
	now := time.Now()

	for _, base := range SupportedCurrencies {
		for _, quote := range SupportedCurrencies {
			if base == quote {
				continue
			}
			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}
			fp := model.ForexPair{
				Ticker:        fmt.Sprintf("%s/%s", base, quote),
				Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
				BaseCurrency:  base,
				QuoteCurrency: quote,
				ExchangeRate:  decimal.Zero,
				Liquidity:     liquidity,
				ExchangeID:    forexExchangeID,
				LastRefresh:   now,
			}
			results = append(results, ForexWithListing{
				Forex:       fp,
				ExchangeID:  forexExchangeID,
				Price:       decimal.Zero,
				LastRefresh: now,
			})
		}
	}

	log.Printf("returning %d hardcoded forex pairs", len(results))
	return results
}

// FetchOptions generates option contracts for a single underlying stock using
// the algorithmic approach. It delegates to GenerateOptionsForStock from
// option_generator.go.
func (s *ExternalSource) FetchOptions(_ context.Context, stock *model.Stock) ([]model.Option, error) {
	return GenerateOptionsForStock(stock), nil
}

// RefreshPrices is a no-op for the external source. The existing refresh
// loop in SecuritySyncService already handles its own cadence for external
// providers — we leave that in place untouched.
func (s *ExternalSource) RefreshPrices(_ context.Context) error {
	return nil
}

// --- helpers (mirror of service package helpers) ---

func currencyName(code string) string {
	names := map[string]string{
		"RSD": "Serbian Dinar", "EUR": "Euro", "CHF": "Swiss Franc",
		"USD": "US Dollar", "GBP": "British Pound", "JPY": "Japanese Yen",
		"CAD": "Canadian Dollar", "AUD": "Australian Dollar",
	}
	if n, ok := names[code]; ok {
		return n
	}
	return code
}

func isMajorPair(base, quote string) bool {
	majors := map[string]bool{"EUR": true, "USD": true, "GBP": true, "JPY": true}
	return majors[base] && majors[quote]
}

func isExoticPair(base, quote string) bool {
	exotic := map[string]bool{"RSD": true}
	return exotic[base] || exotic[quote]
}
