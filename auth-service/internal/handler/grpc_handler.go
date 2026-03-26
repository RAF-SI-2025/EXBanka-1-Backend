package handler

import (
	"context"
	"log"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/auth-service/internal/service"
	pb "github.com/exbanka/contract/authpb"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"),
		strings.Contains(msg, "must have"), strings.Contains(msg, "do not match"),
		strings.Contains(msg, "does not match"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "revoked"):
		return codes.Unauthenticated
	case strings.Contains(msg, "expired"):
		return codes.DeadlineExceeded
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

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
	// Derive legacy Role field from first role for backwards compat
	legacyRole := ""
	if len(claims.Roles) > 0 {
		legacyRole = claims.Roles[0]
	}
	return &pb.ValidateTokenResponse{
		Valid:       true,
		UserId:      claims.UserID,
		Email:       claims.Email,
		Role:        legacyRole,
		Roles:       claims.Roles,
		Permissions: claims.Permissions,
		SystemType:  claims.SystemType,
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
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.ResetPasswordResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) ActivateAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
	if err := h.authService.ActivateAccount(ctx, req.Token, req.Password, req.ConfirmPassword); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.ActivateAccountResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) Logout(ctx context.Context, req *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	if err := h.authService.Logout(ctx, req.RefreshToken); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.LogoutResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) SetAccountStatus(ctx context.Context, req *pb.SetAccountStatusRequest) (*pb.SetAccountStatusResponse, error) {
	if err := h.authService.SetAccountStatus(ctx, req.PrincipalType, req.PrincipalId, req.Active); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set account status: %v", err)
	}
	return &pb.SetAccountStatusResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetAccountStatus(ctx context.Context, req *pb.GetAccountStatusRequest) (*pb.GetAccountStatusResponse, error) {
	st, active, err := h.authService.GetAccountStatus(ctx, req.PrincipalType, req.PrincipalId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "account not found")
	}
	return &pb.GetAccountStatusResponse{Status: st, Active: active}, nil
}

func (h *AuthGRPCHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse, error) {
	if err := h.authService.CreateAccountAndActivationToken(ctx, req.PrincipalId, req.Email, req.FirstName, req.PrincipalType); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.CreateAccountResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetAccountStatusBatch(ctx context.Context, req *pb.GetAccountStatusBatchRequest) (*pb.GetAccountStatusBatchResponse, error) {
	accounts, err := h.authService.GetAccountStatusBatch(ctx, req.PrincipalType, req.PrincipalIds)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get account statuses: %v", err)
	}
	var entries []*pb.AccountStatusEntry
	for pid, acct := range accounts {
		entries = append(entries, &pb.AccountStatusEntry{
			PrincipalId: pid,
			Status:      acct.Status,
			Active:      acct.Status == "active",
		})
	}
	return &pb.GetAccountStatusBatchResponse{Entries: entries}, nil
}
