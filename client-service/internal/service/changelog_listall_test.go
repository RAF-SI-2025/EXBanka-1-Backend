package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/contract/changelog"
)

func seedAllChangelogs(t *testing.T, repo *repository.ChangelogRepository, base time.Time) {
	t.Helper()
	rows := []changelog.Entry{
		{EntityType: "client", EntityID: 1, Action: "create", ChangedBy: 1, ChangedAt: base.Add(-2 * time.Hour)},
		{EntityType: "client_limit", EntityID: 2, Action: "update", ChangedBy: 2, ChangedAt: base.Add(-1 * time.Hour)},
		{EntityType: "client", EntityID: 3, Action: "update", ChangedBy: 1, ChangedAt: base},
	}
	for _, e := range rows {
		require.NoError(t, repo.Create(e))
	}
}

func TestChangelogService_ListAllChangelogs_ReturnsAllAcrossEntities(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	base := time.Now().UTC().Truncate(time.Second)
	seedAllChangelogs(t, repo, base)

	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}

func TestChangelogService_ListAllChangelogs_AppliesActorFilter(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	base := time.Now().UTC().Truncate(time.Second)
	seedAllChangelogs(t, repo, base)

	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{ActorID: 1}, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	for _, r := range rows {
		assert.Equal(t, int64(1), r.ChangedBy)
	}
}

func TestChangelogService_ListAllChangelogs_PaginationDefaults(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	base := time.Now().UTC().Truncate(time.Second)
	seedAllChangelogs(t, repo, base)

	// page<1 → 1, pageSize<=0 → 50 (default). All 3 rows fit.
	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}

func TestChangelogService_ListAllChangelogs_PageSizeCappedAt200(t *testing.T) {
	db := newClientChangelogTestDB(t)
	repo := repository.NewChangelogRepository(db)
	svc := NewChangelogService(repo)
	base := time.Now().UTC().Truncate(time.Second)
	seedAllChangelogs(t, repo, base)

	// pageSize>200 must be capped (still returns all rows, no error).
	rows, total, err := svc.ListAllChangelogs(repository.ChangelogFilters{}, 1, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 3)
}
