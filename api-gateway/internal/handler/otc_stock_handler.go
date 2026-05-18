// Package handler — OTCStockHandler serves the Phase-3 OTC stocks
// marketplace routes (/api/v3/otc/stocks/* and /api/v3/me/otc/stocks).
// Thin HTTP→gRPC translation: validates input + resolves identity +
// proxies to OTCStockMarketGRPCService.
//
// Plan: docs/superpowers/plans/2026-05-16-otc-stocks-marketplace.md.
// Routes wired in api-gateway/internal/router/router_v3.go.
package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

type OTCStockHandler struct {
	client   stockpb.OTCStockMarketGRPCServiceClient
	accounts accountpb.AccountServiceClient
}

func NewOTCStockHandler(client stockpb.OTCStockMarketGRPCServiceClient, accounts accountpb.AccountServiceClient) *OTCStockHandler {
	return &OTCStockHandler{client: client, accounts: accounts}
}

// ---------- request bodies ----------

type createOTCStockOfferRequest struct {
	Direction      string `json:"direction"`            // "sell" | "buy"
	HoldingID      uint64 `json:"holding_id,omitempty"` // sell only
	ListingID      uint64 `json:"listing_id,omitempty"` // buy only
	Quantity       int64  `json:"quantity"`
	PricePerUnit   string `json:"price_per_unit,omitempty"`   // buy only (decimal)
	BuyerAccountID uint64 `json:"buyer_account_id,omitempty"` // buy only
}

// ---------- routes ----------

// CreateOTCStockOffer godoc
// @Summary      Create an OTC stock sell or buy offer
// @Description  direction=sell publishes shares from `holding_id` (accumulative); direction=buy creates a standing buy offer at `price_per_unit` backed by a cash reservation on `buyer_account_id`.
// @Tags         OTCStocks
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createOTCStockOfferRequest true "offer details (direction-keyed)"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/me/otc/stocks [post]
func (h *OTCStockHandler) CreateOTCStockOffer(c *gin.Context) {
	var req createOTCStockOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	direction, err := oneOf("direction", req.Direction, "sell", "buy")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if err := positive("quantity", float64(req.Quantity)); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	pbReq := &stockpb.CreateOTCStockOfferRequest{
		OwnerType:        identity.OwnerType,
		OwnerId:          derefU64Ptr(identity.OwnerID),
		Direction:        direction,
		Quantity:         req.Quantity,
		ActingEmployeeId: derefU64Ptr(identity.ActingEmployeeID),
	}

	if direction == "sell" {
		if req.HoldingID == 0 {
			apiError(c, http.StatusBadRequest, ErrValidation, "holding_id is required for direction=sell")
			return
		}
		// Phase 11 — seller's asking price is now required (was
		// previously auto-filled from holding.AveragePrice in the
		// cache, and hardcoded to "0" in the peer /public-stock
		// endpoint, both of which were wrong).
		if req.PricePerUnit == "" {
			apiError(c, http.StatusBadRequest, ErrValidation, "price_per_unit is required for direction=sell")
			return
		}
		pbReq.HoldingId = req.HoldingID
		pbReq.PricePerUnit = req.PricePerUnit
	} else {
		// direction == "buy" — validate buy-specific fields
		if req.ListingID == 0 {
			apiError(c, http.StatusBadRequest, ErrValidation, "listing_id is required for direction=buy")
			return
		}
		if req.BuyerAccountID == 0 {
			apiError(c, http.StatusBadRequest, ErrValidation, "buyer_account_id is required for direction=buy")
			return
		}
		if req.PricePerUnit == "" {
			apiError(c, http.StatusBadRequest, ErrValidation, "price_per_unit is required for direction=buy")
			return
		}
		// Verify buyer_account_id ownership BEFORE the gRPC call so
		// callers can't bind random accounts to their offer.
		if err := ResolveAndCheckAccount(c, h.accounts, identity, req.BuyerAccountID, 0); err != nil {
			return
		}
		pbReq.ListingId = req.ListingID
		pbReq.PricePerUnit = req.PricePerUnit
		pbReq.BuyerAccountId = req.BuyerAccountID
	}

	resp, err := h.client.CreateOTCStockOffer(c.Request.Context(), pbReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"offer": resp})
}

