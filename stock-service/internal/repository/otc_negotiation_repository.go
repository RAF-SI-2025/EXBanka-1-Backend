// Package repository — OTCNegotiationRepository persists per-bidder
// negotiation chains against parent OTCOffer listings, plus the
// append-only OTCNegotiationRevision history.
package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// ChainAggregate is the per-parent active-chain summary that powers
// the marketplace listing's best-bid / best-ask surface. The caller
// picks BestBid for sell_initiated parents (buyers compete upward) or
// BestAsk for buy_initiated parents (sellers compete downward); both
// are computed in one query so the caller doesn't dispatch twice.
type ChainAggregate struct {
	BestBid     decimal.Decimal
	BestAsk     decimal.Decimal
	ActiveCount int32
}

type OTCNegotiationRepository struct {
	db *gorm.DB
}

func NewOTCNegotiationRepository(db *gorm.DB) *OTCNegotiationRepository {
	return &OTCNegotiationRepository{db: db}
}

func (r *OTCNegotiationRepository) DB() *gorm.DB { return r.db }

// Create inserts a new negotiation row. Use inside a transaction when also
// inserting the matching revision so the chain is atomically created.
func (r *OTCNegotiationRepository) Create(n *model.OTCNegotiation) error {
	return r.db.Create(n).Error
}

func (r *OTCNegotiationRepository) CreateTx(tx *gorm.DB, n *model.OTCNegotiation) error {
	return tx.Create(n).Error
}

func (r *OTCNegotiationRepository) GetByID(id uint64) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := r.db.First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &n, err
}

// LockByID does SELECT FOR UPDATE inside an active transaction. Required
// before any state mutation (counter/accept/reject/cancel) so concurrent
// operations on the same chain serialize correctly.
//
// Guard: remote rows (local == false) are treated as not-found so they can
// never enter the local accept/cancel/reject paths.
func (r *OTCNegotiationRepository) LockByID(tx *gorm.DB, id uint64) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if !n.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &n, nil
}

