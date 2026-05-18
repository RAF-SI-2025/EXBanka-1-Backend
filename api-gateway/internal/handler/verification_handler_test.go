package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	notificationpb "github.com/exbanka/contract/notificationpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

func verificationRouter(h *handler.VerificationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("principal_id", int64(42))
		c.Set("device_id", "dev-1")
	}
	r.POST("/api/v2/verifications", withCtx, h.CreateVerification)
	r.GET("/api/v2/verifications/:id/status", withCtx, h.GetVerificationStatus)
	r.POST("/api/v2/verifications/:id/code", withCtx, h.SubmitVerificationCode)
	r.GET("/api/v2/mobile/verifications/pending", withCtx, h.GetPendingVerifications)
	r.POST("/api/v2/mobile/verifications/:id/submit", withCtx, h.SubmitMobileVerification)
	r.POST("/api/v2/verify/:challenge_id", withCtx, h.VerifyQR)
	r.POST("/api/v2/mobile/verifications/:id/ack", withCtx, h.AckVerification)
	r.POST("/api/v2/mobile/verifications/:id/biometric", withCtx, h.BiometricVerify)
	return r
}

func TestVerification_Create_Success(t *testing.T) {
	st := &stubVerificationClient{
		createFn: func(req *verificationpb.CreateChallengeRequest) (*verificationpb.CreateChallengeResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, "tx-service", req.SourceService)
			require.Equal(t, uint64(7), req.SourceId)
			require.Equal(t, "code_pull", req.Method)
			return &verificationpb.CreateChallengeResponse{ChallengeId: 1}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"source_service":"tx-service","source_id":7}`
	req := httptest.NewRequest("POST", "/api/v2/verifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"challenge_id":1`)
}

func TestVerification_Create_BadBody(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/verifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_Create_BadMethod(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"source_service":"x","source_id":1,"method":"telepathy"}`
	req := httptest.NewRequest("POST", "/api/v2/verifications", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "method must be one of")
}

func TestVerification_GetStatus_Success(t *testing.T) {
	st := &stubVerificationClient{
		getStatusFn: func(req *verificationpb.GetChallengeStatusRequest) (*verificationpb.GetChallengeStatusResponse, error) {
			require.Equal(t, uint64(7), req.ChallengeId)
			return &verificationpb.GetChallengeStatusResponse{Status: "pending"}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/verifications/7/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_GetStatus_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/verifications/x/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_SubmitCode_Success(t *testing.T) {
	st := &stubVerificationClient{
		submitCodeFn: func(req *verificationpb.SubmitCodeRequest) (*verificationpb.SubmitCodeResponse, error) {
			require.Equal(t, uint64(7), req.ChallengeId)
			require.Equal(t, "12345", req.Code)
			return &verificationpb.SubmitCodeResponse{Success: true}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"code":"12345"}`
	req := httptest.NewRequest("POST", "/api/v2/verifications/7/code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_SubmitCode_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"code":"12345"}`
	req := httptest.NewRequest("POST", "/api/v2/verifications/x/code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_SubmitCode_BadBody(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/verifications/7/code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_GetPendingVerifications_Success(t *testing.T) {
	notif := &stubNotificationClient{
		pendingFn: func(req *notificationpb.GetPendingMobileRequest) (*notificationpb.PendingMobileResponse, error) {
			require.Equal(t, uint64(42), req.UserId)
			return &notificationpb.PendingMobileResponse{
				Items: []*notificationpb.MobileInboxEntry{{Id: 1, ChallengeId: 5}},
			}, nil
		},
	}
	h := handler.NewVerificationHandler(&stubVerificationClient{}, notif)
	r := verificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/mobile/verifications/pending", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"challenge_id":5`)
}

func TestVerification_GetPendingVerifications_GRPCError(t *testing.T) {
	notif := &stubNotificationClient{
		pendingFn: func(*notificationpb.GetPendingMobileRequest) (*notificationpb.PendingMobileResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := handler.NewVerificationHandler(&stubVerificationClient{}, notif)
	r := verificationRouter(h)
	req := httptest.NewRequest("GET", "/api/v2/mobile/verifications/pending", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVerification_SubmitMobile_Success(t *testing.T) {
	st := &stubVerificationClient{
		submitFn: func(req *verificationpb.SubmitVerificationRequest) (*verificationpb.SubmitVerificationResponse, error) {
			require.Equal(t, uint64(7), req.ChallengeId)
			require.Equal(t, "dev-1", req.DeviceId)
			require.Equal(t, "yes", req.Response)
			return &verificationpb.SubmitVerificationResponse{Success: true}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"response":"yes"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/7/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_SubmitMobile_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	body := `{"response":"yes"}`
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/x/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_VerifyQR_Success(t *testing.T) {
	st := &stubVerificationClient{
		submitFn: func(req *verificationpb.SubmitVerificationRequest) (*verificationpb.SubmitVerificationResponse, error) {
			require.Equal(t, "qr-tok", req.Response)
			return &verificationpb.SubmitVerificationResponse{Success: true}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/verify/7?token=qr-tok", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_VerifyQR_MissingToken(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/verify/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "token query parameter required")
}

func TestVerification_VerifyQR_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/verify/x?token=t", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_AckVerification_Success(t *testing.T) {
	notif := &stubNotificationClient{
		ackFn: func(req *notificationpb.AckMobileRequest) (*notificationpb.AckMobileResponse, error) {
			require.Equal(t, uint64(7), req.Id)
			return &notificationpb.AckMobileResponse{}, nil
		},
	}
	h := handler.NewVerificationHandler(&stubVerificationClient{}, notif)
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/7/ack", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_AckVerification_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/x/ack", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerification_BiometricVerify_Success(t *testing.T) {
	st := &stubVerificationClient{
		verifyByBioFn: func(req *verificationpb.VerifyByBiometricRequest) (*verificationpb.VerifyByBiometricResponse, error) {
			require.Equal(t, uint64(7), req.ChallengeId)
			require.Equal(t, uint64(42), req.UserId)
			require.Equal(t, "dev-1", req.DeviceId)
			return &verificationpb.VerifyByBiometricResponse{Success: true}, nil
		},
	}
	h := handler.NewVerificationHandler(st, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/7/biometric", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestVerification_BiometricVerify_BadID(t *testing.T) {
	h := handler.NewVerificationHandler(&stubVerificationClient{}, &stubNotificationClient{})
	r := verificationRouter(h)
	req := httptest.NewRequest("POST", "/api/v2/mobile/verifications/x/biometric", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
