// api-gateway/internal/handler/mocks_test.go
//
// Shared hand-written stubs for the gRPC client interfaces consumed by the
// gateway handlers. Each stub uses function fields per RPC method so tests can
// override only the calls they need. Unset methods return zero values; tests
// that rely on a method returning data must set the corresponding function
// field.

package handler_test

import (
	"context"

	"google.golang.org/grpc"

	accountpb "github.com/exbanka/contract/accountpb"
	authpb "github.com/exbanka/contract/authpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	notificationpb "github.com/exbanka/contract/notificationpb"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

// ---------------------------------------------------------------------------
// BankAccountServiceClient
// ---------------------------------------------------------------------------

type stubBankAccountClient struct {
	listFn       func(*accountpb.ListBankAccountsRequest) (*accountpb.ListBankAccountsResponse, error)
	createFn     func(*accountpb.CreateBankAccountRequest) (*accountpb.AccountResponse, error)
	deleteFn     func(*accountpb.DeleteBankAccountRequest) (*accountpb.DeleteBankAccountResponse, error)
	getRSDFn     func(*accountpb.GetBankRSDAccountRequest) (*accountpb.AccountResponse, error)
	debitFn      func(*accountpb.BankAccountOpRequest) (*accountpb.BankAccountOpResponse, error)
	creditFn     func(*accountpb.BankAccountOpRequest) (*accountpb.BankAccountOpResponse, error)
}

func (s *stubBankAccountClient) ListBankAccounts(_ context.Context, in *accountpb.ListBankAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListBankAccountsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &accountpb.ListBankAccountsResponse{}, nil
}
func (s *stubBankAccountClient) CreateBankAccount(_ context.Context, in *accountpb.CreateBankAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &accountpb.AccountResponse{Id: 1}, nil
}
func (s *stubBankAccountClient) DeleteBankAccount(_ context.Context, in *accountpb.DeleteBankAccountRequest, _ ...grpc.CallOption) (*accountpb.DeleteBankAccountResponse, error) {
	if s.deleteFn != nil {
		return s.deleteFn(in)
	}
	return &accountpb.DeleteBankAccountResponse{}, nil
}
func (s *stubBankAccountClient) GetBankRSDAccount(_ context.Context, in *accountpb.GetBankRSDAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if s.getRSDFn != nil {
		return s.getRSDFn(in)
	}
	return &accountpb.AccountResponse{}, nil
}
func (s *stubBankAccountClient) DebitBankAccount(_ context.Context, in *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	if s.debitFn != nil {
		return s.debitFn(in)
	}
	return &accountpb.BankAccountOpResponse{}, nil
}
func (s *stubBankAccountClient) CreditBankAccount(_ context.Context, in *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	if s.creditFn != nil {
		return s.creditFn(in)
	}
	return &accountpb.BankAccountOpResponse{}, nil
}

// ---------------------------------------------------------------------------
// AuthServiceClient
// ---------------------------------------------------------------------------

type stubAuthClient struct {
	loginFn                func(*authpb.LoginRequest) (*authpb.LoginResponse, error)
	validateTokenFn        func(*authpb.ValidateTokenRequest) (*authpb.ValidateTokenResponse, error)
	refreshFn              func(*authpb.RefreshTokenRequest) (*authpb.RefreshTokenResponse, error)
	logoutFn               func(*authpb.LogoutRequest) (*authpb.LogoutResponse, error)
	requestPasswordResetFn func(*authpb.PasswordResetRequest) (*authpb.PasswordResetResponse, error)
	resetPasswordFn        func(*authpb.ResetPasswordRequest) (*authpb.ResetPasswordResponse, error)
	activateAccountFn      func(*authpb.ActivateAccountRequest) (*authpb.ActivateAccountResponse, error)
	setAccountStatusFn     func(*authpb.SetAccountStatusRequest) (*authpb.SetAccountStatusResponse, error)
	getAccountStatusFn     func(*authpb.GetAccountStatusRequest) (*authpb.GetAccountStatusResponse, error)
	getStatusBatchFn       func(*authpb.GetAccountStatusBatchRequest) (*authpb.GetAccountStatusBatchResponse, error)
	resendActivationFn     func(*authpb.ResendActivationEmailRequest) (*authpb.ResendActivationEmailResponse, error)
	listSessionsFn         func(*authpb.ListSessionsRequest) (*authpb.ListSessionsResponse, error)
	revokeSessionFn        func(*authpb.RevokeSessionRequest) (*authpb.RevokeSessionResponse, error)
	revokeAllFn            func(*authpb.RevokeAllSessionsRequest) (*authpb.RevokeAllSessionsResponse, error)
	loginHistoryFn         func(*authpb.LoginHistoryRequest) (*authpb.LoginHistoryResponse, error)
	requestMobileFn        func(*authpb.MobileActivationRequest) (*authpb.MobileActivationResponse, error)
	activateMobileFn       func(*authpb.ActivateMobileDeviceRequest) (*authpb.ActivateMobileDeviceResponse, error)
	refreshMobileFn        func(*authpb.RefreshMobileTokenRequest) (*authpb.RefreshMobileTokenResponse, error)
	deactivateDeviceFn     func(*authpb.DeactivateDeviceRequest) (*authpb.DeactivateDeviceResponse, error)
	transferDeviceFn       func(*authpb.TransferDeviceRequest) (*authpb.TransferDeviceResponse, error)
	getDeviceInfoFn        func(*authpb.GetDeviceInfoRequest) (*authpb.GetDeviceInfoResponse, error)
	setBiometricsFn        func(*authpb.SetBiometricsRequest) (*authpb.SetBiometricsResponse, error)
	getBiometricsFn        func(*authpb.GetBiometricsRequest) (*authpb.GetBiometricsResponse, error)
}

func (s *stubAuthClient) Login(_ context.Context, in *authpb.LoginRequest, _ ...grpc.CallOption) (*authpb.LoginResponse, error) {
	if s.loginFn != nil {
		return s.loginFn(in)
	}
	return &authpb.LoginResponse{}, nil
}
func (s *stubAuthClient) ValidateToken(_ context.Context, in *authpb.ValidateTokenRequest, _ ...grpc.CallOption) (*authpb.ValidateTokenResponse, error) {
	if s.validateTokenFn != nil {
		return s.validateTokenFn(in)
	}
	return &authpb.ValidateTokenResponse{Valid: true}, nil
}
func (s *stubAuthClient) RefreshToken(_ context.Context, in *authpb.RefreshTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshTokenResponse, error) {
	if s.refreshFn != nil {
		return s.refreshFn(in)
	}
	return &authpb.RefreshTokenResponse{}, nil
}
func (s *stubAuthClient) Logout(_ context.Context, in *authpb.LogoutRequest, _ ...grpc.CallOption) (*authpb.LogoutResponse, error) {
	if s.logoutFn != nil {
		return s.logoutFn(in)
	}
	return &authpb.LogoutResponse{}, nil
}
func (s *stubAuthClient) RequestPasswordReset(_ context.Context, in *authpb.PasswordResetRequest, _ ...grpc.CallOption) (*authpb.PasswordResetResponse, error) {
	if s.requestPasswordResetFn != nil {
		return s.requestPasswordResetFn(in)
	}
	return &authpb.PasswordResetResponse{}, nil
}
func (s *stubAuthClient) ResetPassword(_ context.Context, in *authpb.ResetPasswordRequest, _ ...grpc.CallOption) (*authpb.ResetPasswordResponse, error) {
	if s.resetPasswordFn != nil {
		return s.resetPasswordFn(in)
	}
	return &authpb.ResetPasswordResponse{}, nil
}
func (s *stubAuthClient) ActivateAccount(_ context.Context, in *authpb.ActivateAccountRequest, _ ...grpc.CallOption) (*authpb.ActivateAccountResponse, error) {
	if s.activateAccountFn != nil {
		return s.activateAccountFn(in)
	}
	return &authpb.ActivateAccountResponse{}, nil
}
func (s *stubAuthClient) SetAccountStatus(_ context.Context, in *authpb.SetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.SetAccountStatusResponse, error) {
	if s.setAccountStatusFn != nil {
		return s.setAccountStatusFn(in)
	}
	return &authpb.SetAccountStatusResponse{}, nil
}
func (s *stubAuthClient) GetAccountStatus(_ context.Context, in *authpb.GetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusResponse, error) {
	if s.getAccountStatusFn != nil {
		return s.getAccountStatusFn(in)
	}
	return &authpb.GetAccountStatusResponse{Active: true}, nil
}
func (s *stubAuthClient) GetAccountStatusBatch(_ context.Context, in *authpb.GetAccountStatusBatchRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusBatchResponse, error) {
	if s.getStatusBatchFn != nil {
		return s.getStatusBatchFn(in)
	}
	return &authpb.GetAccountStatusBatchResponse{}, nil
}
func (s *stubAuthClient) CreateAccount(_ context.Context, _ *authpb.CreateAccountRequest, _ ...grpc.CallOption) (*authpb.CreateAccountResponse, error) {
	return &authpb.CreateAccountResponse{}, nil
}
func (s *stubAuthClient) ResendActivationEmail(_ context.Context, in *authpb.ResendActivationEmailRequest, _ ...grpc.CallOption) (*authpb.ResendActivationEmailResponse, error) {
	if s.resendActivationFn != nil {
		return s.resendActivationFn(in)
	}
	return &authpb.ResendActivationEmailResponse{}, nil
}
func (s *stubAuthClient) RequestMobileActivation(_ context.Context, in *authpb.MobileActivationRequest, _ ...grpc.CallOption) (*authpb.MobileActivationResponse, error) {
	if s.requestMobileFn != nil {
		return s.requestMobileFn(in)
	}
	return &authpb.MobileActivationResponse{}, nil
}
func (s *stubAuthClient) ActivateMobileDevice(_ context.Context, in *authpb.ActivateMobileDeviceRequest, _ ...grpc.CallOption) (*authpb.ActivateMobileDeviceResponse, error) {
	if s.activateMobileFn != nil {
		return s.activateMobileFn(in)
	}
	return &authpb.ActivateMobileDeviceResponse{}, nil
}
func (s *stubAuthClient) RefreshMobileToken(_ context.Context, in *authpb.RefreshMobileTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshMobileTokenResponse, error) {
	if s.refreshMobileFn != nil {
		return s.refreshMobileFn(in)
	}
	return &authpb.RefreshMobileTokenResponse{}, nil
}
func (s *stubAuthClient) DeactivateDevice(_ context.Context, in *authpb.DeactivateDeviceRequest, _ ...grpc.CallOption) (*authpb.DeactivateDeviceResponse, error) {
	if s.deactivateDeviceFn != nil {
		return s.deactivateDeviceFn(in)
	}
	return &authpb.DeactivateDeviceResponse{}, nil
}
func (s *stubAuthClient) TransferDevice(_ context.Context, in *authpb.TransferDeviceRequest, _ ...grpc.CallOption) (*authpb.TransferDeviceResponse, error) {
	if s.transferDeviceFn != nil {
		return s.transferDeviceFn(in)
	}
	return &authpb.TransferDeviceResponse{}, nil
}
func (s *stubAuthClient) ValidateDeviceSignature(_ context.Context, _ *authpb.ValidateDeviceSignatureRequest, _ ...grpc.CallOption) (*authpb.ValidateDeviceSignatureResponse, error) {
	return &authpb.ValidateDeviceSignatureResponse{Valid: true}, nil
}
func (s *stubAuthClient) GetDeviceInfo(_ context.Context, in *authpb.GetDeviceInfoRequest, _ ...grpc.CallOption) (*authpb.GetDeviceInfoResponse, error) {
	if s.getDeviceInfoFn != nil {
		return s.getDeviceInfoFn(in)
	}
	return &authpb.GetDeviceInfoResponse{}, nil
}
func (s *stubAuthClient) SetBiometricsEnabled(_ context.Context, in *authpb.SetBiometricsRequest, _ ...grpc.CallOption) (*authpb.SetBiometricsResponse, error) {
	if s.setBiometricsFn != nil {
		return s.setBiometricsFn(in)
	}
	return &authpb.SetBiometricsResponse{}, nil
}
func (s *stubAuthClient) GetBiometricsEnabled(_ context.Context, in *authpb.GetBiometricsRequest, _ ...grpc.CallOption) (*authpb.GetBiometricsResponse, error) {
	if s.getBiometricsFn != nil {
		return s.getBiometricsFn(in)
	}
	return &authpb.GetBiometricsResponse{}, nil
}
func (s *stubAuthClient) CheckBiometricsEnabled(_ context.Context, _ *authpb.CheckBiometricsRequest, _ ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error) {
	return &authpb.CheckBiometricsResponse{}, nil
}
func (s *stubAuthClient) ListSessions(_ context.Context, in *authpb.ListSessionsRequest, _ ...grpc.CallOption) (*authpb.ListSessionsResponse, error) {
	if s.listSessionsFn != nil {
		return s.listSessionsFn(in)
	}
	return &authpb.ListSessionsResponse{}, nil
}
func (s *stubAuthClient) RevokeSession(_ context.Context, in *authpb.RevokeSessionRequest, _ ...grpc.CallOption) (*authpb.RevokeSessionResponse, error) {
	if s.revokeSessionFn != nil {
		return s.revokeSessionFn(in)
	}
	return &authpb.RevokeSessionResponse{}, nil
}
func (s *stubAuthClient) RevokeAllSessions(_ context.Context, in *authpb.RevokeAllSessionsRequest, _ ...grpc.CallOption) (*authpb.RevokeAllSessionsResponse, error) {
	if s.revokeAllFn != nil {
		return s.revokeAllFn(in)
	}
	return &authpb.RevokeAllSessionsResponse{}, nil
}
func (s *stubAuthClient) GetLoginHistory(_ context.Context, in *authpb.LoginHistoryRequest, _ ...grpc.CallOption) (*authpb.LoginHistoryResponse, error) {
	if s.loginHistoryFn != nil {
		return s.loginHistoryFn(in)
	}
	return &authpb.LoginHistoryResponse{}, nil
}

// ---------------------------------------------------------------------------
// UserServiceClient
// ---------------------------------------------------------------------------

type stubUserClient struct {
	createEmployeeFn   func(*userpb.CreateEmployeeRequest) (*userpb.EmployeeResponse, error)
	getEmployeeFn      func(*userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error)
	listEmployeesFn    func(*userpb.ListEmployeesRequest) (*userpb.ListEmployeesResponse, error)
	updateEmployeeFn   func(*userpb.UpdateEmployeeRequest) (*userpb.EmployeeResponse, error)
	listRolesFn        func(*userpb.ListRolesRequest) (*userpb.ListRolesResponse, error)
	getRoleFn          func(*userpb.GetRoleRequest) (*userpb.RoleResponse, error)
	createRoleFn       func(*userpb.CreateRoleRequest) (*userpb.RoleResponse, error)
	updateRolePermsFn  func(*userpb.UpdateRolePermissionsRequest) (*userpb.RoleResponse, error)
	assignRolePermFn   func(*userpb.AssignPermissionToRoleRequest) (*userpb.AssignPermissionToRoleResponse, error)
	revokeRolePermFn   func(*userpb.RevokePermissionFromRoleRequest) (*userpb.RevokePermissionFromRoleResponse, error)
	listPermissionsFn  func(*userpb.ListPermissionsRequest) (*userpb.ListPermissionsResponse, error)
	setEmployeeRolesFn func(*userpb.SetEmployeeRolesRequest) (*userpb.EmployeeResponse, error)
	setEmployeePermsFn func(*userpb.SetEmployeePermissionsRequest) (*userpb.EmployeeResponse, error)
}

func (s *stubUserClient) CreateEmployee(_ context.Context, in *userpb.CreateEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	if s.createEmployeeFn != nil {
		return s.createEmployeeFn(in)
	}
	return &userpb.EmployeeResponse{Id: 1}, nil
}
func (s *stubUserClient) GetEmployee(_ context.Context, in *userpb.GetEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	if s.getEmployeeFn != nil {
		return s.getEmployeeFn(in)
	}
	return &userpb.EmployeeResponse{Id: in.Id, Role: "EmployeeBasic"}, nil
}
func (s *stubUserClient) ListEmployees(_ context.Context, in *userpb.ListEmployeesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeesResponse, error) {
	if s.listEmployeesFn != nil {
		return s.listEmployeesFn(in)
	}
	return &userpb.ListEmployeesResponse{}, nil
}
func (s *stubUserClient) UpdateEmployee(_ context.Context, in *userpb.UpdateEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	if s.updateEmployeeFn != nil {
		return s.updateEmployeeFn(in)
	}
	return &userpb.EmployeeResponse{Id: in.Id}, nil
}
func (s *stubUserClient) ListRoles(_ context.Context, in *userpb.ListRolesRequest, _ ...grpc.CallOption) (*userpb.ListRolesResponse, error) {
	if s.listRolesFn != nil {
		return s.listRolesFn(in)
	}
	return &userpb.ListRolesResponse{}, nil
}
func (s *stubUserClient) GetRole(_ context.Context, in *userpb.GetRoleRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	if s.getRoleFn != nil {
		return s.getRoleFn(in)
	}
	return &userpb.RoleResponse{Id: in.Id}, nil
}
func (s *stubUserClient) CreateRole(_ context.Context, in *userpb.CreateRoleRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	if s.createRoleFn != nil {
		return s.createRoleFn(in)
	}
	return &userpb.RoleResponse{}, nil
}
func (s *stubUserClient) UpdateRolePermissions(_ context.Context, in *userpb.UpdateRolePermissionsRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	if s.updateRolePermsFn != nil {
		return s.updateRolePermsFn(in)
	}
	return &userpb.RoleResponse{}, nil
}
func (s *stubUserClient) AssignPermissionToRole(_ context.Context, in *userpb.AssignPermissionToRoleRequest, _ ...grpc.CallOption) (*userpb.AssignPermissionToRoleResponse, error) {
	if s.assignRolePermFn != nil {
		return s.assignRolePermFn(in)
	}
	return &userpb.AssignPermissionToRoleResponse{}, nil
}
func (s *stubUserClient) RevokePermissionFromRole(_ context.Context, in *userpb.RevokePermissionFromRoleRequest, _ ...grpc.CallOption) (*userpb.RevokePermissionFromRoleResponse, error) {
	if s.revokeRolePermFn != nil {
		return s.revokeRolePermFn(in)
	}
	return &userpb.RevokePermissionFromRoleResponse{}, nil
}
func (s *stubUserClient) ListPermissions(_ context.Context, in *userpb.ListPermissionsRequest, _ ...grpc.CallOption) (*userpb.ListPermissionsResponse, error) {
	if s.listPermissionsFn != nil {
		return s.listPermissionsFn(in)
	}
	return &userpb.ListPermissionsResponse{}, nil
}
func (s *stubUserClient) SetEmployeeRoles(_ context.Context, in *userpb.SetEmployeeRolesRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	if s.setEmployeeRolesFn != nil {
		return s.setEmployeeRolesFn(in)
	}
	return &userpb.EmployeeResponse{}, nil
}
func (s *stubUserClient) SetEmployeeAdditionalPermissions(_ context.Context, in *userpb.SetEmployeePermissionsRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	if s.setEmployeePermsFn != nil {
		return s.setEmployeePermsFn(in)
	}
	return &userpb.EmployeeResponse{}, nil
}

// ---------------------------------------------------------------------------
// EmployeeLimitServiceClient
// ---------------------------------------------------------------------------

type stubEmployeeLimitClient struct {
	getFn               func(*userpb.EmployeeLimitRequest) (*userpb.EmployeeLimitResponse, error)
	setFn               func(*userpb.SetEmployeeLimitsRequest) (*userpb.EmployeeLimitResponse, error)
	applyTemplateFn     func(*userpb.ApplyLimitTemplateRequest) (*userpb.EmployeeLimitResponse, error)
	listTemplatesFn     func(*userpb.ListLimitTemplatesRequest) (*userpb.ListLimitTemplatesResponse, error)
	createTemplateFn    func(*userpb.CreateLimitTemplateRequest) (*userpb.LimitTemplateResponse, error)
}

func (s *stubEmployeeLimitClient) GetEmployeeLimits(_ context.Context, in *userpb.EmployeeLimitRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &userpb.EmployeeLimitResponse{}, nil
}
func (s *stubEmployeeLimitClient) SetEmployeeLimits(_ context.Context, in *userpb.SetEmployeeLimitsRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	if s.setFn != nil {
		return s.setFn(in)
	}
	return &userpb.EmployeeLimitResponse{}, nil
}
func (s *stubEmployeeLimitClient) ApplyLimitTemplate(_ context.Context, in *userpb.ApplyLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	if s.applyTemplateFn != nil {
		return s.applyTemplateFn(in)
	}
	return &userpb.EmployeeLimitResponse{}, nil
}
func (s *stubEmployeeLimitClient) ListLimitTemplates(_ context.Context, in *userpb.ListLimitTemplatesRequest, _ ...grpc.CallOption) (*userpb.ListLimitTemplatesResponse, error) {
	if s.listTemplatesFn != nil {
		return s.listTemplatesFn(in)
	}
	return &userpb.ListLimitTemplatesResponse{}, nil
}
func (s *stubEmployeeLimitClient) CreateLimitTemplate(_ context.Context, in *userpb.CreateLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.LimitTemplateResponse, error) {
	if s.createTemplateFn != nil {
		return s.createTemplateFn(in)
	}
	return &userpb.LimitTemplateResponse{}, nil
}

// ---------------------------------------------------------------------------
// ActuaryServiceClient
// ---------------------------------------------------------------------------

type stubActuaryClient struct {
	listFn      func(*userpb.ListActuariesRequest) (*userpb.ListActuariesResponse, error)
	getInfoFn   func(*userpb.GetActuaryInfoRequest) (*userpb.ActuaryInfo, error)
	setLimitFn  func(*userpb.SetActuaryLimitRequest) (*userpb.ActuaryInfo, error)
	resetUsedFn func(*userpb.ResetActuaryUsedLimitRequest) (*userpb.ActuaryInfo, error)
	setApprFn   func(*userpb.SetNeedApprovalRequest) (*userpb.ActuaryInfo, error)
}

func (s *stubActuaryClient) ListActuaries(_ context.Context, in *userpb.ListActuariesRequest, _ ...grpc.CallOption) (*userpb.ListActuariesResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &userpb.ListActuariesResponse{}, nil
}
func (s *stubActuaryClient) GetActuaryInfo(_ context.Context, in *userpb.GetActuaryInfoRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	if s.getInfoFn != nil {
		return s.getInfoFn(in)
	}
	return &userpb.ActuaryInfo{}, nil
}
func (s *stubActuaryClient) SetActuaryLimit(_ context.Context, in *userpb.SetActuaryLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	if s.setLimitFn != nil {
		return s.setLimitFn(in)
	}
	return &userpb.ActuaryInfo{}, nil
}
func (s *stubActuaryClient) ResetActuaryUsedLimit(_ context.Context, in *userpb.ResetActuaryUsedLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	if s.resetUsedFn != nil {
		return s.resetUsedFn(in)
	}
	return &userpb.ActuaryInfo{}, nil
}
func (s *stubActuaryClient) SetNeedApproval(_ context.Context, in *userpb.SetNeedApprovalRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	if s.setApprFn != nil {
		return s.setApprFn(in)
	}
	return &userpb.ActuaryInfo{}, nil
}
func (s *stubActuaryClient) UpdateUsedLimit(_ context.Context, _ *userpb.UpdateUsedLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}

// ---------------------------------------------------------------------------
// BlueprintServiceClient
// ---------------------------------------------------------------------------

type stubBlueprintClient struct {
	createFn func(*userpb.CreateBlueprintRequest) (*userpb.BlueprintResponse, error)
	getFn    func(*userpb.GetBlueprintRequest) (*userpb.BlueprintResponse, error)
	listFn   func(*userpb.ListBlueprintsRequest) (*userpb.ListBlueprintsResponse, error)
	updateFn func(*userpb.UpdateBlueprintRequest) (*userpb.BlueprintResponse, error)
	deleteFn func(*userpb.DeleteBlueprintRequest) (*userpb.DeleteBlueprintResponse, error)
	applyFn  func(*userpb.ApplyBlueprintRequest) (*userpb.ApplyBlueprintResponse, error)
}

func (s *stubBlueprintClient) CreateBlueprint(_ context.Context, in *userpb.CreateBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &userpb.BlueprintResponse{Id: 1}, nil
}
func (s *stubBlueprintClient) GetBlueprint(_ context.Context, in *userpb.GetBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &userpb.BlueprintResponse{Id: in.Id}, nil
}
func (s *stubBlueprintClient) ListBlueprints(_ context.Context, in *userpb.ListBlueprintsRequest, _ ...grpc.CallOption) (*userpb.ListBlueprintsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &userpb.ListBlueprintsResponse{}, nil
}
func (s *stubBlueprintClient) UpdateBlueprint(_ context.Context, in *userpb.UpdateBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	return &userpb.BlueprintResponse{}, nil
}
func (s *stubBlueprintClient) DeleteBlueprint(_ context.Context, in *userpb.DeleteBlueprintRequest, _ ...grpc.CallOption) (*userpb.DeleteBlueprintResponse, error) {
	if s.deleteFn != nil {
		return s.deleteFn(in)
	}
	return &userpb.DeleteBlueprintResponse{}, nil
}
func (s *stubBlueprintClient) ApplyBlueprint(_ context.Context, in *userpb.ApplyBlueprintRequest, _ ...grpc.CallOption) (*userpb.ApplyBlueprintResponse, error) {
	if s.applyFn != nil {
		return s.applyFn(in)
	}
	return &userpb.ApplyBlueprintResponse{}, nil
}

// ---------------------------------------------------------------------------
// ClientServiceClient
// ---------------------------------------------------------------------------

type stubClientClient struct {
	createFn       func(*clientpb.CreateClientRequest) (*clientpb.ClientResponse, error)
	getFn          func(*clientpb.GetClientRequest) (*clientpb.ClientResponse, error)
	getByEmailFn   func(*clientpb.GetClientByEmailRequest) (*clientpb.ClientResponse, error)
	listFn         func(*clientpb.ListClientsRequest) (*clientpb.ListClientsResponse, error)
	updateFn       func(*clientpb.UpdateClientRequest) (*clientpb.ClientResponse, error)
}

func (s *stubClientClient) CreateClient(_ context.Context, in *clientpb.CreateClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &clientpb.ClientResponse{Id: 1}, nil
}
func (s *stubClientClient) GetClient(_ context.Context, in *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &clientpb.ClientResponse{Id: in.Id}, nil
}
func (s *stubClientClient) GetClientByEmail(_ context.Context, in *clientpb.GetClientByEmailRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if s.getByEmailFn != nil {
		return s.getByEmailFn(in)
	}
	return &clientpb.ClientResponse{}, nil
}
func (s *stubClientClient) ListClients(_ context.Context, in *clientpb.ListClientsRequest, _ ...grpc.CallOption) (*clientpb.ListClientsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &clientpb.ListClientsResponse{}, nil
}
func (s *stubClientClient) UpdateClient(_ context.Context, in *clientpb.UpdateClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	return &clientpb.ClientResponse{Id: in.Id}, nil
}

// ---------------------------------------------------------------------------
// ClientLimitServiceClient
// ---------------------------------------------------------------------------

type stubClientLimitClient struct {
	getFn func(*clientpb.GetClientLimitRequest) (*clientpb.ClientLimitResponse, error)
	setFn func(*clientpb.SetClientLimitRequest) (*clientpb.ClientLimitResponse, error)
}

func (s *stubClientLimitClient) GetClientLimits(_ context.Context, in *clientpb.GetClientLimitRequest, _ ...grpc.CallOption) (*clientpb.ClientLimitResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &clientpb.ClientLimitResponse{}, nil
}
func (s *stubClientLimitClient) SetClientLimits(_ context.Context, in *clientpb.SetClientLimitRequest, _ ...grpc.CallOption) (*clientpb.ClientLimitResponse, error) {
	if s.setFn != nil {
		return s.setFn(in)
	}
	return &clientpb.ClientLimitResponse{}, nil
}

// ---------------------------------------------------------------------------
// CardServiceClient
// ---------------------------------------------------------------------------

type stubCardClient struct {
	createFn         func(*cardpb.CreateCardRequest) (*cardpb.CardResponse, error)
	getFn            func(*cardpb.GetCardRequest) (*cardpb.CardResponse, error)
	listByAccountFn  func(*cardpb.ListCardsByAccountRequest) (*cardpb.ListCardsResponse, error)
	listByClientFn   func(*cardpb.ListCardsByClientRequest) (*cardpb.ListCardsResponse, error)
	blockFn          func(*cardpb.BlockCardRequest) (*cardpb.CardResponse, error)
	unblockFn        func(*cardpb.UnblockCardRequest) (*cardpb.CardResponse, error)
	deactivateFn     func(*cardpb.DeactivateCardRequest) (*cardpb.CardResponse, error)
	createAuthPersFn func(*cardpb.CreateAuthorizedPersonRequest) (*cardpb.AuthorizedPersonResponse, error)
}

func (s *stubCardClient) CreateCard(_ context.Context, in *cardpb.CreateCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &cardpb.CardResponse{Id: 1}, nil
}
func (s *stubCardClient) GetCard(_ context.Context, in *cardpb.GetCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &cardpb.CardResponse{Id: in.Id, OwnerId: 1}, nil
}
func (s *stubCardClient) ListCardsByAccount(_ context.Context, in *cardpb.ListCardsByAccountRequest, _ ...grpc.CallOption) (*cardpb.ListCardsResponse, error) {
	if s.listByAccountFn != nil {
		return s.listByAccountFn(in)
	}
	return &cardpb.ListCardsResponse{}, nil
}
func (s *stubCardClient) ListCardsByClient(_ context.Context, in *cardpb.ListCardsByClientRequest, _ ...grpc.CallOption) (*cardpb.ListCardsResponse, error) {
	if s.listByClientFn != nil {
		return s.listByClientFn(in)
	}
	return &cardpb.ListCardsResponse{}, nil
}
func (s *stubCardClient) BlockCard(_ context.Context, in *cardpb.BlockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.blockFn != nil {
		return s.blockFn(in)
	}
	return &cardpb.CardResponse{Id: in.Id}, nil
}
func (s *stubCardClient) UnblockCard(_ context.Context, in *cardpb.UnblockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.unblockFn != nil {
		return s.unblockFn(in)
	}
	return &cardpb.CardResponse{Id: in.Id}, nil
}
func (s *stubCardClient) DeactivateCard(_ context.Context, in *cardpb.DeactivateCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.deactivateFn != nil {
		return s.deactivateFn(in)
	}
	return &cardpb.CardResponse{Id: in.Id}, nil
}
func (s *stubCardClient) CreateAuthorizedPerson(_ context.Context, in *cardpb.CreateAuthorizedPersonRequest, _ ...grpc.CallOption) (*cardpb.AuthorizedPersonResponse, error) {
	if s.createAuthPersFn != nil {
		return s.createAuthPersFn(in)
	}
	return &cardpb.AuthorizedPersonResponse{}, nil
}
func (s *stubCardClient) GetAuthorizedPerson(_ context.Context, _ *cardpb.GetAuthorizedPersonRequest, _ ...grpc.CallOption) (*cardpb.AuthorizedPersonResponse, error) {
	return &cardpb.AuthorizedPersonResponse{}, nil
}

// ---------------------------------------------------------------------------
// VirtualCardServiceClient
// ---------------------------------------------------------------------------

type stubVirtualCardClient struct {
	createFn          func(*cardpb.CreateVirtualCardRequest) (*cardpb.CardResponse, error)
	setPinFn          func(*cardpb.SetCardPinRequest) (*cardpb.SetCardPinResponse, error)
	verifyPinFn       func(*cardpb.VerifyCardPinRequest) (*cardpb.VerifyCardPinResponse, error)
	tempBlockFn       func(*cardpb.TemporaryBlockCardRequest) (*cardpb.CardResponse, error)
	useFn             func(*cardpb.UseCardRequest) (*cardpb.UseCardResponse, error)
}

func (s *stubVirtualCardClient) CreateVirtualCard(_ context.Context, in *cardpb.CreateVirtualCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &cardpb.CardResponse{}, nil
}
func (s *stubVirtualCardClient) SetCardPin(_ context.Context, in *cardpb.SetCardPinRequest, _ ...grpc.CallOption) (*cardpb.SetCardPinResponse, error) {
	if s.setPinFn != nil {
		return s.setPinFn(in)
	}
	return &cardpb.SetCardPinResponse{}, nil
}
func (s *stubVirtualCardClient) VerifyCardPin(_ context.Context, in *cardpb.VerifyCardPinRequest, _ ...grpc.CallOption) (*cardpb.VerifyCardPinResponse, error) {
	if s.verifyPinFn != nil {
		return s.verifyPinFn(in)
	}
	return &cardpb.VerifyCardPinResponse{}, nil
}
func (s *stubVirtualCardClient) TemporaryBlockCard(_ context.Context, in *cardpb.TemporaryBlockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	if s.tempBlockFn != nil {
		return s.tempBlockFn(in)
	}
	return &cardpb.CardResponse{Id: in.Id}, nil
}
func (s *stubVirtualCardClient) UseCard(_ context.Context, in *cardpb.UseCardRequest, _ ...grpc.CallOption) (*cardpb.UseCardResponse, error) {
	if s.useFn != nil {
		return s.useFn(in)
	}
	return &cardpb.UseCardResponse{}, nil
}

// ---------------------------------------------------------------------------
// CardRequestServiceClient
// ---------------------------------------------------------------------------

type stubCardRequestClient struct {
	createFn       func(*cardpb.CreateCardRequestRequest) (*cardpb.CardRequestResponse, error)
	getFn          func(*cardpb.GetCardRequestRequest) (*cardpb.CardRequestResponse, error)
	listFn         func(*cardpb.ListCardRequestsRequest) (*cardpb.ListCardRequestsResponse, error)
	listByClientFn func(*cardpb.ListCardRequestsByClientRequest) (*cardpb.ListCardRequestsResponse, error)
	approveFn      func(*cardpb.ApproveCardRequestRequest) (*cardpb.CardRequestApprovedResponse, error)
	rejectFn       func(*cardpb.RejectCardRequestRequest) (*cardpb.CardRequestResponse, error)
}

func (s *stubCardRequestClient) CreateCardRequest(_ context.Context, in *cardpb.CreateCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &cardpb.CardRequestResponse{Id: 1}, nil
}
func (s *stubCardRequestClient) GetCardRequest(_ context.Context, in *cardpb.GetCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &cardpb.CardRequestResponse{Id: in.Id}, nil
}
func (s *stubCardRequestClient) ListCardRequests(_ context.Context, in *cardpb.ListCardRequestsRequest, _ ...grpc.CallOption) (*cardpb.ListCardRequestsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &cardpb.ListCardRequestsResponse{}, nil
}
func (s *stubCardRequestClient) ListCardRequestsByClient(_ context.Context, in *cardpb.ListCardRequestsByClientRequest, _ ...grpc.CallOption) (*cardpb.ListCardRequestsResponse, error) {
	if s.listByClientFn != nil {
		return s.listByClientFn(in)
	}
	return &cardpb.ListCardRequestsResponse{}, nil
}
func (s *stubCardRequestClient) ApproveCardRequest(_ context.Context, in *cardpb.ApproveCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestApprovedResponse, error) {
	if s.approveFn != nil {
		return s.approveFn(in)
	}
	return &cardpb.CardRequestApprovedResponse{Request: &cardpb.CardRequestResponse{Id: in.Id}, Card: &cardpb.CardResponse{}}, nil
}
func (s *stubCardRequestClient) RejectCardRequest(_ context.Context, in *cardpb.RejectCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	if s.rejectFn != nil {
		return s.rejectFn(in)
	}
	return &cardpb.CardRequestResponse{Id: in.Id}, nil
}

// ---------------------------------------------------------------------------
// BankAccountServiceClient
// ---------------------------------------------------------------------------

// (Defined for use in handlers that need it; only ListBankAccounts/CreateBankAccount/DeleteBankAccount used.)
// (Already inlined per-test; not duplicated here.)

// ---------------------------------------------------------------------------
// CreditServiceClient
// ---------------------------------------------------------------------------

type stubCreditClient struct {
	createReqFn          func(*creditpb.CreateLoanRequestReq) (*creditpb.LoanRequestResponse, error)
	getReqFn             func(*creditpb.GetLoanRequestReq) (*creditpb.LoanRequestResponse, error)
	listReqFn            func(*creditpb.ListLoanRequestsReq) (*creditpb.ListLoanRequestsResponse, error)
	approveReqFn         func(*creditpb.ApproveLoanRequestReq) (*creditpb.LoanResponse, error)
	rejectReqFn          func(*creditpb.RejectLoanRequestReq) (*creditpb.LoanRequestResponse, error)
	getLoanFn            func(*creditpb.GetLoanReq) (*creditpb.LoanResponse, error)
	listLoansByClientFn  func(*creditpb.ListLoansByClientReq) (*creditpb.ListLoansResponse, error)
	listAllLoansFn       func(*creditpb.ListAllLoansReq) (*creditpb.ListLoansResponse, error)
	getInstallmentsFn    func(*creditpb.GetInstallmentsByLoanReq) (*creditpb.ListInstallmentsResponse, error)
	listTiersFn          func(*creditpb.ListInterestRateTiersRequest) (*creditpb.ListInterestRateTiersResponse, error)
	createTierFn         func(*creditpb.CreateInterestRateTierRequest) (*creditpb.InterestRateTierResponse, error)
	updateTierFn         func(*creditpb.UpdateInterestRateTierRequest) (*creditpb.InterestRateTierResponse, error)
	deleteTierFn         func(*creditpb.DeleteInterestRateTierRequest) (*creditpb.DeleteResponse, error)
	listMarginsFn        func(*creditpb.ListBankMarginsRequest) (*creditpb.ListBankMarginsResponse, error)
	updateMarginFn       func(*creditpb.UpdateBankMarginRequest) (*creditpb.BankMarginResponse, error)
	applyVarRateFn       func(*creditpb.ApplyVariableRateUpdateRequest) (*creditpb.ApplyVariableRateUpdateResponse, error)
}

func (s *stubCreditClient) CreateLoanRequest(_ context.Context, in *creditpb.CreateLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	if s.createReqFn != nil {
		return s.createReqFn(in)
	}
	return &creditpb.LoanRequestResponse{Id: 1}, nil
}
func (s *stubCreditClient) GetLoanRequest(_ context.Context, in *creditpb.GetLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	if s.getReqFn != nil {
		return s.getReqFn(in)
	}
	return &creditpb.LoanRequestResponse{Id: in.Id}, nil
}
func (s *stubCreditClient) ListLoanRequests(_ context.Context, in *creditpb.ListLoanRequestsReq, _ ...grpc.CallOption) (*creditpb.ListLoanRequestsResponse, error) {
	if s.listReqFn != nil {
		return s.listReqFn(in)
	}
	return &creditpb.ListLoanRequestsResponse{}, nil
}
func (s *stubCreditClient) ApproveLoanRequest(_ context.Context, in *creditpb.ApproveLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanResponse, error) {
	if s.approveReqFn != nil {
		return s.approveReqFn(in)
	}
	return &creditpb.LoanResponse{}, nil
}
func (s *stubCreditClient) RejectLoanRequest(_ context.Context, in *creditpb.RejectLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	if s.rejectReqFn != nil {
		return s.rejectReqFn(in)
	}
	return &creditpb.LoanRequestResponse{Id: in.RequestId}, nil
}
func (s *stubCreditClient) GetLoan(_ context.Context, in *creditpb.GetLoanReq, _ ...grpc.CallOption) (*creditpb.LoanResponse, error) {
	if s.getLoanFn != nil {
		return s.getLoanFn(in)
	}
	return &creditpb.LoanResponse{Id: in.Id}, nil
}
func (s *stubCreditClient) ListLoansByClient(_ context.Context, in *creditpb.ListLoansByClientReq, _ ...grpc.CallOption) (*creditpb.ListLoansResponse, error) {
	if s.listLoansByClientFn != nil {
		return s.listLoansByClientFn(in)
	}
	return &creditpb.ListLoansResponse{}, nil
}
func (s *stubCreditClient) ListAllLoans(_ context.Context, in *creditpb.ListAllLoansReq, _ ...grpc.CallOption) (*creditpb.ListLoansResponse, error) {
	if s.listAllLoansFn != nil {
		return s.listAllLoansFn(in)
	}
	return &creditpb.ListLoansResponse{}, nil
}
func (s *stubCreditClient) GetInstallmentsByLoan(_ context.Context, in *creditpb.GetInstallmentsByLoanReq, _ ...grpc.CallOption) (*creditpb.ListInstallmentsResponse, error) {
	if s.getInstallmentsFn != nil {
		return s.getInstallmentsFn(in)
	}
	return &creditpb.ListInstallmentsResponse{}, nil
}
func (s *stubCreditClient) ListInterestRateTiers(_ context.Context, in *creditpb.ListInterestRateTiersRequest, _ ...grpc.CallOption) (*creditpb.ListInterestRateTiersResponse, error) {
	if s.listTiersFn != nil {
		return s.listTiersFn(in)
	}
	return &creditpb.ListInterestRateTiersResponse{}, nil
}
func (s *stubCreditClient) CreateInterestRateTier(_ context.Context, in *creditpb.CreateInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.InterestRateTierResponse, error) {
	if s.createTierFn != nil {
		return s.createTierFn(in)
	}
	return &creditpb.InterestRateTierResponse{}, nil
}
func (s *stubCreditClient) UpdateInterestRateTier(_ context.Context, in *creditpb.UpdateInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.InterestRateTierResponse, error) {
	if s.updateTierFn != nil {
		return s.updateTierFn(in)
	}
	return &creditpb.InterestRateTierResponse{}, nil
}
func (s *stubCreditClient) DeleteInterestRateTier(_ context.Context, in *creditpb.DeleteInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.DeleteResponse, error) {
	if s.deleteTierFn != nil {
		return s.deleteTierFn(in)
	}
	return &creditpb.DeleteResponse{}, nil
}
func (s *stubCreditClient) ListBankMargins(_ context.Context, in *creditpb.ListBankMarginsRequest, _ ...grpc.CallOption) (*creditpb.ListBankMarginsResponse, error) {
	if s.listMarginsFn != nil {
		return s.listMarginsFn(in)
	}
	return &creditpb.ListBankMarginsResponse{}, nil
}
func (s *stubCreditClient) UpdateBankMargin(_ context.Context, in *creditpb.UpdateBankMarginRequest, _ ...grpc.CallOption) (*creditpb.BankMarginResponse, error) {
	if s.updateMarginFn != nil {
		return s.updateMarginFn(in)
	}
	return &creditpb.BankMarginResponse{}, nil
}
func (s *stubCreditClient) ApplyVariableRateUpdate(_ context.Context, in *creditpb.ApplyVariableRateUpdateRequest, _ ...grpc.CallOption) (*creditpb.ApplyVariableRateUpdateResponse, error) {
	if s.applyVarRateFn != nil {
		return s.applyVarRateFn(in)
	}
	return &creditpb.ApplyVariableRateUpdateResponse{}, nil
}

// ---------------------------------------------------------------------------
// ExchangeServiceClient
// ---------------------------------------------------------------------------

type stubExchangeClient struct {
	listFn      func(*exchangepb.ListRatesRequest) (*exchangepb.ListRatesResponse, error)
	getFn       func(*exchangepb.GetRateRequest) (*exchangepb.RateResponse, error)
	calculateFn func(*exchangepb.CalculateRequest) (*exchangepb.CalculateResponse, error)
	convertFn   func(*exchangepb.ConvertRequest) (*exchangepb.ConvertResponse, error)
}

func (s *stubExchangeClient) ListRates(_ context.Context, in *exchangepb.ListRatesRequest, _ ...grpc.CallOption) (*exchangepb.ListRatesResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &exchangepb.ListRatesResponse{}, nil
}
func (s *stubExchangeClient) GetRate(_ context.Context, in *exchangepb.GetRateRequest, _ ...grpc.CallOption) (*exchangepb.RateResponse, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &exchangepb.RateResponse{FromCurrency: in.FromCurrency, ToCurrency: in.ToCurrency}, nil
}
func (s *stubExchangeClient) Calculate(_ context.Context, in *exchangepb.CalculateRequest, _ ...grpc.CallOption) (*exchangepb.CalculateResponse, error) {
	if s.calculateFn != nil {
		return s.calculateFn(in)
	}
	return &exchangepb.CalculateResponse{
		FromCurrency:    in.FromCurrency,
		ToCurrency:      in.ToCurrency,
		InputAmount:     in.Amount,
		ConvertedAmount: in.Amount,
	}, nil
}
func (s *stubExchangeClient) Convert(_ context.Context, in *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	if s.convertFn != nil {
		return s.convertFn(in)
	}
	return &exchangepb.ConvertResponse{}, nil
}

// ---------------------------------------------------------------------------
// NotificationServiceClient
// ---------------------------------------------------------------------------

type stubNotificationClient struct {
	listFn         func(*notificationpb.ListNotificationsRequest) (*notificationpb.ListNotificationsResponse, error)
	unreadFn       func(*notificationpb.GetUnreadCountRequest) (*notificationpb.GetUnreadCountResponse, error)
	markReadFn     func(*notificationpb.MarkNotificationReadRequest) (*notificationpb.MarkNotificationReadResponse, error)
	markAllReadFn  func(*notificationpb.MarkAllNotificationsReadRequest) (*notificationpb.MarkAllNotificationsReadResponse, error)
	pendingFn      func(*notificationpb.GetPendingMobileRequest) (*notificationpb.PendingMobileResponse, error)
	ackFn          func(*notificationpb.AckMobileRequest) (*notificationpb.AckMobileResponse, error)
}

func (s *stubNotificationClient) SendEmail(_ context.Context, _ *notificationpb.SendEmailRequest, _ ...grpc.CallOption) (*notificationpb.SendEmailResponse, error) {
	return &notificationpb.SendEmailResponse{}, nil
}
func (s *stubNotificationClient) GetDeliveryStatus(_ context.Context, _ *notificationpb.GetDeliveryStatusRequest, _ ...grpc.CallOption) (*notificationpb.GetDeliveryStatusResponse, error) {
	return &notificationpb.GetDeliveryStatusResponse{}, nil
}
func (s *stubNotificationClient) GetPendingMobileItems(_ context.Context, in *notificationpb.GetPendingMobileRequest, _ ...grpc.CallOption) (*notificationpb.PendingMobileResponse, error) {
	if s.pendingFn != nil {
		return s.pendingFn(in)
	}
	return &notificationpb.PendingMobileResponse{}, nil
}
func (s *stubNotificationClient) AckMobileItem(_ context.Context, in *notificationpb.AckMobileRequest, _ ...grpc.CallOption) (*notificationpb.AckMobileResponse, error) {
	if s.ackFn != nil {
		return s.ackFn(in)
	}
	return &notificationpb.AckMobileResponse{}, nil
}
func (s *stubNotificationClient) ListNotifications(_ context.Context, in *notificationpb.ListNotificationsRequest, _ ...grpc.CallOption) (*notificationpb.ListNotificationsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &notificationpb.ListNotificationsResponse{}, nil
}
func (s *stubNotificationClient) GetUnreadCount(_ context.Context, in *notificationpb.GetUnreadCountRequest, _ ...grpc.CallOption) (*notificationpb.GetUnreadCountResponse, error) {
	if s.unreadFn != nil {
		return s.unreadFn(in)
	}
	return &notificationpb.GetUnreadCountResponse{}, nil
}
func (s *stubNotificationClient) MarkNotificationRead(_ context.Context, in *notificationpb.MarkNotificationReadRequest, _ ...grpc.CallOption) (*notificationpb.MarkNotificationReadResponse, error) {
	if s.markReadFn != nil {
		return s.markReadFn(in)
	}
	return &notificationpb.MarkNotificationReadResponse{}, nil
}
func (s *stubNotificationClient) MarkAllNotificationsRead(_ context.Context, in *notificationpb.MarkAllNotificationsReadRequest, _ ...grpc.CallOption) (*notificationpb.MarkAllNotificationsReadResponse, error) {
	if s.markAllReadFn != nil {
		return s.markAllReadFn(in)
	}
	return &notificationpb.MarkAllNotificationsReadResponse{}, nil
}

// ---------------------------------------------------------------------------
// VerificationGRPCServiceClient
// ---------------------------------------------------------------------------

type stubVerificationClient struct {
	createFn         func(*verificationpb.CreateChallengeRequest) (*verificationpb.CreateChallengeResponse, error)
	getStatusFn      func(*verificationpb.GetChallengeStatusRequest) (*verificationpb.GetChallengeStatusResponse, error)
	getPendingFn     func(*verificationpb.GetPendingChallengeRequest) (*verificationpb.GetPendingChallengeResponse, error)
	submitFn         func(*verificationpb.SubmitVerificationRequest) (*verificationpb.SubmitVerificationResponse, error)
	submitCodeFn     func(*verificationpb.SubmitCodeRequest) (*verificationpb.SubmitCodeResponse, error)
	verifyByBioFn    func(*verificationpb.VerifyByBiometricRequest) (*verificationpb.VerifyByBiometricResponse, error)
}

func (s *stubVerificationClient) CreateChallenge(_ context.Context, in *verificationpb.CreateChallengeRequest, _ ...grpc.CallOption) (*verificationpb.CreateChallengeResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &verificationpb.CreateChallengeResponse{}, nil
}
func (s *stubVerificationClient) GetChallengeStatus(_ context.Context, in *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
	if s.getStatusFn != nil {
		return s.getStatusFn(in)
	}
	return &verificationpb.GetChallengeStatusResponse{}, nil
}
func (s *stubVerificationClient) GetPendingChallenge(_ context.Context, in *verificationpb.GetPendingChallengeRequest, _ ...grpc.CallOption) (*verificationpb.GetPendingChallengeResponse, error) {
	if s.getPendingFn != nil {
		return s.getPendingFn(in)
	}
	return &verificationpb.GetPendingChallengeResponse{}, nil
}
func (s *stubVerificationClient) SubmitVerification(_ context.Context, in *verificationpb.SubmitVerificationRequest, _ ...grpc.CallOption) (*verificationpb.SubmitVerificationResponse, error) {
	if s.submitFn != nil {
		return s.submitFn(in)
	}
	return &verificationpb.SubmitVerificationResponse{}, nil
}
func (s *stubVerificationClient) SubmitCode(_ context.Context, in *verificationpb.SubmitCodeRequest, _ ...grpc.CallOption) (*verificationpb.SubmitCodeResponse, error) {
	if s.submitCodeFn != nil {
		return s.submitCodeFn(in)
	}
	return &verificationpb.SubmitCodeResponse{}, nil
}
func (s *stubVerificationClient) VerifyByBiometric(_ context.Context, in *verificationpb.VerifyByBiometricRequest, _ ...grpc.CallOption) (*verificationpb.VerifyByBiometricResponse, error) {
	if s.verifyByBioFn != nil {
		return s.verifyByBioFn(in)
	}
	return &verificationpb.VerifyByBiometricResponse{}, nil
}

// ---------------------------------------------------------------------------
// TransactionServiceClient
// ---------------------------------------------------------------------------

type stubTransactionClient struct {
	createPaymentFn   func(*transactionpb.CreatePaymentRequest) (*transactionpb.PaymentResponse, error)
	executePaymentFn  func(*transactionpb.ExecutePaymentRequest) (*transactionpb.PaymentResponse, error)
	getPaymentFn      func(*transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error)
	listPmtByAcctFn   func(*transactionpb.ListPaymentsByAccountRequest) (*transactionpb.ListPaymentsResponse, error)
	listPmtByClientFn func(*transactionpb.ListPaymentsByClientRequest) (*transactionpb.ListPaymentsResponse, error)
	createTransferFn  func(*transactionpb.CreateTransferRequest) (*transactionpb.TransferResponse, error)
	executeTransferFn func(*transactionpb.ExecuteTransferRequest) (*transactionpb.TransferResponse, error)
	getTransferFn     func(*transactionpb.GetTransferRequest) (*transactionpb.TransferResponse, error)
	listTransfersFn   func(*transactionpb.ListTransfersByClientRequest) (*transactionpb.ListTransfersResponse, error)
	createRecipFn     func(*transactionpb.CreatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error)
	getRecipFn        func(*transactionpb.GetPaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error)
	listRecipsFn      func(*transactionpb.ListPaymentRecipientsRequest) (*transactionpb.ListPaymentRecipientsResponse, error)
	updateRecipFn     func(*transactionpb.UpdatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error)
	deleteRecipFn     func(*transactionpb.DeletePaymentRecipientRequest) (*transactionpb.DeletePaymentRecipientResponse, error)
}

func (s *stubTransactionClient) CreatePayment(_ context.Context, in *transactionpb.CreatePaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	if s.createPaymentFn != nil {
		return s.createPaymentFn(in)
	}
	return &transactionpb.PaymentResponse{Id: 1}, nil
}
func (s *stubTransactionClient) ExecutePayment(_ context.Context, in *transactionpb.ExecutePaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	if s.executePaymentFn != nil {
		return s.executePaymentFn(in)
	}
	return &transactionpb.PaymentResponse{Id: in.PaymentId}, nil
}
func (s *stubTransactionClient) GetPayment(_ context.Context, in *transactionpb.GetPaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	if s.getPaymentFn != nil {
		return s.getPaymentFn(in)
	}
	return &transactionpb.PaymentResponse{Id: in.Id}, nil
}
func (s *stubTransactionClient) ListPaymentsByAccount(_ context.Context, in *transactionpb.ListPaymentsByAccountRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentsResponse, error) {
	if s.listPmtByAcctFn != nil {
		return s.listPmtByAcctFn(in)
	}
	return &transactionpb.ListPaymentsResponse{}, nil
}
func (s *stubTransactionClient) ListPaymentsByClient(_ context.Context, in *transactionpb.ListPaymentsByClientRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentsResponse, error) {
	if s.listPmtByClientFn != nil {
		return s.listPmtByClientFn(in)
	}
	return &transactionpb.ListPaymentsResponse{}, nil
}
func (s *stubTransactionClient) CreateTransfer(_ context.Context, in *transactionpb.CreateTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	if s.createTransferFn != nil {
		return s.createTransferFn(in)
	}
	return &transactionpb.TransferResponse{Id: 1}, nil
}
func (s *stubTransactionClient) ExecuteTransfer(_ context.Context, in *transactionpb.ExecuteTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	if s.executeTransferFn != nil {
		return s.executeTransferFn(in)
	}
	return &transactionpb.TransferResponse{Id: in.TransferId}, nil
}
func (s *stubTransactionClient) GetTransfer(_ context.Context, in *transactionpb.GetTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	if s.getTransferFn != nil {
		return s.getTransferFn(in)
	}
	return &transactionpb.TransferResponse{Id: in.Id}, nil
}
func (s *stubTransactionClient) ListTransfersByClient(_ context.Context, in *transactionpb.ListTransfersByClientRequest, _ ...grpc.CallOption) (*transactionpb.ListTransfersResponse, error) {
	if s.listTransfersFn != nil {
		return s.listTransfersFn(in)
	}
	return &transactionpb.ListTransfersResponse{}, nil
}
func (s *stubTransactionClient) CreatePaymentRecipient(_ context.Context, in *transactionpb.CreatePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	if s.createRecipFn != nil {
		return s.createRecipFn(in)
	}
	return &transactionpb.PaymentRecipientResponse{Id: 1}, nil
}
func (s *stubTransactionClient) GetPaymentRecipient(_ context.Context, in *transactionpb.GetPaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	if s.getRecipFn != nil {
		return s.getRecipFn(in)
	}
	return &transactionpb.PaymentRecipientResponse{Id: in.Id}, nil
}
func (s *stubTransactionClient) ListPaymentRecipients(_ context.Context, in *transactionpb.ListPaymentRecipientsRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentRecipientsResponse, error) {
	if s.listRecipsFn != nil {
		return s.listRecipsFn(in)
	}
	return &transactionpb.ListPaymentRecipientsResponse{}, nil
}
func (s *stubTransactionClient) UpdatePaymentRecipient(_ context.Context, in *transactionpb.UpdatePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	if s.updateRecipFn != nil {
		return s.updateRecipFn(in)
	}
	return &transactionpb.PaymentRecipientResponse{Id: in.Id}, nil
}
func (s *stubTransactionClient) DeletePaymentRecipient(_ context.Context, in *transactionpb.DeletePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.DeletePaymentRecipientResponse, error) {
	if s.deleteRecipFn != nil {
		return s.deleteRecipFn(in)
	}
	return &transactionpb.DeletePaymentRecipientResponse{}, nil
}

// ---------------------------------------------------------------------------
// FeeServiceClient
// ---------------------------------------------------------------------------

type stubFeeClient struct {
	listFn      func(*transactionpb.ListFeesRequest) (*transactionpb.ListFeesResponse, error)
	createFn    func(*transactionpb.CreateFeeRequest) (*transactionpb.TransferFeeResponse, error)
	updateFn    func(*transactionpb.UpdateFeeRequest) (*transactionpb.TransferFeeResponse, error)
	deleteFn    func(*transactionpb.DeleteFeeRequest) (*transactionpb.DeleteFeeResponse, error)
	calculateFn func(*transactionpb.CalculateFeeRequest) (*transactionpb.CalculateFeeResponse, error)
}

func (s *stubFeeClient) ListFees(_ context.Context, in *transactionpb.ListFeesRequest, _ ...grpc.CallOption) (*transactionpb.ListFeesResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &transactionpb.ListFeesResponse{}, nil
}
func (s *stubFeeClient) CreateFee(_ context.Context, in *transactionpb.CreateFeeRequest, _ ...grpc.CallOption) (*transactionpb.TransferFeeResponse, error) {
	if s.createFn != nil {
		return s.createFn(in)
	}
	return &transactionpb.TransferFeeResponse{}, nil
}
func (s *stubFeeClient) UpdateFee(_ context.Context, in *transactionpb.UpdateFeeRequest, _ ...grpc.CallOption) (*transactionpb.TransferFeeResponse, error) {
	if s.updateFn != nil {
		return s.updateFn(in)
	}
	return &transactionpb.TransferFeeResponse{}, nil
}
func (s *stubFeeClient) DeleteFee(_ context.Context, in *transactionpb.DeleteFeeRequest, _ ...grpc.CallOption) (*transactionpb.DeleteFeeResponse, error) {
	if s.deleteFn != nil {
		return s.deleteFn(in)
	}
	return &transactionpb.DeleteFeeResponse{}, nil
}
func (s *stubFeeClient) CalculateFee(_ context.Context, in *transactionpb.CalculateFeeRequest, _ ...grpc.CallOption) (*transactionpb.CalculateFeeResponse, error) {
	if s.calculateFn != nil {
		return s.calculateFn(in)
	}
	return &transactionpb.CalculateFeeResponse{}, nil
}

// ---------------------------------------------------------------------------
// StockExchangeGRPCServiceClient
// ---------------------------------------------------------------------------

type stubStockExchangeClient struct {
	listFn        func(*stockpb.ListExchangesRequest) (*stockpb.ListExchangesResponse, error)
	getFn         func(*stockpb.GetExchangeRequest) (*stockpb.Exchange, error)
	setTestModeFn func(*stockpb.SetTestingModeRequest) (*stockpb.SetTestingModeResponse, error)
	getTestModeFn func(*stockpb.GetTestingModeRequest) (*stockpb.GetTestingModeResponse, error)
}

func (s *stubStockExchangeClient) ListExchanges(_ context.Context, in *stockpb.ListExchangesRequest, _ ...grpc.CallOption) (*stockpb.ListExchangesResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListExchangesResponse{}, nil
}
func (s *stubStockExchangeClient) GetExchange(_ context.Context, in *stockpb.GetExchangeRequest, _ ...grpc.CallOption) (*stockpb.Exchange, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &stockpb.Exchange{Id: in.Id}, nil
}
func (s *stubStockExchangeClient) SetTestingMode(_ context.Context, in *stockpb.SetTestingModeRequest, _ ...grpc.CallOption) (*stockpb.SetTestingModeResponse, error) {
	if s.setTestModeFn != nil {
		return s.setTestModeFn(in)
	}
	return &stockpb.SetTestingModeResponse{}, nil
}
func (s *stubStockExchangeClient) GetTestingMode(_ context.Context, in *stockpb.GetTestingModeRequest, _ ...grpc.CallOption) (*stockpb.GetTestingModeResponse, error) {
	if s.getTestModeFn != nil {
		return s.getTestModeFn(in)
	}
	return &stockpb.GetTestingModeResponse{}, nil
}

// ---------------------------------------------------------------------------
// OTCGRPCServiceClient
// ---------------------------------------------------------------------------

type stubOTCClient struct {
	listFn func(*stockpb.ListOTCOffersRequest) (*stockpb.ListOTCOffersResponse, error)
	buyFn  func(*stockpb.BuyOTCOfferRequest) (*stockpb.OTCTransaction, error)
}

func (s *stubOTCClient) ListOffers(_ context.Context, in *stockpb.ListOTCOffersRequest, _ ...grpc.CallOption) (*stockpb.ListOTCOffersResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListOTCOffersResponse{}, nil
}
func (s *stubOTCClient) BuyOffer(_ context.Context, in *stockpb.BuyOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCTransaction, error) {
	if s.buyFn != nil {
		return s.buyFn(in)
	}
	return &stockpb.OTCTransaction{}, nil
}

// ---------------------------------------------------------------------------
// TaxGRPCServiceClient
// ---------------------------------------------------------------------------

type stubTaxClient struct {
	listFn         func(*stockpb.ListTaxRecordsRequest) (*stockpb.ListTaxRecordsResponse, error)
	collectFn      func(*stockpb.CollectTaxRequest) (*stockpb.CollectTaxResponse, error)
	listUserFn     func(*stockpb.ListUserTaxRecordsRequest) (*stockpb.ListUserTaxRecordsResponse, error)
}

func (s *stubTaxClient) ListTaxRecords(_ context.Context, in *stockpb.ListTaxRecordsRequest, _ ...grpc.CallOption) (*stockpb.ListTaxRecordsResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &stockpb.ListTaxRecordsResponse{}, nil
}
func (s *stubTaxClient) CollectTax(_ context.Context, in *stockpb.CollectTaxRequest, _ ...grpc.CallOption) (*stockpb.CollectTaxResponse, error) {
	if s.collectFn != nil {
		return s.collectFn(in)
	}
	return &stockpb.CollectTaxResponse{}, nil
}
func (s *stubTaxClient) ListUserTaxRecords(_ context.Context, in *stockpb.ListUserTaxRecordsRequest, _ ...grpc.CallOption) (*stockpb.ListUserTaxRecordsResponse, error) {
	if s.listUserFn != nil {
		return s.listUserFn(in)
	}
	return &stockpb.ListUserTaxRecordsResponse{}, nil
}

// ListEmployeeFullNames was added to UserServiceClient by Celina-4. Stub
// returns empty by default; tests that exercise this RPC can override
// the field on stubUserClient.
func (s *stubUserClient) ListEmployeeFullNames(_ context.Context, _ *userpb.ListEmployeeFullNamesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeeFullNamesResponse, error) {
	return &userpb.ListEmployeeFullNamesResponse{}, nil
}

// ---------------------------------------------------------------------------
// Compile-time interface assertions
// ---------------------------------------------------------------------------

var (
	_ accountpb.BankAccountServiceClient      = (*stubBankAccountClient)(nil)
	_ authpb.AuthServiceClient                = (*stubAuthClient)(nil)
	_ userpb.UserServiceClient                = (*stubUserClient)(nil)
	_ userpb.EmployeeLimitServiceClient       = (*stubEmployeeLimitClient)(nil)
	_ userpb.ActuaryServiceClient             = (*stubActuaryClient)(nil)
	_ userpb.BlueprintServiceClient           = (*stubBlueprintClient)(nil)
	_ clientpb.ClientServiceClient            = (*stubClientClient)(nil)
	_ clientpb.ClientLimitServiceClient       = (*stubClientLimitClient)(nil)
	_ cardpb.CardServiceClient                = (*stubCardClient)(nil)
	_ cardpb.VirtualCardServiceClient         = (*stubVirtualCardClient)(nil)
	_ cardpb.CardRequestServiceClient         = (*stubCardRequestClient)(nil)
	_ creditpb.CreditServiceClient            = (*stubCreditClient)(nil)
	_ exchangepb.ExchangeServiceClient        = (*stubExchangeClient)(nil)
	_ notificationpb.NotificationServiceClient = (*stubNotificationClient)(nil)
	_ verificationpb.VerificationGRPCServiceClient = (*stubVerificationClient)(nil)
	_ transactionpb.TransactionServiceClient  = (*stubTransactionClient)(nil)
	_ transactionpb.FeeServiceClient          = (*stubFeeClient)(nil)
	_ stockpb.StockExchangeGRPCServiceClient  = (*stubStockExchangeClient)(nil)
	_ stockpb.OTCGRPCServiceClient            = (*stubOTCClient)(nil)
	_ stockpb.TaxGRPCServiceClient            = (*stubTaxClient)(nil)
)
