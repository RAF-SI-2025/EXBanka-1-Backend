# Plan: OTC STOCKS Marketplace Refactor (clean-break v3, local + buy-direction)

## 0. Scope guardrails

- **In scope:** local stock OTC offers (sell + new buy direction), the `/otc/stocks` route family, `/me/otc/stocks` (caller's own listings), atomic-safety upgrades.
- **Out of scope:** OTC options (separate plan), cross-bank peer protocol changes, futures/forex OTC.
- **Cross-bank:** the existing `otccache.Refresher.fetchPeer` keeps working unchanged. New local buy offers will only surface through the local arm of the cache. The peer protocol (`/public-stock` response shape) stays sell-only.
- **Clean break:** existing routes `GET /api/v3/otc/offers`, `POST /api/v3/otc/offers/:id/buy`, `POST /api/v3/otc/offers/:id/buy-on-behalf`, `POST /api/v3/me/portfolio/:id/make-public` are deleted, NOT aliased. (User authorized.)

## 1. Renames mapping

| Old (deleted) | New |
|---|---|
| `POST /api/v3/me/portfolio/:id/make-public` | `POST /api/v3/me/otc/stocks` (body discriminates `direction=sell\|buy`) |
| `GET /api/v3/otc/offers` | `GET /api/v3/otc/stocks` |
| `POST /api/v3/otc/offers/:id/buy` | `POST /api/v3/otc/stocks/:id/buy` (fills a sell offer) |
| `POST /api/v3/otc/offers/:id/buy-on-behalf` | `POST /api/v3/otc/stocks/:id/buy-on-behalf` |
| *(new)* | `POST /api/v3/otc/stocks/:id/sell` (fills a buy offer) |
| *(new)* | `GET /api/v3/me/otc/stocks` |
| *(new)* | `DELETE /api/v3/me/otc/stocks/:id` |

Stocks gets `stocks` noun; options keeps `offers` noun within `/otc/options/*`. Deliberate naming split.

## 2. Data model

### 2a. New model: `OTCStockBuyOffer`

File: `stock-service/internal/model/otc_stock_buy_offer.go`

```go
package model

import (
    "time"
    "github.com/shopspring/decimal"
    "gorm.io/gorm"
)

const (
    OTCStockBuyOfferStatusActive    = "active"
    OTCStockBuyOfferStatusFilled    = "filled"
    OTCStockBuyOfferStatusCancelled = "cancelled"
    OTCStockBuyOfferStatusExpired   = "expired"
)

type OTCStockBuyOffer struct {
    ID                        uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
    BuyerOwnerType            OwnerType       `gorm:"size:8;not null;index:idx_otcsbo_buyer,priority:1;check:buyer_owner_type IN ('client','bank')" json:"buyer_owner_type"`
    BuyerOwnerID              *uint64         `gorm:"index:idx_otcsbo_buyer,priority:2" json:"buyer_owner_id"`
    BuyerFirstName            string          `gorm:"size:100;not null" json:"buyer_first_name"`
    BuyerLastName             string          `gorm:"size:100;not null" json:"buyer_last_name"`
    BuyerAccountID            uint64          `gorm:"not null;index" json:"buyer_account_id"`
    BuyerAccountNumber        string          `gorm:"size:32;not null" json:"buyer_account_number"`
    StockID                   uint64          `gorm:"not null;index:idx_otcsbo_stock" json:"stock_id"`
    ListingID                 uint64          `gorm:"not null" json:"listing_id"`
    Ticker                    string          `gorm:"size:30;not null;index:idx_otcsbo_ticker" json:"ticker"`
    Name                      string          `gorm:"size:200;not null" json:"name"`
    OriginalQuantity          int64           `gorm:"not null" json:"original_quantity"`
    RemainingQuantity         int64           `gorm:"not null" json:"remaining_quantity"`
    PricePerUnit              decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"price_per_unit"`
    CurrencyCode              string          `gorm:"size:3;not null" json:"currency_code"`
    ReservedAmount            decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"reserved_amount"`
    OriginalReservedAmount    decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"original_reserved_amount"`
    AccountReservationOrderID uint64          `gorm:"not null;uniqueIndex" json:"account_reservation_order_id"`
    Status                    string          `gorm:"size:16;not null;index" json:"status"`
    ActingEmployeeID          *uint64         `gorm:"index" json:"acting_employee_id,omitempty"`
    SagaID                    *string         `gorm:"size:36;index" json:"saga_id,omitempty"`
    CreatedAt                 time.Time       `json:"created_at"`
    UpdatedAt                 time.Time       `json:"updated_at"`
    Version                   int64           `gorm:"not null;default:0" json:"-"`
}

