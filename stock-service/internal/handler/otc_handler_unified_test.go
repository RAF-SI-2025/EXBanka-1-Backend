package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/otccache"
)

func TestOTCHandler_ListUnifiedOffers_FilterAndPaginate(t *testing.T) {
	cache := otccache.New()
	// Seed cache with two offers so we can assert filtering/pagination.
	otccache.SetForTest(cache, otccache.Snapshot{
		Offers: []otccache.Offer{
			{Kind: "local", BankCode: "111", ID: 1, Ticker: "BAC", SecurityType: "stock", Quantity: 3, PricePerUnit: "38.00"},
			{Kind: "remote", BankCode: "333", OwnerID: "1", Ticker: "JNJ", SecurityType: "stock", Quantity: 5, PricePerUnit: "0", Currency: "USD"},
		},
		LastRefresh:  time.Unix(1714345200, 0).UTC(),
		PeersTotal:   2,
		PeersReached: 1,
	})
	h := NewOTCHandlerWithCache(nil, cache)

	resp, err := h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.GetTotalCount())
	require.Equal(t, int32(2), resp.GetPeersTotal())
	require.Equal(t, int32(1), resp.GetPeersReached())
	require.True(t, resp.GetPartial())
	require.Len(t, resp.GetOffers(), 2)

	// Filter by kind=remote.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Kind: "remote", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.GetTotalCount())
	require.Equal(t, "JNJ", resp.GetOffers()[0].GetTicker())

	// Filter by ticker.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Ticker: "BAC", Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.GetTotalCount())
	require.Equal(t, "local", resp.GetOffers()[0].GetKind())

	// Pagination: page 2 of size 1.
	resp, err = h.ListUnifiedOffers(context.Background(), &pb.ListUnifiedOTCOffersRequest{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.Equal(t, int64(2), resp.GetTotalCount())
	require.Len(t, resp.GetOffers(), 1)
}
