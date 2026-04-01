package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	stockpb "github.com/exbanka/contract/stockpb"
)

type StockExchangeHandler struct {
	client stockpb.StockExchangeGRPCServiceClient
}

func NewStockExchangeHandler(client stockpb.StockExchangeGRPCServiceClient) *StockExchangeHandler {
	return &StockExchangeHandler{client: client}
}

func (h *StockExchangeHandler) ListExchanges(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	search := c.Query("search")

	resp, err := h.client.ListExchanges(c.Request.Context(), &stockpb.ListExchangesRequest{
		Search:   search,
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"exchanges":   resp.Exchanges,
		"total_count": resp.TotalCount,
	})
}

func (h *StockExchangeHandler) GetExchange(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid exchange id")
		return
	}

	resp, err := h.client.GetExchange(c.Request.Context(), &stockpb.GetExchangeRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockExchangeHandler) SetTestingMode(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}

	resp, err := h.client.SetTestingMode(c.Request.Context(), &stockpb.SetTestingModeRequest{
		Enabled: req.Enabled,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"testing_mode": resp.TestingMode})
}

func (h *StockExchangeHandler) GetTestingMode(c *gin.Context) {
	resp, err := h.client.GetTestingMode(c.Request.Context(), &stockpb.GetTestingModeRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"testing_mode": resp.TestingMode})
}
