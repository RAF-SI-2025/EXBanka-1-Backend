package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	accountpb "github.com/exbanka/contract/accountpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

type TransactionHandler struct {
	txClient      transactionpb.TransactionServiceClient
	feeClient     transactionpb.FeeServiceClient
	accountClient accountpb.AccountServiceClient
}

func NewTransactionHandler(txClient transactionpb.TransactionServiceClient, feeClient transactionpb.FeeServiceClient, accountClient accountpb.AccountServiceClient) *TransactionHandler {
	return &TransactionHandler{txClient: txClient, feeClient: feeClient, accountClient: accountClient}
}

// resolveClientAccountNumbers fetches all account numbers belonging to a client from account-service.
func (h *TransactionHandler) resolveClientAccountNumbers(c *gin.Context, clientID uint64) ([]string, error) {
	resp, err := h.accountClient.ListAccountsByClient(c.Request.Context(), &accountpb.ListAccountsByClientRequest{
		ClientId: clientID,
		Page:     1,
		PageSize: 1000,
	})
	if err != nil {
		return nil, err
	}
	numbers := make([]string, 0, len(resp.Accounts))
	for _, acc := range resp.Accounts {
		numbers = append(numbers, acc.AccountNumber)
	}
	return numbers, nil
}

type createPaymentRequest struct {
	FromAccountNumber string  `json:"from_account_number" binding:"required"`
	ToAccountNumber   string  `json:"to_account_number" binding:"required"`
	Amount            float64 `json:"amount" binding:"required"`
	RecipientName     string  `json:"recipient_name"`
	PaymentCode       string  `json:"payment_code"`
	ReferenceNumber   string  `json:"reference_number"`
	PaymentPurpose    string  `json:"payment_purpose"`
}

// @Summary      Create payment
// @Tags         payments
// @Accept       json
// @Produce      json
// @Param        body  body  createPaymentRequest  true  "Payment data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/payments [post]
func (h *TransactionHandler) CreatePayment(c *gin.Context) {
	var req createPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := positive("amount", req.Amount); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := notEqual("from_account_number", req.FromAccountNumber, "to_account_number", req.ToAccountNumber); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.txClient.CreatePayment(c.Request.Context(), &transactionpb.CreatePaymentRequest{
		FromAccountNumber: req.FromAccountNumber,
		ToAccountNumber:   req.ToAccountNumber,
		Amount:            fmt.Sprintf("%.4f", req.Amount),
		RecipientName:     req.RecipientName,
		PaymentCode:       req.PaymentCode,
		ReferenceNumber:   req.ReferenceNumber,
		PaymentPurpose:    req.PaymentPurpose,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, paymentToJSON(resp))
}

// @Summary      Get payment by ID
// @Tags         payments
// @Produce      json
// @Param        id   path  int  true  "Payment ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/payments/{id} [get]
func (h *TransactionHandler) GetPayment(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.txClient.GetPayment(c.Request.Context(), &transactionpb.GetPaymentRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}
	c.JSON(http.StatusOK, paymentToJSON(resp))
}

// @Summary      List payments by account
// @Tags         payments
// @Produce      json
// @Param        account_number  path   string   true   "Account number"
// @Param        page            query  int      false  "Page number (default 1)"
// @Param        page_size       query  int      false  "Items per page (default 20)"
// @Param        date_from       query  string   false  "Start date (RFC3339 or YYYY-MM-DD)"
// @Param        date_to         query  string   false  "End date (RFC3339 or YYYY-MM-DD)"
// @Param        status_filter   query  string   false  "Filter by status"
// @Param        amount_min      query  number   false  "Minimum amount"
// @Param        amount_max      query  number   false  "Maximum amount"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/payments/account/{account_number} [get]
func (h *TransactionHandler) ListPaymentsByAccount(c *gin.Context) {
	accountNumber := c.Param("account_number")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.txClient.ListPaymentsByAccount(c.Request.Context(), &transactionpb.ListPaymentsByAccountRequest{
		AccountNumber: accountNumber,
		DateFrom:      c.Query("date_from"),
		DateTo:        c.Query("date_to"),
		StatusFilter:  c.Query("status_filter"),
		AmountMin:     c.Query("amount_min"),
		AmountMax:     c.Query("amount_max"),
		Page:          int32(page),
		PageSize:      int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	payments := make([]gin.H, 0, len(resp.Payments))
	for _, p := range resp.Payments {
		payments = append(payments, paymentToJSON(p))
	}
	c.JSON(http.StatusOK, gin.H{
		"payments": payments,
		"total":    resp.Total,
	})
}

type createTransferRequest struct {
	FromAccountNumber string  `json:"from_account_number" binding:"required"`
	ToAccountNumber   string  `json:"to_account_number" binding:"required"`
	Amount            float64 `json:"amount" binding:"required"`
}

// @Summary      Create transfer
// @Tags         transfers
// @Accept       json
// @Produce      json
// @Param        body  body  createTransferRequest  true  "Transfer data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/transfers [post]
func (h *TransactionHandler) CreateTransfer(c *gin.Context) {
	var req createTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := positive("amount", req.Amount); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := notEqual("from_account_number", req.FromAccountNumber, "to_account_number", req.ToAccountNumber); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Resolve currencies from account-service
	var fromCurrency, toCurrency string
	fromAcc, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: req.FromAccountNumber})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source account not found"})
		return
	}
	fromCurrency = fromAcc.CurrencyCode
	toAcc, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: req.ToAccountNumber})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination account not found"})
		return
	}
	toCurrency = toAcc.CurrencyCode

	resp, err := h.txClient.CreateTransfer(c.Request.Context(), &transactionpb.CreateTransferRequest{
		FromAccountNumber: req.FromAccountNumber,
		ToAccountNumber:   req.ToAccountNumber,
		Amount:            fmt.Sprintf("%.4f", req.Amount),
		FromCurrency:      fromCurrency,
		ToCurrency:        toCurrency,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, transferToJSON(resp))
}

