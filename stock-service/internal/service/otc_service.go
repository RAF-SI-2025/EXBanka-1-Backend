package service

import (
	"errors"
)

// OTCService handles over-the-counter trading.
// OTC offers are portfolio holdings with public_quantity > 0.
// The actual Holding model is defined in Plan 6 (Portfolio).
// This service provides the business logic shell.
type OTCService struct {
	// holdingRepo will be wired in Plan 6
	orderRepo OrderRepo
	txRepo    OrderTransactionRepo
}

func NewOTCService(orderRepo OrderRepo, txRepo OrderTransactionRepo) *OTCService {
	return &OTCService{
		orderRepo: orderRepo,
		txRepo:    txRepo,
	}
}

// ListOffers returns public holdings available for OTC purchase.
// Stub: will be fully implemented in Plan 6 when Holding model exists.
func (s *OTCService) ListOffers(securityType, ticker string, page, pageSize int) (interface{}, int64, error) {
	return nil, 0, errors.New("OTC offers: implemented in Plan 6 (Portfolio)")
}

// BuyOffer purchases shares from an OTC offer.
// Stub: will be fully implemented in Plan 6 when Holding model exists.
func (s *OTCService) BuyOffer(offerID, buyerID uint64, systemType string, quantity int64, accountID uint64) (interface{}, error) {
	return nil, errors.New("OTC buy: implemented in Plan 6 (Portfolio)")
}
