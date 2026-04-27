package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	sharedsaga "github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// ---------------- mocks ----------------

// fakeHoldingReleaser implements OTCHoldingReleaser for tests of the
// expire saga. Records every call and supports a one-shot fail knob.
type fakeHoldingReleaser struct {
	releaseCalls   int
	failReleaseErr error
}

func (f *fakeHoldingReleaser) ReleaseForOTCContract(_ context.Context, _ uint64) (*ReleaseHoldingResult, error) {
	f.releaseCalls++
	if f.failReleaseErr != nil {
		return nil, f.failReleaseErr
	}
	return &ReleaseHoldingResult{ReleasedQuantity: 0, ReservedQuantity: 0}, nil
}

// ---------------- fixture ----------------

type cbExpireFixture struct {
	saga      *CrossbankExpireSaga
	logsRepo  *repository.InterBankSagaLogRepository
	contracts *repository.OptionContractRepository
	peer      *fakeCrossbankPeer
	releaser  *fakeHoldingReleaser
	publisher *countingPublisher
	contract  *model.OptionContract
}

// crossbankExpireDB opens an in-memory SQLite DB with all the tables the
// crossbank expire saga reads from or writes to.
func crossbankExpireDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.OptionContract{},
		&model.InterBankSagaLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newCrossbankExpireFixture wires the saga + all its deps. ownBank is
// "111" (the seller side). Tests poke fail knobs to drive scenarios.
func newCrossbankExpireFixture(t *testing.T) *cbExpireFixture {
	t.Helper()
	db := crossbankExpireDB(t)
	logsRepo := repository.NewInterBankSagaLogRepository(db)
	contracts := repository.NewOptionContractRepository(db)

	peer := newFakeCrossbankPeer()
	releaser := &fakeHoldingReleaser{}
	publisher := &countingPublisher{}

	// Seed an ACTIVE cross-bank contract. ownBank "111" is the seller.
	buyerCode := "222"
	sellerCode := "111"
	buyerUID := uint64(55)
	sellerUID := uint64(99)
	contract := &model.OptionContract{
		OfferID:         88,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &buyerUID,
		BuyerBankCode:   &buyerCode,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &sellerUID,
		SellerBankCode:  &sellerCode,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5000),
		PremiumPaid:     decimal.NewFromInt(50000),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().AddDate(0, 0, -1).UTC(), // settlement passed
		Status:          model.OptionContractStatusActive,
		SagaID:          "seed-saga-expire",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := contracts.Create(contract); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	saga := NewCrossbankExpireSaga(
		logsRepo,
		nil, // nil producer — countingPublisher replaces the Kafka adapter
		nilPeerRouter{},
		contracts,
		releaser,
		"111", // ownBank = seller
	).withPublisherFactory(func(_ *model.OptionContract, _, _ string) sharedsaga.LifecyclePublisher {
		return publisher
	}).withPeerOps(peer)

	return &cbExpireFixture{
		saga: saga, logsRepo: logsRepo, contracts: contracts,
		peer: peer, releaser: releaser, publisher: publisher,
		contract: contract,
	}
}

// firstExpireSagaTxID returns the tx_id of any row in the inter-bank
// ledger written by the expire saga.
func firstExpireSagaTxID(t *testing.T, fx *cbExpireFixture) string {
	t.Helper()
	farFuture := time.Now().Add(time.Hour)
	for _, status := range []string{
		model.IBSagaStatusFailed,
		model.IBSagaStatusCompleted,
		model.IBSagaStatusPending,
		model.IBSagaStatusCompensated,
		model.IBSagaStatusCompensating,
	} {
		rows, err := fx.logsRepo.ListStaleByStatus(status, farFuture, 50)
		if err == nil {
			for i := range rows {
				if rows[i].SagaKind == model.SagaKindExpire {
					return rows[i].TxID
				}
			}
		}
	}
	t.Fatal("no expire saga rows in ledger; cannot extract tx_id")
	return ""
}

// ---------------- happy path ----------------

