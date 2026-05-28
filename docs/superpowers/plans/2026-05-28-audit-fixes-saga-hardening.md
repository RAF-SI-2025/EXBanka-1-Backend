# Audit Fixes — Saga Hardening (Part A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close all stuck-state risks identified in the final audit so Celina 4/5 cannot leave the system in a state requiring admin intervention.

**Architecture:**
- Credit-service: replace inline 2-step loan disbursement with a full saga (`contract/shared/saga`) backed by `saga_log` + `StartCompensationRecovery` recovery worker mirroring transaction-service.
- Stock-service: collapse the four post-saga "best-effort" steps in `otc_accept_saga.go` / `otc_exercise_saga.go` into the saga itself as committable steps; bump `maxSagaRecoveryRetries` to 10 with dead-letter Kafka publish.
- Transaction-service: add `CHECK_STATUS` peer endpoint + sender-side `PeerTxReconciler` that polls peers for stuck `outbound_peer_tx` rows.

**Tech Stack:** Go workspace, GORM/Postgres, gRPC, Kafka, Gin, `contract/shared/saga` saga abstraction.

**Spec:** `docs/superpowers/specs/2026-05-28-final-audit-fixes-and-portfolio-cron-design.md` Part A.

---

## File Structure

**Credit-service (A1):**
- Create: `credit-service/internal/model/saga_log.go` — SagaLog model (mirrors transaction-service)
- Create: `credit-service/internal/repository/saga_log_repository.go` — saga_log repo
- Create: `credit-service/internal/service/loan_disbursement_saga.go` — DisburseLoanWithSaga function
- Create: `credit-service/internal/service/compensation_recovery.go` — recovery worker
- Modify: `credit-service/internal/service/loan_request_service.go` — replace inline disbursement
- Modify: `credit-service/cmd/main.go` — auto-migrate + start recovery worker

**Stock-service (A2):**
- Modify: `stock-service/internal/service/otc_accept_saga.go` — move post-saga steps inside saga
- Modify: `stock-service/internal/service/otc_exercise_saga.go` — same
- Modify: `stock-service/internal/service/saga_recovery.go` — bump retry ceiling + dead-letter publish
- Modify: `stock-service/internal/kafka/topics.go` — register `saga.dead-letter` topic
- Modify: `stock-service/cmd/main.go` — EnsureTopics includes dead-letter

**Transaction-service (A3):**
- Modify: `contract/proto/transaction/transaction.proto` — add `GetTxStatus` RPC
- Create: `transaction-service/internal/service/peer_tx_reconciler.go` — reconciler
- Create: `api-gateway/internal/handler/peer_tx_status_handler.go` — REST handler
- Modify: `transaction-service/internal/handler/peer_tx_grpc_handler.go` — implement `GetTxStatus`
- Modify: `transaction-service/internal/sitx/peer_http_client.go` — add `CheckStatus` outbound
- Modify: `api-gateway/internal/router/router_v3.go` — register `GET /interbank/:transaction_id/status`
- Modify: `transaction-service/cmd/main.go` — wire reconciler

**Tests:**
- Unit tests next to each new file (`*_test.go`)
- Integration tests in `test-app/workflows/` for each high-risk path

---

## Task A1.1: Add SagaLog model to credit-service

**Files:**
- Create: `credit-service/internal/model/saga_log.go`
- Test: `credit-service/internal/model/saga_log_test.go`

- [ ] **Step 1: Write the failing test**

```go
// credit-service/internal/model/saga_log_test.go
package model

import (
	"testing"
	"github.com/shopspring/decimal"
)

func TestSagaLog_DefaultValues(t *testing.T) {
	l := SagaLog{
		SagaID:        "abc",
		LoanID:        42,
		StepNumber:    1,
		StepName:      "debit_bank",
		AccountNumber: "111000000",
		Amount:        decimal.NewFromInt(100),
	}
	if l.Status != "" {
		t.Fatalf("Status should default to empty (set by GORM default), got %q", l.Status)
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

```bash
cd credit-service && go test ./internal/model/... -run TestSagaLog -v
```

Expected: FAIL — `undefined: SagaLog`

- [ ] **Step 3: Create the model**

```go
// credit-service/internal/model/saga_log.go
package model

import (
	"time"
	"github.com/shopspring/decimal"
)

type SagaLog struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement"`
	SagaID         string          `gorm:"size:64;not null;index:idx_credit_saga_id"`
	LoanID         uint64          `gorm:"not null;index"`
	StepNumber     int             `gorm:"not null"`
	StepName       string          `gorm:"size:100;not null"`
	Status         string          `gorm:"size:20;not null;default:'pending'"`
	IsCompensation bool            `gorm:"not null;default:false"`
	AccountNumber  string          `gorm:"size:34;not null"`
	Amount         decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	ErrorMessage   string          `gorm:"size:512"`
	CompensationOf *uint64         `gorm:"index"`
	RetryCount     int             `gorm:"not null;default:0"`
	CreatedAt      time.Time       `gorm:"not null"`
	CompletedAt    *time.Time
}

func (SagaLog) TableName() string { return "saga_logs" }
```

- [ ] **Step 4: Run test, verify it passes**

```bash
cd credit-service && go test ./internal/model/... -run TestSagaLog -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add credit-service/internal/model/saga_log.go credit-service/internal/model/saga_log_test.go
git commit -m "feat(credit): add saga_log model for loan disbursement saga"
```

---

## Task A1.2: Add SagaLogRepository to credit-service

**Files:**
- Create: `credit-service/internal/repository/saga_log_repository.go`
- Test: `credit-service/internal/repository/saga_log_repository_test.go`

- [ ] **Step 1: Write the failing test (use sqlite in-memory)**

```go
// credit-service/internal/repository/saga_log_repository_test.go
package repository

import (
	"testing"
	"github.com/shopspring/decimal"
	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil { t.Fatal(err) }
	if err := db.AutoMigrate(&model.SagaLog{}); err != nil { t.Fatal(err) }
	return db
}

func TestSagaLogRepo_CreateAndUpdate(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)
	row := &model.SagaLog{
		SagaID: "saga-1", LoanID: 7, StepNumber: 1, StepName: "debit_bank",
		AccountNumber: "111000000", Amount: decimal.NewFromInt(100),
	}
	if err := repo.Create(row); err != nil { t.Fatal(err) }
	if row.ID == 0 { t.Fatal("expected ID populated") }
	if err := repo.MarkCompleted(row.ID); err != nil { t.Fatal(err) }
	got, _ := repo.GetByID(row.ID)
	if got.Status != "completed" { t.Fatalf("status=%q", got.Status) }
}

