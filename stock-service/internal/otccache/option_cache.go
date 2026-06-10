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

	Ticker          string
	Amount          int64
	StrikePrice     string // decimal as string
	StrikeCurrency  string
	Premium         string
	PremiumCurrency string
	SettlementDate  string // RFC3339 UTC
	CreatedAt       string // RFC3339 UTC

	// HasPresetTerms: true for /public-option-offers rows (offer carries
	// owner-set strike + premium). false for shells synthesized from
	// /public-stock (fully buyer-negotiated, no preset terms).
	HasPresetTerms bool

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
	// UpsertRemoteShell is like UpsertRemote but guarantees has_preset_terms is
	// persisted as the struct value even when it is false (zero). Use this for
	// /public-stock shells where HasPresetTerms must be false.
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
				// fetchPeer (option-offers) and fetchPeerStocks (/public-stock
				// shells) are INDEPENDENT: a peer that 404s /public-option-offers
				// (base-spec peer, no proprietary extension) must still have its
				// shells ingested. The early-return on fetchPeer failure was the
				// Bug-1 root cause — fixed by restructuring to if/else.
				if peerOffers, err := r.fetchPeer(cycleCtx, peer); err != nil {
					log.Printf("otccache(options): peer %s fetch failed: %v", peer.GetBankCode(), err)
				} else {
					mu.Lock()
					offers = append(offers, peerOffers...)
					peersReached++
					mu.Unlock()
				}

				if shells, serr := r.fetchPeerStocks(cycleCtx, peer); serr != nil {
					log.Printf("otccache(stock-shells): peer %s fetch failed: %v", peer.GetBankCode(), serr)
				} else {
					mu.Lock()
					offers = append(offers, shells...)
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
			Kind:            "local",
			BankCode:        r.ownBankCode,
			RoutingNumber:   r.ownRouting,
			OfferID:         strconv.FormatUint(o.ID, 10),
			LocalID:         o.ID,
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
			HasPresetTerms:  true,
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
	// Outbound HTTP to the peer's /public-option-offers is centralized in
	// interbank-service: ProxyToPeer resolves the peer's base_url, signs, GETs,
	// and returns the peer's status + body verbatim.
	proxyResp, err := r.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peer.GetBankCode(),
		Method:       http.MethodGet,
		Path:         "/public-option-offers",
	})
	if err != nil {
		return nil, err
	}
	if proxyResp.GetStatusCode() != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", proxyResp.GetStatusCode(), string(proxyResp.GetBody()))
	}
	var resp sitx.PublicOptionOffersResponse
	if err := json.Unmarshal(proxyResp.GetBody(), &resp); err != nil {
		return nil, err
	}
	return r.buildAndMirrorRemoteOffers(peer.GetBankCode(), peerRoutingOf(peer), resp.Offers), nil
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

