package handler

import (
	"context"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/service"
	pb "github.com/exbanka/contract/authpb"
)

// authServiceFacade is the minimal subset of *service.AuthService that this
// handler depends on. Defined as an interface so tests can inject a hand-written
// stub without standing up a real service + DB + Kafka producer. The concrete
// *service.AuthService satisfies it.
type authServiceFacade interface {
	Login(ctx context.Context, email, password, ipAddress, userAgent string) (string, string, error)
	ValidateToken(token string) (*service.Claims, error)
	RefreshToken(ctx context.Context, refreshToken, ipAddress, userAgent string) (string, string, error)
	RequestPasswordReset(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, token, newPassword, confirmPassword string) error
	ActivateAccount(ctx context.Context, token, password, confirmPassword string) error
	Logout(ctx context.Context, refreshToken string) error
	SetAccountStatus(ctx context.Context, principalType string, principalID int64, active bool) error
	GetAccountStatus(ctx context.Context, principalType string, principalID int64) (string, bool, error)
	ResendActivationEmail(ctx context.Context, email string) error
	CreateAccountAndActivationToken(ctx context.Context, principalID int64, email, firstName, principalType string) error
	GetAccountStatusBatch(ctx context.Context, principalType string, principalIDs []int64) (map[int64]model.Account, error)
	RefreshTokenForMobile(ctx context.Context, oldRefreshToken, deviceID string, mobileSvc service.MobileDeviceLookup) (string, string, error)
	ListSessions(ctx context.Context, userID int64) ([]model.ActiveSession, error)
	RevokeSession(ctx context.Context, sessionID int64, callerUserID int64) error
	RevokeAllSessionsExceptCurrent(ctx context.Context, userID int64, currentRefreshToken string) error
	GetLoginHistory(ctx context.Context, email string, limit int) ([]service.LoginHistoryEntry, error)
}

// mobileDeviceFacade is the minimal subset of *service.MobileDeviceService
// that this handler depends on. The concrete service satisfies it.
type mobileDeviceFacade interface {
	RequestActivation(ctx context.Context, email string) error
	ActivateDevice(ctx context.Context, email, code, deviceName string) (string, string, string, string, error)
	DeactivateDevice(userID int64, deviceID string) error
	TransferDevice(ctx context.Context, userID int64, email string) error
	ValidateDeviceSignature(deviceID, timestamp, method, path, bodySHA256, signature string) (bool, error)
	GetDeviceInfo(userID int64) (*model.MobileDevice, error)
	SetBiometricsEnabled(userID int64, deviceID string, enabled bool) error
	GetBiometricsEnabled(userID int64, deviceID string) (bool, error)
	CheckBiometricsEnabled(deviceID string) (bool, error)
}

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
	authService authServiceFacade
	mobileSvc   mobileDeviceFacade
}

func NewAuthGRPCHandler(authService *service.AuthService, mobileSvc *service.MobileDeviceService) *AuthGRPCHandler {
	return &AuthGRPCHandler{authService: authService, mobileSvc: mobileSvc}
}

func (h *AuthGRPCHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	meta := extractRequestMeta(ctx)
	access, refresh, err := h.authService.Login(ctx, req.Email, req.Password, meta.IPAddress, meta.UserAgent)
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
		Valid:             true,
		UserId:            claims.UserID,
		Email:             claims.Email,
		Role:              legacyRole,
		Roles:             claims.Roles,
		Permissions:       claims.Permissions,
		SystemType:        claims.SystemType,
		DeviceType:        claims.DeviceType,
		DeviceId:          claims.DeviceID,
		FirstName:         claims.FirstName,
		LastName:          claims.LastName,
		AccountActive:     claims.AccountActive,
		BiometricsEnabled: claims.BiometricsEnabled,
	}, nil
}

func (h *AuthGRPCHandler) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
	meta := extractRequestMeta(ctx)
	access, refresh, err := h.authService.RefreshToken(ctx, req.RefreshToken, meta.IPAddress, meta.UserAgent)
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

func (h *AuthGRPCHandler) ResendActivationEmail(ctx context.Context, req *pb.ResendActivationEmailRequest) (*pb.ResendActivationEmailResponse, error) {
	if err := h.authService.ResendActivationEmail(ctx, req.Email); err != nil {
		log.Printf("warn: resend activation email failed (suppressed): %v", err)
	}
	// Always return success to not leak email existence
	return &pb.ResendActivationEmailResponse{
		Success: true,
		Message: "if the email is registered and pending activation, a new activation email has been sent",
	}, nil
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

// --- Mobile device RPC methods ---

func (h *AuthGRPCHandler) RequestMobileActivation(ctx context.Context, req *pb.MobileActivationRequest) (*pb.MobileActivationResponse, error) {
	if err := h.mobileSvc.RequestActivation(ctx, req.Email); err != nil {
		// Always return success to avoid email enumeration (same pattern as password reset)
		log.Printf("mobile activation request error (suppressed): %v", err)
	}
	return &pb.MobileActivationResponse{
		Success: true,
		Message: "If the email is registered, an activation code has been sent",
	}, nil
}

func (h *AuthGRPCHandler) ActivateMobileDevice(ctx context.Context, req *pb.ActivateMobileDeviceRequest) (*pb.ActivateMobileDeviceResponse, error) {
	access, refresh, deviceID, deviceSecret, err := h.mobileSvc.ActivateDevice(ctx, req.Email, req.Code, req.DeviceName)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.ActivateMobileDeviceResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		DeviceId:     deviceID,
		DeviceSecret: deviceSecret,
	}, nil
}

