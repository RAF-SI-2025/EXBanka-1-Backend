# Cross-Bank Public-Stock Option Negotiation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let our buyer initiate an option negotiation against any seller a peer publishes on the protocol-standard `/public-stock`, in addition to the existing `/public-option-offers` path.

**Architecture:** Synthesize `sell_initiated` remote `OTCOffer` "shell" rows (no preset terms) from each peer's `/public-stock`, upserting them into the existing `otc_offers` mirror with a `ps:`-namespaced `native_id`. They flow through the existing list/bid/dispatch machinery unchanged. A new `has_preset_terms` flag distinguishes shells from preset option-offers; reconciliation is namespace-scoped so the two sources never clobber each other; bids on a shell re-validate the peer's live `/public-stock` before dispatch.

**Tech Stack:** Go, GORM/Postgres, gRPC (stock-service ↔ api-gateway), the `otccache` refresher + `interbank-service` `PeerEgressService.ProxyToPeer` egress.

**Spec:** `docs/superpowers/specs/2026-06-10-cross-bank-public-stock-option-negotiation-design.md`

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `stock-service/internal/model/otc_offer.go` | OTCOffer schema + `ps:` prefix const | add `HasPresetTerms` column + `RemoteStockShellPrefix` const |
| `stock-service/internal/repository/otc_offer_repository.go` | mirror upsert + namespace-scoped reconcile | add `has_preset_terms` to upsert; scope `ReconcileRemoteNotSeen`; add `ReconcileRemoteShellsNotSeen` |
| `stock-service/internal/otccache/option_cache.go` | refresher: ingest `/public-stock` → shells; carry the flag | new `fetchPeerStocks` + `buildAndMirrorRemoteStockShells`; wire into `refresh`; add `HasPresetTerms` to `OptionOffer`; widen the mirror interface |
| `stock-service/internal/handler/otc_negotiation_remote.go` | bid: shell freshness guard | re-validate `/public-stock` before dispatch when `!HasPresetTerms` |
| stock-service gRPC option-list projection + `contract/stockpb` + api-gateway projection | expose `has_preset_terms` to the FE | add the field end-to-end |
| `stock-service/internal/handler/peer_otc_grpc_handler.go` | inbound any-price (verify only) | none (Task 6 is a read+document) |
| `VERSION`, `api-gateway/internal/version/version.go` | semver | MINOR bump |

---

## Task 1: Add `HasPresetTerms` to the model and propagate it on existing option-offer upserts

**Files:**
- Modify: `stock-service/internal/model/otc_offer.go`
- Modify: `stock-service/internal/repository/otc_offer_repository.go` (`UpsertRemote`)
- Modify: `stock-service/internal/otccache/option_cache.go` (`buildAndMirrorRemoteOffers`)
- Test: `stock-service/internal/repository/otc_offer_remote_test.go`

- [ ] **Step 1: Write the failing test** — append to `otc_offer_remote_test.go`:

```go
func TestUpsertRemote_SetsHasPresetTerms(t *testing.T) {
	r := newTestOfferRepo(t) // existing helper in this _test package
	ncy := "USD"
	native := "peer-offer-1"
	row := &model.OTCOffer{
		RoutingNumber: 222, NativeID: &native, Direction: model.OTCDirectionSellInitiated,
		Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), Premium: decimal.NewFromInt(2),
		StrikeCurrency: &ncy, PremiumCurrency: &ncy,
		HasPresetTerms: true,
		LastModifiedByPrincipalType: "system",
	}
	id, err := r.UpsertRemote(row, time.Now().UTC())
	if err != nil { t.Fatalf("upsert: %v", err) }
	got, err := r.GetRemoteByID(id)
	if err != nil { t.Fatalf("get: %v", err) }
	if !got.HasPresetTerms { t.Fatalf("HasPresetTerms = false, want true") }
}
```

- [ ] **Step 2: Run it, verify it fails** — `cd stock-service && go test ./internal/repository/ -run TestUpsertRemote_SetsHasPresetTerms -v` → FAIL (`HasPresetTerms` undefined).

- [ ] **Step 3: Add the column + prefix const** — in `model/otc_offer.go`, inside the `OTCOffer` struct (next to `RemoteSellerID`), add:

