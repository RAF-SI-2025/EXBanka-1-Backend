// Package otccache — OptionCache + OptionRefresher form the cross-bank
// discovery layer for OPEN OTC option listings. Parallel to Cache /
// Refresher (which serves the stocks marketplace) but with the option-
// specific shape: strike + premium + settlement_date + direction.
//
// Plan: docs/superpowers/plans/2026-05-16-otc-options-cross-bank.md.
// The cache is consumed by OTCHandler.ListUnifiedOptionOffers, exposed
// to the gateway as GET /api/v3/otc/options.
//
// Local source: stock-service OTCOfferRepository.ListOpenForCache().
// Remote source: GET /api/v3/public-option-offers on each registered
// active peer bank, polled every refresh interval.
package otccache

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
)

// OptionOffer is the unified shape stored in the cache. Local offers
// carry the seller_name display string; remote offers leave it empty.
type OptionOffer struct {
	Kind          string // "local" | "remote"
	BankCode      string
	RoutingNumber int64
	OfferID       string // local: strconv(uint64); remote: foreign id

	SellerID   string // SI-TX-prefixed ("client-<N>" | "bank")
	SellerName string // local-only display
	Direction  string // "sell_initiated" | "buy_initiated"

	Ticker          string
	Amount          int64
	StrikePrice     string // decimal as string
	StrikeCurrency  string
	Premium         string
	PremiumCurrency string
	SettlementDate  string // RFC3339 UTC
	CreatedAt       string // RFC3339 UTC

	// Best-bid / best-ask aggregation (Part A 2026-05-16). Empty
	// strings ⇒ no active chains OR a remote peer that doesn't
	// publish these fields. ActiveChainsCount == 0 carries the same
	// meaning. FE renders "—" in that case.
	BestBid           string
	BestAsk           string
	ActiveChainsCount int32
}

// OfferAggregate is otccache's local projection of the
// best-bid / best-ask / active-count surface for one parent listing.
// The wiring code in cmd/main.go adapts the repository's typed result
// into this string-shape so otccache stays decoupled from repository.
type OfferAggregate struct {
	BestBid     string
	BestAsk     string
	ActiveCount int32
}

// AggregateActiveBidsFn is the narrow dependency the local-fetch path
// uses. Pass nil to disable enrichment (legacy mode — fields stay
// empty). Implemented in cmd/main.go as a thin adapter over
// *repository.OTCNegotiationRepository.AggregateActiveBidsByOffer.
type AggregateActiveBidsFn func(offerIDs []uint64) (map[uint64]OfferAggregate, error)

type OptionSnapshot struct {
	Offers       []OptionOffer
	LastRefresh  time.Time
	PeersTotal   int
	PeersReached int
}

// OptionOfferLister is the narrow interface the refresher uses to pull
// local rows. OTCOfferRepository.ListOpenForCache satisfies it; tests
// can substitute a fake.
type OptionOfferLister interface {
	ListOpenForCache(limit int) ([]model.OTCOffer, error)
}

// OptionCurrencyResolver looks up the listing currency for a stock so
// the cache can stamp strike/premium currency on each row. (The
// OTCOffer model itself carries no currency — it lives on the
// StockExchange the listing trades on.)
type OptionCurrencyResolver interface {
	CurrencyForStock(stockID uint64) (string, error)
}

// OptionCache is goroutine-safe; Get returns a defensive copy.
type OptionCache struct {
	mu           sync.RWMutex
	offers       []OptionOffer
	lastRefresh  time.Time
	peersTotal   int
	peersReached int
}

func NewOptionCache() *OptionCache { return &OptionCache{} }

func (c *OptionCache) Get() OptionSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]OptionOffer, len(c.offers))
	copy(out, c.offers)
	return OptionSnapshot{
		Offers:       out,
		LastRefresh:  c.lastRefresh,
		PeersTotal:   c.peersTotal,
		PeersReached: c.peersReached,
	}
}

