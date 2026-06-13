package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestNewHTTPStatusFetcher(t *testing.T) {
	fetcher := newHTTPStatusFetcher(&http.Client{Timeout: 2 * time.Second})
	ctx := context.Background()

	// 200 with isOngoing=true.
	srvOngoing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "tok" {
			t.Errorf("missing api key header")
		}
		_, _ = w.Write([]byte(`{"isOngoing":true}`))
	}))
	defer srvOngoing.Close()
	if ongoing, err := fetcher(ctx, srvOngoing.URL, "tok", "222", "fid"); err != nil || !ongoing {
		t.Errorf("want (true,nil), got (%v,%v)", ongoing, err)
	}

	// 200 with isOngoing=false.
	srvDone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"isOngoing":false}`))
	}))
	defer srvDone.Close()
	if ongoing, err := fetcher(ctx, srvDone.URL, "tok", "222", "fid"); err != nil || ongoing {
		t.Errorf("want (false,nil), got (%v,%v)", ongoing, err)
	}

	// Non-2xx → error (false-cancel guard).
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv500.Close()
	if _, err := fetcher(ctx, srv500.URL, "tok", "222", "fid"); err == nil {
		t.Error("non-2xx should error")
	}

	// Empty body → error.
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srvEmpty.Close()
	if _, err := fetcher(ctx, srvEmpty.URL, "tok", "222", "fid"); err == nil {
		t.Error("empty body should error")
	}

	// Invalid JSON → error.
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{not-json"))
	}))
	defer srvBad.Close()
	if _, err := fetcher(ctx, srvBad.URL, "tok", "222", "fid"); err == nil {
		t.Error("invalid json should error")
	}

	// Transport error (connection refused).
	if _, err := fetcher(ctx, "http://127.0.0.1:0", "tok", "222", "fid"); err == nil {
		t.Error("transport error expected")
	}
}

func TestPortfolioService_ListHoldingTransactions_OwnershipAndNotFound(t *testing.T) {
	svc, mocks := buildPortfolioService()
	owner := uint64(7)
	mocks.holdingRepo.holdings[42] = &model.Holding{
		ID: 42, OwnerType: model.OwnerClient, OwnerID: &owner,
		SecurityType: "stock", SecurityID: 10, Ticker: "AAA", Quantity: 10, AveragePrice: decimal.NewFromInt(50),
	}

	// Missing holding → not found.
	if _, _, err := svc.ListHoldingTransactions(999, model.OwnerClient, &owner, "", 1, 10); !errors.Is(err, ErrHoldingNotFound) {
		t.Fatalf("missing holding → not found, got %v", err)
	}
	// Non-owner → not found (existence not leaked).
	other := uint64(99)
	if _, _, err := svc.ListHoldingTransactions(42, model.OwnerClient, &other, "", 1, 10); !errors.Is(err, ErrHoldingNotFound) {
		t.Fatalf("non-owner → not found, got %v", err)
	}
	// Owner, but no holdingTxRepo wired → empty result, no error.
	rows, total, err := svc.ListHoldingTransactions(42, model.OwnerClient, &owner, "", 1, 10)
	if err != nil || rows != nil || total != 0 {
		t.Fatalf("want empty result with nil tx repo, got rows=%v total=%d err=%v", rows, total, err)
	}
}

func TestListingCron_StartDailyCron_StopsOnCancel(t *testing.T) {
	svc := NewListingCronService(&listingCronListingMock{}, &listingCronDailyMock{}, nil, nilRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // schedule is 23:55; with a cancelled ctx the goroutine exits via ctx.Done.
	svc.StartDailyCron(ctx)
	// Give the goroutine a moment to observe cancellation (no deterministic
	// signal exposed; this only needs to not hang or panic).
	time.Sleep(20 * time.Millisecond)
}
