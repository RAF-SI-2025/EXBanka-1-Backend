package repository

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

// HoldingReservationRepository persists sell-side reservation state and
// settlement history for the bank-safe settlement flow. Reservations are the
// idempotency + recovery ledger for a client's hold on shares of a Holding
// (tied to an OrderID), and settlements record each partial-settle quantity
// debit against them. Quantity-based mirror of AccountReservationRepository.
type HoldingReservationRepository struct {
	db *gorm.DB
}

func NewHoldingReservationRepository(db *gorm.DB) *HoldingReservationRepository {
	return &HoldingReservationRepository{db: db}
}

// WithTx returns a repository bound to the given transaction handle. Lets the
// reservation service run the reservation flow inside an outer transaction
// that also locks+updates the holding row.
func (r *HoldingReservationRepository) WithTx(tx *gorm.DB) *HoldingReservationRepository {
	return &HoldingReservationRepository{db: tx}
}

func (r *HoldingReservationRepository) Create(res *model.HoldingReservation) error {
	return r.db.Create(res).Error
}

// InsertIfAbsent inserts the reservation unless a row with the same OrderID
// (legacy sell-order path) or OTCContractID (OTC option contract path) already
// exists. Returns (inserted, row, error) where row is either the new row
// (inserted=true) or the pre-existing row (inserted=false). Callers use
// this for idempotent ReserveShares / ReserveForOTCContract retries.
func (r *HoldingReservationRepository) InsertIfAbsent(res *model.HoldingReservation) (bool, *model.HoldingReservation, error) {
	conflictCol := "order_id"
	if res.OTCContractID != nil {
		conflictCol = "otc_contract_id"
	}
	result := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: conflictCol}},
		DoNothing: true,
	}).Create(res)
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected == 1 {
		return true, res, nil
	}
	if res.OrderID != nil {
		existing, err := r.GetByOrderID(*res.OrderID)
		if err != nil {
			return false, nil, err
		}
		return false, existing, nil
	}
	existing, err := r.GetByOTCContractID(*res.OTCContractID)
	if err != nil {
		return false, nil, err
	}
	return false, existing, nil
}

// GetByOTCContractID returns the reservation row keyed on OTCContractID
// (mirror of GetByOrderID for the OTC option-contract path).
func (r *HoldingReservationRepository) GetByOTCContractID(otcContractID uint64) (*model.HoldingReservation, error) {
	var res model.HoldingReservation
	if err := r.db.Where("otc_contract_id = ?", otcContractID).First(&res).Error; err != nil {
		return nil, err
	}
	return &res, nil
}

// GetByOTCContractIDForUpdate is the SELECT FOR UPDATE variant.
func (r *HoldingReservationRepository) GetByOTCContractIDForUpdate(otcContractID uint64) (*model.HoldingReservation, error) {
	var res model.HoldingReservation
	err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("otc_contract_id = ?", otcContractID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *HoldingReservationRepository) GetByOrderID(orderID uint64) (*model.HoldingReservation, error) {
	var res model.HoldingReservation
	if err := r.db.Where("order_id = ?", orderID).First(&res).Error; err != nil {
		return nil, err
	}
	return &res, nil
}

// GetByOrderIDForUpdate loads the reservation with SELECT FOR UPDATE; caller
// must already be inside a DB transaction.
func (r *HoldingReservationRepository) GetByOrderIDForUpdate(orderID uint64) (*model.HoldingReservation, error) {
	var res model.HoldingReservation
	err := r.db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("order_id = ?", orderID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// UpdateStatus persists the row via db.Save, relying on the BeforeUpdate hook
// to enforce optimistic-lock version matching. Returns ErrOptimisticLock
// (wrapped) if another transaction modified the row first.
func (r *HoldingReservationRepository) UpdateStatus(res *model.HoldingReservation) error {
	result := r.db.Save(res)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: holding reservation %d was modified concurrently", ErrOptimisticLock, res.ID)
	}
	return nil
}

func (r *HoldingReservationRepository) CreateSettlement(s *model.HoldingReservationSettlement) error {
	return r.db.Create(s).Error
}

func (r *HoldingReservationRepository) ListSettlements(holdingReservationID uint64) ([]model.HoldingReservationSettlement, error) {
	var out []model.HoldingReservationSettlement
	if err := r.db.Where("holding_reservation_id = ?", holdingReservationID).Order("id ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// SumSettlements returns the total quantity already settled against a
// reservation. Used to compute remaining reserved quantity before
// partial-settle.
func (r *HoldingReservationRepository) SumSettlements(holdingReservationID uint64) (int64, error) {
	var total int64
	err := r.db.Model(&model.HoldingReservationSettlement{}).
		Where("holding_reservation_id = ?", holdingReservationID).
		Select("COALESCE(SUM(quantity), 0)").Scan(&total).Error
	if err != nil {
		return 0, err
	}
	return total, nil
}
