package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestNotImplementedHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", notImplemented)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)

	var body map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	errObj, ok := body["error"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "not_implemented", errObj["code"])
	assert.Contains(t, errObj["message"], "coming in a future release")
}

// ---------------------------------------------------------------------------
// Minimal no-op stubs for router setup tests.
// ---------------------------------------------------------------------------

type noopAuthClient struct{}

func (n *noopAuthClient) Login(_ context.Context, _ *authpb.LoginRequest, _ ...grpc.CallOption) (*authpb.LoginResponse, error) {
	return &authpb.LoginResponse{}, nil
}
func (n *noopAuthClient) ValidateToken(_ context.Context, _ *authpb.ValidateTokenRequest, _ ...grpc.CallOption) (*authpb.ValidateTokenResponse, error) {
	return &authpb.ValidateTokenResponse{Valid: false}, nil
}
func (n *noopAuthClient) RefreshToken(_ context.Context, _ *authpb.RefreshTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshTokenResponse, error) {
	return &authpb.RefreshTokenResponse{}, nil
}
func (n *noopAuthClient) Logout(_ context.Context, _ *authpb.LogoutRequest, _ ...grpc.CallOption) (*authpb.LogoutResponse, error) {
	return &authpb.LogoutResponse{}, nil
}
func (n *noopAuthClient) RequestPasswordReset(_ context.Context, _ *authpb.PasswordResetRequest, _ ...grpc.CallOption) (*authpb.PasswordResetResponse, error) {
	return &authpb.PasswordResetResponse{}, nil
}
func (n *noopAuthClient) ResetPassword(_ context.Context, _ *authpb.ResetPasswordRequest, _ ...grpc.CallOption) (*authpb.ResetPasswordResponse, error) {
	return &authpb.ResetPasswordResponse{}, nil
}
func (n *noopAuthClient) ActivateAccount(_ context.Context, _ *authpb.ActivateAccountRequest, _ ...grpc.CallOption) (*authpb.ActivateAccountResponse, error) {
	return &authpb.ActivateAccountResponse{}, nil
}
func (n *noopAuthClient) SetAccountStatus(_ context.Context, _ *authpb.SetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.SetAccountStatusResponse, error) {
	return &authpb.SetAccountStatusResponse{}, nil
}
func (n *noopAuthClient) GetAccountStatus(_ context.Context, _ *authpb.GetAccountStatusRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusResponse, error) {
	return &authpb.GetAccountStatusResponse{}, nil
}
func (n *noopAuthClient) GetAccountStatusBatch(_ context.Context, _ *authpb.GetAccountStatusBatchRequest, _ ...grpc.CallOption) (*authpb.GetAccountStatusBatchResponse, error) {
	return &authpb.GetAccountStatusBatchResponse{}, nil
}
func (n *noopAuthClient) CreateAccount(_ context.Context, _ *authpb.CreateAccountRequest, _ ...grpc.CallOption) (*authpb.CreateAccountResponse, error) {
	return &authpb.CreateAccountResponse{}, nil
}
func (n *noopAuthClient) RequestMobileActivation(_ context.Context, _ *authpb.MobileActivationRequest, _ ...grpc.CallOption) (*authpb.MobileActivationResponse, error) {
	return &authpb.MobileActivationResponse{}, nil
}
func (n *noopAuthClient) ActivateMobileDevice(_ context.Context, _ *authpb.ActivateMobileDeviceRequest, _ ...grpc.CallOption) (*authpb.ActivateMobileDeviceResponse, error) {
	return &authpb.ActivateMobileDeviceResponse{}, nil
}
func (n *noopAuthClient) RefreshMobileToken(_ context.Context, _ *authpb.RefreshMobileTokenRequest, _ ...grpc.CallOption) (*authpb.RefreshMobileTokenResponse, error) {
	return &authpb.RefreshMobileTokenResponse{}, nil
}
func (n *noopAuthClient) DeactivateDevice(_ context.Context, _ *authpb.DeactivateDeviceRequest, _ ...grpc.CallOption) (*authpb.DeactivateDeviceResponse, error) {
	return &authpb.DeactivateDeviceResponse{}, nil
}
func (n *noopAuthClient) TransferDevice(_ context.Context, _ *authpb.TransferDeviceRequest, _ ...grpc.CallOption) (*authpb.TransferDeviceResponse, error) {
	return &authpb.TransferDeviceResponse{}, nil
}
func (n *noopAuthClient) ValidateDeviceSignature(_ context.Context, _ *authpb.ValidateDeviceSignatureRequest, _ ...grpc.CallOption) (*authpb.ValidateDeviceSignatureResponse, error) {
	return &authpb.ValidateDeviceSignatureResponse{Valid: true}, nil
}
func (n *noopAuthClient) GetDeviceInfo(_ context.Context, _ *authpb.GetDeviceInfoRequest, _ ...grpc.CallOption) (*authpb.GetDeviceInfoResponse, error) {
	return &authpb.GetDeviceInfoResponse{}, nil
}
func (n *noopAuthClient) ListSessions(_ context.Context, _ *authpb.ListSessionsRequest, _ ...grpc.CallOption) (*authpb.ListSessionsResponse, error) {
	return &authpb.ListSessionsResponse{}, nil
}
func (n *noopAuthClient) RevokeSession(_ context.Context, _ *authpb.RevokeSessionRequest, _ ...grpc.CallOption) (*authpb.RevokeSessionResponse, error) {
	return &authpb.RevokeSessionResponse{}, nil
}
func (n *noopAuthClient) RevokeAllSessions(_ context.Context, _ *authpb.RevokeAllSessionsRequest, _ ...grpc.CallOption) (*authpb.RevokeAllSessionsResponse, error) {
	return &authpb.RevokeAllSessionsResponse{}, nil
}
func (n *noopAuthClient) GetLoginHistory(_ context.Context, _ *authpb.LoginHistoryRequest, _ ...grpc.CallOption) (*authpb.LoginHistoryResponse, error) {
	return &authpb.LoginHistoryResponse{}, nil
}
func (n *noopAuthClient) SetBiometricsEnabled(_ context.Context, _ *authpb.SetBiometricsRequest, _ ...grpc.CallOption) (*authpb.SetBiometricsResponse, error) {
	return &authpb.SetBiometricsResponse{}, nil
}
func (n *noopAuthClient) GetBiometricsEnabled(_ context.Context, _ *authpb.GetBiometricsRequest, _ ...grpc.CallOption) (*authpb.GetBiometricsResponse, error) {
	return &authpb.GetBiometricsResponse{}, nil
}
func (n *noopAuthClient) CheckBiometricsEnabled(_ context.Context, _ *authpb.CheckBiometricsRequest, _ ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error) {
	return &authpb.CheckBiometricsResponse{}, nil
}
func (n *noopAuthClient) ResendActivationEmail(_ context.Context, _ *authpb.ResendActivationEmailRequest, _ ...grpc.CallOption) (*authpb.ResendActivationEmailResponse, error) {
	return &authpb.ResendActivationEmailResponse{}, nil
}

