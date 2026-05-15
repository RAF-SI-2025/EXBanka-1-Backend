package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/api-gateway/internal/middleware"
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

func derefU64(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
