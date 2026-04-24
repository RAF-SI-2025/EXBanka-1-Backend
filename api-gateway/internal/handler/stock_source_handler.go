package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
)

// StockSourceHandler handles admin routes for switching the active stock data source.
type StockSourceHandler struct {
	client stockpb.SourceAdminServiceClient
}

// NewStockSourceHandler creates a new StockSourceHandler.
func NewStockSourceHandler(c stockpb.SourceAdminServiceClient) *StockSourceHandler {
	return &StockSourceHandler{client: c}
}

type switchSourceRequest struct {
	Source string `json:"source"`
}

// SwitchSource godoc
// @Summary      Switch active stock data source (DESTRUCTIVE)
// @Description  Wipes all stock-service securities and trading state, then reseeds from the new source. Admin-only.
// @Tags         Admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body switchSourceRequest true "target source: external | generated | simulator"
// @Success      202 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      500 {object} map[string]interface{}
// @Router       /api/v2/admin/stock-source [post]
func (h *StockSourceHandler) SwitchSource(c *gin.Context) {
	var req switchSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := oneOf("source", req.Source, "external", "generated", "simulator"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.client.SwitchSource(c.Request.Context(), &stockpb.SwitchSourceRequest{Source: req.Source})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"source":     resp.Status.Source,
		"status":     resp.Status.Status,
		"started_at": resp.Status.StartedAt,
		"last_error": resp.Status.LastError,
	})
}

// GetSourceStatus godoc
// @Summary      Get the current stock data source and status
// @Description  Returns the currently active stock data source, seeding status, and any error details.
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      500 {object} map[string]interface{}
// @Router       /api/v2/admin/stock-source [get]
func (h *StockSourceHandler) GetSourceStatus(c *gin.Context) {
	resp, err := h.client.GetSourceStatus(c.Request.Context(), &stockpb.GetSourceStatusRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source":     resp.Source,
		"status":     resp.Status,
		"started_at": resp.StartedAt,
		"last_error": resp.LastError,
	})
}
