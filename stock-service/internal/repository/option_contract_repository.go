package repository

import (
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type OptionContractRepository struct{ db *gorm.DB }

func NewOptionContractRepository(db *gorm.DB) *OptionContractRepository {
	return &OptionContractRepository{db: db}
}

func (r *OptionContractRepository) DB() *gorm.DB { return r.db }

func (r *OptionContractRepository) Create(c *model.OptionContract) error {
	return r.db.Create(c).Error
}

// GetByID fetches a local option contract by primary key.
//
// Guard: remote rows (local == false) are treated as not-found so they can
// never enter the local exercise/expiry paths.
func (r *OptionContractRepository) GetByID(id uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if !c.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &c, nil
}

// GetBySagaID returns the contract minted by a given accept saga, or
// gorm.ErrRecordNotFound if none exists yet. Used by accept-saga crash recovery
// to rebuild the saga against the contract its (possibly partial) original run
// already created, so a forward-resume reuses that contract instead of minting
// a duplicate.
func (r *OptionContractRepository) GetBySagaID(sagaID string) (*model.OptionContract, error) {
	if sagaID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var c model.OptionContract
	err := r.db.Where("saga_id = ?", sagaID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &c, err
}

// GetByOfferID fetches the local contract minted from a given OTCOffer.
//
// Guard: remote rows (local == false) are treated as not-found so they never
// enter the local accept/exercise/saga paths.
func (r *OptionContractRepository) GetByOfferID(offerID uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.Where("offer_id = ?", offerID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if !c.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &c, nil
}

func (r *OptionContractRepository) Delete(id uint64) error {
	return r.db.Delete(&model.OptionContract{}, id).Error
}

// Save persists a loaded-then-mutated option contract through GORM's Save
// (UPDATE by primary key). The OptionContract.BeforeUpdate hook attaches the
// optimistic-lock WHERE version=? clause and increments Version on the
// caller's struct.
//
// We use Select("*").Save(...) intentionally: bare db.Save in GORM v1.31.1
// falls back to INSERT...ON CONFLICT(id) DO UPDATE when the initial UPDATE
// matches zero rows (finisher_api.go:109-110), which would silently overwrite
// the winner of an optimistic-lock race and hide the conflict. Selecting "*"
// sets the `selectedUpdate` flag in GORM's Save and disables that fallback
// path, so RowsAffected==0 correctly indicates an optimistic-lock conflict.
func (r *OptionContractRepository) Save(c *model.OptionContract) error {
	res := r.db.Select("*").Save(c)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrOptimisticLock
	}
	return nil
}

// ListByOwner returns option contracts where the owner appears as buyer,
// seller, or either. owner_id may be nil for OwnerType=bank.
//
// Guard: remote (cross-bank) contracts now live in this same table as rows
// with local == false. The authoritative local-only scope is the local == true
// predicate, consistent with GetByID, GetByOfferID, and ListExpiring. This
// query is scoped to local rows so it returns only local contracts and remote
// rows can never appear in /me views even when their buyer_owner_id/
// seller_owner_id collide with a local user's ID.
func (r *OptionContractRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64, role string, statuses []string, page, pageSize int) ([]model.OptionContract, int64, error) {
	q := r.db.Model(&model.OptionContract{}).Where("local = ?", true)
	switch role {
	case "buyer":
		q = scopeOwner(q, "buyer_owner_type", "buyer_owner_id", ownerType, ownerID)
	case "seller":
		q = scopeOwner(q, "seller_owner_type", "seller_owner_id", ownerType, ownerID)
	default:
		// OR over the buyer and seller owner-pair predicates. Inline since
		// scopeOwner is single-pair.
		if ownerID == nil {
			q = q.Where("(buyer_owner_type = ? AND buyer_owner_id IS NULL) OR (seller_owner_type = ? AND seller_owner_id IS NULL)",
				ownerType, ownerType)
		} else {
			q = q.Where("(buyer_owner_type = ? AND buyer_owner_id = ?) OR (seller_owner_type = ? AND seller_owner_id = ?)",
				ownerType, *ownerID, ownerType, *ownerID)
		}
	}
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
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
	var out []model.OptionContract
	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&out).Error
	return out, total, err
}

// ListExpiring returns up to limit ACTIVE contracts past settlement_date.
//
// Guard: only local contracts (local == true) are returned. Remote contracts
// (folded into this table by SP-2a) have their own expiry path via
// ListRemoteContractsExpiring and must not enter the local expiry saga.
func (r *OptionContractRepository) ListExpiring(today string, limit int) ([]model.OptionContract, error) {
	var out []model.OptionContract
	err := r.db.Where("status = ? AND settlement_date < ? AND local = ?",
		model.OptionContractStatusActive, today, true).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListExpiringOn returns ACTIVE option contracts whose settlement_date falls on
// exactly the given calendar day [day, day+1). Used by the expiring-soon
// warning pass (SP5 E) — matching on a single day means each contract is warned
// once as it crosses the N-days-out mark. `day` is a date-truncated time.
func (r *OptionContractRepository) ListExpiringOn(day time.Time, limit int) ([]model.OptionContract, error) {
	start := day.UTC().Truncate(24 * time.Hour)
	end := start.Add(24 * time.Hour)
	var out []model.OptionContract
	err := r.db.Where("status = ? AND settlement_date >= ? AND settlement_date < ? AND local = ?",
		model.OptionContractStatusActive, start, end, true).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ---------------------------------------------------------------------------
// REMOTE (cross-bank) contract rows (SP-2a).
//
// These methods are the unified-table replacement for the retired
// peer-option-contract mirror repository. A remote contract lives in
// option_contracts with routing_number = <peer routing> (the COUNTERPARTY — the
// side this bank does NOT host) and native_id = "<crossbank_tx_id>:<posting_index>"
// (preserving the retired mirror's natural key so UpsertRemoteContract stays
// idempotent on the (routing_number, native_id) unique index). The cross-bank
// party/negotiation/direction data lives in the Remote* columns + the shared
// columns (Quantity/StrikePrice/Ticker/StrikeCurrency/SettlementDate/Status/
// CrossbankTxID/BuyerBankCode/SellerBankCode).
//
// EVERY method below scopes to local == false. This is the SAME guarantee the
// local-path queries get from their local == true guards: a local row can NEVER
// satisfy a remote query, and a remote row can NEVER satisfy a local one — so
// folding the two tables together does not leak remote contracts into the local
// exercise/expiry sagas.
// ---------------------------------------------------------------------------

// UpsertRemoteContract inserts the remote contract if its natural key
// (routing_number, native_id) is new, or returns the existing row unchanged.
// Idempotent by design (ON CONFLICT DO NOTHING, never SELECT-then-INSERT per the
// Concurrency requirement) so transaction-service can safely retry COMMIT_TX
// without producing duplicate option contracts. The caller MUST have set
// RoutingNumber to the COUNTERPARTY routing and NativeID to
// "<crossbank_tx_id>:<posting_index>". On a conflict the persisted row (with its
// surrogate ID) is loaded back into c so the caller has the contract id.
func (r *OptionContractRepository) UpsertRemoteContract(c *model.OptionContract) error {
	res := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "routing_number"}, {Name: "native_id"}},
		DoNothing: true,
	}).Create(c)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Row already exists — load it so the caller has the persisted ID.
		// (routing_number, native_id) is the natural key (DATA); local is the
		// remote discriminator.
		var existing model.OptionContract
		err := r.db.Where("routing_number = ? AND native_id = ? AND local = ?",
			c.RoutingNumber, c.NativeID, false).First(&existing).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		*c = existing
	}
	return nil
}

