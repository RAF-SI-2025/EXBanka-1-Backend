# Option Offers as the Cross-Bank `/public-stock` Surface — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `/api/v3/me/otc/options` option offers the single backend surface for cross-bank discovery — served as SI-TX `/public-stock` listings (termless "optionable inventory", one per owner+ticker+direction), remove the proprietary `/public-option-offers` endpoint and its ingestion, add a PUT to edit an offer's total quantity, and re-source the read DTO's strike/premium/settlement from the negotiation chain per viewer.

**Architecture:** Four sequential phases, each ending green and committable: **A** schema/model (drop preset-term columns + partial-unique-index migration), **B** create/edit API (409 on duplicate + new PUT total-quantity), **C** cross-bank serving swap (`GetPublicStocks` reads OTCOffers) + full `/public-option-offers` removal (endpoint, proto, ingestion), **D** read-surface term projection. Spec: `docs/superpowers/specs/2026-06-10-option-offers-as-cross-bank-stock-design.md`.

**Tech Stack:** Go workspace monorepo, gRPC/protobuf (`contract/proto/stock/stock.proto` → `make proto`), GORM/Postgres, Gin gateway, `test-app` integration suite, `make ci`.

---

## ⚠️ REVISION (2026-06-10, after A1 impact analysis) — corrected sequencing

The first implementer's analysis showed the original "drop columns first" order was wrong: dropping `model.OTCOffer.{StrikePrice,Premium,SettlementDate,StrikeCurrency,PremiumCurrency,HasPresetTerms}` first breaks cross-bank money-path **branch conditions** (`otc_negotiation_remote.go` keys on `HasPresetTerms`) and functions later phases delete (`buildAndMirrorRemoteOffers`, `GetPublicOptionOffers` write/emit them), and it strands the **offer-expiry cron** (`ListExpiringOffers` queries `settlement_date`). The column drop must come **LAST**, after every consumer is migrated/deleted.

