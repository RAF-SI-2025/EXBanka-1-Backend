package handler

import (
	"net/http"
	"strconv"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type SecuritiesHandler struct {
	client stockpb.SecurityGRPCServiceClient
}

func NewSecuritiesHandler(client stockpb.SecurityGRPCServiceClient) *SecuritiesHandler {
	return &SecuritiesHandler{client: client}
}

// --- Stocks ---

func (h *SecuritiesHandler) ListStocks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	sortBy := c.Query("sort_by")
	if sortBy != "" {
		if _, err := oneOf("sort_by", sortBy, "price", "volume", "change", "margin"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	sortOrder := c.DefaultQuery("sort_order", "asc")
	if _, err := oneOf("sort_order", sortOrder, "asc", "desc"); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.client.ListStocks(c.Request.Context(), &stockpb.ListStocksRequest{
		Search:          c.Query("search"),
		ExchangeAcronym: c.Query("exchange_acronym"),
		MinPrice:        c.Query("min_price"),
		MaxPrice:        c.Query("max_price"),
		MinVolume:       parseInt64Query(c, "min_volume"),
		MaxVolume:       parseInt64Query(c, "max_volume"),
		SortBy:          sortBy,
		SortOrder:       sortOrder,
		Page:            int32(page),
		PageSize:        int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stocks": emptyIfNil(resp.Stocks), "total_count": resp.TotalCount})
}

func (h *SecuritiesHandler) GetStock(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid stock id")
		return
	}
	resp, err := h.client.GetStock(c.Request.Context(), &stockpb.GetStockRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *SecuritiesHandler) GetStockHistory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid stock id")
		return
	}
	period := c.DefaultQuery("period", "month")
	if _, err := oneOf("period", period, "day", "week", "month", "year", "5y", "all"); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))

	resp, err := h.client.GetStockHistory(c.Request.Context(), &stockpb.GetPriceHistoryRequest{
		Id: id, Period: period, Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": emptyIfNil(resp.History), "total_count": resp.TotalCount})
}

// --- Futures ---

func (h *SecuritiesHandler) ListFutures(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListFutures(c.Request.Context(), &stockpb.ListFuturesRequest{
		Search:             c.Query("search"),
		ExchangeAcronym:    c.Query("exchange_acronym"),
		MinPrice:           c.Query("min_price"),
		MaxPrice:           c.Query("max_price"),
		MinVolume:          parseInt64Query(c, "min_volume"),
		MaxVolume:          parseInt64Query(c, "max_volume"),
		SettlementDateFrom: c.Query("settlement_date_from"),
		SettlementDateTo:   c.Query("settlement_date_to"),
		SortBy:             c.Query("sort_by"),
		SortOrder:          c.DefaultQuery("sort_order", "asc"),
		Page:               int32(page),
		PageSize:           int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"futures": emptyIfNil(resp.Futures), "total_count": resp.TotalCount})
}

func (h *SecuritiesHandler) GetFutures(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid futures id")
		return
	}
	resp, err := h.client.GetFutures(c.Request.Context(), &stockpb.GetFuturesRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *SecuritiesHandler) GetFuturesHistory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid futures id")
		return
	}
	period := c.DefaultQuery("period", "month")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))

	resp, err := h.client.GetFuturesHistory(c.Request.Context(), &stockpb.GetPriceHistoryRequest{
		Id: id, Period: period, Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": emptyIfNil(resp.History), "total_count": resp.TotalCount})
}

// --- Forex ---

func (h *SecuritiesHandler) ListForexPairs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	liquidity := c.Query("liquidity")
	if liquidity != "" {
		if _, err := oneOf("liquidity", liquidity, "high", "medium", "low"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.client.ListForexPairs(c.Request.Context(), &stockpb.ListForexPairsRequest{
		Search:        c.Query("search"),
		BaseCurrency:  c.Query("base_currency"),
		QuoteCurrency: c.Query("quote_currency"),
		Liquidity:     liquidity,
		SortBy:        c.Query("sort_by"),
		SortOrder:     c.DefaultQuery("sort_order", "asc"),
		Page:          int32(page),
		PageSize:      int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"forex_pairs": emptyIfNil(resp.ForexPairs), "total_count": resp.TotalCount})
}

func (h *SecuritiesHandler) GetForexPair(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid forex pair id")
		return
	}
	resp, err := h.client.GetForexPair(c.Request.Context(), &stockpb.GetForexPairRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *SecuritiesHandler) GetForexPairHistory(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid forex pair id")
		return
	}
	period := c.DefaultQuery("period", "month")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))

	resp, err := h.client.GetForexPairHistory(c.Request.Context(), &stockpb.GetPriceHistoryRequest{
		Id: id, Period: period, Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": emptyIfNil(resp.History), "total_count": resp.TotalCount})
}

// --- Options ---

func (h *SecuritiesHandler) ListOptions(c *gin.Context) {
	stockIDStr := c.Query("stock_id")
	if stockIDStr == "" {
		apiError(c, 400, ErrValidation, "stock_id query parameter is required")
		return
	}
	stockID, err := strconv.ParseUint(stockIDStr, 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid stock_id")
		return
	}

	optionType := c.Query("option_type")
	if optionType != "" {
		if _, err := oneOf("option_type", optionType, "call", "put"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.client.ListOptions(c.Request.Context(), &stockpb.ListOptionsRequest{
		StockId:        stockID,
		OptionType:     optionType,
		SettlementDate: c.Query("settlement_date"),
		MinStrike:      c.Query("min_strike"),
		MaxStrike:      c.Query("max_strike"),
		Page:           int32(page),
		PageSize:       int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"options": emptyIfNil(resp.Options), "total_count": resp.TotalCount})
}

func (h *SecuritiesHandler) GetOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid option id")
		return
	}
	resp, err := h.client.GetOption(c.Request.Context(), &stockpb.GetOptionRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// --- Candles ---

func (h *SecuritiesHandler) GetCandles(c *gin.Context) {
	listingIDStr := c.Query("listing_id")
	if listingIDStr == "" {
		apiError(c, 400, ErrValidation, "listing_id query parameter is required")
		return
	}
	listingID, err := strconv.ParseUint(listingIDStr, 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid listing_id")
		return
	}

	interval := c.DefaultQuery("interval", "1h")
	if _, err := oneOf("interval", interval, "1m", "5m", "15m", "1h", "4h", "1d"); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	fromStr := c.Query("from")
	if fromStr == "" {
		apiError(c, 400, ErrValidation, "from query parameter is required (RFC3339)")
		return
	}
	toStr := c.Query("to")
	if toStr == "" {
		apiError(c, 400, ErrValidation, "to query parameter is required (RFC3339)")
		return
	}

	resp, err := h.client.GetCandles(c.Request.Context(), &stockpb.GetCandlesRequest{
		ListingId: listingID,
		Interval:  interval,
		From:      fromStr,
		To:        toStr,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"candles": emptyIfNil(resp.Candles), "count": resp.Count})
}

// parseInt64Query parses a query parameter as int64, returning 0 if absent or invalid.
func parseInt64Query(c *gin.Context, key string) int64 {
	v := c.Query(key)
	if v == "" {
		return 0
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}
