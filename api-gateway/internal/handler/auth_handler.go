package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

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

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.authClient.Login(c.Request.Context(), &authpb.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
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

func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.authClient.RefreshToken(c.Request.Context(), &authpb.RefreshTokenRequest{
		RefreshToken: req.RefreshToken,
	})
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
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

func (h *AuthHandler) RequestPasswordReset(c *gin.Context) {
	var req passwordResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.authClient.RequestPasswordReset(c.Request.Context(), &authpb.PasswordResetRequest{
		Email: req.Email,
	})
	c.JSON(http.StatusOK, gin.H{"message": "if the email exists, a reset link has been sent"})
}

type resetPasswordRequest struct {
	Token           string `json:"token" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := h.authClient.ResetPassword(c.Request.Context(), &authpb.ResetPasswordRequest{
		Token:           req.Token,
		NewPassword:     req.NewPassword,
		ConfirmPassword: req.ConfirmPassword,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}

type activateRequest struct {
	Token           string `json:"token" binding:"required"`
	Password        string `json:"password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

func (h *AuthHandler) ActivateAccount(c *gin.Context) {
	var req activateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := h.authClient.ActivateAccount(c.Request.Context(), &authpb.ActivateAccountRequest{
		Token:           req.Token,
		Password:        req.Password,
		ConfirmPassword: req.ConfirmPassword,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "account activated successfully"})
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req logoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.authClient.Logout(c.Request.Context(), &authpb.LogoutRequest{
		RefreshToken: req.RefreshToken,
	})
	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}
