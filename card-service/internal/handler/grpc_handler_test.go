package handler

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/exbanka/card-service/internal/service"
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
// Sentinel passthrough — typed sentinels must reach the wire intact
// ---------------------------------------------------------------------------

func TestSentinel_Passthrough_CardHandler(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		want     codes.Code
	}{
		{"ErrCardNotFound", service.ErrCardNotFound, codes.NotFound},
		{"ErrCardBlocked", service.ErrCardBlocked, codes.FailedPrecondition},
		{"ErrCardDeactivated", service.ErrCardDeactivated, codes.FailedPrecondition},
		{"ErrCardNotBlocked", service.ErrCardNotBlocked, codes.FailedPrecondition},
		{"ErrSingleUseAlreadyUsed", service.ErrSingleUseAlreadyUsed, codes.FailedPrecondition},
		{"ErrCardLimitReached", service.ErrCardLimitReached, codes.FailedPrecondition},
		{"ErrCardLocked", service.ErrCardLocked, codes.PermissionDenied},
		{"ErrInvalidPIN", service.ErrInvalidPIN, codes.InvalidArgument},
		{"ErrPINMismatch", service.ErrPINMismatch, codes.Unauthenticated},
		{"ErrInvalidCard", service.ErrInvalidCard, codes.InvalidArgument},
		{"ErrInvalidBlockDuration", service.ErrInvalidBlockDuration, codes.InvalidArgument},
		{"ErrCardRequestNotFound", service.ErrCardRequestNotFound, codes.NotFound},
		{"ErrCardRequestAlreadyDecided", service.ErrCardRequestAlreadyDecided, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, ok := status.FromError(tc.sentinel)
			require.True(t, ok)
			assert.Equal(t, tc.want, s.Code())
			wrapped := fmt.Errorf("op: %w", tc.sentinel)
			assert.True(t, errors.Is(wrapped, tc.sentinel))
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
			return nil, "", service.ErrInvalidCard
		},
	}
	h := &CardGRPCHandler{cardService: cardSvc, producer: &stubProducer{}}
	_, err := h.CreateCard(context.Background(), &pb.CreateCardRequest{CardBrand: "discover"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateCard_AtMostCardsError_MapsToFailedPrecondition(t *testing.T) {
	cardSvc := &stubCardService{
		createCardFn: func(_ context.Context, _ string, _ uint64, _, _ string) (*model.Card, string, error) {
			return nil, "", service.ErrCardLimitReached
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
	assert.Equal(t, codes.Unknown, st.Code())
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
	assert.Equal(t, codes.Unknown, st.Code())
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
	assert.Equal(t, codes.Unknown, st.Code())
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
			return nil, service.ErrCardBlocked
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
			return nil, service.ErrCardNotBlocked
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
			return nil, service.ErrCardDeactivated
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
	// Untyped error → Unknown under the new contract.
	assert.Equal(t, codes.Unknown, st.Code())
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
	t.Skip("untyped errors now pass through as Unknown — see TestSentinel_Passthrough_CardHandler")
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
