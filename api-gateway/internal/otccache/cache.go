// Package otccache holds the unified OTC-offer cache surfaced by
// GET /api/v3/otc/offers. The cache is rebuilt every refresh interval
// by the Refresher: local offers come from stock-service over gRPC,
// remote offers come from each active peer bank's GET /public-stock
// over HTTP (PeerAuth via X-Api-Key).
//
// Clients always see a unified list with `kind: "local" | "remote"`;
// the gateway hides the fan-out and the per-peer HTTP failure modes.
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
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// Offer is the unified shape returned by GET /api/v3/otc/offers.
//
// Local offers (kind="local") carry the original stock-service shape so
// existing buy flows (POST /api/v3/otc/offers/:id/buy) keep working.
// Remote offers (kind="remote") carry bank_code + owner_id so the UI
// can route purchases through POST /api/v3/me/peer-otc/negotiations.
type Offer struct {
	Kind     string `json:"kind"`
	BankCode string `json:"bank_code"`

	// Local-only fields (omitted on remote offers).
	ID         uint64 `json:"id,omitempty"`
	SellerID   uint64 `json:"seller_id,omitempty"`
	SellerName string `json:"seller_name,omitempty"`
	Name       string `json:"name,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`

	// Remote-only field. SI-TX shape: "0" means the holding is bank-owned;
	// "1+" is a client id at the seller bank.
	OwnerID string `json:"owner_id,omitempty"`

	// Common fields.
	SecurityType string `json:"security_type"`
	Ticker       string `json:"ticker"`
	Quantity     int64  `json:"quantity"`
	PricePerUnit string `json:"price_per_unit"`
	Currency     string `json:"currency,omitempty"`
}

// Snapshot is an immutable copy of the cache returned to handlers.
type Snapshot struct {
	Offers       []Offer
	LastRefresh  time.Time
	PeersTotal   int
	PeersReached int
}

// Cache is a goroutine-safe holder for the most recent aggregated
// offer list. Reads via Get() return a defensive copy of the slice
// so handlers can paginate/filter without holding the lock.
type Cache struct {
	mu           sync.RWMutex
	offers       []Offer
	lastRefresh  time.Time
	peersTotal   int
	peersReached int
}

// New returns an empty cache. The first Get() before any refresh ticks
// returns an empty snapshot with a zero LastRefresh.
func New() *Cache {
	return &Cache{}
}

// Get returns a snapshot of the current cache contents.
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

// Refresher periodically rebuilds the cache. Local offers are pulled
// from stock-service via gRPC; remote offers are fetched in parallel
// from each active peer bank's GET /api/v3/public-stock (PeerAuth).
type Refresher struct {
	cache       *Cache
	otc         stockpb.OTCGRPCServiceClient
	peerAdmin   transactionpb.PeerBankAdminServiceClient
	httpClient  *http.Client
	ownBankCode string
	interval    time.Duration
}

// NewRefresher wires the dependencies. interval is typically 5s.
func NewRefresher(
	cache *Cache,
	otc stockpb.OTCGRPCServiceClient,
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

// Run blocks until ctx is cancelled. Performs an initial refresh on
// start so the first request after boot has data, then ticks at
// `interval`. A failure on any single source (local or any peer) is
// logged but does not abort the cycle.
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

	if local, err := r.fetchLocal(cycleCtx); err == nil {
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

func (r *Refresher) fetchLocal(ctx context.Context) ([]Offer, error) {
	resp, err := r.otc.ListOffers(ctx, &stockpb.ListOTCOffersRequest{Page: 1, PageSize: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]Offer, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		out = append(out, Offer{
			Kind:         "local",
			BankCode:     r.ownBankCode,
			ID:           o.GetId(),
			SellerID:     o.GetSellerId(),
			SellerName:   o.GetSellerName(),
			SecurityType: o.GetSecurityType(),
			Ticker:       o.GetTicker(),
			Name:         o.GetName(),
			Quantity:     o.GetQuantity(),
			PricePerUnit: o.GetPricePerUnit(),
			CreatedAt:    o.GetCreatedAt(),
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
