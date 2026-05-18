package source

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// generatedExchanges — 20 real-sounding global exchanges.
// Note: StockExchange requires Polity, Currency, TimeZone, OpenTime, CloseTime
// (all not-null at DB level). Those fields default to zero-value strings here;
// the downstream GeneratedSource (Task 3.2) will populate them before persisting.
var generatedExchanges = []model.StockExchange{
	{Acronym: "NYSE", MICCode: "XNYS", Name: "New York Stock Exchange"},
	{Acronym: "NASDAQ", MICCode: "XNAS", Name: "Nasdaq"},
	{Acronym: "LSE", MICCode: "XLON", Name: "London Stock Exchange"},
	{Acronym: "TSE", MICCode: "XTKS", Name: "Tokyo Stock Exchange"},
	{Acronym: "HKEX", MICCode: "XHKG", Name: "Hong Kong Stock Exchange"},
	{Acronym: "SSE", MICCode: "XSHG", Name: "Shanghai Stock Exchange"},
	{Acronym: "EURONEXT", MICCode: "XAMS", Name: "Euronext"},
	{Acronym: "TSX", MICCode: "XTSE", Name: "Toronto Stock Exchange"},
	{Acronym: "BSE", MICCode: "XBOM", Name: "Bombay Stock Exchange"},
	{Acronym: "ASX", MICCode: "XASX", Name: "Australian Securities Exchange"},
	{Acronym: "JSE", MICCode: "XJSE", Name: "Johannesburg Stock Exchange"},
	{Acronym: "BMV", MICCode: "XMEX", Name: "Mexican Stock Exchange"},
	{Acronym: "BVMF", MICCode: "BVMF", Name: "B3 Brazil"},
	{Acronym: "KRX", MICCode: "XKRX", Name: "Korea Exchange"},
	{Acronym: "BME", MICCode: "XMAD", Name: "Bolsas y Mercados Espanoles"},
	{Acronym: "SIX", MICCode: "XSWX", Name: "SIX Swiss Exchange"},
	{Acronym: "OMX", MICCode: "XSTO", Name: "Nasdaq Nordic"},
	{Acronym: "WSE", MICCode: "XWAR", Name: "Warsaw Stock Exchange"},
	{Acronym: "BVC", MICCode: "XBOG", Name: "Colombia Stock Exchange"},
	{Acronym: "MOEX", MICCode: "MISX", Name: "Moscow Exchange"},
}

// stockSeed is an internal helper for the generated stock table.
type stockSeed struct {
	Ticker string
	Name   string
	Price  float64
}

// generatedStocks — 20 well-known real stocks with rough snapshot prices.
var generatedStocks = []stockSeed{
	{"AAPL", "Apple Inc.", 180.00},
	{"MSFT", "Microsoft Corporation", 420.00},
	{"GOOGL", "Alphabet Inc. Class A", 155.00},
	{"AMZN", "Amazon.com Inc.", 185.00},
	{"META", "Meta Platforms Inc.", 510.00},
	{"NVDA", "NVIDIA Corporation", 900.00},
	{"TSLA", "Tesla Inc.", 175.00},
	{"JPM", "JPMorgan Chase & Co.", 200.00},
	{"V", "Visa Inc.", 280.00},
	{"JNJ", "Johnson & Johnson", 155.00},
	{"WMT", "Walmart Inc.", 60.00},
	{"PG", "Procter & Gamble", 160.00},
	{"MA", "Mastercard Inc.", 470.00},
	{"HD", "The Home Depot Inc.", 380.00},
	{"DIS", "The Walt Disney Company", 115.00},
	{"BAC", "Bank of America Corp.", 38.00},
	{"XOM", "Exxon Mobil Corporation", 115.00},
	{"KO", "The Coca-Cola Company", 62.00},
	{"PEP", "PepsiCo Inc.", 175.00},
	{"CSCO", "Cisco Systems Inc.", 50.00},
}

// futuresSeed is an internal helper for the generated futures table.
type futuresSeed struct {
	Ticker       string
	Name         string
	ContractSize int64
	Price        float64
	DaysToExpiry int
}