// GetRemoteContractByNegotiationAndDirection locates the remote contract row for
// a given negotiation reference and direction. Used by the exercise / money-leg
// validation flows where each bank looks up its own row by the embedded
// OptionDescription.negotiationId + this side's direction. Returns the row
// regardless of status; callers check status before transitioning. Scoped to
// local == false so a local row can never be returned.
func (r *OptionContractRepository) GetRemoteContractByNegotiationAndDirection(
	negRouting int64, negNative, direction string,
) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.Where(
		"remote_negotiation_routing = ? AND remote_negotiation_native_id = ? AND remote_direction = ? AND local = ?",
		negRouting, negNative, direction, false,
	).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetRemoteContractByID loads a remote contract by primary key. Returns
// gorm.ErrRecordNotFound when the matched row is LOCAL (local == true), so the
// remote read paths can never surface a local contract.
func (r *OptionContractRepository) GetRemoteContractByID(id uint64) (*model.OptionContract, error) {
	var c model.OptionContract
	err := r.db.First(&c, id).Error
	if err != nil {
		return nil, err
	}
	if c.Local {
		return nil, gorm.ErrRecordNotFound
	}
	return &c, nil
}

// SetRemoteContractStatus transitions a remote contract to a new status. Scoped
// to local == false. SkipHooks: this is a targeted column UPDATE on a REMOTE
// row — the model's BeforeSave runs ValidateOwner against the (zero-value)
// struct's owner columns and BeforeUpdate adds a version guard, neither
// meaningful for a column-scoped remote-row flip; BeforeSave would spuriously
// reject with "invalid owner_type" (the Task-5 hazard).
func (r *OptionContractRepository) SetRemoteContractStatus(id uint64, newStatus string) error {
	return r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OptionContract{}).
		Where("id = ? AND local = ?", id, false).
		Updates(map[string]any{"status": newStatus, "updated_at": time.Now().UTC()}).Error
}

