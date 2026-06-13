package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/card-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

// seedChangelog inserts n entries (all by actor) so ListAllChangelogs has rows.
func seedChangelogEntries(t *testing.T, repo *repository.ChangelogRepository, n int, actor int64, action string) {
	t.Helper()
	now := time.Now().Truncate(time.Second)
	for i := 0; i < n; i++ {
		require.NoError(t, repo.Create(changelog.Entry{
			EntityType: "card", EntityID: int64(i + 1), Action: action,
			ChangedBy: actor, ChangedAt: now,
		}))
	}
}

// TestChangelogService_ListAllChangelogs_ReturnsRows verifies the wrapper
// forwards to the repository and returns the rows + total unfiltered.
func TestChangelogService_ListAllChangelogs_ReturnsRows(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	seedChangelogEntries(t, repo, 3, 5, "update")

	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}

// TestChangelogService_ListAllChangelogs_FilterByActor verifies a filter is
// passed through to the repository.
func TestChangelogService_ListAllChangelogs_FilterByActor(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	seedChangelogEntries(t, repo, 2, 5, "update")
	seedChangelogEntries(t, repo, 1, 9, "block")

	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{ActorID: 9}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(9), rows[0].ChangedBy)
}

// TestChangelogService_ListAllChangelogs_PaginationDefaultsAndCap exercises the
// page<1, pageSize<=0 default and the >200 cap branches.
func TestChangelogService_ListAllChangelogs_PaginationDefaultsAndCap(t *testing.T) {
	db := newCardChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	seedChangelogEntries(t, repo, 1, 1, "update")

	// page<1 and pageSize<=0 default to 1 and 50 respectively.
	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)

	// pageSize>200 is capped (no error, returns the single row).
	rows, total, err = svc.ListAllChangelogs(repository.ChangelogFilters{}, 1, 5000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, rows, 1)
}
