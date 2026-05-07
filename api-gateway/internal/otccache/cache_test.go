package otccache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCache_GetReturnsCopy ensures Get's returned slice is independent
// of the cache's internal storage — mutating the returned slice must
// not affect subsequent reads.
func TestCache_GetReturnsCopy(t *testing.T) {
	c := New()
	c.set(Snapshot{
		Offers:       []Offer{{Kind: "local", Ticker: "BAC"}},
		LastRefresh:  time.Unix(1, 0),
		PeersTotal:   1,
		PeersReached: 1,
	})
	first := c.Get()
	require.Len(t, first.Offers, 1)
	first.Offers[0].Ticker = "MUTATED"

	second := c.Get()
	require.Equal(t, "BAC", second.Offers[0].Ticker, "internal storage must not be mutated by callers")
}

// TestCache_EmptySnapshot covers the pre-first-refresh case: Get must
// return an empty slice and zero LastRefresh, not nil-panic.
func TestCache_EmptySnapshot(t *testing.T) {
	c := New()
	snap := c.Get()
	require.Empty(t, snap.Offers)
	require.True(t, snap.LastRefresh.IsZero())
	require.Zero(t, snap.PeersTotal)
	require.Zero(t, snap.PeersReached)
}
