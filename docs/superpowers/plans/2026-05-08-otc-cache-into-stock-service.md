# Move OTC Offer Cache from api-gateway into stock-service

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Relocate the unified-OTC-offer cache (local + cross-bank, refreshed every 5 s) out of api-gateway and into stock-service, and expose it as a new `OTCGRPCService.ListUnifiedOffers` RPC. The api-gateway becomes a thin pass-through.

**Architecture:** stock-service is the single source of truth for OTC stock data. It owns the in-memory cache + 5 s background refresher. The refresher pulls local offers in-process from `OTCService.ListOffers` and pulls remote offers via outbound HTTP to peer banks' `GET /public-stock` (PeerAuth via X-Api-Key). Peer URLs and plaintext API tokens are resolved from transaction-service via the existing `PeerBankAdminService.ResolvePeerByBankCode` gRPC. The api-gateway's `GET /api/v3/otc/offers` handler shrinks to a gRPC call into the new RPC; the existing JSON shape is preserved.

**Tech Stack:** Go 1.x · gRPC + protobuf · stock-service GORM repos · transaction-service `PeerBankAdminService` (existing) · `net/http` for peer fan-out

---

## File Map

**Add to stock-service:**
- `stock-service/internal/otccache/cache.go` — Cache + Refresher (moved from api-gateway, adapted to call `OTCService.ListOffers` in-process for the local source)
- `stock-service/internal/otccache/cache_test.go` — Get-returns-copy + empty-snapshot tests (moved)
- New tests in `stock-service/internal/handler/otc_handler_unified_test.go`

**Modify in stock-service:**
- `stock-service/internal/handler/otc_handler.go` — Add `ListUnifiedOffers` method; add cache field on the handler
- `stock-service/cmd/main.go` — Dial `PeerBankAdminServiceClient` from existing transactionConn; instantiate cache + refresher; pass cache to `NewOTCHandler`

**Modify in contract:**
- `contract/proto/stock/stock.proto` — Add `UnifiedOTCOffer`, `ListUnifiedOTCOffersRequest`, `ListUnifiedOTCOffersResponse` messages and `ListUnifiedOffers` RPC

**Modify in api-gateway:**
- `api-gateway/internal/handler/portfolio_handler.go` — Replace cache-driven `ListOTCOffers` with a thin gRPC pass-through to `OTCGRPCService.ListUnifiedOffers`; drop `otccache` import
- `api-gateway/internal/router/handlers.go` — Remove `OTCCache` field from `Deps`; drop the `otccache.Cache` argument from `NewPortfolioHandler` call
- `api-gateway/cmd/main.go` — Remove `otccache.New()`, `otccache.NewRefresher(...)`, the `go otcRefresher.Run(ctx)` goroutine, and the `OTCCache` field assignment in Deps
- `api-gateway/internal/handler/portfolio_handler_test.go` — Drop `otccache.New()` arg from all call sites; rewrite `TestPortfolio_ListOTCOffers_Default` to assert the gRPC pass-through

**Delete from api-gateway:**
- `api-gateway/internal/otccache/cache.go`
- `api-gateway/internal/otccache/cache_test.go`

**Modify in docs:**
- `Specification.md` § 11 (gRPC service definitions) — Document new RPC on `OTCGRPCService`
- `Specification.md` § 17 / `docs/api/REST_API_v3.md` — No REST shape change; keep existing § 28 doc

**No infra changes:** stock-service already dials `cfg.TransactionGRPCAddr` for `PeerTxServiceClient` (`stock-service/cmd/main.go:288-296`). Just add a second stub from the same conn. No new env vars, no docker-compose changes, no DB migrations, no Kafka topics.

---

## Task 1: Add proto messages and RPC

**Files:**
- Modify: `contract/proto/stock/stock.proto:667-694`

- [ ] **Step 1: Edit the proto**

In `contract/proto/stock/stock.proto`, find the `OTCGRPCService` block at line 667 and replace it (along with the messages immediately following) with:

