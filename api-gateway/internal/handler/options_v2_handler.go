package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
)

// OptionsV2Handler handles v2 option-specific trading routes.
type OptionsV2Handler struct {
	secClient  stockpb.SecurityGRPCServiceClient
	ordClient  stockpb.OrderGRPCServiceClient
	portClient stockpb.PortfolioGRPCServiceClient
}

// NewOptionsV2Handler constructs an OptionsV2Handler.
func NewOptionsV2Handler(
	sec stockpb.SecurityGRPCServiceClient,
	ord stockpb.OrderGRPCServiceClient,
	port stockpb.PortfolioGRPCServiceClient,
) *OptionsV2Handler {
	return &OptionsV2Handler{secClient: sec, ordClient: ord, portClient: port}
}

type createOptionOrderRequest struct {
	Direction  string  `json:"direction"`
	OrderType  string  `json:"order_type"`
	Quantity   int64   `json:"quantity"`
	LimitValue *string `json:"limit_value,omitempty"`
	StopValue  *string `json:"stop_value,omitempty"`
	AllOrNone  bool    `json:"all_or_none"`
	Margin     bool    `json:"margin"`
	AccountID  uint64  `json:"account_id"`
	HoldingID  uint64  `json:"holding_id,omitempty"`
}

// CreateOrder godoc
// @Summary      Create an order for an option contract (v2)
// @Description  v2 accepts the option_id directly; internally resolves to listing_id and delegates to the standard order flow.
// @Tags         Options V2
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        option_id  path  int                        true  "Option ID"
// @Param        body       body  createOptionOrderRequest   true  "Order details"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      409  {object}  map[string]interface{}
// @Router       /api/v2/options/{option_id}/orders [post]
func (h *OptionsV2Handler) CreateOrder(c *gin.Context) {
	optionID, err := strconv.ParseUint(c.Param("option_id"), 10, 64)
	if err != nil || optionID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid option_id")
		return
	}

	var req createOptionOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}

	direction, err := oneOf("direction", req.Direction, "buy", "sell")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	orderType, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.Quantity <= 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "quantity must be positive")
		return
	}
	if req.AccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "account_id is required")
		return
	}
	if (orderType == "limit" || orderType == "stop_limit") && req.LimitValue == nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "limit_value required for limit/stop_limit orders")
		return
	}
	if (orderType == "stop" || orderType == "stop_limit") && req.StopValue == nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "stop_value required for stop/stop_limit orders")
		return
	}

	opt, err := h.secClient.GetOption(c.Request.Context(), &stockpb.GetOptionRequest{Id: optionID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if opt == nil || opt.ListingId == nil || *opt.ListingId == 0 {
		apiError(c, http.StatusConflict, ErrBusinessRule, "option not tradeable: no listing_id")
		return
	}

	userID := c.GetInt64("user_id")
	systemType := c.GetString("system_type")

	createReq := &stockpb.CreateOrderRequest{
		UserId:     uint64(userID),
		SystemType: systemType,
		ListingId:  *opt.ListingId,
		HoldingId:  req.HoldingID,
		Direction:  direction,
		OrderType:  orderType,
		Quantity:   req.Quantity,
		AllOrNone:  req.AllOrNone,
		Margin:     req.Margin,
		AccountId:  req.AccountID,
	}
	if req.LimitValue != nil {
		createReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		createReq.StopValue = req.StopValue
	}

	order, err := h.ordClient.CreateOrder(c.Request.Context(), createReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, order)
}

type exerciseOptionRequest struct {
	HoldingID uint64 `json:"holding_id,omitempty"`
}

// Exercise godoc
// @Summary      Exercise an option by option ID (v2)
// @Description  v2 takes option_id directly. If holding_id is omitted, the backend auto-resolves the user's oldest long option holding.
// @Tags         Options V2
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        option_id  path   int                    true   "Option ID"
// @Param        body       body   exerciseOptionRequest  false  "Optional holding_id"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Router       /api/v2/options/{option_id}/exercise [post]
func (h *OptionsV2Handler) Exercise(c *gin.Context) {
	optionID, err := strconv.ParseUint(c.Param("option_id"), 10, 64)
	if err != nil || optionID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid option_id")
		return
	}

	var req exerciseOptionRequest
	_ = c.ShouldBindJSON(&req) // body is optional

	userID := uint64(c.GetInt64("user_id"))
	if userID == 0 {
		apiError(c, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}

	result, err := h.portClient.ExerciseOptionByOptionID(c.Request.Context(), &stockpb.ExerciseOptionByOptionIDRequest{
		OptionId:  optionID,
		UserId:    userID,
		HoldingId: req.HoldingID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
