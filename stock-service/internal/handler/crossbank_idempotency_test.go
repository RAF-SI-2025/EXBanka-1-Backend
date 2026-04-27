// Idempotency-contract tests for CrossbankInternalHandler. Verifies the
// saga-step idempotency wiring (Plan 2026-04-27 Task 9) for the
// responder-side Handle* RPCs. Each handler:
//   - rejects requests without idempotency_key with InvalidArgument
//   - returns the cached response on retry without invoking the underlying
//     responder logic
package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func newCrossbankHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.IdempotencyRecord{}))
	return db
}

// TestCrossbank_HandleReserveSellerShares_Idempotent_ReturnsCachedResponse
// asserts that a Phase-2 retry with a previously-cached idempotency_key
// returns the cached response without invoking the underlying responder
// logic. All deps left nil so any re-execution would panic — proving the
// cache-hit path is taken.
func TestCrossbank_HandleReserveSellerShares_Idempotent_ReturnsCachedResponse(t *testing.T) {
	db := newCrossbankHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(db)
	h := &CrossbankInternalHandler{db: db, idem: idem}

	cached := &stockpb.ReserveSellerSharesResponse{
		Confirmed:     true,
		ReservationId: "12345",
	}
	blob, err := proto.Marshal(cached)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.IdempotencyRecord{
		Key: "saga-rss-key", ResponseBlob: blob,
	}).Error)

	resp, err := h.HandleReserveSellerShares(context.Background(), &stockpb.ReserveSellerSharesRequest{
		TxId: "tx-1", ContractId: 100, OfferId: 200,
		Quantity: "10", IdempotencyKey: "saga-rss-key",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Confirmed)
	assert.Equal(t, "12345", resp.ReservationId)
}

// TestCrossbank_MissingIdempotencyKey_Rejected asserts that all five
// saga-callee Handle* RPCs reject requests without idempotency_key with
// InvalidArgument before touching the responder logic.
func TestCrossbank_MissingIdempotencyKey_Rejected(t *testing.T) {
	db := newCrossbankHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(db)
	h := &CrossbankInternalHandler{db: db, idem: idem}

	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "HandleReserveSellerShares",
			call: func() error {
				_, err := h.HandleReserveSellerShares(context.Background(), &stockpb.ReserveSellerSharesRequest{
					TxId: "tx-1", ContractId: 1, OfferId: 1, Quantity: "1",
				})
				return err
			},
		},
		{
			name: "HandleTransferOwnership",
			call: func() error {
				_, err := h.HandleTransferOwnership(context.Background(), &stockpb.TransferOwnershipRequest{
					TxId: "tx-1", ContractId: 1, Quantity: "1",
				})
				return err
			},
		},
		{
			name: "HandleFinalize",
			call: func() error {
				_, err := h.HandleFinalize(context.Background(), &stockpb.FinalizeRequest{
					TxId: "tx-1", ContractId: 1,
				})
				return err
			},
		},
		{
			name: "HandleContractExpire",
			call: func() error {
				_, err := h.HandleContractExpire(context.Background(), &stockpb.ContractExpireRequest{
					TxId: "tx-1", ContractId: 1,
				})
				return err
			},
		},
		{
			name: "HandleReserveSharesRollback",
			call: func() error {
				_, err := h.HandleReserveSharesRollback(context.Background(), &stockpb.ReserveSharesRollbackRequest{
					TxId: "tx-1", ContractId: 1,
				})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err),
				"missing idempotency_key on %s must produce InvalidArgument; got %v", tc.name, err)
		})
	}
}
