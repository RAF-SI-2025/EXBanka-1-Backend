package repository

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OTCOfferRepository struct {
	db *gorm.DB
}

func NewOTCOfferRepository(db *gorm.DB) *OTCOfferRepository {
	return &OTCOfferRepository{db: db}
}

func (r *OTCOfferRepository) DB() *gorm.DB { return r.db }

// MergeDuplicateOpenOffers collapses pre-existing duplicate OPEN LOCAL offers
// sharing (initiator_owner_id, ticker, direction) into the oldest row (summing
// quantity) and marks the rest consumed, so the partial unique index can be
// created. Idempotent: a second run is a no-op. Returns the number consumed.
//
// Status set: the full open-listing set ('open','PENDING','COUNTERED'), matching
// the partial unique index predicate. New LOCAL offers are created PENDING (never
// 'open'), so a status='open'-only filter would never see a real-world duplicate
// and the migration would be inert on deploy — and worse, the unique index would
// then fail to build over pre-existing PENDING duplicates. Collapsing every
// open-listing status keeps the merge a true precondition for the index.
//
// Column updates (UpdateColumn) intentionally bypass the optimistic-lock
// BeforeUpdate hook: that hook appends `WHERE version = ?` keyed off the
// zero-value model's Version (0), which would silently no-op rows whose version
// has advanced past 0. This is a one-shot migration that must apply to the
// exact id regardless of the live version.
func (r *OTCOfferRepository) MergeDuplicateOpenOffers() (int64, error) {
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	var consumed int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.OTCOffer
		if err := tx.Where("status IN ? AND local = ?", openStatuses, true).
			Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		type key struct {
			owner uint64
			tick  string
			dir   string
		}
		keep := map[key]*model.OTCOffer{}
		for i := range rows {
			o := &rows[i]
			if o.InitiatorOwnerID == nil {
				continue
			}
			k := key{*o.InitiatorOwnerID, o.Ticker, o.Direction}
			if first, ok := keep[k]; ok {
				first.Quantity = first.Quantity.Add(o.Quantity)
				res := tx.Model(&model.OTCOffer{}).Where("id = ?", first.ID).
					UpdateColumn("quantity", first.Quantity)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return errors.New("otc merge: quantity update did not apply")
				}
				res = tx.Model(&model.OTCOffer{}).Where("id = ?", o.ID).
					UpdateColumn("status", model.OTCOfferStatusConsumed)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return errors.New("otc merge: status update did not apply")
				}
				consumed++
			} else {
				cp := *o
				keep[k] = &cp
			}
		}
		return nil
	})
	return consumed, err
}

func (r *OTCOfferRepository) Create(o *model.OTCOffer) error {
	return r.db.Create(o).Error
}

// CountOpenByOwnerTickerDirection counts this bank's OPEN LOCAL offers for the
// (owner, ticker, direction) triple — the partial-unique-index key. Used to
// reject a duplicate before insert (friendlier than relying on the DB error).
//
// New offers are created with the legacy status PENDING (the negotiation-thread
// vocabulary). This counts every IsOpenListing status ('open','PENDING',
// 'COUNTERED'), matching the DB partial unique index predicate exactly. This
// service check is the friendly fast path (returns ErrOTCOfferDuplicateOpen
// before the insert); the DB index over the SAME status set is the authoritative
// backstop that closes the non-transactional gap between this count and the
// insert.
func (r *OTCOfferRepository) CountOpenByOwnerTickerDirection(ownerType model.OwnerType, ownerID *uint64, ticker, direction string) (int64, error) {
	var n int64
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	q := r.db.Model(&model.OTCOffer{}).
		Where("status IN ? AND local = ? AND ticker = ? AND direction = ? AND initiator_owner_type = ?",
			openStatuses, true, ticker, direction, ownerType)
	if ownerID != nil {
		q = q.Where("initiator_owner_id = ?", *ownerID)
	} else {
		q = q.Where("initiator_owner_id IS NULL")
	}
	return n, q.Count(&n).Error
}