```proto
service OTCGRPCService {
  rpc ListOffers(ListOTCOffersRequest) returns (ListOTCOffersResponse);
  rpc BuyOffer(BuyOTCOfferRequest) returns (OTCTransaction);
  // Unified view: local offers from this bank's holdings + remote offers
  // pulled from every active peer bank's GET /public-stock. Backed by
  // an in-process cache refreshed every ~5 s by stock-service.
  rpc ListUnifiedOffers(ListUnifiedOTCOffersRequest) returns (ListUnifiedOTCOffersResponse);
}

message OTCOffer {
  uint64 id = 1;
  uint64 seller_id = 2;
  string seller_name = 3;
  string security_type = 4;
  string ticker = 5;
  string name = 6;
  int64 quantity = 7;
  string price_per_unit = 8;
  string created_at = 9;
}

message ListOTCOffersRequest {
  string security_type = 1;
  string ticker = 2;
  int32 page = 3;
  int32 page_size = 4;
}

message ListOTCOffersResponse {
  repeated OTCOffer offers = 1;
  int64 total_count = 2;
}

// UnifiedOTCOffer is one entry in the cross-bank-aggregated OTC view.
// `kind` discriminates the buy flow:
//   - "local"  → buy via POST /api/v3/otc/offers/{id}/buy
//   - "remote" → buy via POST /api/v3/me/peer-otc/negotiations using
//                bank_code as seller_bank_code and owner_id as seller_id.
// Local-only fields (id, seller_id, seller_name, name, created_at) are
// zero/empty on remote offers; owner_id is empty on local offers.
message UnifiedOTCOffer {
  string kind = 1;
  string bank_code = 2;

  // Local-only
  uint64 id = 3;
  uint64 seller_id = 4;
  string seller_name = 5;
  string name = 6;
  string created_at = 7;

  // Remote-only. SI-TX owner shape: "0" means bank-owned, "1+" is a
  // client id at the seller bank.
  string owner_id = 8;

  // Common
  string security_type = 9;
  string ticker = 10;
  int64 quantity = 11;
  string price_per_unit = 12;
  string currency = 13;
}

message ListUnifiedOTCOffersRequest {
  string security_type = 1;
  string ticker = 2;
  string kind = 3;       // optional filter: "local" | "remote"
  string bank_code = 4;  // optional filter
  int32 page = 5;
  int32 page_size = 6;
}

message ListUnifiedOTCOffersResponse {
  repeated UnifiedOTCOffer offers = 1;
  int64 total_count = 2;
  int32 peers_total = 3;
  int32 peers_reached = 4;
  bool partial = 5;
  int64 last_refresh_unix = 6;
}
```

- [ ] **Step 2: Regenerate Go bindings**

Run: `make proto`
Expected: `contract/stockpb/stock.pb.go` and `stock_grpc.pb.go` regenerated; build still passes downstream.

- [ ] **Step 3: Verify regenerated stubs compile**