**Decisions locked in:**
- `HasPresetTerms` branch collapses to **always-shell-path** (this is the spec's "uniform termless model"): every remote offer gets the `/public-stock` freshness guard and derives premium/strike currency from the **bidder's bound account**.
- **Offers never auto-expire** (user decision): they end only via cancel (DELETE) or consume (accept). Remove `ListExpiringOffers` + the **offer**-expiry portion of `OTCExpiryCron`; **contract** expiry (formed options, by the contract's settlement_date) is **untouched**.

**Corrected execution order (supersedes the Phase A→E order below):**
1. **R1 — A3** (migration + partial unique index): standalone, safe; merge duplicate open offers + `ux_otc_offer_open_owner_ticker_dir`. (Plan Task A3.) ✅ DONE
2. **R2 — negotiation-remote migration**: `otc_negotiation_remote.go` → always-shell-path; stop reading `HasPresetTerms`/preset currencies (columns still exist). NEW task — see below. ✅ DONE
2.5. **R9 (moved up) — remove OFFER expiry**: MUST land before B1. Once B1 stops writing `settlement_date`, new offers get a zero date (`0001-01-01`) which the offer-expiry cron would treat as instantly expired. Remove `ListExpiringOffers` + the offer-expiry pass first. (R9 detail below; the earlier "after C2" note was over-cautious — it only needs to precede B1 and R12.)
3. **R3 — B1** Create rejects duplicate + stops writing terms.
4. **R4 — B2** gateway create 409.
5. **R5 — B3** UpdateQuantity + PUT.
6. **R6 — C1** GetPublicStocks serves option offers.
7. **R7 — C2** remove `/public-option-offers` endpoint (deletes `GetPublicOptionOffers`).
8. **R8 — C3** remove outbound ingestion (deletes `buildAndMirrorRemoteOffers`).
9. **R9 — offer-expiry removal**: delete `ListExpiringOffers` + the offer-expiry pass in `OTCExpiryCron`; keep contract expiry; update `otc_expiry_cron_test.go` + `otc_offer_and_tax_test.go` expiry-offer tests. NEW task — see below.
10. **R10 — D1** `LatestRevisionByAuthorForOffer`.
11. **R11 — D2** term projection + drop `HasPresetTerms` from the cache DTO + proto + gateway.
12. **R12 — A1+A2 (final)** drop the 6 `model.OTCOffer` columns + fix ALL remaining model readers/writers: `buildAndMirrorRemoteStockShells` (stops writing zeros/nils), `toOTCOfferProto` (otc_options_handler), `UpsertRemote` `OnConflict` DoUpdates column list (otc_offer_repository), the `otc_offer_service.go` `Counter`/`Reject`/revision-seed readers, and all tests referencing the removed fields. (Plan Tasks A1+A2, now LAST.)
13. **R13 — E1+E2+E3** integration, docs, `VERSION 3.0.0`, full CI.

### NEW Task R2 — `otc_negotiation_remote.go` always-shell-path

**File:** `stock-service/internal/handler/otc_negotiation_remote.go` (`openRemoteNegotiation`).
- Remove the `if !remoteOffer.HasPresetTerms` / preset branches. The flow becomes uniform: ALWAYS run the `/public-stock` freshness guard (`publicStockHasSeller`) for the remote offer, and ALWAYS derive `premiumCurrency`/`strikeCurrency` from the bidder's bound account (`acct.GetCurrencyCode()`), never from `remoteOffer.PremiumCurrency`/`StrikeCurrency`.
- Keep the empty-currency guard (reject if the account currency can't be resolved).
- This makes the handler stop *reading* `HasPresetTerms`/`PremiumCurrency`/`StrikeCurrency` on `model.OTCOffer`, while the columns still physically exist (dropped in R12).
- TDD: a test that an open-negotiation on a remote offer derives currency from the bidder account and runs the freshness guard regardless of any preset flag. Run unit tests for the package; commit.

### NEW Task R9 — Remove offer-expiry (keep contract expiry)

**Files:** `stock-service/internal/repository/otc_offer_repository.go` (delete `ListExpiringOffers`), `stock-service/internal/service/otc_expiry_cron.go` (delete the offer-expiry pass + the `offers` dependency use; keep all contract-expiry logic, `WithExpiryWarning`, capital-gains), `stock-service/cmd/main.go` (drop the offers arg if the cron constructor changes), tests `otc_expiry_cron_test.go` + `otc_offer_and_tax_test.go` (remove offer-expiry assertions).
- TDD: assert the cron no longer expires offers (an old-style offer with a past settlement is left untouched) while still expiring a past-settlement **contract**. Run; commit.
- Do R9 AFTER C2 (so no emitter reads `settlement_date`) and BEFORE R12 (so the column drop has no `ListExpiringOffers` referencing `settlement_date`).

---

## ⚠️ CRITICAL GOTCHA — three different structs carry `StrikePrice`/`Premium`/`SettlementDate`

The Explore map flagged ~40 reads of these field names. **Only `model.OTCOffer` and the local model→`otccache.OptionOffer` mapping change.** The others are different types and must be left **untouched**:

| Type | Where | Action |
|---|---|---|
| `model.OTCOffer` (the listing) | `stock-service/internal/model/otc_offer.go` | **REMOVE the columns** (this plan) |
| `contractsitx.OtcOffer` (SI-TX wire negotiation object, from `protoToOffer`) | `peer_otc_grpc_handler.go` (vars named `offer` assigned from `protoToOffer(req.GetOffer())` or `var offer contractsitx.OtcOffer`) | **LEAVE** — these are negotiated terms on the wire |
| `model.PeerOptionContract` / formed contract (vars named `contract`) | `peer_otc_grpc_handler.go` (`contract.StrikePrice` etc.) | **LEAVE** — agreed contract terms |
| `otccache.OptionOffer` (cache DTO, var `o`) | `otc_handler.go`, `option_cache.go` | **KEEP fields**, re-sourced in Phase D |
| `model.OTCNegotiation` / `OTCNegotiationRevision` (chain) | `otc_negotiation*.go` | **LEAVE** — per-bid terms live here |

**Rule for the implementer:** before editing any `.StrikePrice`/`.Premium`/`.SettlementDate`/`.StrikeCurrency`/`.PremiumCurrency`/`.HasPresetTerms` read, confirm the receiver's declared type. Change it **only** if it is a `model.OTCOffer`. When in doubt, `grep` the variable's assignment.

---

## File map

| File | Responsibility | Phase |
|---|---|---|
| `stock-service/internal/model/otc_offer.go` | drop 6 term columns + `RemoteStockShellPrefix` stays (still used by shells) | A |
| `stock-service/cmd/main.go` | add partial unique index + run duplicate-merge migration | A |
| `stock-service/internal/repository/otc_offer_repository.go` | merge-duplicates migration fn; drop `has_preset_terms` raw-Exec in `UpsertRemoteShell`; `CountOpenByOwnerTickerDirection`; `UpdateQuantity` | A,B |
| `stock-service/internal/service/otc_offer_service.go` | `Create` drops term fields + rejects duplicate; new `UpdateQuantity` | B |
| `api-gateway/internal/handler/otc_negotiation_handler.go` | create handler drops term fields + maps 409; new `UpdateMyOption` PUT handler | B |
| `api-gateway/internal/router/router_v3.go` | add PUT route; remove `/public-option-offers` route | B,C |
| `stock-service/internal/handler/peer_otc_grpc_handler.go` | `GetPublicStocks` reads OTCOffers; delete `GetPublicOptionOffers` | C |
| `stock-service/internal/repository/otc_offer_repository.go` | `ListPublicOptionOffersForPeer` (offers for `/public-stock`) | C |
| `contract/proto/stock/stock.proto` | delete `GetPublicOptionOffers` RPC + 3 messages; `make proto` | C |
| `interbank-service/internal/handler/peer_otc_forwarder.go` | delete `GetPublicOptionOffers` passthrough | C |
| `api-gateway/internal/handler/peer_otc_handler.go` | delete `GetPublicOptionOffers` handler | C |
| `stock-service/internal/otccache/option_cache.go` | delete `fetchPeer`+`buildAndMirrorRemoteOffers`; local fetch drops term fields | C,D |
| `stock-service/internal/handler/otc_handler.go` / `otc_negotiation_handler.go` | request-scoped term projection (bidder chain / owner latest counter) | D |
| `stock-service/internal/repository/otc_negotiation_repository.go` | `LatestRevisionByAuthorForOffer` | D |
| docs + `VERSION` + `version.go` + swagger | docs/version per repo rules | all |

---

# PHASE A — Schema & model

### Task A1: Drop preset-term columns from `model.OTCOffer`

**Files:**
- Modify: `stock-service/internal/model/otc_offer.go:88-90,123-124,126-129`
- Test: `stock-service/internal/model/otc_offer_test.go` (compile-only guard)

- [ ] **Step 1: Write a failing compile guard test**

Add to `stock-service/internal/model/otc_offer_test.go`:

```go
func TestOTCOffer_HasNoPresetTermFields(t *testing.T) {
	// Inventory model: an option offer is (owner, ticker, quantity) only.
	// This test fails to COMPILE until the term fields are removed.
	o := OTCOffer{}
	typ := reflect.TypeOf(o)
	for _, f := range []string{"StrikePrice", "Premium", "SettlementDate", "StrikeCurrency", "PremiumCurrency", "HasPresetTerms"} {
		if _, ok := typ.FieldByName(f); ok {
			t.Fatalf("OTCOffer must not have field %q after the inventory refactor", f)
		}
	}
}
```

Add `"reflect"` and `"testing"` imports if missing.

- [ ] **Step 2: Run it — expect FAIL**

Run: `cd stock-service && go test ./internal/model/ -run TestOTCOffer_HasNoPresetTermFields -count=1`
Expected: FAIL (fields still present).

- [ ] **Step 3: Remove the six fields**

In `stock-service/internal/model/otc_offer.go` delete these struct fields:
```go
StrikePrice           decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"strike_price"`
Premium               decimal.Decimal `gorm:"type:numeric(20,8);not null" json:"premium"`
SettlementDate        time.Time       `gorm:"type:date;not null" json:"settlement_date"`
```
and:
```go
StrikeCurrency  *string `gorm:"size:8" json:"strike_currency,omitempty"`
PremiumCurrency *string `gorm:"size:8" json:"premium_currency,omitempty"`
```
and:
```go
HasPresetTerms bool `gorm:"not null;default:true" json:"has_preset_terms"`
```
Keep the surrounding comment block trimmed (delete the `HasPresetTerms` doc paragraph at lines 126-128). Keep `RemoteStockShellPrefix` (still used to namespace shell native ids). If `decimal`/`time` imports become unused after this and other edits in the file, leave them — they are still used by `Quantity`/`CreatedAt`. Verify with `goimports`.

- [ ] **Step 4: It will not compile yet** — every `model.OTCOffer` reader of these fields breaks. That's expected; fix them in A2 before running tests. Do NOT run the full build yet.

- [ ] **Step 5: Commit after A2** (A1+A2 land together — the tree doesn't compile between them).

### Task A2: Fix the `model.OTCOffer` field readers/writers (NOT wire/contract/DTO)

**Files (the ONLY real `model.OTCOffer` term touchpoints — verify each receiver type per the gotcha):**
- Modify: `stock-service/internal/service/otc_offer_service.go:337-339` (Create write), `:353-354` (revision seed — see note)
- Modify: `stock-service/internal/otccache/option_cache.go:~330` (local model→OptionOffer mapping)
- Modify: `stock-service/internal/repository/otc_offer_repository.go:166-175` (`UpsertRemoteShell` raw `has_preset_terms` Exec)
- Modify: `stock-service/internal/handler/otc_handler.go:340` (`HasPresetTerms: o.HasPresetTerms` — `o` is the cache DTO; the DTO keeps the field name but its source is removed → set from chain in Phase D; for now drop the line) — **verify `o` is `otccache.OptionOffer`** (it is) and that `OptionOffer.HasPresetTerms` removal in Phase C/D is consistent.

- [ ] **Step 1: Create write** — in `otc_offer_service.go` `Create`, delete `StrikePrice: in.StrikePrice, Premium: in.Premium, SettlementDate: in.SettlementDate,` from the `&model.OTCOffer{...}` literal. (The `CreateOfferInput` fields are removed in Phase B; for Phase A keep the input fields but stop writing them to the model so the file compiles — they become unused locals, acceptable temporarily, OR comment the validation; cleanest is to do A2 minimally and finish the input cleanup in B1. To keep A green, also delete the now-dangling validation lines `!in.StrikePrice.IsPositive()` / `in.Premium.IsNegative()` / `in.SettlementDate.After(...)` only if they cause unused-var errors; otherwise leave for B1.)

- [ ] **Step 2: Local cache mapping** — in `option_cache.go` local fetch (`row := OptionOffer{...}` near line 330), delete the `StrikePrice: ...`, `Premium: ...`, `SettlementDate: ...`, `StrikeCurrency: ...`, `PremiumCurrency: ...`, `HasPresetTerms: ...` assignments that read from the `model.OTCOffer` row `o`. Leave the `OptionOffer` struct's fields in place (they're filled in Phase D); they default to empty/false here.

- [ ] **Step 3: `UpsertRemoteShell`** — in `otc_offer_repository.go:166-175` delete the `wantPresetTerms`/raw `UPDATE otc_offers SET has_preset_terms = ?` block entirely; the column no longer exists. The shell upsert keeps everything else (native_id, quantity=0-strike etc.).

- [ ] **Step 4: Build** — `cd stock-service && go build ./... 2>&1 | head`. Fix any remaining `model.OTCOffer.<field>` compile errors by the gotcha rule (only model receivers). Expected: clean build.

- [ ] **Step 5: Run model + repo + service unit tests**

Run: `cd stock-service && CGO_ENABLED=1 go test ./internal/model/ ./internal/repository/ ./internal/service/ -count=1`
Expected: PASS (TestOTCOffer_HasNoPresetTermFields now passes; pre-existing tests that set/read the removed fields must be updated — delete their term assignments/asserts).

- [ ] **Step 6: Commit**
```bash
git add stock-service/internal/model stock-service/internal/service stock-service/internal/otccache stock-service/internal/repository stock-service/internal/handler/otc_handler.go
git commit -m "refactor(otc): drop preset-term columns from OTCOffer (option offer = optionable inventory)"
```

### Task A3: Duplicate-merge migration + partial unique index

**Files:**
- Modify: `stock-service/internal/repository/otc_offer_repository.go` (add `MergeDuplicateOpenOffers`)
- Modify: `stock-service/cmd/main.go:~229` (call migration, then create index — order matters)
- Test: `stock-service/internal/repository/otc_offer_repository_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMergeDuplicateOpenOffers_SumsAndConsumes(t *testing.T) {
	db := newTestDB(t) // sqlite + AutoMigrate(&model.OTCOffer{}) — reuse repo test harness
	repo := NewOTCOfferRepository(db)
	uid := uint64(7)
	mk := func(qty int64) *model.OTCOffer {
		return &model.OTCOffer{
			InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
			Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
			Quantity: decimal.NewFromInt(qty), Status: model.OTCOfferStatusOpen, Local: true,
			LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7,
		}
	}
	require.NoError(t, db.Create(mk(5)).Error)
	require.NoError(t, db.Create(mk(70)).Error)

	n, err := repo.MergeDuplicateOpenOffers()
	require.NoError(t, err)
	require.Equal(t, int64(1), n) // one row consumed

	var open []model.OTCOffer
	require.NoError(t, db.Where("status = ?", model.OTCOfferStatusOpen).Find(&open).Error)
	require.Len(t, open, 1)
	require.True(t, open[0].Quantity.Equal(decimal.NewFromInt(75)), "merged qty = 5+70")
}
```

- [ ] **Step 2: Run — expect FAIL** (`MergeDuplicateOpenOffers` undefined)

Run: `cd stock-service && CGO_ENABLED=1 go test ./internal/repository/ -run TestMergeDuplicateOpenOffers -count=1`

- [ ] **Step 3: Implement `MergeDuplicateOpenOffers`** in `otc_offer_repository.go`:

```go
// MergeDuplicateOpenOffers collapses pre-existing duplicate OPEN LOCAL offers
// sharing (initiator_owner_id, ticker, direction) into the oldest row (summing
// quantity) and marks the rest consumed, so the partial unique index can be
// created. Idempotent: a second run is a no-op. Returns the number consumed.
func (r *OTCOfferRepository) MergeDuplicateOpenOffers() (int64, error) {
	var consumed int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.OTCOffer
		if err := tx.Where("status = ? AND local = ?", model.OTCOfferStatusOpen, true).
			Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		type key struct {
			owner uint64
			tick  string
			dir   string
		}
		keep := map[key]*model.OTCOffer{}
		for i := range rows {
			o := &rows[i]
			if o.InitiatorOwnerID == nil {
				continue
			}
			k := key{*o.InitiatorOwnerID, o.Ticker, o.Direction}
			if first, ok := keep[k]; ok {
				first.Quantity = first.Quantity.Add(o.Quantity)
				if err := tx.Model(&model.OTCOffer{}).Where("id = ?", first.ID).
					Update("quantity", first.Quantity).Error; err != nil {
					return err
				}
				if err := tx.Model(&model.OTCOffer{}).Where("id = ?", o.ID).
					Update("status", model.OTCOfferStatusConsumed).Error; err != nil {
					return err
				}
				consumed++
			} else {
				cp := *o
				keep[k] = &cp
			}
		}
		return nil
	})
	return consumed, err
}
```

- [ ] **Step 4: Run — expect PASS.**

- [ ] **Step 5: Wire migration + index in `cmd/main.go`** (after AutoMigrate, near the other `db.Exec(... UNIQUE INDEX ...)` at line ~229). Migration MUST run before index creation:

```go
if n, err := otcOfferRepo.MergeDuplicateOpenOffers(); err != nil {
	log.Printf("WARN: OTC offer duplicate merge failed: %v", err)
} else if n > 0 {
	log.Printf("OTC offer migration: merged %d duplicate open offers", n)
}
db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_otc_offer_open_owner_ticker_dir
	ON otc_offers (initiator_owner_id, ticker, direction)
	WHERE status = 'open' AND local = true AND initiator_owner_id IS NOT NULL`)
```

(`otcOfferRepo` is the existing repo instance in `main.go`; reuse it. The partial index follows the watchlist/holding `CREATE UNIQUE INDEX IF NOT EXISTS` precedent.)

- [ ] **Step 6: Build + commit**
```bash
cd stock-service && go build ./... && cd ..
git add stock-service/internal/repository/otc_offer_repository.go stock-service/internal/repository/otc_offer_repository_test.go stock-service/cmd/main.go
git commit -m "feat(otc): merge-duplicate migration + partial unique index (one open offer per owner+ticker+direction)"
```

---

# PHASE B — Create / edit API

### Task B1: `Create` drops terms + rejects duplicate (`AlreadyExists`)

**Files:**
- Modify: `stock-service/internal/service/otc_offer_service.go:253-269` (`CreateOfferInput` — drop term fields), `:276-345` (`Create`)
- Modify: `stock-service/internal/repository/otc_offer_repository.go` (add `CountOpenByOwnerTickerDirection`)
- Test: `stock-service/internal/service/otc_offer_service_crud_test.go`

- [ ] **Step 1: Failing test**

```go
func TestCreate_RejectsDuplicateOpenOfferSameTickerDirection(t *testing.T) {
	svc, _ := newOTCOfferServiceWithDB(t) // existing harness
	in := CreateOfferInput{
		ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(5), InitiatorAccountID: 1,
	}
	_, err := svc.Create(context.Background(), in)
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), in) // duplicate
	require.Error(t, err)
	require.ErrorIs(t, err, ErrOTCOfferDuplicateOpen)
}
```

- [ ] **Step 2: Run — expect FAIL** (`ErrOTCOfferDuplicateOpen` undefined / no rejection).

- [ ] **Step 3: Implement**
- In `errors.go` add: `var ErrOTCOfferDuplicateOpen = errors.New("an open option offer already exists for this ticker and direction")`.
- In `CreateOfferInput` delete `StrikePrice`, `Premium`, `SettlementDate` fields.
- In `Create`: delete the `!in.StrikePrice.IsPositive()`, `in.Premium.IsNegative()`, `in.SettlementDate.After(...)` validations and the `StrikePrice/Premium/SettlementDate` literal assignments (if A2 left them). Keep the `assertSellerHasShares` sell-side check. Before building the model, add:

```go
existing, err := s.offerRepo.CountOpenByOwnerTickerDirection(initOwnerType, initOwnerID, in.Ticker, in.Direction)
if err != nil {
	return nil, fmt.Errorf("duplicate check: %w", err)
}
if existing > 0 {
	return nil, ErrOTCOfferDuplicateOpen
}
```
- Add to `otc_offer_repository.go`:
```go
// CountOpenByOwnerTickerDirection counts this bank's OPEN LOCAL offers for the
// (owner, ticker, direction) triple — the partial-unique-index key. Used to
// reject a duplicate before insert (friendlier than relying on the DB error).
func (r *OTCOfferRepository) CountOpenByOwnerTickerDirection(ownerType model.OwnerType, ownerID *uint64, ticker, direction string) (int64, error) {
	var n int64
	q := r.db.Model(&model.OTCOffer{}).
		Where("status = ? AND local = ? AND ticker = ? AND direction = ? AND initiator_owner_type = ?",
			model.OTCOfferStatusOpen, true, ticker, direction, ownerType)
	if ownerID != nil {
		q = q.Where("initiator_owner_id = ?", *ownerID)
	} else {
		q = q.Where("initiator_owner_id IS NULL")
	}
	return n, q.Count(&n).Error
}
```

- [ ] **Step 4: Run — expect PASS.** Update any existing create tests that passed `StrikePrice`/`Premium`/`SettlementDate` to drop those fields.

- [ ] **Step 5: Commit**
```bash
git add stock-service/internal/service stock-service/internal/repository
git commit -m "feat(otc): reject duplicate open option offer per (owner,ticker,direction)"
```

### Task B2: gateway create handler — drop term fields, map 409

**Files:**
- Modify: `api-gateway/internal/handler/otc_negotiation_handler.go` (the `CreateOffer`/create-option handler that builds `stockpb.Create...` — locate the handler bound to `POST /api/v3/me/otc/options`) and the proto create request mapping.
- Modify: `api-gateway/internal/handler/validation.go` is unchanged; reuse `handleGRPCError` (maps `AlreadyExists`→409 `conflict`).
- Test: `api-gateway/internal/handler/otc_negotiation_handler_test.go`

- [ ] **Step 1: Failing test** — POST create with no term fields succeeds (200/201); a second identical POST returns 409 `conflict`. Use the existing gateway handler test harness with a mock stock client whose `CreateOffer` returns `status.Error(codes.AlreadyExists, ...)` on the second call; assert HTTP 409 and body `error.code == "conflict"`.

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Implement** — in the create handler: remove `strike_price`/`premium`/`settlement_date` from the request bind struct and from the `stockpb.Create...Request` it forwards; keep `ticker`/`stock_id`, `quantity`, `account_id`, `direction` (+ existing ownership via `ResolveAndCheckAccount`). The service maps `ErrOTCOfferDuplicateOpen`→`codes.AlreadyExists` (verify the stock-service gRPC handler translates the service error; if not, add the mapping there) so `handleGRPCError` yields 409. Ensure the stock-service handler maps `errors.Is(err, service.ErrOTCOfferDuplicateOpen)` → `status.Error(codes.AlreadyExists, err.Error())`.

- [ ] **Step 4: Run — expect PASS.**

- [ ] **Step 5: Commit**
```bash
git add api-gateway/internal/handler/otc_negotiation_handler.go api-gateway/internal/handler/otc_negotiation_handler_test.go stock-service/internal/handler
git commit -m "feat(otc): create option offer without preset terms; 409 on duplicate"
```

### Task B3: `UpdateQuantity` service + `PUT /api/v3/me/otc/options/:id`

**Files:**
- Modify: `stock-service/internal/service/otc_offer_service.go` (add `UpdateQuantity`)
- Modify: `stock-service/internal/repository/otc_offer_repository.go` (add `OutstandingCommittedQuantity` if not present — sum of quantities of non-terminal chains/contracts on the offer)
- Modify: proto `stock.proto` (add `UpdateOTCOfferQuantity` RPC + request/response) → `make proto`
- Modify: `stock-service/internal/handler/otc_options_handler.go` (gRPC handler) + `api-gateway/internal/handler/otc_negotiation_handler.go` (`UpdateMyOption`) + `router_v3.go`
- Test: service test + gateway handler test

- [ ] **Step 1: Failing service test**

```go
func TestUpdateQuantity_SetsTotal_RejectsBelowCommitted_AndAboveHolding(t *testing.T) {
	svc, h := newOTCOfferServiceWithDB(t) // h: seed a holding of 100 OPK for owner 7
	off, _ := svc.Create(ctx, CreateOfferInput{ActorUserID: 7, ActorSystemType: "client",
		Direction: model.OTCDirectionSellInitiated, StockID: 1, Ticker: "OPK",
		Quantity: decimal.NewFromInt(10), InitiatorAccountID: 1})

	// up to 80 (<=100 holding, >=0 committed) OK
	got, err := svc.UpdateQuantity(ctx, off.ID, ownerClient(7), decimal.NewFromInt(80))
	require.NoError(t, err)
	require.True(t, got.Quantity.Equal(decimal.NewFromInt(80)))

	// above holding rejected
	_, err = svc.UpdateQuantity(ctx, off.ID, ownerClient(7), decimal.NewFromInt(200))
	require.ErrorIs(t, err, ErrOTCInsufficientShares)

	// non-positive rejected
	_, err = svc.UpdateQuantity(ctx, off.ID, ownerClient(7), decimal.Zero)
	require.ErrorIs(t, err, ErrOTCOfferFieldInvalid)
}
```

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Implement `UpdateQuantity`**

```go
// UpdateQuantity sets the offer's TOTAL quantity (edit up or down). Rejects
// non-positive, below the shares already committed to in-flight chains/contracts,
// or above the owner's holding for the ticker. Optimistic-lock safe.
func (s *OTCOfferService) UpdateQuantity(ctx context.Context, offerID uint64, owner Owner, qty decimal.Decimal) (*model.OTCOffer, error) {
	if !qty.IsPositive() {
		return nil, fmt.Errorf("quantity must be > 0: %w", ErrOTCOfferFieldInvalid)
	}
	var out *model.OTCOffer
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var o model.OTCOffer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, offerID).Error; err != nil {
			return mapNotFound(err)
		}
		if !o.Local || !o.IsOpenListing() {
			return fmt.Errorf("offer not editable: %w", ErrOTCOfferFieldInvalid)
		}
		if o.InitiatorOwnerType != owner.Type || !ownerIDEq(o.InitiatorOwnerID, owner.ID) {
			return ErrOTCNotOwner
		}
		committed, err := s.offerRepo.OutstandingCommittedQuantityTx(tx, o.ID)
		if err != nil {
			return err
		}
		if qty.LessThan(committed) {
			return fmt.Errorf("quantity %s below committed %s: %w", qty, committed, ErrOTCOfferFieldInvalid)
		}
		if o.Direction == model.OTCDirectionSellInitiated {
			if err := s.assertSellerHasSharesTx(tx, o.InitiatorOwnerType, o.InitiatorOwnerID, o.StockID, qty); err != nil {
				return err
			}
		}
		o.Quantity = qty
		res := tx.Save(&o)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return shared.ErrOptimisticLock
		}
		out = &o
		return nil
	})
	return out, err
}
```

Add `OutstandingCommittedQuantityTx(tx, offerID)` to the repo: sum `OTCNegotiation.Quantity` for chains on this offer in a non-terminal/accepted state + any formed contract quantity. If a precise "committed" notion doesn't exist yet, define committed = sum of `quantity` over `OTCNegotiation` rows for `parent_offer_id = offerID` with status in (`accepted`) — adjust to the codebase's accepted-chain marker; document the exact predicate in the function comment. Add `ErrOTCNotOwner` to `errors.go` if absent. `assertSellerHasSharesTx` is the tx variant of the existing `assertSellerHasShares` (factor it).

- [ ] **Step 4: Run — expect PASS.**

- [ ] **Step 5: proto + handler + route**
- `stock.proto`: add `rpc UpdateOTCOfferQuantity(UpdateOTCOfferQuantityRequest) returns (OTCOfferResponse);` with request `{ uint64 offer_id; string quantity; string acting_owner_type; uint64 acting_owner_id; }`. `make proto`.
- stock-service gRPC handler `UpdateOTCOfferQuantity`: parse decimal, resolve owner, call `svc.UpdateQuantity`, map `ErrOTCNotOwner`→`PermissionDenied`, `ErrOTCOfferFieldInvalid`→`InvalidArgument`, `ErrOTCInsufficientShares`→`FailedPrecondition`, not-found→`NotFound`.
- gateway `UpdateMyOption(c *gin.Context)`: bind `{ "quantity": "..." }`, `positiveDecimalString("quantity", ...)`, resolve identity, **ownership**: load the offer (via a Get) and verify `me_owner`/owner before forwarding, forward to `UpdateOTCOfferQuantity`, `handleGRPCError`. Swagger annotations required.
- `router_v3.go` in the `/me` group: `me.PUT("/otc/options/:id", bankIfEmp, h.OTCOptions.UpdateMyOption)`.

- [ ] **Step 6: gateway handler test** — PUT sets quantity (200); non-positive → 400; non-owner → 403. Run, expect PASS.

- [ ] **Step 7: Commit**
```bash
make proto
git add contract/proto contract/stockpb stock-service api-gateway
git commit -m "feat(otc): PUT /me/otc/options/:id to set an offer's total quantity"
```

---

# PHASE C — Cross-bank serving swap + `/public-option-offers` removal

### Task C1: `GetPublicStocks` serves OTCOffers (not holdings)

**Files:**
- Modify: `stock-service/internal/handler/peer_otc_grpc_handler.go:536-583` (`GetPublicStocks`)
- Modify: `stock-service/internal/repository/otc_offer_repository.go` (add `ListPublicOptionOffersForPeer`)
- Test: `stock-service/internal/handler/peer_otc_grpc_handler_extra_test.go`

- [ ] **Step 1: Failing test** — `GetPublicStocks` returns one `PeerPublicStock{OwnerId, Ticker, Amount}` per open, sell_initiated, public, non-private, local OTCOffer; excludes remote/non-open/buy_initiated/private rows; seller id is `composePeerSellerID` form (`client-<n>`/`bank`/`employee-<n>`).

```go
func TestGetPublicStocks_ServesOptionOffers(t *testing.T) {
	h, repo := newPeerOTCHandlerWithDB(t) // seeds via repo
	uid := uint64(7)
	repo.mustCreate(&model.OTCOffer{InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, Ticker: "OPK", Quantity: decimal.NewFromInt(75),
		Status: model.OTCOfferStatusOpen, Local: true, Public: true,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7})
	resp, err := h.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Stocks, 1)
	require.Equal(t, "OPK", resp.Stocks[0].Ticker)
	require.Equal(t, int64(75), resp.Stocks[0].Amount)
	require.Equal(t, "client-7", resp.Stocks[0].OwnerId.Id)
}
```

- [ ] **Step 2: Run — expect FAIL** (still reads holdings).

- [ ] **Step 3: Implement**
- Add `ListPublicOptionOffersForPeer()` to `otc_offer_repository.go`:
```go
// ListPublicOptionOffersForPeer returns this bank's OPEN, sell-initiated, public,
// non-private, LOCAL option offers — the inventory exposed to peers on /public-stock.
func (r *OTCOfferRepository) ListPublicOptionOffersForPeer() ([]model.OTCOffer, error) {
	var rows []model.OTCOffer
	err := r.db.Where(`status = ? AND local = ? AND direction = ? AND public = ? AND private = ?`,
		model.OTCOfferStatusOpen, true, model.OTCDirectionSellInitiated, true, false).Find(&rows).Error
	return rows, err
}
```
- Rewrite `GetPublicStocks` body to iterate these offers instead of `h.holdings.ListPublic()`, using `composePeerSellerID(&o)` for the seller id (skip rows where it returns `""`), `o.Ticker`, `o.Quantity.IntPart()`. Drop the `PricePerStock`/`Currency` population that came from the holding (the wire `/public-stock` JSON only carries seller+amount anyway — see the gateway grouping). Keep the `OwnerId: &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: sellerID}` shape. Wire a `publicOptionOffers` reader into the handler (replace/augment the `h.holdings` dependency; keep `h.holdings` if still used elsewhere — `assertSellerHasShares` uses `GetByOwnerAndTicker`, not `ListPublic`, so `ListPublic` may become unused → remove it in C-cleanup).

- [ ] **Step 4: Run — expect PASS.** Update the old holding-based `GetPublicStocks` test to the new behavior or delete it.

- [ ] **Step 5: Commit**
```bash
git add stock-service/internal/handler/peer_otc_grpc_handler.go stock-service/internal/repository/otc_offer_repository.go stock-service/internal/handler/peer_otc_grpc_handler_extra_test.go stock-service/cmd/main.go
git commit -m "feat(otc): peer /public-stock now serves our option offers (one per owner+ticker)"
```

### Task C2: Remove `GetPublicOptionOffers` (endpoint + proto + forwarder + gateway)

**Files (delete, per the Explore map):**
- Modify: `contract/proto/stock/stock.proto` — delete `rpc GetPublicOptionOffers` (line ~1972) + messages `GetPublicOptionOffersRequest`/`PeerPublicOptionOffer`/`GetPublicOptionOffersResponse` (~2060-2090). `make proto`.
- Delete: `stock-service/internal/handler/peer_otc_grpc_handler.go:374-478` (`GetPublicOptionOffers` + its body) and the now-unused `composePeerSellerID`? (NO — C1 uses it; keep.) Remove the `otcOffers OTCOfferReader` wiring **only if** unused after this (it backed `GetPublicOptionOffers`; C1 uses a new reader — verify before deleting).
- Delete: `interbank-service/internal/handler/peer_otc_forwarder.go:36` (`GetPublicOptionOffers` passthrough).
- Delete: `api-gateway/internal/handler/peer_otc_handler.go:86-130` (`GetPublicOptionOffers` handler).
- Modify: `api-gateway/internal/router/router_v3.go:302` — delete `crossBank.GET("/public-option-offers", ...)`.
- Delete tests: `peer_otc_seller_id_test.go:TestGetPublicOptionOffers_SellerIDComposition` (repoint its seller-id assertions to `GetPublicStocks` if still valuable), `peer_otc_buyinit_publish_test.go:TestGetPublicOptionOffers_BuyInitiated_NotPublished` (re-assert buy_initiated exclusion against `GetPublicStocks` instead).

- [ ] **Step 1: Failing test** — gateway route returns 404:

```go
func TestPublicOptionOffers_RouteRemoved(t *testing.T) {
	r := setupTestRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v3/cross-bank-protocol/public-option-offers", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}
```

- [ ] **Step 2: Run — expect FAIL** (route still present / 401).

- [ ] **Step 3: Delete** all the symbols above. `make proto`. Re-point the two buy-init/seller-id tests at `GetPublicStocks`.

- [ ] **Step 4: Build all affected modules**

Run: `cd contract && go build ./... && cd ../stock-service && go build ./... && cd ../interbank-service && go build ./... && cd ../api-gateway && go build ./...`
Expected: clean.

- [ ] **Step 5: Run — expect PASS** (404 test + re-pointed tests).

- [ ] **Step 6: Commit**
```bash
make proto
git add contract api-gateway interbank-service stock-service
git commit -m "refactor(otc): remove /public-option-offers endpoint (cross-bank discovery is /public-stock only)"
```

### Task C3: Remove outbound `/public-option-offers` ingestion

**Files:**
- Modify: `stock-service/internal/otccache/option_cache.go` — delete `fetchPeer` (line ~367) and `buildAndMirrorRemoteOffers` (line ~415); in the refresh goroutine (lines ~266-281) drop the `fetchPeer` branch, keep `fetchPeerStocks`; keep the `reached` liveness from the earlier (b) fix (peer counts reached if `fetchPeerStocks` succeeds).
- Modify: `stock-service/internal/otccache/option_cache_test.go` — delete the `/public-option-offers` fake path stub; keep the shells-from-/public-stock test; the "peers reached via /public-stock alone" test now becomes the only path.
- Modify: `stock-service/internal/otccache/refresher_test.go:243` — `fetchPeer` test removed.

- [ ] **Step 1: Update tests first (red)** — remove `fetchPeer`-based tests; assert the goroutine ingests shells and counts the peer reached via `fetchPeerStocks` only. Run — expect FAIL (symbols still exist / goroutine still calls fetchPeer).

- [ ] **Step 2: Delete `fetchPeer` + `buildAndMirrorRemoteOffers`**; simplify the goroutine to:
```go
reached := false
if shells, serr := r.fetchPeerStocks(cycleCtx, peer); serr != nil {
	log.Printf("otccache(stock-shells): peer %s fetch failed: %v", peer.GetBankCode(), serr)
} else {
	reached = true
	mu.Lock(); offers = append(offers, shells...); mu.Unlock()
}
if reached { mu.Lock(); peersReached++; mu.Unlock() }
```
Remove now-unused imports (`sitx.PublicOptionOffer` usage, etc.) flagged by the compiler.

- [ ] **Step 3: Build + run otccache tests — expect PASS.**

- [ ] **Step 4: Commit**
```bash
git add stock-service/internal/otccache
git commit -m "refactor(otc): drop outbound /public-option-offers ingestion; /public-stock shells are the sole peer option source"
```

---

# PHASE D — Read-surface term projection (§6.5)

### Task D1: `LatestRevisionByAuthorForOffer` repository query

**Files:**
- Modify: `stock-service/internal/repository/otc_negotiation_repository.go`
- Test: `stock-service/internal/repository/otc_negotiation_repository_test.go`

- [ ] **Step 1: Failing test** — given two chains on offer 1 with revisions authored by owner principal `client:7` and bidder `client:9`, the function returns the **owner's** most recent revision terms (newest by created_at), and nil when the owner never authored one.

```go
func TestLatestRevisionByAuthorForOffer(t *testing.T) {
	db := newTestDB(t)
	repo := NewOTCNegotiationRepository(db)
	// seed: offer 1, chain A (bidder client-9) rev COUNTER by client-7 (owner) strike=100,
	//       chain B (bidder client-5) rev COUNTER by client-7 (owner) strike=120 (newer)
	// ... insert OTCNegotiation + OTCNegotiationRevision rows with ModifiedByPrincipalType/ID ...
	rev, err := repo.LatestRevisionByAuthorForOffer(1, "client", 7)
	require.NoError(t, err)
	require.NotNil(t, rev)
	require.True(t, rev.StrikePrice.Equal(decimal.NewFromInt(120)))

	none, err := repo.LatestRevisionByAuthorForOffer(1, "client", 999)
	require.NoError(t, err)
	require.Nil(t, none)
}
```

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Implement**

```go
// LatestRevisionByAuthorForOffer returns the most recent OTCNegotiationRevision
// AUTHORED BY the given principal across all chains under a local parent offer —
// i.e. "the owner's latest counter" for the §6.5 owner-row term projection.
// Returns (nil, nil) when that principal never authored a revision on the offer.
func (r *OTCNegotiationRepository) LatestRevisionByAuthorForOffer(offerID uint64, principalType string, principalID uint64) (*model.OTCNegotiationRevision, error) {
	var rev model.OTCNegotiationRevision
	err := r.db.
		Joins("JOIN otc_negotiations n ON n.id = otc_negotiation_revisions.negotiation_id").
		Where("n.parent_offer_id = ? AND otc_negotiation_revisions.modified_by_principal_type = ? AND otc_negotiation_revisions.modified_by_principal_id = ?",
			offerID, principalType, principalID).
		Order("otc_negotiation_revisions.created_at DESC, otc_negotiation_revisions.id DESC").
		First(&rev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}
```

Verify the revision model's author columns are `modified_by_principal_type`/`_id` (the map showed `ModifiedByPrincipalType`); adjust column names to match the actual GORM tags, and confirm `OTCNegotiation` has `parent_offer_id`.

- [ ] **Step 4: Run — expect PASS. Commit.**
```bash
git add stock-service/internal/repository/otc_negotiation_repository.go stock-service/internal/repository/otc_negotiation_repository_test.go
git commit -m "feat(otc): query latest revision authored by a principal for an offer (owner-latest-counter)"
```

### Task D2: Request-scoped term projection onto the offer DTO

**Files:**
- Modify: `stock-service/internal/handler/otc_handler.go:150-345` (`ListUnifiedOffers` — where `me_owner`/`my_negotiation` are stamped) and `otc_options_handler.go` `GetOffer`
- Modify: `stock-service/internal/otccache/option_cache.go` — drop `HasPresetTerms` from the `OptionOffer` struct (and the proto field `OTCOfferItem.HasPresetTerms` in `stock.proto` → `make proto`, plus the gateway DTO key)
- Test: `stock-service/internal/handler/otc_handler_test.go`

- [ ] **Step 1: Failing test** — for a cached `OptionOffer` (no terms) with:
  - a viewer who is a **bidder** with `my_negotiation` having strike=100 → the response item's `strike_price == "100"`;
  - a viewer who is the **owner**, whose latest counter strike=120 → `strike_price == "120"`;
  - a viewer who is the owner but never countered → `strike_price == ""`;
  - a non-participant → `""`.
Use the handler test harness with a fake `MyNegotiationLister` and a fake `LatestRevisionByAuthorForOffer`.

- [ ] **Step 2: Run — expect FAIL.**

- [ ] **Step 3: Implement** — in `ListUnifiedOffers` (and `GetOffer`), after `me_owner`/`my_negotiation` are resolved per item, set the term fields:
  - If `item.MeOwner == false` and the viewer has a `my_negotiation` for this offer → set `StrikePrice/Premium/SettlementDate` (+ currencies) from that chain's current `OTCNegotiation` terms (already available via the `myNegIdx`; extend the index value to carry the terms if it doesn't already).
  - If `item.MeOwner == true` → call `LatestRevisionByAuthorForOffer(localOfferID, ownerPrincipalType, ownerPrincipalID)`; if non-nil set the fields from it, else leave empty.
  - Else leave empty.
  Do this only for **local** offers (remote shells have no local chain authorship here; a bidder on a remote shell still gets their `my_negotiation` terms via the first branch). Keep `best_bid`/`best_ask`/`active_chains_count` untouched.
  Add a narrow dependency (e.g. `OwnerLatestCounterFn func(offerID uint64, pType string, pID uint64) (*Terms, error)`) wired in `cmd/main.go` over the new repo method, mirroring the existing `WithMyNegotiations` wiring — keep the handler decoupled from the repository type.