```go
	// HasPresetTerms: true ⇔ the offer carries owner-set terms (a /public-option-offers
	// listing — a negotiable "start position"). false ⇔ synthesized from /public-stock
	// (no preset strike/premium; fully buyer-negotiated). Local + remote option-offer rows
	// are true; only public-stock shells are false.
	HasPresetTerms bool `gorm:"not null;default:true" json:"has_preset_terms"`
```

And at package scope (near the `OTCDirection*`/`OTCOfferStatus*` consts):

```go
// RemoteStockShellPrefix namespaces native_id for shells synthesized from a peer's
// /public-stock ("ps:<sellerRn>:<sellerId>:<ticker>"), keeping them distinct from
// option-offer rows (whose native_id is the peer's offer id) so reconcile can scope.
const RemoteStockShellPrefix = "ps:"
```

- [ ] **Step 4: Persist it on upsert** — in `repository/otc_offer_repository.go` `UpsertRemote`, add `"has_preset_terms"` to the `DoUpdates` `AssignmentColumns` list (so a re-poll keeps it correct):

```go
		DoUpdates: clause.AssignmentColumns([]string{
			"initiator_bank_code", "remote_seller_id", "direction", "ticker",
			"quantity", "strike_price", "premium", "settlement_date",
			"strike_currency", "premium_currency", "status", "has_preset_terms",
			"last_seen_at", "updated_at",
		}),
```

- [ ] **Step 5: Set it on existing option-offer ingestion** — in `otccache/option_cache.go` `buildAndMirrorRemoteOffers`, on the `remoteRow := &model.OTCOffer{...}` literal add:

```go
				HasPresetTerms: true,
```

- [ ] **Step 6: Run the test, verify it passes** — `cd stock-service && go test ./internal/repository/ -run TestUpsertRemote_SetsHasPresetTerms -v` → PASS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/model/otc_offer.go stock-service/internal/repository/otc_offer_repository.go stock-service/internal/otccache/option_cache.go stock-service/internal/repository/otc_offer_remote_test.go
git commit -m "feat(otc): add HasPresetTerms column to OTCOffer (option-offers = true)"
```

---

## Task 2: Namespace-scoped reconciliation (shells vs option-offers don't clobber)

**Why:** `ReconcileRemoteNotSeen(peerRouting, seen)` cancels every open remote row for a peer not in `seen`. Run once with only option-offer ids it would cancel the shells, and vice-versa. Scope each reconcile to its own `native_id` namespace.

**Files:**
- Modify: `stock-service/internal/repository/otc_offer_repository.go`
- Test: `stock-service/internal/repository/otc_offer_remote_test.go`

- [ ] **Step 1: Write the failing test:**

```go
func TestReconcile_NamespaceScoped(t *testing.T) {
	r := newTestOfferRepo(t)
	mk := func(native string, preset bool) uint64 {
		n := native
		id, err := r.UpsertRemote(&model.OTCOffer{
			RoutingNumber: 222, NativeID: &n, Direction: model.OTCDirectionSellInitiated,
			Ticker: "AAPL", Quantity: decimal.NewFromInt(1),
			HasPresetTerms: preset, LastModifiedByPrincipalType: "system",
		}, time.Now().UTC())
		if err != nil { t.Fatalf("upsert %s: %v", native, err) }
		return id
	}
	optID := mk("peer-offer-1", true)
	shellID := mk("ps:222:client-5:AAPL", false)

	// Option-offer reconcile (peer listed NO option offers) must NOT cancel the shell.
	if _, err := r.ReconcileRemoteNotSeen(222, nil); err != nil { t.Fatal(err) }
	if got, _ := r.GetRemoteByID(shellID); got.Status != model.OTCOfferStatusOpen {
		t.Fatalf("shell cancelled by option reconcile: %s", got.Status)
	}
	if got, _ := r.GetRemoteByID(optID); got.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("option-offer not cancelled: %s", got.Status)
	}
	// Shell reconcile (peer listed NO public stocks) cancels the shell only.
	if _, err := r.ReconcileRemoteShellsNotSeen(222, nil); err != nil { t.Fatal(err) }
	if got, _ := r.GetRemoteByID(shellID); got.Status != model.OTCOfferStatusCancelled {
		t.Fatalf("shell not cancelled by shell reconcile: %s", got.Status)
	}
}
```

- [ ] **Step 2: Run it, verify it fails** — `go test ./internal/repository/ -run TestReconcile_NamespaceScoped -v` → FAIL (`ReconcileRemoteShellsNotSeen` undefined; and the option reconcile cancels the shell).

- [ ] **Step 3: Implement scoping** — replace `ReconcileRemoteNotSeen` body and add the shell variant via a shared private helper:

```go
// reconcileScoped flips open remote rows for peerRouting whose native_id is NOT in
// seen to cancelled, restricted to the given native_id namespace. shellsOnly=true
// touches only "ps:%" rows; false touches only non-shell rows.
func (r *OTCOfferRepository) reconcileScoped(peerRouting int64, seenNativeIDs []string, shellsOnly bool) (int64, error) {
	q := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCOffer{}).
		Where("routing_number = ? AND status = ?", peerRouting, model.OTCOfferStatusOpen)
	like := model.RemoteStockShellPrefix + "%"
	if shellsOnly {
		q = q.Where("native_id LIKE ?", like)
	} else {
		q = q.Where("native_id NOT LIKE ?", like)
	}
	if len(seenNativeIDs) > 0 {
		q = q.Where("native_id NOT IN ?", seenNativeIDs)
	}
	res := q.Updates(map[string]any{"status": model.OTCOfferStatusCancelled, "updated_at": time.Now().UTC()})
	return res.RowsAffected, res.Error
}

