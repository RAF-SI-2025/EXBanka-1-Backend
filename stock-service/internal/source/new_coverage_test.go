// Package source — additional coverage tests.
// These tests are in the internal source package (not source_test) so they
// can reach unexported helpers: DecFromFloat, hashVolume, currencyName,
// fetchHardcodedForexPairs, fetchDefaultStocks, fetchForexFromFinnhub.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"unsafe"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
)

// ---------------------------------------------------------------------------
// DecFromFloat
// ---------------------------------------------------------------------------

func TestDecFromFloat_RoundTrip(t *testing.T) {
	f := 123.45
	got := DecFromFloat(f)
	want := decimal.NewFromFloat(f)
	if !got.Equal(want) {
		t.Errorf("DecFromFloat(%v) = %s, want %s", f, got, want)
	}
}

func TestDecFromFloat_Zero(t *testing.T) {
	if !DecFromFloat(0).IsZero() {
		t.Error("expected zero")
	}
}

// ---------------------------------------------------------------------------
// hashVolume — edge: maxVol <= minVol
// ---------------------------------------------------------------------------

func TestHashVolume_MaxLessThanMin_ReturnsMin(t *testing.T) {
	got := hashVolume("test", 100, 50) // maxVol < minVol → returns minVol
	if got != 100 {
		t.Errorf("got %d, want 100", got)
	}
}