- [ ] **Step 4: Remove `HasPresetTerms`** from `otccache.OptionOffer`, the `OTCOfferItem` proto field, the gateway DTO mapping (`peer_otc_handler.go` had it for the removed endpoint; the unified item mapping in `otc_handler.go:340`), and `make proto`. Update any reader.

- [ ] **Step 5: Run — expect PASS.** Build all modules.

- [ ] **Step 6: Commit**
```bash
make proto
git add contract stock-service api-gateway
git commit -m "feat(otc): project chain terms onto offer DTO per viewer (bidder position / owner latest counter); drop has_preset_terms"
```

---

# PHASE E — Docs, integration, version, CI

### Task E1: Integration tests (two-stack)

**Files:**
- Modify/Create: `test-app/workflows/wf_otc_options_as_stock_test.go`

- [ ] **Step 1: Write integration tests** (build tag `integration`, use shared helpers):
  - Create an option offer (no terms) → assert it appears on a peer's `/public-stock` ingestion and is biddable cross-bank end-to-end (open → counter → accept → contract). Reuse the existing two-stack cross-bank OTC helper flow.
  - Second create same ticker+direction → HTTP 409 `conflict`.
  - `PUT /api/v3/me/otc/options/:id` quantity → reflected in a subsequent `/public-stock` poll.
  - `GET /api/v3/cross-bank-protocol/public-option-offers` → 404.
  - Read projection: as the bidder, `GET /api/v3/otc/options` shows the bidder's chain strike on the listing row; as owner after a counter, shows the owner's latest counter.

