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

// GetByID loads a peer option contract by primary key.
func (r *PeerOptionContractRepository) GetByID(id uint64) (*model.PeerOptionContract, error) {
	var pc model.PeerOptionContract
	if err := r.db.Where("id = ?", id).First(&pc).Error; err != nil {
		return nil, err
	}
	return &pc, nil
}

// GetActiveByNegotiationAndDirection locates the active contract row
// for a given negotiation reference and direction. Used by the
// exercise flow where each bank looks up its own row by the embedded
// OptionDescription.negotiationId rather than a directly-passed id.
//
// Returns the row regardless of status; callers check status before
// transitioning. Direction filters DEBIT (seller side) vs CREDIT
// (buyer side) so each bank pulls its own row, not the peer's.
func (r *PeerOptionContractRepository) GetByNegotiationAndDirection(
	negotiationRoutingNumber int64,
	negotiationID string,
	direction string,
) (*model.PeerOptionContract, error) {
	var pc model.PeerOptionContract
	err := r.db.Where(
		"negotiation_routing_number = ? AND negotiation_id = ? AND direction = ?",
		negotiationRoutingNumber, negotiationID, direction,
	).First(&pc).Error
	if err != nil {
		return nil, err
	}
	return &pc, nil
}

// SetStatus transitions the contract to a new status (e.g. "active"
// → "exercised" or "expired"). Optimistic locking via the model's
// BeforeUpdate hook protects against concurrent races.
func (r *PeerOptionContractRepository) SetStatus(id uint64, newStatus string) error {
	return r.db.Model(&model.PeerOptionContract{}).Where("id = ?", id).Update("status", newStatus).Error
}

// ListByLocalParticipant returns rows where the user is a participant
// on this bank's side of the contract: a CREDIT row keyed on the user
// when this bank holds the buyer, or a DEBIT row keyed on the user
// when this bank holds the seller. participantID is the SI-TX
// participant identifier (e.g. "client-1"); ownRouting is the local
// bank's routing number used as the discriminator. role can be
// "buyer", "seller", or anything else (= "either"). Pagination is
// 1-based; pageSize <= 0 disables limit.
func (r *PeerOptionContractRepository) ListByLocalParticipant(
	participantID string,
	ownRouting int64,
	role string,
	page, pageSize int,
) ([]model.PeerOptionContract, int64, error) {
	q := r.db.Model(&model.PeerOptionContract{})
	switch role {
	case "buyer":
		q = q.Where("direction = ? AND buyer_routing_number = ? AND buyer_id = ?", "CREDIT", ownRouting, participantID)
	case "seller":
		q = q.Where("direction = ? AND seller_routing_number = ? AND seller_id = ?", "DEBIT", ownRouting, participantID)
	default:
		q = q.Where(
			"(direction = ? AND buyer_routing_number = ? AND buyer_id = ?) OR (direction = ? AND seller_routing_number = ? AND seller_id = ?)",
			"CREDIT", ownRouting, participantID,
			"DEBIT", ownRouting, participantID,
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
	var rows []model.PeerOptionContract
	if err := q.Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
