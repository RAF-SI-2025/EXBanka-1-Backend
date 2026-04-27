package service

import (
	"context"
	"errors"
	"strings"
	"sync"
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

// fakeCrossbankPeer implements crossbankPeerOps so the saga's step
// closures can hit it directly via the withPeerOps test seam, without
// running an httptest server. Records every call for assertions.
//
// Also implements crossbankExpireOps (ContractExpire) so the expire
// saga's withPeerOps seam can use this same fake.
type fakeCrossbankPeer struct {
	mu sync.Mutex

	reserveCalls         int
	reserveConfirmed     bool
	reserveFailReason    string
	reserveErr           error
	transferOwnerCalls   int
	transferOwnerConfirm bool
	transferOwnerFail    string
	transferOwnerErr     error
	finalizeCalls        int
	rollbackSharesCalls  int

	// ContractExpire knobs (used by crossbank expire saga tests).
	contractExpireCalls  int
	contractExpireOK     bool
	contractExpireReason string
	contractExpireErr    error
}

func newFakeCrossbankPeer() *fakeCrossbankPeer {
	return &fakeCrossbankPeer{
		reserveConfirmed:     true,
		transferOwnerConfirm: true,
		contractExpireOK:     true,
	}
}

// ContractExpire satisfies crossbankExpireOps so the same fake can be
// reused by the crossbank expire saga tests.
func (p *fakeCrossbankPeer) ContractExpire(_ context.Context, _ PeerContractExpireRequest) (*PeerContractExpireResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.contractExpireCalls++
	if p.contractExpireErr != nil {
		return nil, p.contractExpireErr
	}
	return &PeerContractExpireResponse{OK: p.contractExpireOK, Reason: p.contractExpireReason}, nil
}

func (p *fakeCrossbankPeer) ReserveShares(_ context.Context, _ PeerReserveSharesRequest) (*PeerReserveSharesResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reserveCalls++
	if p.reserveErr != nil {
		return nil, p.reserveErr
	}
	return &PeerReserveSharesResponse{Confirmed: p.reserveConfirmed, FailReason: p.reserveFailReason}, nil
}

func (p *fakeCrossbankPeer) TransferOwnership(_ context.Context, _ PeerTransferOwnershipRequest) (*PeerTransferOwnershipResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transferOwnerCalls++
	if p.transferOwnerErr != nil {
		return nil, p.transferOwnerErr
	}
	return &PeerTransferOwnershipResponse{Confirmed: p.transferOwnerConfirm, FailReason: p.transferOwnerFail}, nil
}

func (p *fakeCrossbankPeer) Finalize(_ context.Context, _ PeerFinalizeRequest) (*PeerFinalizeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finalizeCalls++
	return &PeerFinalizeResponse{OK: true}, nil
}

func (p *fakeCrossbankPeer) RollbackShares(_ context.Context, _ PeerRollbackSharesRequest) (*PeerRollbackSharesResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rollbackSharesCalls++
	return &PeerRollbackSharesResponse{OK: true}, nil
}

// fakeInterBankTransferer satisfies InterBankTransferer.
type fakeInterBankTransferer struct {
	mu             sync.Mutex
	initiateCalls  int
	initiateErr    error
	initiateCommit bool
	initiateTxID   string
	initiateReason string
	reverseCalls   int
}

func newFakeInterBankTransferer() *fakeInterBankTransferer {
	return &fakeInterBankTransferer{initiateCommit: true, initiateTxID: "tx-spec3-1"}
}

func (f *fakeInterBankTransferer) Initiate(_ context.Context, _, _, _, _, _ string) (string, bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initiateCalls++
	if f.initiateErr != nil {
		return "", false, "", f.initiateErr
	}
	return f.initiateTxID, f.initiateCommit, f.initiateReason, nil
}

func (f *fakeInterBankTransferer) Reverse(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverseCalls++
	return nil
}

// nilPeerRouter is the default CrossbankPeerRouter used when the saga
// would otherwise need a real router. It is never invoked because every
// test wires the peer-call surface through withPeerOps.
type nilPeerRouter struct{}

func (nilPeerRouter) ClientFor(_ string) (*CrossbankPeerClient, error) {
	return nil, errors.New("nilPeerRouter.ClientFor must not be reached when withPeerOps is set")
}

// countingPublisher records every LifecyclePublisher call so the test can
// assert order and counts. Drop-in replacement for the production Kafka
// adapter.
type countingPublisher struct {
	mu sync.Mutex

	startedCalls    int
	committedCalls  int
	rolledBackCalls int
	stuckCalls      int

	lastFailingStep string
	lastReason      string
	lastCompensated []string
}

func (p *countingPublisher) OnStarted(_ context.Context, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.startedCalls++
}

func (p *countingPublisher) OnCommitted(_ context.Context, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.committedCalls++
}

func (p *countingPublisher) OnRolledBack(_ context.Context, _ string, failingStep, reason string, compensated []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rolledBackCalls++
	p.lastFailingStep = failingStep
	p.lastReason = reason
	p.lastCompensated = append([]string(nil), compensated...)
}

func (p *countingPublisher) OnStuck(_ context.Context, _ string, _, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stuckCalls++
}

// ---------------- fixture ----------------

type cbAcceptFixture struct {
	saga      *CrossbankAcceptSaga
	logsRepo  *repository.InterBankSagaLogRepository
	contracts *repository.OptionContractRepository
	offers    *repository.OTCOfferRepository
	accounts  *fakeOTCAccountClient
	transfers *fakeInterBankTransferer
	peer      *fakeCrossbankPeer
	publisher *countingPublisher
	in        CrossbankAcceptInput
	offer     *model.OTCOffer
}

// crossbankAcceptDB opens an in-memory SQLite DB with all the tables the
// crossbank accept saga reads from or writes to.
func crossbankAcceptDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.OTCOffer{},
		&model.OptionContract{},
		&model.InterBankSagaLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newCrossbankAcceptFixture wires the saga + all its deps with sensible
// defaults: peer succeeds on every call, transfer commits, accounts mock
// reserves+releases. Tests poke the fakes' fail knobs to drive scenarios.
func newCrossbankAcceptFixture(t *testing.T) *cbAcceptFixture {
	t.Helper()
	db := crossbankAcceptDB(t)
	logsRepo := repository.NewInterBankSagaLogRepository(db)
	contracts := repository.NewOptionContractRepository(db)
	offers := repository.NewOTCOfferRepository(db)

	accountFake := newFakeFundAccountClient()
	accounts := &fakeOTCAccountClient{fakeFundAccountClient: accountFake}
	accounts.addAccount(7001, "BUYER-RSD", "1000000")
	accounts.accounts[7001].CurrencyCode = "RSD"

	transfers := newFakeInterBankTransferer()
	peer := newFakeCrossbankPeer()
	publisher := &countingPublisher{}

	initUID := uint64(99)
	offer := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &initUID,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 99,
		Direction: model.OTCDirectionSellInitiated,
		StockID:   1,
		Quantity:  decimal.NewFromInt(10), StrikePrice: decimal.NewFromInt(5000),
		Premium:        decimal.NewFromInt(50000),
		SettlementDate: time.Now().AddDate(0, 0, 7),
		Status:         model.OTCOfferStatusPending,
	}
	if err := offers.Create(offer); err != nil {
		t.Fatalf("seed offer: %v", err)
	}

	saga := NewCrossbankAcceptSaga(
		logsRepo,
		nil, // nil producer — countingPublisher replaces the Kafka adapter
		nilPeerRouter{},
		accounts, contracts, offers, transfers,
		"111",
	).withPublisherFactory(func(_ CrossbankAcceptInput, _ string, _ **model.OptionContract) sharedsaga.LifecyclePublisher {
		return publisher
	}).withPeerOps(peer)

	in := CrossbankAcceptInput{
		OfferID:                offer.ID,
		BuyerUserID:            55,
		BuyerSystemType:        "client",
		BuyerBankCode:          "111",
		BuyerClientIDExternal:  "buyer-ext",
		BuyerAccountID:         7001,
		BuyerAccountNumber:     "BUYER-RSD",
		SellerUserID:           99,
		SellerSystemType:       "client",
		SellerBankCode:         "222",
		SellerClientIDExternal: "seller-ext",
		SellerAccountNumber:    "SELLER-RSD",
		Premium:                decimal.NewFromInt(50000),
		Currency:               "RSD",
		Quantity:               decimal.NewFromInt(10),
		StrikePrice:            decimal.NewFromInt(5000),
		SettlementDate:         time.Now().AddDate(0, 0, 7),
		AssetListingID:         42,
	}

	return &cbAcceptFixture{
		saga: saga, logsRepo: logsRepo, contracts: contracts, offers: offers,
		accounts: accounts, transfers: transfers, peer: peer, publisher: publisher,
		in: in, offer: offer,
	}
}

