package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// OTCService handles over-the-counter trading.
// OTC offers are Holding records with public_quantity > 0.
type OTCService struct {
	holdingRepo     HoldingRepo
	capitalGainRepo CapitalGainRepo
	listingRepo     ListingRepo
	accountClient   accountpb.AccountServiceClient
	nameResolver    UserNameResolver
}

func NewOTCService(
	holdingRepo HoldingRepo,
	capitalGainRepo CapitalGainRepo,
	listingRepo ListingRepo,
	accountClient accountpb.AccountServiceClient,
	nameResolver UserNameResolver,
) *OTCService {
	return &OTCService{
		holdingRepo:     holdingRepo,
		capitalGainRepo: capitalGainRepo,
		listingRepo:     listingRepo,
		accountClient:   accountClient,
		nameResolver:    nameResolver,
	}
}

// ListOffers returns public stock holdings available for OTC purchase.
func (s *OTCService) ListOffers(filter OTCFilter) ([]model.Holding, int64, error) {
	return s.holdingRepo.ListPublicOffers(filter)
}

// BuyOffer purchases shares from an OTC offer (a public holding).
//
// **Race-safety (Phase 3B fix).** The seller's holding read is now
// wrapped in a db.Transaction with SELECT FOR UPDATE so two concurrent
// buyers can't both pass the PublicQuantity check on the same N shares
// and double-spend them. The transaction also enforces the canonical
// "cannot sell what they don't have" check (PublicQuantity >= quantity)
// before any account-service call fires. Money compensation paths below
// still run if the saga aborts after holding decrement.
func (s *OTCService) BuyOffer(
	offerID uint64, // holding ID of the seller
	buyerID uint64,
	buyerSystemType string,
	quantity int64,
	buyerAccountID uint64,
) (*OTCBuyResult, error) {
	// Lock the seller's holding for the duration of the read-check-
	// decrement sequence. The actual share decrement happens further
	// down (line ~190), but the lock here serialises concurrent
	// PublicQuantity reads so we never let a stale-read past the
	// availability check. Holding the row through the money saga IS
	// safe — the saga's account-service calls don't touch this row.
	var sellerHolding *model.Holding
	err := s.holdingRepo.DB().Transaction(func(tx *gorm.DB) error {
		h, lerr := s.holdingRepo.LockByIDTx(tx, offerID)
		if lerr != nil {
			if errors.Is(lerr, gorm.ErrRecordNotFound) {
				return fmt.Errorf("OTC offer not found: %w", ErrOTCOfferNotFound)
			}
			return lerr
		}
		if h.PublicQuantity < quantity {
			return fmt.Errorf("insufficient public quantity for OTC purchase: %w", ErrOTCInsufficientPublicQuantity)
		}
		sellerHolding = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Self-buy guard: compare on the buyer's owner pair vs the seller's
	// holding owner. Both sides may legitimately be bank-owned in pathological
	// configurations; the OwnerID nil/non-nil semantics in OwnerFromLegacy
	// handle that without leaking sentinel-value comparisons.
	buyerOwnerType, buyerOwnerID := model.OwnerFromLegacy(buyerID, buyerSystemType)
	if sellerHolding.OwnerType == buyerOwnerType && ownerIDEqual(sellerHolding.OwnerID, buyerOwnerID) {
		return nil, fmt.Errorf("cannot buy your own OTC offer: %w", ErrOTCBuyOwnOffer)
	}

	// Get current market price from listing
	listing, err := s.listingRepo.GetByID(sellerHolding.ListingID)
	if err != nil {
		return nil, fmt.Errorf("listing not found for OTC offer: %w", ErrListingNotFound)
	}

	pricePerUnit := listing.Price
	totalPrice := pricePerUnit.Mul(decimal.NewFromInt(quantity))

	// Commission: same as Market order = min(14% × total, $7)
	commission := totalPrice.Mul(decimal.NewFromFloat(0.14))
	cap := decimal.NewFromFloat(7)
	if commission.GreaterThan(cap) {
		commission = cap
	}
	commission = commission.Round(2)

	// Each BuyOffer call gets a fresh UUID-scoped key so the per-step
	// idempotency_keys below stay deterministic within this call (so a
	// compensation step replays the original step's cached response on
	// retry) while two distinct buys never collide on the same key.
	otcTxID := uuid.New().String()
	buyerDebitKey := "otc-buy-" + otcTxID + ":buyer-debit"
	sellerCreditKey := "otc-buy-" + otcTxID + ":seller-credit"
	compBuyerOnSellerLookup := "otc-buy-" + otcTxID + ":comp-buyer-seller-lookup"
	compBuyerOnSellerCredit := "otc-buy-" + otcTxID + ":comp-buyer-seller-credit"
	compBuyerOnCapGain := "otc-buy-" + otcTxID + ":comp-buyer-capgain"
	compSellerOnCapGain := "otc-buy-" + otcTxID + ":comp-seller-capgain"

	// Debit buyer's account: total + commission
	buyerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: buyerAccountID})
	if err != nil {
		return nil, fmt.Errorf("buyer account not found: %w", ErrOTCBuyerAccountNotFound)
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   buyerAcct.AccountNumber,
		Amount:          totalPrice.Add(commission).Neg().StringFixed(4),
		UpdateAvailable: true,
		IdempotencyKey:  buyerDebitKey,
	})
	if err != nil {
		return nil, errors.New("failed to debit buyer account: " + err.Error())
	}

	// Credit seller's account: total. Holdings aggregate across accounts and
	// AccountID is the audit "last-used" account — use that as the proceeds
	// destination. A seller whose holding has no recorded account cannot
	// participate in OTC; surface that explicitly.
	if sellerHolding.AccountID == 0 {
		return nil, fmt.Errorf("seller holding has no recorded account for OTC: %w", ErrOTCSellerNoAccount)
	}
	sellerAccountID := sellerHolding.AccountID
	sellerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: sellerAccountID})
	if err != nil {
		// Compensate: re-credit buyer since debit succeeded
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
			IdempotencyKey:  compBuyerOnSellerLookup,
		})
		return nil, fmt.Errorf("seller account not found: %w", ErrOTCSellerAccountNotFound)
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   sellerAcct.AccountNumber,
		Amount:          totalPrice.StringFixed(4),
		UpdateAvailable: true,
		IdempotencyKey:  sellerCreditKey,
	})
	if err != nil {
		// Compensate: re-credit buyer since debit succeeded
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
			IdempotencyKey:  compBuyerOnSellerCredit,
		})
		return nil, errors.New("failed to credit seller account: " + err.Error())
	}

	// Record capital gain for seller
	gain := pricePerUnit.Sub(sellerHolding.AveragePrice).Mul(decimal.NewFromInt(quantity))
	capitalGain := &model.CapitalGain{
		OwnerType:        sellerHolding.OwnerType,
		OwnerID:          sellerHolding.OwnerID,
		OTC:              true,
		SecurityType:     sellerHolding.SecurityType,
		Ticker:           sellerHolding.Ticker,
		Quantity:         quantity,
		BuyPricePerUnit:  sellerHolding.AveragePrice,
		SellPricePerUnit: pricePerUnit,
		TotalGain:        gain,
		Currency:         listing.Exchange.Currency,
		AccountID:        sellerAccountID,
		TaxYear:          time.Now().Year(),
		TaxMonth:         int(time.Now().Month()),
	}
	if err := s.capitalGainRepo.Create(capitalGain); err != nil {
		// Compensate: reverse both balance changes
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
			IdempotencyKey:  compBuyerOnCapGain,
		})
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   sellerAcct.AccountNumber,
			Amount:          totalPrice.Neg().StringFixed(4),
			UpdateAvailable: true,
			IdempotencyKey:  compSellerOnCapGain,
		})
		return nil, err
	}

	// Decrease seller's holding
	sellerHolding.Quantity -= quantity
	sellerHolding.PublicQuantity -= quantity
	if sellerHolding.Quantity == 0 {
		if err := s.holdingRepo.Delete(sellerHolding.ID); err != nil {
			return nil, err
		}
	} else {
		if err := s.holdingRepo.Update(sellerHolding); err != nil {
			return nil, err
		}
	}

	// Create/update buyer's holding
	buyerFirstName, buyerLastName := "", ""
	if s.nameResolver != nil {
		fn, ln, resolveErr := s.nameResolver(buyerOwnerType, buyerOwnerID)
		if resolveErr == nil {
			buyerFirstName, buyerLastName = fn, ln
		}
	}

	buyerHolding := &model.Holding{
		OwnerType:     buyerOwnerType,
		OwnerID:       buyerOwnerID,
		UserFirstName: buyerFirstName,
		UserLastName:  buyerLastName,
		SecurityType:  sellerHolding.SecurityType,
		SecurityID:    listing.SecurityID,
		ListingID:     sellerHolding.ListingID,
		Ticker:        sellerHolding.Ticker,
		Name:          sellerHolding.Name,
		Quantity:      quantity,
		AveragePrice:  pricePerUnit,
		AccountID:     buyerAccountID,
	}
	if err := s.holdingRepo.Upsert(context.Background(), buyerHolding); err != nil {
		return nil, err
	}

	StockOTCTradesTotal.Inc()

	return &OTCBuyResult{
		ID:           buyerHolding.ID,
		OfferID:      offerID,
		Quantity:     quantity,
		PricePerUnit: pricePerUnit,
		TotalPrice:   totalPrice,
		Commission:   commission,
	}, nil
}

type OTCBuyResult struct {
	ID           uint64
	OfferID      uint64
	Quantity     int64
	PricePerUnit decimal.Decimal
	TotalPrice   decimal.Decimal
	Commission   decimal.Decimal
}