// ReconcileRemoteNotSeen reconciles the peer's OPTION-OFFER rows (non-shell namespace).
func (r *OTCOfferRepository) ReconcileRemoteNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error) {
	return r.reconcileScoped(peerRouting, seenNativeIDs, false)
}

// ReconcileRemoteShellsNotSeen reconciles the peer's /public-stock SHELL rows.
func (r *OTCOfferRepository) ReconcileRemoteShellsNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error) {
	return r.reconcileScoped(peerRouting, seenNativeIDs, true)
}
```

- [ ] **Step 4: Run the test, verify it passes** — `go test ./internal/repository/ -run TestReconcile_NamespaceScoped -v` → PASS.

- [ ] **Step 5: Run the full repository package** — `go test ./internal/repository/ -count=1` → PASS (confirms the existing `ReconcileRemoteNotSeen` callers still behave).

- [ ] **Step 6: Commit**

```bash
git add stock-service/internal/repository/otc_offer_repository.go stock-service/internal/repository/otc_offer_remote_test.go
git commit -m "feat(otc): namespace-scoped remote reconcile (shells vs option-offers)"
```

---

## Task 3: Ingest `/public-stock` → shells in the option refresher

**Files:**
- Modify: `stock-service/internal/otccache/option_cache.go`
- Test: `stock-service/internal/otccache/option_cache_test.go`

- [ ] **Step 1: Write the failing test** (a fake mirror records upserts + reconciles):

```go
func TestBuildAndMirrorRemoteStockShells(t *testing.T) {
	fake := &fakeMirror{} // implements RemoteOfferMirror; see helper below
	r := &OptionRefresher{mirror: fake}
	stocks := []sitx.PublicStock{{
		Stock:   sitx.StockDescription{Ticker: "AAPL"},
		Sellers: []sitx.PublicSeller{{Seller: sitx.ForeignBankId{RoutingNumber: 222, ID: "client-5"}, Amount: 100}},
	}}
	out := r.buildAndMirrorRemoteStockShells("bank222", 222, stocks)
	if len(out) != 1 { t.Fatalf("rows = %d, want 1", len(out)) }
	got := fake.upserts[0]
	if got.NativeID == nil || *got.NativeID != "ps:222:client-5:AAPL" {
		t.Fatalf("native_id = %v", got.NativeID)
	}
	if got.HasPresetTerms { t.Fatalf("shell HasPresetTerms = true, want false") }
	if got.Direction != model.OTCDirectionSellInitiated { t.Fatalf("direction = %s", got.Direction) }
	if !got.StrikePrice.IsZero() || !got.Premium.IsZero() { t.Fatalf("shell must have zero terms") }
	if got.StrikeCurrency != nil || got.PremiumCurrency != nil { t.Fatalf("shell currencies must be nil") }
	if fake.shellReconcilePeer != 222 || len(fake.shellReconcileSeen) != 1 {
		t.Fatalf("shell reconcile not scoped to peer 222 with the seen id")
	}
}
```

Add this fake to the test file (it must satisfy the widened interface from Step 3):

```go
type fakeMirror struct {
	upserts            []*model.OTCOffer
	shellReconcilePeer int64
	shellReconcileSeen []string
}
func (f *fakeMirror) UpsertRemote(o *model.OTCOffer, _ time.Time) (uint64, error) {
	f.upserts = append(f.upserts, o); return uint64(len(f.upserts)), nil
}
func (f *fakeMirror) ReconcileRemoteNotSeen(_ int64, _ []string) (int64, error) { return 0, nil }
func (f *fakeMirror) ReconcileRemoteShellsNotSeen(peer int64, seen []string) (int64, error) {
	f.shellReconcilePeer = peer; f.shellReconcileSeen = seen; return 0, nil
}
```

- [ ] **Step 2: Run it, verify it fails** — `cd stock-service && go test ./internal/otccache/ -run TestBuildAndMirrorRemoteStockShells -v` → FAIL (method + interface member undefined).

- [ ] **Step 3: Widen the mirror interface** — in `option_cache.go`, add to `RemoteOfferMirror`:

```go
	ReconcileRemoteShellsNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error)
