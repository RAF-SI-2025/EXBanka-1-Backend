package handler

import (
	"net/http"
	"strconv"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type StockExchangeHandler struct {
	client stockpb.StockExchangeGRPCServiceClient
}

func NewStockExchangeHandler(client stockpb.StockExchangeGRPCServiceClient) *StockExchangeHandler {
	return &StockExchangeHandler{client: client}
}

// exchangeResponse mirrors the proto Exchange fields (snake_case, same
// omitempty behavior the proto JSON tags produce) and adds is_open. is_open is
// NOT omitempty: a closed exchange must still report `"is_open": false` rather
// than dropping the field, so the frontend can always read open/closed state.
type exchangeResponse struct {
	ID              uint64 `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Acronym         string `json:"acronym,omitempty"`
	MicCode         string `json:"mic_code,omitempty"`
	Polity          string `json:"polity,omitempty"`
	Currency        string `json:"currency,omitempty"`
	TimeZone        string `json:"time_zone,omitempty"`
	OpenTime        string `json:"open_time,omitempty"`
	CloseTime       string `json:"close_time,omitempty"`
	PreMarketOpen   string `json:"pre_market_open,omitempty"`
	PostMarketClose string `json:"post_market_close,omitempty"`
	IsOpen          bool   `json:"is_open"`
}

func toExchangeResponse(o *stockpb.Exchange) exchangeResponse {
	return exchangeResponse{
		ID:              o.GetId(),
		Name:            o.GetName(),
		Acronym:         o.GetAcronym(),
		MicCode:         o.GetMicCode(),
		Polity:          o.GetPolity(),
		Currency:        o.GetCurrency(),
		TimeZone:        o.GetTimeZone(),
		OpenTime:        o.GetOpenTime(),
		CloseTime:       o.GetCloseTime(),
		PreMarketOpen:   o.GetPreMarketOpen(),
		PostMarketClose: o.GetPostMarketClose(),
		IsOpen:          o.GetIsOpen(),
	}
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
	out := make([]exchangeResponse, 0, len(resp.Exchanges))
	for _, ex := range resp.Exchanges {
		out = append(out, toExchangeResponse(ex))
	}
	c.JSON(http.StatusOK, gin.H{
		"exchanges":   out,
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
	c.JSON(http.StatusOK, toExchangeResponse(resp))
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