func (c *OptionCache) set(s OptionSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offers = s.Offers
	c.lastRefresh = s.LastRefresh
	c.peersTotal = s.PeersTotal
	c.peersReached = s.PeersReached
}

// SetOptionForTest seeds the cache from outside the package (test-only).
func SetOptionForTest(c *OptionCache, s OptionSnapshot) { c.set(s) }

// OptionRefresher rebuilds the cache on every interval tick.
type OptionRefresher struct {
	cache       *OptionCache
	otc         OptionOfferLister
	currency    OptionCurrencyResolver
	peerAdmin   transactionpb.PeerBankAdminServiceClient
	httpClient  *http.Client
	ownBankCode string
	ownRouting  int64
	interval    time.Duration
	// aggregateBids is optional. When non-nil, the local-fetch path
	// enriches each row with best_bid/best_ask/active_chains_count.
	// nil ⇒ rows stay empty in those fields (legacy mode).
	aggregateBids AggregateActiveBidsFn
}

func NewOptionRefresher(
	cache *OptionCache,
	otc OptionOfferLister,
	currency OptionCurrencyResolver,
	peerAdmin transactionpb.PeerBankAdminServiceClient,
	ownBankCode string,
	ownRouting int64,
	interval time.Duration,
) *OptionRefresher {
	return &OptionRefresher{
		cache:       cache,
		otc:         otc,
		currency:    currency,
		peerAdmin:   peerAdmin,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		ownBankCode: ownBankCode,
		ownRouting:  ownRouting,
		interval:    interval,
	}
}

// WithAggregateBids wires the best-bid aggregation dependency. Returns
// the refresher so callers can chain.
func (r *OptionRefresher) WithAggregateBids(fn AggregateActiveBidsFn) *OptionRefresher {
	r.aggregateBids = fn
	return r
}

