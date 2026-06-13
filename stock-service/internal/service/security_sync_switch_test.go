package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// settingRepoSetErr fails on Set so SwitchSource's persist-the-choice step errors.
type settingRepoSetErr struct{}

func (settingRepoSetErr) Get(string) (string, error) { return "", nil }
func (settingRepoSetErr) Set(string, string) error   { return errors.New("set failed") }

// erroringWiper fails WipeAll so SwitchSource's wipe step errors.
type erroringWiper struct{}

func (erroringWiper) WipeAll() error { return errors.New("wipe failed") }

func buildSwitchSvc(t *testing.T, settings SettingRepo, wipe Wiper, initial *stubSource) *SecuritySyncService {
	t.Helper()
	stockRepo := &syncMockStockRepo{}
	futuresRepo := &syncMockFuturesRepo{}
	forexRepo := &syncMockForexRepo{}
	listingRepo := newSyncMockListingRepo()
	listingSvc := NewListingService(listingRepo, nil, stockRepo, futuresRepo, forexRepo)
	return NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, newSyncMockOptionRepo(),
		nil, settings, listingSvc, nil, nil, nil, nil, initial, wipe,
	)
}

func TestSwitchSource_SetActiveSourceFails(t *testing.T) {
	svc := buildSwitchSvc(t, settingRepoSetErr{}, &fakeWiper{}, &stubSource{name: "external"})
	err := svc.SwitchSource(context.Background(), &stubSource{name: "generated"})
	if err == nil {
		t.Fatal("expected error when settingRepo.Set fails")
	}
	status, lastErr, _, _ := svc.GetStatus()
	if status != "failed" || lastErr == "" {
		t.Errorf("status=%s lastErr=%q, want failed + message", status, lastErr)
	}
}

func TestSwitchSource_WipeFails(t *testing.T) {
	svc := buildSwitchSvc(t, &syncMockSettingRepo{}, erroringWiper{}, &stubSource{name: "external"})
	err := svc.SwitchSource(context.Background(), &stubSource{name: "generated"})
	if err == nil {
		t.Fatal("expected error when WipeAll fails")
	}
	status, lastErr, _, _ := svc.GetStatus()
	if status != "failed" {
		t.Errorf("status=%s, want failed", status)
	}
	if lastErr == "" {
		t.Error("expected lastErr to be populated")
	}
}

func TestWithHistoryBackfill(t *testing.T) {
	svc := buildSwitchSvc(t, &syncMockSettingRepo{}, &fakeWiper{}, &stubSource{name: "generated"})
	// Passing a non-nil backfill (zero-value) attaches it without running.
	bf := &ListingHistoryBackfill{}
	out := svc.WithHistoryBackfill(bf)
	if out.backfill != bf {
		t.Error("WithHistoryBackfill did not attach the backfill")
	}
}

func TestSecuritySyncService_GenerateAllOptions_StockListError(t *testing.T) {
	// stockRepo.List error → generateAllOptions logs and returns without panic.
	stockRepo := &listErrStockRepo{}
	listingRepo := newSyncMockListingRepo()
	listingSvc := NewListingService(listingRepo, nil, stockRepo, nil, nil)
	svc := NewSecuritySyncService(
		stockRepo, nil, nil, newSyncMockOptionRepo(),
		nil, &syncMockSettingRepo{}, listingSvc, nil, nil, nil, nil,
		&stubSource{name: "generated"}, nil,
	)
	svc.GenerateAllOptionsForTest() // must not panic
}

// listErrStockRepo returns an error from List to exercise the failure branch.
type listErrStockRepo struct{ syncMockStockRepo }

func (m *listErrStockRepo) List(repository.StockFilter) ([]model.Stock, int64, error) {
	return nil, 0, errors.New("list failed")
}
