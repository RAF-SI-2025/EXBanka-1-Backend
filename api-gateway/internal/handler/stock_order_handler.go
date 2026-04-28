package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type StockOrderHandler struct {
	client        stockpb.OrderGRPCServiceClient
	accountClient accountpb.AccountServiceClient
}

func NewStockOrderHandler(client stockpb.OrderGRPCServiceClient, accountClient accountpb.AccountServiceClient) *StockOrderHandler {
	return &StockOrderHandler{client: client, accountClient: accountClient}
}

// CreateOrder godoc
// @Summary      Place a securities order (stock/futures/forex/option) for the authenticated user
// @Description  Validates forex-specific constraints (forex must be buy, requires base_account_id, base_account_id must differ from account_id).
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        body body object true "Order. security_type is optional ('stock'|'futures'|'forex'|'option'); required for forex validation. base_account_id is required for forex buy orders."
// @Security     BearerAuth
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{} "validation_error — forex orders must be direction=buy; forex orders require base_account_id; base_account_id must differ from account_id"
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{} "business_rule_violation — insufficient available balance"
// @Router       /api/v2/me/orders [post]
func (h *StockOrderHandler) CreateOrder(c *gin.Context) {
	var req struct {
		SecurityType     string  `json:"security_type"`
		ListingID        uint64  `json:"listing_id"`
		HoldingID        uint64  `json:"holding_id"`
		Direction        string  `json:"direction"`
		OrderType        string  `json:"order_type"`
		Quantity         int64   `json:"quantity"`
		LimitValue       *string `json:"limit_value"`
		StopValue        *string `json:"stop_value"`
		AllOrNone        bool    `json:"all_or_none"`
		Margin           bool    `json:"margin"`
		AccountID        uint64  `json:"account_id"`
		BaseAccountID    *uint64 `json:"base_account_id,omitempty"`
		OnBehalfOfFundID uint64  `json:"on_behalf_of_fund_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}

	direction, err := oneOf("direction", req.Direction, "buy", "sell")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	orderType, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	// security_type is optional in the HTTP body (stock-service derives it from the listing),
	// but when provided must be one of the known kinds. When provided as "forex" we enforce
	// additional gateway-level constraints per the bank-safe settlement design (defense in depth).
	securityType := strings.ToLower(strings.TrimSpace(req.SecurityType))
	if securityType != "" {
		var stErr error
		securityType, stErr = oneOf("security_type", securityType, "stock", "futures", "forex", "option")
		if stErr != nil {
			apiError(c, 400, ErrValidation, stErr.Error())
			return
		}
	}

	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if req.ListingID == 0 {
		// listing_id is required for both buy and sell: it names the venue the
		// order will execute on. A user can sell on a different listing than
		// where they bought (holdings are per (user, security), not per
		// listing), so we cannot infer the venue from the holding.
		apiError(c, 400, ErrValidation, "listing_id is required")
		return
	}
	// Bank-acting employee orders (OwnerType=bank) skip account_id —
	// stock-service derives the bank's RSD sentinel internally. Client
	// orders (OwnerType=client) MUST specify account_id so we can charge
	// the right account and enforce ownership.
	principalType, _ := c.Get("principal_type")
	isBankActing := principalType == "employee"
	if direction == "buy" && req.AccountID == 0 && !isBankActing {
		apiError(c, 400, ErrValidation, "account_id is required for buy orders")
		return
	}
	if direction == "buy" && req.AccountID != 0 {
		acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
			return
		}
	}
	// Post-rollup (Part A): holdings aggregate per (user, security) so
	// holding_id is no longer required for sell orders. The request's
	// account_id is the proceeds destination; ownership is enforced below.
	if direction == "sell" {
		if req.AccountID == 0 && !isBankActing {
			apiError(c, 400, ErrValidation, "account_id is required for sell orders (proceeds destination)")
			return
		}
		if req.AccountID != 0 {
			acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
			if err != nil {
				handleGRPCError(c, err)
				return
			}
			if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
				return
			}
		}
	}
	if (orderType == "limit" || orderType == "stop_limit") && req.LimitValue == nil {
		apiError(c, 400, ErrValidation, "limit_value is required for limit/stop_limit orders")
		return
	}
	if (orderType == "stop" || orderType == "stop_limit") && req.StopValue == nil {
		apiError(c, 400, ErrValidation, "stop_value is required for stop/stop_limit orders")
		return
	}

	// Forex-specific validation (defense in depth; stock-service re-validates).
	if securityType == "forex" {
		if direction != "buy" {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders must be direction=buy")
			return
		}
		if req.BaseAccountID == nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders require base_account_id")
			return
		}
	}
	if req.BaseAccountID != nil && *req.BaseAccountID == req.AccountID {
		apiError(c, http.StatusBadRequest, ErrValidation, "base_account_id must differ from account_id")
		return
	}
	// Ownership check for base account, when present.
	if req.BaseAccountID != nil {
		baseAcctResp, baseErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: *req.BaseAccountID})
		if baseErr != nil {
			handleGRPCError(c, baseErr)
			return
		}
		if ownErr := enforceOwnership(c, baseAcctResp.OwnerId); ownErr != nil {
			return
		}
	}

	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	// Acting employee id (carried on the request via ResolvedIdentity) keys
	// stock-service's per-actuary EmployeeLimit gate. It must be set even
	// when the resulting order's owner is "bank" (employee acting for the
	// bank) or "client" (employee on behalf of a client) — in both cases
	// the acting employee is responsible. ResolveIdentity sets this iff
	// the principal is an employee.
	grpcReq := &stockpb.CreateOrderRequest{
		UserId:           ownerToLegacyUserID(identity.OwnerID),
		SystemType:       ownerToLegacySystemType(identity.OwnerType),
		ListingId:        req.ListingID,
		HoldingId:        req.HoldingID,
		Direction:        direction,
		OrderType:        orderType,
		Quantity:         req.Quantity,
		AllOrNone:        req.AllOrNone,
		Margin:           req.Margin,
		AccountId:        req.AccountID,
		ActingEmployeeId: derefU64Ptr(identity.ActingEmployeeID),
	}
	if req.LimitValue != nil {
		grpcReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		grpcReq.StopValue = req.StopValue
	}
	if req.BaseAccountID != nil {
		grpcReq.BaseAccountId = req.BaseAccountID
	}
	if req.OnBehalfOfFundID != 0 {
		// Reject clients placing fund orders — only employees with the
		// fund.manage permission may. Stock-service re-validates the manager
		// binding against the fund. PrincipalType (not the resolved owner
		// type) is what matters: the gate is about who's logged in.
		if identity.PrincipalType != "employee" {
			apiError(c, http.StatusForbidden, ErrForbidden, "on_behalf_of_fund_id requires employee context")
			return
		}
		grpcReq.OnBehalfOfFundId = req.OnBehalfOfFundID
	}

	resp, err := h.client.CreateOrder(c.Request.Context(), grpcReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *StockOrderHandler) ListMyOrders(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListMyOrders(c.Request.Context(), &stockpb.ListMyOrdersRequest{
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
		Status:     c.Query("status"),
		Direction:  c.Query("direction"),
		OrderType:  c.Query("order_type"),
		Page:       int32(page),
		PageSize:   int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": emptyIfNil(resp.Orders), "total_count": resp.TotalCount})
}

func (h *StockOrderHandler) GetMyOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.client.GetOrder(c.Request.Context(), &stockpb.GetOrderRequest{
		Id:         id,
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockOrderHandler) CancelOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.client.CancelOrder(c.Request.Context(), &stockpb.CancelOrderRequest{
		Id:         id,
		UserId:     ownerToLegacyUserID(identity.OwnerID),
		SystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// CreateOrderOnBehalf godoc
// @Summary      Place stock/futures/forex/option order on behalf of a client
// @Description  Employee-only. Requires orders.place-on-behalf permission. Gateway verifies the account belongs to the named client; stock-service records acting_employee_id for audit.
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        body body object true "Order"
// @Security     BearerAuth
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v2/orders [post]
func (h *StockOrderHandler) CreateOrderOnBehalf(c *gin.Context) {
	var req struct {
		ClientID      uint64  `json:"client_id"`
		AccountID     uint64  `json:"account_id"`
		SecurityType  string  `json:"security_type"`
		ListingID     uint64  `json:"listing_id"`
		HoldingID     uint64  `json:"holding_id"`
		Direction     string  `json:"direction"`
		OrderType     string  `json:"order_type"`
		Quantity      int64   `json:"quantity"`
		LimitValue    *string `json:"limit_value"`
		StopValue     *string `json:"stop_value"`
		AllOrNone     bool    `json:"all_or_none"`
		Margin        bool    `json:"margin"`
		BaseAccountID *uint64 `json:"base_account_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 {
		apiError(c, 400, ErrValidation, "client_id is required")
		return
	}
	direction, err := oneOf("direction", req.Direction, "buy", "sell")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	orderType, err := oneOf("order_type", req.OrderType, "market", "limit", "stop", "stop_limit")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	securityType := strings.ToLower(strings.TrimSpace(req.SecurityType))
	if securityType != "" {
		var stErr error
		securityType, stErr = oneOf("security_type", securityType, "stock", "futures", "forex", "option")
		if stErr != nil {
			apiError(c, 400, ErrValidation, stErr.Error())
			return
		}
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if req.ListingID == 0 {
		// Sell requires a listing to identify the execution venue — holdings
		// are keyed on (user, security), not listing, so a user may choose
		// to sell on a different venue than where they bought.
		apiError(c, 400, ErrValidation, "listing_id is required")
		return
	}
	if direction == "buy" && req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "account_id is required for buy orders")
		return
	}
	if direction == "sell" && req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "account_id is required for sell orders (proceeds destination)")
		return
	}
	if (orderType == "limit" || orderType == "stop_limit") && req.LimitValue == nil {
		apiError(c, 400, ErrValidation, "limit_value is required for limit/stop_limit orders")
		return
	}
	if (orderType == "stop" || orderType == "stop_limit") && req.StopValue == nil {
		apiError(c, 400, ErrValidation, "stop_value is required for stop/stop_limit orders")
		return
	}

	// Forex-specific validation (defense in depth; stock-service re-validates).
	if securityType == "forex" {
		if direction != "buy" {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders must be direction=buy")
			return
		}
		if req.BaseAccountID == nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "forex orders require base_account_id")
			return
		}
	}
	if req.BaseAccountID != nil && *req.BaseAccountID == req.AccountID {
		apiError(c, http.StatusBadRequest, ErrValidation, "base_account_id must differ from account_id")
		return
	}

	// Verify the account belongs to the named client. Post-rollup (Part A)
	// sell orders also carry an account_id (proceeds destination) which must
	// belong to the client.
	if direction == "buy" || direction == "sell" {
		acctResp, acctErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
		if acctErr != nil {
			handleGRPCError(c, acctErr)
			return
		}
		if acctResp.OwnerId != req.ClientID {
			apiError(c, 403, ErrForbidden, "account does not belong to client")
			return
		}
	}
	// Also verify the base account belongs to the named client when provided.
	if req.BaseAccountID != nil {
		baseAcctResp, baseErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: *req.BaseAccountID})
		if baseErr != nil {
			handleGRPCError(c, baseErr)
			return
		}
		if baseAcctResp.OwnerId != req.ClientID {
			apiError(c, 403, ErrForbidden, "base account does not belong to client")
			return
		}
	}

	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	grpcReq := &stockpb.CreateOrderRequest{
		UserId:             req.ClientID,
		SystemType:         "employee",
		ListingId:          req.ListingID,
		HoldingId:          req.HoldingID,
		Direction:          direction,
		OrderType:          orderType,
		Quantity:           req.Quantity,
		AllOrNone:          req.AllOrNone,
		Margin:             req.Margin,
		AccountId:          req.AccountID,
		ActingEmployeeId:   derefU64Ptr(identity.ActingEmployeeID),
		OnBehalfOfClientId: req.ClientID,
	}
	if req.LimitValue != nil {
		grpcReq.LimitValue = req.LimitValue
	}
	if req.StopValue != nil {
		grpcReq.StopValue = req.StopValue
	}
	if req.BaseAccountID != nil {
		grpcReq.BaseAccountId = req.BaseAccountID
	}

	resp, err := h.client.CreateOrder(c.Request.Context(), grpcReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *StockOrderHandler) ListOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListOrders(c.Request.Context(), &stockpb.ListOrdersRequest{
		Status:     c.Query("status"),
		AgentEmail: c.Query("agent_email"),
		Direction:  c.Query("direction"),
		OrderType:  c.Query("order_type"),
		Page:       int32(page),
		PageSize:   int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": emptyIfNil(resp.Orders), "total_count": resp.TotalCount})
}

func (h *StockOrderHandler) ApproveOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.client.ApproveOrder(c.Request.Context(), &stockpb.ApproveOrderRequest{
		Id:           id,
		SupervisorId: derefU64Ptr(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *StockOrderHandler) RejectOrder(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid order id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)

	resp, err := h.client.DeclineOrder(c.Request.Context(), &stockpb.DeclineOrderRequest{
		Id:           id,
		SupervisorId: derefU64Ptr(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