// ConsumeOpenByOwnerTickerDirection flips this bank's OPEN LOCAL offer for the
// (owner, ticker, direction) triple to consumed. It matches the same
// IsOpenListing status set ('open','PENDING','COUNTERED') as the partial unique
// index, so it targets exactly the at-most-one open listing for that key. Used
// by the cross-bank accept path (Direction 2: we host the seller) to remove a
// listing from the marketplace once an option contract forms against it — the
// termless /public-stock model carries no offer id on the wire, so the listing
// is resolved by its unique key rather than by parent_offer_id.
//
// Idempotent: 0 rows affected (already consumed / cancelled / never existed) is
// NOT an error. UpdateColumn (hooks skipped) is used because this is a terminal
// status flip targeted by WHERE, not a load-modify-Save subject to the
// optimistic-version contract — mirroring MergeDuplicateOpenOffers.
func (r *OTCOfferRepository) ConsumeOpenByOwnerTickerDirection(ownerType model.OwnerType, ownerID *uint64, ticker, direction string) error {
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	q := r.db.Model(&model.OTCOffer{}).
		Where("status IN ? AND local = ? AND ticker = ? AND direction = ? AND initiator_owner_type = ?",
			openStatuses, true, ticker, direction, ownerType)
	if ownerID != nil {
		q = q.Where("initiator_owner_id = ?", *ownerID)
	} else {
		q = q.Where("initiator_owner_id IS NULL")
	}
	return q.UpdateColumn("status", model.OTCOfferStatusConsumed).Error
}

// GetOpenSellListingForUpdate SELECT-FOR-UPDATEs this bank's single OPEN LOCAL
// sell-initiated listing for (ownerType, ownerID, ticker) — the same (owner,
// ticker, direction) key + open-status set as ConsumeOpenByOwnerTickerDirection,
// so it resolves the at-most-one open row guaranteed by the partial unique index.
// The cross-bank accept path uses it to read the listing's quantity BEFORE
// consuming it, so it can re-list the unsold remainder atomically in the same TX.
func (r *OTCOfferRepository) GetOpenSellListingForUpdate(tx *gorm.DB, ownerType model.OwnerType, ownerID *uint64, ticker string) (*model.OTCOffer, error) {
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("status IN ? AND local = ? AND ticker = ? AND direction = ? AND initiator_owner_type = ?",
			openStatuses, true, ticker, model.OTCDirectionSellInitiated, ownerType)
	if ownerID != nil {
		q = q.Where("initiator_owner_id = ?", *ownerID)
	} else {
		q = q.Where("initiator_owner_id IS NULL")
	}
	var o model.OTCOffer
	if err := q.First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OTCOfferRepository) GetByID(id uint64) (*model.OTCOffer, error) {
	return r.getByID(r.db, id)
}

// GetByIDTx variant for use inside an active transaction (avoids
// acquiring a second connection that would deadlock with the TX under
// single-connection backends such as sqlite :memory: in tests).
func (r *OTCOfferRepository) GetByIDTx(tx *gorm.DB, id uint64) (*model.OTCOffer, error) {
	return r.getByID(tx, id)
}

func (r *OTCOfferRepository) getByID(db *gorm.DB, id uint64) (*model.OTCOffer, error) {
	var o model.OTCOffer
	err := db.First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &o, err
}