// Save persists a modified negotiation. Optimistic-locked via the
// BeforeUpdate hook. Returns ErrOptimisticLock if RowsAffected == 0.
func (r *OTCNegotiationRepository) Save(n *model.OTCNegotiation) error {
	res := r.db.Save(n)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

func (r *OTCNegotiationRepository) SaveTx(tx *gorm.DB, n *model.OTCNegotiation) error {
	res := tx.Save(n)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByParentOffer returns all chains against a given parent listing.
// Used to surface "current bids" on an offer detail view, AND to drive
// the cascade-cancel step when one chain accepts.
//
// Guard: only local chains (local == true) are returned. Remote chains must
// not be cascade-cancelled by a local accept.
func (r *OTCNegotiationRepository) ListByParentOffer(parentOfferID uint64) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	err := r.db.Where("parent_offer_id = ? AND local = ?", parentOfferID, true).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

// ListOpenByParentOfferForUpdate locks every still-open chain on the
// parent. Used by the accept transaction to cascade-cancel siblings
// after the winning chain transitions to "accepted".
//
// Guard: only local chains (local == true) are locked. Remote chains under a
// shared parent_offer_id must not be affected by a local cascade-cancel.
func (r *OTCNegotiationRepository) ListOpenByParentOfferForUpdate(tx *gorm.DB, parentOfferID uint64) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	openStatuses := []string{
		model.OTCNegotiationStatusOpen,
		model.OTCNegotiationStatusCountered,
	}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("parent_offer_id = ? AND status IN ? AND local = ?",
			parentOfferID, openStatuses, true).
		Find(&out).Error
	return out, err
}

// ListByBidder returns chains where the caller is the bidder. Used by
// GET /api/v3/me/otc/options (the "bids I placed" half of the view).
//
// Defense-in-depth (Fix #3, 2026-05-16): also requires bidder_bank_code
// IS NULL — cross-bank bids live in peer_otc_negotiations and never
// populate this column, so the filter is a no-op today. If a future
// path ever writes bidder_bank_code, this query stays safe by excluding
// foreign-bank rows (it would NOT silently leak Bank B's client-1 to
// Bank A's client-1 with the same numeric id).
//
// Guard: local == true ensures remote rows folded into the unified table
// (Tasks 4-6) never appear in a local bidder's view.
func (r *OTCNegotiationRepository) ListByBidder(
	ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int,
) ([]model.OTCNegotiation, int64, error) {
	q := r.db.Model(&model.OTCNegotiation{}).
		Where("bidder_owner_type = ?", ownerType).
		Where("bidder_bank_code IS NULL").
		Where("local = ?", true)
	if ownerType == model.OwnerClient {
		q = q.Where("bidder_owner_id = ?", ownerID)
	} else {
		q = q.Where("bidder_owner_id IS NULL")
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	var out []model.OTCNegotiation
	if err := q.Order("created_at DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// FindChainByBidder returns the (parent_offer_id, bidder) chain row if it
// exists, or ErrRecordNotFound. Used to enforce the "one chain per bidder
// per listing" invariant at chain-open time.
func (r *OTCNegotiationRepository) FindChainByBidder(
	parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	return r.findChainByBidder(r.db, parentOfferID, bidderOwnerType, bidderOwnerID)
}

// FindChainByBidderTx variant for use inside an active transaction. The
// non-Tx version uses r.db which acquires a fresh connection and would
// deadlock under single-connection backends (sqlite :memory:) when the
// caller already holds the TX's connection.
func (r *OTCNegotiationRepository) FindChainByBidderTx(
	tx *gorm.DB, parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	return r.findChainByBidder(tx, parentOfferID, bidderOwnerType, bidderOwnerID)
}

func (r *OTCNegotiationRepository) findChainByBidder(
	db *gorm.DB, parentOfferID uint64, bidderOwnerType model.OwnerType, bidderOwnerID *uint64,
) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	// Guard: restrict to local chains (local == true) so a remote chain for the
	// same (parent, bidder) tuple can never trigger a false "one chain already
	// exists" rejection for a local bidder.
	q := db.Where("parent_offer_id = ? AND bidder_owner_type = ? AND local = ?",
		parentOfferID, bidderOwnerType, true)
	if bidderOwnerType == model.OwnerClient {
		q = q.Where("bidder_owner_id = ?", bidderOwnerID)
	} else {
		q = q.Where("bidder_owner_id IS NULL")
	}
	err := q.First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &n, err
}

// ------- Revisions -------

func (r *OTCNegotiationRepository) AppendRevision(rev *model.OTCNegotiationRevision) error {
	return r.db.Create(rev).Error
}

func (r *OTCNegotiationRepository) AppendRevisionTx(tx *gorm.DB, rev *model.OTCNegotiationRevision) error {
	return tx.Create(rev).Error
}

// ListRevisions returns the full revision history for a chain, ordered by
// revision_number ascending.
func (r *OTCNegotiationRepository) ListRevisions(negotiationID uint64) ([]model.OTCNegotiationRevision, error) {
	var out []model.OTCNegotiationRevision
	err := r.db.Where("negotiation_id = ?", negotiationID).
		Order("revision_number ASC").Find(&out).Error
	return out, err
}

// LatestRevisionByAuthorForOffer returns the most recent OTCNegotiationRevision
// AUTHORED BY the given principal across all chains under a local parent offer —
// i.e. "the owner's latest counter" for the offer-row term projection. Returns
// (nil, nil) when that principal never authored a revision on the offer.
//
// Author is matched on the audit columns (modified_by_principal_type /
// modified_by_principal_id); ordering is created_at DESC with an id DESC
// tie-break so the result is deterministic when two revisions share a timestamp.
func (r *OTCNegotiationRepository) LatestRevisionByAuthorForOffer(offerID uint64, principalType string, principalID uint64) (*model.OTCNegotiationRevision, error) {
	var rev model.OTCNegotiationRevision
	err := r.db.
		Joins("JOIN otc_negotiations n ON n.id = otc_negotiation_revisions.negotiation_id").
		Where("n.parent_offer_id = ? AND otc_negotiation_revisions.modified_by_principal_type = ? AND otc_negotiation_revisions.modified_by_principal_id = ?",
			offerID, principalType, principalID).
		Order("otc_negotiation_revisions.created_at DESC, otc_negotiation_revisions.id DESC").
		First(&rev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// NextRevisionNumber returns the next sequential revision number for the
// chain. Must be called inside the same TX that inserts the revision so
// the (negotiation_id, revision_number) unique index serializes.
func (r *OTCNegotiationRepository) NextRevisionNumber(tx *gorm.DB, negotiationID uint64) (int, error) {
	var current int
	err := tx.Model(&model.OTCNegotiationRevision{}).
		Where("negotiation_id = ?", negotiationID).
		Select("COALESCE(MAX(revision_number), 0)").Row().Scan(&current)
	if err != nil {
		return 0, err
	}
	return current + 1, nil
}

// AggregateActiveBidsByOffer summarises every parent's active-chain
// pricing for the marketplace best-bid / best-ask surface. One query,
// GROUP BY parent_offer_id, filters status IN ('open','countered').
// Parents with zero active chains are absent from the result map (NOT
// keyed with zero values) so the caller can distinguish "no
// competition" from "competition at zero". Empty input ⇒ empty map.
func (r *OTCNegotiationRepository) AggregateActiveBidsByOffer(offerIDs []uint64) (map[uint64]ChainAggregate, error) {
	out := map[uint64]ChainAggregate{}
	if len(offerIDs) == 0 {
		return out, nil
	}
	type row struct {
		ParentOfferID uint64
		BestBid       decimal.Decimal
		BestAsk       decimal.Decimal
		ActiveCount   int32
	}
	var rows []row
	err := r.db.Model(&model.OTCNegotiation{}).
		Select("parent_offer_id, MAX(premium) AS best_bid, MIN(premium) AS best_ask, COUNT(*) AS active_count").
		Where("parent_offer_id IN ? AND status IN ? AND local = ?", offerIDs, []string{
			model.OTCNegotiationStatusOpen,
			model.OTCNegotiationStatusCountered,
		}, true).
		Group("parent_offer_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ParentOfferID] = ChainAggregate{
			BestBid:     r.BestBid,
			BestAsk:     r.BestAsk,
			ActiveCount: r.ActiveCount,
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// REMOTE (cross-bank) negotiation rows (SP-2a).
//
// These methods are the unified-table replacement for the retired
// peer-OTC-negotiation mirror repository. A remote chain lives in otc_negotiations with
// routing_number = <peer routing> and native_id = <peer foreign id>; the
// cross-bank party/offer data is carried in the Remote* columns + the shared
// Status column (which holds the PEER status vocabulary on remote rows).
//
// EVERY method below scopes to local == false (and, when the peer routing is
// known, ALSO matches that explicit peer routing as natural-key data). This is
// the SAME guarantee the local-path queries get from their local == true
// guards: a local row can NEVER satisfy a remote query, and a remote row can
// NEVER satisfy a local one — so folding the two tables together does not leak
// remote chains into local accept/cascade/exercise.
// ---------------------------------------------------------------------------

// UpsertRemoteNeg inserts or updates a remote negotiation keyed on the natural
// key (routing_number, native_id). Mirrors the retired Upsert: party metadata +
// offer JSON are refreshed; a non-empty Status overwrites, an empty one is
// preserved. The caller MUST have set RoutingNumber to the peer's routing and
// NativeID to the peer foreign id. Uses ON CONFLICT (never SELECT-then-INSERT)
// per the Concurrency requirement.
func (r *OTCNegotiationRepository) UpsertRemoteNeg(n *model.OTCNegotiation) error {
	updates := []string{
		"remote_buyer_routing", "remote_buyer_id",
		"remote_seller_routing", "remote_seller_id",
		"remote_offer_json", "remote_parent_routing", "remote_parent_native_id",
		"acting_employee_id", "updated_at",
	}
	if n.Status != "" {
		updates = append(updates, "status")
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "routing_number"}, {Name: "native_id"}},
		DoUpdates: clause.AssignmentColumns(updates),
	}).Create(n).Error
}

// GetRemoteNegByID loads a REMOTE negotiation by its surrogate primary key.
// A LOCAL chain (local == true) is treated as not-found here, so a local
// surrogate id never resolves through the remote dispatch path. Mirrors
// OTCOfferRepository.GetRemoteByID (SP-2b Task 4).
func (r *OTCNegotiationRepository) GetRemoteNegByID(id uint64) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	if err := r.db.First(&n, id).Error; err != nil {
		return nil, err
	}
	if n.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &n, nil
}

// GetRemoteNegByRoutingAndNative loads a remote negotiation by its (peer
// routing, native id) natural key. Returns ErrRecordNotFound when no remote row
// matches or when the matched row is local (local == true). The routing_number
// predicate is the natural key (DATA); local is the remote discriminator.
func (r *OTCNegotiationRepository) GetRemoteNegByRoutingAndNative(routing int64, native string) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := r.db.Where("routing_number = ? AND native_id = ? AND local = ?",
		routing, native, false).First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// GetRemoteNegByNative looks up a remote negotiation by native_id ALONE (the
// negotiation UUID, identical on both banks and unique per bank). Used by the
// accept money-leg validator, which can run on either side and so cannot trust
// the peer bank code. Scoped to local == false so a local row with a colliding
// native id (there is none today) could never be returned.
func (r *OTCNegotiationRepository) GetRemoteNegByNative(native string) (*model.OTCNegotiation, error) {
	var n model.OTCNegotiation
	err := r.db.Where("native_id = ? AND local = ?", native, false).First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// UpdateRemoteNegOffer refreshes the remote offer JSON for a (peer routing,
// native id) pair. No-op match count is not an error (mirrors the retired
// UpdateOffer, which used a bare Updates).
//
// SkipHooks: this is a targeted column UPDATE on a REMOTE row. The model's
// BeforeSave runs ValidateOwner against the (zero-value) struct's bidder
// columns and the BeforeUpdate adds a version guard — neither is meaningful for
// a column-scoped remote-row flip (remote rows carry OwnerBank/nil and aren't
// version-tracked through this path), and BeforeSave would spuriously reject
// with "invalid owner_type". Mirrors the retired peer-repo's bare-Updates
// behaviour. The routing guard keeps this off all local rows.
func (r *OTCNegotiationRepository) UpdateRemoteNegOffer(routing int64, native, offerJSON string) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCNegotiation{}).
		Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
		Updates(map[string]any{"remote_offer_json": offerJSON, "updated_at": time.Now().UTC()}).Error
}

// UpdateRemoteNegStatus sets the status for a (peer routing, native id) pair.
// SkipHooks for the same reason as UpdateRemoteNegOffer.
func (r *OTCNegotiationRepository) UpdateRemoteNegStatus(routing int64, native, status string) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCNegotiation{}).
		Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
		Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()}).Error
}

