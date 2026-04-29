package repository

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

// PeerOtcNegotiationRepository persists inbound peer-bank OTC
// negotiations (Phase 4 SI-TX). Receiver-side only — outbound peers
// initiate via api-gateway /api/v3/negotiations and the records here
// reflect the local mirror updated on POST/PUT/DELETE/Accept.
type PeerOtcNegotiationRepository struct {
	db *gorm.DB
}

func NewPeerOtcNegotiationRepository(db *gorm.DB) *PeerOtcNegotiationRepository {
	return &PeerOtcNegotiationRepository{db: db}
}

func (r *PeerOtcNegotiationRepository) Create(neg *model.PeerOtcNegotiation) error {
	return r.db.Create(neg).Error
}

func (r *PeerOtcNegotiationRepository) GetByPeerAndID(peerCode, foreignID string) (*model.PeerOtcNegotiation, error) {
	var neg model.PeerOtcNegotiation
	if err := r.db.Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).First(&neg).Error; err != nil {
		return nil, err
	}
	return &neg, nil
}

func (r *PeerOtcNegotiationRepository) UpdateOffer(peerCode, foreignID, offerJSON string) error {
	return r.db.Model(&model.PeerOtcNegotiation{}).
		Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Updates(map[string]interface{}{"offer_json": offerJSON}).Error
}

func (r *PeerOtcNegotiationRepository) UpdateStatus(peerCode, foreignID, status string) error {
	return r.db.Model(&model.PeerOtcNegotiation{}).
		Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Updates(map[string]interface{}{"status": status}).Error
}

func (r *PeerOtcNegotiationRepository) Delete(peerCode, foreignID string) error {
	return r.db.Where("peer_bank_code = ? AND foreign_id = ?", peerCode, foreignID).
		Delete(&model.PeerOtcNegotiation{}).Error
}
