package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/service"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newCronTestDB(t *testing.T) (*gorm.DB, *repository.OutboundPeerTxRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.OutboundPeerTx{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, repository.NewOutboundPeerTxRepository(db)
}

func TestOutboundReplayCron_RetriesPendingRow_OnYESCommits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe map[string]any
		_ = json.NewDecoder(r.Body).Decode(&probe)
		if probe["messageType"] == contractsitx.MessageTypeNewTx {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"YES"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-1",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[{"routingNumber":111,"accountId":"111-A","assetId":"RSD","amount":"100","direction":"DEBIT"},{"routingNumber":222,"accountId":"222-B","assetId":"RSD","amount":"100","direction":"CREDIT"}]`,
		Status:         "pending",
	}
	if err := repo.Create(row); err != nil {
		t.Fatalf("create: %v", err)
	}

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup).
		WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-1")
	if got.Status != "committed" {
		t.Errorf("expected committed, got %s last_error=%q", got.Status, got.LastError)
	}
}

func TestOutboundReplayCron_PeerVotesNO_MarksRolledBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"NO","noVotes":[{"reason":"INSUFFICIENT_ASSET"}]}`))
	}))
	defer srv.Close()

	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-no",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[]`,
		Status:         "pending",
	}
	_ = repo.Create(row)

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup).
		WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-no")
	if got.Status != "rolled_back" {
		t.Errorf("expected rolled_back, got %s", got.Status)
	}
	if got.LastError == "" {
		t.Errorf("expected last_error to capture peer reason")
	}
}

func TestOutboundReplayCron_MaxAttemptsExceeded_MarksFailed(t *testing.T) {
	_, repo := newCronTestDB(t)
	row := &model.OutboundPeerTx{
		IdempotenceKey: "cron-fail",
		PeerBankCode:   "222",
		TxKind:         "transfer",
		PostingsJSON:   `[]`,
		Status:         "pending",
		AttemptCount:   4,
	}
	_ = repo.Create(row)

	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return nil, errors.New("should not be called")
	}
	cron := service.NewOutboundReplayCron(repo, httpClient, peerLookup).
		WithMinRetryGap(0).WithMaxAttempts(4)
	cron.Tick(context.Background())

	got, _ := repo.GetByIdempotenceKey("cron-fail")
	if got.Status != "failed" {
		t.Errorf("expected failed, got %s", got.Status)
	}
}
