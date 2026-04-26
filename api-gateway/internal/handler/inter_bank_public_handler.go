package handler

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	accountpb "github.com/exbanka/contract/accountpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// InterBankPublicHandler serves the v3 public-facing transfer surface that
// is aware of inter-bank routing. POST /api/v3/me/transfers detects the
// inter-bank case from the receiver-account 3-digit prefix; if it matches
// this bank (`ownBankCode`), it forwards to the existing intra-bank
// CreateTransfer logic via TransactionService.CreateTransfer. Otherwise
// it calls InterBankService.InitiateInterBankTransfer.
//
// GET /api/v3/me/transfers/{id} unifies the response shape — looks up the
// id in `transfers` first, then falls back to `inter_bank_transactions`.
type InterBankPublicHandler struct {
	interBank     transactionpb.InterBankServiceClient
	tx            transactionpb.TransactionServiceClient
	accountClient accountpb.AccountServiceClient
	ownBankCode   string
}

// NewInterBankPublicHandler constructs the handler. Reads OWN_BANK_CODE
// from env (default "111") so prefix detection matches transaction-service.
func NewInterBankPublicHandler(
	interBank transactionpb.InterBankServiceClient,
	tx transactionpb.TransactionServiceClient,
	accountClient accountpb.AccountServiceClient,
) *InterBankPublicHandler {
	code := os.Getenv("OWN_BANK_CODE")
	if code == "" {
		code = "111"
	}
	return &InterBankPublicHandler{
		interBank: interBank, tx: tx, accountClient: accountClient,
		ownBankCode: code,
	}
}

// internalCreateTransferReq is the v3 input shape — supports both
// snake_case (intra-bank legacy shape) and camelCase (inter-bank wire
// shape from Spec 3 §7.1) so existing clients keep working.
type internalCreateTransferReq struct {
	FromAccountNumber string  `json:"from_account_number"`
	ToAccountNumber   string  `json:"to_account_number"`
	Amount            float64 `json:"amount"`
	Currency          string  `json:"currency"`

	SenderAccount   string `json:"senderAccount"`
	ReceiverAccount string `json:"receiverAccount"`
	AmountStr       string `json:"amountStr"`
	Memo            string `json:"memo"`
}

// CreateTransfer handles POST /api/v3/me/transfers.
func (h *InterBankPublicHandler) CreateTransfer(c *gin.Context) {
	var req internalCreateTransferReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	from := firstNonEmpty(req.FromAccountNumber, req.SenderAccount)
	to := firstNonEmpty(req.ToAccountNumber, req.ReceiverAccount)
	if from == "" || to == "" {
		apiError(c, 400, ErrValidation, "from_account_number and to_account_number are required")
		return
	}
	if len(to) < 3 {
		apiError(c, 400, ErrValidation, "to_account_number too short")
		return
	}
	prefix := to[:3]

	if prefix == h.ownBankCode {
		// Intra-bank — forward to the existing TransactionService path.
		fromAcc, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: from})
		if err != nil {
			apiError(c, 400, ErrValidation, "source account not found")
			return
		}
		toAcc, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: to})
		if err != nil {
			apiError(c, 400, ErrValidation, "destination account not found")
			return
		}
		amount := h.resolveAmount(req)
		if amount <= 0 {
			apiError(c, 400, ErrValidation, "amount must be positive")
			return
		}
		userID, _ := c.Get("user_id")
		uid, _ := userID.(int64)
		emailVal, _ := c.Get("email")
		clientEmail, _ := emailVal.(string)
		resp, err := h.tx.CreateTransfer(c.Request.Context(), &transactionpb.CreateTransferRequest{
			FromAccountNumber: from,
			ToAccountNumber:   to,
			Amount:            fmt.Sprintf("%.4f", amount),
			FromCurrency:      fromAcc.CurrencyCode,
			ToCurrency:        toAcc.CurrencyCode,
			ClientId:          uint64(uid),
			ClientEmail:       clientEmail,
		})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		c.JSON(http.StatusCreated, transferToJSON(resp))
		return
	}

	// Inter-bank.
	fromAcc, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: from})
	if err != nil {
		apiError(c, 400, ErrValidation, "source account not found")
		return
	}
	currency := req.Currency
	if currency == "" {
		currency = fromAcc.CurrencyCode
	}
	amount := h.resolveAmount(req)
	if amount <= 0 {
		apiError(c, 400, ErrValidation, "amount must be positive")
		return
	}
	resp, err := h.interBank.InitiateInterBankTransfer(c.Request.Context(), &transactionpb.InitiateInterBankRequest{
		SenderAccountNumber:   from,
		ReceiverAccountNumber: to,
		Amount:                fmt.Sprintf("%.4f", amount),
		Currency:              currency,
		Memo:                  req.Memo,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"transactionId": resp.TransactionId,
		"status":        resp.Status,
		"errorReason":   resp.ErrorReason,
		"pollUrl":       fmt.Sprintf("/api/v3/me/transfers/%s", resp.TransactionId),
	})
}

func (h *InterBankPublicHandler) resolveAmount(req internalCreateTransferReq) float64 {
	if req.Amount > 0 {
		return req.Amount
	}
	if req.AmountStr != "" {
		if v, err := strconv.ParseFloat(req.AmountStr, 64); err == nil {
			return v
		}
	}
	return 0
}

// GetTransferByID handles GET /api/v3/me/transfers/{id}. The id can be a
// numeric intra-bank transfer id or a UUID inter-bank transactionId — we
// try numeric lookup first, then UUID fallback.
func (h *InterBankPublicHandler) GetTransferByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apiError(c, 400, ErrValidation, "id is required")
		return
	}
	if numID, err := strconv.ParseUint(id, 10, 64); err == nil {
		intra, ierr := h.tx.GetTransfer(c.Request.Context(), &transactionpb.GetTransferRequest{Id: numID})
		if ierr == nil {
			c.JSON(http.StatusOK, transferToJSON(intra))
			return
		}
	}
	resp, err := h.interBank.GetInterBankTransfer(c.Request.Context(), &transactionpb.GetInterBankTransferRequest{TransactionId: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if !resp.Found {
		apiError(c, 404, "not_found", "transfer not found")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transactionId":   resp.TransactionId,
		"kind":            kindFromRole(resp.Role),
		"role":            resp.Role,
		"status":          resp.Status,
		"remoteBankCode":  resp.RemoteBankCode,
		"senderAccount":   resp.SenderAccount,
		"receiverAccount": resp.ReceiverAccount,
		"amount":          resp.AmountNative,
		"currency":        resp.CurrencyNative,
		"finalAmount":     resp.AmountFinal,
		"finalCurrency":   resp.CurrencyFinal,
		"fxRate":          resp.FxRate,
		"fees":            resp.FeesFinal,
		"errorReason":     resp.ErrorReason,
		"createdAt":       resp.CreatedAt,
		"updatedAt":       resp.UpdatedAt,
	})
}

func kindFromRole(role string) string {
	switch role {
	case "sender":
		return "inter_bank_out"
	case "receiver":
		return "inter_bank_in"
	default:
		return "inter_bank"
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
