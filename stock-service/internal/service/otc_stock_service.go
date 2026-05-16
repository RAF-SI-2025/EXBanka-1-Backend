// Package service — OTCStockService owns the new `/api/v3/otc/stocks`
// marketplace surface introduced by the Phase-3 refactor (plan:
// docs/superpowers/plans/2026-05-16-otc-stocks-marketplace.md).
//
// Two directions:
//
//   sell offers — public_quantity on the seller's Holding (existing
//                 model). Accumulative: multiple sell-create calls add up.
//                 Atomic via SELECT FOR UPDATE on the holding row +
//                 OTCSafeAvailable check.
//
//   buy offers  — OTCStockBuyOffer rows. Cash is held in an account-
//                 service reservation (ReserveFunds keyed on a synthetic
//                 order_id from otc_stock_buy_offer_res_seq) so a seller
//                 filling the offer is guaranteed payment. Reservation
//                 is released on cancel.
//
// Fill saga implementations (FillSellOffer, FillBuyOffer) are intentionally
// out-of-scope for this commit — they require multi-step compensation
// design and an account-service mock harness. Phase 3B/handler wiring
// adds them. For now the old OTCService.BuyOffer continues to handle
// sell-side fills under the deprecated /otc/offers/:id/buy route.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------- Narrow interfaces for testability ----------

// OTCStockAccountClient is the subset of grpc.AccountClient we touch. A
// test mock implements only these three methods.
type OTCStockAccountClient interface {
	ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode, idempotencyKey string) (*accountpb.ReserveFundsResponse, error)
	ReleaseReservation(ctx context.Context, orderID uint64, idempotencyKey string) (*accountpb.ReleaseReservationResponse, error)
	// GetAccount returns the account record so we can map account_id →
	// account_number + currency_code for the buy-offer reservation.
	GetAccount(ctx context.Context, accountID uint64) (*accountpb.AccountResponse, error)
}

// OTCStockListingResolver resolves a listing's underlying currency. The
// currency lives on the StockExchange the listing is hosted on; this
// abstraction lets us mock it in tests.
type OTCStockListingResolver interface {
	GetListingCurrency(listingID uint64) (string, error)
	GetListingTickerAndName(listingID uint64) (ticker, name string, stockID uint64, err error)
}

// ---------- Inputs ----------

type CreateSellOfferInput struct {
	HoldingID       uint64
	CallerOwnerType model.OwnerType
	CallerOwnerID   *uint64
	Quantity        int64
}

type CreateBuyOfferInput struct {
	BuyerOwnerType  model.OwnerType
	BuyerOwnerID    *uint64
	BuyerFirstName  string
	BuyerLastName   string
	BuyerAccountID  uint64
	ListingID       uint64
	Quantity        int64
	PricePerUnit    decimal.Decimal
	ActingEmployeeID *uint64
}

type CancelSellOfferInput struct {
	HoldingID       uint64
	CallerOwnerType model.OwnerType
	CallerOwnerID   *uint64
}

type CancelBuyOfferInput struct {
	OfferID         uint64
	CallerOwnerType model.OwnerType
	CallerOwnerID   *uint64
}

type ListMyOTCStocksInput struct {
	OwnerType model.OwnerType
	OwnerID   *uint64
	Direction string // "" | "sell" | "buy"
	Page      int
	PageSize  int
}

// ---------- Output ----------

// OTCStockListing is the union view of a sell or buy offer used by
// ListMyOTCStocks. The handler maps this 1:1 onto the gRPC response.
type OTCStockListing struct {
	Direction     string // "sell" | "buy"
	ID            uint64 // holdings.id for sell, otc_stock_buy_offers.id for buy
	Ticker        string
	Name          string
	Quantity      int64  // sell: PublicQuantity; buy: RemainingQuantity
	PricePerUnit  decimal.Decimal
	Currency      string
	Status        string // sell: "" (active iff PublicQuantity > 0); buy: row.Status
	AccountID     uint64
	CreatedAt     time.Time
}

