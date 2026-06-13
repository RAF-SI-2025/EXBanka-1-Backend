package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/contract/identity"
	"github.com/exbanka/credit-service/internal/model"
)

func ctxAs(c identity.Caller) context.Context {
	md, _ := metadata.FromOutgoingContext(identity.Inject(context.Background(), c))
	return metadata.NewIncomingContext(context.Background(), md)
}

func clientCtx(id int64) context.Context {
	return ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: id})
}

func TestOWN1_GetLoan_ClientForeign_NotFound(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(id uint64) (*model.Loan, error) { return &model.Loan{ID: id, ClientID: 9}, nil }
	_, err := h.GetLoan(clientCtx(5), &pb.GetLoanReq{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetLoan_ClientOwn_OK(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(id uint64) (*model.Loan, error) { return &model.Loan{ID: id, ClientID: 5}, nil }
	_, err := h.GetLoan(clientCtx(5), &pb.GetLoanReq{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_GetLoan_Employee_OK(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(id uint64) (*model.Loan, error) { return &model.Loan{ID: id, ClientID: 9}, nil }
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7})
	_, err := h.GetLoan(ctx, &pb.GetLoanReq{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_GetLoanRequest_ClientForeign_NotFound(t *testing.T) {
	h, lrSvc, _, _, _ := newTestHandler()
	lrSvc.getFn = func(id uint64) (*model.LoanRequest, error) { return &model.LoanRequest{ID: id, ClientID: 9}, nil }
	_, err := h.GetLoanRequest(clientCtx(5), &pb.GetLoanRequestReq{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_ListLoansByClient_ClientForeign_Forbidden(t *testing.T) {
	h, _, _, _, _ := newTestHandler()
	_, err := h.ListLoansByClient(clientCtx(5), &pb.ListLoansByClientReq{ClientId: 9})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestOWN1_GetInstallmentsByLoan_ClientForeign_NotFound(t *testing.T) {
	h, _, lSvc, _, _ := newTestHandler()
	lSvc.getFn = func(id uint64) (*model.Loan, error) { return &model.Loan{ID: id, ClientID: 9}, nil }
	_, err := h.GetInstallmentsByLoan(clientCtx(5), &pb.GetInstallmentsByLoanReq{LoanId: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