```

(`*repository.OTCOfferRepository` already satisfies it after Task 2.)

- [ ] **Step 4: Add the shell builder** — in `option_cache.go`, after `buildAndMirrorRemoteOffers`:

```go
// buildAndMirrorRemoteStockShells converts a peer's /public-stock listings into
// biddable sell_initiated SHELL rows (no preset terms — the buyer proposes
// strike/premium/settlement on bid). native_id is "ps:<sellerRn>:<sellerId>:<ticker>"
// so reconcile scopes to the shell namespace. Called ONLY after a successful peer fetch.
func (r *OptionRefresher) buildAndMirrorRemoteStockShells(peerBankCode string, peerRouting int64, stocks []sitx.PublicStock) []OptionOffer {
	if peerRouting == model.OwnRouting() {
		log.Printf("WARN otccache(stock-shells): peer bank_code=%s routing=%d collides with own routing — skipping", peerBankCode, peerRouting)
		return nil
	}
	now := time.Now().UTC()
	seen := make([]string, 0)
	out := make([]OptionOffer, 0)
	for i := range stocks {
		ticker := stocks[i].Stock.Ticker
		if ticker == "" { continue }
		for _, s := range stocks[i].Sellers {
			if s.Seller.RoutingNumber == model.OwnRouting() || s.Seller.ID == "" { continue }
			native := fmt.Sprintf("%s%d:%s:%s", model.RemoteStockShellPrefix, peerRouting, s.Seller.ID, ticker)
			row := OptionOffer{
				Kind: "remote", BankCode: peerBankCode, RoutingNumber: peerRouting,
				OfferID: native, SellerID: s.Seller.ID, Direction: model.OTCDirectionSellInitiated,
				Ticker: ticker, Amount: s.Amount, HasPresetTerms: false,
			}
			if r.mirror != nil {
				n := native; bc := peerBankCode; sid := s.Seller.ID
				remoteRow := &model.OTCOffer{
					RoutingNumber: peerRouting, NativeID: &n, InitiatorBankCode: &bc, RemoteSellerID: &sid,
					InitiatorOwnerType: model.OwnerBank, Direction: model.OTCDirectionSellInitiated,
					Ticker: ticker, Quantity: decimal.NewFromInt(s.Amount),
					StrikePrice: decimal.Zero, Premium: decimal.Zero,
					StrikeCurrency: nil, PremiumCurrency: nil,
					HasPresetTerms: false, Status: model.OTCOfferStatusOpen,
					LastModifiedByPrincipalType: "system", LastModifiedByPrincipalID: 0,
				}
				if id, err := r.mirror.UpsertRemote(remoteRow, now); err != nil {
					log.Printf("otccache(stock-shells): upsert peer=%s %s failed: %v", peerBankCode, native, err)
				} else {
					row.LocalID = id; seen = append(seen, native)
				}
			}
			out = append(out, row)
		}
	}
	if r.mirror != nil {
		if n, err := r.mirror.ReconcileRemoteShellsNotSeen(peerRouting, seen); err != nil {
			log.Printf("otccache(stock-shells): reconcile peer=%s failed: %v", peerBankCode, err)
		} else if n > 0 {
			log.Printf("otccache(stock-shells): reconciled %d vanished shells from peer=%s", n, peerBankCode)
		}
	}
	return out
}
```

- [ ] **Step 5: Add `HasPresetTerms` to the cache `OptionOffer` struct** — in `option_cache.go`, add `HasPresetTerms bool` to `OptionOffer`; set `HasPresetTerms: true` in `fetchLocal`'s row literal and in `buildAndMirrorRemoteOffers`'s `OptionOffer{...}` literal (local + option-offer rows are preset).

- [ ] **Step 6: Add `fetchPeerStocks` + wire into `refresh`** — add:

```go
func (r *OptionRefresher) fetchPeerStocks(ctx context.Context, peer *transactionpb.PeerBank) ([]OptionOffer, error) {
	proxyResp, err := r.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peer.GetBankCode(), Method: http.MethodGet, Path: "/public-stock",
	})
	if err != nil { return nil, err }
	if proxyResp.GetStatusCode() != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", proxyResp.GetStatusCode(), string(proxyResp.GetBody()))
	}
	var resp sitx.PublicStocksResponse
	if err := json.Unmarshal(proxyResp.GetBody(), &resp); err != nil { return nil, err }
	return r.buildAndMirrorRemoteStockShells(peer.GetBankCode(), peerRoutingOf(peer), resp), nil
}
```

In `refresh`, inside the per-peer goroutine, after the existing option-offer append, ALSO fetch stocks (independent failure — a peer with no `/public-option-offers` still yields shells):

```go
				if shells, serr := r.fetchPeerStocks(cycleCtx, peer); serr != nil {
					log.Printf("otccache(stock-shells): peer %s fetch failed: %v", peer.GetBankCode(), serr)
				} else {
					mu.Lock(); offers = append(offers, shells...); mu.Unlock()
				}