type noopUserClient struct{}

func (n *noopUserClient) CreateEmployee(_ context.Context, _ *userpb.CreateEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return &userpb.EmployeeResponse{}, nil
}
func (n *noopUserClient) GetEmployee(_ context.Context, _ *userpb.GetEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return &userpb.EmployeeResponse{}, nil
}
func (n *noopUserClient) ListEmployees(_ context.Context, _ *userpb.ListEmployeesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeesResponse, error) {
	return &userpb.ListEmployeesResponse{}, nil
}
func (n *noopUserClient) UpdateEmployee(_ context.Context, _ *userpb.UpdateEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return &userpb.EmployeeResponse{}, nil
}
func (n *noopUserClient) ListRoles(_ context.Context, _ *userpb.ListRolesRequest, _ ...grpc.CallOption) (*userpb.ListRolesResponse, error) {
	return &userpb.ListRolesResponse{}, nil
}
func (n *noopUserClient) GetRole(_ context.Context, _ *userpb.GetRoleRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return &userpb.RoleResponse{}, nil
}
func (n *noopUserClient) CreateRole(_ context.Context, _ *userpb.CreateRoleRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return &userpb.RoleResponse{}, nil
}
func (n *noopUserClient) UpdateRolePermissions(_ context.Context, _ *userpb.UpdateRolePermissionsRequest, _ ...grpc.CallOption) (*userpb.RoleResponse, error) {
	return &userpb.RoleResponse{}, nil
}
func (n *noopUserClient) AssignPermissionToRole(_ context.Context, _ *userpb.AssignPermissionToRoleRequest, _ ...grpc.CallOption) (*userpb.AssignPermissionToRoleResponse, error) {
	return &userpb.AssignPermissionToRoleResponse{}, nil
}
func (n *noopUserClient) RevokePermissionFromRole(_ context.Context, _ *userpb.RevokePermissionFromRoleRequest, _ ...grpc.CallOption) (*userpb.RevokePermissionFromRoleResponse, error) {
	return &userpb.RevokePermissionFromRoleResponse{}, nil
}
func (n *noopUserClient) ListPermissions(_ context.Context, _ *userpb.ListPermissionsRequest, _ ...grpc.CallOption) (*userpb.ListPermissionsResponse, error) {
	return &userpb.ListPermissionsResponse{}, nil
}
func (n *noopUserClient) SetEmployeeRoles(_ context.Context, _ *userpb.SetEmployeeRolesRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return &userpb.EmployeeResponse{}, nil
}
func (n *noopUserClient) SetEmployeeAdditionalPermissions(_ context.Context, _ *userpb.SetEmployeePermissionsRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return &userpb.EmployeeResponse{}, nil
}
func (n *noopUserClient) ListEmployeeFullNames(_ context.Context, _ *userpb.ListEmployeeFullNamesRequest, _ ...grpc.CallOption) (*userpb.ListEmployeeFullNamesResponse, error) {
	return &userpb.ListEmployeeFullNamesResponse{}, nil
}

type noopClientClient struct{}

func (n *noopClientClient) CreateClient(_ context.Context, _ *clientpb.CreateClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return &clientpb.ClientResponse{}, nil
}
func (n *noopClientClient) GetClient(_ context.Context, _ *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return &clientpb.ClientResponse{}, nil
}
func (n *noopClientClient) GetClientByEmail(_ context.Context, _ *clientpb.GetClientByEmailRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return &clientpb.ClientResponse{}, nil
}
func (n *noopClientClient) ListClients(_ context.Context, _ *clientpb.ListClientsRequest, _ ...grpc.CallOption) (*clientpb.ListClientsResponse, error) {
	return &clientpb.ListClientsResponse{}, nil
}
func (n *noopClientClient) UpdateClient(_ context.Context, _ *clientpb.UpdateClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return &clientpb.ClientResponse{}, nil
}

type noopAccountClient struct{}

func (n *noopAccountClient) CreateAccount(_ context.Context, _ *accountpb.CreateAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) GetAccount(_ context.Context, _ *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) GetAccountByNumber(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) ListAccountsByClient(_ context.Context, _ *accountpb.ListAccountsByClientRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return &accountpb.ListAccountsResponse{}, nil
}
func (n *noopAccountClient) ListAllAccounts(_ context.Context, _ *accountpb.ListAllAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListAccountsResponse, error) {
	return &accountpb.ListAccountsResponse{}, nil
}
func (n *noopAccountClient) UpdateAccountName(_ context.Context, _ *accountpb.UpdateAccountNameRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) UpdateAccountLimits(_ context.Context, _ *accountpb.UpdateAccountLimitsRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) UpdateAccountStatus(_ context.Context, _ *accountpb.UpdateAccountStatusRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) UpdateBalance(_ context.Context, _ *accountpb.UpdateBalanceRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopAccountClient) CreateCompany(_ context.Context, _ *accountpb.CreateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return &accountpb.CompanyResponse{}, nil
}
func (n *noopAccountClient) GetCompany(_ context.Context, _ *accountpb.GetCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return &accountpb.CompanyResponse{}, nil
}
func (n *noopAccountClient) UpdateCompany(_ context.Context, _ *accountpb.UpdateCompanyRequest, _ ...grpc.CallOption) (*accountpb.CompanyResponse, error) {
	return &accountpb.CompanyResponse{}, nil
}
func (n *noopAccountClient) ListCurrencies(_ context.Context, _ *accountpb.ListCurrenciesRequest, _ ...grpc.CallOption) (*accountpb.ListCurrenciesResponse, error) {
	return &accountpb.ListCurrenciesResponse{}, nil
}
func (n *noopAccountClient) GetCurrency(_ context.Context, _ *accountpb.GetCurrencyRequest, _ ...grpc.CallOption) (*accountpb.CurrencyResponse, error) {
	return &accountpb.CurrencyResponse{}, nil
}
func (n *noopAccountClient) GetLedgerEntries(_ context.Context, _ *accountpb.GetLedgerEntriesRequest, _ ...grpc.CallOption) (*accountpb.GetLedgerEntriesResponse, error) {
	return &accountpb.GetLedgerEntriesResponse{}, nil
}
func (n *noopAccountClient) ReserveFunds(_ context.Context, _ *accountpb.ReserveFundsRequest, _ ...grpc.CallOption) (*accountpb.ReserveFundsResponse, error) {
	return &accountpb.ReserveFundsResponse{}, nil
}
func (n *noopAccountClient) ReleaseReservation(_ context.Context, _ *accountpb.ReleaseReservationRequest, _ ...grpc.CallOption) (*accountpb.ReleaseReservationResponse, error) {
	return &accountpb.ReleaseReservationResponse{}, nil
}
func (n *noopAccountClient) PartialSettleReservation(_ context.Context, _ *accountpb.PartialSettleReservationRequest, _ ...grpc.CallOption) (*accountpb.PartialSettleReservationResponse, error) {
	return &accountpb.PartialSettleReservationResponse{}, nil
}
func (n *noopAccountClient) GetReservation(_ context.Context, _ *accountpb.GetReservationRequest, _ ...grpc.CallOption) (*accountpb.GetReservationResponse, error) {
	return &accountpb.GetReservationResponse{}, nil
}
func (n *noopAccountClient) ReserveIncoming(_ context.Context, _ *accountpb.ReserveIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error) {
	return &accountpb.ReserveIncomingResponse{}, nil
}
func (n *noopAccountClient) CommitIncoming(_ context.Context, _ *accountpb.CommitIncomingRequest, _ ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error) {
	return &accountpb.CommitIncomingResponse{}, nil
}
func (n *noopAccountClient) ReleaseIncoming(_ context.Context, _ *accountpb.ReleaseIncomingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error) {
	return &accountpb.ReleaseIncomingResponse{}, nil
}