func (h *AuthGRPCHandler) RefreshMobileToken(ctx context.Context, req *pb.RefreshMobileTokenRequest) (*pb.RefreshMobileTokenResponse, error) {
	access, refresh, err := h.authService.RefreshTokenForMobile(ctx, req.RefreshToken, req.DeviceId, h.mobileSvc)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.RefreshMobileTokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

func (h *AuthGRPCHandler) DeactivateDevice(ctx context.Context, req *pb.DeactivateDeviceRequest) (*pb.DeactivateDeviceResponse, error) {
	if err := h.mobileSvc.DeactivateDevice(req.UserId, req.DeviceId); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.DeactivateDeviceResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) TransferDevice(ctx context.Context, req *pb.TransferDeviceRequest) (*pb.TransferDeviceResponse, error) {
	if err := h.mobileSvc.TransferDevice(ctx, req.UserId, req.Email); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.TransferDeviceResponse{
		Success: true,
		Message: "Device deactivated. New activation code sent to email.",
	}, nil
}

func (h *AuthGRPCHandler) ValidateDeviceSignature(ctx context.Context, req *pb.ValidateDeviceSignatureRequest) (*pb.ValidateDeviceSignatureResponse, error) {
	valid, err := h.mobileSvc.ValidateDeviceSignature(req.DeviceId, req.Timestamp, req.Method, req.Path, req.BodySha256, req.Signature)
	if err != nil {
		return &pb.ValidateDeviceSignatureResponse{Valid: false}, nil
	}
	return &pb.ValidateDeviceSignatureResponse{Valid: valid}, nil
}

func (h *AuthGRPCHandler) GetDeviceInfo(ctx context.Context, req *pb.GetDeviceInfoRequest) (*pb.GetDeviceInfoResponse, error) {
	device, err := h.mobileSvc.GetDeviceInfo(req.UserId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	resp := &pb.GetDeviceInfoResponse{
		DeviceId:   device.DeviceID,
		DeviceName: device.DeviceName,
		Status:     device.Status,
		LastSeenAt: device.LastSeenAt.Format("2006-01-02T15:04:05Z"),
	}
	if device.ActivatedAt != nil {
		resp.ActivatedAt = device.ActivatedAt.Format("2006-01-02T15:04:05Z")
	}
	return resp, nil
}

// --- Biometrics RPC methods ---

func (h *AuthGRPCHandler) SetBiometricsEnabled(ctx context.Context, req *pb.SetBiometricsRequest) (*pb.SetBiometricsResponse, error) {
	if err := h.mobileSvc.SetBiometricsEnabled(req.UserId, req.DeviceId, req.Enabled); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.SetBiometricsResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetBiometricsEnabled(ctx context.Context, req *pb.GetBiometricsRequest) (*pb.GetBiometricsResponse, error) {
	enabled, err := h.mobileSvc.GetBiometricsEnabled(req.UserId, req.DeviceId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.GetBiometricsResponse{Enabled: enabled}, nil
}

func (h *AuthGRPCHandler) CheckBiometricsEnabled(ctx context.Context, req *pb.CheckBiometricsRequest) (*pb.CheckBiometricsResponse, error) {
	enabled, err := h.mobileSvc.CheckBiometricsEnabled(req.DeviceId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.CheckBiometricsResponse{Enabled: enabled}, nil
}

// --- Session management RPC methods ---

func (h *AuthGRPCHandler) ListSessions(ctx context.Context, req *pb.ListSessionsRequest) (*pb.ListSessionsResponse, error) {
	sessions, err := h.authService.ListSessions(ctx, req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sessions: %v", err)
	}

	var infos []*pb.SessionInfo
	for _, s := range sessions {
		infos = append(infos, &pb.SessionInfo{
			Id:           s.ID,
			UserId:       s.UserID,
			UserRole:     s.UserRole,
			IpAddress:    s.IPAddress,
			UserAgent:    s.UserAgent,
			DeviceId:     s.DeviceID,
			SystemType:   s.SystemType,
			LastActiveAt: s.LastActiveAt.Format(time.RFC3339),
			CreatedAt:    s.CreatedAt.Format(time.RFC3339),
		})
	}
	return &pb.ListSessionsResponse{Sessions: infos}, nil
}

func (h *AuthGRPCHandler) RevokeSession(ctx context.Context, req *pb.RevokeSessionRequest) (*pb.RevokeSessionResponse, error) {
	if err := h.authService.RevokeSession(ctx, req.SessionId, req.CallerUserId); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.RevokeSessionResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) RevokeAllSessions(ctx context.Context, req *pb.RevokeAllSessionsRequest) (*pb.RevokeAllSessionsResponse, error) {
	if err := h.authService.RevokeAllSessionsExceptCurrent(ctx, req.UserId, req.CurrentRefreshToken); err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.RevokeAllSessionsResponse{Success: true}, nil
}

func (h *AuthGRPCHandler) GetLoginHistory(ctx context.Context, req *pb.LoginHistoryRequest) (*pb.LoginHistoryResponse, error) {
	entries, err := h.authService.GetLoginHistory(ctx, req.Email, int(req.Limit))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get login history: %v", err)
	}

	var pbEntries []*pb.LoginHistoryEntry
	for _, e := range entries {
		pbEntries = append(pbEntries, &pb.LoginHistoryEntry{
			Id:         e.ID,
			Email:      e.Email,
			IpAddress:  e.IPAddress,
			UserAgent:  e.UserAgent,
			DeviceType: e.DeviceType,
			Success:    e.Success,
			CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		})
	}
	return &pb.LoginHistoryResponse{Entries: pbEntries}, nil
}
