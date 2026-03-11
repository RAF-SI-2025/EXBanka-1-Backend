package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list employees"})
		return
	}

	employees := make([]gin.H, 0, len(resp.Employees))
	for _, emp := range resp.Employees {
		employees = append(employees, employeeToJSON(emp))
	}
	c.JSON(http.StatusOK, gin.H{
		"employees":   employees,
		"total_count": resp.TotalCount,
	})
}

func (h *EmployeeHandler) GetEmployee(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "employee not found"})
		return
	}
	c.JSON(http.StatusOK, employeeToJSON(resp))
}

type createEmployeeRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth" binding:"required"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required,email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	Username    string `json:"username" binding:"required"`
	Position    string `json:"position"`
	Department  string `json:"department"`
	Role        string `json:"role" binding:"required"`
	Active      bool   `json:"active"`
}

func (h *EmployeeHandler) CreateEmployee(c *gin.Context) {
	var req createEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		Username:    req.Username,
		Position:    req.Position,
		Department:  req.Department,
		Role:        req.Role,
		Active:      req.Active,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Orchestrate: tell auth-service to create activation token and send email
	_, _ = h.authClient.CreateActivationToken(c.Request.Context(), &authpb.CreateActivationTokenRequest{
		UserId:    resp.Id,
		Email:     resp.Email,
		FirstName: resp.FirstName,
	})

	c.JSON(http.StatusCreated, employeeToJSON(resp))
}

type updateEmployeeRequest struct {
	LastName   *string `json:"last_name"`
	Gender     *string `json:"gender"`
	Phone      *string `json:"phone"`
	Address    *string `json:"address"`
	Position   *string `json:"position"`
	Department *string `json:"department"`
	Role       *string `json:"role"`
	Active     *bool   `json:"active"`
}

func (h *EmployeeHandler) UpdateEmployee(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// Admins can only edit non-admin employees
	target, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "employee not found"})
		return
	}
	if target.Role == "EmployeeAdmin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot edit admin employees"})
		return
	}

	var req updateEmployeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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
	if req.Position != nil {
		pbReq.Position = req.Position
	}
	if req.Department != nil {
		pbReq.Department = req.Department
	}
	if req.Role != nil {
		pbReq.Role = req.Role
	}
	if req.Active != nil {
		pbReq.Active = req.Active
	}

	resp, err := h.userClient.UpdateEmployee(c.Request.Context(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, employeeToJSON(resp))
}

func employeeToJSON(emp *userpb.EmployeeResponse) gin.H {
	return gin.H{
		"id":            emp.Id,
		"first_name":    emp.FirstName,
		"last_name":     emp.LastName,
		"date_of_birth": emp.DateOfBirth,
		"gender":        emp.Gender,
		"email":         emp.Email,
		"phone":         emp.Phone,
		"address":       emp.Address,
		"username":      emp.Username,
		"position":      emp.Position,
		"department":    emp.Department,
		"active":        emp.Active,
		"role":          emp.Role,
		"permissions":   emp.Permissions,
	}
}