// CompareAndSetRemoteNegStatus atomically transitions a remote negotiation from
// `from` to `to` in one guarded UPDATE, returning true iff exactly one row
// matched. Same semantics as the retired CompareAndSetStatus: serialises
// concurrent accepts so only one wins and dispatches the option-formation SI-TX.
// SkipHooks for the same reason as UpdateRemoteNegOffer.
func (r *OTCNegotiationRepository) CompareAndSetRemoteNegStatus(routing int64, native, from, to string) (bool, error) {
	res := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCNegotiation{}).
		Where("routing_number = ? AND native_id = ? AND status = ? AND local = ?",
			routing, native, from, false).
		Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ListRemoteNegBySellerAndParent returns every ongoing REMOTE chain under the
// given seller whose remote parent grouping matches the supplied (routing, id)
// tuple. Phase-10 cross-bank cascade-cancel. Free-form chains (NULL parent) are
// excluded by the IS NOT NULL guard inherent in the equality match.
func (r *OTCNegotiationRepository) ListRemoteNegBySellerAndParent(
	sellerRouting int64, sellerID string, parentRouting int64, parentNative string,
) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	err := r.db.Where(
		"remote_seller_routing = ? AND remote_seller_id = ? AND status = ? AND remote_parent_routing = ? AND remote_parent_native_id = ? AND local = ?",
		sellerRouting, sellerID, "ongoing", parentRouting, parentNative, false).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

