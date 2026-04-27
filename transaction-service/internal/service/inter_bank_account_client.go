package service

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
)

// GRPCAccountInterBankClient adapts accountpb.AccountServiceClient to the
// AccountInterBankClient interface used by InterBankService. Inter-bank
// debits/credits are implemented as UpdateBalance calls with idempotency
// keys so retries (after a crash or reconciler intervention) are safe.
type GRPCAccountInterBankClient struct {
	client accountpb.AccountServiceClient
}

// NewGRPCAccountInterBankClient constructs the adapter.
func NewGRPCAccountInterBankClient(c accountpb.AccountServiceClient) *GRPCAccountInterBankClient {
	return &GRPCAccountInterBankClient{client: c}
}

// GetAccountByNumber forwards directly.
func (a *GRPCAccountInterBankClient) GetAccountByNumber(ctx context.Context, accountNumber string) (*accountpb.AccountResponse, error) {
	return a.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: accountNumber})
}

// DebitForInterbank decrements both Balance and AvailableBalance by amount,
// writing a debit ledger entry. Idempotent on idempotencyKey.
func (a *GRPCAccountInterBankClient) DebitForInterbank(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) error {
	if !amount.IsPositive() {
		return fmt.Errorf("debit amount must be > 0, got %s", amount)
	}
	_, err := a.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   accountNumber,
		Amount:          amount.Neg().String(),
		UpdateAvailable: true,
		Memo:            memo,
		IdempotencyKey:  idempotencyKey,
	})
	return err
}

// CreditBackForInterbank credits the sender back when the inter-bank
// transfer rolls back. Mirrors DebitForInterbank with positive amount.
func (a *GRPCAccountInterBankClient) CreditBackForInterbank(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) error {
	if !amount.IsPositive() {
		return fmt.Errorf("credit amount must be > 0, got %s", amount)
	}
	_, err := a.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
		AccountNumber:   accountNumber,
		Amount:          amount.String(),
		UpdateAvailable: true,
		Memo:            memo,
		IdempotencyKey:  idempotencyKey,
	})
	return err
}

// ReserveIncoming forwards to AccountService.ReserveIncoming.
//
// The saga-step idempotency_key is derived from reservationKey + step name
// so retries (saga reconciler, network re-send) collapse through the
// account-side response cache. reservationKey itself remains the
// authoritative domain dedup key inside account-service.
func (a *GRPCAccountInterBankClient) ReserveIncoming(ctx context.Context, accountNumber string, amount decimal.Decimal, currency, reservationKey string) error {
	_, err := a.client.ReserveIncoming(ctx, &accountpb.ReserveIncomingRequest{
		AccountNumber:  accountNumber,
		Amount:         amount.String(),
		Currency:       currency,
		ReservationKey: reservationKey,
		IdempotencyKey: reservationKey + ":reserve_incoming",
	})
	return err
}

// CommitIncoming forwards to AccountService.CommitIncoming.
func (a *GRPCAccountInterBankClient) CommitIncoming(ctx context.Context, reservationKey string) error {
	_, err := a.client.CommitIncoming(ctx, &accountpb.CommitIncomingRequest{
		ReservationKey: reservationKey,
		IdempotencyKey: reservationKey + ":commit_incoming",
	})
	return err
}

// ReleaseIncoming forwards to AccountService.ReleaseIncoming.
func (a *GRPCAccountInterBankClient) ReleaseIncoming(ctx context.Context, reservationKey string) error {
	_, err := a.client.ReleaseIncoming(ctx, &accountpb.ReleaseIncomingRequest{
		ReservationKey: reservationKey,
		IdempotencyKey: reservationKey + ":release_incoming",
	})
	return err
}