// @Summary      Get transfer by ID
// @Tags         transfers
// @Produce      json
// @Param        id   path  int  true  "Transfer ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/transfers/{id} [get]
func (h *TransactionHandler) GetTransfer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.txClient.GetTransfer(c.Request.Context(), &transactionpb.GetTransferRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transfer not found"})
		return
	}
	c.JSON(http.StatusOK, transferToJSON(resp))
}

// @Summary      List transfers by client
// @Tags         transfers
// @Produce      json
// @Param        client_id  path   int  true   "Client ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Items per page (default 20)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/transfers/client/{client_id} [get]
func (h *TransactionHandler) ListTransfersByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	if !enforceClientSelf(c, clientID) {
		return
	}

	accountNumbers, err := h.resolveClientAccountNumbers(c, clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve client accounts"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.txClient.ListTransfersByClient(c.Request.Context(), &transactionpb.ListTransfersByClientRequest{
		ClientId:       clientID,
		Page:           int32(page),
		PageSize:       int32(pageSize),
		AccountNumbers: accountNumbers,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	transfers := make([]gin.H, 0, len(resp.Transfers))
	for _, t := range resp.Transfers {
		transfers = append(transfers, transferToJSON(t))
	}
	c.JSON(http.StatusOK, gin.H{
		"transfers": transfers,
		"total":     resp.Total,
	})
}

type createPaymentRecipientRequest struct {
	ClientID      uint64 `json:"client_id" binding:"required"`
	RecipientName string `json:"recipient_name" binding:"required"`
	AccountNumber string `json:"account_number" binding:"required"`
}

