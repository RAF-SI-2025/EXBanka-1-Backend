// Idempotency-contract tests for InterBankGRPCHandler. Verifies the
// saga-step idempotency wiring (Plan 2026-04-27 Task 9) for the receiver-
// side Handle* RPCs. Each handler:
//   - rejects requests without idempotency_key with InvalidArgument
//   - returns the cached response on retry without invoking svc again
//
// Cache-hit testing exploits the fact that a pre-seeded record returns
// directly from repository.Run, so we can leave svc=nil and still observe
// the cached response on the second call.
package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

func newInterBankHandlerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbName := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:ib_handler_%s?mode=memory&cache=shared", dbName)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.IdempotencyRecord{}))
	return db
}

// TestInterBank_HandlePrepare_Idempotent_ReturnsCachedResponse asserts that
// a Prepare retry with a previously-cached idempotency_key returns the
// cached response without invoking the service layer. The svc is nil so any
// re-execution would panic — proving the cache-hit path is taken.
func TestInterBank_HandlePrepare_Idempotent_ReturnsCachedResponse(t *testing.T) {
	db := newInterBankHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(db)
	h := &InterBankGRPCHandler{svc: nil, db: db, idem: idem}

	cached := &pb.InterBankPrepareResponse{
		TransactionId: "tx-123",
		Ready:         true,
		FinalAmount:   "200.00",
		FinalCurrency: "RSD",
	}
	blob, err := proto.Marshal(cached)
	require.NoError(t, err)
	require.NoError(t, db.Create(&model.IdempotencyRecord{
		Key: "saga-prepare-key", ResponseBlob: blob,
	}).Error)

	resp, err := h.HandlePrepare(context.Background(), &pb.InterBankPrepareRequest{
		TransactionId:  "tx-123",
		Amount:         "200",
		IdempotencyKey: "saga-prepare-key",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "tx-123", resp.TransactionId)
	assert.True(t, resp.Ready)
	assert.Equal(t, "200.00", resp.FinalAmount)
	assert.Equal(t, "RSD", resp.FinalCurrency)
}

// TestInterBank_MissingIdempotencyKey_Rejected asserts that all three
// saga-callee RPCs (HandlePrepare / HandleCommit / ReverseInterBankTransfer)
// reject requests without idempotency_key with InvalidArgument before
// touching the service layer.
func TestInterBank_MissingIdempotencyKey_Rejected(t *testing.T) {
	db := newInterBankHandlerTestDB(t)
	idem := repository.NewIdempotencyRepository(db)
	h := &InterBankGRPCHandler{svc: nil, db: db, idem: idem}

	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "HandlePrepare",
			call: func() error {
				_, err := h.HandlePrepare(context.Background(), &pb.InterBankPrepareRequest{
					TransactionId: "tx-1", Amount: "100",
				})
				return err
			},
		},
		{
			name: "HandleCommit",
			call: func() error {
				_, err := h.HandleCommit(context.Background(), &pb.InterBankCommitRequest{
					TransactionId: "tx-1", FinalAmount: "100", FinalCurrency: "RSD",
				})
				return err
			},
		},
		{
			name: "ReverseInterBankTransfer",
			call: func() error {
				_, err := h.ReverseInterBankTransfer(context.Background(), &pb.ReverseInterBankTransferRequest{
					OriginalTxId: "tx-1",
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
