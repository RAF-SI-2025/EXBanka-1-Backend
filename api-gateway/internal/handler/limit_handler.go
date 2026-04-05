package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)

// setEmployeeLimitsBody is the request body for setting employee limits.
type setEmployeeLimitsBody struct {
	MaxLoanApprovalAmount string `json:"max_loan_approval_amount" example:"50000.0000"`
	MaxSingleTransaction  string `json:"max_single_transaction" example:"100000.0000"`
	MaxDailyTransaction   string `json:"max_daily_transaction" example:"500000.0000"`
	MaxClientDailyLimit   string `json:"max_client_daily_limit" example:"250000.0000"`
	MaxClientMonthlyLimit string `json:"max_client_monthly_limit" example:"2500000.0000"`
}

// createLimitTemplateBody is the request body for creating a limit template.
type createLimitTemplateBody struct {
	Name                  string `json:"name" binding:"required" example:"BasicTeller"`
	Description           string `json:"description" example:"Default teller limits"`
	MaxLoanApprovalAmount string `json:"max_loan_approval_amount" example:"50000.0000"`
	MaxSingleTransaction  string `json:"max_single_transaction" example:"100000.0000"`
	MaxDailyTransaction   string `json:"max_daily_transaction" example:"500000.0000"`
	MaxClientDailyLimit   string `json:"max_client_daily_limit" example:"250000.0000"`
	MaxClientMonthlyLimit string `json:"max_client_monthly_limit" example:"2500000.0000"`
}

// applyLimitTemplateBody is the request body for applying a limit template.
type applyLimitTemplateBody struct {
	TemplateName string `json:"template_name" binding:"required" example:"BasicTeller"`
}

// setClientLimitsBody is the request body for setting client limits.
type setClientLimitsBody struct {
	DailyLimit    string `json:"daily_limit" binding:"required" example:"100000.0000"`
	MonthlyLimit  string `json:"monthly_limit" binding:"required" example:"1000000.0000"`
	TransferLimit string `json:"transfer_limit" binding:"required" example:"50000.0000"`
}

// LimitHandler handles REST endpoints for employee and client limits.
type LimitHandler struct {
	empLimitClient    userpb.EmployeeLimitServiceClient
	clientLimitClient clientpb.ClientLimitServiceClient
}

// NewLimitHandler constructs a LimitHandler.
func NewLimitHandler(
	empLimitClient userpb.EmployeeLimitServiceClient,
	clientLimitClient clientpb.ClientLimitServiceClient,
) *LimitHandler {
	return &LimitHandler{
		empLimitClient:    empLimitClient,
		clientLimitClient: clientLimitClient,
	}
}

// GetEmployeeLimits godoc
// @Summary      Get employee limits
// @Description  Returns the transaction and approval limits for an employee
// @Tags         limits
// @Produce      json
// @Param        id   path   int  true  "Employee ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "employee limits"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/employees/{id}/limits [get]
func (h *LimitHandler) GetEmployeeLimits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid employee id")
		return
	}

	resp, err := h.empLimitClient.GetEmployeeLimits(c.Request.Context(), &userpb.EmployeeLimitRequest{
		EmployeeId: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// SetEmployeeLimits godoc
// @Summary      Set employee limits
// @Description  Creates or updates the transaction and approval limits for an employee
// @Tags         limits
// @Accept       json
// @Produce      json
// @Param        id    path  int                     true  "Employee ID"
// @Param        body  body  setEmployeeLimitsBody  true  "Limit values (all amounts as decimal strings)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated limits"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/employees/{id}/limits [put]
func (h *LimitHandler) SetEmployeeLimits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid employee id")
		return
	}

	var body setEmployeeLimitsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.empLimitClient.SetEmployeeLimits(middleware.GRPCContextWithChangedBy(c), &userpb.SetEmployeeLimitsRequest{
		EmployeeId:            id,
		MaxLoanApprovalAmount: body.MaxLoanApprovalAmount,
		MaxSingleTransaction:  body.MaxSingleTransaction,
		MaxDailyTransaction:   body.MaxDailyTransaction,
		MaxClientDailyLimit:   body.MaxClientDailyLimit,
		MaxClientMonthlyLimit: body.MaxClientMonthlyLimit,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ApplyLimitTemplate godoc
// @Summary      Apply limit template to employee
// @Description  Copies a named template's limit values to the specified employee
// @Tags         limits
// @Accept       json
// @Produce      json
// @Param        id    path  int                    true  "Employee ID"
// @Param        body  body  applyLimitTemplateBody  true  "Template name to apply"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated limits"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/employees/{id}/limits/template [post]
func (h *LimitHandler) ApplyLimitTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid employee id")
		return
	}

	var body applyLimitTemplateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.empLimitClient.ApplyLimitTemplate(c.Request.Context(), &userpb.ApplyLimitTemplateRequest{
		EmployeeId:   id,
		TemplateName: body.TemplateName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListLimitTemplates godoc
// @Summary      List limit templates
// @Description  Returns all predefined limit templates
// @Tags         limits
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "list of templates"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/limits/templates [get]
func (h *LimitHandler) ListLimitTemplates(c *gin.Context) {
	resp, err := h.empLimitClient.ListLimitTemplates(c.Request.Context(), &userpb.ListLimitTemplatesRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// CreateLimitTemplate godoc
// @Summary      Create limit template
// @Description  Creates a new named limit template (admin only)
// @Tags         limits
// @Accept       json
// @Produce      json
// @Param        body  body  createLimitTemplateBody  true  "Template definition"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created template"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/limits/templates [post]
func (h *LimitHandler) CreateLimitTemplate(c *gin.Context) {
	var body createLimitTemplateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.empLimitClient.CreateLimitTemplate(c.Request.Context(), &userpb.CreateLimitTemplateRequest{
		Name:                  body.Name,
		Description:           body.Description,
		MaxLoanApprovalAmount: body.MaxLoanApprovalAmount,
		MaxSingleTransaction:  body.MaxSingleTransaction,
		MaxDailyTransaction:   body.MaxDailyTransaction,
		MaxClientDailyLimit:   body.MaxClientDailyLimit,
		MaxClientMonthlyLimit: body.MaxClientMonthlyLimit,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// GetClientLimits godoc
// @Summary      Get client limits
// @Description  Returns the transaction limits configured for a client
// @Tags         limits
// @Produce      json
// @Param        id   path   int  true  "Client ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "client limits"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/clients/{id}/limits [get]
func (h *LimitHandler) GetClientLimits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid client id")
		return
	}

	resp, err := h.clientLimitClient.GetClientLimits(c.Request.Context(), &clientpb.GetClientLimitRequest{
		ClientId: id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// SetClientLimits godoc
// @Summary      Set client limits
// @Description  Sets the transaction limits for a client (employee only, constrained by employee limits)
// @Tags         limits
// @Accept       json
// @Produce      json
// @Param        id    path  int                        true  "Client ID"
// @Param        body  body  setClientLimitsBody         true  "Limit values"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated client limits"
// @Failure      400  {object}  map[string]string       "invalid input or limit exceeded"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/clients/{id}/limits [put]
func (h *LimitHandler) SetClientLimits(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid client id")
		return
	}

	employeeID, _ := c.Get("user_id")
	empID, _ := employeeID.(int64)

	var body setClientLimitsBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.clientLimitClient.SetClientLimits(middleware.GRPCContextWithChangedBy(c), &clientpb.SetClientLimitRequest{
		ClientId:      id,
		DailyLimit:    body.DailyLimit,
		MonthlyLimit:  body.MonthlyLimit,
		TransferLimit: body.TransferLimit,
		SetByEmployee: empID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
