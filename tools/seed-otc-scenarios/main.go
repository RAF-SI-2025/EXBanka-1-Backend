// seed-otc-scenarios populates a freshly-bootstrapped bank with the
// full set of OTC test scenarios (Celina 4 intra-bank + setup for
// Celina 5 cross-bank). Designed to run AFTER `seeder/cmd/seeder`
// has created the three test clients (admin+testclient,
// admin+testclient2, admin+testclient3). Idempotent: every step is
// best-effort — already-exists responses are treated as success, and
// a step that can't run (e.g. matching engine has no liquidity) logs
// a warning and continues so partial state still produces a useful
// demo dataset.
//
// What it creates:
//
//	OTC stocks marketplace (§47.1):
//	  - one direction=sell offer  (testclient publishes shares)
//	  - one direction=buy  offer  (testclient2 reserves cash)
//
//	OTC options marketplace (§47.2 — Celina 4):
//	  - sell_initiated parent listing (testclient is the seller)
//	    + bid from testclient2  (chain 1, status open)
//	    + bid from testclient3  (chain 2, status open) — sets up the
//	      multi-bidder case so a tester can hit /accept on one and
//	      watch the OTHER cascade-cancel (Phase 2) + emit
//	      OTC_OFFER_CASCADE_CANCELLED notifications
//	    + counter on chain 1 by testclient (poster), so the FE has a
//	      countered chain in the list
//	  - buy_initiated parent listing (testclient2 is the buyer)
//	    + bid from testclient (chain 1, status open) — sets up the
//	      "I'm the seller, somebody wants to buy" case
//
// Celina 5 (cross-bank) cannot be bootstrapped from one side — it
// requires a second running bank stack and a pair of POST /api/v3/peer-banks
// registrations. The script prints instructions for that at the end.
//
// Environment variables (defaults match the local dev stack):
//
//	GATEWAY_URL    — gateway base URL          (default http://localhost:8080)
//	ADMIN_EMAIL    — admin email               (default admin+testadmin@admin.com)
//	ADMIN_PASSWORD — admin + test-client pw    (default AdminAdmin2026!.)
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// tiny HTTP client
// ────────────────────────────────────────────────────────────────────

type apiClient struct {
	baseURL string
	http    *http.Client
	token   string
	label   string // for log lines
}

