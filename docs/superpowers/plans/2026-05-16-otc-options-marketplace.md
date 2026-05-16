# Plan: OTC Options Marketplace Refactor — Public Listings + Parallel Negotiation Chains

## 0. Architectural Pivot Summary

Today: `OTCOffer` IS the negotiation thread; one buyer/seller pair, one revision chain. The route namespace `/api/v3/otc/offers/*` is shared with the legacy stock-listing marketplace handled by `Portfolio.ListOTCOffers` / `Portfolio.BuyOTCOffer`.

After: `OTCOffer` becomes a **public listing** (status: `OPEN`, `CONSUMED`, `CANCELLED`, `EXPIRED`). Each bidder spawns a separate `OTCNegotiation` row carrying its own counter chain (`OTCNegotiationRevision`). First chain to ACCEPT wins atomically; parent flips to `CONSUMED`; siblings auto-cancel. The legacy stock-listing routes get re-keyed to `/api/v3/otc/stocks/*` by a sibling agent — your scope is purely options.

CLAUDE.md says "clean break, no shims." That means: drop the existing `CounterpartyOwnerType/ID`, `LastModifiedByPrincipal*`, the offer's "current terms" duality, and the old `OTCOfferRevision` table. Migrate the model and rewrite the service + handler + gateway around the new shape.

---

## 1. Schema Changes

### 1.1 `OTCOffer` (rewritten as immutable listing) — `stock-service/internal/model/otc_offer.go`

Drop the moving-state fields. Keep only the listing's posted terms (which are the seed/upper-bound for negotiations).

```go
const (
    OTCOfferStatusOpen      = "open"      // listed; accepting negotiations
    OTCOfferStatusConsumed  = "consumed"  // one chain accepted; siblings cancelled
    OTCOfferStatusCancelled = "cancelled" // poster withdrew before any accept
    OTCOfferStatusExpired   = "expired"   // settlement_date passed with no accept
)

type OTCOffer struct {
    ID                 uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
    PosterOwnerType    OwnerType       `gorm:"size:8;not null;index:ix_otc_poster,priority:1;check:poster_owner_type IN ('client','bank')"`
    PosterOwnerID      *uint64         `gorm:"index:ix_otc_poster,priority:2"`
    PosterBankCode     *string         `gorm:"size:32"`
    Direction          string          `gorm:"size:20;not null;check:direction IN ('sell','buy')"` // SIMPLIFIED enum
    StockID            uint64          `gorm:"not null;index:ix_otc_listing,priority:1"`
    Ticker             string          `gorm:"size:16;not null"`
    Quantity           decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    StrikePrice        decimal.Decimal `gorm:"type:numeric(20,8);not null"` // poster's ask/bid
    Premium            decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    SettlementDate     time.Time       `gorm:"type:date;not null;index:ix_otc_settle"`
    PosterAccountID    uint64          `gorm:"not null"` // pre-bound; pays/receives on accept
    Status             string          `gorm:"size:16;not null;index:ix_otc_listing,priority:2"`
    // Reservation FKs — non-NULL exactly once depending on direction
    HoldingReservationID *uint64       `gorm:"index"` // sell-side: locks shares from poster's holding
    AccountReservationID *uint64       `gorm:"index"` // buy-side: locks premium in poster's account
    // Pre-existing cross-bank visibility flags
    Public             bool            `gorm:"not null;default:true"`
    PrivateToBankCode  *string         `gorm:"size:3"`
    CreatedAt          time.Time
    UpdatedAt          time.Time
    ConsumedAt         *time.Time
    WinningNegotiationID *uint64       // set when status=consumed
    Version            int64           `gorm:"not null;default:0" json:"-"`
}
func (o *OTCOffer) BeforeUpdate(tx *gorm.DB) error { tx.Statement.Where("version = ?", o.Version); o.Version++; return nil }
func (o *OTCOffer) IsOpen() bool { return o.Status == OTCOfferStatusOpen }
```

**RISK:** the existing `Direction` enum uses `sell_initiated`/`buy_initiated` strings encoding "who's the initiator." That semantics is gone — every offer has exactly one poster. Use `sell` / `buy` (= what the poster wants to do). Wire-level break.

### 1.2 New `OTCNegotiation` — `stock-service/internal/model/otc_negotiation.go`

```go
const (
    OTCNegotiationStatusPending    = "pending"     // bidder created it
    OTCNegotiationStatusCountered  = "countered"   // last revision was a counter
    OTCNegotiationStatusAccepted   = "accepted"    // contract formed, atomic winner
    OTCNegotiationStatusRejected   = "rejected"    // explicit reject by either party
    OTCNegotiationStatusCancelled  = "cancelled"   // sibling cascade-cancel after winner picked, OR bidder withdrew
    OTCNegotiationStatusExpired    = "expired"     // parent expired
)

type OTCNegotiation struct {
    ID                uint64          `gorm:"primaryKey;autoIncrement"`
    OfferID           uint64          `gorm:"not null;uniqueIndex:ux_neg_per_bidder,priority:1;index:ix_neg_offer_status,priority:1"`
    BidderOwnerType   OwnerType       `gorm:"size:8;not null;uniqueIndex:ux_neg_per_bidder,priority:2;index:ix_neg_bidder,priority:1"`
    BidderOwnerID     *uint64         `gorm:"uniqueIndex:ux_neg_per_bidder,priority:3;index:ix_neg_bidder,priority:2"`
    BidderBankCode    *string         `gorm:"size:32"`
    BidderAccountID   uint64          `gorm:"not null"` // bound at create
    // Current terms — start = parent terms, mutated by each counter
    Quantity          decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    StrikePrice       decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    Premium           decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    SettlementDate    time.Time       `gorm:"type:date;not null"`
    Status            string          `gorm:"size:16;not null;index:ix_neg_offer_status,priority:2;index:ix_neg_bidder,priority:3"`
    LastActorPrincipalType string     `gorm:"size:10;not null"` // who issued the most recent revision
    LastActorPrincipalID   uint64     `gorm:"not null"`
    ActingEmployeeID  *uint64
    ContractID        *uint64         `gorm:"uniqueIndex"` // set on accept
    CreatedAt         time.Time
    UpdatedAt         time.Time
    Version           int64           `gorm:"not null;default:0" json:"-"`
}
func (n *OTCNegotiation) BeforeUpdate(tx *gorm.DB) error { tx.Statement.Where("version = ?", n.Version); n.Version++; return nil }
func (n *OTCNegotiation) IsTerminal() bool { /* accepted|rejected|cancelled|expired */ }
```

