package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	pb "github.com/exbanka/contract/cardpb"
)

// ---------------------------------------------------------------------------
// CreateVirtualCard
// ---------------------------------------------------------------------------

func TestCreateVirtualCard_Success(t *testing.T) {
	cardSvc := &stubCardService{
		createVirtualCardFn: func(_ context.Context, _ string, _ uint64, _, _ string, _, _ int, _ string) (*model.Card, string, error) {
			c := sampleCard()
			c.IsVirtual = true
			c.UsageType = "single_use"
			return c, "456", nil
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.CreateVirtualCard(context.Background(), &pb.CreateVirtualCardRequest{
		AccountNumber: "265000000000000001",
		OwnerId:       42,
		CardBrand:     "visa",
		UsageType:     "single_use",
		MaxUses:       1,
		ExpiryMonths:  1,
		CardLimit:     "1000",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.Id)
	assert.Equal(t, "456", resp.Cvv)
}

func TestCreateVirtualCard_InvalidUsageType(t *testing.T) {
	cardSvc := &stubCardService{
		createVirtualCardFn: func(_ context.Context, _ string, _ uint64, _, _ string, _, _ int, _ string) (*model.Card, string, error) {
			return nil, "", errors.New("usage_type must be one of: single_use, multi_use; got: bogus")
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.CreateVirtualCard(context.Background(), &pb.CreateVirtualCardRequest{UsageType: "bogus"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---------------------------------------------------------------------------
// SetCardPin
// ---------------------------------------------------------------------------

func TestSetCardPin_Success(t *testing.T) {
	cardSvc := &stubCardService{
		setPinFn: func(_ uint64, _ string) error { return nil },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.SetCardPin(context.Background(), &pb.SetCardPinRequest{Id: 7, Pin: "1234"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "PIN set successfully", resp.Message)
}

func TestSetCardPin_InvalidFormat(t *testing.T) {
	cardSvc := &stubCardService{
		setPinFn: func(_ uint64, _ string) error { return errors.New("PIN must be exactly 4 digits") },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.SetCardPin(context.Background(), &pb.SetCardPinRequest{Id: 7, Pin: "abc"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "PIN must be exactly 4 digits")
}

// ---------------------------------------------------------------------------
// VerifyCardPin
// ---------------------------------------------------------------------------

func TestVerifyCardPin_Valid(t *testing.T) {
	cardSvc := &stubCardService{
		verifyPinFn: func(_ uint64, _ string) (bool, error) { return true, nil },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.VerifyCardPin(context.Background(), &pb.VerifyCardPinRequest{Id: 7, Pin: "1234"})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Equal(t, "PIN verified", resp.Message)
}

func TestVerifyCardPin_Invalid_NoError(t *testing.T) {
	cardSvc := &stubCardService{
		verifyPinFn: func(_ uint64, _ string) (bool, error) { return false, nil },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.VerifyCardPin(context.Background(), &pb.VerifyCardPinRequest{Id: 7, Pin: "0000"})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	assert.Equal(t, "incorrect PIN", resp.Message)
}

func TestVerifyCardPin_Locked(t *testing.T) {
	cardSvc := &stubCardService{
		verifyPinFn: func(_ uint64, _ string) (bool, error) {
			return false, errors.New("card blocked due to too many failed PIN attempts")
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.VerifyCardPin(context.Background(), &pb.VerifyCardPinRequest{Id: 7, Pin: "0000"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "too many failed PIN attempts")
}

// ---------------------------------------------------------------------------
// TemporaryBlockCard
// ---------------------------------------------------------------------------

func TestTemporaryBlockCard_Success(t *testing.T) {
	cardSvc := &stubCardService{
		temporaryBlockCardFn: func(_ context.Context, _ uint64, _ int, _ string) (*model.Card, error) {
			c := sampleCard()
			c.Status = "blocked"
			return c, nil
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.TemporaryBlockCard(context.Background(), &pb.TemporaryBlockCardRequest{
		Id: 7, DurationHours: 24, Reason: "lost",
	})
	require.NoError(t, err)
	assert.Equal(t, "blocked", resp.Status)
}

func TestTemporaryBlockCard_NotFound(t *testing.T) {
	cardSvc := &stubCardService{
		temporaryBlockCardFn: func(_ context.Context, _ uint64, _ int, _ string) (*model.Card, error) {
			return nil, gorm.ErrRecordNotFound
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.TemporaryBlockCard(context.Background(), &pb.TemporaryBlockCardRequest{Id: 7, DurationHours: 24})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestTemporaryBlockCard_InvalidDuration(t *testing.T) {
	cardSvc := &stubCardService{
		temporaryBlockCardFn: func(_ context.Context, _ uint64, _ int, _ string) (*model.Card, error) {
			return nil, errors.New("duration_hours must be between 1 and 720")
		},
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.TemporaryBlockCard(context.Background(), &pb.TemporaryBlockCardRequest{Id: 7, DurationHours: 0})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "duration_hours must be between 1 and 720")
}

// ---------------------------------------------------------------------------
// UseCard
// ---------------------------------------------------------------------------

func TestUseCard_Success(t *testing.T) {
	cardSvc := &stubCardService{
		useCardFn: func(_ uint64) error { return nil },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	resp, err := h.UseCard(context.Background(), &pb.UseCardRequest{Id: 7})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestUseCard_NoRemainingUses(t *testing.T) {
	cardSvc := &stubCardService{
		useCardFn: func(_ uint64) error { return errors.New("virtual card has no remaining uses") },
	}
	h := &VirtualCardGRPCHandler{cardService: cardSvc}
	_, err := h.UseCard(context.Background(), &pb.UseCardRequest{Id: 7})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "no remaining uses")
}
