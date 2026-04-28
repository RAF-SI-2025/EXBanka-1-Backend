package service

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// CrossbankDiscovery merges remote offers from every active peer bank into
// a local list, refreshing in the background every 60 seconds. The cache
// is in-process only — for a multi-replica deployment swap to Redis with
// the same TTL.
type CrossbankDiscovery struct {
	peers   CrossbankPeerRouter
	ownBank string

	mu       sync.RWMutex
	cached   []model.OTCOffer
	cachedAt time.Time
	cacheTTL time.Duration
}

func NewCrossbankDiscovery(peers CrossbankPeerRouter, ownBank string, ttl time.Duration) *CrossbankDiscovery {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &CrossbankDiscovery{peers: peers, ownBank: ownBank, cacheTTL: ttl}
}

// MergeRemote returns a slice of remote offers freshened on cache-miss.
// Local offers are joined with the remote set by the caller (the gateway
// list handler is the canonical join site).
func (d *CrossbankDiscovery) MergeRemote(ctx context.Context, peerCodes []string) []model.OTCOffer {
	d.mu.RLock()
	if time.Since(d.cachedAt) < d.cacheTTL && d.cached != nil {
		out := make([]model.OTCOffer, len(d.cached))
		copy(out, d.cached)
		d.mu.RUnlock()
		return out
	}
	d.mu.RUnlock()

	out := d.refresh(ctx, peerCodes)
	d.mu.Lock()
	d.cached = out
	d.cachedAt = time.Now()
	d.mu.Unlock()
	return out
}

// Invalidate forces the next MergeRemote to re-fetch. Triggered by the
// otc.local-offer-changed Kafka consumer when peers signal their data has
// moved.
func (d *CrossbankDiscovery) Invalidate() {
	d.mu.Lock()
	d.cachedAt = time.Time{}
	d.mu.Unlock()
}

// refresh fans out offer-list queries to every active peer in parallel.
// Failures are logged and their slice contributes empty.
func (d *CrossbankDiscovery) refresh(ctx context.Context, peerCodes []string) []model.OTCOffer {
	if d.peers == nil || len(peerCodes) == 0 {
		return nil
	}
	type result struct {
		offers []model.OTCOffer
		err    error
	}
	ch := make(chan result, len(peerCodes))
	for _, code := range peerCodes {
		code := code
		go func() {
			peer, err := d.peers.ClientFor(code)
			if err != nil {
				ch <- result{err: err}
				return
			}
			resp, err := peer.PeerListOffers(ctx, d.cachedAtRFC(), "either")
			if err != nil {
				ch <- result{err: err}
				return
			}
			out := make([]model.OTCOffer, 0, len(resp.Offers))
			for _, item := range resp.Offers {
				qty, _ := decimal.NewFromString(item.Quantity)
				strike, _ := decimal.NewFromString(item.StrikePrice)
				prem, _ := decimal.NewFromString(item.Premium)
				settle, _ := time.Parse("2006-01-02", item.SettlementDate)
				updatedAt, _ := time.Parse(time.RFC3339, item.UpdatedAt)
				bankCode := item.BankCode
				out = append(out, model.OTCOffer{
					ID:                item.OfferID,
					InitiatorBankCode: &bankCode,
					Direction:         item.Direction,
					StockID:           item.StockID,
					Quantity:          qty,
					StrikePrice:       strike,
					Premium:           prem,
					SettlementDate:    settle,
					Status:            item.Status,
					UpdatedAt:         updatedAt,
				})
			}
			ch <- result{offers: out}
		}()
	}
	out := make([]model.OTCOffer, 0)
	for range peerCodes {
		r := <-ch
		if r.err != nil {
			log.Printf("crossbank discovery: %v", r.err)
			continue
		}
		out = append(out, r.offers...)
	}
	_ = json.Marshal // serialization helper retained for future use
	return out
}

// cachedAtRFC returns the cache's last-refresh timestamp in RFC3339, or
// empty string if the cache is cold (full fetch).
func (d *CrossbankDiscovery) cachedAtRFC() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cachedAt.IsZero() {
		return ""
	}
	return d.cachedAt.UTC().Format(time.RFC3339)
}