`ux_neg_per_bidder (offer_id, bidder_owner_type, bidder_owner_id)` — a single bidder can have at most one live negotiation chain per listing. Re-bidding after a reject is a NEW row (the uniqueness is enforced for pending/countered; we allow multiple terminal rows). **RISK:** Postgres unique indexes don't naturally allow "unique among non-terminal" — use a **partial unique index** declared via raw DDL in `cmd/main.go` after AutoMigrate:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS ux_neg_active_per_bidder
ON otc_negotiations (offer_id, bidder_owner_type, COALESCE(bidder_owner_id, 0))
WHERE status IN ('pending','countered');
```

Drop the column-level `uniqueIndex` shown above; switch to the partial. Same pattern is already used at `stock-service/cmd/main.go` for similar XOR/partial constraints (look for the trailing `db.Exec(...)` block ~line 900 — `holding_reservations.otc_contract_id` CHECK is added there).

### 1.3 `OTCNegotiationRevision` — `stock-service/internal/model/otc_negotiation_revision.go`

Replaces `OTCOfferRevision`. Append-only per-chain history.

```go
type OTCNegotiationRevision struct {
    ID               uint64          `gorm:"primaryKey;autoIncrement"`
    NegotiationID    uint64          `gorm:"not null;uniqueIndex:ux_neg_rev,priority:1"`
    RevisionNumber   int             `gorm:"not null;uniqueIndex:ux_neg_rev,priority:2"`
    Quantity         decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    StrikePrice      decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    Premium          decimal.Decimal `gorm:"type:numeric(20,8);not null"`
    SettlementDate   time.Time       `gorm:"type:date;not null"`
    Action           string          `gorm:"size:16;not null;check:action IN ('create','counter','accept','reject','cancel')"`
    ActorPrincipalType string        `gorm:"size:10;not null"`
    ActorPrincipalID   uint64        `gorm:"not null"`
    ActingEmployeeID *uint64
    CreatedAt        time.Time
}
```

### 1.4 `OptionContract` — extend `stock-service/internal/model/option_contract.go`

Add `NegotiationID uint64` (the winning chain) and drop the implicit "OfferID is unique." Today `OfferID` carries `uniqueIndex`; change to a plain index since an offer ID is shared by every chain on the listing (only one chain will produce a contract, but the uniqueness should move to NegotiationID).

```go
NegotiationID uint64 `gorm:"not null;uniqueIndex"`
// OfferID changes from uniqueIndex → index
```

### 1.5 `HoldingReservation` & account-side `Reservation`

`HoldingReservation` model already has `OrderID | OTCContractID | PeerOptionContractID`. Add a fourth FK column for **listing-level reservations** (the seller posting a sell listing locks shares immediately, before any chain exists):

```go
OTCOfferID *uint64 `gorm:"uniqueIndex:ux_holding_reservation_otc_offer"` 
```

Update the XOR CHECK in `BeforeCreate` to "exactly one of the four." Update the DB-layer CHECK constraint in `cmd/main.go` (search for `holding_reservation requires exactly one`).

For premium reservations (buy listings), `account-service`'s `ReserveFunds` is keyed on `order_id uint64`. We pass `offer_id` as the synthetic `order_id` — but RISK: collisions between order IDs and offer IDs in the same column will eventually happen since both are autoIncrement bigserials. Two clean alternatives:
- (a) Add a `reservation_kind` column to account-service's reservations + change the uniqueness to `(kind, ref_id)`.
- (b) Encode `kind` into a high-bit XOR'd into the synthetic order ID before passing (hacky).

**Recommended:** (a). Add `Kind string` (default `"order"`) and a new RPC `ReserveFundsForOffer(account_id, offer_id, amount, currency, idem_key)` that writes `kind="otc_offer"`. Then `ReleaseReservationForOffer(offer_id)` mirrors. This is in `contract/proto/account/account.proto` + `account-service/internal/{model,service}`. Even though account-service is out of your declared scope, it MUST grow this surface or money safety isn't real.

**Decision: implement (a).** The cleanest path is two new RPCs on `AccountService`, keyed on a new `ref_kind` enum column with values `order` (existing) and `otc_offer` (new). All existing reservation rows are backfilled with `ref_kind="order"` during AutoMigrate.

---

## 2. Repository Layer

### 2.1 `stock-service/internal/repository/otc_offer_repository.go` — rewrite

Drop `ListByOwner`, `ListNegotiationHistory`, `SumActiveQuantityForSeller` from this repo's surface (they move to negotiation repo where appropriate; SumActiveQuantityForSeller's seller-invariant SQL must learn the new schema — see §6).

New surface:

```go
func (r *OTCOfferRepository) Create(ctx, *model.OTCOffer) error
func (r *OTCOfferRepository) GetByID(id uint64) (*model.OTCOffer, error)
func (r *OTCOfferRepository) GetByIDForUpdate(tx *gorm.DB, id uint64) (*model.OTCOffer, error)  // SELECT FOR UPDATE — used by accept TX
func (r *OTCOfferRepository) Save(o *model.OTCOffer) error
func (r *OTCOfferRepository) ListPublic(f PublicListingFilter) ([]model.OTCOffer, int64, error) // §4 endpoint backing
func (r *OTCOfferRepository) ListByPoster(ownerType, ownerID, statuses, page, pageSize) ([]model.OTCOffer, int64, error)
func (r *OTCOfferRepository) ListExpiringOpen(today string, limit int) ([]model.OTCOffer, error) // cron
```

`PublicListingFilter` carries `Ticker *string`, `Direction *string` (`buy`/`sell`), `SettlementFrom *time.Time`, `SettlementTo *time.Time`, `Page`, `PageSize`. Status implicitly = `open`.

### 2.2 `stock-service/internal/repository/otc_negotiation_repository.go` — new

```go
func (r *OTCNegotiationRepository) Create(*model.OTCNegotiation) error
func (r *OTCNegotiationRepository) GetByID(id uint64) (*model.OTCNegotiation, error)
func (r *OTCNegotiationRepository) GetByIDForUpdate(tx *gorm.DB, id uint64) (*model.OTCNegotiation, error)
func (r *OTCNegotiationRepository) Save(*model.OTCNegotiation) error
// First-accept-wins cascade: bulk-update every sibling chain to cancelled in one statement
func (r *OTCNegotiationRepository) CancelSiblings(tx *gorm.DB, offerID, winningNegID uint64, actorType string, actorID uint64) (int64, error)
func (r *OTCNegotiationRepository) ListByOffer(offerID uint64, statuses []string) ([]model.OTCNegotiation, error)
func (r *OTCNegotiationRepository) ListByBidder(ownerType, ownerID, statuses, page, pageSize) ([]model.OTCNegotiation, int64, error)
```

`CancelSiblings` uses `tx.Session(&gorm.Session{SkipHooks: true})` + raw `UPDATE otc_negotiations SET status='cancelled', last_actor_principal_type=?, last_actor_principal_id=?, updated_at=NOW(), version=version+1 WHERE offer_id=? AND id<>? AND status IN ('pending','countered')`. SkipHooks because we're doing a bulk update that intentionally skips per-row optimistic locking (we're inside the parent-offer `SELECT FOR UPDATE` so concurrent writers are blocked already). Pattern matches what CLAUDE.md §"For bulk updates that intentionally skip version checks (e.g., spending resets, overdue marking)" allows.

### 2.3 `otc_negotiation_revision_repository.go` — new

Mirror of the existing `OTCOfferRevisionRepository`: `Append`, `NextRevisionNumber(negotiationID)`, `ListByNegotiation`.

### 2.4 Read-receipt repo

Repurpose `OTCOfferReadReceipt` → key on `(OwnerType, OwnerID, NegotiationID)` (rename column). Same idea: drives the `unread` flag on `/me/otc/options` list responses.

---

## 3. Service Layer

`stock-service/internal/service/otc_offer_service.go` — rewrite as three logical services:

### 3.1 `OTCOfferService` — listing CRUD + reservations
```go
type CreateListingInput struct {
    ActorUserID int64; ActorSystemType string
    OnBehalfOfClientID uint64
    Direction string; StockID uint64; Ticker string
    Quantity, StrikePrice, Premium decimal.Decimal
    SettlementDate time.Time
    AccountID uint64 // poster's account
    Public bool; PrivateToBankCode *string
}
func (s *OTCOfferService) CreateListing(ctx, in CreateListingInput) (*model.OTCOffer, error)
func (s *OTCOfferService) CancelListing(ctx, offerID uint64, actor ...) (*model.OTCOffer, error)
func (s *OTCOfferService) ListPublic(ctx, PublicListingFilter) ([]ListingRow, int64, error)
func (s *OTCOfferService) GetListing(ctx, offerID, actor) (*model.OTCOffer, []model.OTCNegotiation, error)
```

`CreateListing` runs in a `db.Transaction`:
1. Validate inputs (positive decimals, settlement_date > today, direction in {buy,sell}).
2. If `direction=sell`: assert seller available shares ≥ qty (see §6). Then call `holdingRes.ReserveForOTCOffer(ctx, ownerType, ownerID, "stock", stockID, offerID=<allocated after insert>, qty)`. **Sequencing problem:** offerID isn't known until insert. Solution: insert offer first with `HoldingReservationID=NULL`, then call the holding reservation, then `Save` with the FK. All in one TX so a failure rolls back the offer.
3. If `direction=buy`: compute total commitment = `premium * quantity` (or just `premium` if premium is per-contract — check existing convention; the accept-saga uses `o.Premium` straight, so `premium` here is total). Call `accounts.ReserveFundsForOffer(ctx, accountID, offerID, amount, currency, idemKey)`. Save the returned `reservation_id` onto `o.AccountReservationID`.
4. Publish `otc.listing-created` Kafka event via outbox.

### 3.2 `OTCNegotiationService` — chain CRUD + counter/reject
```go
type StartNegotiationInput struct { OfferID, ActorUserID, ActorSystemType, OnBehalfOfClientID, BidderAccountID, ... terms ... }
func (s *OTCNegotiationService) Start(ctx, in) (*model.OTCNegotiation, error)
func (s *OTCNegotiationService) Counter(ctx, in CounterInput) (*model.OTCNegotiation, error)
func (s *OTCNegotiationService) Reject(ctx, in RejectInput) (*model.OTCNegotiation, error)
func (s *OTCNegotiationService) Cancel(ctx, in CancelInput) (*model.OTCNegotiation, error) // bidder withdraws
func (s *OTCNegotiationService) ListMyNegotiations(ctx, actor, role string /* bidder|poster|either */, ...) ([]model.OTCNegotiation, int64, error)
func (s *OTCNegotiationService) GetNegotiation(ctx, negID, actor) (*model.OTCNegotiation, []model.OTCNegotiationRevision, error)
```

`Start`: validate parent offer is `open`, bidder ≠ poster, no existing pending chain by this bidder. Seed terms = parent terms (a fresh "I'll take it as-posted" bid). Append revision rev=1 action=`create`. Publish `otc.negotiation-started`.

`Counter`: lock current chain via `GetByIDForUpdate` inside a TX. Validate chain not terminal. Validate actor is the other party (alternating turns: `LastActorPrincipalID != actor`). On a counter that increases quantity, re-run the seller-share-invariant. Update terms, status=`countered`, append revision. Publish `otc.negotiation-countered`.

`Reject` / `Cancel`: terminal write + revision.

### 3.3 `OTCContractFormationService` — first-accept-wins atomic accept

This is the heart of the design. `Accept(ctx, in AcceptNegotiationInput)`:

```go
// PSEUDOCODE — the actual flow
// in.NegotiationID, in.ActorUserID, in.ActorSystemType
err := s.db.Transaction(func(tx *gorm.DB) error {
    neg, err := negRepo.GetByIDForUpdate(tx, in.NegotiationID)
    if err != nil { return err }
    if neg.IsTerminal() { return ErrChainTerminal }
    if neg.LastActorPrincipalID == in.ActorUserID && neg.LastActorPrincipalType == in.ActorSystemType {
        return ErrCannotAcceptOwnTerms
    }

    // ATOMIC LOCK on parent — gate the race
    offer, err := offerRepo.GetByIDForUpdate(tx, neg.OfferID)
    if err != nil { return err }
    if offer.Status != model.OTCOfferStatusOpen {
        return ErrOfferAlreadyConsumed // someone else won
    }

    // Resolve buyer/seller owners + accounts from offer.direction + neg.bidder.
    buyerOwner, sellerOwner, buyerAcctID, sellerAcctID := resolveBuyerSeller(offer, neg)

    // STEP A: re-check seller's reservation still covers this qty. Since the
    // listing reservation was for the listing's original qty and counters may
    // have moved qty *upward*, re-validate here. (Counter already runs the
    // assert, but the TX boundary is here.)
    if offer.Direction == "sell" && neg.Quantity.GreaterThan(offer.Quantity) {
        // Top up the holding reservation by the delta.
        delta := neg.Quantity.Sub(offer.Quantity).IntPart()
        if err := holdingRes.TopUpReservation(tx, *offer.HoldingReservationID, delta); err != nil { return err }
    }

    // STEP B: create the OptionContract row. Premium/strike currency = seller's account currency.
    contract := &model.OptionContract{
        OfferID:          offer.ID,
        NegotiationID:    neg.ID,
        BuyerOwnerType:   buyerOwner.Type, BuyerOwnerID: buyerOwner.ID, BuyerAccountID: buyerAcctID,
        SellerOwnerType:  sellerOwner.Type, SellerOwnerID: sellerOwner.ID, SellerAccountID: sellerAcctID,
        StockID: offer.StockID, Ticker: offer.Ticker,
        Quantity: neg.Quantity, StrikePrice: neg.StrikePrice,
        PremiumPaid: neg.Premium, PremiumCurrency: sellerCcy, StrikeCurrency: sellerCcy,
        SettlementDate: neg.SettlementDate,
        Status: model.OptionContractStatusActive,
        SagaID: uuid.NewString(), PremiumPaidAt: time.Now().UTC(),
    }
    if err := contractRepo.WithTx(tx).Create(contract); err != nil { return err }

    // STEP C: convert the listing's reservation → contract's reservation.
    //   - sell listing: re-key the existing HoldingReservation row from OTCOfferID → OTCContractID
    //     (a single UPDATE; the locked shares move from "offer-locked" to "contract-locked"
    //     without unlocking + relocking, which would have a TOCTOU window).
    //   - buy listing: re-key the existing account reservation similarly via account-service RPC
    //     ReRefReservation(old_ref_kind, old_ref_id, new_ref_kind, new_ref_id).
    if offer.Direction == "sell" {
        if err := holdingResRepo.WithTx(tx).RekeyOfferReservationToContract(*offer.HoldingReservationID, contract.ID); err != nil { return err }
    } else {
        // Note: this is gRPC, NOT a tx participant. But the call is idempotent;
        // on TX rollback we'd need a compensation. To keep this fully
        // transactional we DELAY the account-side rekey until after commit
        // and use the outbox to schedule it. See RISK note below.
    }

    // STEP D: cancel sibling chains in bulk.
    cancelled, err := negRepo.CancelSiblings(tx, offer.ID, neg.ID, in.ActorSystemType, uint64(in.ActorUserID))
    if err != nil { return err }

    // STEP E: mark winning chain accepted + flip parent to consumed.
    neg.Status = model.OTCNegotiationStatusAccepted
    neg.ContractID = &contract.ID
    neg.LastActorPrincipalType = in.ActorSystemType
    neg.LastActorPrincipalID = uint64(in.ActorUserID)
    if err := negRepo.WithTx(tx).Save(neg); err != nil { return err }

    offer.Status = model.OTCOfferStatusConsumed
    now := time.Now().UTC()
    offer.ConsumedAt = &now
    offer.WinningNegotiationID = &neg.ID
    if err := offerRepo.WithTx(tx).Save(offer); err != nil { return err }

    // STEP F: append accept-revision; enqueue outbox events:
    //   - otc.contract-created (winner)
    //   - otc.listing-consumed
    //   - otc.negotiation-cancelled (one per sibling, BATCHED into one Kafka event with N negotiation IDs to keep transactional outbox row count bounded)
    enqueueOutbox(tx, ...)

    // STEP G (sell listing): trigger the existing premium-payment saga AFTER TX commits.
    // Same as today: ReserveFunds(buyer, premium) → settle → credit seller → ConsumeForOTCContract.
    // For sell listings the seller's shares are already pre-reserved (so reserve_and_contract step shrinks to just "contract row already exists; mark already-reserved").
    // For buy listings the buyer's premium is already pre-reserved on the offer; the saga just settles + credits seller.
    sagaToRunAfterCommit = ...

    return nil
})
if err != nil { return nil, err }

