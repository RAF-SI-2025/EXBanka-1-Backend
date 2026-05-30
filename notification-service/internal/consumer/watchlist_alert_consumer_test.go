package consumer

import (
	"errors"
	"sync"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubWatchlistNotifCreator records CreateWithIdempotency calls and
// enforces dedup: a second call with the same key returns (false, nil).
type stubWatchlistNotifCreator struct {
	mu        sync.Mutex
	created   []*model.GeneralNotification
	keys      map[string]struct{}
	createErr error
}

func (s *stubWatchlistNotifCreator) CreateWithIdempotency(n *model.GeneralNotification, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return false, s.createErr
	}
	if s.keys == nil {
		s.keys = make(map[string]struct{})
	}
	if _, exists := s.keys[key]; exists {
		return false, nil // dedup hit
	}
	s.keys[key] = struct{}{}
	s.created = append(s.created, n)
	return true, nil
}

// TestWatchlistAlertConsumer_HappyPath verifies a valid message is rendered
// and written to the notification store.
func TestWatchlistAlertConsumer_HappyPath(t *testing.T) {
	repo := &stubWatchlistNotifCreator{}
	tmpl := &stubGeneralRenderer{subject: "AAPL moved -6.35%", body: "AAPL on your watchlist moved -6.35% today."}
	c := newWatchlistAlertConsumerForTest(repo, tmpl)

	today := time.Now().UTC().Format("20060102")
	msg := kafkamsg.WatchlistPriceMoveMessage{
		UserID:         42,
		Ticker:         "AAPL",
		PercentMove:    "-6.3500",
		CurrentPrice:   "100.0000",
		Timestamp:      time.Now().UTC(),
		IdempotencyKey: "watchlist-alert-42-AAPL-" + today,
	}
	c.handleMessage(mustMarshal(t, msg))

	require.Len(t, repo.created, 1)
	got := repo.created[0]
	assert.Equal(t, uint64(42), got.UserID)
	assert.Equal(t, "WATCHLIST_PRICE_MOVE", got.Type)
	assert.Equal(t, "AAPL moved -6.35%", got.Title)
	assert.Equal(t, "AAPL on your watchlist moved -6.35% today.", got.Message)

	// Template was invoked with the right fields.
	assert.Equal(t, "WATCHLIST_PRICE_MOVE", tmpl.gotType)
	assert.Equal(t, "push", tmpl.gotChannel)
	assert.Equal(t, "AAPL", tmpl.gotData["ticker"])
	assert.Equal(t, "-6.3500", tmpl.gotData["percent_move"])
	assert.Equal(t, "100.0000", tmpl.gotData["current_price"])
}

// TestWatchlistAlertConsumer_MalformedPayloadIgnored verifies bad JSON does
// not panic and nothing is written.
func TestWatchlistAlertConsumer_MalformedPayloadIgnored(t *testing.T) {
	repo := &stubWatchlistNotifCreator{}
	c := newWatchlistAlertConsumerForTest(repo, &stubGeneralRenderer{subject: "S", body: "B"})
	require.NotPanics(t, func() { c.handleMessage([]byte("not json")) })
	assert.Empty(t, repo.created)
}

// TestWatchlistAlertConsumer_RenderErrorDropsMessage verifies that a
// template render failure drops the message silently.
func TestWatchlistAlertConsumer_RenderErrorDropsMessage(t *testing.T) {
	repo := &stubWatchlistNotifCreator{}
	rend := &stubGeneralRenderer{err: errors.New("template not found")}
	c := newWatchlistAlertConsumerForTest(repo, rend)

	today := time.Now().UTC().Format("20060102")
	msg := kafkamsg.WatchlistPriceMoveMessage{
		UserID: 5, Ticker: "TSLA", PercentMove: "+7.0000", CurrentPrice: "250.0000",
		Timestamp: time.Now().UTC(), IdempotencyKey: "watchlist-alert-5-TSLA-" + today,
	}
	require.NotPanics(t, func() { c.handleMessage(mustMarshal(t, msg)) })
	assert.Empty(t, repo.created, "render error should drop the message")
}

// TestWatchlistAlertConsumer_Idempotency verifies that delivering the same
// message twice (same idempotency key) does not result in two DB rows.
func TestWatchlistAlertConsumer_Idempotency(t *testing.T) {
	repo := &stubWatchlistNotifCreator{}
	tmpl := &stubGeneralRenderer{subject: "S", body: "B"}
	c := newWatchlistAlertConsumerForTest(repo, tmpl)

	today := time.Now().UTC().Format("20060102")
	msg := kafkamsg.WatchlistPriceMoveMessage{
		UserID: 7, Ticker: "NVDA", PercentMove: "-8.0000", CurrentPrice: "400.0000",
		Timestamp: time.Now().UTC(), IdempotencyKey: "watchlist-alert-7-NVDA-" + today,
	}
	payload := mustMarshal(t, msg)

	c.handleMessage(payload)
	c.handleMessage(payload) // second delivery of same message

	// Only 1 row created despite 2 deliveries.
	require.Len(t, repo.created, 1, "consumer must deduplicate via idempotency key")
}

// TestWatchlistAlertConsumer_RepoErrorDoesNotPanic verifies graceful handling
// when the repository fails.
func TestWatchlistAlertConsumer_RepoErrorDoesNotPanic(t *testing.T) {
	repo := &stubWatchlistNotifCreator{createErr: errors.New("db error")}
	c := newWatchlistAlertConsumerForTest(repo, &stubGeneralRenderer{subject: "S", body: "B"})

	msg := kafkamsg.WatchlistPriceMoveMessage{
		UserID: 1, Ticker: "AMZN", PercentMove: "+6.0000", CurrentPrice: "180.0000",
		Timestamp: time.Now().UTC(), IdempotencyKey: "watchlist-alert-1-AMZN-20260101",
	}
	require.NotPanics(t, func() { c.handleMessage(mustMarshal(t, msg)) })
}
