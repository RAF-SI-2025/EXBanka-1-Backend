package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type PortfolioHandler struct {
	portfolioClient stockpb.PortfolioGRPCServiceClient
	otcClient       stockpb.OTCGRPCServiceClient
	accountClient   accountpb.AccountServiceClient
}

func NewPortfolioHandler(
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	accountClient accountpb.AccountServiceClient,
) *PortfolioHandler {
	return &PortfolioHandler{
		portfolioClient: portfolioClient,
		otcClient:       otcClient,
		accountClient:   accountClient,
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

// ListOTCOffers serves the unified OTC market view by calling
// stock-service's OTCGRPCService.ListUnifiedOffers, which owns the
// cross-bank discovery cache. The gateway is a thin pass-through:
// query params map 1-to-1 onto the gRPC request, and the response
// is reshaped into the JSON contract documented in REST_API_v3 §28.
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
	kindFilter := c.Query("kind")
	if kindFilter != "" {
		if _, err := oneOf("kind", kindFilter, "local", "remote"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.otcClient.ListUnifiedOffers(c.Request.Context(), &stockpb.ListUnifiedOTCOffersRequest{
		SecurityType: secType,
		Ticker:       c.Query("ticker"),
		Kind:         kindFilter,
		BankCode:     c.Query("bank_code"),
		Page:         int32(page),
		PageSize:     int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	// Project pb.UnifiedOTCOffer to the JSON shape with omitempty
	// semantics matching REST_API_v3 §28.
	offers := make([]gin.H, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		row := gin.H{
			"kind":           o.GetKind(),
			"bank_code":      o.GetBankCode(),
			"security_type":  o.GetSecurityType(),
			"ticker":         o.GetTicker(),
			"quantity":       o.GetQuantity(),
			"price_per_unit": o.GetPricePerUnit(),
		}
		if o.GetKind() == "local" {
			row["id"] = o.GetId()
			row["seller_id"] = o.GetSellerId()
			row["seller_name"] = o.GetSellerName()
			row["name"] = o.GetName()
			row["created_at"] = o.GetCreatedAt()
		} else {
			row["owner_id"] = o.GetOwnerId()
			row["currency"] = o.GetCurrency()
		}
		offers = append(offers, row)
	}

	var lastRefresh string
	if u := resp.GetLastRefreshUnix(); u > 0 {
		lastRefresh = time.Unix(u, 0).UTC().Format("2006-01-02T15:04:05Z")
	}

	c.JSON(http.StatusOK, gin.H{
		"offers":        offers,
		"total_count":   resp.GetTotalCount(),
		"peers_total":   resp.GetPeersTotal(),
		"peers_reached": resp.GetPeersReached(),
		"partial":       resp.GetPartial(),
		"last_refresh":  lastRefresh,
	})
}

// ListOTCOptions godoc
// @Summary      Unified marketplace view of OPEN OTC option listings
// @Description  Phase 6 cross-bank discovery. Returns this bank's open
//
//	OTCOffer rows (kind=local) plus every active peer's
//	open listings (kind=remote) merged in the in-memory
//	cache (refreshed every ~5s). Filter by ticker / kind /
//	bank_code / direction. Pagination is applied in-memory
//	over the cached snapshot.
//
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        ticker    query string false "Filter to one ticker"
// @Param        kind      query string false "local|remote"
// @Param        bank_code query string false "Filter to one bank"
// @Param        direction query string false "sell_initiated|buy_initiated"
// @Param        page      query int    false "1-based, default 1"
// @Param        page_size query int    false "default 10"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/options [get]
func (h *PortfolioHandler) ListOTCOptions(c *gin.Context) {
	h.listUnifiedOTCOptions(c, "")
}

// ListMyOTCOptions godoc
// @Summary      Marketplace view of the caller's OWN open OTC option listings
// @Description  Same response shape as GET /api/v3/otc/options (kind/bank_code/best_bid/best_ask/active_chains_count/...) but scoped to listings the caller posted. Only OPEN listings appear here (the unified cache is open-only); for a full history of every offer you've ever posted use GET /api/v3/me/otc/options/posted.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        ticker    query string false "Filter to one ticker"
// @Param        direction query string false "sell_initiated|buy_initiated"
// @Param        page      query int    false "1-based, default 1"
// @Param        page_size query int    false "default 10"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options [get]
func (h *PortfolioHandler) ListMyOTCOptions(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	var sellerID string
	if identity.OwnerType == "bank" {
		sellerID = "bank"
	} else if identity.OwnerID != nil {
		sellerID = "client-" + strconv.FormatUint(*identity.OwnerID, 10)
	} else {
		// Defensive: identity resolved to something we can't map to a
		// SellerID. Return empty rather than 500.
		c.JSON(http.StatusOK, gin.H{"offers": []gin.H{}, "total_count": 0, "peers_total": 0, "peers_reached": 0, "partial": false, "last_refresh": ""})
		return
	}
	h.listUnifiedOTCOptions(c, sellerID)
}

// listUnifiedOTCOptions is the shared workhorse for ListOTCOptions and
// ListMyOTCOptions. When ownerOnlySellerID is non-empty it short-circuits
// to local listings whose SellerID matches (used for /me/...).
func (h *PortfolioHandler) listUnifiedOTCOptions(c *gin.Context, ownerOnlySellerID string) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if pageSize < 1 {
		pageSize = 10
	}
	kind := c.Query("kind")
	if kind != "" {
		if _, err := oneOf("kind", kind, "local", "remote"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	direction := c.Query("direction")
	if direction != "" {
		if _, err := oneOf("direction", direction, "sell_initiated", "buy_initiated"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	resp, err := h.otcClient.ListUnifiedOptionOffers(c.Request.Context(), &stockpb.ListUnifiedOptionOffersRequest{
		Ticker:             c.Query("ticker"),
		Kind:               kind,
		BankCode:           c.Query("bank_code"),
		Direction:          direction,
		Page:               int32(page),
		PageSize:           int32(pageSize),
		OwnerOnlySellerId:  ownerOnlySellerID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	offers := make([]gin.H, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		row := gin.H{
			"kind":             o.GetKind(),
			"bank_code":        o.GetBankCode(),
			"routing_number":   o.GetRoutingNumber(),
			"offer_id":         o.GetOfferId(),
			"seller_id":        o.GetSellerId(),
			"seller_name":      o.GetSellerName(),
			"direction":        o.GetDirection(),
			"ticker":           o.GetTicker(),
			"amount":           o.GetAmount(),
			"strike_price":     o.GetStrikePrice(),
			"strike_currency":  o.GetStrikeCurrency(),
			"premium":          o.GetPremium(),
			"premium_currency": o.GetPremiumCurrency(),
			"settlement_date":  o.GetSettlementDate(),
			"created_at":       o.GetCreatedAt(),
		}
		// Part A 2026-05-16 best-bid/best-ask surface. Empty strings
		// ⇒ no active competition (or remote peer doesn't publish).
		// FE renders "—" in that case.
		if o.GetBestBid() != "" {
			row["best_bid"] = o.GetBestBid()
		}
		if o.GetBestAsk() != "" {
			row["best_ask"] = o.GetBestAsk()
		}
		if o.GetActiveChainsCount() > 0 {
			row["active_chains_count"] = o.GetActiveChainsCount()
		}
		offers = append(offers, row)
	}
	var lastRefresh string
	if u := resp.GetLastRefreshUnix(); u > 0 {
		lastRefresh = time.Unix(u, 0).UTC().Format("2006-01-02T15:04:05Z")
	}
	c.JSON(http.StatusOK, gin.H{
		"offers":        offers,
		"total_count":   resp.GetTotalCount(),
		"peers_total":   resp.GetPeersTotal(),
		"peers_reached": resp.GetPeersReached(),
		"partial":       resp.GetPartial(),
		"last_refresh":  lastRefresh,
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