// generatedFutures — 20 commodity / index / rates / FX futures.
var generatedFutures = []futuresSeed{
	{"CL", "Crude Oil WTI", 1000, 85.00, 45},
	{"GC", "Gold", 100, 2380.00, 60},
	{"SI", "Silver", 5000, 28.00, 60},
	{"NG", "Natural Gas Henry Hub", 10000, 2.40, 45},
	{"HG", "Copper", 25000, 4.35, 60},
	{"ZC", "Corn", 5000, 4.80, 90},
	{"ZS", "Soybean", 5000, 11.80, 90},
	{"ZW", "Wheat", 5000, 5.50, 90},
	{"CC", "Cocoa", 10, 9500.00, 75},
	{"KC", "Coffee", 37500, 2.10, 75},
	{"SB", "Sugar #11", 112000, 0.23, 75},
	{"CT", "Cotton #2", 50000, 0.82, 75},
	{"ES", "E-mini S&P 500", 50, 5200.00, 30},
	{"NQ", "E-mini Nasdaq-100", 20, 18200.00, 30},
	{"YM", "E-mini Dow", 5, 39000.00, 30},
	{"RTY", "E-mini Russell 2000", 50, 2080.00, 30},
	{"ZB", "30Y US Treasury Bond", 1000, 120.50, 60},
	{"ZN", "10Y US T-Note", 1000, 110.75, 60},
	{"6E", "Euro FX", 125000, 1.085, 30},
	{"6J", "Japanese Yen", 12500000, 0.0066, 30},
}

// supportedCurrencies is the fixed set of 8 currencies used by exchange-service.
// Delegates to model.SupportedCurrencies so the list has a single source of truth.
var supportedCurrencies = model.SupportedCurrencies

// forexSeedPrices maps "BASE/QUOTE" → realistic mid price. 56 ordered pairs
// (8 currencies × 7 counterparties).
// Note: ForexPair uses ExchangeRate (not Price) for the rate field.
var forexSeedPrices = map[string]float64{
	"USD/EUR": 0.925, "EUR/USD": 1.080,
	"USD/GBP": 0.790, "GBP/USD": 1.265,
	"USD/JPY": 152.0, "JPY/USD": 0.00658,
	"USD/CHF": 0.905, "CHF/USD": 1.105,
	"USD/CAD": 1.360, "CAD/USD": 0.735,
	"USD/AUD": 1.530, "AUD/USD": 0.653,
	"USD/RSD": 108.0, "RSD/USD": 0.00925,
	"EUR/GBP": 0.855, "GBP/EUR": 1.170,
	"EUR/JPY": 164.0, "JPY/EUR": 0.00610,
	"EUR/CHF": 0.975, "CHF/EUR": 1.025,
	"EUR/CAD": 1.470, "CAD/EUR": 0.680,
	"EUR/AUD": 1.655, "AUD/EUR": 0.604,
	"EUR/RSD": 117.0, "RSD/EUR": 0.00855,
	"GBP/JPY": 192.0, "JPY/GBP": 0.00520,
	"GBP/CHF": 1.145, "CHF/GBP": 0.873,
	"GBP/CAD": 1.720, "CAD/GBP": 0.581,
	"GBP/AUD": 1.935, "AUD/GBP": 0.517,
	"GBP/RSD": 137.0, "RSD/GBP": 0.00730,
	"JPY/CHF": 0.00596, "CHF/JPY": 168.0,
	"JPY/CAD": 0.00895, "CAD/JPY": 111.7,
	"JPY/AUD": 0.01007, "AUD/JPY": 99.3,
	"JPY/RSD": 0.711, "RSD/JPY": 1.407,
	"CHF/CAD": 1.502, "CAD/CHF": 0.666,
	"CHF/AUD": 1.690, "AUD/CHF": 0.592,
	"CHF/RSD": 119.5, "RSD/CHF": 0.00837,
	"CAD/AUD": 1.125, "AUD/CAD": 0.889,
	"CAD/RSD": 79.4, "RSD/CAD": 0.01259,
	"AUD/RSD": 70.6, "RSD/AUD": 0.01417,
}

// dec is a small helper to keep the tables above readable.
func dec(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// futuresSettlementDate computes a deterministic settlement date.
func futuresSettlementDate(now time.Time, daysOut int) time.Time {
	return now.AddDate(0, 0, daysOut)
}
