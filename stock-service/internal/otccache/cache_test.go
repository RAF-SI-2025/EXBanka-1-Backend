package otccache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Get must return a defensive copy: callers mutating the returned slice
// cannot corrupt the cache's internal state.
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
	require.Equal(t, "BAC", second.Offers[0].Ticker)
}

// Pre-first-refresh: empty slice, zero LastRefresh, no panic.
func TestCache_EmptySnapshot(t *testing.T) {
	c := New()
	snap := c.Get()
	require.Empty(t, snap.Offers)
	require.True(t, snap.LastRefresh.IsZero())
	require.Zero(t, snap.PeersTotal)
	require.Zero(t, snap.PeersReached)
}