func TestHashVolume_MaxEqualsMin_ReturnsMin(t *testing.T) {
	got := hashVolume("test", 42, 42)
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestHashVolume_NormalRange(t *testing.T) {
	for i := 0; i < 20; i++ {
		got := hashVolume("ticker"+string(rune('A'+i)), 100, 1000)
		if got < 100 || got > 1000 {
			t.Errorf("hashVolume out of range: got %d", got)
		}
	}
}

// ---------------------------------------------------------------------------
// currencyName — unknown code returns the code itself
// ---------------------------------------------------------------------------

func TestCurrencyName_KnownCode(t *testing.T) {
	if got := currencyName("USD"); got != "US Dollar" {
		t.Errorf("got %q", got)
	}
}

func TestCurrencyName_UnknownCode_ReturnsSelf(t *testing.T) {
	if got := currencyName("XYZ"); got != "XYZ" {
		t.Errorf("expected XYZ, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// GeneratedSource.FetchExchanges — fallback branch (unknown acronym → "Unknown")
// ---------------------------------------------------------------------------

func TestGeneratedSource_FetchExchanges_FallbackBranch(t *testing.T) {
	// generatedExchanges lists known acronyms. We exercise the fallback by temporarily
	// emptying exchangeDefaults for one acronym. Since exchangeDefaults is a package var
	// we do it with a controlled fake source: just verify the known path runs. The fallback
	// "Unknown" branch is triggered whenever an exchange acronym in generatedExchanges
	// has no entry in exchangeDefaults. We can force that by injecting a temp entry.
	//
	// Simpler approach: since all 20 generated exchanges have entries in exchangeDefaults,
	// we directly call FetchExchanges and confirm it returns normalised currency — this
	// covers the existing covered path. To hit the fallback we temporarily remove one key.
	saved, ok := exchangeDefaults["NYSE"]
	if !ok {
		t.Skip("NYSE not in exchangeDefaults")
	}
	delete(exchangeDefaults, "NYSE")
	t.Cleanup(func() { exchangeDefaults["NYSE"] = saved })

	g := NewGeneratedSource()
	exs, err := g.FetchExchanges(context.Background())
	if err != nil {
		t.Fatalf("FetchExchanges: %v", err)
	}
	// The NYSE entry will now hit the else branch → currency becomes "USD" (fallback).
	found := false
	for _, e := range exs {
		if e.Acronym == "NYSE" {
			found = true
			if e.Polity != "Unknown" {
				t.Errorf("expected Polity=Unknown for missing-defaults exchange, got %q", e.Polity)
			}
		}
	}
	if !found {
		t.Error("NYSE exchange not found in output")
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchExchanges — CSV fails and no EODHD → error path
// ---------------------------------------------------------------------------

func TestExternalSource_FetchExchanges_CSVFails_NoEODHD_Error(t *testing.T) {
	s := NewExternalSource(nil, nil, nil, nil, "/nonexistent_path.csv", "")
	_, err := s.FetchExchanges(context.Background())
	if err == nil {
		t.Fatal("expected error when CSV fails and no EODHD client")
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchStocks — with resolver but no AV (fetchDefaultStocks)
//   nasdaqID == 0 → NASDAQ tickers still fall through to nyseID
// ---------------------------------------------------------------------------

func TestExternalSource_FetchDefaultStocks_NasdaqIDZero(t *testing.T) {
	// Return 0 for NASDAQ (simulates exchange not yet seeded), non-zero for NYSE.
	resolver := func(acronym string) (uint64, error) {
		if acronym == "NYSE" {
			return 1, nil
		}
		// NASDAQ returns 0 → NASDAQ tickers use nyseID
		return 0, nil
	}
	s := NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected stocks")
	}
	// All stocks should have ExchangeID=1 (NYSE) since NASDAQ returned 0.
	for _, sw := range got {
		if sw.ExchangeID != 1 {
			t.Errorf("ticker %s: exchange_id = %d, want 1 (NYSE fallback)", sw.Stock.Ticker, sw.ExchangeID)
		}
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.fetchForexFromFinnhub via a fake Finnhub-shaped HTTP server
// ---------------------------------------------------------------------------

// TestExternalSource_FetchForex_HardcodedPairs_CurrencyNames exercises the
// currencyName + isMajorPair + isExoticPair helpers inside fetchHardcodedForexPairs.
func TestExternalSource_FetchForex_AllPairsHaveNames(t *testing.T) {
	resolver := func(acronym string) (uint64, error) {
		if acronym == "FOREX" {
			return 1, nil
		}
		return 0, nil
	}
	s := NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchForex(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, fp := range got {
		if fp.Forex.Name == "" {
			t.Errorf("pair %s has empty Name", fp.Forex.Ticker)
		}
	}
}

// ---------------------------------------------------------------------------
// estimatePremium — past-expiry (daysToExpiry <= 0) → returns Zero
// ---------------------------------------------------------------------------

func TestEstimatePremium_ExpiredOption_ReturnsZero(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour) // already expired
	got := estimatePremium(decimal.NewFromInt(100), decimal.NewFromInt(100), past, "call")
	if !got.IsZero() {
		t.Errorf("expected zero premium for expired option, got %s", got)
	}
}

func TestEstimatePremium_Call_ITM(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour)
	// ITM call: stock > strike
	got := estimatePremium(decimal.NewFromInt(110), decimal.NewFromInt(100), future, "call")
	if got.IsZero() || got.IsNegative() {
		t.Errorf("expected positive premium for ITM call, got %s", got)
	}
}

func TestEstimatePremium_Put_ITM(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour)
	// ITM put: strike > stock
	got := estimatePremium(decimal.NewFromInt(90), decimal.NewFromInt(100), future, "put")
	if got.IsZero() || got.IsNegative() {
		t.Errorf("expected positive premium for ITM put, got %s", got)
	}
}

func TestEstimatePremium_Call_OTM(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour)
	// OTM call: stock < strike → intrinsic = 0
	got := estimatePremium(decimal.NewFromInt(90), decimal.NewFromInt(100), future, "call")
	// Premium is still non-zero because of time value
	if got.IsNegative() {
		t.Errorf("expected non-negative premium for OTM call, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// GenerateOptionsForStock — settlement dates and strikes
// ---------------------------------------------------------------------------

func TestGenerateOptionsForStock_ZeroPrice_ReturnsNil(t *testing.T) {
	stock := &model.Stock{Price: decimal.Zero}
	got := GenerateOptionsForStock(stock)
	if got != nil {
		t.Errorf("expected nil for zero-price stock")
	}
}

func TestGenerateOptionsForStock_NonZeroPrice_ReturnsOptions(t *testing.T) {
	stock := &model.Stock{ID: 1, Ticker: "TEST", Name: "Test", Price: decimal.NewFromInt(100)}
	got := GenerateOptionsForStock(stock)
	if len(got) == 0 {
		t.Fatal("expected options for non-zero price")
	}
	// Expect both CALLs and PUTs.
	calls, puts := 0, 0
	for _, o := range got {
		switch o.OptionType {
		case "call":
			calls++
		case "put":
			puts++
		}
	}
	if calls == 0 || puts == 0 {
		t.Errorf("expected both calls and puts, got calls=%d puts=%d", calls, puts)
	}
}

// ---------------------------------------------------------------------------
// SimulatorClient — register error paths
// ---------------------------------------------------------------------------

func TestSimulatorClient_Register_StatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	err := c.EnsureRegistered()
	if err == nil {
		t.Fatal("expected error when register returns 4xx")
	}
}

func TestSimulatorClient_Register_BadJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	err := c.EnsureRegistered()
	if err == nil {
		t.Fatal("expected error for bad register JSON response")
	}
}

func TestSimulatorClient_Register_EmptyAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"api_key":""}}`))
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	err := c.EnsureRegistered()
	if err == nil {
		t.Fatal("expected error for empty api_key in register response")
	}
}

// inMemoryStore implements SettingStore for these tests.
type inMemoryStore struct {
	data map[string]string
}

func (s *inMemoryStore) Get(key string) (string, error) { return s.data[key], nil }
func (s *inMemoryStore) Set(key, value string) error    { s.data[key] = value; return nil }

// ---------------------------------------------------------------------------
// SimulatorClient.Do — register-fails-inside-Do
// ---------------------------------------------------------------------------

func TestSimulatorClient_Do_RegisterFailsOnRetry(t *testing.T) {
	// First call to the data endpoint returns 401; register also fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/banks/me":
			// validate OK (initial EnsureRegistered)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"id":1}}`))
		case "/api/banks/register":
			// register fails
			http.Error(w, "register denied", http.StatusForbidden)
		default:
			// data endpoint → 401 triggers re-register
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "old-key"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	_ = c.EnsureRegistered() // populate apiKey via validate

	req, _ := http.NewRequest("GET", c.URL("/api/market/stocks"), nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected error when re-register inside Do fails")
	}
}

// ---------------------------------------------------------------------------
// SimulatorClient.validate — transport error path
// ---------------------------------------------------------------------------

func TestSimulatorClient_Validate_TransportError(t *testing.T) {
	// Server immediately closes connections, causing a transport error in validate.
	// We use a closed server to simulate this.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // close before use

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "key"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	// EnsureRegistered will try validate (which fails due to closed server) then register
	// (which also fails). Both fail → returns error.
	err := c.EnsureRegistered()
	if err == nil {
		t.Fatal("expected error for closed server")
	}
}

// ---------------------------------------------------------------------------
// GeneratedSource — FetchFutures with unknown contract unit
// (ticker not in futuresContractUnit → unit defaults to "contract")
// ---------------------------------------------------------------------------

func TestGeneratedFutures_ContractUnits_NonEmpty(t *testing.T) {
	g := NewGeneratedSource()
	got, err := g.FetchFutures(context.Background())
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	for _, f := range got {
		if f.Futures.ContractUnit == "" {
			t.Errorf("futures %s: empty ContractUnit", f.Futures.Ticker)
		}
	}
}

func TestGeneratedFutures_UnknownTickerFallsBackToContract(t *testing.T) {
	// Exercise the "unit not found → contract" fallback by looking up a ticker
	// we know is not in futuresContractUnit.
	if _, ok := futuresContractUnit["UNKNOWN"]; ok {
		t.Skip("UNKNOWN is in the unit map")
	}
	// The fallback path lives in FetchFutures for tickers whose unit is absent.
	// We can't inject new tickers at runtime, but we can verify the fallback
	// logic directly by removing a known ticker from the map temporarily.
	saved, exists := futuresContractUnit["CL"]
	if !exists {
		t.Skip("CL not in futuresContractUnit")
	}
	delete(futuresContractUnit, "CL")
	t.Cleanup(func() { futuresContractUnit["CL"] = saved })

	g := NewGeneratedSource()
	got, err := g.FetchFutures(context.Background())
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	for _, f := range got {
		if f.Futures.Ticker == "CL" {
			if f.Futures.ContractUnit != "contract" {
				t.Errorf("expected fallback unit 'contract' for CL, got %q", f.Futures.ContractUnit)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// NormalizeExchangeCurrency — wrapper delegates to model.NormalizeCurrency
// ---------------------------------------------------------------------------

func TestNormalizeExchangeCurrency_KnownCodes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"USD", "USD"},
		{"EUR", "EUR"},
		{"HKD", "USD"}, // normalised to USD
		{"", "USD"},    // empty → USD
		{"XYZ", "USD"}, // unknown → USD
	}
	for _, c := range cases {
		if got := NormalizeExchangeCurrency(c.in); got != c.want {
			t.Errorf("NormalizeExchangeCurrency(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// isMajorPair / isExoticPair
// ---------------------------------------------------------------------------

func TestIsMajorPair(t *testing.T) {
	if !isMajorPair("EUR", "USD") {
		t.Error("EUR/USD should be major")
	}
	if isMajorPair("RSD", "EUR") {
		t.Error("RSD/EUR should not be major")
	}
}

func TestIsExoticPair(t *testing.T) {
	if !isExoticPair("RSD", "EUR") {
		t.Error("RSD/EUR should be exotic")
	}
	if isExoticPair("EUR", "USD") {
		t.Error("EUR/USD should not be exotic")
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchFutures — no exchange resolver still returns results
// (resolver nil → exchangeID=0, row appended without ID resolution)
// ---------------------------------------------------------------------------

func TestExternalSource_FetchFutures_NoResolver_AppendWithZeroExchangeID(t *testing.T) {
	// No exchange resolver; the JSON seed has valid rows. Without resolver,
	// exchangeID stays 0 and the row is still appended.
	s := NewExternalSource(nil, nil, nil, nil, "", "../../data/futures_seed.json")
	got, err := s.FetchFutures(context.Background())
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	for _, f := range got {
		if f.ExchangeID != 0 {
			t.Errorf("expected exchangeID=0 without resolver, got %d", f.ExchangeID)
		}
	}
}

// ---------------------------------------------------------------------------
// ExternalSource — FetchStocks context cancellation path
// (av != nil and exchangeByAcronym != nil → enters the per-ticker loop;
//  cancelling the context before any ticker returns ctx.Err)
// ---------------------------------------------------------------------------

// NOTE: we cannot mock AlphaVantageClient / alpaca easily without real HTTP
// because they construct their own http.Client. These paths (av != nil branch)
// are left intentionally uncovered — they depend on live external APIs or
// deep provider-client mocking outside this package's test surface.

// ---------------------------------------------------------------------------
// Simulator source — pagination helper (FetchStocks covers paginator)
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchStocks_EmptyPagination(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                   `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200": `{"data":[],"pagination":{"page":1,"per_page":200,"total":0}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	got, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// fetchOptionsBySimulatorID — JSON parse error path
// (simulator returns invalid JSON for options endpoint)
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchOptionsBySimulatorID_BadJSON(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                   `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200": `{"data":[{"id":1,"ticker":"BAD","name":"Bad"}],"pagination":{"page":1,"per_page":200,"total":1}}`,
		"/api/market/stocks/BAD/listings": `{"data":[{"id":10,"exchange_id":1,"price":"100.00"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		// Options endpoint returns bad JSON.
		if r.URL.Path == "/api/market/options" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("bad-json"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	// FetchStocks to populate ticker→id cache.
	_, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	// FetchOptionsByTicker → fetchOptionsBySimulatorID → bad JSON → error.
	_, err = s.FetchOptionsByTicker(context.Background(), "BAD")
	if err == nil {
		t.Fatal("expected error for malformed options JSON")
	}
}

// ---------------------------------------------------------------------------
// SimulatorSource.FetchStocks — listings-fetch error (non-fatal, stock skipped)
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchStocks_ListingsFetchError_SkipsStock(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                   `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200": `{"data":[{"id":1,"ticker":"ERR","name":"Err"}],"pagination":{"page":1,"per_page":200,"total":1}}`,
		// listings returns bad JSON → stock skipped
		"/api/market/stocks/ERR/listings": `not-json`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	got, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected skipped stock, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Simulator FetchForex — fetchOptionsBySimulatorID coverage via FetchOptions
// using a stock whose ticker IS in the cache.
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchOptions_KnownTickerServerError(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                    `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200":  `{"data":[{"id":7,"ticker":"ERR2","name":"Err2"}],"pagination":{"page":1,"per_page":200,"total":1}}`,
		"/api/market/stocks/ERR2/listings": `{"data":[{"id":10,"exchange_id":1,"price":"50.00"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		// Options endpoint → server error
		if r.URL.Path == "/api/market/options" {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	_, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	_, err = s.FetchOptionsByTicker(context.Background(), "ERR2")
	if err == nil {
		t.Fatal("expected error for 500 from options endpoint")
	}
}

// ---------------------------------------------------------------------------
// SimulatorSource.FetchStocks — pickLowestExchangeID returns false (empty
// listings array, no error) → stock is skipped silently.
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchStocks_EmptyListings_SkipsStock(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                    `{"data":{"id":1}}`,
		"/api/market/stocks?per_page=200":  `{"data":[{"id":1,"ticker":"SKIP","name":"Skip"}],"pagination":{"page":1,"per_page":200,"total":1}}`,
		"/api/market/stocks/SKIP/listings": `{"data":[]}`, // empty → pickLowestExchangeID returns false
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	got, err := s.FetchStocks(context.Background())
	if err != nil {
		t.Fatalf("FetchStocks: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d stocks", len(got))
	}
}

// ---------------------------------------------------------------------------
// SimulatorSource.FetchFutures — listings error (non-fatal, futures skipped)
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchFutures_ListingsError_SkipsFutures(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                    `{"data":{"id":1}}`,
		"/api/market/futures?per_page=200": `{"data":[{"id":1,"ticker":"CL1","name":"Crude","contract_size":1000,"settlement_date":"2026-12-01T00:00:00Z"}]}`,
		"/api/market/futures/CL1/listings": `not-json`, // error → skip
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	got, err := s.FetchFutures(context.Background())
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// SimulatorSource.FetchForex — listings error (non-fatal, forex skipped)
// ---------------------------------------------------------------------------

func TestSimulatorSource_FetchForex_ListingsError_SkipsForex(t *testing.T) {
	routes := map[string]string{
		"/api/banks/me":                      `{"data":{"id":1}}`,
		"/api/market/forex?per_page=200":     `{"data":[{"id":1,"ticker":"EUR/USD","base_currency":"EUR","quote_currency":"USD","rate":"1.08"}]}`,
		"/api/market/forex/EUR/USD/listings": `not-json`, // error → skip
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path + "?" + r.URL.RawQuery
		if body, ok := routes[key]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	store := &inMemoryStore{data: map[string]string{"market_simulator_api_key": "ms_test"}}
	c := NewSimulatorClient(server.URL, "ExBanka", store)
	if err := c.EnsureRegistered(); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	s := NewSimulatorSource(c)
	got, err := s.FetchForex(context.Background())
	if err != nil {
		t.Fatalf("FetchForex: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// GeneratedSource.FetchForex — !ok branch when a pair is missing from forexPx
// ---------------------------------------------------------------------------

func TestGeneratedSource_FetchForex_MissingPair_Skipped(t *testing.T) {
	// Remove one pair from forexSeedPrices temporarily to trigger the !ok branch.
	const testPair = "EUR/USD"
	saved, exists := forexSeedPrices[testPair]
	if !exists {
		t.Skip("EUR/USD not in forexSeedPrices")
	}
	delete(forexSeedPrices, testPair)
	t.Cleanup(func() { forexSeedPrices[testPair] = saved })

	g := NewGeneratedSource() // forexPx built from forexSeedPrices at construction
	got, err := g.FetchForex(context.Background())
	if err != nil {
		t.Fatalf("FetchForex: %v", err)
	}
	// EUR/USD should be absent
	for _, fp := range got {
		if fp.Forex.Ticker == testPair {
			t.Errorf("expected EUR/USD to be skipped, but found it")
		}
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchFutures — exchange resolver error → stock skipped
// ---------------------------------------------------------------------------

func TestExternalSource_FetchFutures_ResolverError_SkipsFutures(t *testing.T) {
	resolver := func(acronym string) (uint64, error) {
		return 0, fmt.Errorf("exchange not found: %s", acronym)
	}
	s := NewExternalSource(nil, nil, nil, nil, "", "../../data/futures_seed.json").
		WithExchangeResolver(resolver)
	got, err := s.FetchFutures(context.Background())
	if err != nil {
		t.Fatalf("FetchFutures: %v", err)
	}
	// All futures skipped because resolver always errors.
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// unsafe redirect helpers — override unexported fields in provider clients.
// These helpers are only used in tests and rely on the structs having the
// same memory layout as the local mirror type, which is guaranteed as long
// as the field order/types match exactly.
// ---------------------------------------------------------------------------

func redirectFinnhub(c *provider.FinnhubClient, baseURL string, cl *http.Client) {
	type m struct {
		apiKey     string
		baseURL    string
		httpClient *http.Client
	}
	(*m)(unsafe.Pointer(c)).baseURL = baseURL
	(*m)(unsafe.Pointer(c)).httpClient = cl
}

func redirectEODHD(c *provider.EODHDClient, baseURL string, cl *http.Client) {
	type m struct {
		apiKey     string
		baseURL    string
		httpClient *http.Client
	}
	(*m)(unsafe.Pointer(c)).baseURL = baseURL
	(*m)(unsafe.Pointer(c)).httpClient = cl
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchForex / fetchForexFromFinnhub — comprehensive coverage
// ---------------------------------------------------------------------------

// finnhubTestMux builds a minimal Finnhub-shaped HTTP handler that exercises
// all control-flow branches inside fetchForexFromFinnhub:
//   - /api/v1/forex/symbol returns mixed symbols (valid, no-slash, unsupported)
//   - /api/v1/forex/rates returns error for "RSD" (covers error-log+continue
//     branch) and success for all other SupportedCurrencies
func buildFinnhubTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/forex/symbol", func(w http.ResponseWriter, r *http.Request) {
		// "no-slash-here": triggers len(parts) != 2 → continue branch
		// "AAPL/MSFT": both unsupported → !supported branch
		// "USD/EUR": major pair → liquidity="high"
		// "RSD/USD": exotic (RSD) → liquidity="low"
		// "CHF/AUD": medium
		syms := `[
			{"symbol":"S1","displaySymbol":"no-slash-here","description":""},
			{"symbol":"S2","displaySymbol":"AAPL/MSFT","description":""},
			{"symbol":"S3","displaySymbol":"USD/EUR","description":""},
			{"symbol":"S4","displaySymbol":"RSD/USD","description":""},
			{"symbol":"S5","displaySymbol":"CHF/AUD","description":""}
		]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(syms))
	})
	mux.HandleFunc("/api/v1/forex/rates", func(w http.ResponseWriter, r *http.Request) {
		base := r.URL.Query().Get("base")
		if base == "RSD" {
			// Simulate FetchForexRates failure for one base → covers log+continue branch.
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		rates := map[string]float64{
			"EUR": 0.92, "USD": 1.00, "CHF": 0.89,
			"GBP": 0.73, "JPY": 149.0, "CAD": 1.37,
			"AUD": 1.53, "RSD": 117.0,
		}
		delete(rates, base)
		body, _ := json.Marshal(map[string]interface{}{"base": base, "quote": rates})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	return mux
}

// TestExternalSource_FetchForexFromFinnhub_FullCoverage exercises almost every
// branch inside fetchForexFromFinnhub via a local test server.
func TestExternalSource_FetchForexFromFinnhub_FullCoverage(t *testing.T) {
	srv := httptest.NewServer(buildFinnhubTestMux())
	defer srv.Close()

	fc := provider.NewFinnhubClient("testkey")
	redirectFinnhub(fc, srv.URL, srv.Client())

	resolver := func(acronym string) (uint64, error) {
		if acronym == "FOREX" {
			return 99, nil
		}
		return 0, fmt.Errorf("unknown exchange: %s", acronym)
	}

	src := NewExternalSource(nil, fc, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := src.FetchForex(context.Background())
	if err != nil {
		t.Fatalf("FetchForex: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected at least one forex pair from Finnhub")
	}
	for _, fp := range got {
		if fp.Forex.ExchangeID != 99 {
			t.Errorf("pair %s: want exchange ID 99, got %d", fp.Forex.Ticker, fp.Forex.ExchangeID)
		}
	}
}

// TestExternalSource_FetchForex_ForexExchangeNotFound covers the
// exchangeByAcronym("FOREX") error path inside FetchForex (returns nil,nil).
func TestExternalSource_FetchForex_ForexExchangeNotFound(t *testing.T) {
	resolver := func(acronym string) (uint64, error) {
		return 0, fmt.Errorf("exchange %q not found", acronym)
	}
	s := NewExternalSource(nil, nil, nil, nil, "", "").WithExchangeResolver(resolver)
	got, err := s.FetchForex(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.fetchDefaultStocks — nil resolver returns nil, nil
// ---------------------------------------------------------------------------

func TestExternalSource_FetchDefaultStocks_NilResolver(t *testing.T) {
	s := &ExternalSource{} // exchangeByAcronym == nil
	got, err := s.fetchDefaultStocks()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil slice, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ExternalSource.FetchExchanges — EODHD enrichment path
// ---------------------------------------------------------------------------

func buildEODHDTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/exchanges-list/", func(w http.ResponseWriter, r *http.Request) {
		// Two entries: one with a multi-MIC OperatingMIC, one with empty MIC (triggers skip).
		data := `[
			{"Name":"NYSE","Code":"NYSE","OperatingMIC":"XNYS","Country":"US","Currency":"USD"},
			{"Name":"LSE","Code":"LSE","OperatingMIC":"XLON,XLOM","Country":"GB","Currency":"GBP"},
			{"Name":"EmptyMIC","Code":"EMX","OperatingMIC":"","Country":"XY","Currency":"EUR"}
		]`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(data))
	})
	return mux
}

// TestExternalSource_FetchExchanges_EODHDEnrichment exercises the EODHD
// enrichment path inside FetchExchanges (no CSV file, all exchanges from EODHD).
func TestExternalSource_FetchExchanges_EODHDEnrichment(t *testing.T) {
	srv := httptest.NewServer(buildEODHDTestMux())
	defer srv.Close()

	ec := provider.NewEODHDClient("testkey")
	redirectEODHD(ec, srv.URL, srv.Client())

	src := NewExternalSource(nil, nil, ec, nil, "", "") // empty csvPath → csvErr != nil
	got, err := src.FetchExchanges(context.Background())
	// With an empty csvPath the CSV load fails, but EODHD succeeds → len(got)>0 (EODHD entries only).
	if err != nil {
		t.Fatalf("FetchExchanges: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected exchanges from EODHD")
	}
}

// TestExternalSource_FetchExchanges_EODHDError covers the EODHD error log path
// when the EODHD client returns an error from FetchExchanges.
func TestExternalSource_FetchExchanges_EODHDError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "eodhd down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ec := provider.NewEODHDClient("testkey")
	redirectEODHD(ec, srv.URL, srv.Client())

	// Use a real CSV path so at least CSV exchanges load; EODHD fails gracefully.
	src := NewExternalSource(nil, nil, ec, nil, "../../data/exchanges.csv", "")
	_, _ = src.FetchExchanges(context.Background()) // should not panic; EODHD error is logged only
}

// ---------------------------------------------------------------------------
// SimulatorClient.validate — non-OK HTTP status covers the error return path
// ---------------------------------------------------------------------------

func TestSimulatorClient_Validate_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := &SimulatorClient{
		baseURL: srv.URL,
		http:    srv.Client(),
	}
	if err := c.validate(); err == nil {
		t.Error("expected error from validate with non-200 status")
	}
}

// ---------------------------------------------------------------------------
// SimulatorClient.Do — reauth error path (register fails → return nil, err)
// ---------------------------------------------------------------------------

func TestSimulatorClient_Do_RegisterFailsDuringReauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/banks/register" {
			// Simulate register endpoint returning 500.
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		// All non-register requests return 401 to trigger reauth.
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	store := &inMemoryStore{data: map[string]string{}}
	c := &SimulatorClient{
		baseURL:  srv.URL,
		bankName: "TestBank",
		store:    store,
		http:     srv.Client(),
		apiKey:   "expired_key",
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/test", nil)
	_, err := c.Do(req)
	if err == nil {
		t.Error("expected error when register fails during reauth")
	}
}

// ---------------------------------------------------------------------------
// SimulatorSource.getJSON — closed-server covers the Do-error return path
// ---------------------------------------------------------------------------

func TestSimulatorSource_GetJSON_DoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	store := &inMemoryStore{data: map[string]string{settingKeyAPIKey: "key"}}
	c := NewSimulatorClient(srv.URL, "bank", store)
	c.apiKey = "key"
	s := NewSimulatorSource(c)
	// Close server BEFORE the request so c.http.Do fails immediately.
	srv.Close()
	var out interface{}
	if err := s.getJSON(context.Background(), "/api/test", &out); err == nil {
		t.Error("expected error from getJSON with closed server")
	}
}
