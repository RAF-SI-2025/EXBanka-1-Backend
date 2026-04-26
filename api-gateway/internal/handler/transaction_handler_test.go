package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	accountpb "github.com/exbanka/contract/accountpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

func transactionRouter(h *handler.TransactionHandler, sysType string, uid int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", uid)
		c.Set("system_type", sysType)
		c.Set("email", "x@y.com")
	}
	r.POST("/api/v2/me/payments", withCtx, h.CreatePayment)
	r.GET("/api/v2/payments/:id", withCtx, h.GetPayment)
	r.GET("/api/v2/payments/account/:account_number", withCtx, h.ListPaymentsByAccount)
	r.GET("/api/v2/payments/client/:client_id", withCtx, h.ListPaymentsByClient)
	r.POST("/api/v2/payments/:id/execute", withCtx, h.ExecutePayment)
	r.POST("/api/v2/me/transfers", withCtx, h.CreateTransfer)
	r.POST("/api/v2/transfers/:id/execute", withCtx, h.ExecuteTransfer)
	r.GET("/api/v2/transfers/:id", withCtx, h.GetTransfer)
	r.GET("/api/v2/transfers/client/:client_id", withCtx, h.ListTransfersByClient)
	r.POST("/api/v2/payment-recipients", withCtx, h.CreatePaymentRecipient)
	r.GET("/api/v2/payment-recipients/:client_id", withCtx, h.ListPaymentRecipients)
	r.DELETE("/api/v2/payment-recipients/:id", withCtx, h.DeletePaymentRecipient)
	r.GET("/api/v2/me/payments", withCtx, h.ListMyPayments)
	r.GET("/api/v2/me/payments/:id", withCtx, h.GetMyPayment)
	r.GET("/api/v2/me/transfers", withCtx, h.ListMyTransfers)
	r.GET("/api/v2/me/transfers/:id", withCtx, h.GetMyTransfer)
	r.GET("/api/v2/me/payment-recipients", withCtx, h.ListMyPaymentRecipients)
	r.POST("/api/v2/me/payment-recipients", withCtx, h.CreateMyPaymentRecipient)
	r.GET("/api/v2/payments", withCtx, h.ListPayments)
	r.GET("/api/v2/transfers", withCtx, h.ListTransfers)
	r.GET("/api/v2/fees", withCtx, h.ListFees)
	r.POST("/api/v2/fees", withCtx, h.CreateFee)
	r.PUT("/api/v2/fees/:id", withCtx, h.UpdateFee)
	r.DELETE("/api/v2/fees/:id", withCtx, h.DeleteFee)
	r.POST("/api/v2/transfers/preview", withCtx, h.PreviewTransfer)
	return r
}

func newTxHandler(tx *stubTransactionClient, fee *stubFeeClient, acct *accountFullStub, ex *stubExchangeClient) *handler.TransactionHandler {
	return handler.NewTransactionHandler(tx, fee, acct, ex)
}

