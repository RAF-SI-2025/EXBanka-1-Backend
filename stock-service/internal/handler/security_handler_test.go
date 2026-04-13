package handler

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/model"
)

func TestToOptionItem_IncludesListingIDWhenSet(t *testing.T) {
	listingID := uint64(42)
	opt := &model.Option{
		ID:             7,
		Ticker:         "AAPL260116C00200000",
		Name:           "AAPL Call Jan 2026 $200",
		StockID:        1,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromInt(200),
		Premium:        decimal.NewFromFloat(5.75),
		SettlementDate: time.Now().AddDate(1, 0, 0),
		ListingID:      &listingID,
	}
	item := toOptionItem(opt)
	require.NotNil(t, item)
	require.NotNil(t, item.ListingId, "ListingId should be set on the proto message")
	require.Equal(t, uint64(42), *item.ListingId)
}

func TestToOptionItem_LeavesListingIDNilWhenUnset(t *testing.T) {
	opt := &model.Option{
		ID:             7,
		Ticker:         "X",
		Name:           "X",
		StrikePrice:    decimal.Zero,
		Premium:        decimal.Zero,
		SettlementDate: time.Now().AddDate(1, 0, 0),
		ListingID:      nil,
	}
	item := toOptionItem(opt)
	require.NotNil(t, item)
	require.Nil(t, item.ListingId)
}

func TestToOptionDetail_IncludesListingIDWhenSet(t *testing.T) {
	listingID := uint64(99)
	opt := &model.Option{
		ID:             3,
		Ticker:         "MSFT260116P00400000",
		Name:           "MSFT Put Jan 2026 $400",
		StockID:        2,
		OptionType:     "put",
		StrikePrice:    decimal.NewFromInt(400),
		Premium:        decimal.NewFromFloat(3.25),
		SettlementDate: time.Now().AddDate(1, 0, 0),
		ListingID:      &listingID,
	}
	detail := toOptionDetail(opt)
	require.NotNil(t, detail)
	require.NotNil(t, detail.ListingId)
	require.Equal(t, uint64(99), *detail.ListingId)
}

func TestToOptionDetail_LeavesListingIDNilWhenUnset(t *testing.T) {
	opt := &model.Option{
		ID:             3,
		Ticker:         "X",
		Name:           "X",
		StrikePrice:    decimal.Zero,
		Premium:        decimal.Zero,
		SettlementDate: time.Now().AddDate(1, 0, 0),
		ListingID:      nil,
	}
	detail := toOptionDetail(opt)
	require.NotNil(t, detail)
	require.Nil(t, detail.ListingId)
}
