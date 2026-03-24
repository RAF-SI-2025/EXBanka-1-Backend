package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	exchangepb "github.com/exbanka/contract/exchangepb"
)

type ExchangeHandler struct {
	exchangeClient exchangepb.ExchangeServiceClient
}

func NewExchangeHandler(exchangeClient exchangepb.ExchangeServiceClient) *ExchangeHandler {
	return &ExchangeHandler{exchangeClient: exchangeClient}
}

// @Summary      List exchange rates
// @Description  Returns all current buy/sell rates for supported currencies against RSD
// @Tags         exchange
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]string
// @Router       /api/exchange/rates [get]
func (h *ExchangeHandler) ListExchangeRates(c *gin.Context) {
	resp, err := h.exchangeClient.ListRates(c.Request.Context(), &exchangepb.ListRatesRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	rates := make([]gin.H, 0, len(resp.Rates))
	for _, r := range resp.Rates {
		rates = append(rates, rateToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"rates": rates})
}

// @Summary      Get exchange rate
// @Description  Returns buy/sell rates for a specific currency pair
// @Tags         exchange
// @Produce      json
// @Param        from  path  string  true  "Source currency (e.g. EUR)"
// @Param        to    path  string  true  "Target currency (e.g. RSD)"
// @Success      200   {object}  map[string]interface{}
// @Failure      404   {object}  map[string]string
// @Router       /api/exchange/rates/{from}/{to} [get]
func (h *ExchangeHandler) GetExchangeRate(c *gin.Context) {
	from := strings.ToUpper(c.Param("from"))
	to := strings.ToUpper(c.Param("to"))

	resp, err := h.exchangeClient.GetRate(c.Request.Context(), &exchangepb.GetRateRequest{
		FromCurrency: from,
		ToCurrency:   to,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, rateToJSON(resp))
}

// CalculateExchangeRequest is the JSON body for POST /api/exchange/calculate.
type CalculateExchangeRequest struct {
	FromCurrency string `json:"fromCurrency" binding:"required" example:"EUR"`
	ToCurrency   string `json:"toCurrency"   binding:"required" example:"USD"`
	Amount       string `json:"amount"       binding:"required" example:"100.00"`
}

// @Summary      Calculate currency conversion
// @Description  Returns the converted amount after applying the bank's selling rate and commission (informational only — no transaction is created)
// @Tags         exchange
// @Accept       json
// @Produce      json
// @Param        body  body      handler.CalculateExchangeRequest  true  "Conversion input"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      404   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/exchange/calculate [post]
func (h *ExchangeHandler) CalculateExchange(c *gin.Context) {
	var req CalculateExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "fromCurrency, toCurrency, and amount are required"})
		return
	}

	// Validate amount is a positive decimal (gateway must validate before forwarding to gRPC).
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be a positive number"})
		return
	}

	supportedCurrencies := map[string]bool{
		"RSD": true, "EUR": true, "CHF": true, "USD": true,
		"GBP": true, "JPY": true, "CAD": true, "AUD": true,
	}
	from := strings.ToUpper(req.FromCurrency)
	to := strings.ToUpper(req.ToCurrency)
	if !supportedCurrencies[from] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported fromCurrency: " + from})
		return
	}
	if !supportedCurrencies[to] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported toCurrency: " + to})
		return
	}

	resp, err := h.exchangeClient.Calculate(c.Request.Context(), &exchangepb.CalculateRequest{
		FromCurrency: from,
		ToCurrency:   to,
		Amount:       amount.StringFixed(4), // normalised decimal string
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"from_currency":    resp.FromCurrency,
		"to_currency":      resp.ToCurrency,
		"input_amount":     resp.InputAmount,
		"converted_amount": resp.ConvertedAmount,
		"commission_rate":  resp.CommissionRate,
		"effective_rate":   resp.EffectiveRate,
	})
}

func rateToJSON(r *exchangepb.RateResponse) gin.H {
	return gin.H{
		"from_currency": r.FromCurrency,
		"to_currency":   r.ToCurrency,
		"buy_rate":      r.BuyRate,
		"sell_rate":     r.SellRate,
		"updated_at":    r.UpdatedAt,
	}
}
