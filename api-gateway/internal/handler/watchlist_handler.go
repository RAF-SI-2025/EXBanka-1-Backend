package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

// WatchlistHandler exposes the WatchlistService via /api/v3/me/watchlist.
type WatchlistHandler struct {
	client stockpb.WatchlistServiceClient
}

func NewWatchlistHandler(client stockpb.WatchlistServiceClient) *WatchlistHandler {
	return &WatchlistHandler{client: client}
}

type addWatchlistRequest struct {
	ListingID uint64 `json:"listing_id"`
}

// AddItem godoc
// @Summary      Add a listing to the caller's watchlist
// @Tags         Watchlist
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body addWatchlistRequest true "listing_id to track"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/watchlist [post]
func (h *WatchlistHandler) AddItem(c *gin.Context) {
	var req addWatchlistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.ListingID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "listing_id is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.AddItem(c.Request.Context(), &stockpb.AddWatchlistItemRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
		ListingId: req.ListingID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"item": resp})
}

// RemoveItem godoc
// @Summary      Remove a listing from the caller's watchlist
// @Tags         Watchlist
// @Security     BearerAuth
// @Produce      json
// @Param        listing_id path int true "listing id"
// @Success      204 {string} string ""
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/watchlist/{listing_id} [delete]
func (h *WatchlistHandler) RemoveItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("listing_id"), 10, 64)
	if err != nil || id == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid listing_id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if _, err := h.client.RemoveItem(c.Request.Context(), &stockpb.RemoveWatchlistItemRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
		ListingId: id,
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMy godoc
// @Summary      List the caller's watchlist with current prices + daily change
// @Tags         Watchlist
// @Security     BearerAuth
// @Produce      json
// @Param        listing_type query string false "stock|option|futures|forex"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Router       /api/v3/me/watchlist [get]
func (h *WatchlistHandler) ListMy(c *gin.Context) {
	listingType := c.Query("listing_type")
	if listingType != "" {
		if _, err := oneOf("listing_type", listingType, "stock", "option", "futures", "forex"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyWatchlistRequest{
		OwnerType:   identity.OwnerType,
		OwnerId:     derefU64(identity.OwnerID),
		ListingType: listingType,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": resp.Items})
}

// GetByPortfolioID godoc
// @Summary      Get watchlist for any owner identified by portfolio_id
// @Description  portfolio_id is in the form client-<n>, bank, or fund-<n>.
//
//	Access is gated identically to GET /api/v3/portfolio/:portfolio_id.
//
// @Tags         Watchlist
// @Security     BearerAuth
// @Produce      json
// @Param        portfolio_id path string true "Portfolio ID (client-42 / bank / fund-7)"
// @Param        listing_type query string false "stock|option|futures|forex"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/watchlist/{portfolio_id} [get]
func (h *WatchlistHandler) GetByPortfolioID(c *gin.Context) {
	pid := c.Param("portfolio_id")
	ot, oid, err := DecodePortfolioID(pid)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}

	id := c.MustGet("identity").(*middleware.ResolvedIdentity)
	perms := middleware.GetCallerPermissions(c)
	if err := enforcePortfolioAccess(c, id, ot, oid, perms); err != nil {
		return
	}

	listingType := c.Query("listing_type")
	if listingType != "" {
		if _, err := oneOf("listing_type", listingType, "stock", "option", "futures", "forex"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}

	var ownerID uint64
	if oid != nil {
		ownerID = *oid
	}
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyWatchlistRequest{
		OwnerType:   ot,
		OwnerId:     ownerID,
		ListingType: listingType,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": resp.Items})
}

// ── SP6 named lists ────────────────────────────────────────────────────────

type createWatchlistRequest struct {
	Name string `json:"name"`
}

// ListWatchlists godoc
// @Summary      List the caller's named watchlists (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/watchlists [get]
func (h *WatchlistHandler) ListWatchlists(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListWatchlists(c.Request.Context(), &stockpb.ListWatchlistsRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	// Hand-shape so item_count is always present as a number (0 for an empty
	// list) — the raw proto omits int64 zero values.
	lists := make([]gin.H, 0, len(resp.Watchlists))
	for _, w := range resp.Watchlists {
		lists = append(lists, gin.H{
			"id": w.GetId(), "name": w.GetName(),
			"item_count": w.GetItemCount(), "created_at": w.GetCreatedAt(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"watchlists": lists})
}

// CreateWatchlist godoc
// @Summary      Create a named watchlist (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createWatchlistRequest true "list name"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Router       /api/v3/me/watchlists [post]
func (h *WatchlistHandler) CreateWatchlist(c *gin.Context) {
	var req createWatchlistRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "name is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.CreateWatchlist(c.Request.Context(), &stockpb.CreateWatchlistRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID), Name: req.Name,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"watchlist": resp})
}

// DeleteWatchlist godoc
// @Summary      Delete a named watchlist and its items (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Param        watchlist_id path int true "watchlist id"
// @Success      204 {string} string ""
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/me/watchlists/{watchlist_id} [delete]
func (h *WatchlistHandler) DeleteWatchlist(c *gin.Context) {
	wid, err := strconv.ParseUint(c.Param("watchlist_id"), 10, 64)
	if err != nil || wid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid watchlist_id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if _, err := h.client.DeleteWatchlist(c.Request.Context(), &stockpb.DeleteWatchlistRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID), WatchlistId: wid,
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListItemsInList godoc
// @Summary      List a named watchlist's items (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Produce      json
// @Param        watchlist_id path int true "watchlist id"
// @Param        listing_type query string false "stock|option|futures|forex"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/watchlists/{watchlist_id}/items [get]
func (h *WatchlistHandler) ListItemsInList(c *gin.Context) {
	wid, err := strconv.ParseUint(c.Param("watchlist_id"), 10, 64)
	if err != nil || wid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid watchlist_id")
		return
	}
	listingType := c.Query("listing_type")
	if listingType != "" {
		if _, err := oneOf("listing_type", listingType, "stock", "option", "futures", "forex"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListMy(c.Request.Context(), &stockpb.ListMyWatchlistRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID),
		ListingType: listingType, WatchlistId: wid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": resp.Items})
}

// AddItemToList godoc
// @Summary      Add a listing to a named watchlist (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        watchlist_id path int true "watchlist id"
// @Param        body body addWatchlistRequest true "listing_id to track"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/me/watchlists/{watchlist_id}/items [post]
func (h *WatchlistHandler) AddItemToList(c *gin.Context) {
	wid, err := strconv.ParseUint(c.Param("watchlist_id"), 10, 64)
	if err != nil || wid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid watchlist_id")
		return
	}
	var req addWatchlistRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ListingID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "listing_id is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.AddItem(c.Request.Context(), &stockpb.AddWatchlistItemRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID),
		ListingId: req.ListingID, WatchlistId: wid,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"item": resp})
}

// RemoveItemFromList godoc
// @Summary      Remove a listing from a named watchlist (SP6)
// @Tags         Watchlist
// @Security     BearerAuth
// @Param        watchlist_id path int true "watchlist id"
// @Param        listing_id path int true "listing id"
// @Success      204 {string} string ""
// @Router       /api/v3/me/watchlists/{watchlist_id}/items/{listing_id} [delete]
func (h *WatchlistHandler) RemoveItemFromList(c *gin.Context) {
	wid, err := strconv.ParseUint(c.Param("watchlist_id"), 10, 64)
	if err != nil || wid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid watchlist_id")
		return
	}
	lid, err := strconv.ParseUint(c.Param("listing_id"), 10, 64)
	if err != nil || lid == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid listing_id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if _, err := h.client.RemoveItem(c.Request.Context(), &stockpb.RemoveWatchlistItemRequest{
		OwnerType: identity.OwnerType, OwnerId: derefU64(identity.OwnerID),
		ListingId: lid, WatchlistId: wid,
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func derefU64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
