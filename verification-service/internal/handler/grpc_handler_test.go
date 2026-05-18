package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/datatypes"

	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/service"
)

// newHandlerForTest wires a stub service into VerificationGRPCHandler. It is
// unexported on purpose: production code must continue to use NewVerificationGRPCHandler.
func newHandlerForTest(svc verificationService) *VerificationGRPCHandler {
	return &VerificationGRPCHandler{svc: svc}
}

// stubService is a hand-written stub satisfying verificationService.
type stubService struct {
	createFn  func(ctx context.Context, userID uint64, src string, srcID uint64, method string, deviceID string) (*model.VerificationChallenge, error)
	statusFn  func(challengeID uint64) (*model.VerificationChallenge, error)
	pendingFn func(userID uint64) (*model.VerificationChallenge, error)
	submitFn  func(ctx context.Context, challengeID uint64, deviceID string, response string) (bool, int, string, error)
	codeFn    func(ctx context.Context, challengeID uint64, code string) (bool, int, error)
	bioFn     func(ctx context.Context, challengeID uint64, userID uint64, deviceID string) error
}

func (s *stubService) CreateChallenge(ctx context.Context, userID uint64, src string, srcID uint64, method string, deviceID string) (*model.VerificationChallenge, error) {
	return s.createFn(ctx, userID, src, srcID, method, deviceID)
}
func (s *stubService) GetChallengeStatus(challengeID uint64) (*model.VerificationChallenge, error) {
	return s.statusFn(challengeID)
}
func (s *stubService) GetPendingChallenge(userID uint64) (*model.VerificationChallenge, error) {
	return s.pendingFn(userID)
}
func (s *stubService) SubmitVerification(ctx context.Context, challengeID uint64, deviceID string, response string) (bool, int, string, error) {
	return s.submitFn(ctx, challengeID, deviceID, response)
}
func (s *stubService) SubmitCode(ctx context.Context, challengeID uint64, code string) (bool, int, error) {
	return s.codeFn(ctx, challengeID, code)
}
func (s *stubService) VerifyByBiometric(ctx context.Context, challengeID uint64, userID uint64, deviceID string) error {
	return s.bioFn(ctx, challengeID, userID, deviceID)
}

// ---------------------------------------------------------------------------
// Sentinel passthrough sanity check
// ---------------------------------------------------------------------------

func TestSentinels_PassthroughCodes(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		code     codes.Code
	}{
		{"ChallengeNotFound", service.ErrChallengeNotFound, codes.NotFound},
		{"InvalidMethod", service.ErrInvalidMethod, codes.InvalidArgument},
		{"InvalidArguments", service.ErrInvalidArguments, codes.InvalidArgument},
		{"ChallengeExpired", service.ErrChallengeExpired, codes.FailedPrecondition},
		{"TooManyAttempts", service.ErrTooManyAttempts, codes.FailedPrecondition},
		{"DeviceMismatch", service.ErrDeviceMismatch, codes.FailedPrecondition},
		{"MethodMismatch", service.ErrMethodMismatch, codes.FailedPrecondition},
		{"ChallengeOwnership", service.ErrChallengeOwnership, codes.PermissionDenied},
		{"BiometricsDisabled", service.ErrBiometricsDisabled, codes.PermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Op: %w", c.sentinel)
			require.True(t, errors.Is(wrapped, c.sentinel))
			assert.Equal(t, c.code, status.Code(wrapped))
		})
	}
}

// ---------------------------------------------------------------------------
// CreateChallenge
// ---------------------------------------------------------------------------