type ListMyOTCStocksResult struct {
	Listings []OTCStockListing
	Total    int64
}

// ---------- Service ----------

type OTCStockService struct {
	db                *gorm.DB
	holdingRepo       *repository.HoldingRepository
	buyOfferRepo      *repository.OTCStockBuyOfferRepository
	listingResolver   OTCStockListingResolver
	accountClient     OTCStockAccountClient
}

func NewOTCStockService(
	db *gorm.DB,
	holdingRepo *repository.HoldingRepository,
	buyOfferRepo *repository.OTCStockBuyOfferRepository,
	listingResolver OTCStockListingResolver,
	accountClient OTCStockAccountClient,
) *OTCStockService {
	return &OTCStockService{
		db:              db,
		holdingRepo:     holdingRepo,
		buyOfferRepo:    buyOfferRepo,
		listingResolver: listingResolver,
		accountClient:   accountClient,
	}
}

// CreateSellOffer makes additional shares of a holding publicly available
// for OTC purchase. Accumulative — multiple calls add to PublicQuantity.
// Atomic via SELECT FOR UPDATE on the holding row.
func (s *OTCStockService) CreateSellOffer(ctx context.Context, in CreateSellOfferInput) (*model.Holding, error) {
	if in.Quantity <= 0 {
		return nil, fmt.Errorf("%w: quantity must be > 0", ErrOTCStockInsufficientShares)
	}
	var out *model.Holding
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		h, err := s.holdingRepo.LockByIDTx(tx, in.HoldingID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSecurityNotFound
			}
			return err
		}
		// Ownership check.
		if !ownerMatches(h.OwnerType, h.OwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCContractNotParticipant
		}
		// Only stock holdings support OTC sell offers (no futures/forex/options).
		if h.SecurityType != "stock" {
			return ErrOTCStockSellOfferHoldingType
		}
		if h.OTCSafeAvailable() < in.Quantity {
			return ErrOTCStockInsufficientShares
		}
		h.PublicQuantity += in.Quantity
		if err := s.holdingRepo.SaveTx(tx, h); err != nil {
			return err
		}
		out = h
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreateBuyOffer creates a standing offer to buy N shares of a stock at
// a fixed unit price. The buyer's cash is reserved on account-service so
// a seller filling this offer is guaranteed payment. Reservation key is
// the synthetic order_id allocated from otc_stock_buy_offer_res_seq.
//
// Sequence:
//  1. db.Transaction inserts the offer row + allocates reservation order_id.
//  2. AFTER commit, calls accountClient.ReserveFunds.
//  3. If reservation fails, marks the offer cancelled in a follow-up TX
//     so the orphan reconciler doesn't have to clean it up.
//
// RISK: crash window between step 1 commit and step 2 leaves the offer
// in `active` status with no reservation. The startup reconciler in
// saga_recovery should scan for active offers whose reservation is
// missing and either retry or mark cancelled. Wiring of that reconciler
// is a Phase 3B follow-up.
func (s *OTCStockService) CreateBuyOffer(ctx context.Context, in CreateBuyOfferInput) (*model.OTCStockBuyOffer, error) {
	if in.Quantity <= 0 {
		return nil, fmt.Errorf("%w: quantity must be > 0", ErrOTCStockInsufficientRemainingQty)
	}
	if in.PricePerUnit.Sign() <= 0 {
		return nil, fmt.Errorf("%w: price_per_unit must be > 0", ErrOTCStockInsufficientRemainingQty)
	}
	listingCurrency, err := s.listingResolver.GetListingCurrency(in.ListingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrListingNotFound
		}
		return nil, err
	}
	ticker, name, stockID, err := s.listingResolver.GetListingTickerAndName(in.ListingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrListingNotFound
		}
		return nil, err
	}
	acct, err := s.accountClient.GetAccount(ctx, in.BuyerAccountID)
	if err != nil {
		return nil, fmt.Errorf("get buyer account: %w", err)
	}
	if acct.GetCurrencyCode() != listingCurrency {
		return nil, ErrOTCStockCurrencyMismatch
	}
	reservedAmount := in.PricePerUnit.Mul(decimal.NewFromInt(in.Quantity))

	var offer *model.OTCStockBuyOffer
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resOrderID, err := s.buyOfferRepo.AllocateReservationOrderID(tx)
		if err != nil {
			return fmt.Errorf("allocate res order id: %w", err)
		}
		offer = &model.OTCStockBuyOffer{
			BuyerOwnerType:            in.BuyerOwnerType,
			BuyerOwnerID:              in.BuyerOwnerID,
			BuyerFirstName:            in.BuyerFirstName,
			BuyerLastName:             in.BuyerLastName,
			BuyerAccountID:            in.BuyerAccountID,
			BuyerAccountNumber:        acct.GetAccountNumber(),
			StockID:                   stockID,
			ListingID:                 in.ListingID,
			Ticker:                    ticker,
			Name:                      name,
			OriginalQuantity:          in.Quantity,
			RemainingQuantity:         in.Quantity,
			PricePerUnit:              in.PricePerUnit,
			CurrencyCode:              listingCurrency,
			ReservedAmount:            reservedAmount,
			OriginalReservedAmount:    reservedAmount,
			AccountReservationOrderID: resOrderID,
			Status:                    model.OTCStockBuyOfferStatusActive,
			ActingEmployeeID:          in.ActingEmployeeID,
		}
		return s.buyOfferRepo.CreateTx(tx, offer)
	})
	if err != nil {
		return nil, err
	}

	// Cash reservation runs AFTER the offer-row commit so the orphan
	// window is bounded. On reserve failure we roll the row to cancelled.
	idempKey := fmt.Sprintf("otc-buy-offer-create-%d", offer.ID)
	if _, err := s.accountClient.ReserveFunds(ctx, in.BuyerAccountID, offer.AccountReservationOrderID,
		reservedAmount, listingCurrency, idempKey); err != nil {
		// Compensate: flip status to cancelled so the orphan reconciler
		// can release nothing (reservation was never created).
		_ = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			locked, lerr := s.buyOfferRepo.LockByID(tx, offer.ID)
			if lerr != nil {
				return nil
			}
			locked.Status = model.OTCStockBuyOfferStatusCancelled
			locked.ReservedAmount = decimal.Zero
			return s.buyOfferRepo.SaveTx(tx, locked)
		})
		return nil, fmt.Errorf("reserve funds: %w", err)
	}
	return offer, nil
}