// firstSagaTxID returns the tx_id of any row in the inter-bank ledger.
// Each test runs exactly one saga, so this unambiguously identifies the
// saga under test (regardless of whether it succeeded or failed).
func firstSagaTxID(t *testing.T, fx *cbAcceptFixture) string {
	t.Helper()
	farFuture := time.Now().Add(time.Hour)
	for _, status := range []string{
		model.IBSagaStatusFailed,
		model.IBSagaStatusCompleted,
		model.IBSagaStatusPending,
		model.IBSagaStatusCompensated,
		model.IBSagaStatusCompensating,
	} {
		rows, err := fx.logsRepo.ListStaleByStatus(status, farFuture, 1)
		if err == nil && len(rows) > 0 {
			return rows[0].TxID
		}
	}
	t.Fatal("no saga rows in ledger; cannot extract tx_id")
	return ""
}

// ---------------- happy path ----------------

func TestCrossbankAccept_HappyPath_All7StepsCompletedAndPublished(t *testing.T) {
	fx := newCrossbankAcceptFixture(t)
	contract, err := fx.saga.Run(context.Background(), fx.in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if contract == nil || contract.ID == 0 {
		t.Fatal("expected non-nil contract with non-zero ID")
	}

	// Lifecycle assertions: started before steps, committed after the last
	// step, no rollback.
	if fx.publisher.startedCalls != 1 {
		t.Errorf("OnStarted: got %d, want 1", fx.publisher.startedCalls)
	}
	if fx.publisher.committedCalls != 1 {
		t.Errorf("OnCommitted: got %d, want 1", fx.publisher.committedCalls)
	}
	if fx.publisher.rolledBackCalls != 0 {
		t.Errorf("OnRolledBack: got %d, want 0", fx.publisher.rolledBackCalls)
	}

	// Side-effect assertions: each external surface called exactly once.
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

	// Ledger assertions: 7 forward rows, all completed, every expected
	// StepKind present.
	rows, err := fx.logsRepo.ListByTxID(contract.SagaID)
	if err != nil {
		t.Fatalf("ListByTxID: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("ledger rows: got %d, want 7", len(rows))
	}
	wantPhases := map[string]bool{
		string(sharedsaga.StepReserveBuyerFunds):   false,
		string(sharedsaga.StepCreateContract):      false,
		string(sharedsaga.StepReserveSellerShares): false,
		string(sharedsaga.StepDebitBuyer):          false,
		string(sharedsaga.StepCreditSeller):        false,
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
	}
	for p, seen := range wantPhases {
		if !seen {
			t.Errorf("missing phase %q in ledger", p)
		}
	}

	// Local offer marked accepted by the finalize step.
	got, _ := fx.offers.GetByID(fx.offer.ID)
	if got.Status != model.OTCOfferStatusAccepted {
		t.Errorf("offer status: got %s, want %s", got.Status, model.OTCOfferStatusAccepted)
	}
}

// ---------------- compensation table ----------------

// TestCrossbankAccept_Compensation drives a failure into each non-pivot
// forward step in turn and asserts:
//   - the failing step's row ends in `failed` status,
//   - prior pre-pivot steps that have a Backward end with a
//     `compensate_<step>` row in `compensated` status,
//   - OnRolledBack publisher fired with the failing step name and the
//     compensated list,
//   - failures past the pivot do NOT trigger backward rollback of
//     pre-pivot steps — the pivot semantic of shared.Saga.
func TestCrossbankAccept_Compensation(t *testing.T) {
	type tc struct {
		name              string
		fail              func(t *testing.T, fx *cbAcceptFixture)
		failingStep       sharedsaga.StepKind
		expectCompensated []sharedsaga.StepKind
	}
	cases := []tc{
		{
			name: "reserve_buyer_funds fails -> no compensation needed",
			fail: func(_ *testing.T, fx *cbAcceptFixture) {
				fx.accounts.failReserveOnce = errors.New("buyer broke")
			},
			failingStep:       sharedsaga.StepReserveBuyerFunds,
			expectCompensated: nil,
		},
		{
			name: "create_contract fails -> reserve_buyer_funds compensated",
			fail: func(t *testing.T, fx *cbAcceptFixture) {
				// Force contracts.Create to error by dropping the table
				// after the saga's BeforeUpdate hook installs constraints.
				if err := fx.contracts.DB().Migrator().DropTable(&model.OptionContract{}); err != nil {
					t.Fatalf("drop contracts table: %v", err)
				}
			},
			failingStep:       sharedsaga.StepCreateContract,
			expectCompensated: []sharedsaga.StepKind{sharedsaga.StepReserveBuyerFunds},
		},
		{
			name: "reserve_seller_shares fails -> create_contract + reserve_buyer_funds compensated",
			fail: func(_ *testing.T, fx *cbAcceptFixture) {
				fx.peer.reserveConfirmed = false
				fx.peer.reserveFailReason = "seller has no shares"
			},
			failingStep: sharedsaga.StepReserveSellerShares,
			expectCompensated: []sharedsaga.StepKind{
				sharedsaga.StepCreateContract,
				sharedsaga.StepReserveBuyerFunds,
			},
		},
		{
			name: "debit_buyer fails (past pivot) -> NO pre-pivot compensation",
			fail: func(_ *testing.T, fx *cbAcceptFixture) {
				fx.transfers.initiateErr = errors.New("transfer down")
			},
			failingStep:       sharedsaga.StepDebitBuyer,
			expectCompensated: nil,
		},
		{
			name: "transfer_ownership fails (past pivot) -> NO pre-pivot compensation",
			fail: func(_ *testing.T, fx *cbAcceptFixture) {
				fx.peer.transferOwnerConfirm = false
				fx.peer.transferOwnerFail = "peer ownership rejected"
			},
			failingStep:       sharedsaga.StepTransferOwnership,
			expectCompensated: nil,
		},
	}

	for _, c := range cases {
		t.Run(string(c.failingStep)+":"+c.name, func(t *testing.T) {
			fx := newCrossbankAcceptFixture(t)
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

			txID := firstSagaTxID(t, fx)
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
				t.Fatalf("failing step %s missing from ledger; phases=%v", c.failingStep, keysOf(byPhase))
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
					t.Errorf("missing compensation row for %s; phases=%v", s, keysOf(byPhase))
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

func keysOf(m map[string]model.InterBankSagaLog) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