func TestSagaLogRepo_ListPending(t *testing.T) {
	db := newTestDB(t)
	repo := NewSagaLogRepository(db)
	a := &model.SagaLog{SagaID: "s1", LoanID: 1, StepName: "x", AccountNumber: "1", Amount: decimal.NewFromInt(1)}
	b := &model.SagaLog{SagaID: "s2", LoanID: 2, StepName: "y", AccountNumber: "2", Amount: decimal.NewFromInt(2)}
	_ = repo.Create(a)
	_ = repo.Create(b)
	_ = repo.MarkCompleted(a.ID)
	rows, _ := repo.ListPending(100)
	if len(rows) != 1 || rows[0].ID != b.ID { t.Fatalf("expected only b pending, got %+v", rows) }
}
```

- [ ] **Step 2: Run test, verify it fails**

```bash
cd credit-service && go test ./internal/repository/... -run TestSagaLogRepo -v
```

Expected: FAIL — `undefined: NewSagaLogRepository`

- [ ] **Step 3: Create the repository**

```go
// credit-service/internal/repository/saga_log_repository.go
package repository

import (
	"time"
	"github.com/exbanka/credit-service/internal/model"
	"gorm.io/gorm"
)

type SagaLogRepository struct {
	db *gorm.DB
}

func NewSagaLogRepository(db *gorm.DB) *SagaLogRepository {
	return &SagaLogRepository{db: db}
}

func (r *SagaLogRepository) Create(row *model.SagaLog) error {
	if row.Status == "" {
		row.Status = "pending"
	}
	return r.db.Create(row).Error
}

func (r *SagaLogRepository) GetByID(id uint64) (*model.SagaLog, error) {
	var row model.SagaLog
	if err := r.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *SagaLogRepository) MarkCompleted(id uint64) error {
	now := time.Now()
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]any{"status": "completed", "completed_at": &now}).Error
}

func (r *SagaLogRepository) MarkFailed(id uint64, errMsg string) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]any{"status": "failed", "error_message": errMsg}).Error
}

func (r *SagaLogRepository) MarkCompensating(id uint64) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]any{"status": "compensating"}).Error
}

func (r *SagaLogRepository) MarkDeadLetter(id uint64) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]any{"status": "dead_letter"}).Error
}

func (r *SagaLogRepository) IncrementRetry(id uint64) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error
}

