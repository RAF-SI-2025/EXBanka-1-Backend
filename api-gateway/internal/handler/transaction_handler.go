package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

type TransactionHandler struct {
	txClient transactionpb.TransactionServiceClient
}

func NewTransactionHandler(txClient transactionpb.TransactionServiceClient) *TransactionHandler {
	return &TransactionHandler{txClient: txClient}
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

func (h *TransactionHandler) CreatePayment(c *gin.Context) {
	var req createPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.CreatePayment(c.Request.Context(), &transactionpb.CreatePaymentRequest{
		FromAccountNumber: req.FromAccountNumber,
		ToAccountNumber:   req.ToAccountNumber,
		Amount:            req.Amount,
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

func (h *TransactionHandler) ListPaymentsByAccount(c *gin.Context) {
	accountNumber := c.Param("account_number")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	amountMin, _ := strconv.ParseFloat(c.Query("amount_min"), 64)
	amountMax, _ := strconv.ParseFloat(c.Query("amount_max"), 64)

	resp, err := h.txClient.ListPaymentsByAccount(c.Request.Context(), &transactionpb.ListPaymentsByAccountRequest{
		AccountNumber: accountNumber,
		DateFrom:      c.Query("date_from"),
		DateTo:        c.Query("date_to"),
		StatusFilter:  c.Query("status_filter"),
		AmountMin:     amountMin,
		AmountMax:     amountMax,
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

func (h *TransactionHandler) CreateTransfer(c *gin.Context) {
	var req createTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.CreateTransfer(c.Request.Context(), &transactionpb.CreateTransferRequest{
		FromAccountNumber: req.FromAccountNumber,
		ToAccountNumber:   req.ToAccountNumber,
		Amount:            req.Amount,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, transferToJSON(resp))
}

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

func (h *TransactionHandler) ListTransfersByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.txClient.ListTransfersByClient(c.Request.Context(), &transactionpb.ListTransfersByClientRequest{
		ClientId: clientID,
		Page:     int32(page),
		PageSize: int32(pageSize),
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

func (h *TransactionHandler) CreateVerificationCode(c *gin.Context) {
	var req createVerificationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.txClient.CreateVerificationCode(c.Request.Context(), &transactionpb.CreateVerificationCodeRequest{
		ClientId:        req.ClientID,
		TransactionId:   req.TransactionID,
		TransactionType: req.TransactionType,
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