func (OTCStockBuyOffer) TableName() string { return "otc_stock_buy_offers" }

func (o *OTCStockBuyOffer) BeforeSave(tx *gorm.DB) error {
    return ValidateOwner(o.BuyerOwnerType, o.BuyerOwnerID)
}

func (o *OTCStockBuyOffer) BeforeUpdate(tx *gorm.DB) error {
    tx.Statement.Where("version = ?", o.Version)
    o.Version++
    return nil
}
```

### 2b. Sell-direction stays on `Holding.PublicQuantity`

Don't introduce a parallel `otc_stock_sell_offer` table. Sell offers continue to be modeled as `holdings.public_quantity > 0`.

**RISK:** `public_quantity` ≠ `reserved_quantity`. Order placement could consume shares the user has implicitly committed to OTC. Treat `public_quantity` as part of unavailability — add helper `Holding.OTCSafeAvailable() = Quantity - ReservedQuantity - PublicQuantity` and use it in placement.

### 2c. Sequence

```go
db.Exec(`CREATE SEQUENCE IF NOT EXISTS otc_stock_buy_offer_res_seq START 1000000`)
```

Offset start to avoid collision with `orders.id` values used in existing `account_reservations`.

## 3. Repository methods

### 3a. `OTCStockBuyOfferRepository` (new)

```go
type OTCStockBuyOfferRepository struct{ db *gorm.DB }
func (r *OTCStockBuyOfferRepository) Create(o *model.OTCStockBuyOffer) error
func (r *OTCStockBuyOfferRepository) GetByID(id uint64) (*model.OTCStockBuyOffer, error)
func (r *OTCStockBuyOfferRepository) LockByID(tx *gorm.DB, id uint64) (*model.OTCStockBuyOffer, error)  // SELECT FOR UPDATE
func (r *OTCStockBuyOfferRepository) Save(o *model.OTCStockBuyOffer) error
func (r *OTCStockBuyOfferRepository) ListActive(filter OTCStockBuyOfferFilter) ([]model.OTCStockBuyOffer, int64, error)
func (r *OTCStockBuyOfferRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int) ([]model.OTCStockBuyOffer, int64, error)
func (r *OTCStockBuyOfferRepository) AllocateReservationOrderID(tx *gorm.DB) (uint64, error)
```

## 4. Service layer

### 4a. CRITICAL — Sell-fill safety hardening

Current bug: `OTCService.BuyOffer` mutates `holding.PublicQuantity` outside a TX → race lets concurrent buyers both pass the check.

**Fix:** wrap entire flow in `db.Transaction` with `SELECT FOR UPDATE` on holding:

```go
err := s.db.Transaction(func(tx *gorm.DB) error {
    tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sellerHolding, offerID)
    if sellerHolding.PublicQuantity < quantity { return ErrOTCInsufficientPublicQuantity }
    sellerHolding.Quantity -= quantity
    sellerHolding.PublicQuantity -= quantity
    return tx.Save(&sellerHolding).Error
})
// Then money saga OUTSIDE the TX with compensation on failure.
```

### 4b. New service: `OTCStockService`

File: `stock-service/internal/service/otc_stock_service.go`

```go
type OTCStockService struct {
    db, holdingRepo, holdingReservSvc, buyOfferRepo, listingRepo,
    accountClient, nameResolver, producer, sagaRepo
}

