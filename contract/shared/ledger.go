// Package shared — ledger.go is a generic, idempotent audit-log abstraction.
//
// "Ledger" here means the append-only record of state changes — debits,
// credits, transfers, settlements — not the *balance* itself. Balances live
// in domain-specific tables (Account, Loan, Holding) because their shape and
// invariants differ. This file gives every service the same vocabulary and
// helpers for the audit-log half of the pattern.
//
// What's reusable across services:
//
//   - Entry shape: type + amount + reference + idempotency key + audit
//     timestamps. Suitable for money movements (account-service, transaction-
//     service), loan installment payments (credit-service), trade settlements
//     (stock-service), and any future bank-grade auditable operation.
//
//   - Idempotent insert: a deterministic IdempotencyKey (e.g., "transfer-42",
//     "loan-7-installment-3") makes a re-applied operation a safe no-op. This
//     mirrors account-service's existing partial unique index pattern.
//
//   - Movement (debit + credit pair): the saga-friendly envelope for a
//     single operation that records two sides of a double-entry transfer.
//
// What stays per-service:
//
//   - The actual GORM model that maps to the table. Each service embeds
//     EntryFields into its own struct so it can add domain-specific columns
//     (e.g., AccountNumber for account-service, LoanID for credit-service).
//
//   - The repository: each service implements Store over its own table.
//
// This file imposes nothing on the storage layer — it's a vocabulary plus
// thin helpers. Adoption is incremental: a service can keep its current
// repository methods and add a Store adapter when it wants to participate
// in cross-service patterns (e.g., the saga Recorder reading idempotency
// keys uniformly).
package shared

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// EntryType is the kind of ledger movement.
type EntryType string

const (
	EntryDebit  EntryType = "debit"
	EntryCredit EntryType = "credit"
)

// IsValid reports whether the type is one of the known values.
func (e EntryType) IsValid() bool {
	return e == EntryDebit || e == EntryCredit
}

// Negate returns the opposite type. Useful when building compensation
// entries (a debit's compensation is a credit and vice versa).
func (e EntryType) Negate() EntryType {
	if e == EntryDebit {
		return EntryCredit
	}
	return EntryDebit
}

// EntryFields is the set of columns every ledger row should carry. Embed
// this in a service's GORM model and add domain-specific columns alongside.
//
// Example (account-service):
//
//	type LedgerEntry struct {
//	    shared.EntryFields
//	    AccountNumber string `gorm:"size:34;not null;index"`
//	}
//
// The GORM tags here are deliberately neutral (no table-specific index
// names) so embedded usage doesn't conflict across tables.
type EntryFields struct {
	ID             uint64          `gorm:"primaryKey;autoIncrement"`
	EntryType      EntryType       `gorm:"size:16;not null"`
	Amount         decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	BalanceBefore  decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	BalanceAfter   decimal.Decimal `gorm:"type:numeric(18,4);not null"`
	Description    string          `gorm:"size:512"`
	ReferenceID    string          `gorm:"size:128;index"`
	ReferenceType  string          `gorm:"size:50"`
	IdempotencyKey string          `gorm:"size:128"` // unique-per-table; index defined by the embedder
	CreatedAt      time.Time       `gorm:"not null;index"`
}

// Entry is the protocol-level (non-GORM) view of a ledger row, used by
// services that want to interact with each other's ledger data without
// importing per-service models.
type Entry struct {
	ID             uint64
	Subject        string // generic owner key — account number, loan id, holding id
	Type           EntryType
	Amount         decimal.Decimal
	BalanceBefore  decimal.Decimal
	BalanceAfter   decimal.Decimal
	Description    string
	ReferenceID    string
	ReferenceType  string
	IdempotencyKey string
	CreatedAt      time.Time
}

// Validate checks the basic invariants every ledger entry must satisfy.
// Catches programmer errors at the call boundary before they hit the DB.
func (e *Entry) Validate() error {
	if e == nil {
		return errors.New("ledger: nil entry")
	}
	if !e.Type.IsValid() {
		return errors.New("ledger: invalid entry type")
	}
	if e.Amount.IsNegative() {
		return errors.New("ledger: amount cannot be negative")
	}
	if e.Amount.IsZero() {
		return errors.New("ledger: amount cannot be zero")
	}
	if e.Subject == "" {
		return errors.New("ledger: subject cannot be empty")
	}
	return nil
}

