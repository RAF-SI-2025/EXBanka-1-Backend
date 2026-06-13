package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/exbanka/account-service/internal/model"
	pb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/contract/identity"
)

// ctxAs builds an incoming-metadata context as the given caller would arrive at
// the service (gateway stamps outgoing identity; the transport moves it to the
// incoming side of the callee).
func ctxAs(c identity.Caller) context.Context {
	md, _ := metadata.FromOutgoingContext(identity.Inject(context.Background(), c))
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestOWN1_GetAccount_ClientForeignAccount_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return &model.Account{ID: id, OwnerID: 9, AccountNumber: "111"}, nil
	}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetAccount(ctx, &pb.GetAccountRequest{Id: 1})
	require.Error(t, err)
	// 404, not 403 — must not leak that the account exists.
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetAccount_ClientOwn_OK(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return &model.Account{ID: id, OwnerID: 5, AccountNumber: "111"}, nil
	}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	resp, err := h.GetAccount(ctx, &pb.GetAccountRequest{Id: 1})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.OwnerId)
}

func TestOWN1_GetAccount_EmployeeAnyAccount_OK(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return &model.Account{ID: id, OwnerID: 9, AccountNumber: "111"}, nil
	}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7})
	_, err := h.GetAccount(ctx, &pb.GetAccountRequest{Id: 1})
	require.NoError(t, err, "employee may read any account")
}

func TestOWN1_GetAccount_ServiceCall_OK(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return &model.Account{ID: id, OwnerID: 9, AccountNumber: "111"}, nil
	}
	// No identity metadata → trusted service call (e.g. stock-service).
	_, err := h.GetAccount(context.Background(), &pb.GetAccountRequest{Id: 1})
	require.NoError(t, err, "service-to-service call may read any account")
}

func TestOWN1_ListAccountsByClient_ClientForeign_Forbidden(t *testing.T) {
	h, _ := newGRPCHandlerFixture(t)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.ListAccountsByClient(ctx, &pb.ListAccountsByClientRequest{ClientId: 9})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestOWN1_GetLedgerEntries_ClientForeign_NotFound(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountByNumberFn = func(n string) (*model.Account, error) {
		return &model.Account{AccountNumber: n, OwnerID: 9}, nil
	}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetLedgerEntries(ctx, &pb.GetLedgerEntriesRequest{AccountNumber: "111", Page: 1, PageSize: 10})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_EmployeeOnBehalf(t *testing.T) {
	h, f := newGRPCHandlerFixture(t)
	f.accountSvc.getAccountFn = func(id uint64) (*model.Account, error) {
		return &model.Account{ID: id, OwnerID: 9, AccountNumber: "111"}, nil
	}
	// Employee acting on-behalf of client 9 → may touch client 9's account.
	ok := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7, OnBehalfClientID: 9})
	_, err := h.GetAccount(ok, &pb.GetAccountRequest{Id: 1})
	require.NoError(t, err)
	// On-behalf of a DIFFERENT client → 404.
	bad := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7, OnBehalfClientID: 8})
	_, err = h.GetAccount(bad, &pb.GetAccountRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