Run: `cd contract && go build ./... && cd ../stock-service && go build ./... && cd ../api-gateway && go build ./...`
Expected: clean exit codes; no missing-symbol errors. (The new RPC has no implementation yet, but that's fine — the generated code compiles independently.)

- [ ] **Step 4: Commit**

```bash
git add contract/proto/stock/stock.proto contract/stockpb/
git commit -m "feat(stockpb): add OTCGRPCService.ListUnifiedOffers RPC"
```

---

## Task 2: Move cache package into stock-service

**Files:**
- Create: `stock-service/internal/otccache/cache.go`
- Create: `stock-service/internal/otccache/cache_test.go`
- (Don't delete the api-gateway copy yet — that happens in Task 7 once the gateway no longer references it.)

- [ ] **Step 1: Write the cache test first**

Create `stock-service/internal/otccache/cache_test.go`:

```go
package otccache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Get must return a defensive copy: callers mutating the returned slice
// cannot corrupt the cache's internal state.
func TestCache_GetReturnsCopy(t *testing.T) {
	c := New()
	c.set(Snapshot{
		Offers:       []Offer{{Kind: "local", Ticker: "BAC"}},
		LastRefresh:  time.Unix(1, 0),
		PeersTotal:   1,
		PeersReached: 1,
	})
	first := c.Get()
	require.Len(t, first.Offers, 1)
	first.Offers[0].Ticker = "MUTATED"

	second := c.Get()
	require.Equal(t, "BAC", second.Offers[0].Ticker)
}

// Pre-first-refresh: empty slice, zero LastRefresh, no panic.
func TestCache_EmptySnapshot(t *testing.T) {
	c := New()
	snap := c.Get()
	require.Empty(t, snap.Offers)
	require.True(t, snap.LastRefresh.IsZero())
	require.Zero(t, snap.PeersTotal)
	require.Zero(t, snap.PeersReached)
}
```

- [ ] **Step 2: Verify test fails (package doesn't exist yet)**

Run: `cd stock-service && go test ./internal/otccache/... -count=1`
Expected: FAIL — `no Go files in .../otccache`

- [ ] **Step 3: Implement the cache package**

Create `stock-service/internal/otccache/cache.go`:

```go
// Package otccache holds the unified OTC-offer cache used by
// OTCGRPCService.ListUnifiedOffers. The cache is rebuilt every refresh
// interval by Refresher: local offers come from OTCService.ListOffers
// in-process, remote offers come from each active peer bank's
// GET /api/v3/public-stock (PeerAuth via X-Api-Key, plaintext token
// resolved through transaction-service's PeerBankAdminService).
package otccache

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// Offer is the unified shape stored in the cache. Local offers carry
// the same fields as OTCService.ListOffers projects (id, seller, name,
// created_at). Remote offers carry bank_code + owner_id so the gateway
// can route purchases through POST /me/peer-otc/negotiations.
type Offer struct {
	Kind     string
	BankCode string

	// Local-only
	ID         uint64
	SellerID   uint64
	SellerName string
	Name       string
	CreatedAt  string

	// Remote-only. SI-TX shape: "0" = bank-owned; "1+" = client id at peer.
	OwnerID string

	// Common
	SecurityType string
	Ticker       string
	Quantity     int64
	PricePerUnit string
	Currency     string
}

type Snapshot struct {
	Offers       []Offer
	LastRefresh  time.Time
	PeersTotal   int
	PeersReached int
}

// OTCLister is the narrow OTCService interface the cache needs for
// fetching local offers. Defined here (not in service/) to keep the
// dependency direction one-way.
type OTCLister interface {
	ListOffers(filter service.OTCFilter) ([]model.Holding, int64, error)
}

// Cache is goroutine-safe. Reads via Get() return a defensive copy.
type Cache struct {
	mu           sync.RWMutex
	offers       []Offer
	lastRefresh  time.Time
	peersTotal   int
	peersReached int
}

func New() *Cache { return &Cache{} }

func (c *Cache) Get() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Offer, len(c.offers))
	copy(out, c.offers)
	return Snapshot{
		Offers:       out,
		LastRefresh:  c.lastRefresh,
		PeersTotal:   c.peersTotal,
		PeersReached: c.peersReached,
	}
}

func (c *Cache) set(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offers = s.Offers
	c.lastRefresh = s.LastRefresh
	c.peersTotal = s.PeersTotal
	c.peersReached = s.PeersReached
}

type Refresher struct {
	cache       *Cache
	otc         OTCLister
	peerAdmin   transactionpb.PeerBankAdminServiceClient
	httpClient  *http.Client
	ownBankCode string
	interval    time.Duration
}

func NewRefresher(
	cache *Cache,
	otc OTCLister,
	peerAdmin transactionpb.PeerBankAdminServiceClient,
	ownBankCode string,
	interval time.Duration,
) *Refresher {
	return &Refresher{
		cache:       cache,
		otc:         otc,
		peerAdmin:   peerAdmin,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		ownBankCode: ownBankCode,
		interval:    interval,
	}
}

// Run blocks until ctx is cancelled. Initial refresh on start, then
// ticks at `interval`. Failure on any single source is logged but
// does not abort the cycle — peers reachable in this tick still appear.
func (r *Refresher) Run(ctx context.Context) {
	r.refresh(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *Refresher) refresh(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var (
		offers       []Offer
		peersTotal   int
		peersReached int
		mu           sync.Mutex
	)

	if local, err := r.fetchLocal(); err == nil {
		offers = append(offers, local...)
	} else {
		log.Printf("otccache: local fetch failed: %v", err)
	}

	peerList, err := r.peerAdmin.ListPeerBanks(cycleCtx, &transactionpb.ListPeerBanksRequest{ActiveOnly: true})
	if err != nil {
		log.Printf("otccache: list peers failed: %v", err)
	} else if peerList != nil {
		var wg sync.WaitGroup
		for _, p := range peerList.GetPeerBanks() {
			peersTotal++
			wg.Add(1)
			go func(peer *transactionpb.PeerBank) {
				defer wg.Done()
				peerOffers, err := r.fetchPeer(cycleCtx, peer)
				if err != nil {
					log.Printf("otccache: peer %s fetch failed: %v", peer.GetBankCode(), err)
					return
				}
				mu.Lock()
				offers = append(offers, peerOffers...)
				peersReached++
				mu.Unlock()
			}(p)
		}
		wg.Wait()
	}

	r.cache.set(Snapshot{
		Offers:       offers,
		LastRefresh:  time.Now().UTC(),
		PeersTotal:   peersTotal,
		PeersReached: peersReached,
	})
}

func (r *Refresher) fetchLocal() ([]Offer, error) {
	// Pull all (page=1, page_size large); the gRPC handler re-paginates.
	holdings, _, err := r.otc.ListOffers(service.OTCFilter{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]Offer, 0, len(holdings))
	for _, h := range holdings {
		out = append(out, Offer{
			Kind:         "local",
			BankCode:     r.ownBankCode,
			ID:           h.ID,
			SellerID:     model.OwnerIDOrZero(h.OwnerID),
			SellerName:   strings.TrimSpace(h.UserFirstName + " " + h.UserLastName),
			SecurityType: h.SecurityType,
			Ticker:       h.Ticker,
			Name:         h.Name,
			Quantity:     h.PublicQuantity,
			PricePerUnit: h.AveragePrice.StringFixed(2),
			CreatedAt:    h.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}
	return out, nil
}

func (r *Refresher) fetchPeer(ctx context.Context, peer *transactionpb.PeerBank) ([]Offer, error) {
	resolveResp, err := r.peerAdmin.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: peer.GetBankCode()})
	if err != nil {
		return nil, err
	}
	full := resolveResp.GetPeerBank()
	if full == nil || !full.GetActive() {
		return nil, nil
	}
	url := strings.TrimRight(full.GetBaseUrl(), "/") + "/public-stock"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", full.GetApiTokenPlaintext())

	httpResp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", httpResp.StatusCode, string(body))
	}
	var resp sitx.PublicStocksResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]Offer, 0, len(resp.Stocks))
	for _, s := range resp.Stocks {
		out = append(out, Offer{
			Kind:         "remote",
			BankCode:     peer.GetBankCode(),
			OwnerID:      s.OwnerID.ID,
			SecurityType: "stock",
			Ticker:       s.Ticker,
			Quantity:     s.Amount,
			PricePerUnit: s.PricePerStock.String(),
			Currency:     s.Currency,
		})
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

Run: `cd stock-service && go test ./internal/otccache/... -count=1 -v`
Expected: `PASS: TestCache_GetReturnsCopy` and `PASS: TestCache_EmptySnapshot`.

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/otccache/
git commit -m "feat(stock-service): add otccache package for unified OTC offer cache"
```

---

## Task 3: Add ListUnifiedOffers gRPC handler in stock-service

**Files:**
- Modify: `stock-service/internal/handler/otc_handler.go`
- Test: `stock-service/internal/handler/otc_handler_unified_test.go`

- [ ] **Step 1: Write the failing test**

Create `stock-service/internal/handler/otc_handler_unified_test.go`:

```go
package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/otccache"
)

func TestOTCHandler_ListUnifiedOffers_FilterAndPaginate(t *testing.T) {
	cache := otccache.New()
	// Seed cache with two offers so we can assert filtering/pagination.
	otccache.SetForTest(cache, otccache.Snapshot{
		Offers: []otccache.Offer{
			{Kind: "local", BankCode: "111", ID: 1, Ticker: "BAC", SecurityType: "stock", Quantity: 3, PricePerUnit: "38.00"},
			{Kind: "remote", BankCode: "333", OwnerID: "1", Ticker: "JNJ", SecurityType: "stock", Quantity: 5, PricePerUnit: "0", Currency: "USD"},
		},
		LastRefresh:  time.Unix(1714345200, 0).UTC(),
		PeersTotal:   2,
		PeersReached: 1,
	})
	h := NewOTCHandlerWithCache(nil, cache)

	resp, err := h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.GetTotalCount())
	require.Equal(t, int32(2), resp.GetPeersTotal())
	require.Equal(t, int32(1), resp.GetPeersReached())
	require.True(t, resp.GetPartial())
	require.Len(t, resp.GetOffers(), 2)

	// Filter by kind=remote.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Kind: "remote", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.GetTotalCount())
	require.Equal(t, "JNJ", resp.GetOffers()[0].GetTicker())

	// Filter by ticker.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Ticker: "BAC", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.GetTotalCount())
	require.Equal(t, "local", resp.GetOffers()[0].GetKind())

	// Pagination: page 2 of size 1.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.GetTotalCount())
	require.Len(t, resp.GetOffers(), 1)
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `cd stock-service && go test ./internal/handler/ -run TestOTCHandler_ListUnifiedOffers -count=1 -v`
Expected: FAIL — `NewOTCHandlerWithCache` undefined; `ListUnifiedOffers` undefined; `otccache.SetForTest` undefined.

- [ ] **Step 3: Add the test seam to the cache package**

Append to `stock-service/internal/otccache/cache.go`:

```go
// SetForTest seeds the cache from outside the package. Test-only — the
// production code path is Refresher.refresh.
func SetForTest(c *Cache, s Snapshot) { c.set(s) }
```

- [ ] **Step 4: Implement the handler**

Edit `stock-service/internal/handler/otc_handler.go`:

Replace the imports block:

```go
import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/otccache"
	"github.com/exbanka/stock-service/internal/service"
)
```

Replace the struct + constructors:

```go
type OTCHandler struct {
	pb.UnimplementedOTCGRPCServiceServer
	otcSvc otcSvcFacade
	cache  *otccache.Cache
}

// NewOTCHandler keeps the prior signature for backwards compatibility
// with cmd/main.go callers that don't yet pass a cache. Pass a nil
// cache to disable the unified-offers RPC (it will return an empty
// list with peers_total=0).
func NewOTCHandler(otcSvc *service.OTCService) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}

// NewOTCHandlerWithCache wires the unified-offers cache. cmd/main.go
// uses this once the cache + refresher are constructed.
func NewOTCHandlerWithCache(otcSvc *service.OTCService, cache *otccache.Cache) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc, cache: cache}
}