- [ ] **Step 2: Run live** against the running two-stack (rebuild `stock-service` + `api-gateway`, reconnect `sitx_shared`):
```bash
docker compose up -d --build stock-service api-gateway
docker network connect --alias exbanka-stock sitx_shared exbanka-1-backend-stock-service-1 || true
docker network connect --alias exbanka-interbank sitx_shared exbanka-1-backend-interbank-service-1 || true
cd test-app && go test ./workflows/ -tags integration -run TestWF_OTCOptionsAsStock -count=1 -v
```
Expected: PASS.

- [ ] **Step 3: Commit.**

### Task E2: Docs + version + Swagger

- [ ] Update `docs/api/REST_API_v3.md`: remove `/public-option-offers`; update `POST /api/v3/me/otc/options` (no term fields, 409); add `PUT /api/v3/me/otc/options/:id`; note `/cross-bank-protocol/public-stock` now serves option offers; note the read-DTO term fields are viewer-contextual (§6.5).
- [ ] Update `docs/protocol/bank-4-interop-otc-results.md` with the unified model (options-as-stocks; one offer per owner+ticker; no per-offer key in `/public-stock`).
- [ ] `make swagger` (regenerate gateway docs); commit generated `api-gateway/docs/`.
- [ ] Bump `VERSION` → **3.0.0** (breaking) and sync `api-gateway/internal/version/version.go`.
- [ ] Commit: `docs+chore(otc): unified options-as-stock model; REST/protocol docs; VERSION 2.x->3.0.0`.

