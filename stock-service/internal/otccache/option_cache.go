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
// Remote source: GET /public-stock on each registered active peer bank,
// polled every refresh interval and synthesized into sell_initiated option
// shells (no preset terms). The outbound /public-option-offers ingestion was
// removed — /public-stock shells are the sole cross-bank option source.
package otccache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/exbanka/contract/sitx"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/shopspring/decimal"
)

// OptionOffer is the unified shape stored in the cache. Local offers
// carry the seller_name display string; remote offers leave it empty.
type OptionOffer struct {
	Kind          string // "local" | "remote"
	BankCode      string
	RoutingNumber int64
	OfferID       string // local: strconv(uint64); remote: foreign id
	// LocalID is the stable local surrogate id. For local offers it equals
	// the numeric OfferID; for remote offers it is the OTCOffer.ID of the
	// folded-in remote row minted by the mirror, so the FE addresses any
	// offer by a plain id.
	LocalID uint64

	SellerID   string // SI-TX-prefixed ("client-<N>" | "bank")
	SellerName string // local-only display
	Direction  string // "sell_initiated" | "buy_initiated"

	Ticker string
	Amount int64
	// StrikePrice / Premium / SettlementDate are NO LONGER sourced from the
	// listing (option offers are termless inventory). The cache leaves them
	// empty; OTCHandler re-sources them per viewer from the negotiation chain
	// (bidder position / owner latest counter) — see otc_handler.go (D2). The
	// currencies still reflect the listing's trading currency.
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

// RemoteOfferMirror gives remote offers stable surrogate ids and reconciles
// peer-side cancels by folding them into the unified OTCOffer table as remote
// rows (routing_number=<peer>, native_id=<foreign id>).
// *repository.OTCOfferRepository satisfies it (SP-2a).
type RemoteOfferMirror interface {
	UpsertRemote(o *model.OTCOffer, seenAt time.Time) (uint64, error)
	// UpsertRemoteShell upserts a /public-stock shell remote row. Shells are
	// termless like every other OTCOffer, so this is a named alias over
	// UpsertRemote kept for self-documenting shell call sites.
	UpsertRemoteShell(o *model.OTCOffer, seenAt time.Time) (uint64, error)
	ReconcileRemoteNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error)
	ReconcileRemoteShellsNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error)
}

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
	egress      transactionpb.PeerEgressServiceClient
	ownBankCode string
	ownRouting  int64
	interval    time.Duration
	// aggregateBids is optional. When non-nil, the local-fetch path
	// enriches each row with best_bid/best_ask/active_chains_count.
	// nil ⇒ rows stay empty in those fields (legacy mode).
	aggregateBids AggregateActiveBidsFn
	mirror        RemoteOfferMirror
}

func NewOptionRefresher(
	cache *OptionCache,
	otc OptionOfferLister,
	currency OptionCurrencyResolver,
	peerAdmin transactionpb.PeerBankAdminServiceClient,
	egress transactionpb.PeerEgressServiceClient,
	ownBankCode string,
	ownRouting int64,
	interval time.Duration,
) *OptionRefresher {
	return &OptionRefresher{
		cache:       cache,
		otc:         otc,
		currency:    currency,
		peerAdmin:   peerAdmin,
		egress:      egress,
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

// WithMirror wires the persistent remote-offer mirror. When set, each
// successful peer fetch upserts its remote offers (stamping LocalID) and
// reconciles that peer's vanished offers to cancelled. nil => legacy mode.
func (r *OptionRefresher) WithMirror(m RemoteOfferMirror) *OptionRefresher {
	r.mirror = m
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
				// The peer's /public-stock is the SOLE cross-bank option source:
				// each peer listing is synthesized into a sell_initiated shell. A
				// peer counts as "reached" iff its /public-stock fetch succeeds, so
				// the options view reports peers up like the stocks view.
				reached := false
				if shells, serr := r.fetchPeerStocks(cycleCtx, peer); serr != nil {
					log.Printf("otccache(stock-shells): peer %s fetch failed: %v", peer.GetBankCode(), serr)
				} else {
					reached = true
					mu.Lock()
					offers = append(offers, shells...)
					mu.Unlock()
				}
				if reached {
					mu.Lock()
					peersReached++
					mu.Unlock()
				}
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
			Kind:          "local",
			BankCode:      r.ownBankCode,
			RoutingNumber: r.ownRouting,
			OfferID:       strconv.FormatUint(o.ID, 10),
			LocalID:       o.ID,
			SellerID:      composeSellerID(o),
			SellerName:    "", // OTCOffer carries no display name — UI can resolve via /user/{rid}/{id}
			Direction:     o.Direction,
			Ticker:        o.Ticker,
			Amount:        o.Quantity.IntPart(),
			// Terms are termless on the listing — re-sourced per viewer by the
			// handler (D2). Leave strike/premium/settlement empty; keep the
			// trading currency for the FE to render alongside negotiated terms.
			StrikePrice:     "",
			StrikeCurrency:  currency,
			Premium:         "",
			PremiumCurrency: currency,
			SettlementDate:  "",
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

func (r *OptionRefresher) fetchPeerStocks(ctx context.Context, peer *transactionpb.PeerBank) ([]OptionOffer, error) {
	proxyResp, err := r.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peer.GetBankCode(),
		Method:       http.MethodGet,
		Path:         "/public-stock",
	})
	if err != nil {
		return nil, err
	}
	if proxyResp.GetStatusCode() != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", proxyResp.GetStatusCode(), string(proxyResp.GetBody()))
	}
	var resp sitx.PublicStocksResponse
	if err := json.Unmarshal(proxyResp.GetBody(), &resp); err != nil {
		return nil, err
	}
	return r.buildAndMirrorRemoteStockShells(peer.GetBankCode(), peerRoutingOf(peer), resp), nil
}