// buildAndMirrorRemoteOffers converts a peer's public option offers into
// unified cache rows, upserting each into the persistent mirror (stamping
// the stable LocalID) and reconciling that peer's vanished offers to
// cancelled. Called ONLY after a successful (2xx) peer fetch, so the
// reconcile never runs on a transport/HTTP error (false-cancel guard).
// The mirror row is keyed by the POLLED peer's routing so reconcile scope
// always matches what we upserted.
func (r *OptionRefresher) buildAndMirrorRemoteOffers(peerBankCode string, peerRouting int64, offers []sitx.PublicOptionOffer) []OptionOffer {
	// Ingestion collision guard (SP-2a): if the peer's routing matches our own,
	// ingesting any of its offers would stamp routing_number=OwnRouting() on the
	// mirror row, making it look LOCAL. Reject the entire peer's payload.
	if peerRouting == model.OwnRouting() {
		log.Printf("WARN otccache(options): peer bank_code=%s routing=%d collides with own routing (%d) — skipping entire peer payload",
			peerBankCode, peerRouting, model.OwnRouting())
		return nil
	}
	now := time.Now().UTC()
	seen := make([]string, 0, len(offers))
	out := make([]OptionOffer, 0, len(offers))
	for i := range offers {
		o := offers[i]
		// Per-offer guard: reject any offer claiming our own routing as its id
		// namespace (defense-in-depth: the per-peer guard above should catch this,
		// but a malformed payload could mix routings).
		if o.OfferID.RoutingNumber == model.OwnRouting() {
			log.Printf("WARN otccache(options): peer=%s offer %s claims own routing (%d) — skipping offer",
				peerBankCode, o.OfferID.ID, model.OwnRouting())
			continue
		}
		// Seller-centric discovery guard (SI-TX §3 / §3.1 / §3.2): the OTC
		// cross-bank model only conveys SELLER-side listings. A buy_initiated
		// offer's poster is a BUYER, which has no spec wire representation. We
		// never publish our own buy_initiated offers, but a non-conformant peer
		// could still emit one with our proprietary `direction` field set.
		// Ingesting it would create a remote listing a local user could "bid" on
		// — only to hit the role-inversion fail-closed at openRemoteNegotiation.
		// Drop it at the ingest boundary so it never becomes a biddable row.
		if o.Direction == model.OTCDirectionBuyInitiated {
			log.Printf("WARN otccache(options): peer=%s offer %s is buy_initiated — skipping (seller-centric cross-bank discovery)",
				peerBankCode, o.OfferID.ID)
			continue
		}
		row := OptionOffer{
			Kind:              "remote",
			BankCode:          peerBankCode,
			RoutingNumber:     peerRouting, // authoritative (registrar-verified); peer's wire value is advisory
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
			BestBid:           o.BestBid,
			BestAsk:           o.BestAsk,
			ActiveChainsCount: o.ActiveChainsCount,
			HasPresetTerms:    true,
		}
		if r.mirror != nil {
			nativeID := o.OfferID.ID
			bankCode := peerBankCode
			sellerID := o.SellerID.ID
			strikeCcy := o.StrikeCurrency
			premiumCcy := o.PremiumCurrency
			remoteRow := &model.OTCOffer{
				RoutingNumber:     peerRouting,
				NativeID:          &nativeID,
				InitiatorBankCode: &bankCode,
				RemoteSellerID:    &sellerID,
				// Remote rows are "bank-ish" from our view: OwnerBank + nil id
				// is the only owner combination ValidateOwner accepts without a
				// concrete local owner. The actual remote seller is carried in
				// RemoteSellerID / InitiatorBankCode for display.
				InitiatorOwnerType: model.OwnerBank,
				Direction:          o.Direction,
				// StockID is local-only and meaningless for a peer listing; 0.
				Ticker: o.Ticker,
				// Wire amount is int64; OTCOffer.Quantity is decimal.
				Quantity:        decimal.NewFromInt(o.Amount),
				StrikePrice:     o.StrikePrice,
				Premium:         o.Premium,
				StrikeCurrency:  &strikeCcy,
				PremiumCurrency: &premiumCcy,
				SettlementDate:  parseRFC3339OrZero(o.SettlementDate),
				Status:          model.OTCOfferStatusOpen,
				// HasPresetTerms = true: a /public-option-offers listing always
				// carries owner-set strike and premium terms.
				HasPresetTerms: true,
				// NOT-NULL audit columns: the refresher is the actor for
				// remote rows. "system"/0 marks a machine-written row.
				LastModifiedByPrincipalType: "system",
				LastModifiedByPrincipalID:   0,
			}
			id, err := r.mirror.UpsertRemote(remoteRow, now)
			if err != nil {
				log.Printf("otccache(options): mirror upsert peer=%s foreign=%s failed: %v", peerBankCode, o.OfferID.ID, err)
			} else {
				row.LocalID = id
				seen = append(seen, o.OfferID.ID)
			}
		}
		out = append(out, row)
	}
	if r.mirror != nil {
		if n, err := r.mirror.ReconcileRemoteNotSeen(peerRouting, seen); err != nil {
			log.Printf("otccache(options): reconcile peer=%s failed: %v", peerBankCode, err)
		} else if n > 0 {
			log.Printf("otccache(options): reconciled %d cancelled offers from peer=%s", n, peerBankCode)
		}
	}
	return out
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
	seen := make([]string, 0)
	out := make([]OptionOffer, 0)
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
			row := OptionOffer{
				Kind:           "remote",
				BankCode:       peerBankCode,
				RoutingNumber:  peerRouting,
				OfferID:        native,
				SellerID:       s.Seller.ID,
				Direction:      model.OTCDirectionSellInitiated,
				Ticker:         ticker,
				Amount:         s.Amount,
				HasPresetTerms: false,
			}
			if r.mirror != nil {
				n := native
				bc := peerBankCode
				sid := s.Seller.ID
				remoteRow := &model.OTCOffer{
					RoutingNumber:               peerRouting,
					NativeID:                    &n,
					InitiatorBankCode:           &bc,
					RemoteSellerID:              &sid,
					InitiatorOwnerType:          model.OwnerBank,
					Direction:                   model.OTCDirectionSellInitiated,
					Ticker:                      ticker,
					Quantity:                    decimal.NewFromInt(s.Amount),
					StrikePrice:                 decimal.Zero,
					Premium:                     decimal.Zero,
					StrikeCurrency:              nil,
					PremiumCurrency:             nil,
					HasPresetTerms:              false,
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

// parseRFC3339OrZero parses an RFC3339 timestamp string into time.Time.
// On a parse error (or empty string) it logs and returns the zero time, so a
// malformed peer settlement_date never aborts the whole refresh — the remote
// row is still folded in, just with a zero settlement_date.
func parseRFC3339OrZero(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		log.Printf("otccache(options): bad RFC3339 settlement_date %q: %v (using zero time)", s, err)
		return time.Time{}
	}
	return t
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