// post-commit: kick saga for money flow.
return s.acceptSaga.Run(ctx, sagaToRunAfterCommit)
```

**Key safety:** the parent `SELECT FOR UPDATE` blocks every concurrent `Accept(neg=X)` attempt on the same offer. The first one to grab the lock commits; subsequent ones see `Status != open` and return `ErrOfferAlreadyConsumed` (mapped to gRPC `FailedPrecondition` → REST 409 `business_rule_violation`).

**RISK A:** The account-side rekey for buy listings can't live inside the DB TX (it's a remote gRPC call). Options:
- (i) Do it BEFORE the TX (eager). If the TX fails afterwards, run a compensating un-rekey. Adds two RPCs per accept.
- (ii) Do it inside the TX as a step right before commit, accept that a crash after commit-but-before-RPC needs an outbox-driven retry RPC.
- (iii) Lazy: the post-commit saga uses the offer's existing reservation as if it were the contract's reservation, and we never rekey — instead the saga's settle step references the `(ref_kind=otc_offer, ref_id=offer.ID)` reservation directly. Cleaner. Account-service's `PartialSettleReservation` becomes overloaded to accept either ref shape.

**Pick (iii).** Add `PartialSettleByRef(ref_kind, ref_id, ...)` to account-service. The saga issues a settle on `(otc_offer, offer.ID)` for buy listings, on `(order, contract.ID)` (the buyer's new reservation created inside the saga) for sell listings. **RISK:** this asymmetry is annoying. Could also unify by having the offer-creation reserve into a synthetic "ref_kind=otc" namespace and the accept saga always uses that. Recommend asymmetry for now; revisit in a follow-up.

**RISK B:** `TopUpReservation` on the holding side. Counters may have increased the share qty above the listing's original. Today the seller-invariant check at counter time is enough to PROVE shares are available, but no shares are LOCKED for the delta until accept. Two bidders could each counter up to the seller's full available quantity; whichever accepts first wins, the other should have been rejected at counter time. Solution: each chain's counter that INCREASES qty must lock the delta as an incremental reservation tied to the negotiation. Add a new HoldingReservation column `OTCNegotiationID *uint64` and treat each chain's delta-above-parent as a separate held reservation; release on chain terminal (reject/cancel/cascade-cancel/expire); convert to part of the contract reservation on accept. **This is a non-trivial subsystem.** Keep it on the implementer's radar but the simplest first cut is: forbid counters from raising quantity above the listing's original. Document in spec §21 as a hard business rule.

**Recommendation: forbid quantity-up counters in V1.** Counters can lower qty, change strike, change premium, change settlement_date. Quantity-up forces the bidder to start a NEW chain (which under (iii) above gets validated against the seller invariant counting all open chains).

### 3.4 `OTCExpiryCron` updates — `stock-service/internal/service/otc_expiry_cron.go`

Existing cron handles `OptionContract` expiry. Extend to also walk:
- `OTCOffer` with `status=open` and `settlement_date < today`: flip to `expired`; release the listing-level reservation (holding for sell, account for buy); cascade-expire all child chains. Publish `otc.listing-expired` + `otc.negotiation-expired` (batched).

---

## 4. gRPC RPCs

### 4.1 `contract/proto/stock/stock.proto` — extend `OTCOptionsService`

Replace the existing OTC option RPCs with these (clean break, no shims):

```proto
service OTCOptionsService {
  // Listings
  rpc CreateListing(CreateOTCListingRequest) returns (OTCListingResponse);
  rpc CancelListing(CancelOTCListingRequest) returns (OTCListingResponse);
  rpc ListPublicListings(ListPublicOTCListingsRequest) returns (ListOTCListingsResponse); // backs /api/v3/otc/options
  rpc GetListing(GetOTCListingRequest) returns (OTCListingDetailResponse); // listing + open negotiations summary
  rpc ListMyListings(ListMyOTCListingsRequest) returns (ListOTCListingsResponse); // backs /api/v3/me/otc/options (poster role)

  // Negotiations
  rpc StartNegotiation(StartOTCNegotiationRequest) returns (OTCNegotiationResponse);
  rpc CounterNegotiation(CounterOTCNegotiationRequest) returns (OTCNegotiationResponse);
  rpc RejectNegotiation(RejectOTCNegotiationRequest) returns (OTCNegotiationResponse);
  rpc CancelNegotiation(CancelOTCNegotiationRequest) returns (OTCNegotiationResponse);
  rpc AcceptNegotiation(AcceptOTCNegotiationRequest) returns (AcceptOTCNegotiationResponse); // atomic accept; returns contract
  rpc GetNegotiation(GetOTCNegotiationRequest) returns (OTCNegotiationDetailResponse);
  rpc ListMyNegotiations(ListMyOTCNegotiationsRequest) returns (ListOTCNegotiationsResponse); // bidder side; backs /api/v3/me/otc/options (bidder role)

  // Contracts — UNCHANGED (existing surface)
  rpc ListMyContracts(ListMyContractsRequest) returns (ListContractsResponse);
  rpc GetContract(GetContractRequest) returns (OptionContractResponse);
  rpc ExerciseContract(ExerciseContractRequest) returns (ExerciseResponse);

  // History (TERMINAL listings + negotiations) — reshaped
  rpc ListNegotiationHistory(ListNegotiationHistoryRequest) returns (ListOTCNegotiationsResponse);

  // Ratings — unchanged
  rpc SubmitRating(...) ...; rpc GetTraderProfile(...) ...; rpc ListReceivedRatings(...) ...;
}
```

New messages:

```proto
message CreateOTCListingRequest {
  int64 actor_user_id = 1;
  string actor_system_type = 2;
  uint64 on_behalf_of_client_id = 3;
  string direction = 4;          // "buy" | "sell"
  string ticker = 5;
  uint64 stock_id = 6;
  string quantity = 7;
  string strike_price = 8;
  string premium = 9;
  string settlement_date = 10;
  uint64 account_id = 11;        // poster's
  bool   public = 12;
  string private_to_bank_code = 13;
}
message OTCListingResponse {
  uint64 id = 1;
  PartyRef poster = 2;
  string direction = 3;
  uint64 stock_id = 4; string ticker = 5;
  string quantity = 6; string strike_price = 7; string premium = 8;
  string settlement_date = 9; string status = 10;
  int64  open_negotiations_count = 11;   // !! drives discovery UI
  string market_reference_price = 12;
  string created_at = 13; string updated_at = 14; int64 version = 15;
}
message ListPublicOTCListingsRequest {
  string ticker = 1;             // optional, exact match (case-insensitive uppered server-side)
  string direction = 2;          // optional, "buy"|"sell"
  string settlement_from = 3;    // optional YYYY-MM-DD
  string settlement_to = 4;
  int32  page = 5; int32 page_size = 6;
}
message ListOTCListingsResponse { repeated OTCListingResponse listings = 1; int64 total = 2; }