```

(Leave `peersReached`/`peersTotal` driven by the existing option-offer fetch.)

- [ ] **Step 7: Run the tests** — `go test ./internal/otccache/ -count=1` → PASS.

- [ ] **Step 8: Commit**

```bash
git add stock-service/internal/otccache/
git commit -m "feat(otc): synthesize biddable shells from peer /public-stock"
```

---

## Task 4: Bid freshness guard — re-validate `/public-stock` for shells before dispatch

**Files:**
- Modify: `stock-service/internal/handler/otc_negotiation_remote.go` (`openRemoteNegotiation`)
- Test: `stock-service/internal/handler/otc_negotiation_remote_action_test.go`

- [ ] **Step 1: Write the failing test** — using the package's existing `fakePeerDispatcher`, assert: a shell (`HasPresetTerms=false`) whose `(seller,ticker)` is absent from the peer's live `/public-stock` returns `FailedPrecondition` and never dispatches `POST /negotiations`. (Model it on `otc_negotiation_remote_action_test.go`'s existing remote-bid tests; seed a remote shell row via the test repo, set the dispatcher's `/public-stock` response to an empty array.)

```go
func TestOpenRemoteNegotiation_ShellFreshnessGuard_Gone(t *testing.T) {
	// ... arrange: shell remote offer (HasPresetTerms=false, ticker AAPL, seller {222,"client-5"})
	// dispatcher returns 200 [] for GET /public-stock (seller no longer offers AAPL)
	// act: openRemoteNegotiation(...)
	// assert: status code == codes.FailedPrecondition AND dispatcher recorded NO POST /negotiations
}
```

- [ ] **Step 2: Run it, verify it fails** — `go test ./internal/handler/ -run ShellFreshnessGuard -v` → FAIL (no guard yet; it dispatches).

- [ ] **Step 3: Implement the guard** — in `openRemoteNegotiation`, AFTER the successful `GetRemoteByID` + the `buy_initiated` guard, BEFORE composing/dispatching the offer:

```go
	// Freshness guard for /public-stock shells (no preset terms): the mirror may be
	// stale, so re-confirm the seller still publicly offers this ticker before we
	// dispatch a doomed negotiation. Preset option-offers skip this — the seller's
	// bank validates them on POST /negotiations.
	if !remoteOffer.HasPresetTerms {
		live, perr := h.peerDispatch.Proxy(ctx, strconv.FormatInt(remoteOffer.RoutingNumber, 10), "", "", "GET", "/public-stock", nil)
		// (use the same egress the bid dispatch uses; signature per peerDispatch.Proxy)
		if perr != nil {
			return nil, false, status.Errorf(codes.FailedPrecondition, "cannot re-validate peer stock listing: %v", perr)
		}
		if !publicStockHasSeller(live, derefStr(remoteOffer.RemoteSellerID), remoteOffer.Ticker) {
			return nil, false, status.Error(codes.FailedPrecondition, "peer no longer offers this stock for OTC")
		}
	}
