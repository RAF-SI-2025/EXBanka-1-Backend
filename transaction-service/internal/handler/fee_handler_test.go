package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

// ---------------------------------------------------------------------------
// Mock stub for feeFacade
// ---------------------------------------------------------------------------

type mockFeeFacade struct {
	listFeesFn             func() ([]model.TransferFee, error)
	createFeeFn            func(fee *model.TransferFee) error
	getFeeFn               func(id uint64) (*model.TransferFee, error)
	updateFeeFn            func(fee *model.TransferFee) error
	deactivateFeeFn        func(id uint64) error
	calculateFeeDetailedFn func(amount decimal.Decimal, txType, currency string) (decimal.Decimal, []service.FeeDetail, error)
}

func (m *mockFeeFacade) ListFees() ([]model.TransferFee, error) {
	if m.listFeesFn != nil {
		return m.listFeesFn()
	}
	return nil, nil
}

func (m *mockFeeFacade) CreateFee(fee *model.TransferFee) error {
	if m.createFeeFn != nil {
		return m.createFeeFn(fee)
	}
	return nil
}

func (m *mockFeeFacade) GetFee(id uint64) (*model.TransferFee, error) {
	if m.getFeeFn != nil {
		return m.getFeeFn(id)
	}
	return nil, nil
}

func (m *mockFeeFacade) UpdateFee(fee *model.TransferFee) error {
	if m.updateFeeFn != nil {
		return m.updateFeeFn(fee)
	}
	return nil
}

func (m *mockFeeFacade) DeactivateFee(id uint64) error {
	if m.deactivateFeeFn != nil {
		return m.deactivateFeeFn(id)
	}
	return nil
}

func (m *mockFeeFacade) CalculateFeeDetailed(amount decimal.Decimal, txType, currency string) (decimal.Decimal, []service.FeeDetail, error) {
	if m.calculateFeeDetailedFn != nil {
		return m.calculateFeeDetailedFn(amount, txType, currency)
	}
	return decimal.Zero, nil, nil
}

// ---------------------------------------------------------------------------
// Fee handler tests
// ---------------------------------------------------------------------------

func TestListFees_Success(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		listFeesFn: func() ([]model.TransferFee, error) {
			return []model.TransferFee{
				{ID: 1, Name: "Basic", FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1), TransactionType: "all", Active: true},
				{ID: 2, Name: "High", FeeType: "fixed", FeeValue: decimal.NewFromInt(5), TransactionType: "payment", Active: true},
			}, nil
		},
	})

	resp, err := h.ListFees(context.Background(), &pb.ListFeesRequest{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Fees, 2)
	assert.Equal(t, uint64(1), resp.Fees[0].Id)
	assert.Equal(t, "Basic", resp.Fees[0].Name)
	assert.Equal(t, "percentage", resp.Fees[0].FeeType)
	assert.True(t, resp.Fees[0].Active)
}

func TestListFees_Error(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		listFeesFn: func() ([]model.TransferFee, error) {
			return nil, errors.New("database error")
		},
	})

	_, err := h.ListFees(context.Background(), &pb.ListFeesRequest{})

	require.Error(t, err)
	// Untyped errors now pass through as Unknown — see
	// TestSentinel_Passthrough_TransactionHandler for the typed contract.
	assert.Equal(t, codes.Unknown, status.Code(err))
}

func TestCreateFee_Success(t *testing.T) {
	created := false
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		createFeeFn: func(fee *model.TransferFee) error {
			fee.ID = 10
			created = true
			return nil
		},
	})

	resp, err := h.CreateFee(context.Background(), &pb.CreateFeeRequest{
		Name:            "Basic",
		FeeType:         "percentage",
		FeeValue:        "0.1",
		TransactionType: "all",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, created)
	assert.Equal(t, uint64(10), resp.Id)
	assert.Equal(t, "Basic", resp.Name)
	assert.Equal(t, "percentage", resp.FeeType)
	assert.True(t, resp.Active)
}

func TestCreateFee_InvalidFeeValue(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{})

	_, err := h.CreateFee(context.Background(), &pb.CreateFeeRequest{
		Name:            "Bad",
		FeeType:         "percentage",
		FeeValue:        "not-a-number",
		TransactionType: "all",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUpdateFee_Success(t *testing.T) {
	existingFee := &model.TransferFee{
		ID:              3,
		Name:            "OldName",
		FeeType:         "fixed",
		FeeValue:        decimal.NewFromInt(5),
		TransactionType: "payment",
		Active:          true,
	}
	updated := false
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		getFeeFn: func(id uint64) (*model.TransferFee, error) {
			return existingFee, nil
		},
		updateFeeFn: func(fee *model.TransferFee) error {
			updated = true
			return nil
		},
	})

	resp, err := h.UpdateFee(context.Background(), &pb.UpdateFeeRequest{
		Id:   3,
		Name: "NewName",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, updated)
	assert.Equal(t, "NewName", resp.Name)
}

func TestUpdateFee_NotFound(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		getFeeFn: func(id uint64) (*model.TransferFee, error) {
			return nil, gorm.ErrRecordNotFound
		},
	})

	_, err := h.UpdateFee(context.Background(), &pb.UpdateFeeRequest{Id: 999})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestCalculateFee_Success(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		calculateFeeDetailedFn: func(amount decimal.Decimal, txType, currency string) (decimal.Decimal, []service.FeeDetail, error) {
			return decimal.NewFromFloat(10.5), []service.FeeDetail{
				{Name: "Basic", FeeType: "percentage", FeeValue: decimal.NewFromFloat(0.1), CalculatedAmount: decimal.NewFromFloat(10.5)},
			}, nil
		},
	})

	resp, err := h.CalculateFee(context.Background(), &pb.CalculateFeeRequest{
		Amount:          "100.00",
		TransactionType: "payment",
		CurrencyCode:    "RSD",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "10.5000", resp.TotalFee)
	assert.Len(t, resp.AppliedFees, 1)
	assert.Equal(t, "Basic", resp.AppliedFees[0].Name)
}

func TestCalculateFee_InvalidAmount(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{})

	_, err := h.CalculateFee(context.Background(), &pb.CalculateFeeRequest{
		Amount:          "-50.00",
		TransactionType: "payment",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCalculateFee_MissingTransactionType(t *testing.T) {
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{})

	_, err := h.CalculateFee(context.Background(), &pb.CalculateFeeRequest{
		Amount:          "100.00",
		TransactionType: "",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestDeleteFee_Success(t *testing.T) {
	deactivated := false
	h := newFeeGRPCHandlerForTest(&mockFeeFacade{
		deactivateFeeFn: func(id uint64) error {
			deactivated = true
			return nil
		},
	})

	resp, err := h.DeleteFee(context.Background(), &pb.DeleteFeeRequest{Id: 1})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "fee deactivated", resp.Message)
	assert.True(t, deactivated)
}