// Save persists a modified offer. Optimistic-locked via the BeforeUpdate hook.
func (r *OTCOfferRepository) Save(o *model.OTCOffer) error {
	res := r.db.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// LockByIDTx does SELECT FOR UPDATE inside an active transaction. Required
// by the first-accept-wins TX in OTCNegotiationService so two parallel
// AcceptNegotiation calls on the same parent serialize: the second one
// waits for the first to commit, then sees parent.Status != open and
// rejects with ErrOTCParentNotOpen.
//
// Guard: remote rows (local == false) are treated as not-found so they can
// never enter the local money/accept paths.
func (r *OTCOfferRepository) LockByIDTx(tx *gorm.DB, id uint64) (*model.OTCOffer, error) {
	var o model.OTCOffer
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if !o.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &o, nil
}

// SaveTx variant for use inside an existing transaction.
func (r *OTCOfferRepository) SaveTx(tx *gorm.DB, o *model.OTCOffer) error {
	res := tx.Save(o)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListOpenForCache returns every OTCOffer currently accepting bids —
// status open/PENDING/COUNTERED AND counterparty_owner_id IS NULL.
// Used by the cross-bank discovery cache (Phase 6) and the peer-facing
// GET /api/v3/public-option-offers endpoint. Both must agree on the
// SAME filter so peers see what local discovery sees.
//
// limit caps the result so a runaway listing pool can't OOM the
// process; caller can pass a large number to effectively disable it.
//
// Guard: only local rows (local == true) are returned. Remote rows folded
// into the unified table (Tasks 4-6) must not be re-published to peers as if
// they originated here.
func (r *OTCOfferRepository) ListOpenForCache(limit int) ([]model.OTCOffer, error) {
	if limit <= 0 {
		limit = 1000
	}
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	var out []model.OTCOffer
	err := r.db.Where("status IN ? AND counterparty_owner_id IS NULL AND local = ?",
		openStatuses, true).
		Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// ListPublicOptionOffersForPeer returns this bank's OPEN, sell-initiated, public,
// non-private, LOCAL option offers — the "optionable inventory" exposed to peer
// banks on the SI-TX /public-stock endpoint (PeerOTCGRPCHandler.GetPublicStocks).
// One row per (owner, ticker, direction): the partial unique index already
// guarantees one open sell offer per that triple, so no aggregation is needed.
//
// Status predicate: the SAME open-listing set ListOpenForCache uses
// ('open','PENDING','COUNTERED'). New sell offers are created with the legacy
// status PENDING (IsOpenListing treats open/PENDING/COUNTERED as open), so a
// strict status='open' filter would MISS freshly-created offers and peers would
// see an empty catalog. Mirroring ListOpenForCache's predicate keeps peer
// /public-stock discovery in agreement with local /public-option-offers
// discovery (both read the same open set).
func (r *OTCOfferRepository) ListPublicOptionOffersForPeer() ([]model.OTCOffer, error) {
	openStatuses := []string{
		model.OTCOfferStatusOpen,
		model.OTCOfferStatusPending,
		model.OTCOfferStatusCountered,
	}
	var rows []model.OTCOffer
	err := r.db.Where(
		"status IN ? AND local = ? AND direction = ? AND public = ? AND private = ?",
		openStatuses, true, model.OTCDirectionSellInitiated, true, false,
	).Find(&rows).Error
	return rows, err
}

// UpsertRemote inserts or refreshes a REMOTE OTCOffer row keyed by the
// natural key (routing_number, native_id), stamping LastSeenAt and
// (re)opening the row. Returns the stable surrogate id. The caller MUST
// have set o.RoutingNumber to the peer's routing and o.NativeID to the
// peer's foreign offer id. Uses ON CONFLICT (never SELECT-then-INSERT) per
// the Concurrency requirement; the conflict target is the natural key.
//
// The peer routing is set explicitly on o, so BeforeCreate's own-routing
// stamping is a no-op here (it only fires for routing_number == 0).
func (r *OTCOfferRepository) UpsertRemote(o *model.OTCOffer, seenAt time.Time) (uint64, error) {
	o.Status = model.OTCOfferStatusOpen
	o.LastSeenAt = &seenAt
	err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "routing_number"}, {Name: "native_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"initiator_bank_code", "remote_seller_id", "direction", "ticker",
			"quantity", "status", "last_seen_at", "updated_at",
		}),
	}).Create(o).Error
	if err != nil {
		return 0, err
	}
	// Defensive only: current GORM populates o.ID on the DO-UPDATE path
	// (Postgres RETURNING / SQLite last_insert_rowid). Kept as a guard
	// against driver behavior changes — mirrors the retired remote repo.
	if o.ID == 0 {
		var row model.OTCOffer
		if e := r.db.Select("id").
			Where("routing_number = ? AND native_id = ?", o.RoutingNumber, o.NativeID).
			First(&row).Error; e != nil {
			return 0, e
		}
		o.ID = row.ID
	}
	return o.ID, nil
}

