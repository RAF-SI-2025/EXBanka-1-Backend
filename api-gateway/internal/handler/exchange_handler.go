package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

type ExchangeHandler struct {
	txClient transactionpb.TransactionServiceClient
}

func NewExchangeHandler(txClient transactionpb.TransactionServiceClient) *ExchangeHandler {
	return &ExchangeHandler{txClient: txClient}
}

// ListExchangeRates godoc
// @Summary      List exchange rates
// @Description  Returns all current currency exchange rates
// @Tags         exchange-rates
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "rates array"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Router       /api/exchange-rates [get]
func (h *ExchangeHandler) ListExchangeRates(c *gin.Context) {
	resp, err := h.txClient.ListExchangeRates(c.Request.Context(), &transactionpb.ListExchangeRatesRequest{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rates := make([]gin.H, 0, len(resp.Rates))
	for _, r := range resp.Rates {
		rates = append(rates, exchangeRateToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"rates": rates})
}

// GetExchangeRate godoc
// @Summary      Get exchange rate
// @Description  Get the exchange rate between two currencies
// @Tags         exchange-rates
// @Produce      json
// @Param        from  path  string  true  "Source currency code (e.g. USD)"
// @Param        to    path  string  true  "Target currency code (e.g. EUR)"
// @Success      200  {object}  map[string]interface{}  "Exchange rate data"
// @Failure      404  {object}  map[string]string       "Exchange rate not found"
// @Router       /api/exchange-rates/{from}/{to} [get]
func (h *ExchangeHandler) GetExchangeRate(c *gin.Context) {
	from := c.Param("from")
	to := c.Param("to")

	resp, err := h.txClient.GetExchangeRate(c.Request.Context(), &transactionpb.GetExchangeRateRequest{
		FromCurrency: from,
		ToCurrency:   to,
	})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "exchange rate not found"})
		return
	}
	c.JSON(http.StatusOK, exchangeRateToJSON(resp))
}

func exchangeRateToJSON(r *transactionpb.ExchangeRateResponse) gin.H {
	return gin.H{
		"from_currency": r.FromCurrency,
		"to_currency":   r.ToCurrency,
		"buy_rate":      r.BuyRate,
		"sell_rate":     r.SellRate,
		"updated_at":    r.UpdatedAt,
	}
}