func TestCrossbankExpire_HappyPath_BothStepsCompletedAndPublished(t *testing.T) {
	fx := newCrossbankExpireFixture(t)
	if err := fx.saga.ExpireContract(context.Background(), fx.contract.ID); err != nil {
		t.Fatalf("ExpireContract: %v", err)
	}

	// Lifecycle assertions.
	if fx.publisher.startedCalls != 1 {
		t.Errorf("OnStarted: got %d, want 1", fx.publisher.startedCalls)
	}
	if fx.publisher.committedCalls != 1 {
		t.Errorf("OnCommitted: got %d, want 1", fx.publisher.committedCalls)
	}
	if fx.publisher.rolledBackCalls != 0 {
		t.Errorf("OnRolledBack: got %d, want 0", fx.publisher.rolledBackCalls)
	}

	// Side-effect assertions.
	if fx.peer.contractExpireCalls != 1 {
		t.Errorf("peer.ContractExpire calls: got %d, want 1", fx.peer.contractExpireCalls)
	}
	if fx.releaser.releaseCalls != 1 {
		t.Errorf("ReleaseForOTCContract calls: got %d, want 1", fx.releaser.releaseCalls)
	}

	// Contract should be marked EXPIRED.
	got, err := fx.contracts.GetByID(fx.contract.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != model.OptionContractStatusExpired {
		t.Errorf("contract status: got %s, want %s", got.Status, model.OptionContractStatusExpired)
	}
	if got.ExpiredAt == nil {
		t.Error("expected ExpiredAt to be set")
	}

	// Ledger assertions: 2 forward rows, all completed.
	txID := firstExpireSagaTxID(t, fx)
	rows, err := fx.logsRepo.ListByTxID(txID)
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ledger rows: got %d, want 2", len(rows))
	}
	wantPhases := map[string]bool{
		string(sharedsaga.StepRefundReservation): false,
		string(sharedsaga.StepMarkExpired):       false,
	}
	for _, row := range rows {
		if row.Status != model.IBSagaStatusCompleted {
			t.Errorf("row %s: status %s, want completed", row.Phase, row.Status)
		}
		if _, ok := wantPhases[row.Phase]; !ok {
			t.Errorf("unexpected phase %q in ledger", row.Phase)
			continue
		}
		wantPhases[row.Phase] = true
		if row.SagaKind != model.SagaKindExpire {
			t.Errorf("row %s: saga_kind %s, want %s", row.Phase, row.SagaKind, model.SagaKindExpire)
		}
	}
	for p, seen := range wantPhases {
		if !seen {
			t.Errorf("missing phase %q in ledger", p)
		}
	}
}

// TestCrossbankExpire_PeerNotifyFailureIsBestEffort asserts that a peer
// notify failure does NOT fail the saga — the local mark_expired step
// still runs and the contract is marked EXPIRED.
func TestCrossbankExpire_PeerNotifyFailureIsBestEffort(t *testing.T) {
	fx := newCrossbankExpireFixture(t)
	fx.peer.contractExpireErr = errors.New("peer down")

	if err := fx.saga.ExpireContract(context.Background(), fx.contract.ID); err != nil {
		t.Fatalf("ExpireContract: best-effort peer notify failure should not fail saga, got: %v", err)
	}

	if fx.publisher.committedCalls != 1 {
		t.Errorf("OnCommitted: got %d, want 1 (best-effort step 1 should not block commit)", fx.publisher.committedCalls)
	}
	if fx.publisher.rolledBackCalls != 0 {
		t.Errorf("OnRolledBack: got %d, want 0", fx.publisher.rolledBackCalls)
	}

	// Local effects still applied.
	if fx.releaser.releaseCalls != 1 {
		t.Errorf("ReleaseForOTCContract calls: got %d, want 1", fx.releaser.releaseCalls)
	}
	got, _ := fx.contracts.GetByID(fx.contract.ID)
	if got.Status != model.OptionContractStatusExpired {
		t.Errorf("contract status: got %s, want %s", got.Status, model.OptionContractStatusExpired)
	}

	// Both step rows should be completed (step 1 swallowed the peer
	// failure inside the Forward closure).
	txID := firstExpireSagaTxID(t, fx)
	rows, err := fx.logsRepo.ListByTxID(txID)
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	for _, row := range rows {
		if row.Status != model.IBSagaStatusCompleted {
			t.Errorf("row %s: status %s, want completed (best-effort)", row.Phase, row.Status)
		}
	}
}