func newOTCHandlerForTest(otcSvc otcSvcFacade) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}
```

Append the new RPC method to the file:

```go
// ListUnifiedOffers serves the cached union of local + cross-bank OTC
// offers. Filters and pagination are applied in-memory over the cached
// snapshot. peers_total / peers_reached / partial reflect the most
// recent refresh cycle so the UI can surface "showing 1 of 2 peers".
func (h *OTCHandler) ListUnifiedOffers(ctx context.Context, req *pb.ListUnifiedOTCOffersRequest) (*pb.ListUnifiedOTCOffersResponse, error) {
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 {
		pageSize = 10
	}
	secType := req.GetSecurityType()
	if secType != "" && secType != "stock" && secType != "futures" {
		return nil, status.Error(codes.InvalidArgument, "security_type must be 'stock' or 'futures'")
	}
	kind := req.GetKind()
	if kind != "" && kind != "local" && kind != "remote" {
		return nil, status.Error(codes.InvalidArgument, "kind must be 'local' or 'remote'")
	}
	ticker := strings.ToUpper(req.GetTicker())
	bankFilter := req.GetBankCode()

	if h.cache == nil {
		return &pb.ListUnifiedOTCOffersResponse{}, nil
	}
	snap := h.cache.Get()

	filtered := make([]otccache.Offer, 0, len(snap.Offers))
	for _, o := range snap.Offers {
		if secType != "" && o.SecurityType != secType {
			continue
		}
		if ticker != "" && strings.ToUpper(o.Ticker) != ticker {
			continue
		}
		if kind != "" && o.Kind != kind {
			continue
		}
		if bankFilter != "" && o.BankCode != bankFilter {
			continue
		}
		filtered = append(filtered, o)
	}

	total := int64(len(filtered))
	start := (page - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}

	out := make([]*pb.UnifiedOTCOffer, 0, end-start)
	for _, o := range filtered[start:end] {
		out = append(out, &pb.UnifiedOTCOffer{
			Kind:         o.Kind,
			BankCode:     o.BankCode,
			Id:           o.ID,
			SellerId:     o.SellerID,
			SellerName:   o.SellerName,
			Name:         o.Name,
			CreatedAt:    o.CreatedAt,
			OwnerId:      o.OwnerID,
			SecurityType: o.SecurityType,
			Ticker:       o.Ticker,
			Quantity:     o.Quantity,
			PricePerUnit: o.PricePerUnit,
			Currency:     o.Currency,
		})
	}

	var lastRefreshUnix int64
	if !snap.LastRefresh.IsZero() {
		lastRefreshUnix = snap.LastRefresh.Unix()
	}
	// Suppress unused-var lint when ctx isn't used; ctx is reserved for
	// future cancellation in case we add per-request peer fan-out.
	_ = ctx
	return &pb.ListUnifiedOTCOffersResponse{
		Offers:          out,
		TotalCount:      total,
		PeersTotal:      int32(snap.PeersTotal),
		PeersReached:    int32(snap.PeersReached),
		Partial:         snap.PeersTotal > 0 && snap.PeersReached < snap.PeersTotal,
		LastRefreshUnix: lastRefreshUnix,
	}, nil
}

