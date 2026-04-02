package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
	"github.com/exbanka/stock-service/internal/repository"
)

// SupportedCurrencies matches the 8 currencies supported by exchange-service.
var SupportedCurrencies = []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}

type SecuritySyncService struct {
	stockRepo     StockRepo
	futuresRepo   FuturesRepo
	forexRepo     ForexPairRepo
	optionRepo    OptionRepo
	exchangeRepo  *repository.ExchangeRepository
	settingRepo   SettingRepo
	avClient      *provider.AlphaVantageClient
	eodhClient    *provider.EODHDClient
	alpacaClient  *provider.AlpacaClient
	finnhubClient *provider.FinnhubClient
	listingSvc    *ListingService
	csvPath       string
}

func NewSecuritySyncService(
	stockRepo StockRepo,
	futuresRepo FuturesRepo,
	forexRepo ForexPairRepo,
	optionRepo OptionRepo,
	exchangeRepo *repository.ExchangeRepository,
	settingRepo SettingRepo,
	avClient *provider.AlphaVantageClient,
	eodhClient *provider.EODHDClient,
	alpacaClient *provider.AlpacaClient,
	finnhubClient *provider.FinnhubClient,
	listingSvc *ListingService,
	csvPath string,
) *SecuritySyncService {
	return &SecuritySyncService{
		stockRepo:     stockRepo,
		futuresRepo:   futuresRepo,
		forexRepo:     forexRepo,
		optionRepo:    optionRepo,
		exchangeRepo:  exchangeRepo,
		settingRepo:   settingRepo,
		avClient:      avClient,
		eodhClient:    eodhClient,
		alpacaClient:  alpacaClient,
		finnhubClient: finnhubClient,
		listingSvc:    listingSvc,
		csvPath:       csvPath,
	}
}

// SeedAll runs the full initial data seed.
func (s *SecuritySyncService) SeedAll(ctx context.Context, futuresSeedPath string) {
	s.syncExchanges()
	s.syncStocks(ctx)
	s.seedForexPairs()
	s.seedFutures(futuresSeedPath)
	s.generateAllOptions()
	// Sync listings from the securities we just seeded
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
}

// RefreshPrices updates price data for all securities.
// Called periodically by the refresh goroutine.
func (s *SecuritySyncService) RefreshPrices(ctx context.Context) {
	if s.isTestingMode() {
		log.Println("testing mode enabled — skipping external API price refresh")
		return
	}
	s.syncStockPrices(ctx)
	s.refreshForexRates()
	if s.listingSvc != nil {
		s.listingSvc.SyncListingsFromSecurities()
	}
	log.Println("price refresh complete")
}

// StartPeriodicRefresh launches a background goroutine that refreshes prices.
func (s *SecuritySyncService) StartPeriodicRefresh(ctx context.Context, intervalMins int) {
	if intervalMins <= 0 {
		intervalMins = 15
	}
	ticker := time.NewTicker(time.Duration(intervalMins) * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.RefreshPrices(ctx)
			case <-ctx.Done():
				log.Println("stopping periodic security price refresh")
				return
			}
		}
	}()
	log.Printf("periodic security price refresh started (every %d min)", intervalMins)
}

func (s *SecuritySyncService) isTestingMode() bool {
	val, err := s.settingRepo.Get("testing_mode")
	if err != nil {
		return false
	}
	return val == "true"
}

// --- Exchanges ---