// CancelOTCStockOffer godoc
// @Summary      Cancel your own OTC stock sell or buy offer
// @Description  Direction-keyed: sell cancels by holding_id (zeros public_quantity); buy cancels by offer_id (releases reserved cash).
// @Tags         OTCStocks
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "holding_id (sell) or otc_stock_buy_offers.id (buy)"
// @Param        direction query string true "sell|buy"
// @Success      204 {string} string ""
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/otc/stocks/{id} [delete]
func (h *OTCStockHandler) CancelOTCStockOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	direction, err := oneOf("direction", c.Query("direction"), "sell", "buy")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	_, err = h.client.CancelOTCStockOffer(c.Request.Context(), &stockpb.CancelOTCStockOfferRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64Ptr(identity.OwnerID),
		Direction: direction,
		Id:        id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type sellOTCStockOfferRequest struct {
	Quantity        int64  `json:"quantity"`
	SellerAccountID uint64 `json:"seller_account_id"`
}

// SellOTCStockOffer godoc
// @Summary      Fill a buy-direction OTC stock offer (sell into it)
// @Description  The caller sells the requested quantity of shares into
//
//	an existing buy offer. The seller's holding is locked
//	with SELECT FOR UPDATE and a shares-available check
//	(Quantity - ReservedQuantity) runs BEFORE any money
//	moves — you literally cannot sell shares you don't
//	have. The buyer's cash was already reserved at
//	buy-offer-create time so payment is guaranteed.
//
// @Tags         OTCStocks
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path int                       true "otc_stock_buy_offers.id"
// @Param        body body sellOTCStockOfferRequest  true "quantity + seller's destination account"
// @Success      200  {object} map[string]interface{}
// @Failure      400  {object} map[string]interface{}
// @Failure      403  {object} map[string]interface{}
// @Failure      404  {object} map[string]interface{}
// @Failure      412  {object} map[string]interface{} "offer not active OR seller short on shares"
// @Router       /api/v3/otc/stocks/{id}/sell [post]
func (h *OTCStockHandler) SellOTCStockOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req sellOTCStockOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if err := positive("quantity", float64(req.Quantity)); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.SellerAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "seller_account_id is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// Verify the seller's destination account belongs to them before
	// forwarding — prevents callers from routing proceeds to a random
	// account.
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.SellerAccountID, 0); err != nil {
		return
	}
	resp, err := h.client.SellOTCStockOffer(c.Request.Context(), &stockpb.SellOTCStockOfferRequest{
		OfferId:          id,
		SellerOwnerType:  identity.OwnerType,
		SellerOwnerId:    derefU64Ptr(identity.OwnerID),
		Quantity:         req.Quantity,
		SellerAccountId:  req.SellerAccountID,
		ActingEmployeeId: derefU64Ptr(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fill": resp})
}

// ListMyOTCStocks godoc
// @Summary      List the caller's OTC stock offers (both directions)
// @Description  Returns sell offers (holdings with public_quantity > 0) and buy offers (otc_stock_buy_offers rows) where the caller is the owner. Filter with `?direction=sell|buy`.
// @Tags         OTCStocks
// @Security     BearerAuth
// @Produce      json
// @Param        direction query string false "sell|buy (omit for both)"
// @Param        page      query int    false "1-based, default 1"
// @Param        page_size query int    false "default 20, max 200"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/stocks [get]
func (h *OTCStockHandler) ListMyOTCStocks(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize > 200 {
		pageSize = 200
	}
	direction := strings.ToLower(strings.TrimSpace(c.Query("direction")))
	if direction != "" {
		if _, err := oneOf("direction", direction, "sell", "buy"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	resp, err := h.client.ListMyOTCStocks(c.Request.Context(), &stockpb.ListMyOTCStocksRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64Ptr(identity.OwnerID),
		Direction: direction,
		Page:      int32(page),
		PageSize:  int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"offers": resp.GetOffers(),
		"total":  resp.GetTotal(),
	})
}