// TestCrossbankExpire_LocalMarkExpiredFailure asserts that a failure of
// the local mark_expired step (e.g., release reservation rejected)
// surfaces as a saga error and triggers OnRolledBack. There is no
// pre-pivot Backward to invoke (step 1 is no-Backward best-effort), so
// no compensation rows are written.
func TestCrossbankExpire_LocalMarkExpiredFailure(t *testing.T) {
	fx := newCrossbankExpireFixture(t)
	fx.releaser.failReleaseErr = errors.New("reservation already gone")

	err := fx.saga.ExpireContract(context.Background(), fx.contract.ID)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if fx.publisher.committedCalls != 0 {
		t.Errorf("OnCommitted: got %d, want 0", fx.publisher.committedCalls)
	}
	if fx.publisher.rolledBackCalls != 1 {
		t.Errorf("OnRolledBack: got %d, want 1", fx.publisher.rolledBackCalls)
	}
	if fx.publisher.lastFailingStep != string(sharedsaga.StepMarkExpired) {
		t.Errorf("rolled-back failing step: got %q, want %q",
			fx.publisher.lastFailingStep, string(sharedsaga.StepMarkExpired))
	}

	// Contract NOT marked expired.
	got, _ := fx.contracts.GetByID(fx.contract.ID)
	if got.Status == model.OptionContractStatusExpired {
		t.Error("contract should not be expired after mark_expired failed")
	}

	// Ledger: mark_expired in failed status; no compensation rows since
	// step 1 has no Backward.
	txID := firstExpireSagaTxID(t, fx)
	rows, err := fx.logsRepo.ListByTxID(txID)
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	byPhase := map[string]model.InterBankSagaLog{}
	for _, r := range rows {
		byPhase[r.Phase] = r
	}
	mer, ok := byPhase[string(sharedsaga.StepMarkExpired)]
	if !ok {
		t.Fatalf("mark_expired row missing")
	}
	if mer.Status != model.IBSagaStatusFailed {
		t.Errorf("mark_expired status: got %s, want %s", mer.Status, model.IBSagaStatusFailed)
	}
	for phase := range byPhase {
		if len(phase) > len("compensate_") && phase[:len("compensate_")] == "compensate_" {
			t.Errorf("unexpected compensation row %q (no pre-pivot Backward exists)", phase)
		}
	}
}

// TestCrossbankExpire_AlreadyExpiredIsNoop asserts the saga short-circuits
// when invoked on an already-EXPIRED contract.
func TestCrossbankExpire_AlreadyExpiredIsNoop(t *testing.T) {
	fx := newCrossbankExpireFixture(t)
	now := time.Now().UTC()
	fx.contract.Status = model.OptionContractStatusExpired
	fx.contract.ExpiredAt = &now
	if err := fx.contracts.Save(fx.contract); err != nil {
		t.Fatalf("seed expired contract: %v", err)
	}

	if err := fx.saga.ExpireContract(context.Background(), fx.contract.ID); err != nil {
		t.Fatalf("ExpireContract on already-expired: %v", err)
	}

	if fx.publisher.startedCalls != 0 {
		t.Errorf("OnStarted: got %d, want 0 (no-op short-circuit)", fx.publisher.startedCalls)
	}
	if fx.peer.contractExpireCalls != 0 {
		t.Errorf("peer.ContractExpire calls: got %d, want 0", fx.peer.contractExpireCalls)
	}
	if fx.releaser.releaseCalls != 0 {
		t.Errorf("ReleaseForOTCContract calls: got %d, want 0", fx.releaser.releaseCalls)
	}
}