type noopCardClient struct{}

func (n *noopCardClient) CreateCard(_ context.Context, _ *cardpb.CreateCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopCardClient) GetCard(_ context.Context, _ *cardpb.GetCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopCardClient) ListCardsByAccount(_ context.Context, _ *cardpb.ListCardsByAccountRequest, _ ...grpc.CallOption) (*cardpb.ListCardsResponse, error) {
	return &cardpb.ListCardsResponse{}, nil
}
func (n *noopCardClient) ListCardsByClient(_ context.Context, _ *cardpb.ListCardsByClientRequest, _ ...grpc.CallOption) (*cardpb.ListCardsResponse, error) {
	return &cardpb.ListCardsResponse{}, nil
}
func (n *noopCardClient) BlockCard(_ context.Context, _ *cardpb.BlockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopCardClient) UnblockCard(_ context.Context, _ *cardpb.UnblockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopCardClient) DeactivateCard(_ context.Context, _ *cardpb.DeactivateCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopCardClient) CreateAuthorizedPerson(_ context.Context, _ *cardpb.CreateAuthorizedPersonRequest, _ ...grpc.CallOption) (*cardpb.AuthorizedPersonResponse, error) {
	return &cardpb.AuthorizedPersonResponse{}, nil
}
func (n *noopCardClient) GetAuthorizedPerson(_ context.Context, _ *cardpb.GetAuthorizedPersonRequest, _ ...grpc.CallOption) (*cardpb.AuthorizedPersonResponse, error) {
	return &cardpb.AuthorizedPersonResponse{}, nil
}

type noopTxClient struct{}

func (n *noopTxClient) CreatePayment(_ context.Context, _ *transactionpb.CreatePaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	return &transactionpb.PaymentResponse{}, nil
}
func (n *noopTxClient) ExecutePayment(_ context.Context, _ *transactionpb.ExecutePaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	return &transactionpb.PaymentResponse{}, nil
}
func (n *noopTxClient) GetPayment(_ context.Context, _ *transactionpb.GetPaymentRequest, _ ...grpc.CallOption) (*transactionpb.PaymentResponse, error) {
	return &transactionpb.PaymentResponse{}, nil
}
func (n *noopTxClient) ListPaymentsByAccount(_ context.Context, _ *transactionpb.ListPaymentsByAccountRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentsResponse, error) {
	return &transactionpb.ListPaymentsResponse{}, nil
}
func (n *noopTxClient) ListPaymentsByClient(_ context.Context, _ *transactionpb.ListPaymentsByClientRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentsResponse, error) {
	return &transactionpb.ListPaymentsResponse{}, nil
}
func (n *noopTxClient) CreateTransfer(_ context.Context, _ *transactionpb.CreateTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	return &transactionpb.TransferResponse{}, nil
}
func (n *noopTxClient) ExecuteTransfer(_ context.Context, _ *transactionpb.ExecuteTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	return &transactionpb.TransferResponse{}, nil
}
func (n *noopTxClient) GetTransfer(_ context.Context, _ *transactionpb.GetTransferRequest, _ ...grpc.CallOption) (*transactionpb.TransferResponse, error) {
	return &transactionpb.TransferResponse{}, nil
}
func (n *noopTxClient) ListTransfersByClient(_ context.Context, _ *transactionpb.ListTransfersByClientRequest, _ ...grpc.CallOption) (*transactionpb.ListTransfersResponse, error) {
	return &transactionpb.ListTransfersResponse{}, nil
}
func (n *noopTxClient) CreatePaymentRecipient(_ context.Context, _ *transactionpb.CreatePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	return &transactionpb.PaymentRecipientResponse{}, nil
}
func (n *noopTxClient) GetPaymentRecipient(_ context.Context, _ *transactionpb.GetPaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	return &transactionpb.PaymentRecipientResponse{}, nil
}
func (n *noopTxClient) ListPaymentRecipients(_ context.Context, _ *transactionpb.ListPaymentRecipientsRequest, _ ...grpc.CallOption) (*transactionpb.ListPaymentRecipientsResponse, error) {
	return &transactionpb.ListPaymentRecipientsResponse{}, nil
}
func (n *noopTxClient) UpdatePaymentRecipient(_ context.Context, _ *transactionpb.UpdatePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.PaymentRecipientResponse, error) {
	return &transactionpb.PaymentRecipientResponse{}, nil
}
func (n *noopTxClient) DeletePaymentRecipient(_ context.Context, _ *transactionpb.DeletePaymentRecipientRequest, _ ...grpc.CallOption) (*transactionpb.DeletePaymentRecipientResponse, error) {
	return &transactionpb.DeletePaymentRecipientResponse{}, nil
}

type noopCreditClient struct{}

