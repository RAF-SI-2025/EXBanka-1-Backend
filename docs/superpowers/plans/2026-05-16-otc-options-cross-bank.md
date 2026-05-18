# Plan: Cross-Bank OPTION OFFER Discovery

## Scope boundary

Cross-bank discovery of open OPTION offers. Delivers: (1) peer endpoint `GET /api/v3/public-option-offers`, (2) sibling cache/refresher, (3) gRPC `ListUnifiedOptionOffers`, (4) docs. Local data model and `/api/v3/otc/options` HTTP route belong to the options marketplace plan; this only feeds the remote half.

## 1. Wire format — `GET /api/v3/public-option-offers`

Mirrors `/public-stock`: top-level `{"offers": [...]}`. Each row is an SI-TX `OtcOffer` shape PLUS an `offerId` so the discoverer can drive a follow-up `POST /api/v3/me/peer-otc/negotiations` against it.

```json
{
  "offers": [
    {
      "offerId":        { "routingNumber": 111, "id": "42" },
      "stock":          { "ticker": "AAPL" },
      "settlementDate": "2026-12-31T00:00:00Z",
      "pricePerUnit":   { "amount": "180.50", "currency": "USD" },
      "premium":        { "amount": "700",    "currency": "USD" },
      "amount":         50,
      "sellerId":       { "routingNumber": 111, "id": "client-7" },
      "direction":      "sell_initiated",
      "createdAt":      "2026-05-10T14:00:00Z",
      "lastModifiedBy": { "routingNumber": 111, "id": "client-7" }
    }
  ]
}
```

Decisions:
- `direction` carries `sell_initiated|buy_initiated`.
- `sellerId` is prefixed form (`client-<N>`, `employee-<N>`, `bank`).
- `pricePerUnit`/`premium` use `MonetaryValue { amount, currency }` (spec-standard) so the body can be reposted to `POST /negotiations`.
- `buyerId` OMITTED — open offers have no counterparty.

Auth: `PeerAuthMiddleware` (X-Api-Key required, HMAC optional). No pagination on the leaf — bounded set, 5 s refresh cycle.

## 2. Proto changes

`contract/proto/stock/stock.proto`:

```proto
service OTCGRPCService {
  // ... existing ...
  rpc ListUnifiedOptionOffers(ListUnifiedOptionOffersRequest) returns (ListUnifiedOptionOffersResponse);
}

message UnifiedOptionOffer {
  string kind          = 1;  // "local" | "remote"
  string bank_code     = 2;
  int64  routing_number = 3;
  string offer_id      = 4;  // local: strconv(uint64); remote: foreign id
  string seller_id     = 5;  // "client-<N>" | "employee-<N>" | "bank"
  string seller_name   = 6;  // local-only
  string direction     = 7;
  string ticker        = 8;
  int64  amount        = 9;
  string strike_price  = 10;
  string strike_currency = 11;
  string premium       = 12;
  string premium_currency = 13;
  string settlement_date = 14;
  string created_at    = 15;
}

message ListUnifiedOptionOffersRequest { string ticker = 1; string kind = 2; string bank_code = 3; string direction = 4; int32 page = 5; int32 page_size = 6; }
message ListUnifiedOptionOffersResponse { repeated UnifiedOptionOffer offers = 1; int64 total_count = 2; int32 peers_total = 3; int32 peers_reached = 4; bool partial = 5; int64 last_refresh_unix = 6; }

service PeerOTCService {
  // ... existing ...
  rpc GetPublicOptionOffers(GetPublicOptionOffersRequest) returns (GetPublicOptionOffersResponse);
}

message GetPublicOptionOffersRequest { string peer_bank_code = 1; }

message PeerPublicOptionOffer {
  PeerForeignBankId offer_id = 1;
  string ticker         = 2;
  int64  amount         = 3;
  string strike_price   = 4;
  string strike_currency = 5;
  string premium        = 6;
  string premium_currency = 7;
  string settlement_date = 8;
  PeerForeignBankId seller_id = 9;
  string direction      = 10;
  string created_at     = 11;
  PeerForeignBankId last_modified_by = 12;
}

message GetPublicOptionOffersResponse { repeated PeerPublicOptionOffer offers = 1; }
```

## 3. Cache layer

Add sibling types in NEW files in same package, don't mutate `Offer/Cache/Refresher`.

`stock-service/internal/otccache/option_cache.go`:

```go
type OptionOffer struct {
    Kind, BankCode string
    RoutingNumber  int64
    OfferID, SellerID, SellerName string
    Direction, Ticker string
    Amount         int64
    StrikePrice, StrikeCurrency string
    Premium, PremiumCurrency    string
    SettlementDate, CreatedAt   string
}

type OptionSnapshot struct {
    Offers []OptionOffer
    LastRefresh time.Time
    PeersTotal, PeersReached int
}

type OptionOfferLister interface {
    ListOpenOffers(ctx context.Context) ([]model.OTCOffer, error)
}

type OptionCache struct{ /* same Get/set/mu pattern as Cache */ }
```

