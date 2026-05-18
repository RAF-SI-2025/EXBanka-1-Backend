package service

import (
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/exbanka/contract/shared/svcerr"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// WatchlistEntry is the enriched per-row view returned to handlers — adds
// ticker + percent-change on top of the repository projection.
type WatchlistEntry struct {
	ID                 uint64
	ListingID          uint64
	SecurityType       string
	Ticker             string
	CurrentPrice       decimal.Decimal
	DailyChange        decimal.Decimal
	DailyChangePercent decimal.Decimal
	AddedAtUnix        int64
}

// WatchlistService wires the watchlist repo with existence checks and
// per-item ticker resolution.
type WatchlistService struct {
	repo        *repository.WatchlistRepository
	listingRepo ListingRepo
	stocks      stockTickerLookup
	options     optionTickerLookup
	futures     futuresTickerLookup
	forex       forexTickerLookup
}

// stockTickerLookup / etc — narrow interfaces over existing repos so
// tests can stub without dragging in the full repo types. Each returns
// just the ticker for a given security_id.
type stockTickerLookup interface {
	GetByID(id uint64) (*model.Stock, error)
}
type optionTickerLookup interface {
	GetByID(id uint64) (*model.Option, error)
}
type futuresTickerLookup interface {
	GetByID(id uint64) (*model.FuturesContract, error)
}
type forexTickerLookup interface {
	GetByID(id uint64) (*model.ForexPair, error)
}

func NewWatchlistService(
	repo *repository.WatchlistRepository,
	listingRepo ListingRepo,
	stocks stockTickerLookup,
	options optionTickerLookup,
	futures futuresTickerLookup,
	forex forexTickerLookup,
) *WatchlistService {
	return &WatchlistService{
		repo:        repo,
		listingRepo: listingRepo,
		stocks:      stocks,
		options:     options,
		futures:     futures,
		forex:       forex,
	}
}

var (
	// ErrWatchlistListingNotFound — caller pointed at a listing_id that
	// doesn't exist. Mapped to gRPC NotFound → HTTP 404 at the gateway.
	ErrWatchlistListingNotFound = svcerr.New(codes.NotFound, "listing not found")

	// ErrWatchlistEntryNotFound — Remove called with no matching row.
	ErrWatchlistEntryNotFound = svcerr.New(codes.NotFound, "watchlist entry not found")
)

// Add inserts a row after verifying the listing exists. Idempotent on
// the (owner, listing) pair (no error on double-add).
func (s *WatchlistService) Add(ownerType model.OwnerType, ownerID *uint64, listingID uint64) error {
	if _, err := s.listingRepo.GetByID(listingID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWatchlistListingNotFound
		}
		return err
	}
	item := &model.WatchlistItem{
		OwnerType: ownerType,
		OwnerID:   ownerID,
		ListingID: listingID,
	}
	return s.repo.Add(item)
}

// Remove returns ErrWatchlistEntryNotFound when the row was not present.
func (s *WatchlistService) Remove(ownerType model.OwnerType, ownerID *uint64, listingID uint64) error {
	removed, err := s.repo.Remove(ownerType, ownerID, listingID)
	if err != nil {
		return err
	}
	if !removed {
		return ErrWatchlistEntryNotFound
	}
	return nil
}

// List returns enriched entries with current prices + tickers.
func (s *WatchlistService) List(ownerType model.OwnerType, ownerID *uint64, listingType string) ([]WatchlistEntry, error) {
	rows, err := s.repo.ListWithListings(ownerType, ownerID, listingType)
	if err != nil {
		return nil, err
	}
	out := make([]WatchlistEntry, 0, len(rows))
	for _, r := range rows {
		entry := WatchlistEntry{
			ID:                 r.ID,
			ListingID:          r.ListingID,
			SecurityType:       r.SecurityType,
			CurrentPrice:       r.Price,
			DailyChange:        r.DailyChange,
			DailyChangePercent: dailyChangePercent(r.Price, r.DailyChange),
			AddedAtUnix:        r.AddedAt.Unix(),
		}
		entry.Ticker = s.resolveTicker(r.SecurityType, r.SecurityID)
		out = append(out, entry)
	}
	return out, nil
}

// resolveTicker fans out to the type-specific repo. Missing rows surface
// as empty tickers — the watchlist list endpoint stays best-effort on
// the enrichment side; the row IDs remain authoritative.
func (s *WatchlistService) resolveTicker(securityType string, securityID uint64) string {
	switch securityType {
	case "stock":
		if s.stocks == nil {
			return ""
		}
		st, err := s.stocks.GetByID(securityID)
		if err != nil || st == nil {
			return ""
		}
		return st.Ticker
	case "option":
		if s.options == nil {
			return ""
		}
		o, err := s.options.GetByID(securityID)
		if err != nil || o == nil {
			return ""
		}
		return o.Ticker
	case "futures":
		if s.futures == nil {
			return ""
		}
		f, err := s.futures.GetByID(securityID)
		if err != nil || f == nil {
			return ""
		}
		return f.Ticker
	case "forex":
		if s.forex == nil {
			return ""
		}
		f, err := s.forex.GetByID(securityID)
		if err != nil || f == nil {
			return ""
		}
		return f.Ticker
	}
	return ""
}

// dailyChangePercent returns (change / (price - change)) * 100 with safe
// fallback when the previous close was zero.
func dailyChangePercent(price, change decimal.Decimal) decimal.Decimal {
	prev := price.Sub(change)
	if prev.IsZero() {
		return decimal.Zero
	}
	return change.Div(prev).Mul(decimal.NewFromInt(100)).Round(4)
}
