package handler

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	authpb "github.com/exbanka/contract/authpb"
	userpb "github.com/exbanka/contract/userpb"
)

type EmployeeHandler struct {
	userClient userpb.UserServiceClient
	authClient authpb.AuthServiceClient
}

func NewEmployeeHandler(userClient userpb.UserServiceClient, authClient authpb.AuthServiceClient) *EmployeeHandler {
	return &EmployeeHandler{userClient: userClient, authClient: authClient}
}

// ListEmployees godoc
// @Summary      List employees
// @Description  Get paginated list of employees with optional filters
// @Tags         employees
// @Produce      json
// @Param        page       query  int     false  "Page number"  default(1)
// @Param        page_size  query  int     false  "Page size"    default(20)
// @Param        email      query  string  false  "Filter by email (partial match)"
// @Param        name       query  string  false  "Filter by first/last name (partial match)"
// @Param        position   query  string  false  "Filter by position (partial match)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "employees array and total_count"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/employees [get]
func (h *EmployeeHandler) ListEmployees(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.userClient.ListEmployees(c.Request.Context(), &userpb.ListEmployeesRequest{
		EmailFilter:    c.Query("email"),
		NameFilter:     c.Query("name"),
		PositionFilter: c.Query("position"),
		Page:           int32(page),
		PageSize:       int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	ids := make([]int64, 0, len(resp.Employees))
	for _, emp := range resp.Employees {
		ids = append(ids, emp.Id)
	}
	statusMap := make(map[int64]bool)
	if len(ids) > 0 {
		if batchResp, batchErr := h.authClient.GetAccountStatusBatch(c.Request.Context(), &authpb.GetAccountStatusBatchRequest{
			PrincipalType: "employee",
			PrincipalIds:  ids,
		}); batchErr == nil {
			for _, entry := range batchResp.Entries {
				statusMap[entry.PrincipalId] = entry.Active
			}
		} else {
			log.Printf("WARN: failed to fetch account statuses for employees: %v", batchErr)
		}
	}

	employees := make([]gin.H, 0, len(resp.Employees))
	for _, emp := range resp.Employees {
		employees = append(employees, employeeToJSONWithActive(emp, statusMap[emp.Id]))
	}
	c.JSON(http.StatusOK, gin.H{
		"employees":   employees,
		"total_count": resp.TotalCount,
	})
}

// GetEmployee godoc
// @Summary      Get employee by ID
// @Description  Retrieve a single employee's details
// @Tags         employees
// @Produce      json
// @Param        id  path  int  true  "Employee ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "employee details"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      404  {object}  map[string]string       "not found"
// @Router       /api/employees/{id} [get]
func (h *EmployeeHandler) GetEmployee(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	active := false
	if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
		PrincipalType: "employee",
		PrincipalId:   id,
	}); statusErr == nil {
		active = statusResp.Active
	} else {
		log.Printf("WARN: failed to fetch account status for employee %d: %v", id, statusErr)
	}
	c.JSON(http.StatusOK, employeeToJSONWithActive(resp, active))
}

type createEmployeeRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth" binding:"required"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required,email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	JMBG        string `json:"jmbg" binding:"required"`
	Username    string `json:"username" binding:"required"`
	Position    string `json:"position"`
	Department  string `json:"department"`
	Role        string `json:"role" binding:"required"`
}