// @Summary      Create payment recipient
// @Tags         payment-recipients
// @Accept       json
// @Produce      json
// @Param        body  body  createPaymentRecipientRequest  true  "Recipient data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/payment-recipients [post]
func (h *TransactionHandler) CreatePaymentRecipient(c *gin.Context) {
	var req createPaymentRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.CreatePaymentRecipient(c.Request.Context(), &transactionpb.CreatePaymentRecipientRequest{
		ClientId:      req.ClientID,
		RecipientName: req.RecipientName,
		AccountNumber: req.AccountNumber,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, recipientToJSON(resp))
}

// @Summary      List payment recipients
// @Tags         payment-recipients
// @Produce      json
// @Param        client_id  path  int  true  "Client ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/payment-recipients/{client_id} [get]
func (h *TransactionHandler) ListPaymentRecipients(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}

	resp, err := h.txClient.ListPaymentRecipients(c.Request.Context(), &transactionpb.ListPaymentRecipientsRequest{
		ClientId: clientID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	recipients := make([]gin.H, 0, len(resp.Recipients))
	for _, r := range resp.Recipients {
		recipients = append(recipients, recipientToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"recipients": recipients})
}

type updatePaymentRecipientRequest struct {
	RecipientName *string `json:"recipient_name"`
	AccountNumber *string `json:"account_number"`
}

// @Summary      Update payment recipient
// @Tags         payment-recipients
// @Accept       json
// @Produce      json
// @Param        id    path  int                            true  "Recipient ID"
// @Param        body  body  updatePaymentRecipientRequest  true  "Fields to update"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/payment-recipients/{id} [put]
func (h *TransactionHandler) UpdatePaymentRecipient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updatePaymentRecipientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pbReq := &transactionpb.UpdatePaymentRecipientRequest{Id: id}
	pbReq.RecipientName = req.RecipientName
	pbReq.AccountNumber = req.AccountNumber

	resp, err := h.txClient.UpdatePaymentRecipient(c.Request.Context(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, recipientToJSON(resp))
}

// @Summary      Delete payment recipient
// @Tags         payment-recipients
// @Produce      json
// @Param        id   path  int  true  "Recipient ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/payment-recipients/{id} [delete]
func (h *TransactionHandler) DeletePaymentRecipient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.txClient.DeletePaymentRecipient(c.Request.Context(), &transactionpb.DeletePaymentRecipientRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": resp.Success})
}

type createVerificationCodeRequest struct {
	ClientID        uint64 `json:"client_id" binding:"required"`
	TransactionID   uint64 `json:"transaction_id" binding:"required"`
	TransactionType string `json:"transaction_type" binding:"required"`
}

// @Summary      Create verification code
// @Tags         verification
// @Accept       json
// @Produce      json
// @Param        body  body  createVerificationCodeRequest  true  "Verification request"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/verification [post]
func (h *TransactionHandler) CreateVerificationCode(c *gin.Context) {
	var req createVerificationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	txType, err := oneOf("transaction_type", req.TransactionType, "payment", "transfer")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.CreateVerificationCode(c.Request.Context(), &transactionpb.CreateVerificationCodeRequest{
		ClientId:        req.ClientID,
		TransactionId:   req.TransactionID,
		TransactionType: txType,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"code":       resp.Code,
		"expires_at": resp.ExpiresAt,
	})
}

type validateVerificationCodeRequest struct {
	ClientID      uint64 `json:"client_id" binding:"required"`
	TransactionID uint64 `json:"transaction_id" binding:"required"`
	Code          string `json:"code" binding:"required"`
}

// @Summary      Validate verification code
// @Tags         verification
// @Accept       json
// @Produce      json
// @Param        body  body  validateVerificationCodeRequest  true  "Code to validate"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/verification/validate [post]
func (h *TransactionHandler) ValidateVerificationCode(c *gin.Context) {
	var req validateVerificationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.ValidateVerificationCode(c.Request.Context(), &transactionpb.ValidateVerificationCodeRequest{
		ClientId:      req.ClientID,
		TransactionId: req.TransactionID,
		Code:          req.Code,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": resp.Valid})
}

func paymentToJSON(p *transactionpb.PaymentResponse) gin.H {
	return gin.H{
		"id":                  p.Id,
		"from_account_number": p.FromAccountNumber,
		"to_account_number":   p.ToAccountNumber,
		"initial_amount":      p.InitialAmount,
		"final_amount":        p.FinalAmount,
		"commission":          p.Commission,
		"recipient_name":      p.RecipientName,
		"payment_code":        p.PaymentCode,
		"reference_number":    p.ReferenceNumber,
		"payment_purpose":     p.PaymentPurpose,
		"status":              p.Status,
		"timestamp":           p.Timestamp,
	}
}

func transferToJSON(t *transactionpb.TransferResponse) gin.H {
	return gin.H{
		"id":                  t.Id,
		"from_account_number": t.FromAccountNumber,
		"to_account_number":   t.ToAccountNumber,
		"initial_amount":      t.InitialAmount,
		"final_amount":        t.FinalAmount,
		"exchange_rate":       t.ExchangeRate,
		"commission":          t.Commission,
		"timestamp":           t.Timestamp,
	}
}

func recipientToJSON(r *transactionpb.PaymentRecipientResponse) gin.H {
	return gin.H{
		"id":             r.Id,
		"client_id":      r.ClientId,
		"recipient_name": r.RecipientName,
		"account_number": r.AccountNumber,
		"created_at":     r.CreatedAt,
	}
}

// createFeeBody is the swagger body for creating a fee rule.
type createFeeBody struct {
	Name            string `json:"name" binding:"required" example:"Standard Payment Fee"`
	FeeType         string `json:"fee_type" binding:"required" example:"percentage"`
	FeeValue        string `json:"fee_value" binding:"required" example:"0.1000"`
	MinAmount       string `json:"min_amount" example:"1000.0000"`
	MaxFee          string `json:"max_fee" example:"0.0000"`
	TransactionType string `json:"transaction_type" binding:"required" example:"all"`
	CurrencyCode    string `json:"currency_code" example:""`
}

// updateFeeBody is the swagger body for updating a fee rule.
type updateFeeBody struct {
	Name            string `json:"name" example:"Updated Fee"`
	FeeType         string `json:"fee_type" example:"percentage"`
	FeeValue        string `json:"fee_value" example:"0.2000"`
	MinAmount       string `json:"min_amount" example:"500.0000"`
	MaxFee          string `json:"max_fee" example:"1000.0000"`
	TransactionType string `json:"transaction_type" example:"payment"`
	CurrencyCode    string `json:"currency_code" example:"RSD"`
	Active          bool   `json:"active" example:"true"`
}

// ListFees godoc
// @Summary      List transfer fee rules
// @Description  Returns all configurable fee rules (admin only)
// @Tags         fees
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "fee rules"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/fees [get]
func (h *TransactionHandler) ListFees(c *gin.Context) {
	resp, err := h.feeClient.ListFees(c.Request.Context(), &transactionpb.ListFeesRequest{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// CreateFee godoc
// @Summary      Create fee rule
// @Description  Creates a new configurable fee rule (admin only)
// @Tags         fees
// @Accept       json
// @Produce      json
// @Param        body  body  createFeeBody  true  "Fee rule definition"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created fee rule"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/fees [post]
func (h *TransactionHandler) CreateFee(c *gin.Context) {
	var body createFeeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	feeType, err := oneOf("fee_type", body.FeeType, "percentage", "fixed")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	txType, err := oneOf("transaction_type", body.TransactionType, "payment", "transfer", "all")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.feeClient.CreateFee(c.Request.Context(), &transactionpb.CreateFeeRequest{
		Name:            body.Name,
		FeeType:         feeType,
		FeeValue:        body.FeeValue,
		MinAmount:       body.MinAmount,
		MaxFee:          body.MaxFee,
		TransactionType: txType,
		CurrencyCode:    body.CurrencyCode,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// UpdateFee godoc
// @Summary      Update fee rule
// @Description  Updates an existing fee rule (admin only)
// @Tags         fees
// @Accept       json
// @Produce      json
// @Param        id    path  int             true  "Fee Rule ID"
// @Param        body  body  updateFeeBody   true  "Updated fee rule"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated fee rule"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/fees/{id} [put]
func (h *TransactionHandler) UpdateFee(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body updateFeeBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updFeeType := body.FeeType
	if updFeeType != "" {
		updFeeType, err = oneOf("fee_type", updFeeType, "percentage", "fixed")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	updTxType := body.TransactionType
	if updTxType != "" {
		updTxType, err = oneOf("transaction_type", updTxType, "payment", "transfer", "all")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	resp, err := h.feeClient.UpdateFee(c.Request.Context(), &transactionpb.UpdateFeeRequest{
		Id:              id,
		Name:            body.Name,
		FeeType:         updFeeType,
		FeeValue:        body.FeeValue,
		MinAmount:       body.MinAmount,
		MaxFee:          body.MaxFee,
		TransactionType: updTxType,
		CurrencyCode:    body.CurrencyCode,
		Active:          body.Active,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteFee godoc
// @Summary      Deactivate fee rule
// @Description  Deactivates a fee rule (admin only). Cannot be undone except via update.
// @Tags         fees
// @Produce      json
// @Param        id   path   int  true  "Fee Rule ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "deactivated"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/fees/{id} [delete]
func (h *TransactionHandler) DeleteFee(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	resp, err := h.feeClient.DeleteFee(c.Request.Context(), &transactionpb.DeleteFeeRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}