func (s *SecuritySyncService) syncExchanges() {
	if s.eodhClient != nil {
		exchanges, err := s.eodhClient.FetchExchanges()
		if err != nil {
			log.Printf("WARN: EODHD FetchExchanges failed: %v — falling back to CSV", err)
		} else {
			synced := 0
			for _, ex := range exchanges {
				details, err := s.eodhClient.FetchExchangeDetails(ex.Code)
				if err != nil {
					log.Printf("WARN: EODHD FetchExchangeDetails(%s) failed: %v", ex.Code, err)
					continue
				}
				s.upsertExchangeFromEODHD(ex, details)
				synced++
			}
			if synced > 0 {
				log.Printf("synced %d exchanges from EODHD API", synced)
				return
			}
			log.Println("WARN: no exchanges synced from EODHD — falling back to CSV")
		}
	} else {
		log.Println("WARN: no EODHD API key — falling back to CSV for exchanges")
	}

	// Fallback: CSV
	csvExchanges, err := provider.LoadExchangesFromCSVFile(s.csvPath)
	if err != nil {
		log.Printf("WARN: failed to load exchanges from CSV: %v", err)
		return
	}
	for _, ex := range csvExchanges {
		ex := ex
		if err := s.exchangeRepo.UpsertByMICCode(&ex); err != nil {
			log.Printf("WARN: failed to upsert exchange %s: %v", ex.MICCode, err)
		}
	}
	log.Printf("seeded %d exchanges from CSV fallback", len(csvExchanges))
}

func (s *SecuritySyncService) upsertExchangeFromEODHD(ex provider.EODHDExchange, details *provider.EODHDExchangeDetails) {
	mics := strings.Split(ex.OperatingMIC, ",")
	openTime := strings.TrimSuffix(details.TradingHours.OpenHour, ":00")
	closeTime := strings.TrimSuffix(details.TradingHours.CloseHour, ":00")

	for _, mic := range mics {
		mic = strings.TrimSpace(mic)
		if mic == "" {
			continue
		}
		exchange := &model.StockExchange{
			Name:      ex.Name,
			Acronym:   ex.Code,
			MICCode:   mic,
			Polity:    ex.Country,
			Currency:  ex.Currency,
			TimeZone:  details.Timezone,
			OpenTime:  openTime,
			CloseTime: closeTime,
		}
		if err := s.exchangeRepo.UpsertByMICCode(exchange); err != nil {
			log.Printf("WARN: failed to upsert exchange MIC %s: %v", mic, err)
		}
	}
}

// --- Stocks ---

func (s *SecuritySyncService) syncStocks(ctx context.Context) {
	tickers := s.getStockTickers()

	if s.avClient == nil {
		log.Println("WARN: no AlphaVantage API key — seeding default stocks")
		s.seedDefaultStocks()
		return
	}

	for _, ticker := range tickers {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stockData, err := s.avClient.FetchStockData(ticker)
		if err != nil {
			log.Printf("WARN: failed to fetch stock %s: %v", ticker, err)
			continue
		}

		// Resolve exchange — FetchStockData already called FetchOverview internally,
		// so we use the exchange name from overview. Try to look it up.
		exchangeAcronym := "NYSE"
		overview, err := s.avClient.FetchOverview(ticker)
		if err == nil && overview.Exchange != "" {
			exchangeAcronym = overview.Exchange
		}
		exchange, err := s.exchangeRepo.GetByAcronym(exchangeAcronym)
		if err != nil {
			log.Printf("WARN: exchange %s not found for stock %s, skipping", exchangeAcronym, ticker)
			continue
		}
		stockData.ExchangeID = exchange.ID

		if err := s.stockRepo.UpsertByTicker(stockData); err != nil {
			log.Printf("WARN: failed to upsert stock %s: %v", ticker, err)
		}

		// Rate limit: AlphaVantage free tier allows 5 calls/min
		time.Sleep(12 * time.Second)
	}
	log.Printf("synced %d stock tickers", len(tickers))
}

