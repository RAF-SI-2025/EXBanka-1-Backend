package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/identity"
	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// mockSagaLogReader satisfies the handler's sagaLogReader interface.
type mockSagaLogReader struct {
	gotFilter repository.SagaLogFilter
	logs      []model.SagaLog
	total     int64
	err       error
}

func (m *mockSagaLogReader) ListSagaLogs(f repository.SagaLogFilter) ([]model.SagaLog, int64, error) {
	m.gotFilter = f
	return m.logs, m.total, m.err
}

func newSagaHandler(reader sagaLogReader) *TransactionGRPCHandler {
	h := newTransactionGRPCHandlerForTest(&mockPaymentFacade{}, &mockTransferFacade{}, &mockRecipientFacade{}, &mockVerificationClient{}, &mockTxProducer{})
	if reader != nil {
		h.WithSagaLogReader(reader)
	}
	return h
}

// TestListSagaLogs_NoReader_Unimplemented verifies the RPC is Unimplemented
// when no saga-log reader was wired.
func TestListSagaLogs_NoReader_Unimplemented(t *testing.T) {
	h := newSagaHandler(nil)
	_, err := h.ListSagaLogs(context.Background(), &pb.ListSagaLogsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

// TestListSagaLogs_Success maps DB rows to proto entries and forwards the
// filter (including Since/Until conversion). It seeds one row with the optional
// CompensationOf and CompletedAt set to exercise those branches.
func TestListSagaLogs_Success(t *testing.T) {
	completed := time.Now()
	compOf := uint64(77)
	reader := &mockSagaLogReader{
		total: 2,
		logs: []model.SagaLog{
			{
				ID: 1, SagaID: "saga-A", TransactionID: 10, TransactionType: "transfer",
				StepNumber: 1, StepName: "debit_sender", Status: "completed",
				IsCompensation: false, AccountNumber: "ACC-1", Amount: decimal.NewFromInt(-100),
				RetryCount: 0, CreatedAt: time.Now(), CompletedAt: &completed,
			},
			{
				ID: 2, SagaID: "saga-A", TransactionID: 10, TransactionType: "transfer",
				StepNumber: 2, StepName: "credit_recipient", Status: "compensating",
				IsCompensation: true, AccountNumber: "ACC-2", Amount: decimal.NewFromInt(100),
				ErrorMessage: "boom", RetryCount: 3, CompensationOf: &compOf, CreatedAt: time.Now(),
			},
		},
	}
	h := newSagaHandler(reader)

	since := time.Now().Add(-time.Hour).Unix()
	until := time.Now().Add(time.Hour).Unix()
	resp, err := h.ListSagaLogs(context.Background(), &pb.ListSagaLogsRequest{
		SagaId: "saga-A", Status: "", TransactionType: "transfer",
		Page: 1, PageSize: 50, Since: since, Until: until,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int64(2), resp.Total)
	require.Len(t, resp.Logs, 2)

	// Filter forwarded correctly.
	assert.Equal(t, "saga-A", reader.gotFilter.SagaID)
	assert.Equal(t, "transfer", reader.gotFilter.TransactionType)
	assert.Equal(t, 1, reader.gotFilter.Page)
	assert.Equal(t, 50, reader.gotFilter.PageSize)
	assert.Equal(t, since, reader.gotFilter.Since.Unix())
	assert.Equal(t, until, reader.gotFilter.Until.Unix())

	// First entry: forward step, CompletedAt populated, no CompensationOf.
	e0 := resp.Logs[0]
	assert.Equal(t, uint64(1), e0.Id)
	assert.Equal(t, "debit_sender", e0.StepName)
	assert.Equal(t, int32(1), e0.StepNumber)
	assert.False(t, e0.IsCompensation)
	assert.NotZero(t, e0.CompletedAt)
	assert.Equal(t, uint64(0), e0.CompensationOf)

	// Second entry: compensation, CompensationOf populated.
	e1 := resp.Logs[1]
	assert.True(t, e1.IsCompensation)
	assert.Equal(t, uint64(77), e1.CompensationOf)
	assert.Equal(t, "boom", e1.ErrorMessage)
	assert.Equal(t, int32(3), e1.RetryCount)
}

// TestListSagaLogs_RepoError surfaces a repository error as Internal.
func TestListSagaLogs_RepoError(t *testing.T) {
	reader := &mockSagaLogReader{err: errors.New("db down")}
	h := newSagaHandler(reader)
	_, err := h.ListSagaLogs(context.Background(), &pb.ListSagaLogsRequest{Page: 0, PageSize: 0})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestGetTransferStatus_Success_AllStatuses verifies the four-state client
// mapping (mapTransferStatusToClient) and the last-changed timestamp selection.
func TestGetTransferStatus_Success_AllStatuses(t *testing.T) {
	completed := time.Now()
	cases := []struct {
		internal     string
		wantClient   string
		withComplete bool
	}{
		{"pending", "INITIATED", false},
		{"pending_verification", "INITIATED", false},
		{"processing", "PENDING", false},
		{"completed", "COMPLETED", true},
		{"failed", "FAILED", false},
		{"some_unknown_state", "INITIATED", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.internal, func(t *testing.T) {
			h := newTestHandler(nil, func(tm *mockTransferFacade) {
				tm.getTransferFn = func(id uint64) (*model.Transfer, error) {
					tr := &model.Transfer{
						ID: id, ClientID: 5, FromAccountNumber: "ACC-1", ToAccountNumber: "ACC-2",
						InitialAmount: decimal.NewFromInt(100), ExchangeRate: decimal.NewFromInt(1),
						Status: tc.internal, FailureReason: "rsn", Timestamp: time.Now(),
					}
					if tc.withComplete {
						tr.CompletedAt = &completed
					}
					return tr, nil
				}
			}, nil)
			ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
			resp, err := h.GetTransferStatus(ctx, &pb.GetTransferRequest{Id: 1})
			require.NoError(t, err)
			assert.Equal(t, tc.wantClient, resp.Status)
			assert.Equal(t, tc.internal, resp.InternalStatus)
			assert.Equal(t, "rsn", resp.FailureReason)
			assert.NotZero(t, resp.LastChangedUnix, "last-changed must fall back to timestamp when not completed")
		})
	}
}

// TestGetTransferStatus_NotFound verifies the lookup-error path returns NotFound.
func TestGetTransferStatus_NotFound(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.getTransferFn = func(uint64) (*model.Transfer, error) {
			return nil, errors.New("missing")
		}
	}, nil)
	_, err := h.GetTransferStatus(context.Background(), &pb.GetTransferRequest{Id: 9})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