func (n *noopCreditClient) CreateLoanRequest(_ context.Context, _ *creditpb.CreateLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	return &creditpb.LoanRequestResponse{}, nil
}
func (n *noopCreditClient) GetLoanRequest(_ context.Context, _ *creditpb.GetLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	return &creditpb.LoanRequestResponse{}, nil
}
func (n *noopCreditClient) ListLoanRequests(_ context.Context, _ *creditpb.ListLoanRequestsReq, _ ...grpc.CallOption) (*creditpb.ListLoanRequestsResponse, error) {
	return &creditpb.ListLoanRequestsResponse{}, nil
}
func (n *noopCreditClient) ApproveLoanRequest(_ context.Context, _ *creditpb.ApproveLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanResponse, error) {
	return &creditpb.LoanResponse{}, nil
}
func (n *noopCreditClient) RejectLoanRequest(_ context.Context, _ *creditpb.RejectLoanRequestReq, _ ...grpc.CallOption) (*creditpb.LoanRequestResponse, error) {
	return &creditpb.LoanRequestResponse{}, nil
}
func (n *noopCreditClient) GetLoan(_ context.Context, _ *creditpb.GetLoanReq, _ ...grpc.CallOption) (*creditpb.LoanResponse, error) {
	return &creditpb.LoanResponse{}, nil
}
func (n *noopCreditClient) ListLoansByClient(_ context.Context, _ *creditpb.ListLoansByClientReq, _ ...grpc.CallOption) (*creditpb.ListLoansResponse, error) {
	return &creditpb.ListLoansResponse{}, nil
}
func (n *noopCreditClient) ListAllLoans(_ context.Context, _ *creditpb.ListAllLoansReq, _ ...grpc.CallOption) (*creditpb.ListLoansResponse, error) {
	return &creditpb.ListLoansResponse{}, nil
}
func (n *noopCreditClient) GetInstallmentsByLoan(_ context.Context, _ *creditpb.GetInstallmentsByLoanReq, _ ...grpc.CallOption) (*creditpb.ListInstallmentsResponse, error) {
	return &creditpb.ListInstallmentsResponse{}, nil
}
func (n *noopCreditClient) ListInterestRateTiers(_ context.Context, _ *creditpb.ListInterestRateTiersRequest, _ ...grpc.CallOption) (*creditpb.ListInterestRateTiersResponse, error) {
	return &creditpb.ListInterestRateTiersResponse{}, nil
}
func (n *noopCreditClient) CreateInterestRateTier(_ context.Context, _ *creditpb.CreateInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.InterestRateTierResponse, error) {
	return &creditpb.InterestRateTierResponse{}, nil
}
func (n *noopCreditClient) UpdateInterestRateTier(_ context.Context, _ *creditpb.UpdateInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.InterestRateTierResponse, error) {
	return &creditpb.InterestRateTierResponse{}, nil
}
func (n *noopCreditClient) DeleteInterestRateTier(_ context.Context, _ *creditpb.DeleteInterestRateTierRequest, _ ...grpc.CallOption) (*creditpb.DeleteResponse, error) {
	return &creditpb.DeleteResponse{}, nil
}
func (n *noopCreditClient) ListBankMargins(_ context.Context, _ *creditpb.ListBankMarginsRequest, _ ...grpc.CallOption) (*creditpb.ListBankMarginsResponse, error) {
	return &creditpb.ListBankMarginsResponse{}, nil
}
func (n *noopCreditClient) UpdateBankMargin(_ context.Context, _ *creditpb.UpdateBankMarginRequest, _ ...grpc.CallOption) (*creditpb.BankMarginResponse, error) {
	return &creditpb.BankMarginResponse{}, nil
}
func (n *noopCreditClient) ApplyVariableRateUpdate(_ context.Context, _ *creditpb.ApplyVariableRateUpdateRequest, _ ...grpc.CallOption) (*creditpb.ApplyVariableRateUpdateResponse, error) {
	return &creditpb.ApplyVariableRateUpdateResponse{}, nil
}

type noopEmpLimitClient struct{}

func (n *noopEmpLimitClient) GetEmployeeLimits(_ context.Context, _ *userpb.EmployeeLimitRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return &userpb.EmployeeLimitResponse{}, nil
}
func (n *noopEmpLimitClient) SetEmployeeLimits(_ context.Context, _ *userpb.SetEmployeeLimitsRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return &userpb.EmployeeLimitResponse{}, nil
}
func (n *noopEmpLimitClient) ApplyLimitTemplate(_ context.Context, _ *userpb.ApplyLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.EmployeeLimitResponse, error) {
	return &userpb.EmployeeLimitResponse{}, nil
}
func (n *noopEmpLimitClient) ListLimitTemplates(_ context.Context, _ *userpb.ListLimitTemplatesRequest, _ ...grpc.CallOption) (*userpb.ListLimitTemplatesResponse, error) {
	return &userpb.ListLimitTemplatesResponse{}, nil
}
func (n *noopEmpLimitClient) CreateLimitTemplate(_ context.Context, _ *userpb.CreateLimitTemplateRequest, _ ...grpc.CallOption) (*userpb.LimitTemplateResponse, error) {
	return &userpb.LimitTemplateResponse{}, nil
}

type noopClientLimitClient struct{}

func (n *noopClientLimitClient) GetClientLimits(_ context.Context, _ *clientpb.GetClientLimitRequest, _ ...grpc.CallOption) (*clientpb.ClientLimitResponse, error) {
	return &clientpb.ClientLimitResponse{}, nil
}
func (n *noopClientLimitClient) SetClientLimits(_ context.Context, _ *clientpb.SetClientLimitRequest, _ ...grpc.CallOption) (*clientpb.ClientLimitResponse, error) {
	return &clientpb.ClientLimitResponse{}, nil
}

type noopVirtualCardClient struct{}

func (n *noopVirtualCardClient) CreateVirtualCard(_ context.Context, _ *cardpb.CreateVirtualCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopVirtualCardClient) SetCardPin(_ context.Context, _ *cardpb.SetCardPinRequest, _ ...grpc.CallOption) (*cardpb.SetCardPinResponse, error) {
	return &cardpb.SetCardPinResponse{}, nil
}
func (n *noopVirtualCardClient) VerifyCardPin(_ context.Context, _ *cardpb.VerifyCardPinRequest, _ ...grpc.CallOption) (*cardpb.VerifyCardPinResponse, error) {
	return &cardpb.VerifyCardPinResponse{}, nil
}
func (n *noopVirtualCardClient) TemporaryBlockCard(_ context.Context, _ *cardpb.TemporaryBlockCardRequest, _ ...grpc.CallOption) (*cardpb.CardResponse, error) {
	return &cardpb.CardResponse{}, nil
}
func (n *noopVirtualCardClient) UseCard(_ context.Context, _ *cardpb.UseCardRequest, _ ...grpc.CallOption) (*cardpb.UseCardResponse, error) {
	return &cardpb.UseCardResponse{}, nil
}