func (s *OTCStockService) CreateSellOffer(ctx, in) (*model.Holding, error)
func (s *OTCStockService) CreateBuyOffer(ctx, in) (*model.OTCStockBuyOffer, error)
func (s *OTCStockService) CancelSellOffer(ctx, holdingID, ownerType, ownerID) error
func (s *OTCStockService) CancelBuyOffer(ctx, offerID, ownerType, ownerID) error
func (s *OTCStockService) FillSellOffer(ctx, in) (*FillResult, error)  // someone buys
func (s *OTCStockService) FillBuyOffer(ctx, in) (*FillResult, error)   // someone sells
func (s *OTCStockService) ListMyListings(ctx, ownerType, ownerID, page, pageSize) (*MyListingsResult, error)
```

### 4c. Buy-offer create (pseudocode)

```go
amount := quantity * pricePerUnit
err := s.db.Transaction(func(tx) error {
    resOrderID := buyOfferRepo.AllocateReservationOrderID(tx)
    offer := &OTCStockBuyOffer{Status: Active, RemainingQuantity: qty, ReservedAmount: amount,
        AccountReservationOrderID: resOrderID, ...}
    return tx.Save(offer)
})
// AFTER commit:
_, err = accountClient.ReserveFunds(ctx, accountID, resOrderID, amount, ccy, "create-buy-offer-"+id)
// On reserve failure → cancel the offer row. On crash → saga_recovery reconciles.
```

### 4d. Fill buy-offer (someone sells) — NEW saga

Steps: `lock_buy_offer` (SELECT FOR UPDATE, decrement remaining, save) → `decrement_seller_holding` (TX) → `settle_buyer_reservation` (accountClient.PartialSettleReservation) → `credit_seller` → `upsert_buyer_holding`. Each step has Backward for compensation.

**RISK:** `settleSeq` for `PartialSettleReservation` requires unique ID. Use `otc_stock_buy_offer_settlement` table or `hash(sagaID+offer.id+qty)` deterministically.

### 4e. Cancel buy-offer

```go
err := s.db.Transaction(func(tx) error {
    o := buyOfferRepo.LockByID(tx, offerID)
    if o.Status != Active { return ErrNotActive }
    if !ownerMatch(o, caller) { return ErrOwnership }
    o.Status = Cancelled
    return tx.Save(o)
})
// After commit: accountClient.ReleaseReservation(resOrderID, ...)
// Saga_recovery: scans cancelled-with-active-reservation pairs.
```

## 5. gRPC RPC

File: `contract/proto/stock/stock.proto`

```proto
service OTCStockMarketGRPCService {
  rpc ListOTCStocks(ListOTCStocksRequest) returns (ListOTCStocksResponse);
  rpc ListMyOTCStocks(ListMyOTCStocksRequest) returns (ListMyOTCStocksResponse);
  rpc CreateOTCStockOffer(CreateOTCStockOfferRequest) returns (OTCStockOffer);
  rpc BuyOTCStockOffer(BuyOTCStockOfferRequest) returns (OTCStockFillResult);
  rpc SellOTCStockOffer(SellOTCStockOfferRequest) returns (OTCStockFillResult);
  rpc CancelOTCStockOffer(CancelOTCStockOfferRequest) returns (CancelOTCStockOfferResponse);
}

message OTCStockOffer {
  string kind          = 1;   // "local" | "remote"
  string bank_code     = 2;
  string direction     = 3;   // "sell" | "buy"
  uint64 id            = 4;
  uint64 owner_id      = 5;
  string owner_name    = 6;
  uint64 account_id    = 7;
  string ticker        = 8;
  string name          = 9;
  int64  quantity      = 10;
  string price_per_unit = 11;
  string currency      = 12;
  string status        = 13;
  string created_at    = 14;
}

message ListOTCStocksRequest { string direction = 1; string ticker = 2; string kind = 3; string bank_code = 4; int32 page = 5; int32 page_size = 6; }
message ListOTCStocksResponse { repeated OTCStockOffer offers = 1; int64 total_count = 2; int32 peers_total = 3; int32 peers_reached = 4; bool partial = 5; int64 last_refresh_unix = 6; }

