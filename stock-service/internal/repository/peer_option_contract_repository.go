package repository

import (
	"errors"

	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PeerOptionContractRepository persists cross-bank option contracts
// formed by SI-TX OTC accept flows.
type PeerOptionContractRepository struct {
	db *gorm.DB
}

func NewPeerOptionContractRepository(db *gorm.DB) *PeerOptionContractRepository {
	return &PeerOptionContractRepository{db: db}
}

// UpsertIdempotent inserts the row if (crossbank_tx_id, posting_index)
// is new, or returns the existing row unchanged. Idempotent by design
// so transaction-service can safely retry COMMIT_TX without producing
// duplicate option contracts.
func (r *PeerOptionContractRepository) UpsertIdempotent(c *model.PeerOptionContract) error {
	res := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "crossbank_tx_id"}, {Name: "posting_index"}},
		DoNothing: true,
	}).Create(c)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		// Row already exists — load it so caller has the persisted ID.
		var existing model.PeerOptionContract
		if err := r.db.Where("crossbank_tx_id = ? AND posting_index = ?", c.CrossbankTxID, c.PostingIndex).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		*c = existing
	}
	return nil
}

func (r *PeerOptionContractRepository) GetByCrossbankTxAndPosting(crossbankTxID string, postingIndex int32) (*model.PeerOptionContract, error) {
	var pc model.PeerOptionContract
	if err := r.db.Where("crossbank_tx_id = ? AND posting_index = ?", crossbankTxID, postingIndex).First(&pc).Error; err != nil {
		return nil, err
	}
	return &pc, nil
}
