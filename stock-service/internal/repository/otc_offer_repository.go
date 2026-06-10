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

func (r *OTCOfferRepository) Create(o *model.OTCOffer) error {
	return r.db.Create(o).Error
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
			"quantity", "strike_price", "premium", "settlement_date",
			"strike_currency", "premium_currency", "has_preset_terms", "status", "last_seen_at", "updated_at",
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

// UpsertRemoteShell upserts a /public-stock SHELL remote row. Because
// OTCOffer.HasPresetTerms is `default:true`, GORM substitutes `true` for the
// zero-value `false` in the INSERT, and also scans the inserted value back
// into the struct; capturing the desired value BEFORE calling UpsertRemote
// and forcing it via a raw Exec afterward guarantees shells persist as
// has_preset_terms=false.
func (r *OTCOfferRepository) UpsertRemoteShell(o *model.OTCOffer, seenAt time.Time) (uint64, error) {
	// Capture the caller's intent before UpsertRemote may overwrite o.HasPresetTerms
	// with the DB default (true) via the GORM RETURNING scan.
	wantPresetTerms := o.HasPresetTerms
	id, err := r.UpsertRemote(o, seenAt)
	if err != nil {
		return 0, err
	}
	if err := r.db.Exec("UPDATE otc_offers SET has_preset_terms = ? WHERE id = ?",
		wantPresetTerms, id).Error; err != nil {
		return 0, err
	}
	return id, nil
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

// ListExpiringOffers returns up to limit pending/countered offers whose
// settlement_date is in the past. Used by the expiry cron.
//
// Guard: only local rows (local == true) are returned so remote offers folded
// in by Tasks 4-6 never enter the local expiry path.
func (r *OTCOfferRepository) ListExpiringOffers(today string, limit int) ([]model.OTCOffer, error) {
	var out []model.OTCOffer
	err := r.db.Where("status IN ? AND settlement_date < ? AND local = ?",
		[]string{model.OTCOfferStatusPending, model.OTCOfferStatusCountered}, today, true).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// SumActiveQuantityForSeller returns Σ over (a) active option contracts
// where the seller matches, plus (b) PENDING/COUNTERED sell-initiated
// offers where the initiator is the seller, plus (c) PENDING/COUNTERED
// buy-initiated offers where the counterparty is the seller. Used by the
// seller-invariant check (§4.6 of spec). owner_id may be nil for bank
// owners; predicates emit IS NULL in that case.
func (r *OTCOfferRepository) SumActiveQuantityForSeller(sellerOwnerType model.OwnerType, sellerOwnerID *uint64, stockID uint64) (decimal.Decimal, error) {
	var rows []struct{ Sum decimal.Decimal }
	if sellerOwnerID == nil {
		err := r.db.Raw(`
			SELECT COALESCE(SUM(q), 0) AS sum FROM (
				SELECT quantity AS q FROM option_contracts
				 WHERE seller_owner_type = ? AND seller_owner_id IS NULL
				   AND stock_id = ? AND status = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND initiator_owner_type = ? AND initiator_owner_id IS NULL
				   AND stock_id = ?
				UNION ALL
				SELECT quantity AS q FROM otc_offers
				 WHERE direction = ? AND status IN (?, ?)
				   AND counterparty_owner_type = ? AND counterparty_owner_id IS NULL
				   AND stock_id = ?
			) AS t`,
			sellerOwnerType, stockID, model.OptionContractStatusActive,
			model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
			model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
			sellerOwnerType, stockID,
		).Scan(&rows).Error
		if err != nil || len(rows) == 0 {
			return decimal.Zero, err
		}
		return rows[0].Sum, nil
	}
	err := r.db.Raw(`
		SELECT COALESCE(SUM(q), 0) AS sum FROM (
			SELECT quantity AS q FROM option_contracts
			 WHERE seller_owner_type = ? AND seller_owner_id = ?
			   AND stock_id = ? AND status = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND initiator_owner_type = ? AND initiator_owner_id = ?
			   AND stock_id = ?
			UNION ALL
			SELECT quantity AS q FROM otc_offers
			 WHERE direction = ? AND status IN (?, ?)
			   AND counterparty_owner_type = ? AND counterparty_owner_id = ?
			   AND stock_id = ?
		) AS t`,
		sellerOwnerType, *sellerOwnerID, stockID, model.OptionContractStatusActive,
		model.OTCDirectionSellInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerOwnerType, *sellerOwnerID, stockID,
		model.OTCDirectionBuyInitiated, model.OTCOfferStatusPending, model.OTCOfferStatusCountered,
		sellerOwnerType, *sellerOwnerID, stockID,
	).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return decimal.Zero, err
	}
	return rows[0].Sum, nil
}