// CompareAndSetRemoteContractStatus atomically transitions a remote contract
// from `from` to `to` in one guarded UPDATE, returning true iff exactly one row
// matched. Same semantics as the retired CompareAndSetStatus: it claims a
// contract for exercise (active → exercising) so of two concurrent exercise
// attempts only one observes a match and dispatches the strike payment — the
// loser is rejected, preventing a double charge. The WHERE status guard IS the
// concurrency control. SkipHooks for the same reason as SetRemoteContractStatus.
func (r *OptionContractRepository) CompareAndSetRemoteContractStatus(id uint64, from, to string) (bool, error) {
	res := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.OptionContract{}).
		Where("id = ? AND status = ? AND local = ?", id, from, false).
		Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// HasRemoteContractForNegotiation returns true if at least one remote contract
// row exists for the given negotiation reference. Used by the reconciler to
// distinguish an ACCEPTED negotiation (contract formed) from a CANCELLED one
// (no contract). Scoped to local == false.
func (r *OptionContractRepository) HasRemoteContractForNegotiation(negRouting int64, negNative string) (bool, error) {
	var count int64
	err := r.db.Model(&model.OptionContract{}).
		Where("remote_negotiation_routing = ? AND remote_negotiation_native_id = ? AND local = ?",
			negRouting, negNative, false).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListRemoteContractsExpiring returns up to limit ACTIVE remote contracts whose
// settlement_date is strictly before `today` (a "YYYY-MM-DD" date string; the
// SettlementDate date column compares correctly against it in Postgres). Used by
// the daily expiry cron's cross-bank pass. Scoped to local == false so remote
// rows are processed ONLY here and never enter the local expiry saga (and
// vice-versa). Status filtered on the PEER vocabulary "active".
func (r *OptionContractRepository) ListRemoteContractsExpiring(today string, limit int) ([]model.OptionContract, error) {
	var out []model.OptionContract
	err := r.db.Where("status = ? AND settlement_date < ? AND local = ?",
		"active", today, false).
		Order("id ASC").Limit(limit).Find(&out).Error
	return out, err
}

// ListRemoteContractsByLocalParticipant returns remote rows where the user is a
// participant on THIS bank's side of the contract: a CREDIT row keyed on the
// user when this bank holds the buyer, or a DEBIT row keyed on the user when
// this bank holds the seller. participantID is the SI-TX participant identifier
// (e.g. "client-1"); ownRouting is the local bank's routing used as the side
// discriminator (matched against the COUNTERPARTY routing stored in
// buyer_bank_code/seller_bank_code). role can be "buyer", "seller", or anything
// else (= "either"). Pagination is 1-based; pageSize <= 0 disables the limit.
// Scoped to local == false.
func (r *OptionContractRepository) ListRemoteContractsByLocalParticipant(
	participantID string, ownRouting int64, role string, page, pageSize int,
) ([]model.OptionContract, int64, error) {
	own := strconv.FormatInt(ownRouting, 10)
	q := r.db.Model(&model.OptionContract{}).Where("local = ?", false)
	switch role {
	case "buyer":
		q = q.Where("remote_direction = ? AND buyer_bank_code = ? AND remote_buyer_id = ?", "CREDIT", own, participantID)
	case "seller":
		q = q.Where("remote_direction = ? AND seller_bank_code = ? AND remote_seller_id = ?", "DEBIT", own, participantID)
	default:
		q = q.Where(
			"(remote_direction = ? AND buyer_bank_code = ? AND remote_buyer_id = ?) OR (remote_direction = ? AND seller_bank_code = ? AND remote_seller_id = ?)",
			"CREDIT", own, participantID,
			"DEBIT", own, participantID,
		)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize > 0 {
		offset := (page - 1) * pageSize
		if offset < 0 {
			offset = 0
		}
		q = q.Order("id DESC").Offset(offset).Limit(pageSize)
	} else {
		q = q.Order("id DESC")
	}
	var rows []model.OptionContract
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListRemoteContractsByBankParty returns remote rows where the side WE host
// (the local routing == ownRouting, stored in buyer_bank_code/seller_bank_code)
// is the BANK — the party id carries the "employee-" prefix. This is the
// contracts analog of ListRemoteNegByBankParty (SP-3 Task 5b): a CREDIT row
// where this bank holds the BUYER (remote_buyer_id LIKE 'employee-%') or a DEBIT
// row where this bank holds the SELLER (remote_seller_id LIKE 'employee-%').
// ownRouting is the local bank's routing used as the side discriminator (matched
// against the COUNTERPARTY routing stored in buyer_bank_code/seller_bank_code).
// role can be "buyer", "seller", or anything else (= "either"). The bank has no
// single wire principal across contracts, so it is matched by PREFIX, not an
// exact id; the "employee-%" LIKE pattern is a constant prefix (no user input),
// so there is no injection risk. Pagination is 1-based; pageSize <= 0 disables
// the limit. Scoped to local == false (same remote scope as
// ListRemoteContractsByLocalParticipant) so a LOCAL row can never satisfy it.
func (r *OptionContractRepository) ListRemoteContractsByBankParty(
	ownRouting int64, role string, page, pageSize int,
) ([]model.OptionContract, int64, error) {
	own := strconv.FormatInt(ownRouting, 10)
	const employeePrefix = "employee-%"
	q := r.db.Model(&model.OptionContract{}).Where("local = ?", false)
	switch role {
	case "buyer":
		q = q.Where("remote_direction = ? AND buyer_bank_code = ? AND remote_buyer_id LIKE ?", "CREDIT", own, employeePrefix)
	case "seller":
		q = q.Where("remote_direction = ? AND seller_bank_code = ? AND remote_seller_id LIKE ?", "DEBIT", own, employeePrefix)
	default:
		q = q.Where(
			"(remote_direction = ? AND buyer_bank_code = ? AND remote_buyer_id LIKE ?) OR (remote_direction = ? AND seller_bank_code = ? AND remote_seller_id LIKE ?)",
			"CREDIT", own, employeePrefix,
			"DEBIT", own, employeePrefix,
		)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if pageSize > 0 {
		offset := (page - 1) * pageSize
		if offset < 0 {
			offset = 0
		}
		q = q.Order("id DESC").Offset(offset).Limit(pageSize)
	} else {
		q = q.Order("id DESC")
	}
	var rows []model.OptionContract
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