// buildAndMirrorRemoteStockShells converts a peer's /public-stock listings into
// biddable sell_initiated SHELL rows (no preset terms — buyer proposes
// strike/premium/settlement on bid). native_id = "ps:<sellerRouting>:<sellerId>:<ticker>"
// where sellerRouting is the individual seller's routing number (s.Seller.RoutingNumber),
// NOT the peer gateway routing. This avoids collisions when sellers from different
// origin banks appear in a single peer's /public-stock response.
// Call ONLY after a successful peer fetch.
func (r *OptionRefresher) buildAndMirrorRemoteStockShells(peerBankCode string, peerRouting int64, stocks []sitx.PublicStock) []OptionOffer {
	if peerRouting == model.OwnRouting() {
		log.Printf("WARN otccache(stock-shells): peer bank_code=%s routing=%d collides with own routing — skipping", peerBankCode, peerRouting)
		return nil
	}
	now := time.Now().UTC()

	// §3.1 /public-stock identifies a listing SOLELY by its seller (ForeignBankId
	// routing+id) within a ticker — there is NO per-offer key (see sitx.PublicSeller).
	// So a seller's availability for a ticker is a single quantity, and
	// native_id = "ps:<sellerRouting>:<sellerId>:<ticker>" is the unique negotiable
	// unit. A non-conformant peer that lists the same (seller, ticker) more than once
	// (e.g. two "offers" of 5 and 70) would otherwise yield multiple cache rows that
	// COLLIDE on one native_id/local id — so a bid on one silently targets the other.
	// Aggregate duplicates by native_id (summing the available amount) so every
	// emitted shell maps 1:1 to a distinct id. Insertion order is preserved for
	// determinism.
	type aggShell struct {
		native   string
		sellerID string
		ticker   string
		amount   int64
	}
	order := make([]string, 0)
	agg := make(map[string]*aggShell)
	for i := range stocks {
		ticker := stocks[i].Stock.Ticker
		if ticker == "" {
			continue
		}
		for _, s := range stocks[i].Sellers {
			if s.Seller.RoutingNumber == model.OwnRouting() || s.Seller.ID == "" {
				continue
			}
			native := fmt.Sprintf("%s%d:%s:%s", model.RemoteStockShellPrefix, s.Seller.RoutingNumber, s.Seller.ID, ticker)
			if cur, ok := agg[native]; ok {
				cur.amount += s.Amount
				continue
			}
			agg[native] = &aggShell{native: native, sellerID: s.Seller.ID, ticker: ticker, amount: s.Amount}
			order = append(order, native)
		}
	}

	seen := make([]string, 0, len(order))
	out := make([]OptionOffer, 0, len(order))
	for _, native := range order {
		a := agg[native]
		row := OptionOffer{
			Kind:          "remote",
			BankCode:      peerBankCode,
			RoutingNumber: peerRouting,
			OfferID:       a.native,
			SellerID:      a.sellerID,
			Direction:     model.OTCDirectionSellInitiated,
			Ticker:        a.ticker,
			Amount:        a.amount,
		}
		if r.mirror != nil {
			n := a.native
			bc := peerBankCode
			sid := a.sellerID
			remoteRow := &model.OTCOffer{
				RoutingNumber:               peerRouting,
				NativeID:                    &n,
				InitiatorBankCode:           &bc,
				RemoteSellerID:              &sid,
				InitiatorOwnerType:          model.OwnerBank,
				Direction:                   model.OTCDirectionSellInitiated,
				Ticker:                      a.ticker,
				Quantity:                    decimal.NewFromInt(a.amount),
				Status:                      model.OTCOfferStatusOpen,
				LastModifiedByPrincipalType: "system",
				LastModifiedByPrincipalID:   0,
			}
			if id, err := r.mirror.UpsertRemoteShell(remoteRow, now); err != nil {
				log.Printf("otccache(stock-shells): upsert peer=%s %s failed: %v", peerBankCode, native, err)
			} else {
				row.LocalID = id
				seen = append(seen, native)
			}
		}
		out = append(out, row)
	}
	if r.mirror != nil {
		if n, err := r.mirror.ReconcileRemoteShellsNotSeen(peerRouting, seen); err != nil {
			log.Printf("otccache(stock-shells): reconcile peer=%s failed: %v", peerBankCode, err)
		} else if n > 0 {
			log.Printf("otccache(stock-shells): reconciled %d vanished shells from peer=%s", n, peerBankCode)
		}
	}
	return out
}

// peerRoutingOf returns the polled peer's routing number (SI-TX bank codes
// are the routing number as a string).
func peerRoutingOf(peer *transactionpb.PeerBank) int64 {
	if rn := peer.GetRoutingNumber(); rn != 0 {
		return rn
	}
	n, _ := strconv.ParseInt(peer.GetBankCode(), 10, 64)
	return n
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