type noopBankAccountClient struct{}

func (n *noopBankAccountClient) ListBankAccounts(_ context.Context, _ *accountpb.ListBankAccountsRequest, _ ...grpc.CallOption) (*accountpb.ListBankAccountsResponse, error) {
	return &accountpb.ListBankAccountsResponse{}, nil
}
func (n *noopBankAccountClient) CreateBankAccount(_ context.Context, _ *accountpb.CreateBankAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopBankAccountClient) DeleteBankAccount(_ context.Context, _ *accountpb.DeleteBankAccountRequest, _ ...grpc.CallOption) (*accountpb.DeleteBankAccountResponse, error) {
	return &accountpb.DeleteBankAccountResponse{}, nil
}
func (n *noopBankAccountClient) GetBankRSDAccount(_ context.Context, _ *accountpb.GetBankRSDAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return &accountpb.AccountResponse{}, nil
}
func (n *noopBankAccountClient) DebitBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	return &accountpb.BankAccountOpResponse{}, nil
}
func (n *noopBankAccountClient) CreditBankAccount(_ context.Context, _ *accountpb.BankAccountOpRequest, _ ...grpc.CallOption) (*accountpb.BankAccountOpResponse, error) {
	return &accountpb.BankAccountOpResponse{}, nil
}

type noopFeeClient struct{}

func (n *noopFeeClient) ListFees(_ context.Context, _ *transactionpb.ListFeesRequest, _ ...grpc.CallOption) (*transactionpb.ListFeesResponse, error) {
	return &transactionpb.ListFeesResponse{}, nil
}
func (n *noopFeeClient) CreateFee(_ context.Context, _ *transactionpb.CreateFeeRequest, _ ...grpc.CallOption) (*transactionpb.TransferFeeResponse, error) {
	return &transactionpb.TransferFeeResponse{}, nil
}
func (n *noopFeeClient) UpdateFee(_ context.Context, _ *transactionpb.UpdateFeeRequest, _ ...grpc.CallOption) (*transactionpb.TransferFeeResponse, error) {
	return &transactionpb.TransferFeeResponse{}, nil
}
func (n *noopFeeClient) DeleteFee(_ context.Context, _ *transactionpb.DeleteFeeRequest, _ ...grpc.CallOption) (*transactionpb.DeleteFeeResponse, error) {
	return &transactionpb.DeleteFeeResponse{}, nil
}
func (n *noopFeeClient) CalculateFee(_ context.Context, _ *transactionpb.CalculateFeeRequest, _ ...grpc.CallOption) (*transactionpb.CalculateFeeResponse, error) {
	return &transactionpb.CalculateFeeResponse{}, nil
}

type noopCardRequestClient struct{}

func (n *noopCardRequestClient) CreateCardRequest(_ context.Context, _ *cardpb.CreateCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	return &cardpb.CardRequestResponse{}, nil
}
func (n *noopCardRequestClient) GetCardRequest(_ context.Context, _ *cardpb.GetCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	return &cardpb.CardRequestResponse{}, nil
}
func (n *noopCardRequestClient) ListCardRequests(_ context.Context, _ *cardpb.ListCardRequestsRequest, _ ...grpc.CallOption) (*cardpb.ListCardRequestsResponse, error) {
	return &cardpb.ListCardRequestsResponse{}, nil
}
func (n *noopCardRequestClient) ListCardRequestsByClient(_ context.Context, _ *cardpb.ListCardRequestsByClientRequest, _ ...grpc.CallOption) (*cardpb.ListCardRequestsResponse, error) {
	return &cardpb.ListCardRequestsResponse{}, nil
}
func (n *noopCardRequestClient) ApproveCardRequest(_ context.Context, _ *cardpb.ApproveCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestApprovedResponse, error) {
	return &cardpb.CardRequestApprovedResponse{}, nil
}
func (n *noopCardRequestClient) RejectCardRequest(_ context.Context, _ *cardpb.RejectCardRequestRequest, _ ...grpc.CallOption) (*cardpb.CardRequestResponse, error) {
	return &cardpb.CardRequestResponse{}, nil
}

type noopExchangeClient struct{}

func (n *noopExchangeClient) ListRates(_ context.Context, _ *exchangepb.ListRatesRequest, _ ...grpc.CallOption) (*exchangepb.ListRatesResponse, error) {
	return &exchangepb.ListRatesResponse{}, nil
}
func (n *noopExchangeClient) GetRate(_ context.Context, _ *exchangepb.GetRateRequest, _ ...grpc.CallOption) (*exchangepb.RateResponse, error) {
	return &exchangepb.RateResponse{}, nil
}
func (n *noopExchangeClient) Calculate(_ context.Context, _ *exchangepb.CalculateRequest, _ ...grpc.CallOption) (*exchangepb.CalculateResponse, error) {
	return &exchangepb.CalculateResponse{}, nil
}
func (n *noopExchangeClient) Convert(_ context.Context, _ *exchangepb.ConvertRequest, _ ...grpc.CallOption) (*exchangepb.ConvertResponse, error) {
	return &exchangepb.ConvertResponse{}, nil
}

type noopStockExchangeClient struct{}

func (n *noopStockExchangeClient) ListExchanges(_ context.Context, _ *stockpb.ListExchangesRequest, _ ...grpc.CallOption) (*stockpb.ListExchangesResponse, error) {
	return &stockpb.ListExchangesResponse{}, nil
}
func (n *noopStockExchangeClient) GetExchange(_ context.Context, _ *stockpb.GetExchangeRequest, _ ...grpc.CallOption) (*stockpb.Exchange, error) {
	return &stockpb.Exchange{}, nil
}
func (n *noopStockExchangeClient) SetTestingMode(_ context.Context, _ *stockpb.SetTestingModeRequest, _ ...grpc.CallOption) (*stockpb.SetTestingModeResponse, error) {
	return &stockpb.SetTestingModeResponse{}, nil
}
func (n *noopStockExchangeClient) GetTestingMode(_ context.Context, _ *stockpb.GetTestingModeRequest, _ ...grpc.CallOption) (*stockpb.GetTestingModeResponse, error) {
	return &stockpb.GetTestingModeResponse{}, nil
}