`stock-service/internal/otccache/option_refresher.go` — structurally identical to `Refresher`:
- `r.otc.ListOpenOffers(ctx)` for local
- `{peer.base_url}/public-option-offers` (leaf only) for remote
- Decodes new SI-TX response type `PublicOptionOffersResponse` in `contract/sitx/otc_types.go`
- Same 5 s `httpClient.Timeout`, 8 s `cycleCtx`, WaitGroup fan-out, partial-failure accounting

### Mapping in fetchLocal:
- `OfferID = strconv.FormatUint(o.ID, 10)`
- `SellerID` = SI-TX-prefixed initiator ID (`client-<N>`, `employee-<N>`, `bank`)
- `StrikePrice = o.StrikePrice.String()`
- **RISK [currency]:** `OTCOffer` has no Currency column. Join with `listings`/`stocks` repo to resolve currency by `stock_id`; fallback `"USD"` with log.warn.
- `SettlementDate/CreatedAt` use RFC3339 UTC.

**Open-offer filter:** local rows must satisfy `status IN ('open') AND counterparty_owner_id IS NULL`. The options-marketplace plan owns the exact column shape — these clauses must be enforced by `ListOpenOffers`.

## 4. Peer endpoint handler

`stock-service/internal/handler/peer_otc_grpc_handler.go` — add:

```go
func (h *PeerOTCGRPCHandler) GetPublicOptionOffers(ctx, req) (*pb.GetPublicOptionOffersResponse, error) {
    if h.otcOffers == nil { return nil, status.Error(codes.Unimplemented, "otc offers reader not wired") }
    rows, err := h.otcOffers.ListOpenOffers(ctx)
    if err != nil { return nil, status.Errorf(codes.Internal, "...") }
    out := make([]*pb.PeerPublicOptionOffer, 0, len(rows))
    for i := range rows {
        o := &rows[i]
        // Optional privacy: drop o.Private==true rows not addressed to req.GetPeerBankCode().
        sellerID, currency := h.composeSellerAndCurrency(o)
        out = append(out, &pb.PeerPublicOptionOffer{
            OfferId:         &pb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: strconv.FormatUint(o.ID, 10)},
            Ticker:          o.Ticker,
            Amount:          o.Quantity.IntPart(),
            StrikePrice:     o.StrikePrice.String(),
            StrikeCurrency:  currency,
            Premium:         o.Premium.String(),
            PremiumCurrency: currency,
            SettlementDate:  o.SettlementDate.UTC().Format(time.RFC3339),
            SellerId:        &pb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: sellerID},
            Direction:       o.Direction,
            CreatedAt:       o.CreatedAt.UTC().Format(time.RFC3339),
            LastModifiedBy:  &pb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: lastModBy(o)},
        })
    }
    return &pb.GetPublicOptionOffersResponse{Offers: out}, nil
}
```

Add interface `OTCOfferReader { ListOpenOffers(ctx) ([]model.OTCOffer, error) }` on the handler, wired in `cmd/main.go` to either repo or service.

**RISK [Quantity precision]:** `OTCOffer.Quantity` is decimal but wire is int64. Use `IntPart()`, log warning if non-integer.

## 5. Gateway HTTP handler

`api-gateway/internal/handler/peer_otc_grpc_handler.go`:

```go
func (h *PeerOTCHandler) GetPublicOptionOffers(c *gin.Context) {
    pbCode, _ := c.Get("peer_bank_code")
    resp, err := h.client.GetPublicOptionOffers(c.Request.Context(),
        &stockpb.GetPublicOptionOffersRequest{PeerBankCode: peerCtxString(pbCode)})
    if err != nil { handleGRPCError(c, err); return }
    out := make([]gin.H, 0, len(resp.GetOffers()))
    for _, o := range resp.GetOffers() {
        out = append(out, gin.H{
            "offerId":        gin.H{"routingNumber": o.GetOfferId().GetRoutingNumber(), "id": o.GetOfferId().GetId()},
            "stock":          gin.H{"ticker": o.GetTicker()},
            "settlementDate": o.GetSettlementDate(),
            "pricePerUnit":   gin.H{"amount": o.GetStrikePrice(), "currency": o.GetStrikeCurrency()},
            "premium":        gin.H{"amount": o.GetPremium(), "currency": o.GetPremiumCurrency()},
            "amount":         o.GetAmount(),
            "sellerId":       gin.H{"routingNumber": o.GetSellerId().GetRoutingNumber(), "id": o.GetSellerId().GetId()},
            "direction":      o.GetDirection(),
            "createdAt":      o.GetCreatedAt(),
            "lastModifiedBy": gin.H{"routingNumber": o.GetLastModifiedBy().GetRoutingNumber(), "id": o.GetLastModifiedBy().GetId()},
        })
    }
    c.JSON(http.StatusOK, gin.H{"offers": out})
}
```

Router wiring (`router_v3.go`, peer group around line 217):
```go
peer.GET("/public-option-offers", h.PeerOTC.GetPublicOptionOffers)
```

