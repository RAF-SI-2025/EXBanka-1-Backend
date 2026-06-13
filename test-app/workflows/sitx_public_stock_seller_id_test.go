//go:build integration

package workflows

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestSITX_PublicStockSellerIdAndOpaqueBuyerId is the live two-stack repro for
// the partner-reported interop bug (fix(sitx): publish standard client-<N>
// seller id + stop interpreting opaque peer ids). It asserts the two endpoints
// are CONSISTENT and spec-conformant (SI-TX §2.3 ForeignBankId, §3.1
// /public-stock, §3.2 POST /negotiations):
//
//  1. GET /cross-bank-protocol/public-stock advertises each seller as a
//     ForeignBankId whose id is the STANDARD opaque form ("client-<N>" for a
//     client-held holding) — NOT a bare numeric owner id. A bare numeric could
//     not be addressed back by a peer (it fails the local seller resolver).
//
//  2. POST /cross-bank-protocol/negotiations ACCEPTS that exact seller id
//     echoed back verbatim, AND accepts an arbitrary OPAQUE buyerId.id (a UUID
//     in a scheme we do not own). Per §2.3 a bank MUST NOT interpret a peer's
//     opaque id; the only valid checks are non-empty + ≤ 64 bytes. The prior
//     "client-<N>"/"employee-<N>" regex on buyerId was a spec violation that
//     rejected conformant peers (the partner's report).
//
//  3. A > 64-byte id is still rejected (the real §2.3 invariant).
//
// REQUIRES A LIVE STACK on localhost:8080. Gated behind the `integration`
// build tag; skips cleanly when the gateway / stocks / order-sim aren't ready.
func TestSITX_PublicStockSellerIdAndOpaqueBuyerId(t *testing.T) {
	if _, err := net.DialTimeout("tcp", "localhost:8080", 1*time.Second); err != nil {
		t.Skipf("gateway not reachable on localhost:8080 (run `make docker-up` first): %v", err)
	}

	adminC := loginAsAdmin(t)
	enableTestingMode(t, adminC)

	// Authenticate the inbound peer-OTC calls below via a registered peer bank's
	// api_token (X-Api-Key). In a fresh stack we register a throwaway peer 222;
	// on a live two-stack where peer 222 is ALREADY registered (cohort setup),
	// we reuse its existing token (default "shared-111-222", overridable via
	// SITX_PEER_TOKEN) and leave the registration untouched. We only exercise
	// INBOUND routes this bank EXPOSES, never dispatch outbound, so base_url is
	// irrelevant here.
	peerToken := "sitx-publicstock-token"
	cleanup := func() {}
	createResp, err := adminC.POST("/api/v3/peer-banks", map[string]interface{}{
		"bank_code":      "222",
		"routing_number": 222,
		"base_url":       "http://127.0.0.1:1/api/v3/cross-bank-protocol",
		"api_token":      peerToken,
		"active":         true,
	})
	if err != nil {
		t.Fatalf("create peer bank: %v", err)
	}
	switch createResp.StatusCode {
	case http.StatusCreated:
		peerBankIDFloat, _ := createResp.Body["id"].(float64)
		peerBankID := strconv.FormatInt(int64(peerBankIDFloat), 10)
		cleanup = func() {
			if _, derr := adminC.DELETE("/api/v3/peer-banks/" + peerBankID); derr != nil {
				t.Logf("cleanup: delete peer bank: %v", derr)
			}
		}
	default:
		// Peer 222 already exists (live cohort stack). Reuse its token.
		peerToken = "shared-111-222"
		if v := os.Getenv("SITX_PEER_TOKEN"); v != "" {
			peerToken = v
		}
		t.Logf("peer bank 222 already registered (status %d); reusing existing token", createResp.StatusCode)
	}
	defer cleanup()

	// Seed a client with a holding, then make it public so /public-stock has a
	// row to advertise.
	sellerID, _, sellerC, _ := setupActivatedClient(t, adminC)
	sellerAcctID, _ := createClientAccount(t, adminC, sellerID, "RSD", 1_000_000)

	_, ticker, listingID := firstStock(t, adminC)
	if ticker == "" || listingID == 0 {
		t.Skip("no seeded stock — skipping")
	}

	orderResp, err := sellerC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id": listingID, "order_type": "market", "direction": "buy", "quantity": 3,
		"account_id": sellerAcctID,
	})
	if err != nil {
		t.Fatalf("seed buy: %v", err)
	}
	if orderResp.StatusCode != 201 {
		t.Skipf("seed buy returned %d — skipping", orderResp.StatusCode)
	}
	if !tryWaitForOrderFill(t, sellerC, int(helpers.GetNumberField(t, orderResp, "id")), 45*time.Second) {
		t.Skip("seed buy did not fill — skipping")
	}

	// Find the resulting holding id from the portfolio, then make it public.
	portResp, err := sellerC.GET("/api/v3/me/portfolio")
	if err != nil {
		t.Fatalf("get portfolio: %v", err)
	}
	helpers.RequireStatus(t, portResp, 200)
	pos := firstStockPosition(t, portResp.Body)
	if pos == nil {
		t.Skip("no stock position after fill — skipping")
	}
	var holdingID uint64
	if f, ok := pos["holding_id"].(float64); ok {
		holdingID = uint64(f)
	}
	if holdingID == 0 {
		t.Skipf("could not resolve holding_id from position: %v", pos)
	}

	makePublicResp, err := sellerC.POST("/api/v3/me/otc/stocks", map[string]interface{}{
		"direction": "sell", "holding_id": holdingID, "quantity": 2, "price_per_unit": "100.00",
	})
	if err != nil {
		t.Fatalf("make holding public: %v", err)
	}
	if makePublicResp.StatusCode != 201 {
		t.Fatalf("make holding public: want 201, got %d body=%s", makePublicResp.StatusCode, string(makePublicResp.RawBody))
	}

	wantSellerID := "client-" + strconv.Itoa(sellerID)

	// (1) GET /public-stock peer-authed — assert the seller id is the standard
	// "client-<N>" form, NOT a bare numeric.
	psBody := peerGet(t, "/api/v3/cross-bank-protocol/public-stock", peerToken)
	var stocks []struct {
		Stock   map[string]any `json:"stock"`
		Sellers []struct {
			Seller struct {
				RoutingNumber int64  `json:"routingNumber"`
				ID            string `json:"id"`
			} `json:"seller"`
			Amount int64 `json:"amount"`
		} `json:"sellers"`
	}
	if err := json.Unmarshal(psBody, &stocks); err != nil {
		t.Fatalf("public-stock not a bare array: %v body=%s", err, string(psBody))
	}
	var foundSeller bool
	for _, s := range stocks {
		for _, sel := range s.Sellers {
			id := sel.Seller.ID
			// Hard invariant: a bare numeric owner id must never reach the wire.
			if id != "" {
				if _, perr := strconv.Atoi(id); perr == nil {
					t.Errorf("public-stock advertised a BARE NUMERIC seller id %q (must be standard ForeignBankId form, e.g. client-<N>)", id)
				}
			}
			if id == wantSellerID {
				foundSeller = true
				if sel.Seller.RoutingNumber == 0 {
					t.Errorf("seller routingNumber must be set, got 0 for %q", id)
				}
			}
		}
	}
	if !foundSeller {
		t.Fatalf("public-stock did not advertise our seller as %q; body=%s", wantSellerID, string(psBody))
	}

	// (2) POST /negotiations peer-authed: echo the catalog's seller id back
	// verbatim, with an ARBITRARY OPAQUE buyerId.id (a UUID — not client-/employee-).
	// Per §2.3 we must NOT interpret it → must be ACCEPTED (201).
	// An opaque buyer id in a scheme WE do not own (UUID-shaped); the gateway
	// must store it verbatim and must NOT format-check it (§2.3).
	opaqueBuyer := "550e8400-e29b-41d4-a716-446655440000"
	offer := map[string]any{
		"stock":          map[string]any{"ticker": ticker},
		"settlementDate": "2030-12-31T00:00:00+00:00",
		"pricePerUnit":   map[string]any{"amount": 100, "currency": "RSD"},
		"premium":        map[string]any{"amount": 5, "currency": "RSD"},
		"buyerId":        map[string]any{"routingNumber": 222, "id": opaqueBuyer},
		"sellerId":       map[string]any{"routingNumber": 111, "id": wantSellerID},
		"amount":         2,
		"lastModifiedBy": map[string]any{"routingNumber": 222, "id": opaqueBuyer},
	}
	negResp, err := adminC.POSTWithHeaders("/api/v3/cross-bank-protocol/negotiations", offer, map[string]string{
		"X-Api-Key":   peerToken,
		"X-Bank-Code": "222",
	})
	if err != nil {
		t.Fatalf("create negotiation: %v", err)
	}
	if negResp.StatusCode != http.StatusCreated {
		t.Fatalf("opaque buyer id + standard seller id MUST be accepted (201), got %d body=%s", negResp.StatusCode, string(negResp.RawBody))
	}
	if id, _ := negResp.Body["id"].(string); id == "" {
		t.Errorf("negotiation response missing ForeignBankId.id: %s", string(negResp.RawBody))
	}

	// (3) A > 64-byte id is still rejected (§2.3 max length).
	overlong := strings.Repeat("x", 65)
	offer["buyerId"] = map[string]any{"routingNumber": 222, "id": overlong}
	offer["lastModifiedBy"] = map[string]any{"routingNumber": 222, "id": overlong}
	overResp, err := adminC.POSTWithHeaders("/api/v3/cross-bank-protocol/negotiations", offer, map[string]string{
		"X-Api-Key":   peerToken,
		"X-Bank-Code": "222",
	})
	if err != nil {
		t.Fatalf("create negotiation (overlong): %v", err)
	}
	if overResp.StatusCode != http.StatusBadRequest {
		t.Errorf("a 65-byte buyerId.id must be rejected with 400 (§2.3 max 64 bytes), got %d body=%s", overResp.StatusCode, string(overResp.RawBody))
	}
}

// peerGet performs a peer-authenticated GET and returns the raw body, failing
// the test on transport error or non-200 status.
func peerGet(t *testing.T, path, apiKey string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, cfg.GatewayURL+path, nil)
	if err != nil {
		t.Fatalf("build peer GET: %v", err)
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("X-Bank-Code", "222")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("peer GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("peer GET %s: status %d body=%s", path, resp.StatusCode, string(body))
	}
	return body
}