type noopSecurityClient struct{}

func (n *noopSecurityClient) ListStocks(_ context.Context, _ *stockpb.ListStocksRequest, _ ...grpc.CallOption) (*stockpb.ListStocksResponse, error) {
	return &stockpb.ListStocksResponse{}, nil
}
func (n *noopSecurityClient) GetStock(_ context.Context, _ *stockpb.GetStockRequest, _ ...grpc.CallOption) (*stockpb.StockDetail, error) {
	return &stockpb.StockDetail{}, nil
}
func (n *noopSecurityClient) GetStockHistory(_ context.Context, _ *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return &stockpb.PriceHistoryResponse{}, nil
}
func (n *noopSecurityClient) ListFutures(_ context.Context, _ *stockpb.ListFuturesRequest, _ ...grpc.CallOption) (*stockpb.ListFuturesResponse, error) {
	return &stockpb.ListFuturesResponse{}, nil
}
func (n *noopSecurityClient) GetFutures(_ context.Context, _ *stockpb.GetFuturesRequest, _ ...grpc.CallOption) (*stockpb.FuturesDetail, error) {
	return &stockpb.FuturesDetail{}, nil
}
func (n *noopSecurityClient) GetFuturesHistory(_ context.Context, _ *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return &stockpb.PriceHistoryResponse{}, nil
}
func (n *noopSecurityClient) ListForexPairs(_ context.Context, _ *stockpb.ListForexPairsRequest, _ ...grpc.CallOption) (*stockpb.ListForexPairsResponse, error) {
	return &stockpb.ListForexPairsResponse{}, nil
}
func (n *noopSecurityClient) GetForexPair(_ context.Context, _ *stockpb.GetForexPairRequest, _ ...grpc.CallOption) (*stockpb.ForexPairDetail, error) {
	return &stockpb.ForexPairDetail{}, nil
}
func (n *noopSecurityClient) GetForexPairHistory(_ context.Context, _ *stockpb.GetPriceHistoryRequest, _ ...grpc.CallOption) (*stockpb.PriceHistoryResponse, error) {
	return &stockpb.PriceHistoryResponse{}, nil
}
func (n *noopSecurityClient) ListOptions(_ context.Context, _ *stockpb.ListOptionsRequest, _ ...grpc.CallOption) (*stockpb.ListOptionsResponse, error) {
	return &stockpb.ListOptionsResponse{}, nil
}
func (n *noopSecurityClient) GetOption(_ context.Context, _ *stockpb.GetOptionRequest, _ ...grpc.CallOption) (*stockpb.OptionDetail, error) {
	return &stockpb.OptionDetail{}, nil
}
func (n *noopSecurityClient) GetCandles(_ context.Context, _ *stockpb.GetCandlesRequest, _ ...grpc.CallOption) (*stockpb.GetCandlesResponse, error) {
	return &stockpb.GetCandlesResponse{}, nil
}

type noopOrderClient struct{}

func (n *noopOrderClient) CreateOrder(_ context.Context, _ *stockpb.CreateOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	return &stockpb.Order{}, nil
}
func (n *noopOrderClient) GetOrder(_ context.Context, _ *stockpb.GetOrderRequest, _ ...grpc.CallOption) (*stockpb.OrderDetail, error) {
	return &stockpb.OrderDetail{}, nil
}
func (n *noopOrderClient) ListMyOrders(_ context.Context, _ *stockpb.ListMyOrdersRequest, _ ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	return &stockpb.ListOrdersResponse{}, nil
}
func (n *noopOrderClient) CancelOrder(_ context.Context, _ *stockpb.CancelOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	return &stockpb.Order{}, nil
}
func (n *noopOrderClient) ListOrders(_ context.Context, _ *stockpb.ListOrdersRequest, _ ...grpc.CallOption) (*stockpb.ListOrdersResponse, error) {
	return &stockpb.ListOrdersResponse{}, nil
}
func (n *noopOrderClient) ApproveOrder(_ context.Context, _ *stockpb.ApproveOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	return &stockpb.Order{}, nil
}
func (n *noopOrderClient) DeclineOrder(_ context.Context, _ *stockpb.DeclineOrderRequest, _ ...grpc.CallOption) (*stockpb.Order, error) {
	return &stockpb.Order{}, nil
}

type noopPortfolioClient struct{}

func (n *noopPortfolioClient) ListHoldings(_ context.Context, _ *stockpb.ListHoldingsRequest, _ ...grpc.CallOption) (*stockpb.ListHoldingsResponse, error) {
	return &stockpb.ListHoldingsResponse{}, nil
}
func (n *noopPortfolioClient) GetPortfolioSummary(_ context.Context, _ *stockpb.GetPortfolioSummaryRequest, _ ...grpc.CallOption) (*stockpb.PortfolioSummary, error) {
	return &stockpb.PortfolioSummary{}, nil
}
func (n *noopPortfolioClient) MakePublic(_ context.Context, _ *stockpb.MakePublicRequest, _ ...grpc.CallOption) (*stockpb.Holding, error) {
	return &stockpb.Holding{}, nil
}
func (n *noopPortfolioClient) ExerciseOption(_ context.Context, _ *stockpb.ExerciseOptionRequest, _ ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	return &stockpb.ExerciseResult{}, nil
}
func (n *noopPortfolioClient) ExerciseOptionByOptionID(_ context.Context, _ *stockpb.ExerciseOptionByOptionIDRequest, _ ...grpc.CallOption) (*stockpb.ExerciseResult, error) {
	return &stockpb.ExerciseResult{}, nil
}
func (n *noopPortfolioClient) ListHoldingTransactions(_ context.Context, _ *stockpb.ListHoldingTransactionsRequest, _ ...grpc.CallOption) (*stockpb.ListHoldingTransactionsResponse, error) {
	return &stockpb.ListHoldingTransactionsResponse{}, nil
}

type noopOTCClient struct{}

func (n *noopOTCClient) ListOffers(_ context.Context, _ *stockpb.ListOTCOffersRequest, _ ...grpc.CallOption) (*stockpb.ListOTCOffersResponse, error) {
	return &stockpb.ListOTCOffersResponse{}, nil
}
func (n *noopOTCClient) BuyOffer(_ context.Context, _ *stockpb.BuyOTCOfferRequest, _ ...grpc.CallOption) (*stockpb.OTCTransaction, error) {
	return &stockpb.OTCTransaction{}, nil
}

type noopTaxClient struct{}