Add Swagger annotations.

## 6. `ListUnifiedOptionOffers` gRPC handler

`stock-service/internal/handler/otc_handler.go` — add method on `OTCHandler` with `optionCache *otccache.OptionCache` field. Filter by ticker/kind/bank_code/direction, paginate, set `partial = PeersTotal > PeersReached`. Same shape as `ListUnifiedOffers`.

## 7. Wiring (cmd/main.go)

```go
optionOfferCache := otccache.NewOptionCache()
optionRefresher := otccache.NewOptionRefresher(
    optionOfferCache, otcOfferSvc, peerBankAdminClient, cfg.OwnBankCode, 5*time.Second,
)
go optionRefresher.Run(ctx)

pb.RegisterOTCGRPCServiceServer(s,
    handler.NewOTCHandlerWithCache(otcSvc, otcOfferCache).WithOptionCache(optionOfferCache))

peerOtcHandler = peerOtcHandler.WithOTCOfferReader(otcOfferRepo)
pb.RegisterPeerOTCServiceServer(s, peerOtcHandler)
```

No new Kafka topics.

## 8. Tests

**Unit `option_cache_test.go`:** Get returns copy, empty snapshot.

**Unit `option_refresher_test.go`:** mirror existing `refresher_test.go`:
- NewOptionRefresher field wiring
- FetchLocal mapping (sell_initiated + buy_initiated)
- FetchLocal_ErrorPropagates
- Refresh_LocalOnly_NoPeers, _LocalFetchFails, _PeerListFails
- FetchPeer_HappyPath (httptest server, asserts X-Api-Key, path ends `/public-option-offers`, mapping kind=remote)
- FetchPeer_InactivePeer, ResolveError, BadStatusCode, BadJSON, NilFullPeer
- Refresh_WithReachablePeer
- Refresh_PartialFailure_PeerTotalCorrect (2 peers, 1 returns 500)
- Run_StopsOnContextCancel

**Unit `otc_handler_unified_option_test.go`:** filters, pagination, empty cache, InvalidArgument validation, `partial=true` when peers_total > peers_reached.

**Unit `peer_otc_grpc_handler_options_test.go`:** Unimplemented when reader nil, happy path (PENDING vs ACCEPTED filtering), private filtering, direction passes through, currency fallback.

**Unit `peer_otc_handler_options_test.go` (gateway):** happy path JSON shape, gRPC error mapping, missing peer_bank_code ctx.

**Integration `test-app/workflows/peer_public_option_offers_test.go`:**
- Two-bank stack
- Client A creates open option offer; B hits `/public-option-offers` against A, expects 1 row
- B's cache ticks after ~6 s, then `GET /api/v3/otc/options?kind=remote` on B surfaces it
- Partial failure: stop A → B's call returns `partial=true`, no 5xx
- Auth negative: missing X-Api-Key → 401

## 9. Docs

- `docs/api/REST_API_v3.md` — new subsection `### GET /api/v3/public-option-offers` after the `/public-stock` one
- `docs/Specification.md`:
  - §27 peer routes table — add row for `GET /api/v3/public-option-offers`
  - §11 gRPC services — add `ListUnifiedOptionOffers` to OTCGRPCService and `GetPublicOptionOffers` to PeerOTCService
  - §17 peer routes table — add row
- `make swagger` after Swagger annotations

## 10. RISK summary

- **Currency on OTCOffer:** model has no currency column. Join with listings at fetch time; fallback `"USD"` with log. Long-term: coordinate with options-marketplace plan to add the column.
- **Quantity precision:** decimal → int64 via IntPart. Options plan should enforce integers at create time.
- **Seller ID format:** MUST emit `client-<N>` not bare `<N>`.
- **Partition tolerance:** 5 s timeout per peer, 8 s cycle ctx, goroutine isolates per-peer hangs. Mirror existing stock-cache test patterns.
- **Open-offer definition drift:** both `ListOpenOffers` and `GetPublicOptionOffers` must agree on the WHERE clause. Document in §27.
- **Cache staleness vs accept:** between cached discovery (5 s) and accept (fresh HTTP), offer may be gone. Accept already 404s gracefully; document eventually-consistent semantics in §27.
- **No negotiation row at discovery time:** intentional. The bite flow goes through existing `POST /me/peer-otc/negotiations`; discoverer reuses `bank_code` + `sellerId`. No `negotiationId` in discovery payload.

## Critical files

- `contract/proto/stock/stock.proto`
- `stock-service/internal/otccache/option_cache.go` (NEW)
- `stock-service/internal/otccache/option_refresher.go` (NEW)
- `stock-service/internal/handler/peer_otc_grpc_handler.go`
- `api-gateway/internal/router/router_v3.go`

Supporting: `api-gateway/internal/handler/peer_otc_grpc_handler.go`, `stock-service/internal/handler/otc_handler.go`, `stock-service/cmd/main.go`, `contract/sitx/otc_types.go`, `docs/api/REST_API_v3.md`, `docs/Specification.md`.