// _ explicit static check that OTCHandler still implements every
// generated server interface method (caught at compile time).
var _ pb.OTCGRPCServiceServer = (*OTCHandler)(nil)
```

- [ ] **Step 5: Run test, verify pass**

Run: `cd stock-service && go test ./internal/handler/ -run TestOTCHandler_ListUnifiedOffers -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Run the broader stock-service test suite to confirm nothing else broke**

Run: `cd stock-service && go test ./... -count=1`
Expected: all packages PASS or `[no test files]`.

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/handler/otc_handler.go stock-service/internal/handler/otc_handler_unified_test.go stock-service/internal/otccache/cache.go
git commit -m "feat(stock-service): add OTCGRPCService.ListUnifiedOffers RPC backed by cache"
```

---

## Task 4: Wire cache + refresher in stock-service main.go

**Files:**
- Modify: `stock-service/cmd/main.go:280-300, 470-475, 670-680`

- [ ] **Step 1: Add the PeerBankAdminServiceClient stub**

In `stock-service/cmd/main.go`, find the block around line 288–296 that currently reads:

```go
transactionConn, err := grpc.NewClient(cfg.TransactionGRPCAddr,
    grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
    log.Fatalf("failed to dial transaction-service: %v", err)
}
defer transactionConn.Close()
peerTxClient := transactionpb.NewPeerTxServiceClient(transactionConn)
```

Add the line below `peerTxClient := ...`:

```go
peerBankAdminClient := transactionpb.NewPeerBankAdminServiceClient(transactionConn)
```

- [ ] **Step 2: Add the otccache import**

In the import block of `stock-service/cmd/main.go`, add:

```go
"github.com/exbanka/stock-service/internal/otccache"
```

- [ ] **Step 3: Construct the cache + refresher and start the goroutine**

After `otcSvc := service.NewOTCService(...)` (line ~472), add:

```go
// Unified OTC offer cache (local + cross-bank). Refresher rebuilds
// every 5 s by pulling local offers in-process from otcSvc and fanning
// out HTTP GETs to active peer banks' /public-stock (PeerAuth). The
// gRPC method OTCGRPCService.ListUnifiedOffers serves the cached view.
otcOfferCache := otccache.New()
otcRefresher := otccache.NewRefresher(otcOfferCache, otcSvc, peerBankAdminClient, cfg.OwnBankCode, 5*time.Second)
go otcRefresher.Run(ctx)
```

(The `ctx` here is the same one already passed to other goroutines in this function — confirm by `grep -n "ctx" stock-service/cmd/main.go | head` and use whichever long-lived background context is in scope. If none exists, add `ctx, cancel := context.WithCancel(context.Background()); defer cancel()` immediately above this block.)

- [ ] **Step 4: Pass the cache into the OTC handler**

Find the line that currently registers OTCGRPCService (around line 674):

```go
pb.RegisterOTCGRPCServiceServer(s, handler.NewOTCHandler(otcSvc))
```

Replace with:

```go
pb.RegisterOTCGRPCServiceServer(s, handler.NewOTCHandlerWithCache(otcSvc, otcOfferCache))
```

- [ ] **Step 5: Build stock-service**

Run: `cd stock-service && go build ./...`
Expected: clean exit.

- [ ] **Step 6: Run stock-service tests**

Run: `cd stock-service && go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add stock-service/cmd/main.go
git commit -m "feat(stock-service): wire OTC cache refresher in main.go"
```

---

## Task 5: Replace api-gateway handler with gRPC pass-through

**Files:**
- Modify: `api-gateway/internal/handler/portfolio_handler.go`

- [ ] **Step 1: Drop the otccache import**

In `api-gateway/internal/handler/portfolio_handler.go`, change:

```go
import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/exbanka/api-gateway/internal/otccache"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)
```

To (drop `strings` if it ends up unused after this task; keep `otccache` removed):

```go
import (
	"net/http"
	"strconv"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)
