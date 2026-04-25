package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/credit-service/internal/model"
)

func TestLoanRequestService_GetLoanRequest_Found(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	req := &model.LoanRequest{
		ClientID: 7, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(150000), CurrencyCode: "RSD",
		RepaymentPeriod: 24, AccountNumber: "ACC-Q1", Status: "pending",
	}
	require.NoError(t, db.Create(req).Error)

	got, err := svc.GetLoanRequest(req.ID)
	require.NoError(t, err)
	assert.Equal(t, req.ID, got.ID)
	assert.Equal(t, "cash", got.LoanType)
	assert.Equal(t, uint64(7), got.ClientID)
}

func TestLoanRequestService_GetLoanRequest_NotFound(t *testing.T) {
	svc, _ := buildLoanRequestSvc(t, nil)
	_, err := svc.GetLoanRequest(99999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve loan request")
}

func TestLoanRequestService_ListLoanRequests_FiltersByClient(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	for i := 0; i < 3; i++ {
		req := &model.LoanRequest{
			ClientID: 1, LoanType: "cash", InterestType: "fixed",
			Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
			RepaymentPeriod: 12, AccountNumber: "ACC-A", Status: "pending",
		}
		require.NoError(t, db.Create(req).Error)
	}
	require.NoError(t, db.Create(&model.LoanRequest{
		ClientID: 2, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-B", Status: "pending",
	}).Error)

	got, total, err := svc.ListLoanRequests("", "", "", 1, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)
}

func TestLoanRequestService_ListLoanRequests_FiltersByStatus(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	require.NoError(t, db.Create(&model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-S1", Status: "pending",
	}).Error)
	require.NoError(t, db.Create(&model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "ACC-S2", Status: "approved",
	}).Error)

	got, total, err := svc.ListLoanRequests("", "", "approved", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "approved", got[0].Status)
}

func TestLoanRequestService_ListLoanRequests_FiltersByLoanTypeAndAccount(t *testing.T) {
	svc, db := buildLoanRequestSvc(t, nil)
	require.NoError(t, db.Create(&model.LoanRequest{
		ClientID: 1, LoanType: "cash", InterestType: "fixed",
		Amount: decimal.NewFromInt(100000), CurrencyCode: "RSD",
		RepaymentPeriod: 12, AccountNumber: "TARGET", Status: "pending",
	}).Error)
	require.NoError(t, db.Create(&model.LoanRequest{
		ClientID: 1, LoanType: "housing", InterestType: "fixed",
		Amount: decimal.NewFromInt(2000000), CurrencyCode: "RSD",
		RepaymentPeriod: 240, AccountNumber: "TARGET", Status: "pending",
	}).Error)

	got, total, err := svc.ListLoanRequests("cash", "", "", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "cash", got[0].LoanType)

	got2, total2, err := svc.ListLoanRequests("", "TARGET", "", 0, 1, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total2)
	for _, r := range got2 {
		assert.Equal(t, "TARGET", r.AccountNumber)
	}
}
