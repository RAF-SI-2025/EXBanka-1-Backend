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

	// Fix R5 (2026-05-16): gateway-side ownership pre-check per
	// CLAUDE.md hard requirement. Without this we'd be trusting the
	// service to enforce — defense in depth says verify here too. 404
	// (not 403) for non-owner because we don't want to leak whether
	// the holding id exists for someone else.
	got, gerr := h.portfolioClient.GetHolding(c.Request.Context(), &stockpb.GetHoldingRequest{Id: id})
	if gerr != nil {
		handleGRPCError(c, gerr)
		return
	}
	if got.GetOwnerType() != string(identity.OwnerType) ||
		got.GetOwnerId() != derefU64(identity.OwnerID) {
		apiError(c, 404, ErrNotFound, "holding not found")
		return
	}

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

// ListOTCOptions godoc
// @Summary      Unified marketplace view of OPEN OTC option listings
// @Description  Phase 6 cross-bank discovery. Returns this bank's open
//
//	OTCOffer rows (kind=local) plus every active peer's
//	open listings (kind=remote) merged in the in-memory
//	cache (refreshed every ~5s). Filter by ticker / kind /
//	bank_code / direction. Pagination is applied in-memory
//	over the cached snapshot. Each offer the authenticated
//	caller has an own (bidder) negotiation chain against
//	carries my_negotiation_id + my_negotiation_status so the
//	FE can jump straight to its chain (absent otherwise).
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
	identity, _ := c.MustGet("identity").(*middleware.ResolvedIdentity)
	var identityOwnerType string
	var identityOwnerID uint64
	var actorUserID int64
	var actorSystemType string
	if identity != nil {
		identityOwnerType = identity.OwnerType
		identityOwnerID = derefU64(identity.OwnerID)
		// Acting PRINCIPAL ("client"/"employee" + id) — drives the owner
		// latest-counter term projection in the stock-service (D2). Unlike
		// the owner type/id (which collapse employees to "bank"), this carries
		// the concrete employee id so a bank owner resolves correctly.
		actorUserID = int64(identity.PrincipalID)
		actorSystemType = identity.PrincipalType
	}
	resp, err := h.otcClient.ListUnifiedOptionOffers(c.Request.Context(), &stockpb.ListUnifiedOptionOffersRequest{
		Ticker:            c.Query("ticker"),
		Kind:              kind,
		BankCode:          c.Query("bank_code"),
		Direction:         direction,
		Page:              int32(page),
		PageSize:          int32(pageSize),
		OwnerOnlySellerId: ownerOnlySellerID,
		ActingOwnerType:   identityOwnerType,
		ActingOwnerId:     identityOwnerID,
		ActorUserId:       actorUserID,
		ActorSystemType:   actorSystemType,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	offers := make([]gin.H, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		row := gin.H{
			"id":               o.GetLocalId(),
			"kind":             o.GetKind(),
			"bank_code":        o.GetBankCode(),
			"routing_number":   o.GetRoutingNumber(),
			"offer_id":         o.GetOfferId(),
			"seller_id":        o.GetSellerId(),
			"me_owner":         o.GetMeOwner(),
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
		// SP-2b — the caller's own (bidder) negotiation chain on this offer, so
		// the FE can jump straight to it. Omitted when the caller has no chain
		// here (0 / "").
		if o.GetMyNegotiationId() > 0 {
			row["my_negotiation_id"] = o.GetMyNegotiationId()
			row["my_negotiation_status"] = o.GetMyNegotiationStatus()
		}
		// Viewer-relative action hints (4.7.0) — drive the row's Bid / Counter /
		// Accept / Reject / Withdraw buttons. ALWAYS present (explicit booleans)
		// so the FE can read them unambiguously rather than inferring from an
		// absent field. For the listing's own poster only viewer_role="poster"
		// is meaningful (they act per-bid via /otc/options/:id/negotiations).
		row["viewer_role"] = o.GetViewerRole()
		row["last_action_mine"] = o.GetLastActionMine()
		row["awaiting_viewer"] = o.GetAwaitingViewer()
		row["can_bid"] = o.GetCanBid()
		row["can_accept"] = o.GetCanAccept()
		row["can_counter"] = o.GetCanCounter()
		row["can_reject"] = o.GetCanReject()
		row["can_withdraw"] = o.GetCanWithdraw()
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
