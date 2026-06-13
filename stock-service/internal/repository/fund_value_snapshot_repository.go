package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/stock-service/internal/model"
)

type FundValueSnapshotRepository struct {
	db *gorm.DB
}

func NewFundValueSnapshotRepository(db *gorm.DB) *FundValueSnapshotRepository {
	return &FundValueSnapshotRepository{db: db}
}

// UpsertByFundAndDate inserts or updates the snapshot for (fund_id, date),
// so the daily cron can re-run within the same day without creating a
// duplicate row.
func (r *FundValueSnapshotRepository) UpsertByFundAndDate(snap *model.FundValueSnapshot) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.FundValueSnapshot
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("fund_id = ? AND date = ?", snap.FundID, snap.Date).
			First(&existing).Error
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Create(snap).Error
			}
			return err
		}
		existing.TotalValueRSD = snap.TotalValueRSD
		existing.LiquidRSDBal = snap.LiquidRSDBal
		existing.HoldingsValueRSD = snap.HoldingsValueRSD
		existing.InvestorCount = snap.InvestorCount
		return tx.Save(&existing).Error
	})
}

// ListByFundSince returns a fund's snapshots dated >= since, ascending by date.
// A zero `since` returns the full history.
func (r *FundValueSnapshotRepository) ListByFundSince(fundID uint64, since time.Time) ([]model.FundValueSnapshot, error) {
	var rows []model.FundValueSnapshot
	q := r.db.Where("fund_id = ?", fundID)
	if !since.IsZero() {
		q = q.Where("date >= ?", since)
	}
	if err := q.Order("date ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListAllSince returns every fund's snapshots dated >= since, ascending by
// (fund_id, date). Used to build the system-average comparison series.
func (r *FundValueSnapshotRepository) ListAllSince(since time.Time) ([]model.FundValueSnapshot, error) {
	var rows []model.FundValueSnapshot
	q := r.db.Model(&model.FundValueSnapshot{})
	if !since.IsZero() {
		q = q.Where("date >= ?", since)
	}
	if err := q.Order("fund_id ASC, date ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
