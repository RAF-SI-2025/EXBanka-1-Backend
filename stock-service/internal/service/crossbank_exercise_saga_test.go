package service

import (
	"context"
	"errors"
	"strings"
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

// ---------------- fixture ----------------

type cbExerciseFixture struct {
	saga      *CrossbankExerciseSaga
	logsRepo  *repository.InterBankSagaLogRepository
	contracts *repository.OptionContractRepository
	accounts  *fakeOTCAccountClient
	transfers *fakeInterBankTransferer
	peer      *fakeCrossbankPeer
	publisher *countingPublisher
	in        CrossbankExerciseInput
	contract  *model.OptionContract
}

// crossbankExerciseDB opens an in-memory SQLite DB with all the tables the
// crossbank exercise saga reads from or writes to.
func crossbankExerciseDB(t *testing.T) *gorm.DB {
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

// newCrossbankExerciseFixture wires the saga + all its deps with sensible
// defaults: peer succeeds on every call, transfer commits, accounts mock
// reserves+releases. Tests poke the fakes' fail knobs to drive scenarios.
func newCrossbankExerciseFixture(t *testing.T) *cbExerciseFixture {
	t.Helper()
	db := crossbankExerciseDB(t)
	logsRepo := repository.NewInterBankSagaLogRepository(db)
	contracts := repository.NewOptionContractRepository(db)

	accountFake := newFakeFundAccountClient()
	accounts := &fakeOTCAccountClient{fakeFundAccountClient: accountFake}
	accounts.addAccount(7001, "BUYER-RSD", "1000000")
	accounts.accounts[7001].CurrencyCode = "RSD"

	transfers := newFakeInterBankTransferer()
	peer := newFakeCrossbankPeer()
	publisher := &countingPublisher{}

	// Seed an ACTIVE cross-bank contract to exercise.
	buyerCode := "111"
	sellerCode := "222"
	contract := &model.OptionContract{
		OfferID:         88,
		BuyerUserID:     55,
		BuyerSystemType: "client",
		BuyerBankCode:   &buyerCode,
		SellerUserID:    99,
		SellerSystemType: "client",
		SellerBankCode:  &sellerCode,
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromInt(5000),
		PremiumPaid:     decimal.NewFromInt(50000),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().AddDate(0, 0, 7).UTC(),
		Status:          model.OptionContractStatusActive,
		SagaID:          "seed-saga",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := contracts.Create(contract); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	saga := NewCrossbankExerciseSaga(
		logsRepo,
		nil, // nil producer — countingPublisher replaces the Kafka adapter
		nilPeerRouter{},
		accounts, contracts, transfers,
		"111",
	).withPublisherFactory(func(_ *model.OptionContract, _ CrossbankExerciseInput, _ string) sharedsaga.LifecyclePublisher {
		return publisher
	}).withPeerOps(peer)

	in := CrossbankExerciseInput{
		ContractID:             contract.ID,
		BuyerAccountID:         7001,
		BuyerAccountNumber:     "BUYER-RSD",
		BuyerClientIDExternal:  "buyer-ext",
		SellerAccountNumber:    "SELLER-RSD",
		SellerClientIDExternal: "seller-ext",
		AssetListingID:         42,
	}

	return &cbExerciseFixture{
		saga: saga, logsRepo: logsRepo, contracts: contracts,
		accounts: accounts, transfers: transfers, peer: peer, publisher: publisher,
		in: in, contract: contract,
	}
}

// firstExerciseSagaTxID returns the tx_id of any row in the inter-bank
// ledger written by the exercise saga. Each test runs exactly one saga, so
// this unambiguously identifies the saga under test.
func firstExerciseSagaTxID(t *testing.T, fx *cbExerciseFixture) string {
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
				if rows[i].SagaKind == model.SagaKindExercise {
					return rows[i].TxID
				}
			}
		}
	}
	t.Fatal("no exercise saga rows in ledger; cannot extract tx_id")
	return ""
}

// ---------------- happy path ----------------