func TestHandlerCreateChallenge_CodePull_Success(t *testing.T) {
	now := time.Now()
	stub := &stubService{
		createFn: func(_ context.Context, userID uint64, src string, srcID uint64, method string, deviceID string) (*model.VerificationChallenge, error) {
			assert.Equal(t, uint64(7), userID)
			assert.Equal(t, "transaction", src)
			assert.Equal(t, uint64(99), srcID)
			assert.Equal(t, "code_pull", method)
			assert.Equal(t, "device-A", deviceID)
			return &model.VerificationChallenge{
				ID:            1234,
				Method:        "code_pull",
				ChallengeData: datatypes.JSON([]byte("{}")),
				ExpiresAt:     now.Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.CreateChallenge(context.Background(), &pb.CreateChallengeRequest{
		UserId: 7, SourceService: "transaction", SourceId: 99, Method: "code_pull", DeviceId: "device-A",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(1234), resp.ChallengeId)
	assert.Equal(t, "code_pull", resp.Method)
	assert.NotEmpty(t, resp.ExpiresAt)
	assert.Equal(t, "{}", resp.ChallengeData)
}

func TestHandlerCreateChallenge_QRScan_AddsVerifyURL(t *testing.T) {
	stub := &stubService{
		createFn: func(_ context.Context, _ uint64, _ string, _ uint64, _ string, _ string) (*model.VerificationChallenge, error) {
			data, _ := json.Marshal(map[string]interface{}{"token": "abcd"})
			return &model.VerificationChallenge{
				ID:            55,
				Method:        "qr_scan",
				ChallengeData: datatypes.JSON(data),
				ExpiresAt:     time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.CreateChallenge(context.Background(), &pb.CreateChallengeRequest{
		UserId: 1, SourceService: "transaction", SourceId: 1, Method: "qr_scan",
	})
	require.NoError(t, err)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.ChallengeData), &data))
	assert.Equal(t, "abcd", data["token"])
	verifyURL, ok := data["verify_url"].(string)
	assert.True(t, ok)
	assert.Contains(t, verifyURL, "/api/verify/55")
	assert.Contains(t, verifyURL, "token=abcd")
}

func TestHandlerCreateChallenge_NumberMatch_StripsOptions(t *testing.T) {
	stub := &stubService{
		createFn: func(_ context.Context, _ uint64, _ string, _ uint64, _ string, _ string) (*model.VerificationChallenge, error) {
			data, _ := json.Marshal(map[string]interface{}{
				"target":  42,
				"options": []int{1, 42, 7, 8, 9},
			})
			return &model.VerificationChallenge{
				ID:            6,
				Method:        "number_match",
				ChallengeData: datatypes.JSON(data),
				ExpiresAt:     time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.CreateChallenge(context.Background(), &pb.CreateChallengeRequest{
		UserId: 1, SourceService: "transaction", SourceId: 1, Method: "number_match",
	})
	require.NoError(t, err)
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.ChallengeData), &data))
	_, hasTarget := data["target"]
	assert.True(t, hasTarget, "browser response should include target")
	_, hasOptions := data["options"]
	assert.False(t, hasOptions, "options leaked into browser response")
}

func TestHandlerCreateChallenge_InvalidMethod_MapsToInvalidArgument(t *testing.T) {
	stub := &stubService{
		createFn: func(context.Context, uint64, string, uint64, string, string) (*model.VerificationChallenge, error) {
			return nil, fmt.Errorf("invalid verification method foo: %w", service.ErrInvalidMethod)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.CreateChallenge(context.Background(), &pb.CreateChallengeRequest{Method: "foo"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrInvalidMethod))
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetChallengeStatus
// ---------------------------------------------------------------------------

func TestHandlerGetChallengeStatus_Success(t *testing.T) {
	verifiedAt := time.Now()
	stub := &stubService{
		statusFn: func(challengeID uint64) (*model.VerificationChallenge, error) {
			assert.Equal(t, uint64(11), challengeID)
			return &model.VerificationChallenge{
				ID:         11,
				Status:     "verified",
				Method:     "code_pull",
				ExpiresAt:  time.Now().Add(5 * time.Minute),
				VerifiedAt: &verifiedAt,
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.GetChallengeStatus(context.Background(), &pb.GetChallengeStatusRequest{ChallengeId: 11})
	require.NoError(t, err)
	assert.Equal(t, uint64(11), resp.ChallengeId)
	assert.Equal(t, "verified", resp.Status)
	assert.Equal(t, "code_pull", resp.Method)
	assert.NotEmpty(t, resp.VerifiedAt)
}

func TestHandlerGetChallengeStatus_NotFound(t *testing.T) {
	stub := &stubService{
		statusFn: func(uint64) (*model.VerificationChallenge, error) {
			return nil, fmt.Errorf("challenge not found: %w", service.ErrChallengeNotFound)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.GetChallengeStatus(context.Background(), &pb.GetChallengeStatusRequest{ChallengeId: 999})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrChallengeNotFound))
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandlerGetChallengeStatus_PendingNoVerifiedAt(t *testing.T) {
	stub := &stubService{
		statusFn: func(uint64) (*model.VerificationChallenge, error) {
			return &model.VerificationChallenge{
				ID:        2,
				Status:    "pending",
				Method:    "code_pull",
				ExpiresAt: time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)
	resp, err := h.GetChallengeStatus(context.Background(), &pb.GetChallengeStatusRequest{ChallengeId: 2})
	require.NoError(t, err)
	assert.Empty(t, resp.VerifiedAt)
}

// ---------------------------------------------------------------------------
// GetPendingChallenge
// ---------------------------------------------------------------------------

func TestHandlerGetPendingChallenge_NotFoundReturnsFoundFalse(t *testing.T) {
	stub := &stubService{
		pendingFn: func(uint64) (*model.VerificationChallenge, error) {
			return nil, errors.New("not found")
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.GetPendingChallenge(context.Background(), &pb.GetPendingChallengeRequest{UserId: 1})
	require.NoError(t, err, "handler must convert not-found into Found=false, not gRPC error")
	assert.False(t, resp.Found)
}

func TestHandlerGetPendingChallenge_CodePull_IncludesCode(t *testing.T) {
	stub := &stubService{
		pendingFn: func(uint64) (*model.VerificationChallenge, error) {
			return &model.VerificationChallenge{
				ID:            17,
				Method:        "code_pull",
				Code:          "424242",
				ChallengeData: datatypes.JSON([]byte("{}")),
				ExpiresAt:     time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.GetPendingChallenge(context.Background(), &pb.GetPendingChallengeRequest{UserId: 1})
	require.NoError(t, err)
	assert.True(t, resp.Found)
	assert.Equal(t, uint64(17), resp.ChallengeId)
	assert.Equal(t, "code_pull", resp.Method)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.ChallengeData), &data))
	assert.Equal(t, "424242", data["code"])
}

func TestHandlerGetPendingChallenge_NumberMatch_OptionsOnly(t *testing.T) {
	chal, _ := json.Marshal(map[string]interface{}{
		"target":  42,
		"options": []int{1, 42, 7, 8, 9},
	})
	stub := &stubService{
		pendingFn: func(uint64) (*model.VerificationChallenge, error) {
			return &model.VerificationChallenge{
				ID:            18,
				Method:        "number_match",
				ChallengeData: datatypes.JSON(chal),
				ExpiresAt:     time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.GetPendingChallenge(context.Background(), &pb.GetPendingChallengeRequest{UserId: 1})
	require.NoError(t, err)
	assert.True(t, resp.Found)

	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.ChallengeData), &data))
	_, hasOptions := data["options"]
	assert.True(t, hasOptions, "mobile must see options")
	_, hasTarget := data["target"]
	assert.False(t, hasTarget, "mobile must NOT see target")
}

func TestHandlerGetPendingChallenge_QRScan_EmptyData(t *testing.T) {
	stub := &stubService{
		pendingFn: func(uint64) (*model.VerificationChallenge, error) {
			data, _ := json.Marshal(map[string]interface{}{"token": "abcd"})
			return &model.VerificationChallenge{
				ID:            19,
				Method:        "qr_scan",
				ChallengeData: datatypes.JSON(data),
				ExpiresAt:     time.Now().Add(5 * time.Minute),
			}, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.GetPendingChallenge(context.Background(), &pb.GetPendingChallengeRequest{UserId: 1})
	require.NoError(t, err)
	assert.True(t, resp.Found)

	// qr_scan returns empty display data — phone scans QR from browser.
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(resp.ChallengeData), &data))
	assert.Empty(t, data, "qr_scan must not leak token to mobile display data")
}

// ---------------------------------------------------------------------------
// SubmitVerification
// ---------------------------------------------------------------------------

func TestHandlerSubmitVerification_Success(t *testing.T) {
	stub := &stubService{
		submitFn: func(_ context.Context, id uint64, dev string, resp string) (bool, int, string, error) {
			assert.Equal(t, uint64(101), id)
			assert.Equal(t, "device-X", dev)
			assert.Equal(t, "424242", resp)
			return true, 2, "", nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.SubmitVerification(context.Background(), &pb.SubmitVerificationRequest{
		ChallengeId: 101, DeviceId: "device-X", Response: "424242",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, int32(2), resp.RemainingAttempts)
	assert.Empty(t, resp.NewChallengeData)
}

func TestHandlerSubmitVerification_FailedReturnsRemaining(t *testing.T) {
	stub := &stubService{
		submitFn: func(context.Context, uint64, string, string) (bool, int, string, error) {
			return false, 1, `{"options":[1,2,3]}`, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.SubmitVerification(context.Background(), &pb.SubmitVerificationRequest{ChallengeId: 1})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, int32(1), resp.RemainingAttempts)
	assert.NotEmpty(t, resp.NewChallengeData)
}

func TestHandlerSubmitVerification_DeviceMismatch_FailedPrecondition(t *testing.T) {
	stub := &stubService{
		submitFn: func(context.Context, uint64, string, string) (bool, int, string, error) {
			return false, 0, "", fmt.Errorf("challenge already bound to a different device: %w", service.ErrDeviceMismatch)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.SubmitVerification(context.Background(), &pb.SubmitVerificationRequest{ChallengeId: 1})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrDeviceMismatch))
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestHandlerSubmitVerification_Expired_FailedPrecondition(t *testing.T) {
	stub := &stubService{
		submitFn: func(context.Context, uint64, string, string) (bool, int, string, error) {
			return false, 0, "", fmt.Errorf("challenge has expired: %w", service.ErrChallengeExpired)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.SubmitVerification(context.Background(), &pb.SubmitVerificationRequest{ChallengeId: 1})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrChallengeExpired))
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// SubmitCode
// ---------------------------------------------------------------------------

func TestHandlerSubmitCode_Success(t *testing.T) {
	stub := &stubService{
		codeFn: func(_ context.Context, id uint64, code string) (bool, int, error) {
			assert.Equal(t, uint64(7), id)
			assert.Equal(t, "111111", code)
			return true, 2, nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.SubmitCode(context.Background(), &pb.SubmitCodeRequest{ChallengeId: 7, Code: "111111"})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, int32(2), resp.RemainingAttempts)
}

func TestHandlerSubmitCode_NotFound(t *testing.T) {
	stub := &stubService{
		codeFn: func(context.Context, uint64, string) (bool, int, error) {
			return false, 0, fmt.Errorf("challenge not found: %w", service.ErrChallengeNotFound)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.SubmitCode(context.Background(), &pb.SubmitCodeRequest{ChallengeId: 7, Code: "111111"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrChallengeNotFound))
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandlerSubmitCode_OnlyAllowedForCodePull_FailedPrecondition(t *testing.T) {
	stub := &stubService{
		codeFn: func(context.Context, uint64, string) (bool, int, error) {
			return false, 0, fmt.Errorf("code submission is only allowed for code_pull method: %w", service.ErrMethodMismatch)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.SubmitCode(context.Background(), &pb.SubmitCodeRequest{ChallengeId: 7, Code: "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrMethodMismatch))
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// VerifyByBiometric
// ---------------------------------------------------------------------------

func TestHandlerVerifyByBiometric_Success(t *testing.T) {
	stub := &stubService{
		bioFn: func(_ context.Context, id uint64, userID uint64, dev string) error {
			assert.Equal(t, uint64(8), id)
			assert.Equal(t, uint64(2), userID)
			assert.Equal(t, "device-Z", dev)
			return nil
		},
	}
	h := newHandlerForTest(stub)

	resp, err := h.VerifyByBiometric(context.Background(), &pb.VerifyByBiometricRequest{
		ChallengeId: 8, UserId: 2, DeviceId: "device-Z",
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
}

func TestHandlerVerifyByBiometric_OwnershipDenied(t *testing.T) {
	stub := &stubService{
		bioFn: func(context.Context, uint64, uint64, string) error {
			return fmt.Errorf("challenge does not belong to this user: %w", service.ErrChallengeOwnership)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.VerifyByBiometric(context.Background(), &pb.VerifyByBiometricRequest{ChallengeId: 1, UserId: 9})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrChallengeOwnership))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandlerVerifyByBiometric_BiometricsDisabled(t *testing.T) {
	stub := &stubService{
		bioFn: func(context.Context, uint64, uint64, string) error {
			return fmt.Errorf("biometrics not enabled for this device: %w", service.ErrBiometricsDisabled)
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.VerifyByBiometric(context.Background(), &pb.VerifyByBiometricRequest{ChallengeId: 1, UserId: 1})
	require.Error(t, err)
	assert.True(t, errors.Is(err, service.ErrBiometricsDisabled))
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandlerVerifyByBiometric_Internal(t *testing.T) {
	// A bare error from the service surfaces as codes.Unknown (no GRPCStatus
	// embedded). Either Unknown or Internal is acceptable as a "non-business"
	// error code; we check it's not OK and the original message propagates.
	stub := &stubService{
		bioFn: func(context.Context, uint64, uint64, string) error {
			return errors.New("oops db connection lost")
		},
	}
	h := newHandlerForTest(stub)

	_, err := h.VerifyByBiometric(context.Background(), &pb.VerifyByBiometricRequest{ChallengeId: 1, UserId: 1})
	require.Error(t, err)
	// status message should also propagate the original error string.
	assert.Contains(t, err.Error(), "oops")
}