message StartOTCNegotiationRequest {
  uint64 offer_id = 1;
  int64  actor_user_id = 2; string actor_system_type = 3; uint64 on_behalf_of_client_id = 4;
  uint64 bidder_account_id = 5;
  // Initial counter-terms (optional; default = parent terms).
  string quantity = 6; string strike_price = 7; string premium = 8; string settlement_date = 9;
}
message OTCNegotiationResponse {
  uint64 id = 1; uint64 offer_id = 2;
  PartyRef bidder = 3; PartyRef poster = 4;
  string direction = 5;
  string quantity = 6; string strike_price = 7; string premium = 8; string settlement_date = 9;
  string status = 10; PartyRef last_actor = 11;
  uint64 contract_id = 12;       // 0 unless accepted
  string created_at = 13; string updated_at = 14; int64 version = 15; bool unread = 16;
}
message CounterOTCNegotiationRequest { uint64 negotiation_id = 1; int64 actor_user_id = 2; string actor_system_type = 3; uint64 on_behalf_of_client_id = 4; string quantity = 5; string strike_price = 6; string premium = 7; string settlement_date = 8; }
message AcceptOTCNegotiationRequest { uint64 negotiation_id = 1; int64 actor_user_id = 2; string actor_system_type = 3; uint64 on_behalf_of_client_id = 4; }
message AcceptOTCNegotiationResponse { uint64 negotiation_id = 1; uint64 offer_id = 2; uint64 contract_id = 3; string status = 4; string saga_id = 5; OptionContractResponse contract = 6; int32 siblings_cancelled = 7; }
// + RejectOTCNegotiationRequest, CancelOTCNegotiationRequest, Get/List/Detail counterparts.
```

**RISK:** existing proto field numbers in `OTCOptionsService` are widely referenced. Since CLAUDE.md says "clean break, no shims authorized," the RPC names + messages can be replaced wholesale. Bump proto field numbers from 1 in the new messages. Drop the old `OTCOfferResponse`, `CreateOTCOfferRequest`, etc. message types entirely. Regenerate via `make proto`.

### 4.2 Handler — `stock-service/internal/handler/otc_options_handler.go`

Wholesale rewrite. Each new RPC gets a handler method that:
- Parses decimals + dates.
- Delegates to the appropriate service (OTCOfferService for listings, OTCNegotiationService for chains, OTCContractFormationService for AcceptNegotiation).
- Maps owner type via `model.OwnerFromLegacy`.
- Resolves `ActingEmployeeID` via `resolveOrderOwner` helper (already in `otc_handler.go`).
- Surfaces sentinel errors via the existing `mapOTCErr` helper.

Drop `otc_handler.go`'s old methods? **No** — those serve the stock-listing marketplace, NOT options. Leave them; only their route binding changes (sibling agent's scope: bind `Portfolio.ListOTCOffers` under `/api/v3/otc/stocks`).

### 4.3 `account-service` proto additions — `contract/proto/account/account.proto`

```proto
rpc ReserveFundsForOffer(ReserveFundsForOfferRequest) returns (ReserveFundsResponse);
rpc ReleaseReservationForOffer(ReleaseReservationForOfferRequest) returns (ReleaseReservationResponse);
rpc PartialSettleByRef(PartialSettleByRefRequest) returns (PartialSettleReservationResponse);

