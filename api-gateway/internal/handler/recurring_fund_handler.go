package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

type RecurringFundHandler struct {
	client stockpb.RecurringFundServiceClient
}

func NewRecurringFundHandler(client stockpb.RecurringFundServiceClient) *RecurringFundHandler {
	return &RecurringFundHandler{client: client}
}

type createRecurringFundRequest struct {
	FundID          uint64 `json:"fund_id"`
	AmountRSD       string `json:"amount_rsd"`
	SourceAccountID uint64 `json:"source_account_id"`
	DayOfMonth      int32  `json:"day_of_month"`
}

// Create godoc
// @Summary      Create a monthly DCA fund-investment template
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createRecurringFundRequest true "template"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-funds [post]
func (h *RecurringFundHandler) Create(c *gin.Context) {
	var req createRecurringFundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.FundID == 0 || req.SourceAccountID == 0 || req.AmountRSD == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "fund_id, source_account_id, amount_rsd are required")
		return
	}
	if req.DayOfMonth < 1 || req.DayOfMonth > 28 {
		apiError(c, http.StatusBadRequest, ErrValidation, "day_of_month must be 1..28")
		return
	}
	clientID := callerClientID(c)
	if clientID == 0 {
		apiError(c, http.StatusForbidden, ErrForbidden, "only clients can create recurring fund investments")
		return
	}
	resp, err := h.client.Create(c.Request.Context(), &stockpb.CreateRecurringFundRequest{
		ClientId:        clientID,
		FundId:          req.FundID,
		AmountRsd:       req.AmountRSD,
		SourceAccountId: req.SourceAccountID,
		DayOfMonth:      req.DayOfMonth,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"recurring_fund": resp})
}

// Get godoc
// @Summary      Get a recurring fund investment
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-funds/{id} [get]
func (h *RecurringFundHandler) Get(c *gin.Context) {
	id, ok := parseRecurringFundID(c)
	if !ok {
		return
	}
	clientID := callerClientID(c)
	resp, err := h.client.Get(c.Request.Context(), &stockpb.GetRecurringFundRequest{Id: id, ClientId: clientID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_fund": resp})
}

// Pause godoc
// @Summary      Pause a recurring fund investment
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-funds/{id}/pause [post]
func (h *RecurringFundHandler) Pause(c *gin.Context) {
	id, ok := parseRecurringFundID(c)
	if !ok {
		return
	}
	clientID := callerClientID(c)
	resp, err := h.client.Pause(c.Request.Context(), &stockpb.GetRecurringFundRequest{Id: id, ClientId: clientID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_fund": resp})
}

// Resume godoc
// @Summary      Resume a paused recurring fund investment
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-funds/{id}/resume [post]
func (h *RecurringFundHandler) Resume(c *gin.Context) {
	id, ok := parseRecurringFundID(c)
	if !ok {
		return
	}
	clientID := callerClientID(c)
	resp, err := h.client.Resume(c.Request.Context(), &stockpb.GetRecurringFundRequest{Id: id, ClientId: clientID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_fund": resp})
}

// Cancel godoc
// @Summary      Cancel a recurring fund investment
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "id"
// @Success      204 {string} string ""
// @Router       /api/v3/me/recurring-funds/{id} [delete]
func (h *RecurringFundHandler) Cancel(c *gin.Context) {
	id, ok := parseRecurringFundID(c)
	if !ok {
		return
	}
	clientID := callerClientID(c)
	if _, err := h.client.Cancel(c.Request.Context(), &stockpb.GetRecurringFundRequest{Id: id, ClientId: clientID}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMy godoc
// @Summary      List my recurring fund investments
// @Tags         RecurringFunds
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/recurring-funds [get]
func (h *RecurringFundHandler) ListMy(c *gin.Context) {
	clientID := callerClientID(c)
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyRecurringFundsRequest{ClientId: clientID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recurring_funds": resp.Items})
}

func parseRecurringFundID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return 0, false
	}
	return id, true
}

// callerClientID extracts the caller's principal_id when the JWT is a
// client token. Employees get 0 (recurring fund investments are
// personal to clients).
func callerClientID(c *gin.Context) uint64 {
	identity, ok := c.Get("identity")
	if ok {
		if id, ok := identity.(*middleware.ResolvedIdentity); ok && id.OwnerType == "client" && id.OwnerID != nil {
			return *id.OwnerID
		}
	}
	pid, _ := c.Get("principal_id")
	if v, ok := pid.(int64); ok && v > 0 {
		return uint64(v)
	}
	return 0
}