message ListMyOTCStocksRequest { uint64 user_id = 1; string system_type = 2; string direction = 3; string status = 4; int32 page = 5; int32 page_size = 6; }
message ListMyOTCStocksResponse { repeated OTCStockOffer offers = 1; int64 total_count = 2; }

message CreateOTCStockOfferRequest { uint64 user_id = 1; string system_type = 2; string direction = 3; uint64 holding_id = 4; uint64 listing_id = 5; int64 quantity = 6; string price_per_unit = 7; uint64 buyer_account_id = 8; uint64 acting_employee_id = 9; uint64 on_behalf_of_client_id = 10; }

message BuyOTCStockOfferRequest { uint64 offer_id = 1; uint64 buyer_id = 2; string system_type = 3; int64 quantity = 4; uint64 account_id = 5; uint64 acting_employee_id = 6; uint64 on_behalf_of_client_id = 7; }
message SellOTCStockOfferRequest { uint64 offer_id = 1; uint64 seller_id = 2; string system_type = 3; int64 quantity = 4; uint64 seller_account_id = 5; uint64 holding_id = 6; uint64 acting_employee_id = 7; uint64 on_behalf_of_client_id = 8; }
message CancelOTCStockOfferRequest { uint64 user_id = 1; string system_type = 2; string direction = 3; uint64 id = 4; }
message CancelOTCStockOfferResponse { bool ok = 1; }
message OTCStockFillResult { uint64 offer_id = 1; int64 filled_quantity = 2; string price_per_unit = 3; string total_amount = 4; string commission = 5; }
```

Remove (clean break):
- `OTCGRPCService.BuyOffer` → `BuyOTCStockOffer`
- `OTCGRPCService.ListOffers` / `ListUnifiedOffers` → `ListOTCStocks`
- `PortfolioGRPCService.MakePublic` → `CreateOTCStockOffer(direction=sell)`

## 6. Cache shape

`stock-service/internal/otccache/cache.go` — extend `Offer`:

```go
type Offer struct {
    Kind, BankCode string
    Direction      string  // NEW: "sell" | "buy"
    ID             uint64
    OwnerID        string  // promote to string
    OwnerName      string
    AccountID      uint64
    Status         string
    SecurityType, Ticker, Name string
    Quantity       int64
    PricePerUnit, Currency string
    CreatedAt      string
}
```

`fetchLocal()` returns BOTH sells (from holdings) and buys (from buyOfferRepo.ListActive). `fetchPeer()` always stamps `Direction:"sell"`.

## 7. New REST routes

```go
// /me (AnyAuth + bankIfEmp):
me.GET("/otc/stocks", bankIfEmp, h.OTCStock.ListMyOTCStocks)
me.POST("/otc/stocks", bankIfEmp, h.OTCStock.CreateOTCStockOffer)
me.DELETE("/otc/stocks/:id", bankIfEmp, h.OTCStock.CancelOTCStockOffer)

// Marketplace (AnyAuth):
otcStocks.GET("", h.OTCStock.ListOTCStocks)

// Trading (AnyAuth + perms):
otcStocksTrade.POST("/:id/buy",  h.OTCStock.BuyOTCStockOffer)
otcStocksTrade.POST("/:id/sell", h.OTCStock.SellOTCStockOffer)

