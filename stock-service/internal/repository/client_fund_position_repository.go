package repository

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type ClientFundPositionRepository struct {
	db *gorm.DB
}

func NewClientFundPositionRepository(db *gorm.DB) *ClientFundPositionRepository {
	return &ClientFundPositionRepository{db: db}
}

// IncrementContribution adds delta to TotalContributedRSD, creating the
// row if missing. **Idempotent on contributionID** (Fix R2,
// 2026-05-16): a fund_position_settlements row is inserted first with
// UNIQUE (fund_contribution_id); if the insert returns RowsAffected=0
// the increment has already been applied for this contribution and the
// position is left untouched. This makes the fund_invest saga's last
// step safe to retry — without it, a recovery loop replay would
// double-count the user's stake.
func (r *ClientFundPositionRepository) IncrementContribution(fundID uint64, ownerType model.OwnerType, ownerID *uint64, delta decimal.Decimal, contributionID uint64) error {
	if contributionID == 0 {
		return errors.New("contributionID is required for idempotent increment")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Step 1: upsert the position row (creates with delta if new;
		// no balance change if existing — that's done in step 3 only
		// when the settlement insert actually adds a new row).
		row := &model.ClientFundPosition{
			FundID:              fundID,
			OwnerType:           ownerType,
			OwnerID:             ownerID,
			TotalContributedRSD: decimal.Zero,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "fund_id"}, {Name: "owner_type"}, {Name: "owner_id"}},
			DoNothing: true,
		}).Create(row).Error; err != nil {
			return err
		}
		// Re-load (the OnConflict path leaves row.ID at 0 on conflict).
		var pos model.ClientFundPosition
		q := tx.Where("fund_id = ?", fundID)
		q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
		if err := q.First(&pos).Error; err != nil {
			return err
		}

		// Step 2: try to record the settlement. Unique index on
		// fund_contribution_id makes the second arrival a no-op.
		settlement := &model.FundPositionSettlement{
			ClientFundPositionID: pos.ID,
			FundContributionID:   contributionID,
			Delta:                delta,
			Direction:            model.FundSettlementDirectionInvest,
		}
		ins := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(settlement)
		if ins.Error != nil {
			return ins.Error
		}
		if ins.RowsAffected == 0 {
			// Replay: this contribution has already been applied. Leave
			// the position untouched.
			return nil
		}

		// Step 3: first arrival → actually increment the running total.
		// Load-modify-save (not map-based Updates) so the model's
		// BeforeSave ValidateOwner hook sees the real OwnerType — a
		// map-based update passes a zero-value struct through hooks and
		// fails with "invalid owner_type".
		pos.TotalContributedRSD = pos.TotalContributedRSD.Add(delta)
		return tx.Save(&pos).Error
	})
}

// DecrementContribution is the redeem mirror of IncrementContribution.
// Idempotent on contributionID via the same fund_position_settlements
// ledger; the second arrival is a no-op so a redeem-saga replay can't
// double-subtract.
func (r *ClientFundPositionRepository) DecrementContribution(fundID uint64, ownerType model.OwnerType, ownerID *uint64, delta decimal.Decimal, contributionID uint64) error {
	if contributionID == 0 {
		return errors.New("contributionID is required for idempotent decrement")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var pos model.ClientFundPosition
		q := tx.Where("fund_id = ?", fundID)
		q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
		if err := q.First(&pos).Error; err != nil {
			return err
		}
		settlement := &model.FundPositionSettlement{
			ClientFundPositionID: pos.ID,
			FundContributionID:   contributionID,
			Delta:                delta,
			Direction:            model.FundSettlementDirectionRedeem,
		}
		ins := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(settlement)
		if ins.Error != nil {
			return ins.Error
		}
		if ins.RowsAffected == 0 {
			return nil // replay
		}
		pos.TotalContributedRSD = pos.TotalContributedRSD.Sub(delta)
		return tx.Save(&pos).Error
	})
}

func (r *ClientFundPositionRepository) GetByFundAndOwner(fundID uint64, ownerType model.OwnerType, ownerID *uint64) (*model.ClientFundPosition, error) {
	var p model.ClientFundPosition
	q := r.db.Where("fund_id = ?", fundID)
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &p, err
}

func (r *ClientFundPositionRepository) ListByOwner(ownerType model.OwnerType, ownerID *uint64) ([]model.ClientFundPosition, error) {
	var out []model.ClientFundPosition
	q := r.db.Model(&model.ClientFundPosition{})
	q = scopeOwner(q, "owner_type", "owner_id", ownerType, ownerID)
	err := q.Order("created_at ASC").Find(&out).Error
	return out, err
}

func (r *ClientFundPositionRepository) SumTotalContributed(fundID uint64) (decimal.Decimal, error) {
	var result struct{ Total decimal.Decimal }
	err := r.db.Model(&model.ClientFundPosition{}).
		Select("COALESCE(SUM(total_contributed_rsd), 0) AS total").
		Where("fund_id = ?", fundID).
		Scan(&result).Error
	return result.Total, err
}
