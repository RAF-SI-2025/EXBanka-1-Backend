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

// ListBySellerAndParentOffer returns every ongoing negotiation under
// the given seller whose parent_offer matches the supplied (routing,
// id) tuple. Phase 10 cascade-cancel: when one chain is accepted, the
// seller's bank flips every other chain sharing the same precise
// parent_offer_id to cancelled. The match key is atomic per listing
// (sourced from /public-option-offers discovery) so two LEGITIMATELY
// DISTINCT listings on the same ticker + settlement date never
// accidentally cancel each other — they get different parent ids.
//
// Rows initiated free-form (no parent) are excluded by the IS NOT
// NULL guard.
func (r *PeerOtcNegotiationRepository) ListBySellerAndParentOffer(
	sellerRouting int64, sellerID string, parentRouting int64, parentID string,
) ([]model.PeerOtcNegotiation, error) {
	var out []model.PeerOtcNegotiation
	err := r.db.Where(
		"seller_routing_number = ? AND seller_id = ? AND status = ? AND parent_offer_routing = ? AND parent_offer_id = ?",
		sellerRouting, sellerID, "ongoing", parentRouting, parentID).
		Order("created_at ASC").Find(&out).Error
	return out, err
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
