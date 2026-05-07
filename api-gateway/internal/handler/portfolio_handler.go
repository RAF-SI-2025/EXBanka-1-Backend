package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/exbanka/api-gateway/internal/otccache"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type PortfolioHandler struct {
	portfolioClient stockpb.PortfolioGRPCServiceClient
	otcClient       stockpb.OTCGRPCServiceClient
	accountClient   accountpb.AccountServiceClient
	otcCache        *otccache.Cache
}

func NewPortfolioHandler(
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	accountClient accountpb.AccountServiceClient,
	otcCache *otccache.Cache,
) *PortfolioHandler {
	return &PortfolioHandler{
		portfolioClient: portfolioClient,
		otcClient:       otcClient,
		accountClient:   accountClient,
		otcCache:        otcCache,
	}
}

func (h *PortfolioHandler) ListHoldings(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	secType := c.Query("security_type")
	if secType != "" {
		if _, err := oneOf("security_type", secType, "stock", "futures", "option"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.portfolioClient.ListHoldings(c.Request.Context(), &stockpb.ListHoldingsRequest{
		UserId:       ownerToLegacyUserID(id.OwnerID),
		SystemType:   ownerToLegacySystemType(id.OwnerType),
		SecurityType: secType,
		Page:         int32(page),
		PageSize:     int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"holdings": emptyIfNil(resp.Holdings), "total_count": resp.TotalCount})
}

func (h *PortfolioHandler) GetPortfolioSummary(c *gin.Context) {
	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.portfolioClient.GetPortfolioSummary(c.Request.Context(), &stockpb.GetPortfolioSummaryRequest{
		UserId:     ownerToLegacyUserID(id.OwnerID),
		SystemType: ownerToLegacySystemType(id.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *PortfolioHandler) MakePublic(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid holding id")
		return
	}
	var req struct {
		Quantity int64 `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.portfolioClient.MakePublic(c.Request.Context(), &stockpb.MakePublicRequest{
		HoldingId:  id,
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
		Quantity:   req.Quantity,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListHoldingTransactions godoc
// @Summary      List per-purchase history for a holding
// @Description  Returns the OrderTransactions that contributed to a holding — when each buy/sell executed, price per unit, native vs account currency, FX rate, commission, and which account was used. Replaces the per-purchase detail removed from /me/portfolio in Part C.
// @Tags         portfolio
// @Produce      json
// @Param        id         path   integer true  "Holding ID"
// @Param        direction  query  string  false "Filter by direction (buy|sell); empty for both"
// @Param        page       query  int     false "Page number (default 1)"
// @Param        page_size  query  int     false "Page size (default 10)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "transactions + total_count"
// @Failure      400  {object}  map[string]interface{}  "validation_error"
// @Failure      401  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}  "not_found — holding does not exist or does not belong to caller"
// @Router       /api/v2/me/holdings/{id}/transactions [get]
func (h *PortfolioHandler) ListHoldingTransactions(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid holding id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	direction := c.Query("direction")
	if direction != "" {
		if _, err := oneOf("direction", direction, "buy", "sell"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.portfolioClient.ListHoldingTransactions(c.Request.Context(), &stockpb.ListHoldingTransactionsRequest{
		HoldingId:  id,
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
		Direction:  direction,
		Page:       int32(page),
		PageSize:   int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transactions": emptyIfNil(resp.Transactions),
		"total_count":  resp.TotalCount,
	})
}

func (h *PortfolioHandler) ExerciseOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid holding id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.portfolioClient.ExerciseOption(c.Request.Context(), &stockpb.ExerciseOptionRequest{
		HoldingId:  id,
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// --- OTC ---

// ListOTCOffers serves the unified OTC market view: local offers (this
// bank's holdings flagged public_quantity > 0) plus remote offers
// pulled from every active peer bank's GET /api/v3/public-stock. The
// data is served from an in-memory cache that the gateway rebuilds on
// a background ticker (~5 s); peer fan-out happens off the request
// path so a slow or down peer never blocks the client. Each row carries
// `kind: "local" | "remote"` and `bank_code` so the UI can route
// purchases to the right flow (local → POST /otc/offers/:id/buy;
// remote → POST /me/peer-otc/negotiations).
func (h *PortfolioHandler) ListOTCOffers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if pageSize < 1 {
		pageSize = 10
	}

	secType := c.Query("security_type")
	if secType != "" {
		if _, err := oneOf("security_type", secType, "stock", "futures"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	ticker := strings.ToUpper(c.Query("ticker"))
	kindFilter := c.Query("kind")
	if kindFilter != "" {
		if _, err := oneOf("kind", kindFilter, "local", "remote"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	bankFilter := c.Query("bank_code")

	snap := h.otcCache.Get()
	filtered := make([]otccache.Offer, 0, len(snap.Offers))
	for _, o := range snap.Offers {
		if secType != "" && o.SecurityType != secType {
			continue
		}
		if ticker != "" && strings.ToUpper(o.Ticker) != ticker {
			continue
		}
		if kindFilter != "" && o.Kind != kindFilter {
			continue
		}
		if bankFilter != "" && o.BankCode != bankFilter {
			continue
		}
		filtered = append(filtered, o)
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	var lastRefresh string
	if !snap.LastRefresh.IsZero() {
		lastRefresh = snap.LastRefresh.Format("2006-01-02T15:04:05Z07:00")
	}

	c.JSON(http.StatusOK, gin.H{
		"offers":         filtered[start:end],
		"total_count":    total,
		"peers_total":    snap.PeersTotal,
		"peers_reached":  snap.PeersReached,
		"partial":        snap.PeersTotal > 0 && snap.PeersReached < snap.PeersTotal,
		"last_refresh":   lastRefresh,
	})
}

// BuyOTCOfferOnBehalf godoc
// @Summary      Buy an OTC offer on behalf of a client
// @Description  Employee-only. Requires orders.place-on-behalf permission. Gateway verifies the account belongs to the named client.
// @Tags         otc
// @Accept       json
// @Produce      json
// @Param        id path integer true "Offer ID"
// @Param        body body object true "Purchase"
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v2/otc/admin/offers/{id}/buy [post]
func (h *PortfolioHandler) BuyOTCOfferOnBehalf(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid offer id")
		return
	}
	var req struct {
		ClientID  uint64 `json:"client_id"`
		AccountID uint64 `json:"account_id"`
		Quantity  int64  `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 || req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "client_id and account_id are required")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}

	acctResp, acctErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if acctErr != nil {
		handleGRPCError(c, acctErr)
		return
	}
	if acctResp.OwnerId != req.ClientID {
		apiError(c, 403, ErrForbidden, "account does not belong to client")
		return
	}

	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.otcClient.BuyOffer(c.Request.Context(), &stockpb.BuyOTCOfferRequest{
		OfferId:            id,
		BuyerId:            req.ClientID,
		SystemType:         "employee",
		Quantity:           req.Quantity,
		AccountId:          req.AccountID,
		ActingEmployeeId:   derefU64Ptr(identity.ActingEmployeeID),
		OnBehalfOfClientId: req.ClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *PortfolioHandler) BuyOTCOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid offer id")
		return
	}
	var req struct {
		Quantity  int64  `json:"quantity"`
		AccountID uint64 `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "account_id is required")
		return
	}

	acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
		return
	}

	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.otcClient.BuyOffer(c.Request.Context(), &stockpb.BuyOTCOfferRequest{
		OfferId:    id,
		BuyerId:    ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
		Quantity:   req.Quantity,
		AccountId:  req.AccountID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