// Employee on-behalf (protected + RequirePermission):
otcStocksOnBehalf.POST("/:id/buy-on-behalf",  h.OTCStock.BuyOTCStockOfferOnBehalf)
otcStocksOnBehalf.POST("/:id/sell-on-behalf", h.OTCStock.SellOTCStockOfferOnBehalf)
```

Body `POST /me/otc/stocks`: `{direction, holding_id (sell), listing_id (buy), quantity, price_per_unit (buy), buyer_account_id (buy)}`.

Routes to DELETE: `/me/portfolio/:id/make-public`, `/otc/offers` (GET), `/otc/offers/:id/buy`, `/otc/offers/:id/buy-on-behalf`.

## 8. Gateway handler

File: `api-gateway/internal/handler/otc_stock_handler.go` (new) — full method set matching the RPCs. Delete `MakePublic`, `ListOTCOffers`, `BuyOTCOffer`, `BuyOTCOfferOnBehalf` from `portfolio_handler.go`.

## 9. Kafka topics (new)

```go
TopicOTCStockSellOfferCreated   = "stock.otc-sell-offer-created"
TopicOTCStockSellOfferCancelled = "stock.otc-sell-offer-cancelled"
TopicOTCStockSellOfferFilled    = "stock.otc-sell-offer-filled"
TopicOTCStockBuyOfferCreated    = "stock.otc-buy-offer-created"
TopicOTCStockBuyOfferCancelled  = "stock.otc-buy-offer-cancelled"
TopicOTCStockBuyOfferFilled     = "stock.otc-buy-offer-filled"
```

Wire into `EnsureTopics`.

## 10. Sentinel errors

```go
ErrOTCBuyOfferNotFound          = svcerr.New(codes.NotFound, "OTC buy offer not found")
ErrOTCBuyOfferNotActive         = svcerr.New(codes.FailedPrecondition, "OTC buy offer not active")
ErrOTCBuyOfferOwnership         = svcerr.New(codes.PermissionDenied, "buy offer does not belong to user")
ErrOTCInsufficientRemainingQty  = svcerr.New(codes.FailedPrecondition, "insufficient remaining quantity on buy offer")
ErrNoActiveSellOffer            = svcerr.New(codes.FailedPrecondition, "holding has no active sell offer to cancel")
ErrOTCDirectionRequired         = svcerr.New(codes.InvalidArgument, "direction must be sell or buy")
ErrOTCCurrencyMismatch          = svcerr.New(codes.FailedPrecondition, "listing currency must match buyer account currency")
```

## 11. Tests

**Unit (service):** TestCreateSellOffer_*, TestCreateBuyOffer_ReservesCash, TestCreateBuyOffer_RollsBackOnReserveFailure, TestFillSellOffer_ConcurrentBuyers_OnlyOneWins, TestFillBuyOffer_*, TestCancelBuyOffer_ReleasesReservedCash, TestListMyOTCStocks_ReturnsBothDirections, TestSagaRecovery_OrphanBuyOffer_GetsCancelled.

**Handler:** each RPC method (success, InvalidArgument, PermissionDenied, NotFound).

**Gateway:** validation rejections, ownership (403), success forwarding.

**Integration** — `test-app/workflows/otc_stocks_test.go`: full marketplace flow including concurrent fills, employee on-behalf, cancel paths, both directions visible in GET.

## 12. Docs

- `docs/Specification.md` §17, §18, §19, §20, §21.
- `docs/api/REST_API_v3.md` — replace "OTC Offers" with "OTC Stocks".
- Swagger annotations + `make swagger`.

## 13. RISK summary

- **Race — sell-offer fill:** wrap in TX + SELECT FOR UPDATE (§4a).
- **Orphan — buy-offer create:** saga_log + startup reconciler.
- **Orphan — buy-offer cancel:** saga_recovery scans cancelled-with-reservation.
- **Semantics — MakePublic accumulative:** document in REST_API_v3.md.
- **Concurrency — PublicQuantity ≠ ReservedQuantity:** add `Holding.OTCSafeAvailable()` and use in placement.
- **Compensation — share restore after delete:** never delete in fill-saga step 1; cleanup cron handles zero rows.
- **Idempotency — settleSeq:** introduce `otc_stock_buy_offer_settlement` table with serial PK.
- **Cross-bank scope:** peers don't see local buy offers (intentional). Document in spec.
- **Employee on-behalf:** holding ownership check must be against `on_behalf_of_client_id`, not employee. Mirror existing BuyOTCOfferOnBehalf pattern.

## Critical files

- `stock-service/internal/service/otc_service.go`
- `stock-service/internal/service/holding_reservation_service.go`
- `stock-service/internal/otccache/cache.go`
- `api-gateway/internal/router/router_v3.go`
- `contract/proto/stock/stock.proto`