```

Add the two helpers in the same file:

```go
func derefStr(p *string) string { if p == nil { return "" }; return *p }

// publicStockHasSeller reports whether a /public-stock body lists (seller, ticker).
func publicStockHasSeller(body []byte, sellerID, ticker string) bool {
	var resp contractsitx.PublicStocksResponse
	if json.Unmarshal(body, &resp) != nil { return false }
	for _, ps := range resp {
		if ps.Stock.Ticker != ticker { continue }
		for _, s := range ps.Sellers {
			if s.Seller.ID == sellerID { return true }
		}
	}
	return false
}
```

> **Confirm at implementation time:** the exact signature of `h.peerDispatch.Proxy` (it returns `(resp []byte, code int, err error)` in `otc_negotiation_remote_action.go`; the bid path here may use a thinner dispatcher — match whichever `peerDispatch` field `openRemoteNegotiation` already holds, reusing the SAME call shape used for the `POST /negotiations` dispatch in this file). If the handler's dispatcher only exposes `CreateNegotiation`, add a narrow `PublicStock(ctx, peerCode) ([]byte,int,error)` method to that interface + its real adapter, mirroring how `cache.go` reaches `/public-stock`.

- [ ] **Step 4: Run the tests** — `go test ./internal/handler/ -run ShellFreshnessGuard -v` → PASS; then `go test ./internal/handler/ -count=1` → PASS.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/handler/otc_negotiation_remote.go stock-service/internal/handler/otc_negotiation_remote_action_test.go
git commit -m "feat(otc): re-validate peer /public-stock before bidding on a shell"
```

---

## Task 5: Expose `has_preset_terms` to the frontend (gRPC → gateway)

**Files:**
- Modify: `contract/proto/stock.proto` (the option-offer list message) → `make proto`
- Modify: stock-service handler that projects `otccache.OptionOffer` → the gRPC list response
- Modify: api-gateway projection for `GET /api/v3/otc/options`
- Test: api-gateway handler test for the option-offers list

- [ ] **Step 1: Find the chain** — `grep -rn "ListUnifiedOptionOffers\|kind.*remote\|HasPresetTerms\|has_preset_terms" stock-service contract/proto api-gateway/internal/handler | grep -i option`. Identify (a) the proto message for one option offer, (b) the stock-service func mapping `otccache.OptionOffer` → that proto, (c) the gateway func mapping the proto → JSON.

- [ ] **Step 2: Add the proto field** — in the per-offer message in `contract/proto/stock.proto`, add `bool has_preset_terms = <next_tag>;`. Run `make proto`. Expected: `contract/stockpb/*.pb.go` regenerated with `GetHasPresetTerms()`.

- [ ] **Step 3: Map it in stock-service** — in the func that builds the gRPC option-offer message from `otccache.OptionOffer`, set `HasPresetTerms: o.HasPresetTerms`.

- [ ] **Step 4: Map it in the gateway** — in the gateway projection for `GET /api/v3/otc/options`, add `"has_preset_terms": o.GetHasPresetTerms()` to the JSON object.

