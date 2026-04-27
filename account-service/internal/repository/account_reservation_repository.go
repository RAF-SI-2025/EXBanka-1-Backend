package repository

import (
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/contract/shared"
)

// AccountReservationRepository persists reservation state and settlement
// history for the bank-safe settlement flow. Reservations are the idempotency
// + recovery ledger for a client's hold on an account (tied to an OrderID),
// and settlements record each partial-settle debit against them.
type AccountReservationRepository struct {
	db *gorm.DB
}

func NewAccountReservationRepository(db *gorm.DB) *AccountReservationRepository {
	return &AccountReservationRepository{db: db}
}

// WithTx returns a repository bound to the given transaction handle. Lets
// the reservation service run the reservation flow inside an outer
// transaction that also locks+updates the account row.
func (r *AccountReservationRepository) WithTx(tx *gorm.DB) *AccountReservationRepository {
	return &AccountReservationRepository{db: tx}
}

func (r *AccountReservationRepository) Create(res *model.AccountReservation) error {
	return r.db.Create(res).Error
}

// InsertIfAbsent inserts the reservation unless a row with the same OrderID
// already exists. Returns (inserted, row, error) where row is either the new
// row (inserted=true) or the pre-existing row (inserted=false). Callers use
// this for idempotent ReserveFunds retries.
func (r *AccountReservationRepository) InsertIfAbsent(res *model.AccountReservation) (bool, *model.AccountReservation, error) {
	result := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "order_id"}},
		DoNothing: true,
	}).Create(res)
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected == 1 {
		return true, res, nil
	}
	existing, err := r.GetByOrderID(res.OrderID)
	if err != nil {
		return false, nil, err
	}
	return false, existing, nil
}

func (r *AccountReservationRepository) GetByOrderID(orderID uint64) (*model.AccountReservation, error) {
	var res model.AccountReservation
	if err := r.db.Where("order_id = ?", orderID).First(&res).Error; err != nil {
		return nil, err
	}
	return &res, nil
}

// GetByOrderIDForUpdate loads the reservation with SELECT FOR UPDATE; caller
// must already be inside a DB transaction.
func (r *AccountReservationRepository) GetByOrderIDForUpdate(orderID uint64) (*model.AccountReservation, error) {
	var res model.AccountReservation
	err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_id = ?", orderID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateStatus persists the row via db.Save, relying on the BeforeUpdate hook
// to enforce optimistic-lock version matching. Returns shared.ErrOptimisticLock
// (wrapped) if another transaction modified the row first.
//
// db.Select("*").Save(res) instead of bare db.Save: the bare form silently
// upserts on UPDATE-mismatch (RowsAffected==1, lock conflict masked).
// Select("*") sets GORM's selectedUpdate flag which disables the upsert
// fallback so RowsAffected==0 correctly surfaces. See F15 in
// docs/superpowers/specs/2026-04-27-future-ideas-backlog.md.
func (r *AccountReservationRepository) UpdateStatus(res *model.AccountReservation) error {
	result := r.db.Select("*").Save(res)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: reservation %d was modified concurrently", shared.ErrOptimisticLock, res.ID)
	}
	return nil
}

func (r *AccountReservationRepository) CreateSettlement(s *model.AccountReservationSettlement) error {
	return r.db.Create(s).Error
}

func (r *AccountReservationRepository) ListSettlements(reservationID uint64) ([]model.AccountReservationSettlement, error) {
	var out []model.AccountReservationSettlement
	if err := r.db.Where("reservation_id = ?", reservationID).Order("id ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// SumSettlements returns the total amount already settled against a
// reservation. Used to compute remaining balance before partial-settle.
func (r *AccountReservationRepository) SumSettlements(reservationID uint64) (decimal.Decimal, error) {
	var total decimal.Decimal
	err := r.db.Model(&model.AccountReservationSettlement{}).
		Where("reservation_id = ?", reservationID).
		Select("COALESCE(SUM(amount), 0)").Scan(&total).Error
	if err != nil {
		return decimal.Zero, err
	}
	return total, nil
}
