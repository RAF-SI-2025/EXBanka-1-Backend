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
