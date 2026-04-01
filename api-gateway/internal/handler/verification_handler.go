package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	notificationpb "github.com/exbanka/contract/notificationpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

type VerificationHandler struct {
	verificationClient verificationpb.VerificationGRPCServiceClient
	notificationClient notificationpb.NotificationServiceClient
}

func NewVerificationHandler(
	vc verificationpb.VerificationGRPCServiceClient,
	nc notificationpb.NotificationServiceClient,
) *VerificationHandler {
	return &VerificationHandler{verificationClient: vc, notificationClient: nc}
}

type createVerificationReq struct {
	SourceService string `json:"source_service" binding:"required"`
	SourceID      uint64 `json:"source_id" binding:"required"`
	Method        string `json:"method"`
}

// @Summary Create verification challenge
// @Tags verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body createVerificationReq true "Challenge data"
// @Success 200 {object} map[string]interface{}
// @Router /api/verifications [post]
func (h *VerificationHandler) CreateVerification(c *gin.Context) {
	var req createVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	if req.Method == "" {
		req.Method = "code_pull"
	}
	method, err := oneOf("method", req.Method, "code_pull", "email")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")
	deviceIDStr := ""
	if deviceID != nil {
		deviceIDStr = deviceID.(string)
	}

	resp, err := h.verificationClient.CreateChallenge(c.Request.Context(), &verificationpb.CreateChallengeRequest{
		UserId:        uint64(userID),
		SourceService: req.SourceService,
		SourceId:      req.SourceID,
		Method:        method,
		DeviceId:      deviceIDStr,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"challenge_id":   resp.ChallengeId,
		"challenge_data": resp.ChallengeData,
		"expires_at":     resp.ExpiresAt,
	})
}

// @Summary Get verification challenge status
// @Tags verifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Challenge ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/verifications/{id}/status [get]
func (h *VerificationHandler) GetVerificationStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	resp, err := h.verificationClient.GetChallengeStatus(c.Request.Context(), &verificationpb.GetChallengeStatusRequest{
		ChallengeId: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      resp.Status,
		"method":      resp.Method,
		"verified_at": resp.VerifiedAt,
		"expires_at":  resp.ExpiresAt,
	})
}

type submitCodeReq struct {
	Code string `json:"code" binding:"required"`
}

// @Summary Submit verification code (browser, code_pull method)
// @Tags verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Challenge ID"
// @Param body body submitCodeReq true "Verification code"
// @Success 200 {object} map[string]interface{}
// @Router /api/verifications/{id}/code [post]
func (h *VerificationHandler) SubmitVerificationCode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	var req submitCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	resp, err := h.verificationClient.SubmitCode(c.Request.Context(), &verificationpb.SubmitCodeRequest{
		ChallengeId: id,
		Code:        req.Code,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            resp.Success,
		"remaining_attempts": resp.RemainingAttempts,
	})
}

// @Summary Get pending mobile verifications (mobile polling)
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/mobile/verifications/pending [get]
func (h *VerificationHandler) GetPendingVerifications(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.notificationClient.GetPendingMobileItems(c.Request.Context(), &notificationpb.GetPendingMobileRequest{
		UserId:   uint64(userID),
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]gin.H, len(resp.Items))
	for i, item := range resp.Items {
		items[i] = gin.H{
			"id":           item.Id,
			"challenge_id": item.ChallengeId,
			"method":       item.Method,
			"display_data": item.DisplayData,
			"expires_at":   item.ExpiresAt,
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type submitMobileVerificationReq struct {
	Response string `json:"response" binding:"required"`
}

// @Summary Submit mobile verification response
// @Tags mobile-verifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Param body body submitMobileVerificationReq true "Verification response"
// @Success 200 {object} map[string]interface{}
// @Router /api/mobile/verifications/{challenge_id}/submit [post]
func (h *VerificationHandler) SubmitMobileVerification(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("challenge_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	var req submitMobileVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	deviceID, _ := c.Get("device_id")
	resp, err := h.verificationClient.SubmitVerification(c.Request.Context(), &verificationpb.SubmitVerificationRequest{
		ChallengeId: challengeID,
		DeviceId:    deviceID.(string),
		Response:    req.Response,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":            resp.Success,
		"remaining_attempts": resp.RemainingAttempts,
	})
}

// @Summary QR code verification (mobile scans QR)
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Param token query string true "QR verification token"
// @Success 200 {object} map[string]interface{}
// @Router /api/verify/{challenge_id} [post]
func (h *VerificationHandler) VerifyQR(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("challenge_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	token := c.Query("token")
	if token == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "token query parameter required")
		return
	}

	deviceID, _ := c.Get("device_id")
	resp, err := h.verificationClient.SubmitVerification(c.Request.Context(), &verificationpb.SubmitVerificationRequest{
		ChallengeId: challengeID,
		DeviceId:    deviceID.(string),
		Response:    token,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}