func (s *SecuritySyncService) getStockTickers() []string {
	if s.alpacaClient != nil {
		assets, err := s.alpacaClient.FetchAssets()
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

func (s *SecuritySyncService) syncStockPrices(ctx context.Context) {
	if s.avClient == nil {
		return
	}

	stocks, _, err := s.stockRepo.List(repository.StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for price refresh: %v", err)
		return
	}

	for _, stock := range stocks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		quote, err := s.avClient.FetchQuote(stock.Ticker)
		if err != nil {
			log.Printf("WARN: failed to refresh price for %s: %v", stock.Ticker, err)
			continue
		}

		stock.Price = quote.Price
		stock.High = quote.High
		stock.Low = quote.Low
		stock.Change = quote.Change
		stock.Volume = quote.Volume
		stock.LastRefresh = time.Now()

		if err := s.stockRepo.UpsertByTicker(&stock); err != nil {
			log.Printf("WARN: failed to update stock %s price: %v", stock.Ticker, err)
		}

		time.Sleep(12 * time.Second) // rate limit
	}
}

// seedDefaultStocks creates placeholder stocks when no API key is configured.
func (s *SecuritySyncService) seedDefaultStocks() {
	nyse, _ := s.exchangeRepo.GetByAcronym("NYSE")
	nasdaq, _ := s.exchangeRepo.GetByAcronym("NASDAQ")

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

	for i := range defaults {
		defaults[i].LastRefresh = time.Now()
		// Assign to NYSE for most, NASDAQ for tech names
		if nyse != nil {
			defaults[i].ExchangeID = nyse.ID
		}
		if nasdaq != nil && (defaults[i].Ticker == "AAPL" || defaults[i].Ticker == "MSFT" ||
			defaults[i].Ticker == "GOOGL" || defaults[i].Ticker == "AMZN" ||
			defaults[i].Ticker == "TSLA" || defaults[i].Ticker == "META" || defaults[i].Ticker == "NVDA") {
			defaults[i].ExchangeID = nasdaq.ID
		}
		if err := s.stockRepo.UpsertByTicker(&defaults[i]); err != nil {
			log.Printf("WARN: failed to seed stock %s: %v", defaults[i].Ticker, err)
		}
	}
	log.Printf("seeded %d default stocks", len(defaults))
}

// --- Futures ---

func (s *SecuritySyncService) seedFutures(seedPath string) {
	rows, err := provider.LoadFuturesFromJSON(seedPath)
	if err != nil {
		log.Printf("WARN: failed to load futures seed data: %v", err)
		return
	}

	for _, row := range rows {
		exchange, err := s.exchangeRepo.GetByAcronym(row.ExchangeAcronym)
		if err != nil {
			log.Printf("WARN: exchange %s not found for futures %s, skipping",
				row.ExchangeAcronym, row.Contract.Ticker)
			continue
		}
		row.Contract.ExchangeID = exchange.ID
		if err := s.futuresRepo.UpsertByTicker(&row.Contract); err != nil {
			log.Printf("WARN: failed to upsert futures %s: %v", row.Contract.Ticker, err)
		}
	}
	log.Printf("seeded %d futures contracts from JSON", len(rows))
}

// --- Forex Pairs ---

func (s *SecuritySyncService) seedForexPairs() {
	forexExchange, err := s.exchangeRepo.GetByAcronym("FOREX")
	if err != nil {
		log.Println("WARN: no FOREX exchange found — forex pairs will not have exchange association")
		return
	}

	if s.finnhubClient != nil {
		if s.seedForexFromFinnhub(forexExchange.ID) {
			return
		}
	} else {
		log.Println("WARN: no Finnhub API key — seeding hardcoded forex pairs")
	}

	s.seedHardcodedForexPairs(forexExchange.ID)
}

func (s *SecuritySyncService) seedForexFromFinnhub(forexExchangeID uint64) bool {
	symbols, err := s.finnhubClient.FetchForexSymbols()
	if err != nil {
		log.Printf("WARN: Finnhub FetchForexSymbols failed: %v — falling back to hardcoded", err)
		return false
	}

	rates := make(map[string]map[string]float64)
	for _, base := range SupportedCurrencies {
		r, err := s.finnhubClient.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: Finnhub FetchForexRates(%s) failed: %v", base, err)
			continue
		}
		rates[base] = r
	}

	count := 0
	supported := make(map[string]bool)
	for _, c := range SupportedCurrencies {
		supported[c] = true
	}

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

		fp := &model.ForexPair{
			Ticker:        fmt.Sprintf("%s/%s", base, quote),
			Name:          fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote)),
			BaseCurrency:  base,
			QuoteCurrency: quote,
			ExchangeRate:  decimal.NewFromFloat(rate),
			Liquidity:     liquidity,
			ExchangeID:    forexExchangeID,
			LastRefresh:   time.Now(),
		}
		if err := s.forexRepo.UpsertByTicker(fp); err != nil {
			log.Printf("WARN: failed to upsert forex pair %s: %v", fp.Ticker, err)
		}
		count++
	}

	if count == 0 {
		log.Println("WARN: no supported forex pairs found from Finnhub — falling back")
		return false
	}
	log.Printf("seeded %d forex pairs from Finnhub", count)
	return true
}