func (n *noopTaxClient) ListTaxRecords(_ context.Context, _ *stockpb.ListTaxRecordsRequest, _ ...grpc.CallOption) (*stockpb.ListTaxRecordsResponse, error) {
	return &stockpb.ListTaxRecordsResponse{}, nil
}
func (n *noopTaxClient) CollectTax(_ context.Context, _ *stockpb.CollectTaxRequest, _ ...grpc.CallOption) (*stockpb.CollectTaxResponse, error) {
	return &stockpb.CollectTaxResponse{}, nil
}
func (n *noopTaxClient) ListUserTaxRecords(_ context.Context, _ *stockpb.ListUserTaxRecordsRequest, _ ...grpc.CallOption) (*stockpb.ListUserTaxRecordsResponse, error) {
	return &stockpb.ListUserTaxRecordsResponse{}, nil
}

type noopActuaryClient struct{}

func (n *noopActuaryClient) ListActuaries(_ context.Context, _ *userpb.ListActuariesRequest, _ ...grpc.CallOption) (*userpb.ListActuariesResponse, error) {
	return &userpb.ListActuariesResponse{}, nil
}
func (n *noopActuaryClient) GetActuaryInfo(_ context.Context, _ *userpb.GetActuaryInfoRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}
func (n *noopActuaryClient) SetActuaryLimit(_ context.Context, _ *userpb.SetActuaryLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}
func (n *noopActuaryClient) ResetActuaryUsedLimit(_ context.Context, _ *userpb.ResetActuaryUsedLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}
func (n *noopActuaryClient) SetNeedApproval(_ context.Context, _ *userpb.SetNeedApprovalRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}
func (n *noopActuaryClient) UpdateUsedLimit(_ context.Context, _ *userpb.UpdateUsedLimitRequest, _ ...grpc.CallOption) (*userpb.ActuaryInfo, error) {
	return &userpb.ActuaryInfo{}, nil
}

type noopBlueprintClient struct{}

func (n *noopBlueprintClient) CreateBlueprint(_ context.Context, _ *userpb.CreateBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	return &userpb.BlueprintResponse{}, nil
}
func (n *noopBlueprintClient) GetBlueprint(_ context.Context, _ *userpb.GetBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	return &userpb.BlueprintResponse{}, nil
}
func (n *noopBlueprintClient) ListBlueprints(_ context.Context, _ *userpb.ListBlueprintsRequest, _ ...grpc.CallOption) (*userpb.ListBlueprintsResponse, error) {
	return &userpb.ListBlueprintsResponse{}, nil
}
func (n *noopBlueprintClient) UpdateBlueprint(_ context.Context, _ *userpb.UpdateBlueprintRequest, _ ...grpc.CallOption) (*userpb.BlueprintResponse, error) {
	return &userpb.BlueprintResponse{}, nil
}
func (n *noopBlueprintClient) DeleteBlueprint(_ context.Context, _ *userpb.DeleteBlueprintRequest, _ ...grpc.CallOption) (*userpb.DeleteBlueprintResponse, error) {
	return &userpb.DeleteBlueprintResponse{}, nil
}
func (n *noopBlueprintClient) ApplyBlueprint(_ context.Context, _ *userpb.ApplyBlueprintRequest, _ ...grpc.CallOption) (*userpb.ApplyBlueprintResponse, error) {
	return &userpb.ApplyBlueprintResponse{}, nil
}

type noopVerificationClient struct{}

func (n *noopVerificationClient) CreateChallenge(_ context.Context, _ *verificationpb.CreateChallengeRequest, _ ...grpc.CallOption) (*verificationpb.CreateChallengeResponse, error) {
	return &verificationpb.CreateChallengeResponse{}, nil
}
func (n *noopVerificationClient) GetChallengeStatus(_ context.Context, _ *verificationpb.GetChallengeStatusRequest, _ ...grpc.CallOption) (*verificationpb.GetChallengeStatusResponse, error) {
	return &verificationpb.GetChallengeStatusResponse{}, nil
}
func (n *noopVerificationClient) GetPendingChallenge(_ context.Context, _ *verificationpb.GetPendingChallengeRequest, _ ...grpc.CallOption) (*verificationpb.GetPendingChallengeResponse, error) {
	return &verificationpb.GetPendingChallengeResponse{}, nil
}
func (n *noopVerificationClient) SubmitVerification(_ context.Context, _ *verificationpb.SubmitVerificationRequest, _ ...grpc.CallOption) (*verificationpb.SubmitVerificationResponse, error) {
	return &verificationpb.SubmitVerificationResponse{}, nil
}
func (n *noopVerificationClient) SubmitCode(_ context.Context, _ *verificationpb.SubmitCodeRequest, _ ...grpc.CallOption) (*verificationpb.SubmitCodeResponse, error) {
	return &verificationpb.SubmitCodeResponse{}, nil
}
func (n *noopVerificationClient) VerifyByBiometric(_ context.Context, _ *verificationpb.VerifyByBiometricRequest, _ ...grpc.CallOption) (*verificationpb.VerifyByBiometricResponse, error) {
	return &verificationpb.VerifyByBiometricResponse{}, nil
}

type noopNotificationClient struct{}

func (n *noopNotificationClient) SendEmail(_ context.Context, _ *notificationpb.SendEmailRequest, _ ...grpc.CallOption) (*notificationpb.SendEmailResponse, error) {
	return &notificationpb.SendEmailResponse{}, nil
}
func (n *noopNotificationClient) GetDeliveryStatus(_ context.Context, _ *notificationpb.GetDeliveryStatusRequest, _ ...grpc.CallOption) (*notificationpb.GetDeliveryStatusResponse, error) {
	return &notificationpb.GetDeliveryStatusResponse{}, nil
}
func (n *noopNotificationClient) GetPendingMobileItems(_ context.Context, _ *notificationpb.GetPendingMobileRequest, _ ...grpc.CallOption) (*notificationpb.PendingMobileResponse, error) {
	return &notificationpb.PendingMobileResponse{}, nil
}
func (n *noopNotificationClient) AckMobileItem(_ context.Context, _ *notificationpb.AckMobileRequest, _ ...grpc.CallOption) (*notificationpb.AckMobileResponse, error) {
	return &notificationpb.AckMobileResponse{}, nil
}
func (n *noopNotificationClient) ListNotifications(_ context.Context, _ *notificationpb.ListNotificationsRequest, _ ...grpc.CallOption) (*notificationpb.ListNotificationsResponse, error) {
	return &notificationpb.ListNotificationsResponse{}, nil
}
func (n *noopNotificationClient) GetUnreadCount(_ context.Context, _ *notificationpb.GetUnreadCountRequest, _ ...grpc.CallOption) (*notificationpb.GetUnreadCountResponse, error) {
	return &notificationpb.GetUnreadCountResponse{}, nil
}
func (n *noopNotificationClient) MarkNotificationRead(_ context.Context, _ *notificationpb.MarkNotificationReadRequest, _ ...grpc.CallOption) (*notificationpb.MarkNotificationReadResponse, error) {
	return &notificationpb.MarkNotificationReadResponse{}, nil
}
func (n *noopNotificationClient) MarkAllNotificationsRead(_ context.Context, _ *notificationpb.MarkAllNotificationsReadRequest, _ ...grpc.CallOption) (*notificationpb.MarkAllNotificationsReadResponse, error) {
	return &notificationpb.MarkAllNotificationsReadResponse{}, nil
}

