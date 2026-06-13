package repository

import (
	"testing"
	"time"

	"github.com/exbanka/contract/changelog"
)

// seedChangelogRows inserts three rows with distinct actors/actions/timestamps
// so the ListAll filters can be exercised individually. Timestamps are built
// from epoch seconds via time.Unix so they serialize in the same timezone as
// the filter bind values (which the production code derives from time.Unix),
// keeping SQLite's text-based DATETIME comparison consistent.
func seedChangelogRows(t *testing.T, r *ChangelogRepository, baseEpoch int64) {
	t.Helper()
	rows := []changelog.Entry{
		{EntityType: "client", EntityID: 1, Action: "create", ChangedBy: 1, ChangedAt: time.Unix(baseEpoch-7200, 0)},
		{EntityType: "client_limit", EntityID: 2, Action: "update", ChangedBy: 2, ChangedAt: time.Unix(baseEpoch-3600, 0)},
		{EntityType: "client", EntityID: 3, Action: "update", ChangedBy: 1, ChangedAt: time.Unix(baseEpoch, 0)},
	}
	for _, e := range rows {
		if err := r.Create(e); err != nil {
			t.Fatalf("seed create: %v", err)
		}
	}
}

func TestChangelogRepository_ListAll_NoFilters(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)
	base := time.Now().Unix()
	seedChangelogRows(t, r, base)

	rows, total, err := r.ListAll(ChangelogFilters{}, 1, 50)
	if err != nil {
		t.Fatalf("listall: %v", err)
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("want total=3 len=3, got total=%d len=%d", total, len(rows))
	}
	// Ordered by changed_at DESC — newest row (EntityID=3) first.
	if rows[0].EntityID != 3 {
		t.Fatalf("want newest row first (EntityID=3), got %d", rows[0].EntityID)
	}
}

func TestChangelogRepository_ListAll_ActorFilter(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)
	base := time.Now().Unix()
	seedChangelogRows(t, r, base)

	rows, total, err := r.ListAll(ChangelogFilters{ActorID: 1}, 1, 50)
	if err != nil {
		t.Fatalf("listall: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("actor filter: want 2, got total=%d len=%d", total, len(rows))
	}
	for _, row := range rows {
		if row.ChangedBy != 1 {
			t.Fatalf("actor filter leaked row changed_by=%d", row.ChangedBy)
		}
	}
}

func TestChangelogRepository_ListAll_ActionFilter(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)
	base := time.Now().Unix()
	seedChangelogRows(t, r, base)

	rows, total, err := r.ListAll(ChangelogFilters{Action: "update"}, 1, 50)
	if err != nil {
		t.Fatalf("listall: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Fatalf("action filter: want 2, got total=%d len=%d", total, len(rows))
	}
	for _, row := range rows {
		if row.Action != "update" {
			t.Fatalf("action filter leaked row action=%s", row.Action)
		}
	}
}

func TestChangelogRepository_ListAll_SinceUntilFilters(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)
	base := time.Now().Unix()
	seedChangelogRows(t, r, base)

	cutoff := base - 5400

	// Since cutoff: rows at -1h and now → 2.
	_, totalSince, err := r.ListAll(ChangelogFilters{Since: cutoff}, 1, 50)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if totalSince != 2 {
		t.Fatalf("since filter: want 2, got %d", totalSince)
	}

	// Until cutoff: only the -2h row → 1.
	_, totalUntil, err := r.ListAll(ChangelogFilters{Until: cutoff}, 1, 50)
	if err != nil {
		t.Fatalf("until: %v", err)
	}
	if totalUntil != 1 {
		t.Fatalf("until filter: want 1, got %d", totalUntil)
	}
}

func TestChangelogRepository_ListAll_Pagination(t *testing.T) {
	db := newDB(t)
	r := NewChangelogRepository(db)
	base := time.Now().Unix()
	seedChangelogRows(t, r, base)

	// page 1 size 2 → 2 rows but total 3.
	rows, total, err := r.ListAll(ChangelogFilters{}, 1, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total != 3 || len(rows) != 2 {
		t.Fatalf("page1: want total=3 len=2, got total=%d len=%d", total, len(rows))
	}

	// page 2 size 2 → 1 remaining row.
	rows2, _, err := r.ListAll(ChangelogFilters{}, 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("page2: want 1 remaining, got %d", len(rows2))
	}
}
