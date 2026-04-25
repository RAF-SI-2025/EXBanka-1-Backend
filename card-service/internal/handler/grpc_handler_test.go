package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	pb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
)

// sampleCard returns a fully populated *model.Card useful as a stub return.
func sampleCard() *model.Card {
	return &model.Card{
		ID:             7,
		CardNumber:     "4111XXXXXXXX1234",
		CardNumberFull: "4111111111111234",
		CVV:            "123",
		CardType:       "debit",
		CardBrand:      "visa",
		AccountNumber:  "265000000000000001",
		OwnerID:        42,
		OwnerType:      "client",
		Status:         "active",
		CardLimit:      decimal.NewFromInt(1000000),
		ExpiresAt:      time.Now().AddDate(3, 0, 0),
		CreatedAt:      time.Now(),
	}
}

// ---------------------------------------------------------------------------
// mapServiceError
// ---------------------------------------------------------------------------

func TestMapServiceError_Categories(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not found", errors.New("card 1 not found"), codes.NotFound},
		{"must be", errors.New("usage_type must be one of: single_use"), codes.InvalidArgument},
		{"invalid", errors.New("invalid card brand: discover"), codes.InvalidArgument},
		{"already exists", errors.New("client already exists"), codes.AlreadyExists},
		{"already blocked", errors.New("card 1 is already blocked"), codes.FailedPrecondition},
		{"already deactivated", errors.New("card 1 is already deactivated"), codes.FailedPrecondition},
		{"is not blocked", errors.New("card 1 is not blocked"), codes.FailedPrecondition},
		{"no remaining uses", errors.New("virtual card has no remaining uses"), codes.FailedPrecondition},
		{"at most cards", errors.New("personal accounts can have at most 2 cards"), codes.FailedPrecondition},
		{"too many failed", errors.New("too many failed PIN attempts"), codes.ResourceExhausted},
		{"permission", errors.New("permission denied"), codes.PermissionDenied},
		{"default", errors.New("some random failure"), codes.Internal},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := mapServiceError(tc.err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// CreateCard
// ---------------------------------------------------------------------------

func TestCreateCard_Success_PublishesEvents(t *testing.T) {
	cardSvc := &stubCardService{
		createCardFn: func(_ context.Context, _ string, _ uint64, _, _ string) (*model.Card, string, error) {
			return sampleCard(), "999", nil
		},
	}
	prod := &stubProducer{}
	h := &CardGRPCHandler{cardService: cardSvc, producer: prod}

	resp, err := h.CreateCard(context.Background(), &pb.CreateCardRequest{
		AccountNumber: "265000000000000001",
		OwnerId:       42,
		OwnerType:     "client",
		CardBrand:     "visa",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.Id)
	assert.Equal(t, "999", resp.Cvv)
	assert.Equal(t, 1, prod.cardCreatedCount, "card.created event must be published")
	assert.Equal(t, 1, prod.generalNotificationCount, "general notification must be published")
}

func TestCreateCard_ServiceError_MapsToInvalidArgument(t *testing.T) {
	cardSvc := &stubCardService{
		createCardFn: func(_ context.Context, _ string, _ uint64, _, _ string) (*model.Card, string, error) {
			return nil, "", errors.New("card brand must be one of: visa, mastercard, dinacard, amex; got: discover")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.CreateCard(context.Background(), &pb.CreateCardRequest{CardBrand: "discover"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "failed to create card")
}

func TestCreateCard_AtMostCardsError_MapsToFailedPrecondition(t *testing.T) {
	cardSvc := &stubCardService{
		createCardFn: func(_ context.Context, _ string, _ uint64, _, _ string) (*model.Card, string, error) {
			return nil, "", errors.New("personal accounts can have at most 2 cards; account X already has 2")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.CreateCard(context.Background(), &pb.CreateCardRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ---------------------------------------------------------------------------
// GetCard
// ---------------------------------------------------------------------------

func TestGetCard_Found(t *testing.T) {
	cardSvc := &stubCardService{
		getCardFn: func(_ uint64) (*model.Card, error) { return sampleCard(), nil },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	resp, err := h.GetCard(context.Background(), &pb.GetCardRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.Id)
	assert.Equal(t, "visa", resp.CardBrand)
}

func TestGetCard_NotFound_RecordNotFound(t *testing.T) {
	cardSvc := &stubCardService{
		getCardFn: func(_ uint64) (*model.Card, error) { return nil, gorm.ErrRecordNotFound },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.GetCard(context.Background(), &pb.GetCardRequest{Id: 999})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "card not found", st.Message())
}

func TestGetCard_GenericError_MapsViaMapServiceError(t *testing.T) {
	cardSvc := &stubCardService{
		getCardFn: func(_ uint64) (*model.Card, error) { return nil, errors.New("unexpected db failure") },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.GetCard(context.Background(), &pb.GetCardRequest{Id: 1})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// ListCardsByAccount / ListCardsByClient
// ---------------------------------------------------------------------------

func TestListCardsByAccount_ReturnsAll(t *testing.T) {
	cards := []model.Card{*sampleCard(), {ID: 8, CardLimit: decimal.NewFromInt(500), ExpiresAt: time.Now()}}
	cardSvc := &stubCardService{
		listCardsByAccountFn: func(_ string) ([]model.Card, error) { return cards, nil },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	resp, err := h.ListCardsByAccount(context.Background(), &pb.ListCardsByAccountRequest{AccountNumber: "x"})
	require.NoError(t, err)
	assert.Len(t, resp.Cards, 2)
}

func TestListCardsByAccount_Error(t *testing.T) {
	cardSvc := &stubCardService{
		listCardsByAccountFn: func(_ string) ([]model.Card, error) { return nil, errors.New("boom") },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.ListCardsByAccount(context.Background(), &pb.ListCardsByAccountRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestListCardsByClient_ReturnsAll(t *testing.T) {
	cards := []model.Card{*sampleCard()}
	cardSvc := &stubCardService{
		listCardsByClientFn: func(_ uint64) ([]model.Card, error) { return cards, nil },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	resp, err := h.ListCardsByClient(context.Background(), &pb.ListCardsByClientRequest{ClientId: 42})
	require.NoError(t, err)
	assert.Len(t, resp.Cards, 1)
}

func TestListCardsByClient_Error(t *testing.T) {
	cardSvc := &stubCardService{
		listCardsByClientFn: func(_ uint64) ([]model.Card, error) { return nil, errors.New("boom") },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.ListCardsByClient(context.Background(), &pb.ListCardsByClientRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// BlockCard
// ---------------------------------------------------------------------------

func TestBlockCard_Success_PublishesEventsAndEmail(t *testing.T) {
	cardSvc := &stubCardService{
		blockCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			c := sampleCard()
			c.Status = "blocked"
			return c, nil
		},
	}
	prod := &stubProducer{}
	client := &stubClientClient{
		getClientFn: func(_ context.Context, _ *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
			return &clientpb.ClientResponse{Id: 42, Email: "owner@example.com"}, nil
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: prod, clientClient: client}

	resp, err := h.BlockCard(context.Background(), &pb.BlockCardRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, "blocked", resp.Status)
	assert.Equal(t, 1, prod.cardStatusChangedCount)
	assert.Equal(t, 1, prod.generalNotificationCount)
	assert.Equal(t, 1, prod.sendEmailCount)
}

func TestBlockCard_ClientFetchError_StillSucceeds(t *testing.T) {
	cardSvc := &stubCardService{
		blockCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			c := sampleCard()
			c.Status = "blocked"
			return c, nil
		},
	}
	prod := &stubProducer{}
	client := &stubClientClient{
		getClientFn: func(_ context.Context, _ *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
			return nil, errors.New("client-service unavailable")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: prod, clientClient: client}

	resp, err := h.BlockCard(context.Background(), &pb.BlockCardRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, "blocked", resp.Status)
	assert.Equal(t, 0, prod.sendEmailCount, "no email when client fetch fails")
}

func TestBlockCard_NotFound_RecordNotFound(t *testing.T) {
	cardSvc := &stubCardService{
		blockCardFn: func(_ uint64, _ int64) (*model.Card, error) { return nil, gorm.ErrRecordNotFound },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.BlockCard(context.Background(), &pb.BlockCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestBlockCard_AlreadyBlocked_FailedPrecondition(t *testing.T) {
	cardSvc := &stubCardService{
		blockCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			return nil, errors.New("card 7 is already blocked")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.BlockCard(context.Background(), &pb.BlockCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ---------------------------------------------------------------------------
// UnblockCard
// ---------------------------------------------------------------------------

func TestUnblockCard_Success(t *testing.T) {
	cardSvc := &stubCardService{
		unblockCardFn: func(_ uint64, _ int64) (*model.Card, error) { return sampleCard(), nil },
	}
	prod := &stubProducer{}
	client := &stubClientClient{}
	h := &CardGRPCHandler{cardService: cardSvc, producer: prod, clientClient: client}

	resp, err := h.UnblockCard(context.Background(), &pb.UnblockCardRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, "active", resp.Status)
	assert.Equal(t, 1, prod.cardStatusChangedCount)
	assert.Equal(t, 1, prod.sendEmailCount)
}

func TestUnblockCard_NotFound(t *testing.T) {
	cardSvc := &stubCardService{
		unblockCardFn: func(_ uint64, _ int64) (*model.Card, error) { return nil, gorm.ErrRecordNotFound },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.UnblockCard(context.Background(), &pb.UnblockCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestUnblockCard_NotBlocked_FailedPrecondition(t *testing.T) {
	cardSvc := &stubCardService{
		unblockCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			return nil, errors.New("card 7 is not blocked")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.UnblockCard(context.Background(), &pb.UnblockCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ---------------------------------------------------------------------------
// DeactivateCard
// ---------------------------------------------------------------------------

func TestDeactivateCard_Success(t *testing.T) {
	cardSvc := &stubCardService{
		deactivateCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			c := sampleCard()
			c.Status = "deactivated"
			return c, nil
		},
	}
	prod := &stubProducer{}
	client := &stubClientClient{}
	h := &CardGRPCHandler{cardService: cardSvc, producer: prod, clientClient: client}

	resp, err := h.DeactivateCard(context.Background(), &pb.DeactivateCardRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, "deactivated", resp.Status)
	assert.Equal(t, 1, prod.cardStatusChangedCount)
	assert.Equal(t, 1, prod.sendEmailCount)
}

func TestDeactivateCard_NotFound(t *testing.T) {
	cardSvc := &stubCardService{
		deactivateCardFn: func(_ uint64, _ int64) (*model.Card, error) { return nil, gorm.ErrRecordNotFound },
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.DeactivateCard(context.Background(), &pb.DeactivateCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestDeactivateCard_AlreadyDeactivated_FailedPrecondition(t *testing.T) {
	cardSvc := &stubCardService{
		deactivateCardFn: func(_ uint64, _ int64) (*model.Card, error) {
			return nil, errors.New("card 7 is already deactivated")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.DeactivateCard(context.Background(), &pb.DeactivateCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// ---------------------------------------------------------------------------
// CreateAuthorizedPerson / GetAuthorizedPerson
// ---------------------------------------------------------------------------

func TestCreateAuthorizedPerson_Success(t *testing.T) {
	cardSvc := &stubCardService{
		createAuthorizedPersonFn: func(_ context.Context, ap *model.AuthorizedPerson) error {
			ap.ID = 11
			ap.CreatedAt = time.Now()
			return nil
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	resp, err := h.CreateAuthorizedPerson(context.Background(), &pb.CreateAuthorizedPersonRequest{
		FirstName: "Alice", LastName: "Smith", Email: "a@b.c", AccountId: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(11), resp.Id)
	assert.Equal(t, "Alice", resp.FirstName)
}

func TestCreateAuthorizedPerson_Error(t *testing.T) {
	cardSvc := &stubCardService{
		createAuthorizedPersonFn: func(_ context.Context, _ *model.AuthorizedPerson) error {
			return errors.New("invalid email format")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.CreateAuthorizedPerson(context.Background(), &pb.CreateAuthorizedPersonRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetAuthorizedPerson_Found(t *testing.T) {
	cardSvc := &stubCardService{
		getAuthorizedPersonFn: func(id uint64) (*model.AuthorizedPerson, error) {
			return &model.AuthorizedPerson{
				ID: id, FirstName: "Bob", LastName: "Doe", Email: "b@c.d", CreatedAt: time.Now(),
			}, nil
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	resp, err := h.GetAuthorizedPerson(context.Background(), &pb.GetAuthorizedPersonRequest{Id: 5})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.Id)
	assert.Equal(t, "Bob", resp.FirstName)
}

func TestGetAuthorizedPerson_NotFound(t *testing.T) {
	cardSvc := &stubCardService{
		getAuthorizedPersonFn: func(_ uint64) (*model.AuthorizedPerson, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.GetAuthorizedPerson(context.Background(), &pb.GetAuthorizedPersonRequest{Id: 999})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "authorized person not found", st.Message())
}

func TestGetAuthorizedPerson_GenericError(t *testing.T) {
	cardSvc := &stubCardService{
		getAuthorizedPersonFn: func(_ uint64) (*model.AuthorizedPerson, error) {
			return nil, errors.New("db connection lost")
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.GetAuthorizedPerson(context.Background(), &pb.GetAuthorizedPersonRequest{Id: 1})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// maskCardNumber helper
// ---------------------------------------------------------------------------

func TestMaskCardNumber_LastFourDigits(t *testing.T) {
	assert.Equal(t, "1234", maskCardNumber("4111111111111234"))
	assert.Equal(t, "abc", maskCardNumber("abc"), "short string returned unchanged")
}
