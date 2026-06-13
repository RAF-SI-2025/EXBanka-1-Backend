package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

// UnifiedPortfolioHandler serves the GET /api/v3/portfolio/* and
// GET /api/v3/me/portfolio unified portfolio routes. Each route decodes the
// portfolio identity, enforces ownership, then delegates to the stock-service
// GetUnifiedPortfolio gRPC RPC.
type UnifiedPortfolioHandler struct {
	client stockpb.PortfolioGRPCServiceClient
}

// NewUnifiedPortfolioHandler wires the gRPC client.
func NewUnifiedPortfolioHandler(c stockpb.PortfolioGRPCServiceClient) *UnifiedPortfolioHandler {
	return &UnifiedPortfolioHandler{client: c}
}

// GetMy godoc
// @Summary      Get the caller's unified portfolio
// @Description  Returns all securities holdings and fund positions for the caller,
//
//	grouped by asset class with P/L totals computed on read.
//	Clients see their own portfolio; employees see the bank's portfolio.
//
// @Tags         portfolio
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Router       /api/v3/me/portfolio [get]
func (h *UnifiedPortfolioHandler) GetMy(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, id.OwnerType, id.OwnerID)
}

// GetByPortfolioID godoc
// @Summary      Get unified portfolio by portfolio_id
// @Description  portfolio_id is in the form client-<n>, bank, or fund-<n>.
//
//	Access is gated by the caller's role and permissions.
//
// @Tags         portfolio
// @Security     BearerAuth
// @Produce      json
// @Param        portfolio_id path string true "Portfolio ID (client-42 / bank / fund-7)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}  "invalid_portfolio_id"
// @Failure      403  {object}  map[string]interface{}  "forbidden"
// @Router       /api/v3/portfolio/{portfolio_id} [get]
func (h *UnifiedPortfolioHandler) GetByPortfolioID(c *gin.Context) {
	pid := c.Param("portfolio_id")
	ot, oid, err := DecodePortfolioID(pid)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, ot, oid)
}

// GetByClientID godoc
// @Summary      Get unified portfolio for a specific client
// @Description  Requires portfolio.view_client permission.
// @Tags         portfolio
// @Security     BearerAuth
// @Produce      json
// @Param        client_id path integer true "Client ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Router       /api/v3/portfolio/client/{client_id} [get]
func (h *UnifiedPortfolioHandler) GetByClientID(c *gin.Context) {
	raw := c.Param("client_id")
	cid, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || cid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "client_id must be positive integer")
		return
	}
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "client", &cid)
}

// GetBank godoc
// @Summary      Get the bank's unified portfolio
// @Description  Returns the bank-owned securities holdings and fund positions.
//
//	Requires AuthMiddleware (employee token).
//
// @Tags         portfolio
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Router       /api/v3/portfolio/bank [get]
func (h *UnifiedPortfolioHandler) GetBank(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "bank", nil)
}

// GetByFundID godoc
// @Summary      Get unified portfolio for an investment fund
// @Description  Requires portfolio.view_fund permission.
// @Tags         portfolio
// @Security     BearerAuth
// @Produce      json
// @Param        fund_id path integer true "Fund ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Router       /api/v3/portfolio/investment-fund/{fund_id} [get]
func (h *UnifiedPortfolioHandler) GetByFundID(c *gin.Context) {
	raw := c.Param("fund_id")
	fid, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || fid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "fund_id must be positive integer")
		return
	}
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	h.dispatch(c, id, "investment_fund", &fid)
}

// dispatch is the shared workhorse: enforces portfolio access and calls gRPC.
func (h *UnifiedPortfolioHandler) dispatch(c *gin.Context, id *middleware.ResolvedIdentity, ot string, oid *uint64) {
	perms := middleware.GetCallerPermissions(c)
	if err := enforcePortfolioAccess(c, id, ot, oid, perms); err != nil {
		return // apiError already written by enforcePortfolioAccess
	}

	var ownerID uint64
	if oid != nil {
		ownerID = *oid
	}
	resp, err := h.client.GetUnifiedPortfolio(c.Request.Context(), &stockpb.GetUnifiedPortfolioRequest{
		OwnerType: ot,
		OwnerId:   ownerID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