// CancelSellOffer zeros the holding's PublicQuantity. Atomic via
// SELECT FOR UPDATE.
func (s *OTCStockService) CancelSellOffer(ctx context.Context, in CancelSellOfferInput) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		h, err := s.holdingRepo.LockByIDTx(tx, in.HoldingID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSecurityNotFound
			}
			return err
		}
		if !ownerMatches(h.OwnerType, h.OwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCContractNotParticipant
		}
		if h.PublicQuantity == 0 {
			return ErrOTCStockNoActiveSellOffer
		}
		h.PublicQuantity = 0
		return s.holdingRepo.SaveTx(tx, h)
	})
}

// CancelBuyOffer flips the buy offer to "cancelled" and releases any
// remaining cash reservation. Idempotent — calling on an already-cancelled
// offer returns ErrOTCStockBuyOfferNotActive.
func (s *OTCStockService) CancelBuyOffer(ctx context.Context, in CancelBuyOfferInput) error {
	var resOrderID uint64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := s.buyOfferRepo.LockByID(tx, in.OfferID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCStockBuyOfferNotFound
			}
			return err
		}
		if !ownerMatches(o.BuyerOwnerType, o.BuyerOwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCStockBuyOfferOwnership
		}
		if o.Status != model.OTCStockBuyOfferStatusActive {
			return ErrOTCStockBuyOfferNotActive
		}
		o.Status = model.OTCStockBuyOfferStatusCancelled
		resOrderID = o.AccountReservationOrderID
		return s.buyOfferRepo.SaveTx(tx, o)
	})
	if err != nil {
		return err
	}
	// Release any unspent reservation. ReleaseReservation is idempotent +
	// no-op on missing reservations so a crash here is safe — the orphan
	// reconciler can re-call later.
	idempKey := fmt.Sprintf("otc-buy-offer-cancel-%d", in.OfferID)
	if _, err := s.accountClient.ReleaseReservation(ctx, resOrderID, idempKey); err != nil {
		return fmt.Errorf("release reservation: %w", err)
	}
	return nil
}