// CreateEmployee godoc
// @Summary      Create a new employee
// @Description  Create employee and send activation email. Requires employees.create permission.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        body  body  createEmployeeRequest  true  "Employee data"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error message"
// @Router       /api/employees [post]
func (h *EmployeeHandler) CreateEmployee(c *gin.Context) {
	var req createEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.userClient.CreateEmployee(c.Request.Context(), &userpb.CreateEmployeeRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		Jmbg:        req.JMBG,
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Role:        req.Role,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	// Activation email is triggered via Kafka (user.employee-created event consumed by auth-service)
	// New accounts always start as "pending" — auth-service handles this automatically
	c.JSON(http.StatusCreated, employeeToJSONWithActive(resp, false))
}

type updateEmployeeRequest struct {
	LastName   *string `json:"last_name"`
	Gender     *string `json:"gender"`
	Phone      *string `json:"phone"`
	Address    *string `json:"address"`
	JMBG       *string `json:"jmbg"`
	Position   *string `json:"position"`
	Department *string `json:"department"`
	Role       *string `json:"role"`
	Active     *bool   `json:"active"`
}

// UpdateEmployee godoc
// @Summary      Update an employee
// @Description  Partially update employee fields. Cannot edit admin employees.
// @Tags         employees
// @Accept       json
// @Produce      json
// @Param        id    path  int                    true  "Employee ID"
// @Param        body  body  updateEmployeeRequest  true  "Fields to update"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated employee"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      403  {object}  map[string]string       "cannot edit admin"
// @Failure      404  {object}  map[string]string       "not found"
// @Router       /api/employees/{id} [put]
func (h *EmployeeHandler) UpdateEmployee(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	// Admins can only edit non-admin employees
	target, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if target.Role == "EmployeeAdmin" {
		apiError(c, 403, ErrForbidden, "cannot edit admin employees")
		return
	}

	var req updateEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	hasProfileUpdate := req.LastName != nil || req.Gender != nil || req.Phone != nil ||
		req.Address != nil || req.JMBG != nil || req.Position != nil ||
		req.Department != nil || req.Role != nil

	var resp *userpb.EmployeeResponse
	if hasProfileUpdate {
		pbReq := &userpb.UpdateEmployeeRequest{Id: id}
		if req.LastName != nil {
			pbReq.LastName = req.LastName
		}
		if req.Gender != nil {
			pbReq.Gender = req.Gender
		}
		if req.Phone != nil {
			pbReq.Phone = req.Phone
		}
		if req.Address != nil {
			pbReq.Address = req.Address
		}
		if req.JMBG != nil {
			pbReq.Jmbg = req.JMBG
		}
		if req.Position != nil {
			pbReq.Position = req.Position
		}
		if req.Department != nil {
			pbReq.Department = req.Department
		}
		if req.Role != nil {
			pbReq.Role = req.Role
		}

		resp, err = h.userClient.UpdateEmployee(middleware.GRPCContextWithChangedBy(c), pbReq)
		if err != nil {
			handleGRPCError(c, err)
			return
		}
	} else {
		// No profile fields — still fetch current employee for response
		var fetchErr error
		resp, fetchErr = h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
		if fetchErr != nil {
			handleGRPCError(c, fetchErr)
			return
		}
	}

	if req.Active != nil {
		_, authErr := h.authClient.SetAccountStatus(c.Request.Context(), &authpb.SetAccountStatusRequest{
			PrincipalType: "employee",
			PrincipalId:   id,
			Active:        *req.Active,
		})
		if authErr != nil {
			handleGRPCError(c, authErr)
			return
		}
	}

	// Re-fetch active status to return accurate value
	active := false
	if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
		PrincipalType: "employee",
		PrincipalId:   id,
	}); statusErr == nil {
		active = statusResp.Active
	} else {
		log.Printf("WARN: failed to fetch account status for employee %d: %v", id, statusErr)
	}
	c.JSON(http.StatusOK, employeeToJSONWithActive(resp, active))
}

func employeeToJSONWithActive(emp *userpb.EmployeeResponse, active bool) gin.H {
	return gin.H{
		"id":            emp.Id,
		"first_name":    emp.FirstName,
		"last_name":     emp.LastName,
		"date_of_birth": emp.DateOfBirth,
		"gender":        emp.Gender,
		"email":         emp.Email,
		"phone":         emp.Phone,
		"address":       emp.Address,
		"jmbg":          emp.Jmbg,
		"username":      emp.Username,
		"position":      emp.Position,
		"department":    emp.Department,
		"active":        active,
		"role":          emp.Role,
		"roles":         emp.Roles,
		"permissions":   emp.Permissions,
	}
}