type noopSourceAdminClient struct{}

func (n *noopSourceAdminClient) SwitchSource(_ context.Context, _ *stockpb.SwitchSourceRequest, _ ...grpc.CallOption) (*stockpb.SwitchSourceResponse, error) {
	return &stockpb.SwitchSourceResponse{}, nil
}
func (n *noopSourceAdminClient) GetSourceStatus(_ context.Context, _ *stockpb.GetSourceStatusRequest, _ ...grpc.CallOption) (*stockpb.SourceStatus, error) {
	return &stockpb.SourceStatus{}, nil
}

// buildTestRouter creates a minimal but fully wired gin.Engine for route
// registration tests. All gRPC clients are no-ops.
func buildTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	SetupV1Routes(
		r,
		&noopAuthClient{}, &noopUserClient{}, &noopClientClient{},
		&noopAccountClient{}, &noopCardClient{}, &noopTxClient{},
		&noopCreditClient{}, &noopEmpLimitClient{}, &noopClientLimitClient{},
		&noopVirtualCardClient{}, &noopBankAccountClient{}, &noopFeeClient{},
		&noopCardRequestClient{}, &noopExchangeClient{}, &noopStockExchangeClient{},
		&noopSecurityClient{}, &noopOrderClient{}, &noopPortfolioClient{},
		&noopOTCClient{}, &noopTaxClient{}, &noopActuaryClient{},
		&noopBlueprintClient{}, &noopVerificationClient{}, &noopNotificationClient{},
		&noopSourceAdminClient{},
	)
	return r
}

// routeExists returns true if the registered routes include a route with the
// given method and path.
func routeExists(r *gin.Engine, method, path string) bool {
	for _, ri := range r.Routes() {
		if ri.Method == method && ri.Path == path {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// SetupV1Routes — route registration tests
// ---------------------------------------------------------------------------

func TestSetupV1Routes_AuthRoutes(t *testing.T) {
	r := buildTestRouter()
	assert.True(t, routeExists(r, "POST", "/api/v1/auth/login"), "POST /api/v1/auth/login should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/auth/refresh"), "POST /api/v1/auth/refresh should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/auth/logout"), "POST /api/v1/auth/logout should be registered")
}

func TestSetupV1Routes_EmployeeRoutes(t *testing.T) {
	r := buildTestRouter()
	assert.True(t, routeExists(r, "GET", "/api/v1/employees"), "GET /api/v1/employees should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/employees"), "POST /api/v1/employees should be registered")
}

func TestSetupV1Routes_ExchangePublicRoutes(t *testing.T) {
	r := buildTestRouter()
	assert.True(t, routeExists(r, "GET", "/api/v1/exchange/rates"), "GET /api/v1/exchange/rates should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/exchange/calculate"), "POST /api/v1/exchange/calculate should be registered")
}

func TestSetupV1Routes_MeRoutes(t *testing.T) {
	r := buildTestRouter()
	assert.True(t, routeExists(r, "GET", "/api/v1/me"), "GET /api/v1/me should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/me/payments"), "POST /api/v1/me/payments should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/me/transfers"), "POST /api/v1/me/transfers should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v1/me/orders"), "POST /api/v1/me/orders should be registered")
}

func TestSetupV1Routes_PublicRouteResponds(t *testing.T) {
	r := buildTestRouter()
	// Exchange rates is a public route (no auth middleware). A GET request
	// reaches the handler which calls the noop gRPC client → 200.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exchange/rates", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSetupV1Routes_AuthLoginRouteResponds(t *testing.T) {
	r := buildTestRouter()
	// POST /auth/login with empty body → 400 (validation error from handler),
	// which proves the route is registered and the handler runs.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// SetupV2Routes — basic smoke test
// ---------------------------------------------------------------------------

func buildTestV2Router() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := NewRouter()
	SetupV2Routes(
		r,
		&noopAuthClient{}, &noopUserClient{}, &noopClientClient{},
		&noopAccountClient{}, &noopCardClient{}, &noopTxClient{},
		&noopCreditClient{}, &noopEmpLimitClient{}, &noopClientLimitClient{},
		&noopVirtualCardClient{}, &noopBankAccountClient{}, &noopFeeClient{},
		&noopCardRequestClient{}, &noopExchangeClient{}, &noopStockExchangeClient{},
		&noopSecurityClient{}, &noopOrderClient{}, &noopPortfolioClient{},
		&noopOTCClient{}, &noopTaxClient{}, &noopActuaryClient{},
		&noopBlueprintClient{}, &noopVerificationClient{}, &noopNotificationClient{},
		&noopSourceAdminClient{},
	)
	return r
}

func TestSetupV2Routes_CoreRoutesRegistered(t *testing.T) {
	r := buildTestV2Router()
	assert.True(t, routeExists(r, "POST", "/api/v2/auth/login"), "POST /api/v2/auth/login should be registered")
	assert.True(t, routeExists(r, "GET", "/api/v2/employees"), "GET /api/v2/employees should be registered")
	assert.True(t, routeExists(r, "GET", "/api/v2/exchange/rates"), "GET /api/v2/exchange/rates should be registered")
	assert.True(t, routeExists(r, "POST", "/api/v2/me/payments"), "POST /api/v2/me/payments should be registered")
}

func TestSetupV2Routes_V2ExclusiveRoutes(t *testing.T) {
	r := buildTestV2Router()
	// Options routes are v2-only additions (not registered in v1)
	assert.True(t, routeExists(r, "POST", "/api/v2/options/:option_id/orders"), "POST /api/v2/options/:option_id/orders should be v2 only")
}

func TestSetupV2Routes_UnknownPathReturns404(t *testing.T) {
	r := buildTestV2Router()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/does-not-exist-xyz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
