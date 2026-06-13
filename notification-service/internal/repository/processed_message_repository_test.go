package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/exbanka/notification-service/internal/model"
)

func newProcessedMsgRepo(t *testing.T) *ProcessedMessageRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ProcessedMessage{}))
	return NewProcessedMessageRepository(db)
}

func TestProcessedMessageRepository_SeenAndMark(t *testing.T) {
	r := newProcessedMsgRepo(t)
	ctx := context.Background()

	seen, err := r.Seen(ctx, "k1")
	require.NoError(t, err)
	require.False(t, seen, "unmarked key must be unseen")

	require.NoError(t, r.Mark(ctx, "k1"))

	seen, err = r.Seen(ctx, "k1")
	require.NoError(t, err)
	require.True(t, seen, "marked key must be seen")

	// Re-marking the same key is a no-op (ON CONFLICT DO NOTHING), not an error —
	// this is what makes a redelivered message a safe dedup hit.
	require.NoError(t, r.Mark(ctx, "k1"))

	seen, err = r.Seen(ctx, "other")
	require.NoError(t, err)
	require.False(t, seen, "a different key is still unseen")
}