// ListRemoteNegByParent returns every ongoing REMOTE chain grouped under the
// given remote parent listing (routing, native id), regardless of which party
// is the seller. Used by the listing-cancel cascade so a poster cancelling a
// cross-bank listing terminates ALL its remote child chains (not just the local
// ones, which key on the numeric parent_offer_id). Free-form chains (NULL
// parent) are excluded by the equality match.
func (r *OTCNegotiationRepository) ListRemoteNegByParent(
	parentRouting int64, parentNative string,
) ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	err := r.db.Where(
		"status = ? AND remote_parent_routing = ? AND remote_parent_native_id = ? AND local = ?",
		"ongoing", parentRouting, parentNative, false).
		Order("created_at ASC").Find(&out).Error
	return out, err
}

// ListRemoteNegByClient returns remote rows where the caller's bank hosts a
// party matching (ownRouting, clientPrincipal). clientPrincipal is the wire id
// ("client-<N>"); role narrows to "buyer", "seller" or "" / "both". Scoped to
// local == false so only remote chains are returned.
func (r *OTCNegotiationRepository) ListRemoteNegByClient(ownRouting int64, clientPrincipal, role string) ([]model.OTCNegotiation, error) {
	q := r.db.Model(&model.OTCNegotiation{}).Where("local = ?", false)
	switch role {
	case "buyer":
		q = q.Where("remote_buyer_routing = ? AND remote_buyer_id = ?", ownRouting, clientPrincipal)
	case "seller":
		q = q.Where("remote_seller_routing = ? AND remote_seller_id = ?", ownRouting, clientPrincipal)
	default:
		q = q.Where(
			"(remote_buyer_routing = ? AND remote_buyer_id = ?) OR (remote_seller_routing = ? AND remote_seller_id = ?)",
			ownRouting, clientPrincipal, ownRouting, clientPrincipal,
		)
	}
	var out []model.OTCNegotiation
	err := q.Order("updated_at DESC").Find(&out).Error
	return out, err
}