// Movement is a double-entry pair: debit one subject, credit another. The
// canonical case is a transfer (debit sender, credit receiver). Both
// entries share the same ReferenceID and ReferenceType so the pair is
// queryable as a unit.
type Movement struct {
	Debit  Entry
	Credit Entry
}

// NewMovement constructs a debit/credit pair from the named subjects with
// matching reference fields. Idempotency keys are derived from the
// reference id with side-suffixes so retries are safe at row granularity.
func NewMovement(refType, refID, debitSubject, creditSubject string, amount decimal.Decimal, description string) Movement {
	now := time.Now().UTC()
	return Movement{
		Debit: Entry{
			Subject:        debitSubject,
			Type:           EntryDebit,
			Amount:         amount,
			Description:    description,
			ReferenceID:    refID,
			ReferenceType:  refType,
			IdempotencyKey: refID + ":debit",
			CreatedAt:      now,
		},
		Credit: Entry{
			Subject:        creditSubject,
			Type:           EntryCredit,
			Amount:         amount,
			Description:    description,
			ReferenceID:    refID,
			ReferenceType:  refType,
			IdempotencyKey: refID + ":credit",
			CreatedAt:      now,
		},
	}
}

// Store is the persistence port a service implements over its own ledger
// table. Adopting Store lets cross-service code (e.g., a saga recovery
// loop checking whether an idempotency key already landed) work uniformly
// without per-service plumbing.
//
// Implementations must:
//
//   - RecordEntry: insert one entry. May fail with ErrDuplicateEntry if
//     IdempotencyKey collides — the caller can treat the existing row as
//     the authoritative result.
//
//   - RecordMovement: insert both sides of a Movement in one DB transaction.
//     If either side fails, both are rolled back.
//
//   - GetByIdempotencyKey: return the entry with the given key, or
//     ErrNotFound if no row exists. Used by saga recovery to determine
//     whether a forward step's effect already landed.
//
//   - ListBySubject / ListByReference: read access for queries.
type Store interface {
	RecordEntry(ctx context.Context, entry *Entry) error
	RecordMovement(ctx context.Context, mv *Movement) error
	GetByIdempotencyKey(ctx context.Context, key string) (*Entry, error)
	ListBySubject(ctx context.Context, subject string, page, pageSize int) ([]Entry, int64, error)
	ListByReference(ctx context.Context, refType, refID string) ([]Entry, error)
}

// ErrDuplicateEntry indicates an attempted insert with an IdempotencyKey
// that already exists. Callers may treat this as success and read the
// existing row via GetByIdempotencyKey.
var ErrDuplicateEntry = errors.New("ledger: duplicate idempotency key")

// ErrNotFound indicates a lookup found no matching row.
var ErrNotFound = errors.New("ledger: entry not found")

// ApplyMovement validates a movement and records it via the store. Returns
// the store's error directly so callers can match on ErrDuplicateEntry to
// treat re-applied movements as no-ops.
//
// Helper kept thin on purpose: services that need pre/post hooks (event
// publishing, metrics) should call store.RecordMovement directly so the
// hooks live in the service layer where they belong.
func ApplyMovement(ctx context.Context, store Store, mv *Movement) error {
	if mv == nil {
		return errors.New("ledger: nil movement")
	}
	if err := mv.Debit.Validate(); err != nil {
		return err
	}
	if err := mv.Credit.Validate(); err != nil {
		return err
	}
	if !mv.Debit.Amount.Equal(mv.Credit.Amount) {
		return errors.New("ledger: movement debit/credit amounts do not match")
	}
	if mv.Debit.ReferenceID != mv.Credit.ReferenceID || mv.Debit.ReferenceType != mv.Credit.ReferenceType {
		return errors.New("ledger: movement debit/credit references do not match")
	}
	return store.RecordMovement(ctx, mv)
}