func TestCrossbankExercise_HappyPath_All6StepsCompletedAndPublished(t *testing.T) {
	fx := newCrossbankExerciseFixture(t)
	contract, err := fx.saga.Run(context.Background(), fx.in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if contract == nil {
		t.Fatal("expected non-nil contract")
	}
	if contract.Status != model.OptionContractStatusExercised {
		t.Errorf("contract status: got %s, want %s", contract.Status, model.OptionContractStatusExercised)
	}
	if contract.ExercisedAt == nil {
		t.Error("expected ExercisedAt to be set")
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

	// Side-effect assertions: each external surface called as expected.
	if fx.accounts.reserveCalls != 1 {
		t.Errorf("ReserveFunds calls: got %d, want 1", fx.accounts.reserveCalls)
	}
	if fx.peer.reserveCalls != 1 {
		t.Errorf("peer.ReserveShares calls: got %d, want 1", fx.peer.reserveCalls)
	}
	if fx.transfers.initiateCalls != 1 {
		t.Errorf("Initiate calls: got %d, want 1", fx.transfers.initiateCalls)
	}
	if fx.peer.transferOwnerCalls != 1 {
		t.Errorf("peer.TransferOwnership calls: got %d, want 1", fx.peer.transferOwnerCalls)
	}
	if fx.peer.finalizeCalls != 1 {
		t.Errorf("peer.Finalize calls: got %d, want 1", fx.peer.finalizeCalls)
	}

	// Ledger assertions: 6 forward rows, all completed, every expected
	// StepKind present.
	txID := firstExerciseSagaTxID(t, fx)
	rows, err := fx.logsRepo.ListByTxID(txID)
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("ledger rows: got %d, want 6", len(rows))
	}
	wantPhases := map[string]bool{
		string(sharedsaga.StepReserveBuyerFunds):   false,
		string(sharedsaga.StepReserveSellerShares): false,
		string(sharedsaga.StepDebitStrike):         false,
		string(sharedsaga.StepCreditStrike):        false,
		string(sharedsaga.StepTransferOwnership):   false,
		string(sharedsaga.StepFinalizeAccept):      false,
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
		if row.SagaKind != model.SagaKindExercise {
			t.Errorf("row %s: saga_kind %s, want %s", row.Phase, row.SagaKind, model.SagaKindExercise)
		}
	}
	for p, seen := range wantPhases {
		if !seen {
			t.Errorf("missing phase %q in ledger", p)
		}
	}
}

// ---------------- compensation table ----------------

// TestCrossbankExercise_Compensation drives a failure into each non-pivot
// forward step in turn and asserts:
//   - the failing step's row ends in `failed` status,
//   - prior pre-pivot steps that have a Backward end with a
//     `compensate_<step>` row in `compensated` status,
//   - OnRolledBack publisher fired with the failing step name and the
//     compensated list,
//   - failures past the pivot do NOT trigger backward rollback of
//     pre-pivot steps — the pivot semantic of shared.Saga.
func TestCrossbankExercise_Compensation(t *testing.T) {
	type tc struct {
		name              string
		fail              func(t *testing.T, fx *cbExerciseFixture)
		failingStep       sharedsaga.StepKind
		expectCompensated []sharedsaga.StepKind
	}
	cases := []tc{
		{
			name: "reserve_buyer_funds fails -> no compensation needed",
			fail: func(_ *testing.T, fx *cbExerciseFixture) {
				fx.accounts.failReserveOnce = errors.New("buyer broke")
			},
			failingStep:       sharedsaga.StepReserveBuyerFunds,
			expectCompensated: nil,
		},
		{
			name: "reserve_seller_shares fails -> reserve_buyer_funds compensated",
			fail: func(_ *testing.T, fx *cbExerciseFixture) {
				fx.peer.reserveConfirmed = false
				fx.peer.reserveFailReason = "seller has no shares"
			},
			failingStep: sharedsaga.StepReserveSellerShares,
			expectCompensated: []sharedsaga.StepKind{
				sharedsaga.StepReserveBuyerFunds,
			},
		},
		{
			name: "debit_strike fails -> reserve_seller_shares + reserve_buyer_funds compensated",
			fail: func(_ *testing.T, fx *cbExerciseFixture) {
				fx.transfers.initiateErr = errors.New("transfer down")
			},
			failingStep: sharedsaga.StepDebitStrike,
			expectCompensated: []sharedsaga.StepKind{
				sharedsaga.StepReserveSellerShares,
				sharedsaga.StepReserveBuyerFunds,
			},
		},
		{
			name: "transfer_ownership fails (past pivot) -> NO pre-pivot compensation",
			fail: func(_ *testing.T, fx *cbExerciseFixture) {
				fx.peer.transferOwnerConfirm = false
				fx.peer.transferOwnerFail = "peer ownership rejected"
			},
			failingStep:       sharedsaga.StepTransferOwnership,
			expectCompensated: nil,
		},
	}

	for _, c := range cases {
		t.Run(string(c.failingStep)+":"+c.name, func(t *testing.T) {
			fx := newCrossbankExerciseFixture(t)
			c.fail(t, fx)

			_, runErr := fx.saga.Run(context.Background(), fx.in)
			if runErr == nil {
				t.Fatal("expected error, got nil")
			}

			if fx.publisher.rolledBackCalls != 1 {
				t.Errorf("OnRolledBack: got %d, want 1", fx.publisher.rolledBackCalls)
			}
			if fx.publisher.committedCalls != 0 {
				t.Errorf("OnCommitted: got %d, want 0", fx.publisher.committedCalls)
			}
			if fx.publisher.lastFailingStep != string(c.failingStep) {
				t.Errorf("rolled-back failing step: got %q, want %q",
					fx.publisher.lastFailingStep, string(c.failingStep))
			}

			txID := firstExerciseSagaTxID(t, fx)
			rows, err := fx.logsRepo.ListByTxID(txID)
			if err != nil {
				t.Fatalf("ListByTxID: %v", err)
			}
			byPhase := map[string]model.InterBankSagaLog{}
			for _, r := range rows {
				byPhase[r.Phase] = r
			}

			// Failing step must end in failed.
			failingRow, ok := byPhase[string(c.failingStep)]
			if !ok {
				t.Fatalf("failing step %s missing from ledger; phases=%v", c.failingStep, exKeysOf(byPhase))
			}
			if failingRow.Status != model.IBSagaStatusFailed {
				t.Errorf("failing step status: got %s, want %s",
					failingRow.Status, model.IBSagaStatusFailed)
			}

			// Compensation rows: each expected step must have a
			// compensate_<step> row in compensated status.
			expectedComp := map[sharedsaga.StepKind]bool{}
			for _, s := range c.expectCompensated {
				expectedComp[s] = true
				phaseName := "compensate_" + string(s)
				row, ok := byPhase[phaseName]
				if !ok {
					t.Errorf("missing compensation row for %s; phases=%v", s, exKeysOf(byPhase))
					continue
				}
				if row.Status != model.IBSagaStatusCompensated {
					t.Errorf("compensation status for %s: got %s, want %s",
						s, row.Status, model.IBSagaStatusCompensated)
				}
			}

			// Inverse: no compensation rows for steps NOT expected (the
			// pivot semantic must suppress past-pivot rollback).
			for phase := range byPhase {
				if !strings.HasPrefix(phase, "compensate_") {
					continue
				}
				stepName := strings.TrimPrefix(phase, "compensate_")
				if !expectedComp[sharedsaga.StepKind(stepName)] {
					t.Errorf("unexpected compensation row for %s "+
						"(pivot semantic should have suppressed it)", stepName)
				}
			}

			// The compensated-list passed to OnRolledBack must contain
			// every expected step (set comparison).
			gotComp := map[string]bool{}
			for _, s := range fx.publisher.lastCompensated {
				gotComp[s] = true
			}
			for _, s := range c.expectCompensated {
				if !gotComp[string(s)] {
					t.Errorf("OnRolledBack compensated list missing %s; got %v",
						s, fx.publisher.lastCompensated)
				}
			}
		})
	}
}

func exKeysOf(m map[string]model.InterBankSagaLog) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