- [ ] **Step 5: Write/extend a gateway handler test** — assert the JSON for a remote offer includes `has_preset_terms` and that a shell-origin offer reports `false`, a preset offer `true` (use the existing option-offers handler test + mocks in `api-gateway/internal/handler`).

- [ ] **Step 6: Run** — `cd contract && go build ./... && cd ../stock-service && go test ./... -count=1` and `cd ../api-gateway && go test ./internal/handler/ -count=1` → PASS.

- [ ] **Step 7: Update docs** — add `has_preset_terms` to the `GET /api/v3/otc/options` response in `docs/api/REST_API_v3.md` and re-run `make swagger`.

- [ ] **Step 8: Commit**

```bash
git add contract/ stock-service/ api-gateway/ docs/api/REST_API_v3.md
git commit -m "feat(otc): expose has_preset_terms on the option-offers list"
```

---

## Task 6: Verify inbound any-price (no change) + document

**Files:** read-only — `stock-service/internal/handler/peer_otc_grpc_handler.go` (`CreateNegotiation`, `UpdateNegotiation`)

- [ ] **Step 1: Read** both handlers and confirm there is NO rejection of a bid/counter for being below a preset/minimum strike or premium (the only checks are: currency-in-enum, well-formed buyer/seller ids, `amount > 0`, turn/`isOngoing`). Direction-1 live testing already demonstrated arbitrary strikes (44 then 45) accepted.

- [ ] **Step 2: Document** — add a one-line note to `docs/protocol/bank-4-interop-otc-results.md` (A-5 section) that inbound peer bids/counters accept any strike/premium/date by design (no minimum floor). If — contrary to expectation — a floor IS found, STOP and raise it (removing it is a behavior change requiring its own task + the user's nod).

- [ ] **Step 3: Commit** (docs only)

```bash
git add docs/protocol/bank-4-interop-otc-results.md
git commit -m "docs(otc): confirm inbound peer bids accept any strike/premium/date"
```

---

## Task 7: Version bump, integration test, CI

**Files:** `VERSION`, `api-gateway/internal/version/version.go`, `test-app/workflows/`

- [ ] **Step 1: Bump VERSION** — MINOR (new capability + field). Set `VERSION` to the next `x.(y+1).0` and update `var Version` in `version.go` to match.

- [ ] **Step 2: Integration test** — add a `test-app/workflows/` test: peer publishes a public stock (no option-offer) → it surfaces in `GET /api/v3/otc/options?kind=remote` with `has_preset_terms=false` → buyer bids with chosen terms → assert the negotiation opens on the peer (200/201) and a local remote-negotiation row exists. Reuse `helpers_test.go`.

- [ ] **Step 3: Run the FULL CI locally** — `make ci` (build + unit tests + lint + `gofmt -l .` empty + `go mod tidy` no diff). Fix anything it surfaces.

- [ ] **Step 4: Commit**

```bash
git add VERSION api-gateway/internal/version/version.go test-app/
git commit -m "test(otc): integration test for bidding off a peer /public-stock; bump VERSION"
```

- [ ] **Step 5: Manual two-stack verification (optional)** — with Banka 4 up: confirm Banka 4's AAPL `/public-stock` listing now surfaces for our buyer with `has_preset_terms=false`, and a bid opens a negotiation on Banka 4 (reverse of the verified Direction 1). (Banka 4's exercise key-length item B-1 must be fixed on their side for the exercise leg.)

---

## Self-Review (done)

- **Spec coverage:** §3.1 model→T1; §3.2 ingestion+reconcile→T2,T3; §3.3 bid+freshness→T4; §3.4 no-dedup→inherent (no suppression added); §3.5 any-price→T6; §3.6 response field→T5; §7 testing→each task + T7. All covered.
- **Type consistency:** `HasPresetTerms` (model + cache `OptionOffer` + proto), `RemoteStockShellPrefix`, `ReconcileRemoteShellsNotSeen`, `buildAndMirrorRemoteStockShells`, `fetchPeerStocks`, `publicStockHasSeller`/`derefStr` — names used identically across tasks.
- **Open confirm:** Task 4 Step 3 flags the exact `peerDispatch` signature to match at implementation time (the only spot needing a look at the handler's dispatcher field).