// ListRemoteNegByBankParty returns REMOTE negotiation chains where the side WE
// host (routing == ownRouting) is the BANK (party id has the "employee-" prefix).
// role: "buyer" → we host the bank as buyer (our cross-bank bids); "seller" → we
// host the bank as the offer's writer (peer bids on our bank-owned offer); "" →
// either. The bank has no single wire principal, so this matches by prefix, not
// exact id.
//
// Scoped to local == false (same remote scope as ListRemoteNegByClient) so
// only chains WE host as the bank come back — never a peer-hosted bank side,
// and never a local row. The "employee-%" LIKE pattern is a constant prefix (no
// user input), so there is no injection risk.
func (r *OTCNegotiationRepository) ListRemoteNegByBankParty(ownRouting int64, role string) ([]model.OTCNegotiation, error) {
	q := r.db.Model(&model.OTCNegotiation{}).Where("local = ?", false)
	switch role {
	case "buyer":
		q = q.Where("remote_buyer_routing = ? AND remote_buyer_id LIKE ?", ownRouting, "employee-%")
	case "seller":
		q = q.Where("remote_seller_routing = ? AND remote_seller_id LIKE ?", ownRouting, "employee-%")
	default:
		q = q.Where("(remote_buyer_routing = ? AND remote_buyer_id LIKE ?) OR (remote_seller_routing = ? AND remote_seller_id LIKE ?)",
			ownRouting, "employee-%", ownRouting, "employee-%")
	}
	var out []model.OTCNegotiation
	err := q.Order("updated_at DESC").Find(&out).Error
	return out, err
}