// UpsertRemoteShell upserts a /public-stock SHELL remote row (native_id
// "ps:..."). Shells are termless listings just like every other OTCOffer, so
// this is now a thin alias over UpsertRemote, retained as a named entry point
// for the shell-mirror callers and to keep the shell/option-offer call sites
// self-documenting.
func (r *OTCOfferRepository) UpsertRemoteShell(o *model.OTCOffer, seenAt time.Time) (uint64, error) {
	return r.UpsertRemote(o, seenAt)
}

// reconcileScoped flips open remote rows for peerRouting whose native_id is NOT
// in seenNativeIDs to cancelled, restricted to one native_id namespace.
// shellsOnly=true touches only "ps:%" rows (public-stock shells);
// shellsOnly=false touches only non-shell option-offer rows.
// Bulk update with SkipHooks (intentional non-versioned mass flip per the
// Concurrency requirement). peer offer counts are O(tens); NOT IN (...) is
// acceptable.
func (r *OTCOfferRepository) reconcileScoped(peerRouting int64, seenNativeIDs []string, shellsOnly bool) (int64, error) {
	q := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OTCOffer{}).
		Where("routing_number = ? AND status = ?", peerRouting, model.OTCOfferStatusOpen)
	like := model.RemoteStockShellPrefix + "%"
	if shellsOnly {
		q = q.Where("native_id LIKE ?", like)
	} else {
		q = q.Where("native_id NOT LIKE ?", like)
	}
	if len(seenNativeIDs) > 0 {
		q = q.Where("native_id NOT IN ?", seenNativeIDs)
	}
	res := q.Updates(map[string]any{"status": model.OTCOfferStatusCancelled, "updated_at": time.Now().UTC()})
	return res.RowsAffected, res.Error
}

// ReconcileRemoteNotSeen flips every open REMOTE option-offer row (non-shell
// namespace) for peerRouting whose native_id is NOT in seenNativeIDs to
// "cancelled", and returns the count flipped. MUST be called only after a
// SUCCESSFUL poll of that peer. A nil/empty seen slice means the peer listed
// nothing -> cancel all open option-offer rows for that peer.
//
// peerRouting is the peer's routing (!= OwnRouting(), guaranteed by the
// caller), so this never touches local rows. Shell rows (native_id LIKE "ps:%")
// are excluded — use ReconcileRemoteShellsNotSeen for the /public-stock
// namespace.
func (r *OTCOfferRepository) ReconcileRemoteNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error) {
	return r.reconcileScoped(peerRouting, seenNativeIDs, false)
}

// ReconcileRemoteShellsNotSeen flips every open REMOTE public-stock shell row
// (native_id LIKE "ps:%") for peerRouting whose native_id is NOT in
// seenNativeIDs to "cancelled", and returns the count flipped. MUST be called
// only after a SUCCESSFUL /public-stock poll of that peer. A nil/empty seen
// slice means the peer listed nothing -> cancel all open shell rows for that
// peer.
//
// Option-offer rows (non-shell namespace) are excluded — use
// ReconcileRemoteNotSeen for the /public-option-offers namespace.
func (r *OTCOfferRepository) ReconcileRemoteShellsNotSeen(peerRouting int64, seenNativeIDs []string) (int64, error) {
	return r.reconcileScoped(peerRouting, seenNativeIDs, true)
}

