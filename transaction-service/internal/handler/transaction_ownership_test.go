package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/identity"
	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
)

func ctxAs(c identity.Caller) context.Context {
	md, _ := metadata.FromOutgoingContext(identity.Inject(context.Background(), c))
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestOWN1_GetPayment_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.getPaymentFn = func(id uint64) (*model.Payment, error) {
			return &model.Payment{ID: id, ClientID: 9}, nil
		}
	}, nil, nil)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetPayment(ctx, &pb.GetPaymentRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetPayment_ClientOwn_OK(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.getPaymentFn = func(id uint64) (*model.Payment, error) {
			return &model.Payment{ID: id, ClientID: 5}, nil
		}
	}, nil, nil)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetPayment(ctx, &pb.GetPaymentRequest{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_GetPayment_Employee_OK(t *testing.T) {
	h := newTestHandler(func(pm *mockPaymentFacade) {
		pm.getPaymentFn = func(id uint64) (*model.Payment, error) {
			return &model.Payment{ID: id, ClientID: 9}, nil
		}
	}, nil, nil)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7})
	_, err := h.GetPayment(ctx, &pb.GetPaymentRequest{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_GetTransfer_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.getTransferFn = func(id uint64) (*model.Transfer, error) {
			return &model.Transfer{ID: id, ClientID: 9}, nil
		}
	}, nil)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetTransfer(ctx, &pb.GetTransferRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetTransferStatus_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(nil, func(tm *mockTransferFacade) {
		tm.getTransferFn = func(id uint64) (*model.Transfer, error) {
			return &model.Transfer{ID: id, ClientID: 9}, nil
		}
	}, nil)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetTransferStatus(ctx, &pb.GetTransferRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// foreignRecipient stubs the recipient facade to return a recipient owned by
// client 9 for any id — the cross-tenant fixture for the OWN-1 recipient tests.
func foreignRecipient(rm *mockRecipientFacade) {
	rm.getByIDFn = func(id uint64) (*model.PaymentRecipient, error) {
		return &model.PaymentRecipient{ID: id, ClientID: 9}, nil
	}
	rm.updateFn = func(id uint64, _, _ *string) (*model.PaymentRecipient, error) {
		return &model.PaymentRecipient{ID: id, ClientID: 9}, nil
	}
	rm.deleteFn = func(uint64) error { return nil }
}

func TestOWN1_GetPaymentRecipient_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(nil, nil, foreignRecipient)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetPaymentRecipient(ctx, &pb.GetPaymentRecipientRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetPaymentRecipient_ClientOwn_OK(t *testing.T) {
	h := newTestHandler(nil, nil, func(rm *mockRecipientFacade) {
		rm.getByIDFn = func(id uint64) (*model.PaymentRecipient, error) {
			return &model.PaymentRecipient{ID: id, ClientID: 5}, nil
		}
	})
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetPaymentRecipient(ctx, &pb.GetPaymentRecipientRequest{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_UpdatePaymentRecipient_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(nil, nil, foreignRecipient)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.UpdatePaymentRecipient(ctx, &pb.UpdatePaymentRecipientRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_DeletePaymentRecipient_ClientForeign_NotFound(t *testing.T) {
	h := newTestHandler(nil, nil, foreignRecipient)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.DeletePaymentRecipient(ctx, &pb.DeletePaymentRecipientRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_DeletePaymentRecipient_Employee_OK(t *testing.T) {
	h := newTestHandler(nil, nil, foreignRecipient)
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7})
	_, err := h.DeletePaymentRecipient(ctx, &pb.DeletePaymentRecipientRequest{Id: 1})
	require.NoError(t, err)
}
