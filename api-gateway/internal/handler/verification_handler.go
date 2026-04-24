package handler

import (
	"net/http"
	"strconv"

	notificationpb "github.com/exbanka/contract/notificationpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
	"github.com/gin-gonic/gin"
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
// @Router /api/v2/verifications [post]
func (h *VerificationHandler) CreateVerification(c *gin.Context) {
	var req createVerificationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	if req.Method == "" {
		req.Method = "code_pull"
	}
	method, err := oneOf("method", req.Method, "code_pull")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceIDStr := c.GetString("device_id")

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
// @Router /api/v2/verifications/{id}/status [get]
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
// @Router /api/v2/verifications/{id}/code [post]
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
// @Router /api/v2/mobile/verifications/pending [get]
func (h *VerificationHandler) GetPendingVerifications(c *gin.Context) {
	userID := c.GetInt64("user_id")

	resp, err := h.notificationClient.GetPendingMobileItems(c.Request.Context(), &notificationpb.GetPendingMobileRequest{
		UserId: uint64(userID),
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
// @Param id path int true "Challenge ID"
// @Param body body submitMobileVerificationReq true "Verification response"
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/verifications/{id}/submit [post]
func (h *VerificationHandler) SubmitMobileVerification(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("id"), 10, 64)
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
// @Router /api/v2/verify/{challenge_id} [post]
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

// @Summary Acknowledge a delivered mobile verification notification
// @Description Marks an inbox item as delivered so it no longer appears in future polls.
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param id path int true "Inbox item ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{} "Invalid item id"
// @Failure 404 {object} map[string]interface{} "Item not found or already delivered"
// @Router /api/v2/mobile/verifications/{id}/ack [post]
func (h *VerificationHandler) AckVerification(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid item id")
		return
	}

	_, err = h.notificationClient.AckMobileItem(c.Request.Context(), &notificationpb.AckMobileRequest{
		Id: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// @Summary Verify challenge using device biometrics
// @Description Verifies a pending challenge using the device's biometric authentication.
// @Description No request body needed - the device signature IS the proof of authentication.
// @Description Requires MobileAuthMiddleware + RequireDeviceSignature.
// @Tags mobile-verifications
// @Produce json
// @Security BearerAuth
// @Param challenge_id path int true "Challenge ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{} "Biometrics not enabled"
// @Failure 409 {object} map[string]interface{} "Challenge expired or already verified"
// @Router /api/v2/mobile/verifications/{id}/biometric [post]
func (h *VerificationHandler) BiometricVerify(c *gin.Context) {
	challengeID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid challenge id")
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.verificationClient.VerifyByBiometric(c.Request.Context(), &verificationpb.VerifyByBiometricRequest{
		ChallengeId: challengeID,
		UserId:      uint64(userID),
		DeviceId:    deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}