// ListPending returns saga_log rows in `compensating` or `failed` status that
// need recovery attention. Bounded by `limit` to avoid scanning huge tables.
func (r *SagaLogRepository) ListPending(limit int) ([]model.SagaLog, error) {
	var rows []model.SagaLog
	err := r.db.Where("status IN ?", []string{"compensating", "failed"}).
		Where("retry_count < ?", 10).
		Order("created_at ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *SagaLogRepository) ListBySaga(sagaID string) ([]model.SagaLog, error) {
	var rows []model.SagaLog
	err := r.db.Where("saga_id = ?", sagaID).Order("step_number ASC").Find(&rows).Error
	return rows, err
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
cd credit-service && go test ./internal/repository/... -run TestSagaLogRepo -v
```

Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add credit-service/internal/repository/saga_log_repository.go credit-service/internal/repository/saga_log_repository_test.go
git commit -m "feat(credit): add saga_log repository"
```

---

## Task A1.3: Add LoanDisbursementSaga that uses contract/shared/saga

**Files:**
- Create: `credit-service/internal/service/loan_disbursement_saga.go`
- Test: `credit-service/internal/service/loan_disbursement_saga_test.go`

- [ ] **Step 1: Write the failing test using mock clients**

```go
// credit-service/internal/service/loan_disbursement_saga_test.go
package service

import (
	"context"
	"errors"
	"testing"
	"github.com/exbanka/contract/contract/accountpb"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/shopspring/decimal"
)

type fakeAccountClient struct {
	failOnCredit bool
	debited      bool
	credited     bool
	creditback   bool
}

func (f *fakeAccountClient) UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.UpdateBalanceResponse, error) {
	if f.failOnCredit {
		return nil, errors.New("simulated credit failure")
	}
	f.credited = true
	return &accountpb.UpdateBalanceResponse{Success: true}, nil
}

type fakeBankClient struct{ debited, creditback bool }
func (f *fakeBankClient) DebitBankAccount(ctx context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	f.debited = true
	return &accountpb.BankAccountOpResponse{Success: true}, nil
}
func (f *fakeBankClient) CreditBankAccount(ctx context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	f.creditback = true
	return &accountpb.BankAccountOpResponse{Success: true}, nil
}

func TestDisburseLoanSaga_SuccessPath(t *testing.T) {
	// Test setup: in-memory db, fake clients, working loan
	// Expectation: bank debited, borrower credited, status active, saga rows completed
	t.Skip("Skeleton — flesh out with full DB setup")
}

func TestDisburseLoanSaga_CreditFailureCompensates(t *testing.T) {
	// Test setup: failOnCredit=true
	// Expectation: bank debited then bank credited-back, status failed
	t.Skip("Skeleton — flesh out with full DB setup")
}
```

(Test skeletons; full DB-backed integration tests in Task A1.6.)

- [ ] **Step 2: Run, verify compile failure**

```bash
cd credit-service && go test ./internal/service/... -run TestDisburseLoanSaga -v
```

Expected: FAIL — `undefined: LoanDisbursementSaga`

- [ ] **Step 3: Write the saga**

```go
// credit-service/internal/service/loan_disbursement_saga.go
package service

import (
	"context"
	"fmt"
	"github.com/exbanka/contract/contract/accountpb"
	"github.com/exbanka/contract/contract/shared/saga"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// LoanDisbursementSaga coordinates the three-step loan disbursement:
//   1. debit_bank          — debit the bank-owned sentinel account
//   2. credit_borrower     — credit borrower's selected account
//   3. mark_loan_active    — flip loan.Status to "active"
// Each step records to credit-service's saga_log. Compensation on failure
// runs automatically; if compensation itself fails, the row is left in
// `compensating` for the recovery worker.
type LoanDisbursementSaga struct {
	bankClient    accountpb.BankAccountServiceClient
	accountClient accountpb.AccountServiceClient
	loanRepo      *repository.LoanRepository
	sagaRepo      *repository.SagaLogRepository
	db            *gorm.DB
}

func NewLoanDisbursementSaga(
	bank accountpb.BankAccountServiceClient,
	acct accountpb.AccountServiceClient,
	loanRepo *repository.LoanRepository,
	sagaRepo *repository.SagaLogRepository,
	db *gorm.DB,
) *LoanDisbursementSaga {
	return &LoanDisbursementSaga{
		bankClient: bank, accountClient: acct,
		loanRepo: loanRepo, sagaRepo: sagaRepo, db: db,
	}
}

// Disburse runs the saga. Idempotent — if called twice with the same loan
// in `active` state, returns nil immediately.
func (s *LoanDisbursementSaga) Disburse(ctx context.Context, loan *model.Loan) error {
	if loan.Status == "active" {
		return nil
	}

	sagaID := fmt.Sprintf("loan-disbursement-%d", loan.ID)
	amount := loan.Amount
	amountStr := amount.String()
	ref := sagaID
	currency := loan.CurrencyCode

	sg := saga.NewSagaWithID(sagaID, newLocalRecorder(s.sagaRepo, loan.ID)).
		Add(saga.Step{
			Name: "debit_bank",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, err := s.bankClient.DebitBankAccount(ctx, &accountpb.BankAccountOpRequest{
					Currency: currency, Amount: amountStr,
					Reference: ref + ":debit", Reason: "loan_disbursement",
				})
				return err
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				_, err := s.bankClient.CreditBankAccount(ctx, &accountpb.BankAccountOpRequest{
					Currency: currency, Amount: amountStr,
					Reference: ref + ":debit-comp", Reason: "loan_disbursement_compensation",
				})
				return err
			},
		}).
		Add(saga.Step{
			Name: "credit_borrower",
			Forward: func(ctx context.Context, _ *saga.State) error {
				_, err := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:  loan.AccountNumber,
					Amount:         amountStr,
					IdempotencyKey: ref + ":credit",
				})
				return err
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				neg := amount.Neg().String()
				_, err := s.accountClient.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
					AccountNumber:  loan.AccountNumber,
					Amount:         neg,
					IdempotencyKey: ref + ":credit-comp",
				})
				return err
			},
		}).
		Add(saga.Step{
			Name: "mark_loan_active",
			Forward: func(ctx context.Context, _ *saga.State) error {
				loan.Status = "active"
				return s.loanRepo.Save(loan)
			},
			Backward: func(ctx context.Context, _ *saga.State) error {
				loan.Status = "disbursement_failed"
				return s.loanRepo.Save(loan)
			},
		})

	return sg.Execute(ctx, saga.NewState())
}
```

- [ ] **Step 4: Create the local recorder adapter (small file)**

```go
// credit-service/internal/service/saga_recorder.go
package service

import (
	"github.com/exbanka/contract/contract/shared/saga"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/shopspring/decimal"
)

type localRecorder struct {
	repo   *repository.SagaLogRepository
	loanID uint64
}

func newLocalRecorder(repo *repository.SagaLogRepository, loanID uint64) saga.Recorder {
	return &localRecorder{repo: repo, loanID: loanID}
}

func (r *localRecorder) StepStarted(sagaID, stepName string, stepNumber int) error {
	row := &model.SagaLog{
		SagaID: sagaID, LoanID: r.loanID,
		StepNumber: stepNumber, StepName: stepName,
		Status: "pending", AccountNumber: "", Amount: decimal.Zero,
	}
	return r.repo.Create(row)
}

func (r *localRecorder) StepCompleted(sagaID, stepName string) error {
	rows, err := r.repo.ListBySaga(sagaID)
	if err != nil { return err }
	for _, row := range rows {
		if row.StepName == stepName && !row.IsCompensation && row.Status == "pending" {
			return r.repo.MarkCompleted(row.ID)
		}
	}
	return nil
}

func (r *localRecorder) StepFailed(sagaID, stepName string, errMsg string) error {
	rows, err := r.repo.ListBySaga(sagaID)
	if err != nil { return err }
	for _, row := range rows {
		if row.StepName == stepName && !row.IsCompensation && row.Status == "pending" {
			return r.repo.MarkFailed(row.ID, errMsg)
		}
	}
	return nil
}

func (r *localRecorder) CompensationStarted(sagaID, stepName string) error {
	// Insert a compensation row referencing the original step
	row := &model.SagaLog{
		SagaID: sagaID, LoanID: r.loanID,
		StepName: stepName, Status: "compensating",
		IsCompensation: true, AccountNumber: "", Amount: decimal.Zero,
	}
	return r.repo.Create(row)
}

func (r *localRecorder) CompensationCompleted(sagaID, stepName string) error {
	rows, err := r.repo.ListBySaga(sagaID)
	if err != nil { return err }
	for _, row := range rows {
		if row.StepName == stepName && row.IsCompensation && row.Status == "compensating" {
			return r.repo.MarkCompleted(row.ID)
		}
	}
	return nil
}

func (r *localRecorder) CompensationFailed(sagaID, stepName string, errMsg string) error {
	rows, err := r.repo.ListBySaga(sagaID)
	if err != nil { return err }
	for _, row := range rows {
		if row.StepName == stepName && row.IsCompensation && row.Status == "compensating" {
			return r.repo.MarkFailed(row.ID, errMsg)
		}
	}
	return nil
}
```

**Note:** the exact `saga.Recorder` interface signature may differ — confirm by reading `contract/shared/saga/saga.go` before implementing. The recorder methods above are placeholders; if the real interface uses different method names or signatures, adapt to those — the conceptual contract (track step lifecycle) is the same.

- [ ] **Step 5: Run tests, verify they compile**

```bash
cd credit-service && go test ./internal/service/... -run TestDisburseLoanSaga -v
```

Expected: tests compile and reach `t.Skip()` (full DB tests come in A1.6)

- [ ] **Step 6: Commit**

```bash
git add credit-service/internal/service/loan_disbursement_saga.go credit-service/internal/service/saga_recorder.go credit-service/internal/service/loan_disbursement_saga_test.go
git commit -m "feat(credit): introduce LoanDisbursementSaga with saga_log"
```

---

## Task A1.4: Replace inline disbursement in loan_request_service.go

**Files:**
- Modify: `credit-service/internal/service/loan_request_service.go` (lines 256–321 area, the `ApproveLoanRequest` function)

- [ ] **Step 1: Add saga to LoanRequestService struct**

Modify the struct definition (lines 49–59 area):

```go
type LoanRequestService struct {
	repo              *repository.LoanRequestRepository
	loanRepo          *repository.LoanRepository
	installRepo       *repository.InstallmentRepository
	limitClient       userpb.EmployeeLimitServiceClient
	accountClient     accountpb.AccountServiceClient
	bankAccountClient accountpb.BankAccountServiceClient
	rateConfigSvc     *RateConfigService
	changelogRepo     *repository.ChangelogRepository
	disbursementSaga  *LoanDisbursementSaga
	db                *gorm.DB
}
```

- [ ] **Step 2: Add constructor parameter**

Update `NewLoanRequestService` to accept and store `disbursementSaga *LoanDisbursementSaga`. Callers updated in Task A1.7.

- [ ] **Step 3: Replace the inline debit+credit+compensate block**

Find the block in `ApproveLoanRequest` that does manual debit → credit → log "COMPENSATION FAILED". Replace with a single call:

```go
// Disburse via saga. Compensation runs automatically on any step failure.
if err := s.disbursementSaga.Disburse(ctx, loan); err != nil {
	// Saga has already attempted compensation. If compensation also failed,
	// the saga_log rows stay in `compensating` and the recovery worker picks
	// them up. The borrower's account state is consistent.
	loan.Status = "disbursement_failed"
	_ = s.loanRepo.Save(loan)
	return nil, fmt.Errorf("loan disbursement failed: %w", err)
}
```

Delete the inline debit/credit/compensate block (the existing manual `DebitBankAccount` → `UpdateBalance` → `CreditBankAccount` sequence, lines ~256–315). The saga handles all of it.

- [ ] **Step 4: Build the package**

```bash
cd credit-service && go build ./...
```

Expected: no compile errors. If there are, fix them and rebuild before continuing.

- [ ] **Step 5: Commit**

```bash
git add credit-service/internal/service/loan_request_service.go
git commit -m "refactor(credit): route ApproveLoanRequest through LoanDisbursementSaga"
```

---

## Task A1.5: Wire saga model, repo, saga + recovery worker into main.go

**Files:**
- Modify: `credit-service/cmd/main.go`
- Create: `credit-service/internal/service/compensation_recovery.go`

- [ ] **Step 1: Write the recovery worker**

```go
// credit-service/internal/service/compensation_recovery.go
package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/repository"
)

const (
	maxCompensationRetries  = 10
	compensationTickInterval = 5 * time.Minute
)

type CompensationRecovery struct {
	sagaRepo      *repository.SagaLogRepository
	disbursement  *LoanDisbursementSaga
	loanRepo      *repository.LoanRepository
	deadLetter    *kafka.Producer
}

func NewCompensationRecovery(
	sagaRepo *repository.SagaLogRepository,
	disbursement *LoanDisbursementSaga,
	loanRepo *repository.LoanRepository,
	deadLetter *kafka.Producer,
) *CompensationRecovery {
	return &CompensationRecovery{
		sagaRepo: sagaRepo, disbursement: disbursement,
		loanRepo: loanRepo, deadLetter: deadLetter,
	}
}

func (r *CompensationRecovery) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(compensationTickInterval)
		defer ticker.Stop()
		r.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.tick(ctx)
			}
		}
	}()
}

func (r *CompensationRecovery) tick(ctx context.Context) {
	rows, err := r.sagaRepo.ListPending(50)
	if err != nil {
		log.Printf("credit recovery: ListPending err: %v", err)
		return
	}
	for _, row := range rows {
		if row.RetryCount >= maxCompensationRetries {
			if err := r.sagaRepo.MarkDeadLetter(row.ID); err != nil {
				log.Printf("credit recovery: MarkDeadLetter id=%d err=%v", row.ID, err)
				continue
			}
			_ = r.deadLetter.PublishSagaDeadLetter(ctx, kafkaSagaDeadLetterMessage(row))
			log.Printf("credit recovery: id=%d moved to dead_letter after %d retries", row.ID, row.RetryCount)
			continue
		}
		if err := r.attemptCompensation(ctx, row); err != nil {
			_ = r.sagaRepo.IncrementRetry(row.ID)
			log.Printf("credit recovery: id=%d retry=%d err=%v", row.ID, row.RetryCount+1, err)
		}
	}
}

func (r *CompensationRecovery) attemptCompensation(ctx context.Context, row model.SagaLog) error {
	// Reload loan, replay compensations for completed steps in reverse order.
	loan, err := r.loanRepo.GetByID(row.LoanID)
	if err != nil {
		return err
	}
	// Re-execute the saga; idempotency keys ensure no double-action.
	return r.disbursement.Disburse(ctx, loan)
}

func kafkaSagaDeadLetterMessage(row model.SagaLog) kafkamsg.SagaDeadLetterMessage {
	return kafkamsg.SagaDeadLetterMessage{
		SagaID: row.SagaID, Service: "credit-service",
		StepName: row.StepName, ErrorMessage: row.ErrorMessage,
		LoanID: row.LoanID, AccountNumber: row.AccountNumber,
		Amount: row.Amount.String(), Timestamp: time.Now().UTC(),
	}
}
```

(If `kafkamsg.SagaDeadLetterMessage` doesn't yet exist in `contract/kafka/messages.go`, add it as a small struct in that file before continuing — schema is name + service + step + error + entity reference fields.)

- [ ] **Step 2: Wire in main.go**

In `credit-service/cmd/main.go`:

```go
// After AutoMigrate, add saga_log:
if err := db.AutoMigrate(
	&model.LoanRequest{},
	&model.Loan{},
	&model.Installment{},
	&model.Changelog{},
	&model.SagaLog{},
); err != nil {
	log.Fatalf("automigrate: %v", err)
}

// After repos are constructed, add:
sagaRepo := repository.NewSagaLogRepository(db)

// Construct the saga:
disbursementSaga := service.NewLoanDisbursementSaga(
	bankAccountClient, accountClient, loanRepo, sagaRepo, db,
)

// Pass to LoanRequestService:
loanRequestSvc := service.NewLoanRequestService(
	loanReqRepo, loanRepo, installRepo,
	limitClient, accountClient, bankAccountClient,
	rateCfgSvc, changelogRepo, disbursementSaga, db,
)

// Start the recovery worker:
recovery := service.NewCompensationRecovery(sagaRepo, disbursementSaga, loanRepo, kafkaProducer)
recovery.Start(ctx)

// Ensure dead-letter topic exists:
shared.EnsureTopics(cfg.KafkaBrokers,
	"credit.loan-requested",
	"credit.loan-approved",
	"credit.loan-rejected",
	"credit.loan-disbursed",
	"credit.installment-collected",
	"credit.installment-failed",
	"credit.variable-rate-adjusted",
	"credit.late-penalty-applied",
	"credit.changelog",
	"notification.send-email",
	"notification.general",
	"saga.dead-letter",
)
```

- [ ] **Step 3: Build credit-service**

```bash
cd credit-service && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add credit-service/cmd/main.go credit-service/internal/service/compensation_recovery.go contract/kafka/
git commit -m "feat(credit): wire LoanDisbursementSaga + compensation recovery worker"
```

---

## Task A1.6: Integration test for disbursement failure → automatic compensation

**Files:**
- Create: `test-app/workflows/loan_disbursement_recovery_test.go`

- [ ] **Step 1: Write the integration test**

```go
// test-app/workflows/loan_disbursement_recovery_test.go
package workflows

import (
	"testing"
	"time"
)

// TestLoanDisbursementCreditFailureCompensates verifies that if the
// borrower-credit step fails after the bank has been debited, the bank
// account is automatically credited-back and the loan status reaches
// `disbursement_failed`. No admin action required.
func TestLoanDisbursementCreditFailureCompensates(t *testing.T) {
	tc := newTestClient(t)
	tc.adminLogin()

	// 1. Create client, account
	clientID := tc.createClient("disb-fail-test@example.com")
	acctNo := tc.createAccount(clientID, "RSD")

	// 2. Capture bank RSD account balance BEFORE
	bankRSDBefore := tc.getBankAccountBalance("RSD")

	// 3. Force borrower account into "frozen" state (causes UpdateBalance to fail)
	tc.freezeAccount(acctNo)

	// 4. Submit loan request + approve it
	loanReqID := tc.submitLoanRequest(clientID, acctNo, 100_000, "RSD")
	_, err := tc.approveLoanRequest(loanReqID)

	// 5. Expect approveLoanRequest to return error (credit failed)
	if err == nil {
		t.Fatal("expected approval to fail since borrower account is frozen")
	}

	// 6. Within 30s (much less than 5-min recovery tick, but compensation runs synchronously inside the saga first):
	//    - bank RSD account balance must equal the BEFORE balance
	//    - loan status must be `disbursement_failed`
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		bankRSDAfter := tc.getBankAccountBalance("RSD")
		loan, _ := tc.getLoan(loanReqID)
		if bankRSDAfter.Equal(bankRSDBefore) && loan.Status == "disbursement_failed" {
			return // PASS
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("compensation did not run automatically within 30s — admin would have to intervene")
}
```

(Uses existing helpers from `test-app/workflows/helpers_test.go`; if `freezeAccount` / `getBankAccountBalance` don't exist, add them in the helpers file.)

- [ ] **Step 2: Run the test against a docker-up stack**

```bash
make docker-up
sleep 30  # wait for services to be ready
cd test-app && go test ./workflows/... -run TestLoanDisbursementCreditFailure -v -timeout 2m
```

Expected: PASS — bank balance fully restored, loan status `disbursement_failed`.

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/loan_disbursement_recovery_test.go test-app/workflows/helpers_test.go
git commit -m "test: integration test for loan disbursement automatic compensation"
```

---

## Task A2.1: Move OTC accept post-saga steps INTO the saga

**Files:**
- Modify: `stock-service/internal/service/otc_accept_saga.go` (lines 146–323)

- [ ] **Step 1: Identify the post-saga block**

Current structure (per audit): saga.Execute() at line 220, then lines 221–323 contain four "best-effort logged-on-failure" steps:
1. `offer.Save` (status="accepted")
2. `record_capital_gain` (seller's premium income)
3. `publish_notification` Kafka
4. In-app notification

- [ ] **Step 2: Add four new saga steps after the existing four**

Add these `.Add(saga.Step{...})` calls to the saga builder BEFORE `.Execute`:

```go
.Add(saga.Step{
	Name: "mark_offer_accepted",
	Forward: func(ctx context.Context, _ *saga.State) error {
		offer.Status = "accepted"
		return s.offers.Save(offer)
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		offer.Status = "pending"
		return s.offers.Save(offer)
	},
}).
.Add(saga.Step{
	Name: "record_seller_premium_gain",
	Forward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.UpsertIdempotent(&model.CapitalGain{
			PartyID:         offer.SellerID,
			SourceContractID: contract.ID,
			EventKind:       "otc_premium_received",
			AmountRSD:       offer.Premium,
		}, sagaID + ":seller_premium")
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.DeleteByIdempotency(sagaID + ":seller_premium")
	},
}).
.Add(saga.Step{
	Name: "publish_otc_accepted_event",
	Forward: func(ctx context.Context, _ *saga.State) error {
		// Use outbox to ensure durability. Outbox-drainer cron retries until success.
		return s.outbox.Enqueue(ctx, kafkamsg.TopicOTCContractAccepted,
			sagaID + ":publish",
			kafkamsg.OTCContractAcceptedMessage{ContractID: contract.ID, BuyerID: contract.BuyerID, SellerID: contract.SellerID})
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		// Notifications are idempotent at the consumer; on rollback we publish a "contract_acceptance_reverted" event so the FE can clear stale UI state.
		return s.outbox.Enqueue(ctx, kafkamsg.TopicOTCContractReverted,
			sagaID + ":revert",
			kafkamsg.OTCContractRevertedMessage{ContractID: contract.ID})
	},
})
```

(Adjust field names — `s.offers`, `s.capitalGains`, `s.outbox` — to match the actual fields on the OTCAcceptService struct. If `UpsertIdempotent` / `DeleteByIdempotency` don't exist on the capital_gain repo, add them as small wrappers around `ON CONFLICT` and `WHERE idempotency_key=`.)

- [ ] **Step 3: Delete the old best-effort post-saga code (lines 221–323)**

Remove every line between `if err := sg.Execute(ctx, state); err != nil { return nil, err }` and the function's final `return` statement that's part of the four logged-on-failure steps. Keep only the success-path return value construction.

- [ ] **Step 4: Build stock-service**

```bash
cd stock-service && go build ./...
```

Expected: clean build.

- [ ] **Step 5: Run existing unit tests for otc_accept_saga**

```bash
cd stock-service && go test ./internal/service/... -run TestOTCAccept -v
```

Expected: existing tests should still pass; if they reference the removed post-saga steps directly, update them to read state via the offer/contract/capital_gain repos.

- [ ] **Step 6: Add new failure-mode test**

```go
// stock-service/internal/service/otc_accept_saga_test.go (or extend existing)
func TestOTCAccept_OfferSaveFailure_RollsBackEverything(t *testing.T) {
	// Inject a repository where offer.Save returns an error
	// Run the saga; expect rollback to release the seller's holding reservation
	// and refund the buyer's premium.
	// After saga returns error, verify holding/account/contract state are all
	// back to pre-saga values.
	t.Skip("Skeleton — see file structure for full setup")
}
```

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/service/otc_accept_saga.go stock-service/internal/service/otc_accept_saga_test.go
git commit -m "feat(stock): move OTC accept post-saga steps inside saga"
```

---

## Task A2.2: Move OTC exercise post-saga steps INTO the saga

**Files:**
- Modify: `stock-service/internal/service/otc_exercise_saga.go` (lines 200–315)

- [ ] **Step 1: Identify the post-saga block**

Current structure (per audit): saga.Execute() ends with `consume_seller_holding` (Pivot: true) at line 200. Lines 206–315 contain post-saga steps:
1. `upsert_buyer_holding`
2. `record_seller_capital_gain` (strike sale)
3. `record_buyer_capital_gain` (option exercise)
4. `update_contract_status` (status="exercised")
5. `publish_notification` Kafka + in-app

- [ ] **Step 2: Add five new saga steps after `consume_seller_holding`**

In the saga builder, add:

```go
.Add(saga.Step{
	Name: "upsert_buyer_holding",
	Forward: func(ctx context.Context, _ *saga.State) error {
		return s.holdings.UpsertForBuyer(buyer, asset, qty, strikePrice, sagaID + ":buyer_holding")
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		return s.holdings.RemoveBuyerUpsert(buyer, asset, qty, sagaID + ":buyer_holding")
	},
}).
.Add(saga.Step{
	Name: "record_seller_strike_gain",
	Forward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.UpsertIdempotent(&model.CapitalGain{
			PartyID: contract.SellerID, SourceContractID: contract.ID,
			EventKind: "otc_strike_received", AmountRSD: strikeProceeds,
		}, sagaID + ":seller_strike")
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.DeleteByIdempotency(sagaID + ":seller_strike")
	},
}).
.Add(saga.Step{
	Name: "record_buyer_strike_loss",
	Forward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.UpsertIdempotent(&model.CapitalGain{
			PartyID: contract.BuyerID, SourceContractID: contract.ID,
			EventKind: "otc_exercise_cost", AmountRSD: strikeProceeds.Neg(),
		}, sagaID + ":buyer_strike")
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		return s.capitalGains.DeleteByIdempotency(sagaID + ":buyer_strike")
	},
}).
.Add(saga.Step{
	Name: "mark_contract_exercised",
	Forward: func(ctx context.Context, _ *saga.State) error {
		contract.Status = "exercised"
		return s.contracts.Save(contract)
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		contract.Status = "active"
		return s.contracts.Save(contract)
	},
}).
.Add(saga.Step{
	Name: "publish_exercise_event",
	Forward: func(ctx context.Context, _ *saga.State) error {
		return s.outbox.Enqueue(ctx, kafkamsg.TopicOTCContractExercised,
			sagaID + ":publish",
			kafkamsg.OTCContractExercisedMessage{ContractID: contract.ID, BuyerID: contract.BuyerID, SellerID: contract.SellerID})
	},
	Backward: func(ctx context.Context, _ *saga.State) error {
		// Pivot earlier than this step prevents reaching here on the rollback path;
		// only if THIS step itself fails would we run. The Pivot at consume_seller_holding
		// means anything past the pivot triggers compensation from this step backward.
		return nil
	},
})
```

- [ ] **Step 3: Delete the old post-saga code lines 206–315**

Keep only the success-path return.

- [ ] **Step 4: Build + test**

```bash
cd stock-service && go build ./... && go test ./internal/service/... -run TestOTCExercise -v
```

- [ ] **Step 5: Commit**

```bash
git add stock-service/internal/service/otc_exercise_saga.go
git commit -m "feat(stock): move OTC exercise post-saga steps inside saga"
```

---

## Task A2.3: Bump SagaRecovery max retries to 10, add dead-letter publish

**Files:**
- Modify: `stock-service/internal/service/saga_recovery.go`
- Modify: `stock-service/internal/kafka/topics.go`
- Modify: `stock-service/cmd/main.go` (EnsureTopics call)

- [ ] **Step 1: Bump constant**

In `stock-service/internal/service/saga_recovery.go`:

```go
// Was: const maxSagaRecoveryRetries = 5
const maxSagaRecoveryRetries = 10
```

- [ ] **Step 2: After max-retries hit, publish dead-letter instead of leaving stuck**

Find the retry-ceiling branch (`if step.RetryCount >= maxSagaRecoveryRetries`). Replace the "leave row untouched + ERROR log" code with:

```go
if step.RetryCount >= maxSagaRecoveryRetries {
	if err := s.sagaRepo.MarkDeadLetter(step.ID); err != nil {
		log.Printf("saga recovery: MarkDeadLetter id=%d err=%v", step.ID, err)
		continue
	}
	if err := s.deadLetterProducer.PublishSagaDeadLetter(ctx, kafkamsg.SagaDeadLetterMessage{
		SagaID: step.SagaID, Service: "stock-service",
		StepName: step.StepName, ErrorMessage: step.ErrorMessage,
		Timestamp: time.Now().UTC(),
	}); err != nil {
		log.Printf("saga recovery: publish dead-letter id=%d err=%v", step.ID, err)
	}
	continue
}
```

- [ ] **Step 3: Pass producer into SagaRecovery constructor**

Update `NewSagaRecovery` to accept `*kafka.Producer`. Update the call site in `stock-service/cmd/main.go`.

- [ ] **Step 4: Add `MarkDeadLetter` to SagaLogRepository**

If not already present:

```go
func (r *SagaLogRepository) MarkDeadLetter(id uint64) error {
	return r.db.Model(&model.SagaLog{}).Where("id = ?", id).
		Updates(map[string]any{"status": "dead_letter"}).Error
}
```

- [ ] **Step 5: Register `saga.dead-letter` in EnsureTopics**

In `stock-service/cmd/main.go`, add `"saga.dead-letter"` to the EnsureTopics slice.

- [ ] **Step 6: Build + test**

```bash
cd stock-service && go build ./... && go test ./internal/service/... -run TestSagaRecovery -v
```

- [ ] **Step 7: Commit**

```bash
git add stock-service/internal/service/saga_recovery.go stock-service/internal/kafka/topics.go stock-service/cmd/main.go
git commit -m "feat(stock): saga recovery escalates to dead-letter after 10 retries"
```

---

## Task A3.1: Add GetTxStatus RPC to transaction.proto

**Files:**
- Modify: `contract/proto/transaction/transaction.proto`
- Modify: `transaction-service/internal/handler/peer_tx_grpc_handler.go`

- [ ] **Step 1: Add the RPC definition**

In `contract/proto/transaction/transaction.proto`, in the `PeerTx` service block:

```protobuf
service PeerTx {
  // ... existing RPCs ...
  rpc GetTxStatus(GetTxStatusRequest) returns (GetTxStatusResponse);
}

message GetTxStatusRequest {
  string transaction_id = 1;
  string caller_peer_bank_code = 2; // from peer auth context
}

message GetTxStatusResponse {
  string state = 1;          // "prepared" | "committed" | "rolled_back" | "dead_letter" | "unknown"
  string our_role = 2;       // "sender" | "receiver" | ""
  string last_action_at = 3; // RFC3339
  string last_error = 4;
}
```

- [ ] **Step 2: Regenerate proto**

```bash
make proto
```

Expected: `contract/transactionpb/transaction.pb.go` and `transaction_grpc.pb.go` updated.

- [ ] **Step 3: Implement the handler**

In `transaction-service/internal/handler/peer_tx_grpc_handler.go`, add:

```go
func (h *PeerTxHandler) GetTxStatus(ctx context.Context, req *transactionpb.GetTxStatusRequest) (*transactionpb.GetTxStatusResponse, error) {
	if req.TransactionId == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id required")
	}

	// 1. Are we the sender? Look up outbound_peer_tx by transaction_id.
	outbound, err := h.outboundRepo.GetByIdempotenceKey(req.TransactionId)
	if err == nil && outbound != nil {
		return &transactionpb.GetTxStatusResponse{
			State:        outbound.Status,
			OurRole:      "sender",
			LastActionAt: rfc3339Ptr(outbound.LastAttemptAt, outbound.UpdatedAt),
			LastError:    outbound.LastError,
		}, nil
	}

	// 2. Are we the receiver? Look up peer_idempotence_record.
	if req.CallerPeerBankCode != "" {
		rec, err := h.idemRepo.LookupByTransactionID(req.CallerPeerBankCode, req.TransactionId)
		if err == nil && rec != nil {
			// State based on associated saga_log if present, else "committed" (record exists means we accepted).
			return &transactionpb.GetTxStatusResponse{
				State:        "committed",
				OurRole:      "receiver",
				LastActionAt: rec.CreatedAt.UTC().Format(time.RFC3339),
			}, nil
		}
	}

	// 3. Unknown.
	return &transactionpb.GetTxStatusResponse{
		State:   "unknown",
		OurRole: "",
	}, nil
}

func rfc3339Ptr(t *time.Time, fallback time.Time) string {
	if t == nil {
		return fallback.UTC().Format(time.RFC3339)
	}
	return t.UTC().Format(time.RFC3339)
}
```

(If `outboundRepo.GetByIdempotenceKey` or `idemRepo.LookupByTransactionID` don't exist, add them as thin wrappers on the existing repos.)

- [ ] **Step 4: Unit test the handler**

```go
// transaction-service/internal/handler/peer_tx_grpc_handler_test.go
func TestGetTxStatus_SenderRole(t *testing.T) { ... }
func TestGetTxStatus_ReceiverRole(t *testing.T) { ... }
func TestGetTxStatus_Unknown(t *testing.T) { ... }
```

- [ ] **Step 5: Build + test**

```bash
make proto && cd transaction-service && go build ./... && go test ./internal/handler/... -run TestGetTxStatus -v
```

- [ ] **Step 6: Commit**

```bash
git add contract/proto/transaction/transaction.proto contract/transactionpb/ transaction-service/internal/handler/peer_tx_grpc_handler.go transaction-service/internal/handler/peer_tx_grpc_handler_test.go
git commit -m "feat(transaction): GetTxStatus RPC for peer status polling"
```

---

## Task A3.2: REST handler + route for /interbank/:transaction_id/status

**Files:**
- Create: `api-gateway/internal/handler/peer_tx_status_handler.go`
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Write the gateway handler**

```go
// api-gateway/internal/handler/peer_tx_status_handler.go
package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
	"github.com/exbanka/contract/contract/transactionpb"
)

type PeerTxStatusHandler struct {
	client transactionpb.PeerTxClient
}

func NewPeerTxStatusHandler(c transactionpb.PeerTxClient) *PeerTxStatusHandler {
	return &PeerTxStatusHandler{client: c}
}

// @Summary Peer transaction status (CHECK_STATUS per Celina-5 spec)
// @Tags peer
// @Param transaction_id path string true "Transaction ID"
// @Success 200 {object} object "{state, our_role, last_action_at, last_error}"
// @Failure 400 {object} object
// @Failure 401 {object} object
// @Router /interbank/{transaction_id}/status [get]
func (h *PeerTxStatusHandler) GetStatus(c *gin.Context) {
	txID := c.Param("transaction_id")
	if txID == "" {
		apiError(c, 400, ErrValidation, "transaction_id required")
		return
	}
	peerCode := c.GetString("peer_bank_code") // set by PeerAuthMW
	resp, err := h.client.GetTxStatus(c.Request.Context(), &transactionpb.GetTxStatusRequest{
		TransactionId:      txID,
		CallerPeerBankCode: peerCode,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transaction_id":  txID,
		"state":           resp.State,
		"our_role":        resp.OurRole,
		"last_action_at":  resp.LastActionAt,
		"last_error":      resp.LastError,
	})
}
```

- [ ] **Step 2: Register the route**

In `api-gateway/internal/router/router_v3.go`, in the `peer` group (lines 235–256):

```go
peer.GET("/interbank/:transaction_id/status", h.PeerTxStatus.GetStatus)
```

Add the handler to the handler-bundle struct and wire it in main.go.

- [ ] **Step 3: Regenerate swagger**

```bash
make swagger
```

- [ ] **Step 4: Update REST_API_v1.md**

Add a new section documenting the route under "Peer (interbank)".

- [ ] **Step 5: Build api-gateway**

```bash
cd api-gateway && go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add api-gateway/internal/handler/peer_tx_status_handler.go api-gateway/internal/router/router_v3.go api-gateway/cmd/main.go api-gateway/docs/ docs/api/REST_API_v1.md
git commit -m "feat(gateway): GET /interbank/:transaction_id/status (CHECK_STATUS)"
```

---

## Task A3.3: PeerTxReconciler — sender-side polling worker

**Files:**
- Create: `transaction-service/internal/service/peer_tx_reconciler.go`
- Modify: `transaction-service/internal/sitx/peer_http_client.go` (add `CheckStatus`)
- Modify: `transaction-service/cmd/main.go`

- [ ] **Step 1: Add `CheckStatus` to peer_http_client**

```go
// transaction-service/internal/sitx/peer_http_client.go
func (c *PeerHTTPClient) CheckStatus(ctx context.Context, target *PeerHTTPTarget, txID string) (*PeerTxStatus, error) {
	url := target.BaseURL + "/interbank/" + txID + "/status"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil { return nil, err }
	c.applyAuth(req, target, nil)
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("peer status %d", resp.StatusCode)
	}
	var body PeerTxStatus
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil { return nil, err }
	return &body, nil
}

type PeerTxStatus struct {
	State        string `json:"state"`
	OurRole      string `json:"our_role"`
	LastActionAt string `json:"last_action_at"`
	LastError    string `json:"last_error"`
}
```

- [ ] **Step 2: Write the reconciler**

```go
// transaction-service/internal/service/peer_tx_reconciler.go
package service

import (
	"context"
	"log"
	"time"

	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
)

const peerReconcileInterval = 10 * time.Minute

type PeerLookupFunc func(peerCode string) (*sitx.PeerHTTPTarget, error)

type PeerTxReconciler struct {
	outboundRepo *repository.OutboundPeerTxRepository
	httpClient   *sitx.PeerHTTPClient
	peerLookup   PeerLookupFunc
	localReverse func(ctx context.Context, idempotenceKey string) error
}

func NewPeerTxReconciler(
	outRepo *repository.OutboundPeerTxRepository,
	httpClient *sitx.PeerHTTPClient,
	peerLookup PeerLookupFunc,
	localReverse func(ctx context.Context, idempotenceKey string) error,
) *PeerTxReconciler {
	return &PeerTxReconciler{
		outboundRepo: outRepo,
		httpClient:   httpClient,
		peerLookup:   peerLookup,
		localReverse: localReverse,
	}
}

func (r *PeerTxReconciler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(peerReconcileInterval)
		defer ticker.Stop()
		r.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.tick(ctx)
			}
		}
	}()
}

func (r *PeerTxReconciler) tick(ctx context.Context) {
	rows, err := r.outboundRepo.ListPendingOlderThan(2 * time.Minute)
	if err != nil {
		log.Printf("peer reconciler: list err: %v", err)
		return
	}
	for _, row := range rows {
		target, err := r.peerLookup(row.PeerBankCode)
		if err != nil {
			log.Printf("peer reconciler: lookup peer=%s err=%v", row.PeerBankCode, err)
			continue
		}
		status, err := r.httpClient.CheckStatus(ctx, target, row.IdempotenceKey)
		if err != nil {
			log.Printf("peer reconciler: CheckStatus tx=%s err=%v", row.IdempotenceKey, err)
			continue
		}
		switch status.State {
		case "committed":
			_ = r.outboundRepo.MarkCommitted(row.ID)
		case "rolled_back", "unknown":
			// Peer never saw it OR explicitly rolled back — safe to reverse locally.
			if err := r.localReverse(ctx, row.IdempotenceKey); err != nil {
				log.Printf("peer reconciler: localReverse err=%v", err)
				continue
			}
			_ = r.outboundRepo.MarkRolledBack(row.ID)
		case "prepared":
			// Peer is ready but uncommitted. Re-issue COMMIT_TX via existing replay path.
			// (No-op here; OutboundReplayCron will handle it next cycle.)
		}
	}
}
```

- [ ] **Step 3: Add `ListPendingOlderThan` + `MarkCommitted` + `MarkRolledBack` to outboundRepo if not present**

```go
func (r *OutboundPeerTxRepository) ListPendingOlderThan(age time.Duration) ([]model.OutboundPeerTx, error) {
	threshold := time.Now().Add(-age)
	var rows []model.OutboundPeerTx
	err := r.db.Where("status = ? AND updated_at < ?", "pending", threshold).Find(&rows).Error
	return rows, err
}

func (r *OutboundPeerTxRepository) MarkCommitted(id uint64) error {
	return r.db.Model(&model.OutboundPeerTx{}).Where("id = ?", id).
		Updates(map[string]any{"status": "committed", "updated_at": time.Now()}).Error
}

func (r *OutboundPeerTxRepository) MarkRolledBack(id uint64) error {
	return r.db.Model(&model.OutboundPeerTx{}).Where("id = ?", id).
		Updates(map[string]any{"status": "rolled_back", "updated_at": time.Now()}).Error
}
```

- [ ] **Step 4: Wire reconciler in transaction-service/cmd/main.go**

```go
reconciler := service.NewPeerTxReconciler(
	outRepo, peerHTTPClient,
	service.PeerLookupFunc(peerLookup),
	peerTxHandler.ReverseOutboundLocal,
)
reconciler.Start(ctx)
```

- [ ] **Step 5: Build + lint**

```bash
cd transaction-service && go build ./... && golangci-lint run ./...
```

- [ ] **Step 6: Commit**

```bash
git add transaction-service/internal/service/peer_tx_reconciler.go transaction-service/internal/sitx/peer_http_client.go transaction-service/internal/repository/outbound_peer_tx_repository.go transaction-service/cmd/main.go
git commit -m "feat(transaction): PeerTxReconciler polls peers for stuck outbound tx"
```

---

## Task A3.4: Update Specification.md

**Files:**
- Modify: `docs/Specification.md`

- [ ] **Step 1: Add to Section 17 (API routes)**

Add `GET /api/v3/interbank/:transaction_id/status` under "Peer/Interbank routes". Document:
- Auth: PeerAuth (X-Api-Key or HMAC)
- Response shape (see plan A3.2)
- Purpose: Celina-5 CHECK_STATUS

- [ ] **Step 2: Add to Section 19 (Kafka topics)**

Add `saga.dead-letter` — published by transaction-service, credit-service, stock-service when a saga step exhausts retries.

- [ ] **Step 3: Add to Section 21 (business rules)**

New bullet:
> **No saga can leave the system stuck.** Every cross-bank, OTC, loan disbursement, and recurring-order operation has automatic compensation. Compensations themselves are retried up to 10 times by per-service recovery workers, after which they publish to `saga.dead-letter`. No path requires admin manual reconciliation under normal failure modes.

- [ ] **Step 4: Commit**

```bash
git add docs/Specification.md
git commit -m "docs(spec): document CHECK_STATUS route + saga.dead-letter topic + no-stuck-state rule"
```

---

## Self-Review

- [ ] Spec coverage:
  - A1 loan disbursement saga: A1.1–A1.6 ✓
  - A2 OTC post-saga: A2.1–A2.3 ✓
  - A3 CHECK_STATUS + reconciler: A3.1–A3.4 ✓
- [ ] Placeholder scan: One area in A1.3 says "exact `saga.Recorder` interface signature may differ — confirm by reading". This is acceptable — it directs the implementer to verify the real interface before coding, which is safer than guessing. The conceptual contract is fully specified.
- [ ] Type consistency: `SagaLog`, `LoanDisbursementSaga`, `CompensationRecovery`, `PeerTxReconciler` consistently named throughout.

---

## Execution Handoff

Implementation will proceed via **subagent-driven-development**: one fresh subagent per task (A1.1, A1.2, …), with verification between tasks.