// Run blocks until ctx is cancelled. Initial refresh on start, then
// ticks at interval. Per-source failures are logged + skipped so the
// cycle yields whatever was reachable.
func (r *OptionRefresher) Run(ctx context.Context) {
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
func (r *OptionRefresher) Refresh(ctx context.Context) { r.refresh(ctx) }

func (r *OptionRefresher) refresh(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	var (
		offers       []OptionOffer
		peersTotal   int
		peersReached int
		mu           sync.Mutex
	)

	if local, err := r.fetchLocal(); err == nil {
		offers = append(offers, local...)
	} else {
		log.Printf("otccache(options): local fetch failed: %v", err)
	}

	peerList, err := r.peerAdmin.ListPeerBanks(cycleCtx, &transactionpb.ListPeerBanksRequest{ActiveOnly: true})
	if err != nil {
		log.Printf("otccache(options): list peers failed: %v", err)
	} else if peerList != nil {
		var wg sync.WaitGroup
		for _, p := range peerList.GetPeerBanks() {
			peersTotal++
			wg.Add(1)
			go func(peer *transactionpb.PeerBank) {
				defer wg.Done()
				peerOffers, err := r.fetchPeer(cycleCtx, peer)
				if err != nil {
					log.Printf("otccache(options): peer %s fetch failed: %v", peer.GetBankCode(), err)
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

	r.cache.set(OptionSnapshot{
		Offers:       offers,
		LastRefresh:  time.Now().UTC(),
		PeersTotal:   peersTotal,
		PeersReached: peersReached,
	})
}

func (r *OptionRefresher) fetchLocal() ([]OptionOffer, error) {
	rows, err := r.otc.ListOpenForCache(1000)
	if err != nil {
		return nil, err
	}
	// Bulk-aggregate active chain pricing for every local row in one
	// query (Part A 2026-05-16). Best-effort: aggregation errors fall
	// back to empty fields rather than failing the whole refresh.
	var aggregates map[uint64]OfferAggregate
	if r.aggregateBids != nil && len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		if got, aggErr := r.aggregateBids(ids); aggErr != nil {
			log.Printf("otccache(options): aggregate active bids failed (continuing without enrichment): %v", aggErr)
		} else {
			aggregates = got
		}
	}
	out := make([]OptionOffer, 0, len(rows))
	for i := range rows {
		o := &rows[i]
		currency := r.resolveCurrency(o.StockID)
		row := OptionOffer{
			Kind:            "local",
			BankCode:        r.ownBankCode,
			RoutingNumber:   r.ownRouting,
			OfferID:         strconv.FormatUint(o.ID, 10),
			SellerID:        composeSellerID(o),
			SellerName:      "", // OTCOffer carries no display name — UI can resolve via /user/{rid}/{id}
			Direction:       o.Direction,
			Ticker:          o.Ticker,
			Amount:          o.Quantity.IntPart(),
			StrikePrice:     o.StrikePrice.String(),
			StrikeCurrency:  currency,
			Premium:         o.Premium.String(),
			PremiumCurrency: currency,
			SettlementDate:  o.SettlementDate.UTC().Format(time.RFC3339),
			CreatedAt:       o.CreatedAt.UTC().Format(time.RFC3339),
		}
		// Pick the side relevant to the parent's direction. A buyer-
		// posted listing (buy_initiated) has sellers bidding their ask
		// downward → expose best_ask; a seller-posted listing has
		// buyers bidding their premium upward → expose best_bid.
		if agg, ok := aggregates[o.ID]; ok {
			row.ActiveChainsCount = agg.ActiveCount
			switch o.Direction {
			case "buy_initiated":
				row.BestAsk = agg.BestAsk
			default:
				row.BestBid = agg.BestBid
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *OptionRefresher) fetchPeer(ctx context.Context, peer *transactionpb.PeerBank) ([]OptionOffer, error) {
	resolveResp, err := r.peerAdmin.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: peer.GetBankCode()})
	if err != nil {
		return nil, err
	}
	full := resolveResp.GetPeerBank()
	if full == nil || !full.GetActive() {
		return nil, nil
	}
	url := strings.TrimRight(full.GetBaseUrl(), "/") + "/public-option-offers"
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
	var resp sitx.PublicOptionOffersResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]OptionOffer, 0, len(resp.Offers))
	for _, o := range resp.Offers {
		out = append(out, OptionOffer{
			Kind:              "remote",
			BankCode:          peer.GetBankCode(),
			RoutingNumber:     o.OfferID.RoutingNumber,
			OfferID:           o.OfferID.ID,
			SellerID:          o.SellerID.ID,
			Direction:         o.Direction,
			Ticker:            o.Ticker,
			Amount:            o.Amount,
			StrikePrice:       o.StrikePrice.String(),
			StrikeCurrency:    o.StrikeCurrency,
			Premium:           o.Premium.String(),
			PremiumCurrency:   o.PremiumCurrency,
			SettlementDate:    o.SettlementDate,
			CreatedAt:         o.CreatedAt,
			BestBid:           o.BestBid,           // empty when peer doesn't publish
			BestAsk:           o.BestAsk,           // empty when peer doesn't publish
			ActiveChainsCount: o.ActiveChainsCount, // 0 when peer doesn't publish
		})
	}
	return out, nil
}

func (r *OptionRefresher) resolveCurrency(stockID uint64) string {
	if r.currency == nil {
		return "USD"
	}
	c, err := r.currency.CurrencyForStock(stockID)
	if err != nil || c == "" {
		return "USD"
	}
	return c
}

// composeSellerID returns the SI-TX-prefixed initiator id ("client-<N>"
// or "bank") for use as the seller in marketplace discovery. The
// "seller" semantically = the listing's poster regardless of Direction
// — peers driving negotiation against this listing always quote
// sellerId.id as the seller_id of their POST /negotiations call.
func composeSellerID(o *model.OTCOffer) string {
	if o.InitiatorOwnerType == model.OwnerBank {
		return "bank"
	}
	if o.InitiatorOwnerID == nil {
		return ""
	}
	return "client-" + strconv.FormatUint(*o.InitiatorOwnerID, 10)
}
