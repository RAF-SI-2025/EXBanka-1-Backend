package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
)

// InvestmentFundHandler handles REST routes for the investment-funds feature
// (Celina 4). Each method delegates to the stock-service gRPC
// InvestmentFundService.
type InvestmentFundHandler struct {
	client stockpb.InvestmentFundServiceClient
}

func NewInvestmentFundHandler(client stockpb.InvestmentFundServiceClient) *InvestmentFundHandler {
	return &InvestmentFundHandler{client: client}
}

type createFundRequest struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	MinimumContributionRSD string `json:"minimum_contribution_rsd"`
}

// CreateFund godoc
// @Summary      Create a new investment fund
// @Description  Supervisor or admin creates a fund. Triggers an account-service call to provision the fund's RSD account.
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createFundRequest true "fund details"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v1/investment-funds [post]
func (h *InvestmentFundHandler) CreateFund(c *gin.Context) {
	var req createFundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Name == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "name is required")
		return
	}
	actorID := c.GetInt64("principal_id")
	resp, err := h.client.CreateFund(c.Request.Context(), &stockpb.CreateFundRequest{
		ActorEmployeeId:        actorID,
		Name:                   req.Name,
		Description:            req.Description,
		MinimumContributionRsd: req.MinimumContributionRSD,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"fund": resp})
}

// ListFunds godoc
// @Summary      List investment funds
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Param        page query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
// @Param        search query string false "case-insensitive name substring"
// @Param        active_only query bool false "filter to active funds"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/investment-funds [get]
func (h *InvestmentFundHandler) ListFunds(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")
	activeOnly := c.Query("active_only") == "true"
	resp, err := h.client.ListFunds(c.Request.Context(), &stockpb.ListFundsRequest{
		Page: int32(page), PageSize: int32(pageSize), Search: search, ActiveOnly: activeOnly,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"funds": resp.Funds, "total": resp.Total})
}

// GetFund godoc
// @Summary      Get investment fund detail
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "fund id"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v1/investment-funds/{id} [get]
func (h *InvestmentFundHandler) GetFund(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	resp, err := h.client.GetFund(c.Request.Context(), &stockpb.GetFundRequest{FundId: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type updateFundRequest struct {
	Name                   *string `json:"name,omitempty"`
	Description            *string `json:"description,omitempty"`
	MinimumContributionRSD *string `json:"minimum_contribution_rsd,omitempty"`
	Active                 *bool   `json:"active,omitempty"`
}

// UpdateFund godoc
// @Summary      Update an investment fund
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "fund id"
// @Param        body body updateFundRequest true "fields to change (omitted = unchanged)"
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v1/investment-funds/{id} [put]
func (h *InvestmentFundHandler) UpdateFund(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req updateFundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	actorID := c.GetInt64("principal_id")
	in := &stockpb.UpdateFundRequest{ActorEmployeeId: actorID, FundId: id}
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.Description != nil {
		in.Description = *req.Description
	}
	if req.MinimumContributionRSD != nil {
		in.MinimumContributionRsd = *req.MinimumContributionRSD
	}
	if req.Active != nil {
		in.ActiveSet = true
		in.Active = *req.Active
	}
	resp, err := h.client.UpdateFund(c.Request.Context(), in)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fund": resp})
}

type investRequest struct {
	SourceAccountID uint64 `json:"source_account_id"`
	Amount          string `json:"amount"`
	Currency        string `json:"currency"`
	OnBehalfOfType  string `json:"on_behalf_of_type"`
}

// Invest godoc
// @Summary      Invest in an investment fund
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "fund id"
// @Param        body body investRequest true "invest payload"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v1/investment-funds/{id}/invest [post]
func (h *InvestmentFundHandler) Invest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req investRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.SourceAccountID == 0 || req.Amount == "" || req.Currency == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "source_account_id, amount and currency required")
		return
	}
	if req.OnBehalfOfType == "" {
		req.OnBehalfOfType = "self"
	}
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.InvestInFund(c.Request.Context(), &stockpb.InvestInFundRequest{
		FundId:          id,
		ActorUserId:     uid,
		ActorSystemType: st,
		SourceAccountId: req.SourceAccountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		OnBehalfOf:      &stockpb.OnBehalfOf{Type: req.OnBehalfOfType},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"contribution": resp})
}

type redeemRequest struct {
	AmountRSD       string `json:"amount_rsd"`
	TargetAccountID uint64 `json:"target_account_id"`
	OnBehalfOfType  string `json:"on_behalf_of_type"`
}

// Redeem godoc
// @Summary      Redeem from an investment fund
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "fund id"
// @Param        body body redeemRequest true "redeem payload"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v1/investment-funds/{id}/redeem [post]
func (h *InvestmentFundHandler) Redeem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req redeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.AmountRSD == "" || req.TargetAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "amount_rsd and target_account_id required")
		return
	}
	if req.OnBehalfOfType == "" {
		req.OnBehalfOfType = "self"
	}
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.RedeemFromFund(c.Request.Context(), &stockpb.RedeemFromFundRequest{
		FundId:          id,
		ActorUserId:     uid,
		ActorSystemType: st,
		AmountRsd:       req.AmountRSD,
		TargetAccountId: req.TargetAccountID,
		OnBehalfOf:      &stockpb.OnBehalfOf{Type: req.OnBehalfOfType},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"contribution": resp})
}

// ListMyPositions godoc
// @Summary      List the caller's investment-fund positions
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/me/investment-funds [get]
func (h *InvestmentFundHandler) ListMyPositions(c *gin.Context) {
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.ListMyPositions(c.Request.Context(), &stockpb.ListMyPositionsRequest{
		ActorUserId: uid, ActorSystemType: st,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"positions": resp.Positions})
}

// ListBankPositions godoc
// @Summary      List the bank's investment-fund positions
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/investment-funds/positions [get]
func (h *InvestmentFundHandler) ListBankPositions(c *gin.Context) {
	resp, err := h.client.ListBankPositions(c.Request.Context(), &stockpb.ListBankPositionsRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"positions": resp.Positions})
}

// ActuaryPerformance godoc
// @Summary      Actuary performance — realised profit by acting employee
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/actuaries/performance [get]
func (h *InvestmentFundHandler) ActuaryPerformance(c *gin.Context) {
	resp, err := h.client.GetActuaryPerformance(c.Request.Context(), &stockpb.GetActuaryPerformanceRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"actuaries": resp.Actuaries})
}
