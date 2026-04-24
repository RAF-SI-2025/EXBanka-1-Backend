package handler

import (
	"net/http"

	authpb "github.com/exbanka/contract/authpb"
	"github.com/gin-gonic/gin"
)

type MobileAuthHandler struct {
	authClient authpb.AuthServiceClient
}

func NewMobileAuthHandler(authClient authpb.AuthServiceClient) *MobileAuthHandler {
	return &MobileAuthHandler{authClient: authClient}
}

type requestActivationReq struct {
	Email string `json:"email" binding:"required,email"`
}

// @Summary Request mobile app activation code
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body requestActivationReq true "Email"
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/auth/request-activation [post]
func (h *MobileAuthHandler) RequestActivation(c *gin.Context) {
	var req requestActivationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	resp, err := h.authClient.RequestMobileActivation(c.Request.Context(), &authpb.MobileActivationRequest{
		Email: req.Email,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success, "message": resp.Message})
}

type activateDeviceReq struct {
	Email      string `json:"email" binding:"required,email"`
	Code       string `json:"code" binding:"required"`
	DeviceName string `json:"device_name" binding:"required"`
}

// @Summary Activate mobile device with code
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body activateDeviceReq true "Activation data"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /api/v2/mobile/auth/activate [post]
func (h *MobileAuthHandler) ActivateDevice(c *gin.Context) {
	var req activateDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if len(req.Code) != 6 {
		apiError(c, http.StatusBadRequest, ErrValidation, "code must be 6 digits")
		return
	}

	resp, err := h.authClient.ActivateMobileDevice(c.Request.Context(), &authpb.ActivateMobileDeviceRequest{
		Email:      req.Email,
		Code:       req.Code,
		DeviceName: req.DeviceName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
		"device_id":     resp.DeviceId,
		"device_secret": resp.DeviceSecret,
	})
}

type refreshMobileReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// @Summary Refresh mobile token
// @Tags mobile-auth
// @Accept json
// @Produce json
// @Param body body refreshMobileReq true "Refresh token"
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/auth/refresh [post]
func (h *MobileAuthHandler) RefreshMobileToken(c *gin.Context) {
	var req refreshMobileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	deviceID := c.GetHeader("X-Device-ID")
	if deviceID == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "X-Device-ID header required")
		return
	}

	resp, err := h.authClient.RefreshMobileToken(c.Request.Context(), &authpb.RefreshMobileTokenRequest{
		RefreshToken: req.RefreshToken,
		DeviceId:     deviceID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"access_token":  resp.AccessToken,
		"refresh_token": resp.RefreshToken,
	})
}

// @Summary Get current device info
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/device [get]
func (h *MobileAuthHandler) GetDeviceInfo(c *gin.Context) {
	userID := c.GetInt64("user_id")
	resp, err := h.authClient.GetDeviceInfo(c.Request.Context(), &authpb.GetDeviceInfoRequest{
		UserId: userID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"device_id":    resp.DeviceId,
		"device_name":  resp.DeviceName,
		"status":       resp.Status,
		"activated_at": resp.ActivatedAt,
		"last_seen_at": resp.LastSeenAt,
	})
}

// @Summary Deactivate current device
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/device/deactivate [post]
func (h *MobileAuthHandler) DeactivateDevice(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")
	resp, err := h.authClient.DeactivateDevice(c.Request.Context(), &authpb.DeactivateDeviceRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}

type transferDeviceReq struct {
	Email string `json:"email" binding:"required,email"`
}

// @Summary Transfer device (deactivate current + send new activation code)
// @Tags mobile-device
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/device/transfer [post]
func (h *MobileAuthHandler) TransferDevice(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req transferDeviceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.authClient.TransferDevice(c.Request.Context(), &authpb.TransferDeviceRequest{
		UserId: userID,
		Email:  req.Email,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success, "message": resp.Message})
}

type setBiometricsReq struct {
	Enabled bool `json:"enabled"`
}

// @Summary Enable or disable biometric verification for device
// @Tags mobile-device
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body setBiometricsReq true "Biometrics setting"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /api/v2/mobile/device/biometrics [post]
func (h *MobileAuthHandler) SetBiometrics(c *gin.Context) {
	var req setBiometricsReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.authClient.SetBiometricsEnabled(c.Request.Context(), &authpb.SetBiometricsRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
		Enabled:  req.Enabled,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}

// @Summary Get biometric verification status for device
// @Tags mobile-device
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /api/v2/mobile/device/biometrics [get]
func (h *MobileAuthHandler) GetBiometrics(c *gin.Context) {
	userID := c.GetInt64("user_id")
	deviceID, _ := c.Get("device_id")

	resp, err := h.authClient.GetBiometricsEnabled(c.Request.Context(), &authpb.GetBiometricsRequest{
		UserId:   userID,
		DeviceId: deviceID.(string),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"enabled": resp.Enabled})
}