message ReserveFundsForOfferRequest { uint64 account_id = 1; uint64 offer_id = 2; string amount = 3; string currency_code = 4; string idempotency_key = 5; }
message ReleaseReservationForOfferRequest { uint64 offer_id = 1; string idempotency_key = 2; }
message PartialSettleByRefRequest { string ref_kind = 1; uint64 ref_id = 2; uint64 settle_seq = 3; string amount = 4; string memo = 5; string idempotency_key = 6; }
```

Add `RefKind string` column to `account-service/internal/model/reservation.go` (default `"order"`, indexed). Update repo to filter on `(ref_kind, ref_id)`. Update existing handlers to default `ref_kind="order"` so old behavior is preserved.

---

## 5. REST Routes — `api-gateway`

### 5.1 New handler — `api-gateway/internal/handler/otc_options_handler.go` (REWRITE)

Replace the existing file methods. Keep the existing `OTCOptionsHandler` struct + injected gRPC clients.

| Method | Path | Middleware | Handler | Body / Query | Ownership rule |
|---|---|---|---|---|---|
| GET | `/api/v3/otc/options` | `AnyAuthMiddleware` | `ListPublicListings` | query: `ticker?`, `direction?` (`buy`/`sell`), `settlement_from?`, `settlement_to?`, `page?`, `page_size?` | none — public |
| POST | `/api/v3/otc/options` | `AnyAuth` + `RequirePermissionOrClient(All, securities.trade, otc.trade)` + `ResolveIdentity(OwnerIsBankIfEmployee)` | `CreateListing` | `{direction, ticker, quantity, strike_price, premium, settlement_date, account_id, on_behalf_of_client_id?, public?}` | `ResolveAndCheckAccount(account_id, on_behalf_of_client_id)` |
| GET | `/api/v3/otc/options/:id` | `AnyAuth` + `ResolveIdentity` | `GetListing` | — | listings are publicly readable; negotiation summary list only includes chains the caller participates in or all if caller is the poster |
| POST | `/api/v3/otc/options/:id/cancel` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `CancelListing` | `{on_behalf_of_client_id?}` | only poster can cancel; verify in service layer + gateway via `enforceOwnership` against the listing's poster |
| POST | `/api/v3/otc/options/:id/negotiations` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `StartNegotiation` | `{bidder_account_id, on_behalf_of_client_id?, quantity?, strike_price?, premium?, settlement_date?}` | `ResolveAndCheckAccount(bidder_account_id, ...)`; bidder ≠ poster check in service |
| GET | `/api/v3/otc/options/:id/negotiations` | `AnyAuth` + `ResolveIdentity` | `ListNegotiationsForListing` | `?status=...&page=...` | poster sees ALL chains; non-poster sees only their own chain (filter in service) |
| GET | `/api/v3/otc/negotiations/:nid` | `AnyAuth` + `ResolveIdentity` | `GetNegotiation` | — | service enforces participant-only (bidder or poster) |
| POST | `/api/v3/otc/negotiations/:nid/counter` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `CounterNegotiation` | `{quantity, strike_price, premium, settlement_date, on_behalf_of_client_id?}` | participant check in service |
| POST | `/api/v3/otc/negotiations/:nid/accept` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `AcceptNegotiation` | `{on_behalf_of_client_id?}` | participant check in service; cannot accept own most recent terms |
| POST | `/api/v3/otc/negotiations/:nid/reject` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `RejectNegotiation` | `{on_behalf_of_client_id?}` | participant check |
| POST | `/api/v3/otc/negotiations/:nid/cancel` | `AnyAuth` + `RequirePermissionOrClient` + `ResolveIdentity` | `CancelNegotiation` | `{on_behalf_of_client_id?}` | bidder-only |
| GET | `/api/v3/me/otc/options` | `AnyAuth` + `ResolveIdentity` | `ListMyOTC` | `?role=poster|bidder|either&status=...` | merges: listings posted by caller + negotiations caller is bidder on. Caller's identity drives the filter. |
| GET | `/api/v3/me/otc/history` | unchanged | unchanged | reshape to walk both `OTCOffer` terminal + `OTCNegotiation` terminal | |
| GET | `/api/v3/me/otc/contracts` | unchanged | unchanged | |
| POST | `/api/v3/otc/contracts/:id/exercise` | unchanged | unchanged | |

`/api/v3/me/otc/options` response shape:
```json
{
  "as_poster": [ { OTCListing... open_negotiations_count: N } ],
  "as_bidder": [ { OTCNegotiation... offer: {...} } ],
  "total_poster": int, "total_bidder": int
}
```

Validation specifics (CLAUDE.md):
- `direction` → `oneOf("direction", req.Direction, "buy", "sell")`.
- `quantity`, `strike_price`, `premium` decimals → parse + `positive()`.
- `settlement_date` → `time.Parse("2006-01-02")` + must be `> today`.
- `status` query param on list endpoints → `oneOf` against `{open, consumed, cancelled, expired}` for listings, against `{pending, countered, accepted, rejected, cancelled, expired}` for negotiations.
- `account_id` → `ResolveAndCheckAccount` (existing helper in `api-gateway/internal/handler/validation.go`).

### 5.2 Router — `api-gateway/internal/router/router_v3.go`

DELETE these existing blocks (lines ~261-296):
- `otc := v3.Group("/otc/offers"); otc.GET("", h.Portfolio.ListOTCOffers)` — moves to `/api/v3/otc/stocks` (sibling agent's scope).
- `otcTrade.POST("/:id/buy")` — same.
- `otcRead.GET("/offers/:id", h.OTCOptions.GetOffer)` — replaced.
- All five `otcOptionsTrade.POST("/offers/...")` lines — replaced.
- `otcOnBehalf` block at line 775 — `/otc/offers/:id/buy-on-behalf` moves to stocks agent.

REPLACE with one consolidated `otcOptions := v3.Group("/otc")` block:

```go
otcOptions := v3.Group("/otc")
otcOptions.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
otcOptions.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
{
    // Public discovery — no extra perm
    otcOptions.GET("/options", h.OTCOptions.ListPublicListings)
    otcOptions.GET("/options/:id", h.OTCOptions.GetListing)
    otcOptions.GET("/options/:id/negotiations", h.OTCOptions.ListNegotiationsForListing)
    otcOptions.GET("/negotiations/:nid", h.OTCOptions.GetNegotiation)
    otcOptions.GET("/contracts/:id", h.OTCOptions.GetContract)
    otcOptions.GET("/traders/:owner_type/:owner_id/rating", h.OTCOptions.GetTraderProfile)
}
otcOptionsTrade := v3.Group("/otc")
otcOptionsTrade.Use(middleware.AnyAuthMiddleware(h.Auth.Client()))
otcOptionsTrade.Use(middleware.RequirePermissionOrClient(middleware.PermAll, perms.Securities.Trade.Any, perms.Otc.Trade.Accept))
otcOptionsTrade.Use(middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee))
{
    otcOptionsTrade.POST("/options", h.OTCOptions.CreateListing)
    otcOptionsTrade.POST("/options/:id/cancel", h.OTCOptions.CancelListing)
    otcOptionsTrade.POST("/options/:id/negotiations", h.OTCOptions.StartNegotiation)
    otcOptionsTrade.POST("/negotiations/:nid/counter", h.OTCOptions.CounterNegotiation)
    otcOptionsTrade.POST("/negotiations/:nid/accept",  h.OTCOptions.AcceptNegotiation)
    otcOptionsTrade.POST("/negotiations/:nid/reject",  h.OTCOptions.RejectNegotiation)
    otcOptionsTrade.POST("/negotiations/:nid/cancel",  h.OTCOptions.CancelNegotiation)
    otcOptionsTrade.POST("/contracts/:id/exercise",    h.OTCOptions.ExerciseContract)
}
```

In the `/me` group, change:
```go
me.GET("/otc/offers", bankIfEmp, h.OTCOptions.ListMyOffers)
```
to:
```go
me.GET("/otc/options", bankIfEmp, h.OTCOptions.ListMyOTC)
```

`/me/otc/history`, `/me/otc/ratings`, `/me/otc/ratings/received`, `/me/otc/contracts`, `/me/otc/contracts/peer/:id/exercise`, `/me/peer-otc/negotiations*` — all stay.

---

## 6. Seller-Invariant Check Rewrite

`stock-service/internal/repository/otc_offer_repository.go` `SumActiveQuantityForSeller` must change since the offer schema changed.

New SQL (inside the same function):
```
SELECT COALESCE(SUM(q),0) FROM (
  SELECT quantity AS q FROM option_contracts WHERE seller_owner_*=? AND stock_id=? AND status='active'
  UNION ALL
  -- sell listings, status=open or with any pending/countered chain
  SELECT quantity AS q FROM otc_offers
   WHERE direction='sell' AND status='open' AND poster_owner_*=? AND stock_id=?
  UNION ALL
  -- buy listings where I am the poster (irrelevant — buy listings don't reserve my shares)
  -- (omitted)
  UNION ALL
  -- negotiations where I'm the BIDDER on a buy listing (I am the would-be seller)
  SELECT n.quantity AS q FROM otc_negotiations n
   JOIN otc_offers o ON o.id=n.offer_id AND o.direction='buy' AND o.stock_id=?
   WHERE n.bidder_owner_*=? AND n.status IN ('pending','countered')
) t;
```

Cross-bank surface (`peer_otc_negotiations`, `peer_option_contracts`) — preserve existing UNION ALL branches.

---

## 7. Kafka Topics

New topic constants in `contract/kafka/messages.go`:
```go
TopicOTCListingCreated    = "otc.listing-created"
TopicOTCListingCancelled  = "otc.listing-cancelled"
TopicOTCListingConsumed   = "otc.listing-consumed"
TopicOTCListingExpired    = "otc.listing-expired"
TopicOTCNegotiationStarted   = "otc.negotiation-started"
TopicOTCNegotiationCountered = "otc.negotiation-countered"
TopicOTCNegotiationAccepted  = "otc.negotiation-accepted"
TopicOTCNegotiationRejected  = "otc.negotiation-rejected"
TopicOTCNegotiationCancelled = "otc.negotiation-cancelled"
TopicOTCNegotiationExpired   = "otc.negotiation-expired"
```

Retire (drop the constants and all publish call sites): `TopicOTCOfferCreated`, `TopicOTCOfferCountered`, `TopicOTCOfferRejected`, `TopicOTCOfferExpired`. `TopicOTCContractCreated/Exercised/Expired/Failed` stay.

Add corresponding `OTCListingCreatedMessage`, `OTCNegotiationStartedMessage`, etc. payload structs.

Update `stock-service/cmd/main.go` `EnsureTopics(...)` call (line 207) — replace the four `otc.offer-*` lines with the new ten, keep the four `otc.contract-*` ones. Also any service that CONSUMES otc.offer-* (notification-service `internal/consumer/` — check) needs the new topic names added to its `EnsureTopics`.

---

## 8. AutoMigrate + DDL

`stock-service/cmd/main.go` ~line 60:
- ADD `&model.OTCNegotiation{}, &model.OTCNegotiationRevision{}` to the AutoMigrate list.
- REMOVE `&model.OTCOfferRevision{}` (the chain-history table — replaced by negotiation revisions).
- KEEP `&model.OTCOffer{}` but the columns differ — AutoMigrate will add the new columns (`HoldingReservationID`, `AccountReservationID`, `ConsumedAt`, `WinningNegotiationID`, `PosterOwnerType/ID`) and leave the deleted ones in place (it never drops). Add an explicit `db.Migrator().DropColumn(&model.OTCOffer{}, "initiator_owner_type")` etc. block (mirror the existing block ~line 905 that drops pre-Task-4 columns).

DDL to add after AutoMigrate:
```sql
-- Partial unique: one active chain per (offer, bidder)
CREATE UNIQUE INDEX IF NOT EXISTS ux_neg_active_per_bidder
  ON otc_negotiations (offer_id, bidder_owner_type, COALESCE(bidder_owner_id, 0))
  WHERE status IN ('pending','countered');

-- Update XOR CHECK on holding_reservations to include otc_offer_id
ALTER TABLE holding_reservations DROP CONSTRAINT IF EXISTS holding_reservations_xor_check;
ALTER TABLE holding_reservations ADD CONSTRAINT holding_reservations_xor_check
  CHECK ( (order_id IS NOT NULL)::int + (otc_contract_id IS NOT NULL)::int
        + (peer_option_contract_id IS NOT NULL)::int + (otc_offer_id IS NOT NULL)::int = 1 );
```

---

## 9. Migration ("clean break") strategy

Per CLAUDE.md: no shims. The migration plan:

1. **Drop old data.** Since this is a refactor not a production migration, the AutoMigrate path will leave the old `otc_offers` rows in place but with NULL values in the new not-null columns. Add a one-shot `db.Exec("TRUNCATE TABLE otc_offers, otc_offer_revisions, otc_offer_read_receipts, option_contracts RESTART IDENTITY CASCADE")` inside `cmd/main.go` ONLY when an env var `OTC_SCHEMA_RESET=1` is set, with a `log.Println("OTC: resetting tables for schema reset")` warning. Don't run it by default.
2. Drop `OTCOfferRevision` table via `db.Migrator().DropTable("otc_offer_revisions")` after the AutoMigrate of new tables succeeds. Same for the read-receipt rename: `db.Migrator().RenameColumn(&model.OTCNegotiationReadReceipt{}, "offer_id", "negotiation_id")` if AutoMigrate doesn't pick up the rename (it won't — drop + add).
3. Drop pre-refactor columns on `otc_offers` via `db.Migrator().DropColumn(...)` calls (`initiator_owner_*`, `counterparty_owner_*`, `last_modified_by_*`, `initiator_account_id`, etc.). Pattern is already present in main.go.
4. Test-app workflows previously inserting `OTCOffer` rows with the old shape must be rewritten — see §11.

---

## 10. Docs to Update

- `docs/Specification.md`:
  - §17 (Routes table) — replace all `POST /api/v3/otc/offers*` rows with the new `/options` and `/negotiations` rows. Add `/me/otc/options` (replaces `/me/otc/offers`).
  - §18 (Entities) — replace `OTCOffer` entry with new shape; add `OTCNegotiation`, `OTCNegotiationRevision`. Drop `OTCOfferRevision`.
  - §19 (Kafka) — drop the four `otc.offer-*` topics; add the ten new ones.
  - §20 (Enums) — replace `otc_offer_status` (`PENDING/COUNTERED/ACCEPTED/REJECTED/EXPIRED/FAILED`) with `otc_listing_status` (`open/consumed/cancelled/expired`) and `otc_negotiation_status` (`pending/countered/accepted/rejected/cancelled/expired`). Change `otc_offer_direction` enum values from `sell_initiated/buy_initiated` to `sell/buy`.
  - §21 (Rules) — add: (a) parent-offer FOR UPDATE on accept, (b) sibling cascade-cancel, (c) seller-invariant including all pending/countered bidder chains on buy listings, (d) cannot-accept-own-terms rule applies per-chain, (e) counters may not increase quantity above listing original.
  - §26 (Intra-bank OTC) — rewrite the whole section to reflect the new model.
- `docs/api/REST_API_v3.md`:
  - DELETE sections for the old `/api/v3/otc/offers/*` endpoints.
  - ADD new sections for all routes in §5.1's table with full request/response examples.
- Swagger annotations in every new handler method; run `make swagger` (i.e. `cd api-gateway && swag init -g cmd/main.go --output docs`).

---

## 11. Tests to Write/Update

### Unit (stock-service)
- `stock-service/internal/repository/otc_negotiation_repository_test.go` — `Create`, `CancelSiblings` cascade behavior, `ListByOffer`, `ListByBidder` paging.
- `stock-service/internal/repository/otc_offer_repository_test.go` — rewrite for new shape; `ListPublic` filter combinations (ticker/direction/window); `GetByIDForUpdate` returns gorm.ErrRecordNotFound on missing.
- `stock-service/internal/service/otc_offer_service_test.go` — rewrite `Create_*` tests for `CreateListing` (sell reserves shares, buy reserves funds via account stub, both paths fail cleanly if reservation RPC errors).
- `stock-service/internal/service/otc_negotiation_service_test.go` (new) — `Start` validates bidder ≠ poster; `Counter` rejects own-latest-actor; `Counter` rejects qty > listing.qty; `Reject`/`Cancel` terminality.
- `stock-service/internal/service/otc_accept_test.go` — REWRITE. Cases:
  1. Single chain accept → parent flips to consumed, contract created.
  2. **Concurrent accept on two chains** (use `sync.WaitGroup` + DB-backed tx) → exactly one wins, the other returns `ErrOfferAlreadyConsumed`. The losing chain remains pending (not auto-cancelled by the loser — the WINNER's CancelSiblings handles that).
  3. Three chains, one accepts → two siblings flip to cancelled; counts match.
  4. Sell listing accept consumes shares via existing reservation re-key.
  5. Buy listing accept consumes premium via account-service rekey/settle path.
  6. Accept on a non-terminal chain where parent is already consumed (race) → `ErrOfferAlreadyConsumed`.
- `stock-service/internal/handler/otc_options_handler_test.go` — REWRITE: mappers + RPC behavior tests for each new RPC.
- `stock-service/internal/service/otc_expiry_cron_test.go` — extend with: listing with no chains expires; listing with open chains expires + chains cascade-expire.

### Unit (api-gateway)
- `api-gateway/internal/handler/otc_options_handler_test.go` — REWRITE. Cover: validation rejection (bad direction, non-positive decimals, past settlement_date, missing account_id); ownership-mismatch 403 on `account_id` not belonging to caller; route → gRPC client call pass-through; 409 on first-accept-wins race.

### Unit (account-service)
- `account-service/internal/repository/reservation_repository_test.go` — `ReserveByRef("otc_offer", id, ...)` idempotency, `PartialSettleByRef`, `ReleaseByRef`.
- `account-service/internal/handler/reservation_handler_test.go` — new RPC endpoint behavior.

### Integration (test-app/workflows)
- `test-app/workflows/wf_otc_options_test.go` (new) — full lifecycle:
  1. Seed seller with shares + buyer with funds.
  2. Seller creates SELL listing → `GET /api/v3/otc/options` returns it; seller's holding reserved.
  3. Two buyers each `POST /options/:id/negotiations` → both pending.
  4. Buyer1 counters down on premium; Seller counters back; Buyer2 counters separately.
  5. Seller accepts Buyer1's chain → contract returned; Buyer2's chain auto-cancelled (verify via `GET /me/otc/options` for Buyer2); listing.status=consumed; shares moved from buyer→seller premium settled.
  6. Cancel attempt on consumed listing → 409.
- `test-app/workflows/wf_otc_options_race_test.go` (new) — spin two goroutines each calling `POST /negotiations/:nid/accept` on different chains of the same listing. Assert exactly one 201, exactly one 409 `business_rule_violation`; assert siblings_cancelled=1 on the winner's response; assert DB has one contract, one accepted neg, one cancelled neg, listing consumed.
- `test-app/workflows/wf_otc_options_buy_listing_test.go` (new) — BUY listing path: buyer posts (premium reserved); seller bids; buyer accepts; premium settles to seller's account; shares move from seller's holding via the accept saga's holding reservation step (no listing-level holding reservation existed for buy listings).
- `test-app/workflows/otc_options_test.go` — REWRITE to use the new endpoints.
- `test-app/workflows/otc_test.go` — leave alone (stock-listing marketplace, sibling agent's scope).

---

## 12. Permissions

No new permissions needed. The existing `otc.trade.accept`, `otc.trade.on_behalf`, `securities.trade.any` cover create/counter/accept/reject of negotiations.

---

## 13. Order of Implementation (sequencing)

1. account-service: add `ref_kind` column + new RPCs + tests. (Independent of stock-service.)
2. contract/proto changes (`stock.proto`, `account.proto`). `make proto`.
3. Model additions (`otc_negotiation.go`, `otc_negotiation_revision.go`) + `otc_offer.go` rewrite. AutoMigrate + DDL block + drop-column block. `OTC_SCHEMA_RESET` env switch.
4. Repository layer.
5. Kafka topic constants + `EnsureTopics` update + payload structs.
6. Service layer (CreateListing first, then negotiations, then atomic Accept last).
7. gRPC handler rewrite + wiring in `cmd/main.go`.
8. API-gateway handler rewrite + router rewrite + Swagger.
9. Tests (unit then integration).
10. Specification.md + REST_API_v3.md.

---

## 14. RISK Roll-Up

- **RISK A (account-side rekey not transactional):** picked option (iii) — leave the offer-keyed reservation in place, account-service supports `PartialSettleByRef(ref_kind="otc_offer", ref_id=offer.ID)`. Asymmetric vs sell listings.
- **RISK B (counter raising qty needs incremental locking):** forbid in V1. Document in §21.
- **RISK C (partial unique index isn't supported by GORM AutoMigrate):** must hand-roll via `db.Exec` after AutoMigrate. Same precedent in main.go.
- **RISK D (`OfferID` uniqueIndex on `option_contracts`):** must change to a plain index since multiple chains' contracts could theoretically exist (only one will in practice, but the DB shouldn't enforce it on the wrong column). AutoMigrate won't drop indexes — explicit `DROP INDEX` then ADD.
- **RISK E (concurrent OTCOffer accept across two banks):** intra-bank atomicity is the parent FOR UPDATE. Cross-bank chains (peer_otc_negotiations) live in a separate table and are out of scope for the parent-offer cascade — make the cascade target only `otc_negotiations`, not `peer_otc_negotiations`. Document this limitation in §27 (cross-bank chains on the same listing race independently against intra-bank ones, and the design currently doesn't reconcile across the boundary; flag for future Phase 5).
- **RISK F (existing `Direction` enum strings are persisted in many places):** old data uses `sell_initiated/buy_initiated`. The `OTC_SCHEMA_RESET=1` truncate handles dev/CI. In prod the user would need to opt in to running this — document explicitly in the plan's run-book.
- **RISK G (sibling auto-cancel notifications could be a flood):** if a listing has 50 open chains, that's 49 Kafka events + 49 in-app pushes on accept. Batch into one `otc.negotiations-cascade-cancelled` event carrying the array of (negotiationID, bidderOwner) pairs; have notification-service expand to per-recipient push messages on its side. Already noted in §3.3 step F.

---

### Critical Files for Implementation
- `/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/stock-service/internal/model/otc_offer.go`
- `/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/stock-service/internal/service/otc_offer_service.go`
- `/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/stock-service/internal/service/otc_accept_saga.go`
- `/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/contract/proto/stock/stock.proto`
- `/Users/lukasavic/Desktop/Faks/Softversko inzenjerstvo/EXBanka-1-Backend/api-gateway/internal/router/router_v3.go`