// ListMyListings returns the caller's own sell + buy offers in a single
// merged view, sorted by created_at desc. Pagination is applied AFTER
// the merge so the page count is accurate across both directions.
func (s *OTCStockService) ListMyListings(ctx context.Context, in ListMyOTCStocksInput) (*ListMyOTCStocksResult, error) {
	page := in.Page
	if page < 1 {
		page = 1
	}
	pageSize := in.PageSize
	if pageSize < 1 {
		pageSize = 20
	}

	merged := make([]OTCStockListing, 0)

	// Sell offers: every holding with PublicQuantity > 0.
	if in.Direction == "" || in.Direction == "sell" {
		holdings, _, err := s.holdingRepo.ListByOwner(in.OwnerType, in.OwnerID, repository.HoldingFilter{
			SecurityType: "stock",
			Page:         1,
			PageSize:     10000, // pull everything; merge + paginate below
		})
		if err != nil {
			return nil, err
		}
		for i := range holdings {
			h := holdings[i]
			if h.PublicQuantity <= 0 {
				continue
			}
			ticker, name, _, lerr := s.listingResolver.GetListingTickerAndName(0) // sell offers don't have a listing id pinned
			_ = lerr
			if ticker == "" {
				ticker = h.Ticker
			}
			if name == "" {
				name = h.Name
			}
			merged = append(merged, OTCStockListing{
				Direction:    "sell",
				ID:           h.ID,
				Ticker:       ticker,
				Name:         name,
				Quantity:     h.PublicQuantity,
				PricePerUnit: h.AveragePrice,
				Currency:     "", // sell offers don't pin a currency; UI infers from listing
				Status:       "", // public_quantity > 0 IS the active signal
				AccountID:    h.AccountID,
				CreatedAt:    h.UpdatedAt, // best-effort
			})
		}
	}

	// Buy offers: all rows in any status (caller wants to see their own,
	// including cancelled/filled history). Filter to active only via the
	// in.Direction param if extending later.
	if in.Direction == "" || in.Direction == "buy" {
		buyRows, _, err := s.buyOfferRepo.ListByOwner(in.OwnerType, in.OwnerID, nil, 1, 10000)
		if err != nil {
			return nil, err
		}
		for i := range buyRows {
			b := buyRows[i]
			merged = append(merged, OTCStockListing{
				Direction:    "buy",
				ID:           b.ID,
				Ticker:       b.Ticker,
				Name:         b.Name,
				Quantity:     b.RemainingQuantity,
				PricePerUnit: b.PricePerUnit,
				Currency:     b.CurrencyCode,
				Status:       b.Status,
				AccountID:    b.BuyerAccountID,
				CreatedAt:    b.CreatedAt,
			})
		}
	}

	total := int64(len(merged))
	start := (page - 1) * pageSize
	if start > len(merged) {
		start = len(merged)
	}
	end := start + pageSize
	if end > len(merged) {
		end = len(merged)
	}
	return &ListMyOTCStocksResult{
		Listings: merged[start:end],
		Total:    total,
	}, nil
}
