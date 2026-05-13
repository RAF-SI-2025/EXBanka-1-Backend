package service

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// listingCronDailyMock captures upserts and exposes a configurable
// GetHistory response.
type listingCronDailyMock struct {
	upserts     []*model.ListingDailyPriceInfo
	upsertErr   error
	historyByID map[uint64][]model.ListingDailyPriceInfo
	historyErr  error
}

func (m *listingCronDailyMock) Create(_ *model.ListingDailyPriceInfo) error { return nil }
func (m *listingCronDailyMock) UpsertByListingAndDate(info *model.ListingDailyPriceInfo) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upserts = append(m.upserts, info)
	return nil
}
func (m *listingCronDailyMock) GetHistory(listingID uint64, _, _ time.Time, _, _ int) ([]model.ListingDailyPriceInfo, int64, error) {
	if m.historyErr != nil {
		return nil, 0, m.historyErr
	}
	if m.historyByID == nil {
		return nil, 0, nil
	}
	rows := m.historyByID[listingID]
	return rows, int64(len(rows)), nil
}

// listingCronListingMock: the tiny ListingRepo surface SnapshotDailyPrices /
// SeedInitialSnapshot needs (only ListAll matters for the cron paths).
type listingCronListingMock struct {
	listings []model.Listing
	listErr  error
}

func (m *listingCronListingMock) Create(_ *model.Listing) error { return nil }
func (m *listingCronListingMock) GetByID(_ uint64) (*model.Listing, error) {
	return nil, nil
}
func (m *listingCronListingMock) GetBySecurityIDAndType(_ uint64, _ string) (*model.Listing, error) {
	return nil, nil
}
func (m *listingCronListingMock) ListBySecurityIDsAndType(_ []uint64, _ string) ([]model.Listing, error) {
	return nil, nil
}
func (m *listingCronListingMock) Update(_ *model.Listing) error           { return nil }
func (m *listingCronListingMock) UpsertBySecurity(_ *model.Listing) error { return nil }
func (m *listingCronListingMock) UpsertForOption(_ *model.Listing) (*model.Listing, error) {
	return nil, nil
}
func (m *listingCronListingMock) ListAll() ([]model.Listing, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listings, nil
}
func (m *listingCronListingMock) ListBySecurityType(_ string) ([]model.Listing, error) {
	return nil, nil
}
func (m *listingCronListingMock) UpdatePriceByTicker(_, _ string, _, _, _ decimal.Decimal) error {
	return nil
}

func TestListingCron_NewListingCronService(t *testing.T) {
	c := NewListingCronService(&listingCronListingMock{}, &listingCronDailyMock{}, nil)
	if c == nil {
		t.Fatal("NewListingCronService returned nil")
	}
}

func TestListingCron_SnapshotDailyPrices(t *testing.T) {
	listingRepo := &listingCronListingMock{
		listings: []model.Listing{
			{ID: 1, Price: decimal.NewFromInt(100)},
			{ID: 2, Price: decimal.NewFromInt(200)},
		},
	}
	dailyRepo := &listingCronDailyMock{}
	svc := NewListingCronService(listingRepo, dailyRepo, nil)

	svc.SnapshotDailyPrices()

	if len(dailyRepo.upserts) != 2 {
		t.Errorf("expected 2 upserts, got %d", len(dailyRepo.upserts))
	}
	for _, u := range dailyRepo.upserts {
		if u.Date.IsZero() {
			t.Error("expected non-zero date")
		}
	}
}

func TestListingCron_SnapshotDailyPrices_ListError(t *testing.T) {
	listingRepo := &listingCronListingMock{listErr: errors.New("db down")}
	dailyRepo := &listingCronDailyMock{}
	svc := NewListingCronService(listingRepo, dailyRepo, nil)
	// Just ensure no panic and no upserts
	svc.SnapshotDailyPrices()
	if len(dailyRepo.upserts) != 0 {
		t.Errorf("expected no upserts on list error")
	}
}

func TestListingCron_SnapshotDailyPrices_UpsertErrorContinues(t *testing.T) {
	listingRepo := &listingCronListingMock{
		listings: []model.Listing{{ID: 1}, {ID: 2}},
	}
	dailyRepo := &listingCronDailyMock{upsertErr: errors.New("conflict")}
	svc := NewListingCronService(listingRepo, dailyRepo, nil)

	svc.SnapshotDailyPrices()
	// upserts list will be empty because the mock returns error before append
	if len(dailyRepo.upserts) != 0 {
		t.Errorf("got %d upserts", len(dailyRepo.upserts))
	}
}

func TestListingCron_SeedInitialSnapshot_Empty(t *testing.T) {
	listingRepo := &listingCronListingMock{listings: []model.Listing{{ID: 1}, {ID: 2}}}
	dailyRepo := &listingCronDailyMock{} // GetHistory returns empty
	svc := NewListingCronService(listingRepo, dailyRepo, nil)
	svc.SeedInitialSnapshot()
	if len(dailyRepo.upserts) != 2 {
		t.Errorf("expected 2 seeded snapshots, got %d", len(dailyRepo.upserts))
	}
}

func TestListingCron_SeedInitialSnapshot_AlreadyExists(t *testing.T) {
	listingRepo := &listingCronListingMock{listings: []model.Listing{{ID: 1}}}
	dailyRepo := &listingCronDailyMock{
		historyByID: map[uint64][]model.ListingDailyPriceInfo{
			1: {{ID: 9}},
		},
	}
	svc := NewListingCronService(listingRepo, dailyRepo, nil)
	svc.SeedInitialSnapshot()
	if len(dailyRepo.upserts) != 0 {
		t.Errorf("expected zero upserts when history exists, got %d", len(dailyRepo.upserts))
	}
}

func TestListingCron_SeedInitialSnapshot_ListError(t *testing.T) {
	listingRepo := &listingCronListingMock{listErr: errors.New("oops")}
	dailyRepo := &listingCronDailyMock{}
	svc := NewListingCronService(listingRepo, dailyRepo, nil)
	svc.SeedInitialSnapshot()
	if len(dailyRepo.upserts) != 0 {
		t.Errorf("got %d upserts", len(dailyRepo.upserts))
	}
}
