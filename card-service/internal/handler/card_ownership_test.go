package handler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/exbanka/card-service/internal/model"
	pb "github.com/exbanka/contract/cardpb"
	"github.com/exbanka/contract/identity"
)

func ctxAs(c identity.Caller) context.Context {
	md, _ := metadata.FromOutgoingContext(identity.Inject(context.Background(), c))
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestOWN1_GetCard_ClientForeign_NotFound(t *testing.T) {
	h := &CardGRPCHandler{cardService: &stubCardService{
		getCardFn: func(id uint64) (*model.Card, error) {
			return &model.Card{ID: id, OwnerID: 9, OwnerType: "client"}, nil
		},
	}}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetCard(ctx, &pb.GetCardRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestOWN1_GetCard_ClientOwn_OK(t *testing.T) {
	h := &CardGRPCHandler{cardService: &stubCardService{
		getCardFn: func(id uint64) (*model.Card, error) {
			return &model.Card{ID: id, OwnerID: 5, OwnerType: "client"}, nil
		},
	}}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.GetCard(ctx, &pb.GetCardRequest{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_GetCard_Employee_OK(t *testing.T) {
	h := &CardGRPCHandler{cardService: &stubCardService{
		getCardFn: func(id uint64) (*model.Card, error) {
			return &model.Card{ID: id, OwnerID: 9, OwnerType: "client"}, nil
		},
	}}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalEmployee, PrincipalID: 7})
	_, err := h.GetCard(ctx, &pb.GetCardRequest{Id: 1})
	require.NoError(t, err)
}

func TestOWN1_ListCardsByClient_ClientForeign_Forbidden(t *testing.T) {
	h := &CardGRPCHandler{cardService: &stubCardService{}}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.ListCardsByClient(ctx, &pb.ListCardsByClientRequest{ClientId: 9})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestOWN1_SetCardPin_ClientForeign_NotFound(t *testing.T) {
	h := &VirtualCardGRPCHandler{cardService: &stubCardService{
		getCardFn: func(id uint64) (*model.Card, error) {
			return &model.Card{ID: id, OwnerID: 9, OwnerType: "client"}, nil
		},
	}}
	ctx := ctxAs(identity.Caller{PrincipalType: identity.PrincipalClient, PrincipalID: 5})
	_, err := h.SetCardPin(ctx, &pb.SetCardPinRequest{Id: 1, Pin: "1234"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