func TestTx_CreatePayment_Success(t *testing.T) {
	tx := &stubTransactionClient{
		createPaymentFn: func(req *transactionpb.CreatePaymentRequest) (*transactionpb.PaymentResponse, error) {
			require.Equal(t, "1234", req.FromAccountNumber)
			require.Equal(t, "5678", req.ToAccountNumber)
			require.Equal(t, "289", req.PaymentCode)
			return &transactionpb.PaymentResponse{Id: 99}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1234","to_account_number":"5678","amount":100}`
	req := httptest.NewRequest("POST", "/api/v2/me/payments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestTx_CreatePayment_NegativeAmount(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1234","to_account_number":"5678","amount":-1}`
	req := httptest.NewRequest("POST", "/api/v2/me/payments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "amount must be positive")
}

func TestTx_CreatePayment_SameAccounts(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1234","to_account_number":"1234","amount":1}`
	req := httptest.NewRequest("POST", "/api/v2/me/payments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "must be different")
}

func TestTx_CreatePayment_BadPaymentCode(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1234","to_account_number":"5678","amount":1,"payment_code":"999"}`
	req := httptest.NewRequest("POST", "/api/v2/me/payments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "payment_code")
}

func TestTx_CreatePayment_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `not json`
	req := httptest.NewRequest("POST", "/api/v2/me/payments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_GetPayment_Success(t *testing.T) {
	tx := &stubTransactionClient{
		getPaymentFn: func(req *transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error) {
			require.Equal(t, uint64(99), req.Id)
			return &transactionpb.PaymentResponse{Id: 99}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_GetPayment_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_GetPayment_NotFound(t *testing.T) {
	tx := &stubTransactionClient{
		getPaymentFn: func(*transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error) {
			return nil, status.Error(codes.NotFound, "no")
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTx_ListPaymentsByAccount_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listPmtByAcctFn: func(req *transactionpb.ListPaymentsByAccountRequest) (*transactionpb.ListPaymentsResponse, error) {
			require.Equal(t, "265-1-00", req.AccountNumber)
			return &transactionpb.ListPaymentsResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/account/265-1-00", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListPaymentsByClient_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments/client/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListPaymentsByClient_ClientCantViewOthers(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/client/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestTx_ListPaymentsByClient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listPmtByClientFn: func(req *transactionpb.ListPaymentsByClientRequest) (*transactionpb.ListPaymentsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListPaymentsResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/payments/client/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ExecutePayment_Success(t *testing.T) {
	tx := &stubTransactionClient{
		executePaymentFn: func(req *transactionpb.ExecutePaymentRequest) (*transactionpb.PaymentResponse, error) {
			require.Equal(t, uint64(99), req.PaymentId)
			require.Equal(t, "12345", req.VerificationCode)
			return &transactionpb.PaymentResponse{Id: req.PaymentId}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"verification_code":"12345"}`
	req := httptest.NewRequest("POST", "/api/v2/payments/99/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ExecutePayment_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"verification_code":"12345"}`
	req := httptest.NewRequest("POST", "/api/v2/payments/x/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_CreateTransfer_Success(t *testing.T) {
	acct := &accountFullStub{
		getByNumFn: func(req *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: req.AccountNumber, CurrencyCode: "RSD"}, nil
		},
	}
	tx := &stubTransactionClient{
		createTransferFn: func(req *transactionpb.CreateTransferRequest) (*transactionpb.TransferResponse, error) {
			require.Equal(t, "RSD", req.FromCurrency)
			require.Equal(t, "RSD", req.ToCurrency)
			return &transactionpb.TransferResponse{Id: 99}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1","to_account_number":"2","amount":100}`
	req := httptest.NewRequest("POST", "/api/v2/me/transfers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestTx_CreateTransfer_NegativeAmount(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1","to_account_number":"2","amount":-5}`
	req := httptest.NewRequest("POST", "/api/v2/me/transfers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "amount must be positive")
}

func TestTx_CreateTransfer_FromAccountNotFound(t *testing.T) {
	acct := &accountFullStub{
		getByNumFn: func(*accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return nil, status.Error(codes.NotFound, "")
		},
	}
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"1","to_account_number":"2","amount":1}`
	req := httptest.NewRequest("POST", "/api/v2/me/transfers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "source account not found")
}

func TestTx_ExecuteTransfer_Success(t *testing.T) {
	tx := &stubTransactionClient{
		executeTransferFn: func(req *transactionpb.ExecuteTransferRequest) (*transactionpb.TransferResponse, error) {
			require.Equal(t, uint64(99), req.TransferId)
			require.Equal(t, "12345", req.VerificationCode)
			return &transactionpb.TransferResponse{Id: req.TransferId}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"verification_code":"12345"}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/99/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ExecuteTransfer_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"verification_code":"x"}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/x/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_GetTransfer_Success(t *testing.T) {
	tx := &stubTransactionClient{
		getTransferFn: func(req *transactionpb.GetTransferRequest) (*transactionpb.TransferResponse, error) {
			require.Equal(t, uint64(99), req.Id)
			return &transactionpb.TransferResponse{Id: req.Id}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/transfers/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_GetTransfer_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/transfers/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListTransfersByClient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listTransfersFn: func(req *transactionpb.ListTransfersByClientRequest) (*transactionpb.ListTransfersResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListTransfersResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/transfers/client/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_CreatePaymentRecipient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		createRecipFn: func(req *transactionpb.CreatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			require.Equal(t, "Bob", req.RecipientName)
			return &transactionpb.PaymentRecipientResponse{Id: 1}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"client_id":7,"recipient_name":"Bob","account_number":"265-1-00"}`
	req := httptest.NewRequest("POST", "/api/v2/payment-recipients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestTx_CreatePaymentRecipient_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/payment-recipients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListPaymentRecipients_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listRecipsFn: func(req *transactionpb.ListPaymentRecipientsRequest) (*transactionpb.ListPaymentRecipientsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListPaymentRecipientsResponse{}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payment-recipients/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListPaymentRecipients_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payment-recipients/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_DeletePaymentRecipient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		getRecipFn: func(req *transactionpb.GetPaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			return &transactionpb.PaymentRecipientResponse{Id: req.Id, ClientId: 7}, nil
		},
		deleteRecipFn: func(req *transactionpb.DeletePaymentRecipientRequest) (*transactionpb.DeletePaymentRecipientResponse, error) {
			require.Equal(t, uint64(99), req.Id)
			return &transactionpb.DeletePaymentRecipientResponse{Success: true}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("DELETE", "/api/v2/payment-recipients/99", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_DeletePaymentRecipient_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("DELETE", "/api/v2/payment-recipients/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// /me/* endpoints

func TestTx_ListMyPayments_Success(t *testing.T) {
	acct := &accountFullStub{
		listByClient: func(req *accountpb.ListAccountsByClientRequest) (*accountpb.ListAccountsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &accountpb.ListAccountsResponse{
				Accounts: []*accountpb.AccountResponse{{AccountNumber: "265-1-00"}},
			}, nil
		},
	}
	tx := &stubTransactionClient{
		listPmtByClientFn: func(req *transactionpb.ListPaymentsByClientRequest) (*transactionpb.ListPaymentsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			require.Contains(t, req.AccountNumbers, "265-1-00")
			return &transactionpb.ListPaymentsResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/payments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListMyPayments_BadUserContext(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/payments", func(c *gin.Context) {
		c.Set("user_id", "not-int")
		h.ListMyPayments(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/payments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTx_GetMyPayment_OwnershipMismatch_Returns404(t *testing.T) {
	tx := &stubTransactionClient{
		getPaymentFn: func(*transactionpb.GetPaymentRequest) (*transactionpb.PaymentResponse, error) {
			return &transactionpb.PaymentResponse{Id: 1, ClientId: 99}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/payments/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTx_GetMyPayment_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/payments/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListMyTransfers_Success(t *testing.T) {
	acct := &accountFullStub{
		listByClient: func(req *accountpb.ListAccountsByClientRequest) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{
				Accounts: []*accountpb.AccountResponse{{AccountNumber: "x"}},
			}, nil
		},
	}
	tx := &stubTransactionClient{
		listTransfersFn: func(req *transactionpb.ListTransfersByClientRequest) (*transactionpb.ListTransfersResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListTransfersResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/transfers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListMyTransfers_BadUserContext(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v2/me/transfers", func(c *gin.Context) {
		c.Set("user_id", "x")
		h.ListMyTransfers(c)
	})
	req := httptest.NewRequest("GET", "/api/v2/me/transfers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTx_GetMyTransfer_OwnershipMismatch_Returns404(t *testing.T) {
	tx := &stubTransactionClient{
		getTransferFn: func(*transactionpb.GetTransferRequest) (*transactionpb.TransferResponse, error) {
			return &transactionpb.TransferResponse{Id: 1, ClientId: 99}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/transfers/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTx_GetMyTransfer_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/transfers/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListMyPaymentRecipients_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listRecipsFn: func(req *transactionpb.ListPaymentRecipientsRequest) (*transactionpb.ListPaymentRecipientsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListPaymentRecipientsResponse{}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	req := httptest.NewRequest("GET", "/api/v2/me/payment-recipients", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_CreateMyPaymentRecipient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		createRecipFn: func(req *transactionpb.CreatePaymentRecipientRequest) (*transactionpb.PaymentRecipientResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			require.Equal(t, "Bob", req.RecipientName)
			return &transactionpb.PaymentRecipientResponse{Id: 1}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"recipient_name":"Bob","account_number":"265-1-00"}`
	req := httptest.NewRequest("POST", "/api/v2/me/payment-recipients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestTx_CreateMyPaymentRecipient_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{}`
	req := httptest.NewRequest("POST", "/api/v2/me/payment-recipients", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Fee-related routes
func TestTx_ListFees_Success(t *testing.T) {
	fee := &stubFeeClient{
		listFn: func(*transactionpb.ListFeesRequest) (*transactionpb.ListFeesResponse, error) {
			return &transactionpb.ListFeesResponse{}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/fees", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListFees_GRPCError(t *testing.T) {
	fee := &stubFeeClient{
		listFn: func(*transactionpb.ListFeesRequest) (*transactionpb.ListFeesResponse, error) {
			return nil, status.Error(codes.Internal, "")
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/fees", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestTx_DeleteFee_Success(t *testing.T) {
	fee := &stubFeeClient{
		deleteFn: func(req *transactionpb.DeleteFeeRequest) (*transactionpb.DeleteFeeResponse, error) {
			require.Equal(t, uint64(7), req.Id)
			return &transactionpb.DeleteFeeResponse{}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("DELETE", "/api/v2/fees/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_DeleteFee_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("DELETE", "/api/v2/fees/x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── ListPayments ──────────────────────────────────────────────────────────────

func TestTx_ListPayments_BothFilters(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments?client_id=7&account_number=265", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListPayments_NoFilter(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListPayments_BadClientID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments?client_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListPayments_ByClient_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listPmtByClientFn: func(req *transactionpb.ListPaymentsByClientRequest) (*transactionpb.ListPaymentsResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListPaymentsResponse{Total: 0}, nil
		},
	}
	acct := &accountFullStub{
		listByClient: func(in *accountpb.ListAccountsByClientRequest) (*accountpb.ListAccountsResponse, error) {
			return &accountpb.ListAccountsResponse{Accounts: []*accountpb.AccountResponse{
				{AccountNumber: "265-1-00"},
			}}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments?client_id=7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_ListPayments_ByAccount_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listPmtByAcctFn: func(req *transactionpb.ListPaymentsByAccountRequest) (*transactionpb.ListPaymentsResponse, error) {
			require.Equal(t, "265-1-00", req.AccountNumber)
			return &transactionpb.ListPaymentsResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/payments?account_number=265-1-00", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// ── ListTransfers ─────────────────────────────────────────────────────────────

func TestTx_ListTransfers_NoFilter(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/transfers", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListTransfers_BadClientID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/transfers?client_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_ListTransfers_Success(t *testing.T) {
	tx := &stubTransactionClient{
		listTransfersFn: func(req *transactionpb.ListTransfersByClientRequest) (*transactionpb.ListTransfersResponse, error) {
			require.Equal(t, uint64(7), req.ClientId)
			return &transactionpb.ListTransfersResponse{Total: 0}, nil
		},
	}
	h := newTxHandler(tx, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	req := httptest.NewRequest("GET", "/api/v2/transfers?client_id=7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// ── CreateFee / UpdateFee ─────────────────────────────────────────────────────

func TestTx_CreateFee_Success(t *testing.T) {
	fee := &stubFeeClient{
		createFn: func(req *transactionpb.CreateFeeRequest) (*transactionpb.TransferFeeResponse, error) {
			require.Equal(t, "Standard", req.Name)
			return &transactionpb.TransferFeeResponse{Id: 1, Name: req.Name}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"name":"Standard","fee_type":"percentage","fee_value":"0.1","min_amount":"100","max_fee":"1000","transaction_type":"all","currency_code":"RSD"}`
	req := httptest.NewRequest("POST", "/api/v2/fees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestTx_CreateFee_BadFeeType(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"name":"X","fee_type":"bogus","fee_value":"0.1","transaction_type":"all"}`
	req := httptest.NewRequest("POST", "/api/v2/fees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_CreateFee_BadTxType(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"name":"X","fee_type":"percentage","fee_value":"0.1","transaction_type":"bogus"}`
	req := httptest.NewRequest("POST", "/api/v2/fees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_CreateFee_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{not valid json`
	req := httptest.NewRequest("POST", "/api/v2/fees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_UpdateFee_Success(t *testing.T) {
	fee := &stubFeeClient{
		updateFn: func(req *transactionpb.UpdateFeeRequest) (*transactionpb.TransferFeeResponse, error) {
			require.Equal(t, uint64(5), req.Id)
			return &transactionpb.TransferFeeResponse{Id: req.Id}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"name":"Updated","fee_type":"percentage","fee_value":"0.2","transaction_type":"payment","active":true}`
	req := httptest.NewRequest("PUT", "/api/v2/fees/5", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_UpdateFee_BadID(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/api/v2/fees/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_UpdateFee_BadFeeType(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"fee_type":"bogus","active":true}`
	req := httptest.NewRequest("PUT", "/api/v2/fees/5", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_UpdateFee_BadTxType(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "employee", 1)
	body := `{"fee_type":"percentage","transaction_type":"bogus","active":true}`
	req := httptest.NewRequest("PUT", "/api/v2/fees/5", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── PreviewTransfer ───────────────────────────────────────────────────────────

func TestTx_PreviewTransfer_SameCurrency(t *testing.T) {
	acct := &accountFullStub{
		getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: "RSD"}, nil
		},
	}
	fee := &stubFeeClient{
		calculateFn: func(req *transactionpb.CalculateFeeRequest) (*transactionpb.CalculateFeeResponse, error) {
			return &transactionpb.CalculateFeeResponse{TotalFee: "10.0000"}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"265-1-00","to_account_number":"265-2-00","amount":1000}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_PreviewTransfer_CrossCurrency(t *testing.T) {
	acct := &accountFullStub{
		getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			currency := "RSD"
			if in.AccountNumber == "265-EUR-00" {
				currency = "EUR"
			}
			return &accountpb.AccountResponse{AccountNumber: in.AccountNumber, CurrencyCode: currency}, nil
		},
	}
	fee := &stubFeeClient{
		calculateFn: func(req *transactionpb.CalculateFeeRequest) (*transactionpb.CalculateFeeResponse, error) {
			return &transactionpb.CalculateFeeResponse{TotalFee: "5.0000"}, nil
		},
	}
	h := newTxHandler(&stubTransactionClient{}, fee, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"265-1-00","to_account_number":"265-EUR-00","amount":1000}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTx_PreviewTransfer_BadBody(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{not valid`
	req := httptest.NewRequest("POST", "/api/v2/transfers/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_PreviewTransfer_NegativeAmount(t *testing.T) {
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, &accountFullStub{}, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"265-1-00","to_account_number":"265-2-00","amount":-100}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTx_PreviewTransfer_FromAccountNotFound(t *testing.T) {
	acct := &accountFullStub{
		getByNumFn: func(in *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}
	h := newTxHandler(&stubTransactionClient{}, &stubFeeClient{}, acct, &stubExchangeClient{})
	r := transactionRouter(h, "client", 7)
	body := `{"from_account_number":"265-1-00","to_account_number":"265-2-00","amount":100}`
	req := httptest.NewRequest("POST", "/api/v2/transfers/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
