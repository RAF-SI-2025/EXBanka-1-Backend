// Package grpc contains thin wrappers around gRPC client stubs used by
// stock-service. Keeping the wrappers here (instead of scattering
// accountpb.* calls throughout the service layer) isolates message-shape
// changes and gives a single place to attach reservation-lifecycle helpers
// used by the placement / fill sagas for bank-safe settlement.
package grpc

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/accountpb"
)

// AccountClient wraps the generated account-service gRPC stub with the
// reservation-lifecycle helpers that stock-service's placement and fill
// sagas rely on. Compensation and forex base-credit paths go through the
// same client, so all account-side writes originate here.
type AccountClient struct {
	stub pb.AccountServiceClient
}

// NewAccountClient builds an AccountClient around an existing gRPC stub.
// The stub is typically obtained from accountpb.NewAccountServiceClient(conn)
// in cmd/main.go; passing the stub (rather than dialing here) keeps
// connection ownership with the caller.
func NewAccountClient(stub pb.AccountServiceClient) *AccountClient {
	return &AccountClient{stub: stub}
}

// Stub exposes the underlying generated client for code paths that still
// need to call non-wrapped RPCs (GetAccount, ListAccountsByClient, etc.).
// New call sites should prefer dedicated wrapper methods.
func (c *AccountClient) Stub() pb.AccountServiceClient {
	return c.stub
}

// ReserveFunds holds funds on the user's account for a securities order.
// Idempotent on orderID: retries are safe no-ops — account-service replays
// the original response for a duplicate (accountID, orderID) pair.
func (c *AccountClient) ReserveFunds(ctx context.Context, accountID, orderID uint64, amount decimal.Decimal, currencyCode string) (*pb.ReserveFundsResponse, error) {
	return c.stub.ReserveFunds(ctx, &pb.ReserveFundsRequest{
		AccountId:    accountID,
		OrderId:      orderID,
		Amount:       amount.String(),
		CurrencyCode: currencyCode,
	})
}

// ReleaseReservation releases the unsettled remainder of a reservation.
// No-op if the reservation is missing, already released, or fully settled —
// account-service returns a zero released_amount in those cases instead of
// an error so cancel paths can be called unconditionally.
func (c *AccountClient) ReleaseReservation(ctx context.Context, orderID uint64) (*pb.ReleaseReservationResponse, error) {
	return c.stub.ReleaseReservation(ctx, &pb.ReleaseReservationRequest{OrderId: orderID})
}

// PartialSettleReservation commits part of a reservation as an actual debit
// and writes a ledger entry with the supplied memo. Idempotent on
// orderTransactionID: retries are safe no-ops — the same ledger entry is
// returned rather than double-debiting the account.
func (c *AccountClient) PartialSettleReservation(ctx context.Context, orderID, orderTransactionID uint64, amount decimal.Decimal, memo string) (*pb.PartialSettleReservationResponse, error) {
	return c.stub.PartialSettleReservation(ctx, &pb.PartialSettleReservationRequest{
		OrderId:            orderID,
		OrderTransactionId: orderTransactionID,
		Amount:             amount.String(),
		Memo:               memo,
	})
}

// GetReservation is read-only; stock-service uses it for crash-recovery
// reconciliation to check whether a pending settlement already committed
// before the process crashed.
func (c *AccountClient) GetReservation(ctx context.Context, orderID uint64) (*pb.GetReservationResponse, error) {
	return c.stub.GetReservation(ctx, &pb.GetReservationRequest{OrderId: orderID})
}

// CreditAccount increases the balance of the account identified by
// accountNumber by the given (positive) amount. Used for:
//   - Reverse-compensation after a downstream failure (re-credit the user).
//   - Forex base-currency credit (e.g. add EUR to the user's EUR account).
//   - Commission credit to the bank's commission account.
//
// Wraps the existing UpdateBalance RPC, which takes a signed amount and an
// update_available flag. This helper always updates the available balance
// (the semantics stock-service wants for every credit path) and forces the
// amount to be non-negative so callers can't accidentally debit via a
// negative value — debits must go through PartialSettleReservation.
//
// Note: account-service's UpdateBalanceRequest doesn't carry a memo field;
// the memo parameter is accepted here so call sites can document intent at
// the wrapper layer even though it's not persisted in a ledger entry.
// When memo-backed audit trails are required, callers should go through
// PartialSettleReservation (which does write to ledger_entries) instead.
func (c *AccountClient) CreditAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, _ string) (*pb.AccountResponse, error) {
	credit := amount.Abs()
	return c.stub.UpdateBalance(ctx, &pb.UpdateBalanceRequest{
		AccountNumber:   accountNumber,
		Amount:          credit.StringFixed(4),
		UpdateAvailable: true,
	})
}

// DebitAccount decreases the balance of the account identified by
// accountNumber by the given (positive) amount. It is the mirror of
// CreditAccount and is used by the sell-fill saga to reverse a previously
// credited amount when a later saga step (decrement_holding) fails.
//
// The `amount` argument MUST be positive; the wrapper forces the signed
// delta passed to UpdateBalance to be negative so callers cannot
// accidentally credit via a negative value. Debits tied to a reservation
// should still go through PartialSettleReservation (which writes a
// ledger-backed audit entry); this wrapper is deliberately narrow to the
// compensation path, where no reservation exists at the moment of reversal.
//
// Note: account-service's UpdateBalanceRequest doesn't carry a memo field;
// the memo parameter is accepted here so call sites can document intent at
// the wrapper layer even though it's not persisted in a ledger entry.
func (c *AccountClient) DebitAccount(ctx context.Context, accountNumber string, amount decimal.Decimal, _ string) (*pb.AccountResponse, error) {
	if amount.Sign() <= 0 {
		return nil, errors.New("amount must be > 0")
	}
	debit := amount.Abs().Neg()
	return c.stub.UpdateBalance(ctx, &pb.UpdateBalanceRequest{
		AccountNumber:   accountNumber,
		Amount:          debit.StringFixed(4),
		UpdateAvailable: true,
	})
}