func (s *SecuritySyncService) seedHardcodedForexPairs(forexExchangeID uint64) {
	count := 0
	for _, base := range SupportedCurrencies {
		for _, quote := range SupportedCurrencies {
			if base == quote {
				continue
			}
			ticker := fmt.Sprintf("%s/%s", base, quote)
			name := fmt.Sprintf("%s to %s", currencyName(base), currencyName(quote))
			liquidity := "medium"
			if isMajorPair(base, quote) {
				liquidity = "high"
			} else if isExoticPair(base, quote) {
				liquidity = "low"
			}
			fp := &model.ForexPair{
				Ticker:        ticker,
				Name:          name,
				BaseCurrency:  base,
				QuoteCurrency: quote,
				ExchangeRate:  decimal.Zero,
				Liquidity:     liquidity,
				ExchangeID:    forexExchangeID,
				LastRefresh:   time.Now(),
			}
			if err := s.forexRepo.UpsertByTicker(fp); err != nil {
				log.Printf("WARN: failed to seed forex pair %s: %v", ticker, err)
			}
			count++
		}
	}
	log.Printf("seeded %d hardcoded forex pairs", count)
}

func (s *SecuritySyncService) refreshForexRates() {
	if s.finnhubClient == nil {
		return
	}
	for _, base := range SupportedCurrencies {
		rates, err := s.finnhubClient.FetchForexRates(base)
		if err != nil {
			log.Printf("WARN: failed to refresh forex rates for %s: %v", base, err)
			continue
		}
		for quote, rate := range rates {
			ticker := fmt.Sprintf("%s/%s", base, quote)
			existing, err := s.forexRepo.GetByTicker(ticker)
			if err != nil {
				continue
			}
			existing.ExchangeRate = decimal.NewFromFloat(rate)
			existing.LastRefresh = time.Now()
			if err := s.forexRepo.Update(existing); err != nil {
				log.Printf("WARN: failed to update forex rate %s: %v", ticker, err)
			}
		}
	}
}

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

// --- Options ---

func (s *SecuritySyncService) generateAllOptions() {
	stocks, _, err := s.stockRepo.List(repository.StockFilter{Page: 1, PageSize: 1000})
	if err != nil {
		log.Printf("WARN: failed to list stocks for option generation: %v", err)
		return
	}

	totalGenerated := 0
	for _, stock := range stocks {
		stock := stock
		options := GenerateOptionsForStock(&stock)
		for _, opt := range options {
			opt := opt
			if err := s.optionRepo.UpsertByTicker(&opt); err != nil {
				log.Printf("WARN: failed to upsert option %s: %v", opt.Ticker, err)
			}
		}
		totalGenerated += len(options)
	}

	// Clean up expired options
	deleted, err := s.optionRepo.DeleteExpiredBefore(time.Now())
	if err != nil {
		log.Printf("WARN: failed to clean expired options: %v", err)
	}

	log.Printf("generated %d options for %d stocks, cleaned %d expired", totalGenerated, len(stocks), deleted)
}
