package handler

// dividend_handler.go — E4.4 (Plan E, 2026-05-28)
//
// Four routes:
//   POST   /api/v3/admin/dividends                  — declare a dividend (perm: securities.manage.catalog)
//   POST   /api/v3/admin/dividends/:id/payout        — trigger payout   (perm: securities.manage.catalog)
//   GET    /api/v3/me/dividends                      — caller's dividend history  (AnyAuth)
//   GET    /api/v3/investment-funds/:id/dividends    — fund's dividend history    (AnyAuth)

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

// DividendHandler exposes the E4 dividend endpoints.
type DividendHandler struct {
	client stockpb.InvestmentFundServiceClient
}

// NewDividendHandler constructs a DividendHandler.
func NewDividendHandler(client stockpb.InvestmentFundServiceClient) *DividendHandler {
	return &DividendHandler{client: client}
}

type declareDividendRequest struct {
	SecurityID        uint64 `json:"security_id"`
	Ticker            string `json:"ticker"`
	AmountPerShareRSD string `json:"amount_per_share_rsd"`
	PaymentDate       string `json:"payment_date"` // "2026-06-15"
}

// DeclareDividend godoc
// @Summary      Declare a dividend for a security
// @Description  Admin creates a declared DividendPayment row. Idempotent on (security_id, payment_date).
// @Tags         Dividends
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body declareDividendRequest true "dividend declaration"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/admin/dividends [post]
func (h *DividendHandler) DeclareDividend(c *gin.Context) {
	var req declareDividendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid request body")
		return
	}
	if req.SecurityID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "security_id is required")
		return
	}
	if req.Ticker == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "ticker is required")
		return
	}
	if req.AmountPerShareRSD == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "amount_per_share_rsd is required")
		return
	}
	if req.PaymentDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "payment_date is required (YYYY-MM-DD)")
		return
	}

	actorID := c.GetInt64("principal_id")
	resp, err := h.client.DeclareDividend(c.Request.Context(), &stockpb.DeclareDividendRequest{
		DeclaredByEmployeeId: actorID,
		SecurityId:           req.SecurityID,
		Ticker:               req.Ticker,
		AmountPerShareRsd:    req.AmountPerShareRSD,
		PaymentDate:          req.PaymentDate,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"dividend_payment": resp})
}

// PayoutDividend godoc
// @Summary      Trigger dividend payout
// @Description  Fans out dividend credits to all holders of the security. Idempotent.
// @Tags         Dividends
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "dividend_payment_id"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/admin/dividends/{id}/payout [post]
func (h *DividendHandler) PayoutDividend(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	resp, err := h.client.PayoutDividend(c.Request.Context(), &stockpb.PayoutDividendRequest{
		DividendPaymentId: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"payouts_created":  resp.GetPayoutsCreated(),
		"fund_payouts":     resp.GetFundPayouts(),
		"total_amount_rsd": resp.GetTotalAmountRsd(),
	})
}

// ListMyDividends godoc
// @Summary      List the caller's dividend history
// @Description  Returns paginated dividend_payouts for the caller's holdings, most-recent first.
// @Tags         Dividends
// @Security     BearerAuth
// @Produce      json
// @Param        page query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Router       /api/v3/me/dividends [get]
func (h *DividendHandler) ListMyDividends(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var ownerID uint64
	if identity.OwnerID != nil {
		ownerID = *identity.OwnerID
	}
	resp, err := h.client.ListMyDividends(c.Request.Context(), &stockpb.ListMyDividendsRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   ownerID,
		Page:      int32(page),
		PageSize:  int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"payouts": resp.GetPayouts(), "total": resp.GetTotal()})
}

// ListFundDividends godoc
// @Summary      List an investment fund's dividend history
// @Description  Returns paginated fund_dividend_payments for the fund, most-recent first.
// @Tags         Dividends
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "fund id"
// @Param        page query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Router       /api/v3/investment-funds/{id}/dividends [get]
func (h *DividendHandler) ListFundDividends(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid fund id")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListFundDividends(c.Request.Context(), &stockpb.ListFundDividendsRequest{
		FundId:   id,
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"payments": resp.GetPayments(), "total": resp.GetTotal()})
}
