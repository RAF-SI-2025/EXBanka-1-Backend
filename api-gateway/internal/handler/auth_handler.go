package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"

	authpb "github.com/exbanka/contract/authpb"
)

type AuthHandler struct {
	authClient authpb.AuthServiceClient
}

func NewAuthHandler(authClient authpb.AuthServiceClient) *AuthHandler {
	return &AuthHandler{authClient: authClient}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// Login godoc
// @Summary      User login
// @Description  Authenticate with email and password, returns access and refresh tokens
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  loginRequest  true  "Login credentials"
// @Success      200  {object}  map[string]string  "access_token, refresh_token"
// @Failure      400  {object}  map[string]string  "error message"
// @Failure      401  {object}  map[string]string  "invalid credentials"
// @Router       /api/v2/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// Forward client IP and User-Agent to auth-service via gRPC metadata
	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	resp, err := h.authClient.Login(ctx, &authpb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
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

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshToken godoc
// @Summary      Refresh access token
// @Description  Exchange a valid refresh token for a new access/refresh token pair
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  refreshRequest  true  "Refresh token"
// @Success      200  {object}  map[string]string  "access_token, refresh_token"
// @Failure      400  {object}  map[string]string  "error message"
// @Failure      401  {object}  map[string]string  "invalid refresh token"
// @Router       /api/v2/auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// Forward client IP and User-Agent to auth-service via gRPC metadata
	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	resp, err := h.authClient.RefreshToken(ctx, &authpb.RefreshTokenRequest{
		RefreshToken: req.RefreshToken,
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

type passwordResetRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// RequestPasswordReset godoc
// @Summary      Request password reset
// @Description  Send a password reset link to the user's email
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  passwordResetRequest  true  "Email address"
// @Success      200  {object}  map[string]string  "confirmation message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/v2/auth/password/reset-request [post]
func (h *AuthHandler) RequestPasswordReset(c *gin.Context) {
	var req passwordResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	_, _ = h.authClient.RequestPasswordReset(c.Request.Context(), &authpb.PasswordResetRequest{
		Email: req.Email,
	})
	c.JSON(http.StatusOK, gin.H{"message": "if the email exists, a reset link has been sent"})
}

type resetPasswordRequest struct {
	Token           string `json:"token" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

// ResetPassword godoc
// @Summary      Reset password
// @Description  Reset password using a token from the reset email link
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  resetPasswordRequest  true  "Reset token and new password"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/v2/auth/password/reset [post]
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.ResetPassword(c.Request.Context(), &authpb.ResetPasswordRequest{
		Token:           req.Token,
		NewPassword:     req.NewPassword,
		ConfirmPassword: req.ConfirmPassword,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}

type activateRequest struct {
	Token           string `json:"token" binding:"required"`
	Password        string `json:"password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

// ActivateAccount godoc
// @Summary      Activate account
// @Description  Activate a new account using the token from the activation email link
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  activateRequest  true  "Activation token and password"
// @Success      200  {object}  map[string]string  "success message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/v2/auth/activate [post]
func (h *AuthHandler) ActivateAccount(c *gin.Context) {
	var req activateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	_, err := h.authClient.ActivateAccount(c.Request.Context(), &authpb.ActivateAccountRequest{
		Token:           req.Token,
		Password:        req.Password,
		ConfirmPassword: req.ConfirmPassword,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "account activated successfully"})
}

type resendActivationRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// ResendActivationEmail godoc
// @Summary      Resend activation email
// @Description  Resend the activation email for a pending account. No-op if the account is already activated.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  resendActivationRequest  true  "Email address"
// @Success      200  {object}  map[string]string  "confirmation message"
// @Failure      400  {object}  map[string]string  "error message"
// @Router       /api/v2/auth/resend-activation [post]
func (h *AuthHandler) ResendActivationEmail(c *gin.Context) {
	var req resendActivationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	_, _ = h.authClient.ResendActivationEmail(c.Request.Context(), &authpb.ResendActivationEmailRequest{
		Email: req.Email,
	})
	c.JSON(http.StatusOK, gin.H{"message": "if the email is registered and pending activation, a new activation email has been sent"})
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Logout godoc
// @Summary      Logout
// @Description  Revoke the refresh token to end the session
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body  logoutRequest  true  "Refresh token to revoke"
// @Success      200  {object}  map[string]string  "success message"
// @Router       /api/v2/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// Forward client IP and User-Agent to auth-service via gRPC metadata
	md := metadata.Pairs(
		"x-forwarded-for", c.ClientIP(),
		"x-user-agent", c.Request.UserAgent(),
	)
	ctx := metadata.NewOutgoingContext(c.Request.Context(), md)

	_, _ = h.authClient.Logout(ctx, &authpb.LogoutRequest{
		RefreshToken: req.RefreshToken,
	})
	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}