### Task E3: Full CI

- [ ] Run `make ci` (build, unit tests, lint, gofmt, go mod tidy) across all modules — must be green. Fix anything surfaced (esp. repo-wide `gofmt -l .` and `go mod tidy` diffs in `contract`/`stock-service`/`api-gateway`/`interbank-service`/`test-app`).
- [ ] Final commit if CI fixes were needed.

---

## Self-Review (against the spec)

**Spec coverage:** R1 (remove `/public-option-offers`) → C2+C3. R2 (`/public-stock` serves offers) → C1. R3 (uniqueness) → A3+B1. R4 (drop preset terms) → A1+A2. R5 (PUT total quantity) → B3. R6 (`/otc/stocks` untouched) → not modified (verify no task touches `otc_stock_service.go`/`/otc/stocks`). §6.5 read projection → D1+D2. Migration → A3. Tests/docs/version → E1-E3.

**Placeholder scan:** No "TBD"/"handle edge cases"; each step lists exact files, code, and commands. Where per-site judgment is required (model-vs-wire term reads) the rule and the exact candidate list are given in the Critical Gotcha.

**Type consistency:** `ErrOTCOfferDuplicateOpen`, `UpdateQuantity`, `CountOpenByOwnerTickerDirection`, `OutstandingCommittedQuantityTx`, `ListPublicOptionOffersForPeer`, `MergeDuplicateOpenOffers`, `LatestRevisionByAuthorForOffer` are each defined once and reused with matching signatures. `composePeerSellerID` is kept (used by C1); `sellerIDForOwner`/`ListPublic` become removable (noted in C1). `OptionOffer.HasPresetTerms` removed in D2 consistent with A2 dropping its source.

**Known verification points for the implementer** (call out, don't guess): exact author-column names on `OTCNegotiationRevision` (D1), the precise "committed quantity" predicate (B3), and that `h.holdings.ListPublic` is unused after C1 before deleting it.
