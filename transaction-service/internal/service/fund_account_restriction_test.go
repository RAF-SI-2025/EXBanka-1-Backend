package service

// E0 — Fund RSD account outflow restriction tests.
//
// Verifies that CreatePayment and CreateTransfer reject any attempt to
// use an investment_fund account as the source of a generic
// transfer/payment (Plan E invariant, 2026-05-28).

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/transaction-service/internal/model"
)

// TestCreatePayment_FundSourceRejected verifies that a payment whose source
// account carries account_category="investment_fund" is rejected with
// ErrFundAccountRestricted.
func TestCreatePayment_FundSourceRejected(t *testing.T) {
	ctx := context.Background()

	// FROM account tagged as investment_fund; TO account is a regular client.
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"FUND-RSD-001": 1_000_000_000, // bank-owned sentinel
			"CLIENT-001":   42,
		},
		categoryOverrides: map[string]string{
			"FUND-RSD-001": "investment_fund",
		},
	}

	feeSvc := newTestFeeService(0, 0) // zero fee to keep it simple
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-999", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	p := &model.Payment{
		IdempotencyKey:    "e0-fund-payment-001",
		FromAccountNumber: "FUND-RSD-001",
		ToAccountNumber:   "CLIENT-001",
		InitialAmount:     decimal.NewFromInt(1000),
		CurrencyCode:      "RSD",
	}
	err := svc.CreatePayment(ctx, p)
	require.Error(t, err, "payment from fund RSD account must be rejected")
	require.True(t, errors.Is(err, ErrFundAccountRestricted),
		"error must wrap ErrFundAccountRestricted; got: %v", err)
}

// TestCreateTransfer_FundSourceRejected verifies that a transfer whose source
// account carries account_category="investment_fund" is rejected with
// ErrFundAccountRestricted.
func TestCreateTransfer_FundSourceRejected(t *testing.T) {
	ctx := context.Background()

	// Both accounts owned by the bank sentinel (same owner = valid for transfer,
	// but the fund-category check must fire first).
	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"FUND-RSD-002": 1_000_000_000,
			"BANK-RSD-002": 1_000_000_000,
		},
		categoryOverrides: map[string]string{
			"FUND-RSD-002": "investment_fund",
		},
	}

	feeSvc := newTestFeeService(0, 0)
	repo := newMockTransferRepo()
	bankClient := &mockBankAccountClient{}
	svc := NewTransferService(repo, nil, accountClient, bankClient, feeSvc, nil, nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	tr := &model.Transfer{
		IdempotencyKey:    "e0-fund-transfer-001",
		FromAccountNumber: "FUND-RSD-002",
		ToAccountNumber:   "BANK-RSD-002",
		InitialAmount:     decimal.NewFromInt(500),
	}
	err := svc.CreateTransfer(ctx, tr)
	require.Error(t, err, "transfer from fund RSD account must be rejected")
	require.True(t, errors.Is(err, ErrFundAccountRestricted),
		"error must wrap ErrFundAccountRestricted; got: %v", err)
}

// TestCreatePayment_NonFundSourceAllowed verifies the happy path: a payment
// from a normal (non-fund) account is not blocked by the E0 guard.
func TestCreatePayment_NonFundSourceAllowed(t *testing.T) {
	ctx := context.Background()

	accountClient := &mockAccountClientForTransfer{
		ownerOverrides: map[string]uint64{
			"CLIENT-FROM-003": 10,
			"CLIENT-TO-003":   20,
		},
		// no categoryOverrides → AccountCategory defaults to ""
	}

	feeSvc := newTestFeeService(0, 0)
	repo := newMockPaymentRepo()
	svc := NewPaymentService(repo, accountClient, feeSvc, nil, "BANK-RSD-999", nil)
	svc.retryConfig = shared.RetryConfig{MaxAttempts: 1}

	p := &model.Payment{
		IdempotencyKey:    "e0-normal-payment-001",
		FromAccountNumber: "CLIENT-FROM-003",
		ToAccountNumber:   "CLIENT-TO-003",
		InitialAmount:     decimal.NewFromInt(100),
		CurrencyCode:      "RSD",
	}
	// Should not fail at CreatePayment (E0 guard must not fire).
	err := svc.CreatePayment(ctx, p)
	require.NoError(t, err, "normal payment must not be blocked by E0 guard")
}