```

- [ ] **Step 2: Drop the cache field and revert constructor**

Replace the struct + constructor block:

```go
type PortfolioHandler struct {
	portfolioClient stockpb.PortfolioGRPCServiceClient
	otcClient       stockpb.OTCGRPCServiceClient
	accountClient   accountpb.AccountServiceClient
}

func NewPortfolioHandler(
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	accountClient accountpb.AccountServiceClient,
) *PortfolioHandler {
	return &PortfolioHandler{
		portfolioClient: portfolioClient,
		otcClient:       otcClient,
		accountClient:   accountClient,
	}
}
```

- [ ] **Step 3: Replace ListOTCOffers with the pass-through**

Replace the entire `ListOTCOffers` method with:

```go
// ListOTCOffers serves the unified OTC market view by calling
// stock-service's OTCGRPCService.ListUnifiedOffers, which owns the
// cross-bank discovery cache. The gateway is a thin pass-through:
// query params map 1-to-1 onto the gRPC request, and the response
// is reshaped into the JSON contract documented in REST_API_v3 §28.
func (h *PortfolioHandler) ListOTCOffers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if pageSize < 1 {
		pageSize = 10
	}

	secType := c.Query("security_type")
	if secType != "" {
		if _, err := oneOf("security_type", secType, "stock", "futures"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	kindFilter := c.Query("kind")
	if kindFilter != "" {
		if _, err := oneOf("kind", kindFilter, "local", "remote"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.otcClient.ListUnifiedOffers(c.Request.Context(), &stockpb.ListUnifiedOTCOffersRequest{
		SecurityType: secType,
		Ticker:       c.Query("ticker"),
		Kind:         kindFilter,
		BankCode:     c.Query("bank_code"),
		Page:         int32(page),
		PageSize:     int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	// Project pb.UnifiedOTCOffer to the JSON shape with omitempty
	// semantics matching REST_API_v3 §28.
	offers := make([]gin.H, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		row := gin.H{
			"kind":           o.GetKind(),
			"bank_code":      o.GetBankCode(),
			"security_type":  o.GetSecurityType(),
			"ticker":         o.GetTicker(),
			"quantity":       o.GetQuantity(),
			"price_per_unit": o.GetPricePerUnit(),
		}
		if o.GetKind() == "local" {
			row["id"] = o.GetId()
			row["seller_id"] = o.GetSellerId()
			row["seller_name"] = o.GetSellerName()
			row["name"] = o.GetName()
			row["created_at"] = o.GetCreatedAt()
		} else {
			row["owner_id"] = o.GetOwnerId()
			row["currency"] = o.GetCurrency()
		}
		offers = append(offers, row)
	}

	var lastRefresh string
	if u := resp.GetLastRefreshUnix(); u > 0 {
		lastRefresh = time.Unix(u, 0).UTC().Format("2006-01-02T15:04:05Z")
	}

	c.JSON(http.StatusOK, gin.H{
		"offers":        offers,
		"total_count":   resp.GetTotalCount(),
		"peers_total":   resp.GetPeersTotal(),
		"peers_reached": resp.GetPeersReached(),
		"partial":       resp.GetPartial(),
		"last_refresh":  lastRefresh,
	})
}
```

- [ ] **Step 4: Add the time import**

The new code uses `time.Unix(...)`. Update the imports block:

```go
import (
	"net/http"
	"strconv"
	"time"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)
```

- [ ] **Step 5: Build the gateway**

Run: `cd api-gateway && go build ./...`
Expected: build error: `handlers.go: NewPortfolioHandler ... too many arguments` (next task fixes the caller).

- [ ] **Step 6: Commit (broken build is fine; the next task completes the change)**

```bash
git add api-gateway/internal/handler/portfolio_handler.go
git commit -m "refactor(api-gateway): turn ListOTCOffers into stockpb.ListUnifiedOffers pass-through"
```

---

## Task 6: Drop OTCCache from api-gateway Deps + main wiring

**Files:**
- Modify: `api-gateway/internal/router/handlers.go`
- Modify: `api-gateway/cmd/main.go`

- [ ] **Step 1: Drop OTCCache from Deps + remove the otccache import**

In `api-gateway/internal/router/handlers.go`, remove this import line:

```go
"github.com/exbanka/api-gateway/internal/otccache"
```

Remove the `OTCCache` field from the `Deps` struct (the block that currently reads):

```go
// OTCCache holds the unified (local + cross-bank) OTC offer list
// surfaced by GET /api/v3/otc/offers. Populated by a background
// goroutine in cmd/main.go; the handler reads from it instead of
// hitting stock-service on every request.
OTCCache *otccache.Cache
```

Drop those lines (delete the whole comment + field).

Update the `NewPortfolioHandler` call (currently `handler.NewPortfolioHandler(d.PortfolioClient, d.OTCClient, d.AccountClient, d.OTCCache)`) to drop the last arg:

```go
Portfolio:        handler.NewPortfolioHandler(d.PortfolioClient, d.OTCClient, d.AccountClient),
```

- [ ] **Step 2: Drop cache wiring from main.go**

In `api-gateway/cmd/main.go`, remove the otccache import:

```go
"github.com/exbanka/api-gateway/internal/otccache"
```

Remove the cache instantiation block:

```go
// Unified OTC offer cache (local + cross-bank). The refresher
// rebuilds the cache every 5 s by pulling local offers from
// stock-service and fanning out to active peers' /public-stock.
// GET /api/v3/otc/offers reads from the cache.
otcOfferCache := otccache.New()
otcRefresher := otccache.NewRefresher(otcOfferCache, otcClient, peerBankAdminClient, cfg.OwnBankCode, 5*time.Second)
go otcRefresher.Run(ctx)
```

In the `Deps{...}` struct literal, remove the `OTCCache: otcOfferCache,` line.

- [ ] **Step 3: Build the gateway**

Run: `cd api-gateway && go build ./...`
Expected: clean exit. (If `time` is now unused in main.go, the compiler will say so — keep `time` only if it's used elsewhere in the file. `grep -n "time\." api-gateway/cmd/main.go` to confirm.)

- [ ] **Step 4: Run the gateway tests**

Run: `cd api-gateway && go test ./... -count=1`
Expected: portfolio_handler_test.go fails — call sites still pass `otccache.New()`. Next task fixes them.

- [ ] **Step 5: Commit (broken test is fine; next task fixes)**

```bash
git add api-gateway/internal/router/handlers.go api-gateway/cmd/main.go
git commit -m "refactor(api-gateway): remove OTCCache from Deps + main wiring"
```

---

## Task 7: Update api-gateway tests + delete otccache package

**Files:**
- Modify: `api-gateway/internal/handler/portfolio_handler_test.go`
- Delete: `api-gateway/internal/otccache/cache.go`
- Delete: `api-gateway/internal/otccache/cache_test.go`

- [ ] **Step 1: Drop the otccache import + extra arg from all call sites**

In `api-gateway/internal/handler/portfolio_handler_test.go`:

Remove the import line `"github.com/exbanka/api-gateway/internal/otccache"`.

Strip the last arg from every `handler.NewPortfolioHandler(...)` call. You can do this in a single Python pass:

```python
import re
fp = 'api-gateway/internal/handler/portfolio_handler_test.go'
with open(fp) as f: src = f.read()
new_src = re.sub(r'handler\.NewPortfolioHandler\((.+?), otccache\.New\(\)\)$', r'handler.NewPortfolioHandler(\1)', src, flags=re.MULTILINE)
with open(fp, 'w') as f: f.write(new_src)
```

(Run that snippet from the repo root.)

- [ ] **Step 2: Rewrite TestPortfolio_ListOTCOffers_Default to test the pass-through**

In `api-gateway/internal/handler/portfolio_handler_test.go`, find `TestPortfolio_ListOTCOffers_Default` and replace its body:

```go
func TestPortfolio_ListOTCOffers_Default(t *testing.T) {
	otc := &stubOTCClient{
		listUnifiedFn: func(req *stockpb.ListUnifiedOTCOffersRequest) (*stockpb.ListUnifiedOTCOffersResponse, error) {
			require.Equal(t, int32(1), req.Page)
			require.Equal(t, int32(10), req.PageSize)
			return &stockpb.ListUnifiedOTCOffersResponse{
				Offers: []*stockpb.UnifiedOTCOffer{
					{Kind: "local", BankCode: "111", Id: 7, Ticker: "BAC", SecurityType: "stock", Quantity: 3, PricePerUnit: "38.00"},
				},
				TotalCount:      1,
				PeersTotal:      0,
				PeersReached:    0,
				Partial:         false,
				LastRefreshUnix: 0,
			}, nil
		},
	}
	h := handler.NewPortfolioHandler(&portfolioStub{}, otc, &accountFullStub{})
	r := portfolioRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/otc/offers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"kind":"local"`)
	require.Contains(t, rec.Body.String(), `"ticker":"BAC"`)
}
```

- [ ] **Step 3: Add the listUnifiedFn stub field on stubOTCClient**

Find `stubOTCClient` (in this same file or in a sibling test helper) and add `listUnifiedFn` + a `ListUnifiedOffers` method:

```go
type stubOTCClient struct {
	listFn         func(*stockpb.ListOTCOffersRequest) (*stockpb.ListOTCOffersResponse, error)
	listUnifiedFn  func(*stockpb.ListUnifiedOTCOffersRequest) (*stockpb.ListUnifiedOTCOffersResponse, error)
	buyFn          func(*stockpb.BuyOTCOfferRequest) (*stockpb.OTCTransaction, error)
}

func (s *stubOTCClient) ListOffers(_ context.Context, in *stockpb.ListOTCOffersRequest, _ ...grpc.CallOption) (*stockpb.ListOTCOffersResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListOTCOffersResponse{}, nil
}

func (s *stubOTCClient) ListUnifiedOffers(_ context.Context, in *stockpb.ListUnifiedOTCOffersRequest, _ ...grpc.CallOption) (*stockpb.ListUnifiedOTCOffersResponse, error) {
	if s.listUnifiedFn != nil {
		return s.listUnifiedFn(in)
	}
	return &stockpb.ListUnifiedOTCOffersResponse{}, nil
}
```

(Adapt to the existing struct shape — only ADD the unified field/method; don't drop the existing ones used by other tests.)

- [ ] **Step 4: Delete the api-gateway otccache package**

```bash
rm api-gateway/internal/otccache/cache.go
rm api-gateway/internal/otccache/cache_test.go
rmdir api-gateway/internal/otccache
```

- [ ] **Step 5: Run gateway tests**

Run: `cd api-gateway && go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 6: Run go vet and gofmt**

Run: `cd api-gateway && go vet ./... && gofmt -l . | head`
Expected: no vet issues; gofmt output empty.

- [ ] **Step 7: Run lint on modified packages**

Run: `make lint` (or `cd api-gateway && golangci-lint run ./... && cd ../stock-service && golangci-lint run ./...`)
Expected: no new warnings.

- [ ] **Step 8: Commit**

```bash
git add api-gateway/internal/handler/portfolio_handler_test.go
git rm -r api-gateway/internal/otccache
git commit -m "refactor(api-gateway): drop otccache package; tests use stockpb stub"
```

---

## Task 8: Update Specification.md

**Files:**
- Modify: `Specification.md`

- [ ] **Step 1: Find the OTCGRPCService block in §11**

Run: `grep -n "OTCGRPCService\|OTC.*service" Specification.md | head -10`

Locate the gRPC service definitions section. Add (or update) the entry for `OTCGRPCService`:

```
OTCGRPCService (stock-service)
  rpc ListOffers(ListOTCOffersRequest) returns (ListOTCOffersResponse)
  rpc BuyOffer(BuyOTCOfferRequest) returns (OTCTransaction)
  rpc ListUnifiedOffers(ListUnifiedOTCOffersRequest) returns (ListUnifiedOTCOffersResponse)
    — unified local + cross-bank view; backed by 5 s in-memory cache
      that fans out to peer banks' /public-stock for remote offers.
```

- [ ] **Step 2: Add a one-paragraph note in §27 (Market / OTC) or wherever cross-bank OTC is discussed**

Pick the existing section that covers cross-bank OTC and append:

```
The unified OTC offer view (local + cross-bank) is served by
stock-service's OTCGRPCService.ListUnifiedOffers. An in-process
goroutine in stock-service rebuilds the cache every 5 s by reading
local offers from OTCService.ListOffers and HTTP-GETting each active
peer bank's /api/v3/public-stock (PeerAuth via X-Api-Key resolved
through transaction-service's PeerBankAdminService). The api-gateway's
GET /api/v3/otc/offers handler is a thin pass-through; it owns no cache.
```

- [ ] **Step 3: Commit**

```bash
git add Specification.md
git commit -m "docs(spec): document OTCGRPCService.ListUnifiedOffers + cache placement"
```

---

## Task 9: Final verification across the repo

- [ ] **Step 1: Full build**

Run: `make build`
Expected: clean exit; all four (and onward) services build.

- [ ] **Step 2: Full test**

Run: `make test`
Expected: all package tests PASS.

- [ ] **Step 3: Lint**

Run: `make lint`
Expected: no new warnings.

- [ ] **Step 4: Smoke test the new RPC end-to-end (optional but recommended)**

Start the stack locally (or against a deployed cohort), publish a holding via `POST /api/v3/me/portfolio/:id/make-public`, wait ~5 s, then `GET /api/v3/otc/offers` from a peer bank. The peer should see the new offer with `kind: "remote"`, `bank_code` matching the source bank, and `owner_id` matching the seller's SI-TX id ("0" for bank-owned, numeric string for client-owned).

Run (against local cohort, replace JWT/host accordingly):

```bash
curl -s "http://localhost:8081/api/v3/otc/offers" -H "Authorization: Bearer $JWT" | jq
```

Expected: response includes both local and cross-bank offers, plus `peers_total`, `peers_reached`, `partial`, `last_refresh` fields.

- [ ] **Step 5: Final commit (only if any tweaks were needed during smoke testing)**

```bash
git add -A
git commit -m "chore: final touch-ups from end-to-end OTC unified-offers smoke test"
```

---

## Self-Review Notes

**Spec coverage:** every requirement from the conversation is mapped:
- Single source of truth in stock-service ✓ (Tasks 2–4)
- 5 s refresh cycle ✓ (Task 4 wires `5*time.Second`)
- Local + remote tag ✓ (UnifiedOTCOffer.kind, Task 1)
- bank_code on every row ✓ (Task 1)
- Existing REST route preserved ✓ (Task 5 — same path, augmented JSON)
- api-gateway becomes pass-through ✓ (Task 5)
- Removal of api-gateway/internal/otccache ✓ (Task 7)
- No DB changes ✓ (cache is in-memory only)
- No new env vars ✓ (`TRANSACTION_GRPC_ADDR` already in stock-service config)
- No docker-compose changes ✓ (transaction-service already a `depends_on`)
- Tests covering filter/pagination ✓ (Task 3 step 1)
- Spec doc update ✓ (Task 8)

**Type consistency check:**
- `UnifiedOTCOffer.kind` is `string` everywhere ("local" | "remote")
- `UnifiedOTCOffer.owner_id` is `string` everywhere (SI-TX shape)
- `peers_total` / `peers_reached` are `int32` in proto, `int` on `Snapshot`, projected at the boundary (Task 3 step 4)
- `LastRefreshUnix` is `int64` on the wire, `time.Time` in the cache, projected at both ends

**Placeholder scan:** none. Every code block contains complete code; every command is exact.
