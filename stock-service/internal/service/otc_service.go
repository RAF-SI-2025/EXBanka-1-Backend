package service

import (
	"context"
	"errors"
	"time"

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
func (s *OTCService) BuyOffer(
	offerID uint64, // holding ID of the seller
	buyerID uint64,
	buyerSystemType string,
	quantity int64,
	buyerAccountID uint64,
) (*OTCBuyResult, error) {
	// Get seller's holding
	sellerHolding, err := s.holdingRepo.GetByID(offerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("OTC offer not found")
		}
		return nil, err
	}
	if sellerHolding.PublicQuantity < quantity {
		return nil, errors.New("insufficient public quantity for OTC purchase")
	}
	if sellerHolding.UserID == buyerID {
		return nil, errors.New("cannot buy your own OTC offer")
	}

	// Get current market price from listing
	listing, err := s.listingRepo.GetByID(sellerHolding.ListingID)
	if err != nil {
		return nil, errors.New("listing not found for OTC offer")
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

	// Debit buyer's account: total + commission
	buyerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: buyerAccountID})
	if err != nil {
		return nil, errors.New("buyer account not found")
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   buyerAcct.AccountNumber,
		Amount:          totalPrice.Add(commission).Neg().StringFixed(4),
		UpdateAvailable: true,
	})
	if err != nil {
		return nil, errors.New("failed to debit buyer account: " + err.Error())
	}

	// Credit seller's account: total. Holdings aggregate across accounts and
	// AccountID is the audit "last-used" account — use that as the proceeds
	// destination. A seller whose holding has no recorded account cannot
	// participate in OTC; surface that explicitly.
	if sellerHolding.AccountID == 0 {
		return nil, errors.New("seller holding has no recorded account for OTC")
	}
	sellerAccountID := sellerHolding.AccountID
	sellerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: sellerAccountID})
	if err != nil {
		// Compensate: re-credit buyer since debit succeeded
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
		})
		return nil, errors.New("seller account not found")
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   sellerAcct.AccountNumber,
		Amount:          totalPrice.StringFixed(4),
		UpdateAvailable: true,
	})
	if err != nil {
		// Compensate: re-credit buyer since debit succeeded
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
		})
		return nil, errors.New("failed to credit seller account: " + err.Error())
	}

	// Record capital gain for seller
	gain := pricePerUnit.Sub(sellerHolding.AveragePrice).Mul(decimal.NewFromInt(quantity))
	capitalGain := &model.CapitalGain{
		UserID:           sellerHolding.UserID,
		SystemType:       sellerHolding.SystemType,
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
		})
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   sellerAcct.AccountNumber,
			Amount:          totalPrice.Neg().StringFixed(4),
			UpdateAvailable: true,
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
		fn, ln, resolveErr := s.nameResolver(buyerID, buyerSystemType)
		if resolveErr == nil {
			buyerFirstName, buyerLastName = fn, ln
		}
	}

	buyerHolding := &model.Holding{
		UserID:        buyerID,
		SystemType:    buyerSystemType,
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
	if err := s.holdingRepo.Upsert(buyerHolding); err != nil {
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
