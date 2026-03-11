package handler

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/authpb"
	"github.com/exbanka/auth-service/internal/service"
)

type AuthGRPCHandler struct {
	pb.UnimplementedAuthServiceServer
	authService *service.AuthService
}

func NewAuthGRPCHandler(authService *service.AuthService) *AuthGRPCHandler {
	return &AuthGRPCHandler{authService: authService}
}

func (h *AuthGRPCHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	access, refresh, err := h.authService.Login(ctx, req.Email, req.Password)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid credentials")
	}
	return &pb.LoginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (h *AuthGRPCHandler) ValidateToken(ctx context.Context, req *pb.ValidateTokenRequest) (*pb.ValidateTokenResponse, error) {
	claims, err := h.authService.ValidateToken(req.Token)
	if err != nil {
		return &pb.ValidateTokenResponse{Valid: false}, nil
	}
	return &pb.ValidateTokenResponse{
		Valid:       true,
		UserId:      claims.UserID,
		Email:       claims.Email,
		Role:        claims.Role,
		Permissions: claims.Permissions,
	}, nil
}

func (h *AuthGRPCHandler) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
	access, refresh, err := h.authService.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid refresh token")
	}
	return &pb.RefreshTokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (h *AuthGRPCHandler) RequestPasswordReset(ctx context.Context, req *pb.PasswordResetRequest) (*pb.PasswordResetResponse, error) {
	if err := h.authService.RequestPasswordReset(ctx, req.Email); err != nil {
		log.Printf("warn: password reset request failed for email (suppressed): %v", err)
	}
	// Always return success to not leak email existence
	return &pb.PasswordResetResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordResponse, error) {
	if err := h.authService.ResetPassword(ctx, req.Token, req.NewPassword, req.ConfirmPassword); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &pb.ResetPasswordResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) ActivateAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
	if err := h.authService.ActivateAccount(ctx, req.Token, req.Password, req.ConfirmPassword); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &pb.ActivateAccountResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	if err := h.authService.Logout(ctx, req.RefreshToken); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &pb.LogoutResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) CreateActivationToken(ctx context.Context, req *pb.CreateActivationTokenRequest) (*pb.CreateActivationTokenResponse, error) {
	if err := h.authService.CreateActivationToken(ctx, req.UserId, req.Email, req.FirstName); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &pb.CreateActivationTokenResponse{Success: true}, nil
}
