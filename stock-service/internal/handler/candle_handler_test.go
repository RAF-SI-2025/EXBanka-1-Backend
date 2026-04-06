package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

func TestGetCandles_MissingListingID(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		Interval: "1h",
		From:     "2026-04-01T00:00:00Z",
		To:       "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing_id is required")
}

func TestGetCandles_MissingInterval(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		From:      "2026-04-01T00:00:00Z",
		To:        "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interval is required")
}

func TestGetCandles_InvalidFromTimestamp(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "not-a-timestamp",
		To:        "2026-04-04T00:00:00Z",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid from timestamp")
}

func TestGetCandles_InvalidToTimestamp(t *testing.T) {
	h := NewSecurityHandler(nil, nil, nil)
	_, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "2026-04-01T00:00:00Z",
		To:        "not-a-timestamp",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid to timestamp")
}

func TestGetCandles_NilCandleService_ReturnsEmpty(t *testing.T) {
	// CandleService with nil influx client returns empty candles (graceful degradation)
	h := NewSecurityHandler(nil, nil, service.NewCandleService(nil))
	resp, err := h.GetCandles(context.Background(), &pb.GetCandlesRequest{
		ListingId: 1,
		Interval:  "1h",
		From:      "2026-04-01T00:00:00Z",
		To:        "2026-04-04T00:00:00Z",
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Candles)
	assert.Equal(t, int64(0), resp.Count)
}