// ListRemoteNegOngoing returns every REMOTE negotiation whose status is
// "ongoing". Used by the safety-net reconciler to find rows that may have
// missed a peer-driven cancel webhook.
func (r *OTCNegotiationRepository) ListRemoteNegOngoing() ([]model.OTCNegotiation, error) {
	var out []model.OTCNegotiation
	err := r.db.Where("status = ? AND local = ?", "ongoing", false).
		Order("updated_at ASC").Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// Remote chain revision logging (full-history parity with local chains).
//
// Each REMOTE mutation is paired with a revision append in the SAME transaction
// so a remote chain accumulates the same per-move history (BID/COUNTER/ACCEPT/
// REJECT) that a local chain does. The rev template the caller passes carries the
// terms, Action, ModifiedByPrincipalType (role: "buyer"/"seller"), and
// RemoteActorWireID; this layer fills NegotiationID + RevisionNumber.
// ---------------------------------------------------------------------------

// txGetRemoteNeg loads a remote chain by its (peer routing, native id) natural key
// inside tx.
func txGetRemoteNeg(tx *gorm.DB, routing int64, native string) (*model.OTCNegotiation, error) {
	var row model.OTCNegotiation
	err := tx.Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// appendRemoteRevisionTx appends rev to chain negID inside tx, numbering it with
// NextRevisionNumber so the (negotiation_id, revision_number) unique index
// serializes concurrent appends.
func (r *OTCNegotiationRepository) appendRemoteRevisionTx(tx *gorm.DB, negID uint64, rev *model.OTCNegotiationRevision) error {
	n, err := r.NextRevisionNumber(tx, negID)
	if err != nil {
		return err
	}
	rev.NegotiationID = negID
	rev.RevisionNumber = n
	return tx.Create(rev).Error
}

// lastRevisionTx returns the most recent revision for negID, or (nil, nil) if none.
func (r *OTCNegotiationRepository) lastRevisionTx(tx *gorm.DB, negID uint64) (*model.OTCNegotiationRevision, error) {
	var rev model.OTCNegotiationRevision
	err := tx.Where("negotiation_id = ?", negID).Order("revision_number DESC").First(&rev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// sameRevisionMove reports whether a (the last recorded revision) and b (the move
// about to be recorded) are the SAME move — equal action, terms, and remote actor
// wire id. Used to drop retried webhooks while still recording a legitimate
// same-terms counter by the OTHER party (different wire id).
func sameRevisionMove(a, b *model.OTCNegotiationRevision) bool {
	if a == nil {
		return false
	}
	aw, bw := "", ""
	if a.RemoteActorWireID != nil {
		aw = *a.RemoteActorWireID
	}
	if b.RemoteActorWireID != nil {
		bw = *b.RemoteActorWireID
	}
	return a.Action == b.Action && aw == bw &&
		a.Quantity.Equal(b.Quantity) && a.StrikePrice.Equal(b.StrikePrice) &&
		a.Premium.Equal(b.Premium) && a.SettlementDate.Equal(b.SettlementDate)
}

// UpsertRemoteNegWithRevision upserts a remote chain (same as UpsertRemoteNeg) and,
// when the chain has NO revisions yet, appends the supplied BID revision — all in
// one TX. A retried create (chain already carries a BID) is a no-op for the
// revision, keeping inbound/outbound bid delivery idempotent.
func (r *OTCNegotiationRepository) UpsertRemoteNegWithRevision(n *model.OTCNegotiation, rev *model.OTCNegotiationRevision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txr := &OTCNegotiationRepository{db: tx}
		if err := txr.UpsertRemoteNeg(n); err != nil {
			return err
		}
		row, err := txGetRemoteNeg(tx, n.RoutingNumber, derefNativeID(n.NativeID))
		if err != nil {
			return err
		}
		last, err := r.lastRevisionTx(tx, row.ID)
		if err != nil {
			return err
		}
		if last != nil {
			return nil // already has history → retried create, no-op
		}
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
}

// UpdateRemoteNegOfferWithRevision updates the remote offer JSON and appends a
// COUNTER revision, unless the chain's latest revision is the SAME move (a retry).
func (r *OTCNegotiationRepository) UpdateRemoteNegOfferWithRevision(routing int64, native, offerJSON string, rev *model.OTCNegotiationRevision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
			Updates(map[string]any{"remote_offer_json": offerJSON, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		last, err := r.lastRevisionTx(tx, row.ID)
		if err != nil {
			return err
		}
		if sameRevisionMove(last, rev) {
			return nil // retried counter → no-op
		}
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
}

// CompareAndSetRemoteNegStatusWithRevision CASes status from→to and, only when the
// CAS matched exactly one row (a real transition), appends rev. Returns whether it
// transitioned. Idempotent: a second call matches 0 rows → no revision.
func (r *OTCNegotiationRepository) CompareAndSetRemoteNegStatusWithRevision(routing int64, native, from, to string, rev *model.OTCNegotiationRevision) (bool, error) {
	var transitioned bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		res := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND status = ? AND local = ?", routing, native, from, false).
			Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return nil
		}
		transitioned = true
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
	return transitioned, err
}

// SetRemoteNegStatusWithRevision flips status to `to` and appends rev only when the
// chain is currently NON-terminal (status "ongoing") — a real party-driven terminal
// move. Idempotent: a second call on an already-terminal chain is a no-op.
func (r *OTCNegotiationRepository) SetRemoteNegStatusWithRevision(routing int64, native, to string, rev *model.OTCNegotiationRevision) (bool, error) {
	var transitioned bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		if row.Status != "ongoing" {
			return nil
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
			Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		transitioned = true
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
	return transitioned, err
}

// AppendRemoteRevision appends rev to the remote chain (routing, native),
// idempotently skipping a retry whose latest revision is the SAME move. Used on
// the INBOUND-accept SUCCESS path: the status was already CAS-claimed to
// "accepted" at the concurrency-claim point, so the ACCEPT revision is recorded
// only once the accept truly committed — never on a rolled-back claim.
func (r *OTCNegotiationRepository) AppendRemoteRevision(routing int64, native string, rev *model.OTCNegotiationRevision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		last, err := r.lastRevisionTx(tx, row.ID)
		if err != nil {
			return err
		}
		if sameRevisionMove(last, rev) {
			return nil
		}
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
}

// derefNativeID returns the string value of a NativeID pointer, or "" if nil.
func derefNativeID(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
