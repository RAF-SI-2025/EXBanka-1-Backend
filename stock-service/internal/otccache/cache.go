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

// Refresh is the exported single-cycle version of the internal refresh loop.
// Called by the cronreg-gated loop in main.go.
func (r *Refresher) Refresh(ctx context.Context) { r.refresh(ctx) }

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
		// Phase 11 — surface the seller's asking price set at make-
		// public time. Fall back to AveragePrice (the weighted-avg
		// buy cost) for legacy rows that have PublicQuantity > 0 but
		// no explicit ask yet.
		price := h.PublicPrice
		if price.Sign() <= 0 {
			price = h.AveragePrice
		}
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
			PricePerUnit: price.StringFixed(2),
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
	// base_url already carries the peer's SI-TX path prefix (set by the
	// registering admin) so cohort banks with different gateway
	// layouts can all interop. We only append the leaf path.
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

// SetForTest seeds the cache from outside the package. Test-only — the
// production code path is Refresher.refresh.
func SetForTest(c *Cache, s Snapshot) { c.set(s) }
