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

// Upsert creates or updates the offer_json + party metadata for a
// (peer_bank_code, foreign_id) pair. Used by RecordOutboundNegotiation
// so the buyer-side mirror lands idempotently — a retry of the same
// init doesn't double-insert.
func (r *PeerOtcNegotiationRepository) Upsert(neg *model.PeerOtcNegotiation) error {
	existing, err := r.GetByPeerAndID(neg.PeerBankCode, neg.ForeignID)
	if err == nil && existing != nil {
		existing.BuyerRoutingNumber = neg.BuyerRoutingNumber
		existing.BuyerID = neg.BuyerID
		existing.SellerRoutingNumber = neg.SellerRoutingNumber
		existing.SellerID = neg.SellerID
		existing.OfferJSON = neg.OfferJSON
		if neg.Status != "" {
			existing.Status = neg.Status
		}
		return r.db.Save(existing).Error
	}
	return r.db.Create(neg).Error
}

// ListByClient returns rows where the caller's bank hosts a party
// matching (ownRouting, clientPrincipal). The clientPrincipal is the
// wire-form expected on PeerForeignBankId.id — i.e. "client-<N>". role
// narrows to "buyer", "seller" or "both" / "".
func (r *PeerOtcNegotiationRepository) ListByClient(ownRouting int64, clientPrincipal, role string) ([]model.PeerOtcNegotiation, error) {
	q := r.db.Model(&model.PeerOtcNegotiation{})
	switch role {
	case "buyer":
		q = q.Where("buyer_routing_number = ? AND buyer_id = ?", ownRouting, clientPrincipal)
	case "seller":
		q = q.Where("seller_routing_number = ? AND seller_id = ?", ownRouting, clientPrincipal)
	default:
		q = q.Where(
			"(buyer_routing_number = ? AND buyer_id = ?) OR (seller_routing_number = ? AND seller_id = ?)",
			ownRouting, clientPrincipal, ownRouting, clientPrincipal,
		)
	}
	var out []model.PeerOtcNegotiation
	err := q.Order("updated_at DESC").Find(&out).Error
	return out, err
}