// GetRemoteByID returns a REMOTE OTCOffer row by surrogate id, or
// gorm.ErrRecordNotFound. A LOCAL offer (local == true) is treated as
// not-found here, so a local id never resolves through the remote-offer path.
func (r *OTCOfferRepository) GetRemoteByID(id uint64) (*model.OTCOffer, error) {
	var o model.OTCOffer
	if err := r.db.First(&o, id).Error; err != nil {
		return nil, err
	}
	if o.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &o, nil
}

// ListByOwner returns offers where the owner appears as initiator,
// counterparty, or either, optionally filtered by status (variadic) and
// stock_id (zero = no filter). owner_id may be nil for OwnerType=bank.
//
// Defense-in-depth (Fix #3, 2026-05-16): the matched side must have
// bank_code IS NULL (no writer populates these columns today; this
// filter prevents future cross-bank writes from leaking via owner_id
// collision). See OptionContractRepository.ListByOwner for the full
// rationale.
func (r *OTCOfferRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	q := r.db.Model(&model.OTCOffer{})
	switch role {
	case "initiator":
		q = scopeOwner(q, "initiator_owner_type", "initiator_owner_id", ownerType, ownerID).
			Where("initiator_bank_code IS NULL")
	case "counterparty":
		q = scopeOwner(q, "counterparty_owner_type", "counterparty_owner_id", ownerType, ownerID).
			Where("counterparty_bank_code IS NULL")
	default:
		// Match owner as either initiator OR counterparty. Inline the scopeOwner
		// expansion since we need an OR over two column-pair predicates. Each
		// side carries its own bank_code NULL guard so a foreign-counterparty
		// row never matches via the initiator-id predicate.
		if ownerID == nil {
			q = q.Where("(initiator_owner_type = ? AND initiator_owner_id IS NULL AND initiator_bank_code IS NULL) OR (counterparty_owner_type = ? AND counterparty_owner_id IS NULL AND counterparty_bank_code IS NULL)",
				ownerType, ownerType)
		} else {
			q = q.Where("(initiator_owner_type = ? AND initiator_owner_id = ? AND initiator_bank_code IS NULL) OR (counterparty_owner_type = ? AND counterparty_owner_id = ? AND counterparty_bank_code IS NULL)",
				ownerType, *ownerID, ownerType, *ownerID)
		}
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	if stockID != 0 {
		q = q.Where("stock_id = ?", stockID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}
	var out []model.OTCOffer
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// HistoryFilter narrows ListNegotiationHistory output.
type HistoryFilter struct {
	Statuses       []string   // default: terminal set (accepted/rejected/cancelled/expired) if empty
	Since          *time.Time // optional lower bound on updated_at
	Until          *time.Time // optional upper bound on updated_at
	CounterpartyID *uint64    // optional filter — caller must NOT also be this id
	Page           int        // 1-based; defaults to 1 if zero/negative
	PageSize       int        // bounded [1,100]; defaults to 20
}

// ListNegotiationHistory returns the caller's terminal OTC negotiations
// (accepted/rejected/cancelled/expired) with the supplied filters. The
// counterparty filter matches "owner_id appears on the OTHER side of the
// offer from the caller" — so a buyer querying for counterparty_id=X
// gets offers where the seller is X, and vice versa.
func (r *OTCOfferRepository) ListNegotiationHistory(ownerType model.OwnerType, ownerID *uint64, f HistoryFilter) ([]model.OTCOffer, int64, error) {
	// Local-only history: a bank/employee caller (OwnerBank, nil id) would
	// otherwise match folded-in remote offer rows (also OwnerBank/nil), so
	// scope to local rows (parity with the other local-only queries).
	q := r.db.Model(&model.OTCOffer{}).Where("local = ?", true)

	// Caller is one of the two parties — match either side.
	if ownerID == nil {
		q = q.Where("(initiator_owner_type = ? AND initiator_owner_id IS NULL) OR (counterparty_owner_type = ? AND counterparty_owner_id IS NULL)",
			ownerType, ownerType)
	} else {
		q = q.Where("(initiator_owner_type = ? AND initiator_owner_id = ?) OR (counterparty_owner_type = ? AND counterparty_owner_id = ?)",
			ownerType, *ownerID, ownerType, *ownerID)
	}

	// Default to the terminal set so "history" never accidentally
	// surfaces pending offers.
	statuses := f.Statuses
	if len(statuses) == 0 {
		// Terminal set per the OTCOfferStatus enum (cancellation isn't
		// modelled — withdrawn offers become REJECTED). FAILED is included
		// so an aborted accept-saga remains discoverable in history.
		statuses = []string{
			model.OTCOfferStatusAccepted,
			model.OTCOfferStatusRejected,
			model.OTCOfferStatusExpired,
			model.OTCOfferStatusFailed,
		}
	}
	q = q.Where("status IN ?", statuses)

	if f.Since != nil {
		q = q.Where("updated_at >= ?", *f.Since)
	}
	if f.Until != nil {
		q = q.Where("updated_at <= ?", *f.Until)
	}
	if f.CounterpartyID != nil {
		cpID := *f.CounterpartyID
		// Match counterparty on the side OPPOSITE the caller.
		q = q.Where(
			"(initiator_owner_type = ? AND initiator_owner_id = ? AND counterparty_owner_id = ?) OR (counterparty_owner_type = ? AND counterparty_owner_id = ? AND initiator_owner_id = ?)",
			ownerType, derefOr0(ownerID), cpID,
			ownerType, derefOr0(ownerID), cpID,
		)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	var out []model.OTCOffer
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

func derefOr0(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}

// OutstandingCommittedQuantityTx returns the total share quantity already
// committed to contract-forming negotiation chains on the given parent offer —
// i.e. Σ OTCNegotiation.Quantity over chains whose parent_offer_id == offerID
// and whose status is the contract-forming terminal status "accepted".
//
// PREDICATE RATIONALE ("committed" = the lower bound a quantity edit must not
// drop below): the marketplace is all-or-nothing per offer. A bidder opens a
// negotiation chain (status open/countered — a mere proposal the owner never
// agreed to, so NOT committed); the first chain to ACCEPT mints a contract and
// flips the parent listing to "consumed" (status the editor rejects via
// IsOpenListing). rejected/cancelled/expired chains carry no obligation.
// Therefore the ONLY chain status that represents a real, formed/forming
// obligation on this offer is "accepted", and we sum exactly those. Open and
// terminal-non-accepted chains are deliberately excluded.
//
// Consequence: for any editable (open) offer this sum is 0 (an accept would have
// consumed the offer, blocking the edit). It is the authoritative defense-in-
// depth lower bound regardless — it can never let an owner under-cut shares
// locked into a formed contract on this offer, and never spuriously blocks an
// edit on a genuinely open offer.
func (r *OTCOfferRepository) OutstandingCommittedQuantityTx(tx *gorm.DB, offerID uint64) (decimal.Decimal, error) {
	var rows []struct{ Sum decimal.Decimal }
	err := tx.Raw(`
		SELECT COALESCE(SUM(quantity), 0) AS sum
		  FROM otc_negotiations
		 WHERE parent_offer_id = ? AND status = ?`,
		offerID, model.OTCNegotiationStatusAccepted,
	).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return decimal.Zero, err
	}
	return rows[0].Sum, nil
}

// SumActiveQuantityForSeller returns Σ over (a) active option contracts
// where the seller matches, plus (b) PENDING/COUNTERED sell-initiated
// offers where the initiator is the seller, plus (c) PENDING/COUNTERED
// buy-initiated offers where the counterparty is the seller. Used by the
// seller-invariant check (§4.6 of spec). owner_id may be nil for bank
// owners; predicates emit IS NULL in that case.
func (r *OTCOfferRepository) SumActiveQuantityForSeller(sellerOwnerType model.OwnerType, sellerOwnerID *uint64, stockID uint64) (decimal.Decimal, error) {
	return r.sumActiveQuantityForSeller(r.db, sellerOwnerType, sellerOwnerID, stockID, 0)
}

// SumActiveQuantityForSellerExcludingOfferTx is SumActiveQuantityForSeller run
// inside tx with one OTCOffer id excluded from the offer (b)/(c) subqueries.
// Used by the edit-quantity path so the offer being resized does NOT count its
// OWN current quantity against the seller's holding (otherwise the available
// bound would be the holding minus the offer's current size rather than the
// full holding, wrongly rejecting an edit up to the holding). Active contracts
// (a) are never minted from an editable/open offer so they are not excluded.
func (r *OTCOfferRepository) SumActiveQuantityForSellerExcludingOfferTx(tx *gorm.DB, sellerOwnerType model.OwnerType, sellerOwnerID *uint64, stockID, excludeOfferID uint64) (decimal.Decimal, error) {
	return r.sumActiveQuantityForSeller(tx, sellerOwnerType, sellerOwnerID, stockID, excludeOfferID)
}

func (r *OTCOfferRepository) sumActiveQuantityForSeller(db *gorm.DB, sellerOwnerType model.OwnerType, sellerOwnerID *uint64, stockID, excludeOfferID uint64) (decimal.Decimal, error) {
	var rows []struct{ Sum decimal.Decimal }
	// excludeOfferID (0 = exclude nothing) keeps the offer being resized from
	// counting its own current quantity in the (b)/(c) offer subqueries.
	exclude := ""
	if excludeOfferID != 0 {
		exclude = " AND id <> ?"
	}
	var err error
	if sellerOwnerID == nil {
		args := []any{
			sellerOwnerType, stockID, model.OptionContractStatusActive,
			model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
		}
		if excludeOfferID != 0 {
			args = append(args, excludeOfferID)
		}
		args = append(args,
			model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
		)
		if excludeOfferID != 0 {
			args = append(args, excludeOfferID)
		}
		err = db.Raw(`
			SELECT COALESCE(SUM(q), 0) AS sum FROM (
				SELECT quantity AS q FROM option_contracts
				 WHERE seller_owner_type = ? AND seller_owner_id IS NULL
				   AND stock_id = ? AND status = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND initiator_owner_type = ? AND initiator_owner_id IS NULL
				   AND stock_id = ?`+exclude+`
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND counterparty_owner_type = ? AND counterparty_owner_id IS NULL
				   AND stock_id = ?`+exclude+`
			) AS t`, args...).Scan(&rows).Error
	} else {
		args := []any{
			sellerOwnerType, *sellerOwnerID, stockID, model.OptionContractStatusActive,
			model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, *sellerOwnerID, stockID,
		}
		if excludeOfferID != 0 {
			args = append(args, excludeOfferID)
		}
		args = append(args,
			model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, *sellerOwnerID, stockID,
		)
		if excludeOfferID != 0 {
			args = append(args, excludeOfferID)
		}
		err = db.Raw(`
			SELECT COALESCE(SUM(q), 0) AS sum FROM (
				SELECT quantity AS q FROM option_contracts
				 WHERE seller_owner_type = ? AND seller_owner_id = ?
				   AND stock_id = ? AND status = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND initiator_owner_type = ? AND initiator_owner_id = ?
				   AND stock_id = ?`+exclude+`
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND counterparty_owner_type = ? AND counterparty_owner_id = ?
				   AND stock_id = ?`+exclude+`
			) AS t`, args...).Scan(&rows).Error
	}
	if err != nil || len(rows) == 0 {
		return decimal.Zero, err
	}
	return rows[0].Sum, nil
}
