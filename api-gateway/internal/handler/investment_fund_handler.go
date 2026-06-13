package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
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
	DividendMode           string `json:"dividend_mode"` // payout|reinvest; "" defaults to payout (SP4)
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
	dividendMode := req.DividendMode
	if dividendMode != "" {
		if _, err := oneOf("dividend_mode", dividendMode, "payout", "reinvest"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
		dividendMode, _ = oneOf("dividend_mode", dividendMode, "payout", "reinvest")
	}
	actorID := c.GetInt64("principal_id")
	resp, err := h.client.CreateFund(c.Request.Context(), &stockpb.CreateFundRequest{
		ActorEmployeeId:        actorID,
		Name:                   req.Name,
		Description:            req.Description,
		MinimumContributionRsd: req.MinimumContributionRSD,
		DividendMode:           dividendMode,
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
// @Param        sort_by query string false "name|value|profit|annualized_return|volatility|reward_to_variability|max_drawdown"
// @Param        sort_order query string false "asc|desc"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v1/investment-funds [get]
func (h *InvestmentFundHandler) ListFunds(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")
	activeOnly := c.Query("active_only") == "true"

	// SP3 sorting (optional). Validate against the allowed metric/base keys.
	sortBy := c.Query("sort_by")
	if sortBy != "" {
		if _, err := oneOf("sort_by", sortBy, "name", "value", "profit", "annualized_return", "volatility", "reward_to_variability", "max_drawdown"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
		sortBy, _ = oneOf("sort_by", sortBy, "name", "value", "profit", "annualized_return", "volatility", "reward_to_variability", "max_drawdown")
	}
	sortOrder := c.Query("sort_order")
	if sortOrder != "" {
		if _, err := oneOf("sort_order", sortOrder, "asc", "desc"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
		sortOrder, _ = oneOf("sort_order", sortOrder, "asc", "desc")
	}

	resp, err := h.client.ListFunds(c.Request.Context(), &stockpb.ListFundsRequest{
		Page: int32(page), PageSize: int32(pageSize), Search: search, ActiveOnly: activeOnly,
		SortBy: sortBy, SortOrder: sortOrder,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	// Hand-shape so every field is present (esp. metrics_available=false, which
	// the raw proto omits) while keeping numeric ids as JSON numbers.
	out := make([]gin.H, 0, len(resp.Funds))
	for _, f := range resp.Funds {
		out = append(out, fundRespToJSON(f))
	}
	c.JSON(http.StatusOK, gin.H{"funds": out, "total": resp.Total})
}

// fundRespToJSON renders a FundResponse with all fields present (so discovery
// cards can rely on metrics_available/dividend_mode always being there) and
// numeric ids as numbers (matching the detail endpoint).
func fundRespToJSON(f *stockpb.FundResponse) gin.H {
	return gin.H{
		"id":                       f.GetId(),
		"name":                     f.GetName(),
		"description":              f.GetDescription(),
		"manager_employee_id":      f.GetManagerEmployeeId(),
		"manager_full_name":        f.GetManagerFullName(),
		"minimum_contribution_rsd": f.GetMinimumContributionRsd(),
		"rsd_account_id":           f.GetRsdAccountId(),
		"rsd_account_number":       f.GetRsdAccountNumber(),
		"active":                   f.GetActive(),
		"created_at":               f.GetCreatedAt(),
		"updated_at":               f.GetUpdatedAt(),
		"value_rsd":                f.GetValueRsd(),
		"liquid_rsd":               f.GetLiquidRsd(),
		"profit_rsd":               f.GetProfitRsd(),
		"annualized_return_pct":    f.GetAnnualizedReturnPct(),
		"volatility_pct":           f.GetVolatilityPct(),
		"reward_to_variability":    f.GetRewardToVariability(),
		"max_drawdown_pct":         f.GetMaxDrawdownPct(),
		"metrics_available":        f.GetMetricsAvailable(),
		"dividend_mode":            f.GetDividendMode(),
	}
}

// GetFund godoc
// @Summary      Get investment fund detail (enriched — E1)
// @Description  Returns the fund's basic fields plus computed statistics: investor_count, total_contributed_rsd, liquid_rsd_balance, total_holdings_value_rsd, total_value_rsd, total_dividends_paid_rsd (always "0.00" until E4), profit_rsd, profit_pct, and a holdings[] array with current_value_rsd per position.
// @Tags         InvestmentFunds
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "fund id"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/investment-funds/{id} [get]
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
	// Return the full enriched shape. The proto fields are already populated
	// by the stock-service handler (E1). We shape the JSON response here so
	// the frontend gets a flat structure with all statistics at the top level.
	// holdings is a proto `repeated` field: an empty list is indistinguishable
	// from absent on the wire, so resp.GetHoldings() decodes as a nil slice for
	// any fund with no positions. emptyIfNil keeps it serializing as `[]`
	// rather than `null` (matches every other list-returning gateway handler).
	c.JSON(http.StatusOK, gin.H{
		"fund":                     resp.GetFund(),
		"holdings":                 emptyIfNil(resp.GetHoldings()),
		"investor_count":           resp.GetInvestorCount(),
		"total_contributed_rsd":    resp.GetTotalContributedRsd(),
		"liquid_rsd_balance":       resp.GetLiquidRsdBalance(),
		"total_holdings_value_rsd": resp.GetTotalHoldingsValueRsd(),
		"total_value_rsd":          resp.GetTotalValueRsd(),
		"total_dividends_paid_rsd": resp.GetTotalDividendsPaidRsd(),
		"profit_rsd":               resp.GetProfitRsd(),
		"profit_pct":               resp.GetProfitPct(),
		// SP3 statistics + history.
		"annualized_return_pct": resp.GetAnnualizedReturnPct(),
		"volatility_pct":        resp.GetVolatilityPct(),
		"reward_to_variability": resp.GetRewardToVariability(),
		"max_drawdown_pct":      resp.GetMaxDrawdownPct(),
		"metrics_available":     resp.GetMetricsAvailable(),
		"history":               emptyIfNil(resp.GetHistory()),
		"average_history":       emptyIfNil(resp.GetAverageHistory()),
	})
}

type updateFundRequest struct {
	Name                   *string `json:"name,omitempty"`
	Description            *string `json:"description,omitempty"`
	MinimumContributionRSD *string `json:"minimum_contribution_rsd,omitempty"`
	Active                 *bool   `json:"active,omitempty"`
	DividendMode           *string `json:"dividend_mode,omitempty"` // payout|reinvest (SP4)
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
	if req.DividendMode != nil {
		if _, err := oneOf("dividend_mode", *req.DividendMode, "payout", "reinvest"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
		mode, _ := oneOf("dividend_mode", *req.DividendMode, "payout", "reinvest")
		in.DividendMode = mode
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
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.InvestInFund(c.Request.Context(), &stockpb.InvestInFundRequest{
		FundId:          id,
		ActorUserId:     ownerToLegacyUserID(identity.OwnerID),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
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
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.RedeemFromFund(c.Request.Context(), &stockpb.RedeemFromFundRequest{
		FundId:          id,
		ActorUserId:     ownerToLegacyUserID(identity.OwnerID),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
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
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListMyPositions(c.Request.Context(), &stockpb.ListMyPositionsRequest{
		ActorUserId:     ownerToLegacyUserID(identity.OwnerID),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
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