func newClient(baseURL, label string) *apiClient {
	return &apiClient{
		baseURL: baseURL,
		label:   label,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

type apiResp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (c *apiClient) do(method, path string, body any) (*apiResp, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	r := &apiResp{Status: resp.StatusCode, Raw: raw}
	_ = json.Unmarshal(raw, &r.Body)
	return r, nil
}

func (c *apiClient) get(path string) (*apiResp, error)         { return c.do("GET", path, nil) }
func (c *apiClient) post(path string, b any) (*apiResp, error) { return c.do("POST", path, b) }

// ────────────────────────────────────────────────────────────────────
// helpers
// ────────────────────────────────────────────────────────────────────

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// deriveTestEmail mirrors seeder/cmd/main.go so we re-derive the same
// addresses the seeder created.
func deriveTestEmail(adminEmail, suffix string) string {
	parts := strings.SplitN(adminEmail, "@", 2)
	if len(parts) != 2 {
		return suffix + "@admin.com"
	}
	local := parts[0]
	if i := strings.Index(local, "+"); i != -1 {
		local = local[:i]
	}
	return local + "+" + suffix + "@" + parts[1]
}

// login authenticates and stamps the JWT onto the client. Returns
// the numeric principal_id (decoded from the JWT payload — the login
// response itself carries only access_token + refresh_token).
func login(c *apiClient, email, password string) (int64, error) {
	r, err := c.post("/api/v3/auth/login", map[string]string{"email": email, "password": password})
	if err != nil {
		return 0, err
	}
	if r.Status != 200 {
		return 0, fmt.Errorf("login %s: status %d body=%s", email, r.Status, string(r.Raw))
	}
	tok, _ := r.Body["access_token"].(string)
	if tok == "" {
		return 0, fmt.Errorf("login %s: no access_token in response", email)
	}
	c.token = tok
	pid, perr := principalIDFromJWT(tok)
	if perr != nil {
		return 0, fmt.Errorf("login %s: decode jwt: %w", email, perr)
	}
	return pid, nil
}

// principalIDFromJWT pulls the principal_id claim out of an unsigned
// JWT payload. We never verify the signature here — this is a client
// extracting its own id from a token the server just minted for it.
func principalIDFromJWT(token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("not a jwt (parts=%d)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("base64: %w", err)
	}
	var claims struct {
		PrincipalID int64 `json:"principal_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("unmarshal: %w", err)
	}
	if claims.PrincipalID == 0 {
		return 0, fmt.Errorf("no principal_id in claims")
	}
	return claims.PrincipalID, nil
}

// nf is a tiny "number field" extractor that copes with both float64
// (most JSON numbers) and json.Number (rarely seen here but safe).
func nf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// sf pulls a string field, tolerating absent / wrong-type entries.
func sf(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// step prints a banner so the script reads like a runbook.
func step(format string, a ...any) {
	log.Printf("▶ "+format, a...)
}

func warn(format string, a ...any) {
	log.Printf("⚠ "+format, a...)
}

func ok(format string, a ...any) {
	log.Printf("✓ "+format, a...)
}

// ────────────────────────────────────────────────────────────────────
// orchestration
// ────────────────────────────────────────────────────────────────────

type clientState struct {
	label      string // "testclient" etc.
	email      string
	c          *apiClient
	principal  int64
	rsdAcct    uint64
	usdAcct    uint64
	holdingID  uint64 // first stock holding once orders have filled
	holdingQty int64
}

func main() {
	log.SetFlags(log.Ltime)

	gateway := env("GATEWAY_URL", "http://localhost:8080")
	adminEmail := env("ADMIN_EMAIL", "admin+testadmin@admin.com")
	password := env("ADMIN_PASSWORD", "AdminAdmin2026!.")

	log.Printf("seed-otc-scenarios: gateway=%s admin=%s", gateway, adminEmail)

	// ── 1. Admin + clients ────────────────────────────────────────
	step("Phase 1 — log in admin + three test clients")
	admin := newClient(gateway, "admin")
	if _, err := login(admin, adminEmail, password); err != nil {
		log.Fatalf("admin login: %v", err)
	}
	ok("admin logged in")

	suffixes := []string{"testclient", "testclient2", "testclient3"}
	clients := make([]*clientState, 0, len(suffixes))
	for _, s := range suffixes {
		email := deriveTestEmail(adminEmail, s)
		c := newClient(gateway, s)
		pid, err := login(c, email, password)
		if err != nil {
			warn("%s login failed (run `seeder` first?): %v", s, err)
			continue
		}
		clients = append(clients, &clientState{label: s, email: email, c: c, principal: pid})
		ok("%s logged in (principal=%d)", s, pid)
	}
	if len(clients) == 0 {
		log.Fatalf("no test clients logged in; need at least admin+testclient seeded")
	}
	// Note: on hosted single-bank instances only `testclient` is
	// typically seeded. The intra-bank multi-bidder scenarios are
	// skipped in that mode (cross-bank takes that role via Celina 5).
	if len(clients) < 3 {
		warn("only %d test clients seeded — intra-bank multi-bidder scenarios will be skipped (run cross-bank instead)", len(clients))
	}

	// ── 2. Accounts ───────────────────────────────────────────────
	step("Phase 2 — provision RSD + USD accounts for each test client")
	for _, cl := range clients {
		cl.rsdAcct = ensureAccount(admin, cl, "RSD", 1_000_000)
		cl.usdAcct = ensureAccount(admin, cl, "USD", 100_000)
		ok("%s: rsd=%d usd=%d", cl.label, cl.rsdAcct, cl.usdAcct)
	}

	// ── 3. Holdings ───────────────────────────────────────────────
	step("Phase 3 — buy stock so clients have something to OTC-sell")
	stockID, ticker, listingID := firstStock(admin)
	if listingID == 0 {
		log.Fatalf("no seeded stock listings found — run stock-service first")
	}
	log.Printf("using listing %d (stock id=%d ticker=%s)", listingID, stockID, ticker)

	for _, cl := range clients {
		placeBuyOrder(cl, listingID, 10)
	}
	// Give the matching engine ~3s to fill before we read portfolios.
	time.Sleep(3 * time.Second)
	for _, cl := range clients {
		readFirstHolding(cl)
	}

	// ── 4. OTC stocks marketplace ────────────────────────────────
	step("Phase 4a — OTC stocks: one sell offer + one buy offer")
	if h := findHoldingClient(clients, "testclient"); h != nil {
		// Cap the public quantity at what the holding actually has —
		// market simulator may have only partially filled the buy.
		qty := int64(2)
		if h.holdingQty < qty {
			qty = h.holdingQty
		}
		createOTCStockSell(h, qty, "155.00")
	} else {
		warn("no client has holdings — skipping OTC sell offer")
	}
	// Buy offer: prefer testclient2 so on a 3-client instance the
	// directions split across users; fall back to testclient on
	// single-client hosted instances so the BUY direction is still
	// represented for cross-bank discovery.
	if buyer := pickOrFallback(clients, "testclient2", "testclient"); buyer != nil {
		createOTCStockBuy(buyer, listingID, 1, "140.00")
	}

	// ── 5. OTC options marketplace ───────────────────────────────
	step("Phase 4b — OTC options: sell_initiated listing + bids if available")
	seller := findHoldingClient(clients, "testclient")
	if seller != nil {
		sellListingID := createOTCOption(seller, "sell_initiated", ticker)
		if sellListingID != 0 {
			// First bidder must NOT be the listing poster
			// (ErrOTCBidOwnListing). On hosted single-client instances
			// no intra-bank bidder exists; cross-bank bidders from
			// other instances will discover this listing via
			// /api/v3/otc/options?kind=remote.
			var firstNegID uint64
			b1 := pickAnyExcept(clients, seller.label)
			if b1 != nil {
				firstNegID = bidOnOption(b1, sellListingID, "5", "150", "40")
			} else {
				warn("no intra-bank bidder for sell_initiated listing — cross-bank bidders only")
			}
			// Second bidder for the cascade demo — needs to be a
			// different client AGAIN (i.e. third distinct identity).
			b2 := pickAnyExceptMany(clients, seller.label, labelOf(b1))
			if b2 != nil {
				bidOnOption(b2, sellListingID, "5", "150", "45")
			}
			// Seller counters chain 1 so the FE has a `countered`-status
			// chain in the list (only meaningful when chain 1 exists).
			if firstNegID != 0 {
				counterChain(seller, sellListingID, firstNegID, "5", "150", "47")
			}
		}
	}

	step("Phase 4c — OTC options: buy_initiated listing + bid if available")
	buyer := pickOrFallback(clients, "testclient2", "testclient")
	if buyer != nil {
		buyListingID := createOTCOption(buyer, "buy_initiated", ticker)
		if buyListingID != 0 {
			// Bidder on a buy_initiated listing is a SELLER — must
			// have a holding AND not be the listing poster.
			bidder := pickHolderExcept(clients, buyer.label)
			if bidder != nil {
				bidOnOption(bidder, buyListingID, "3", "150", "30")
			} else {
				warn("no intra-bank bidder for buy_initiated listing — cross-bank bidders only")
			}
		}
	}

	// ── 6. Celina 5 cross-bank instructions ──────────────────────
	step("Done. Celina 5 (cross-bank) setup is manual — see below.")
	printCelina5Notes()
}

// ────────────────────────────────────────────────────────────────────
// per-phase helpers
// ────────────────────────────────────────────────────────────────────

// ensureAccount returns an existing account's ID for the given currency
// if one is already present, otherwise creates one as admin with the
// requested initial balance. The admin-on-behalf POST /api/v3/accounts
// path needs owner_id; the client's principal id IS the owner id (1:1
// for client principals).
func ensureAccount(admin *apiClient, cl *clientState, currency string, initial float64) uint64 {
	r, err := cl.c.get("/api/v3/me/accounts")
	if err == nil && r.Status == 200 {
		if accs, ok := r.Body["accounts"].([]any); ok {
			for _, a := range accs {
				m, _ := a.(map[string]any)
				if m == nil {
					continue
				}
				if sf(m, "currency_code") == currency {
					return uint64(nf(m["id"]))
				}
			}
		}
	}
	// account_kind: RSD must be "current"; everything else must be
	// "foreign" (account-service validation, observed against the
	// hosted instance).
	kind := "current"
	if currency != "RSD" {
		kind = "foreign"
	}
	created, err := admin.post("/api/v3/accounts", map[string]any{
		"owner_id":        cl.principal,
		"account_kind":    kind,
		"account_type":    "personal",
		"currency_code":   currency,
		"initial_balance": initial,
	})
	if err != nil {
		warn("%s: create %s account failed: %v", cl.label, currency, err)
		return 0
	}
	if created.Status != 201 {
		warn("%s: create %s account status=%d body=%s", cl.label, currency, created.Status, string(created.Raw))
		return 0
	}
	return uint64(nf(created.Body["id"]))
}

// firstStock returns the id/ticker/listing_id of the first seeded
// stock (any market). Used as the underlying for every order + OTC
// option in this script.
func firstStock(admin *apiClient) (uint64, string, uint64) {
	r, err := admin.get("/api/v3/securities/stocks?page=1&page_size=1")
	if err != nil || r.Status != 200 {
		return 0, "", 0
	}
	stocks, _ := r.Body["stocks"].([]any)
	if len(stocks) == 0 {
		return 0, "", 0
	}
	s, _ := stocks[0].(map[string]any)
	id := uint64(nf(s["id"]))
	ticker := sf(s, "ticker")
	var listingID uint64
	if listing, ok := s["listing"].(map[string]any); ok {
		listingID = uint64(nf(listing["id"]))
	}
	return id, ticker, listingID
}

// placeBuyOrder fires a market buy from the client's USD account.
// Best-effort: if the order doesn't accept (no liquidity, missing
// market simulator, etc.) we log a warning and move on.
func placeBuyOrder(cl *clientState, listingID uint64, qty int64) {
	if cl.usdAcct == 0 {
		warn("%s: no USD account — skip buy order", cl.label)
		return
	}
	r, err := cl.c.post("/api/v3/me/orders", map[string]any{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    qty,
		"all_or_none": false,
		"margin":      false,
		"account_id":  cl.usdAcct,
	})
	if err != nil {
		warn("%s: order POST failed: %v", cl.label, err)
		return
	}
	if r.Status != 201 {
		warn("%s: order rejected status=%d body=%s", cl.label, r.Status, string(r.Raw))
		return
	}
	ok("%s: buy order submitted (id=%d)", cl.label, int64(nf(r.Body["id"])))
}

// readFirstHolding stamps cl.holdingID / cl.holdingQty if the matching
// engine has reified a holding for this client. No-op + warning if the
// portfolio is empty (means the buy order never filled — happens when
// the market simulator isn't running, which is fine, we still create
// the OTC-buy scenarios that don't depend on it).
func readFirstHolding(cl *clientState) {
	r, err := cl.c.get("/api/v3/me/portfolio?security_type=stock")
	if err != nil || r.Status != 200 {
		warn("%s: portfolio read failed", cl.label)
		return
	}
	hs, _ := r.Body["holdings"].([]any)
	if len(hs) == 0 {
		warn("%s: no holdings yet (matching engine inactive?)", cl.label)
		return
	}
	first, _ := hs[0].(map[string]any)
	cl.holdingID = uint64(nf(first["id"]))
	cl.holdingQty = int64(nf(first["quantity"]))
	ok("%s: holding id=%d qty=%d", cl.label, cl.holdingID, cl.holdingQty)
}

// createOTCStockSell publishes shares from an existing holding at the
// given asking price (Phase 11 — price_per_unit is now required for
// sell direction).
func createOTCStockSell(cl *clientState, qty int64, pricePerUnit string) {
	if cl.holdingID == 0 {
		warn("%s: cannot create OTC sell — no holding", cl.label)
		return
	}
	r, err := cl.c.post("/api/v3/me/otc/stocks", map[string]any{
		"direction":      "sell",
		"holding_id":     cl.holdingID,
		"quantity":       qty,
		"price_per_unit": pricePerUnit,
	})
	if err != nil || r.Status != 201 {
		warn("%s: OTC sell create failed status=%d body=%s", cl.label, r.Status, string(r.Raw))
		return
	}
	ok("%s: OTC stock SELL offer created (qty=%d @ %s)", cl.label, qty, pricePerUnit)
}

func createOTCStockBuy(cl *clientState, listingID uint64, qty int64, pricePerUnit string) {
	if cl.usdAcct == 0 {
		warn("%s: cannot create OTC buy — no USD account", cl.label)
		return
	}
	r, err := cl.c.post("/api/v3/me/otc/stocks", map[string]any{
		"direction":        "buy",
		"listing_id":       listingID,
		"quantity":         qty,
		"price_per_unit":   pricePerUnit,
		"buyer_account_id": cl.usdAcct,
	})
	if err != nil || r.Status != 201 {
		warn("%s: OTC buy create failed status=%d body=%s", cl.label, r.Status, string(r.Raw))
		return
	}
	ok("%s: OTC stock BUY offer created (qty=%d @ %s)", cl.label, qty, pricePerUnit)
}

// createOTCOption posts an option listing (parent OTCOffer). Returns
// the listing id or 0 on failure. The settlement date is 60 days out
// — far enough that the listing won't auto-expire mid-demo.
//
// For sell_initiated listings the service pre-flight checks the
// seller has enough free shares (held - already-committed >= qty),
// so we cap qty at what's actually available. buy_initiated has no
// share check (the seller commits at accept time), so the cap is
// purely cosmetic there.
func createOTCOption(cl *clientState, direction, ticker string) uint64 {
	if cl.usdAcct == 0 {
		warn("%s: cannot create OTC option — no USD account", cl.label)
		return 0
	}
	qty := int64(5)
	if direction == "sell_initiated" && cl.holdingQty > 0 && cl.holdingQty < qty {
		qty = cl.holdingQty
	}
	if direction == "sell_initiated" && qty < 1 {
		warn("%s: cannot create sell_initiated option — holding empty", cl.label)
		return 0
	}
	settle := time.Now().UTC().Add(60 * 24 * time.Hour).Format("2006-01-02")
	r, err := cl.c.post("/api/v3/me/otc/options", map[string]any{
		"direction":       direction,
		"ticker":          ticker,
		"quantity":        fmt.Sprintf("%d", qty),
		"strike_price":    "150.00",
		"premium":         "40.00",
		"settlement_date": settle,
		"account_id":      cl.usdAcct,
	})
	if err != nil || r.Status != 201 {
		warn("%s: create %s option failed status=%d body=%s", cl.label, direction, r.Status, string(r.Raw))
		return 0
	}
	// Response shape: { "offer": { "id": ... } }
	id := uint64(nf(r.Body["id"]))
	if id == 0 {
		if off, ok := r.Body["offer"].(map[string]any); ok {
			id = uint64(nf(off["id"]))
		}
	}
	ok("%s: %s option listing created (id=%d ticker=%s)", cl.label, direction, id, ticker)
	return id
}

// bidOnOption opens a new negotiation chain on the parent listing.
// Returns the negotiation id (chain id) or 0.
func bidOnOption(cl *clientState, parentID uint64, qty, strike, premium string) uint64 {
	if cl.usdAcct == 0 {
		warn("%s: cannot bid — no USD account", cl.label)
		return 0
	}
	settle := time.Now().UTC().Add(60 * 24 * time.Hour).Format("2006-01-02")
	r, err := cl.c.post(fmt.Sprintf("/api/v3/otc/options/%d/bid", parentID), map[string]any{
		"bidder_account_id": cl.usdAcct,
		"quantity":          qty,
		"strike_price":      strike,
		"premium":           premium,
		"settlement_date":   settle,
	})
	if err != nil || r.Status != 201 {
		warn("%s: bid on %d failed status=%d body=%s", cl.label, parentID, r.Status, string(r.Raw))
		return 0
	}
	negID := uint64(nf(r.Body["id"]))
	if negID == 0 {
		if neg, ok := r.Body["negotiation"].(map[string]any); ok {
			negID = uint64(nf(neg["id"]))
		}
	}
	ok("%s: bid placed on listing %d (chain=%d premium=%s)", cl.label, parentID, negID, premium)
	return negID
}

// counterChain counters the most recent terms on a chain with new ones.
// Used here so the FE list shows a "countered" chain (otherwise every
// chain in the demo data would be plain "open").
func counterChain(cl *clientState, parentID, negID uint64, qty, strike, premium string) {
	settle := time.Now().UTC().Add(60 * 24 * time.Hour).Format("2006-01-02")
	r, err := cl.c.post(fmt.Sprintf("/api/v3/me/otc/options/%d/negotiations/%d/counter", parentID, negID), map[string]any{
		"quantity":        qty,
		"strike_price":    strike,
		"premium":         premium,
		"settlement_date": settle,
	})
	if err != nil || r.Status != 200 {
		warn("%s: counter on chain %d failed status=%d body=%s", cl.label, negID, r.Status, string(r.Raw))
		return
	}
	ok("%s: countered chain %d (new premium=%s)", cl.label, negID, premium)
}

// findClient returns the in-script handle by label, or nil if that
// client never logged in (caller must skip gracefully).
func findClient(clients []*clientState, label string) *clientState {
	for _, c := range clients {
		if c.label == label {
			return c
		}
	}
	return nil
}

// findHoldingClient picks a client that already has a stock holding,
// preferring the requested label. Used so scenarios degrade gracefully
// when the matching engine fills only some clients' orders.
func findHoldingClient(clients []*clientState, preferLabel string) *clientState {
	if c := findClient(clients, preferLabel); c != nil && c.holdingID != 0 {
		return c
	}
	for _, c := range clients {
		if c.holdingID != 0 {
			return c
		}
	}
	return nil
}

// pickOrFallback returns the client with preferLabel if logged in,
// otherwise the client with fallbackLabel. Used for direction-keyed
// scenarios that want different clients on a 3-client run but accept
// re-use on a single-client run.
func pickOrFallback(clients []*clientState, preferLabel, fallbackLabel string) *clientState {
	if c := findClient(clients, preferLabel); c != nil {
		return c
	}
	return findClient(clients, fallbackLabel)
}

// pickAnyExcept returns the first client whose label is NOT excludeLabel.
// Used for opening a negotiation chain: bidder cannot be the listing
// poster (ErrOTCBidOwnListing).
func pickAnyExcept(clients []*clientState, excludeLabel string) *clientState {
	for _, c := range clients {
		if c.label != excludeLabel {
			return c
		}
	}
	return nil
}

// pickAnyExceptMany returns the first client whose label isn't in
// the exclude list. Used to find a 2nd bidder distinct from both
// the seller AND the 1st bidder.
func pickAnyExceptMany(clients []*clientState, excludeLabels ...string) *clientState {
	for _, c := range clients {
		if !contains(excludeLabels, c.label) {
			return c
		}
	}
	return nil
}

// pickHolderExcept is pickAnyExcept that also requires a holding —
// used for bidding on a buy_initiated listing (the bidder there is
// the SELLER, who needs shares to deliver).
func pickHolderExcept(clients []*clientState, excludeLabel string) *clientState {
	for _, c := range clients {
		if c.label != excludeLabel && c.holdingID != 0 {
			return c
		}
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// labelOf is a nil-safe label getter for chaining picks.
func labelOf(c *clientState) string {
	if c == nil {
		return ""
	}
	return c.label
}

// printCelina5Notes prints the cross-bank-setup runbook the script
// can't automate (requires a second running stack).
func printCelina5Notes() {
	const notes = `
─────────────────────────────────────────────────────────────────────
Celina 5 (cross-bank) setup — manual steps:

  1. Boot a second instance of this stack with a different
     OWN_BANK_CODE (e.g. 222) using docker-compose.bank-b.yml:
         docker compose --env-file .env.bank-b \
           -f docker-compose.yml -f docker-compose.bank-b.yml up

  2. On EACH bank, register the OTHER bank as a peer via
     POST /api/v3/peer-banks. The base_url is the FULL SI-TX
     prefix the peer's gateway exposes:
         http://host.docker.internal:<peer-gateway-port>/api/v3

  3. Re-run THIS script against the second bank too, so it has
     three test clients with the same logins.

  4. To exercise cross-bank flows:
       - Bank A: testclient → GET /api/v3/otc/options?kind=remote
         shows Bank B's listings (also via peer /public-option-offers).
       - Bank A: testclient → POST /api/v3/me/peer-otc/negotiations
         with seller_bank_code=222 + seller_id=<peer client id> +
         parent_offer_id={routingNumber, id} from discovery to set up
         the Phase 10 cascade group.
       - Have a second cross-bank bidder (e.g. testclient3 on Bank A)
         place a competing bid against the SAME parent_offer_id.
       - Have the seller on Bank B accept one chain — the other
         sibling chain cascade-cancels on both banks AND each loser
         gets an OTC_OFFER_CASCADE_CANCELLED notification in
         /api/v3/me/notifications.

─────────────────────────────────────────────────────────────────────
`
	fmt.Print(notes)
}
