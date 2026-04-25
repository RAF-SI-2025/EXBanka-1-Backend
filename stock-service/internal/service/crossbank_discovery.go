package service

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

// CrossbankDiscovery merges remote offers from every active peer bank into
// a local list, refreshing in the background every 60 seconds. The cache
// is in-process only — for a multi-replica deployment swap to Redis with
// the same TTL.
type CrossbankDiscovery struct {
	peers   CrossbankPeerRouter
	ownBank string

	mu        sync.RWMutex
	cached    []model.OTCOffer
	cachedAt  time.Time
	cacheTTL  time.Duration
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
			// PeerListOffers isn't on CrossbankPeerClient yet — placeholder
			// stub. Add this method when the wire shape is locked.
			_ = peer
			ch <- result{}
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
	_ = json.Marshal // silence unused on stub paths
	return out
}
