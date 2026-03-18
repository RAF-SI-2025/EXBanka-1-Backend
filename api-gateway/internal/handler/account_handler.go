package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	accountpb "github.com/exbanka/contract/accountpb"
)

type AccountHandler struct {
	accountClient accountpb.AccountServiceClient
}

func NewAccountHandler(accountClient accountpb.AccountServiceClient) *AccountHandler {
	return &AccountHandler{accountClient: accountClient}
}

type createAccountRequest struct {
	OwnerID         uint64   `json:"owner_id" binding:"required"`
	AccountKind     string   `json:"account_kind" binding:"required"`
	AccountType     string   `json:"account_type" binding:"required"`
	AccountCategory string   `json:"account_category"`
	CurrencyCode    string   `json:"currency_code" binding:"required"`
	EmployeeID      uint64   `json:"employee_id"`
	InitialBalance  float64  `json:"initial_balance"`
	CreateCard      bool     `json:"create_card"`
	CompanyID       *uint64  `json:"company_id"`
}

// @Summary      Create account
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body  body  createAccountRequest  true  "Account data"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/accounts [post]
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pbReq := &accountpb.CreateAccountRequest{
		OwnerId:         req.OwnerID,
		AccountKind:     req.AccountKind,
		AccountType:     req.AccountType,
		AccountCategory: req.AccountCategory,
		CurrencyCode:    req.CurrencyCode,
		EmployeeId:      req.EmployeeID,
		InitialBalance:  fmt.Sprintf("%.4f", req.InitialBalance),
		CreateCard:      req.CreateCard,
		CompanyId:       req.CompanyID,
	}

	resp, err := h.accountClient.CreateAccount(c.Request.Context(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, accountToJSON(resp))
}

// @Summary      List accounts
// @Tags         accounts
// @Produce      json
// @Param        page                  query  int     false  "Page number (default 1)"
// @Param        page_size             query  int     false  "Items per page (default 20)"
// @Param        name_filter           query  string  false  "Filter by account name"
// @Param        account_number_filter query  string  false  "Filter by account number"
// @Param        type_filter           query  string  false  "Filter by account type"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/accounts [get]
func (h *AccountHandler) ListAllAccounts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.accountClient.ListAllAccounts(c.Request.Context(), &accountpb.ListAllAccountsRequest{
		NameFilter:          c.Query("name_filter"),
		AccountNumberFilter: c.Query("account_number_filter"),
		TypeFilter:          c.Query("type_filter"),
		Page:                int32(page),
		PageSize:            int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	accounts := make([]gin.H, 0, len(resp.Accounts))
	for _, acc := range resp.Accounts {
		accounts = append(accounts, accountToJSON(acc))
	}
	c.JSON(http.StatusOK, gin.H{
		"accounts": accounts,
		"total":    resp.Total,
	})
}

// @Summary      Get account by ID
// @Tags         accounts
// @Produce      json
// @Param        id   path  int  true  "Account ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /api/accounts/{id} [get]
func (h *AccountHandler) GetAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// @Summary      Get account by number
// @Tags         accounts
// @Produce      json
// @Param        account_number  path  string  true  "Account number"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /api/accounts/by-number/{account_number} [get]
func (h *AccountHandler) GetAccountByNumber(c *gin.Context) {
	accountNumber := c.Param("account_number")
	resp, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{
		AccountNumber: accountNumber,
	})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// @Summary      List accounts by client
// @Tags         accounts
// @Produce      json
// @Param        client_id  path   int  true   "Client ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Items per page (default 20)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/accounts/client/{client_id} [get]
func (h *AccountHandler) ListAccountsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.accountClient.ListAccountsByClient(c.Request.Context(), &accountpb.ListAccountsByClientRequest{
		ClientId: clientID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	accounts := make([]gin.H, 0, len(resp.Accounts))
	for _, acc := range resp.Accounts {
		accounts = append(accounts, accountToJSON(acc))
	}
	c.JSON(http.StatusOK, gin.H{
		"accounts": accounts,
		"total":    resp.Total,
	})
}

type updateAccountNameRequest struct {
	NewName  string `json:"new_name" binding:"required"`
	ClientID uint64 `json:"client_id"`
}

// @Summary      Update account name
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        id    path  int                       true  "Account ID"
// @Param        body  body  updateAccountNameRequest  true  "New name"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/accounts/{id}/name [put]
func (h *AccountHandler) UpdateAccountName(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateAccountNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.accountClient.UpdateAccountName(c.Request.Context(), &accountpb.UpdateAccountNameRequest{
		Id:       id,
		ClientId: req.ClientID,
		NewName:  req.NewName,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

type updateAccountLimitsRequest struct {
	DailyLimit   *float64 `json:"daily_limit"`
	MonthlyLimit *float64 `json:"monthly_limit"`
}

// @Summary      Update account limits
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        id    path  int                         true  "Account ID"
// @Param        body  body  updateAccountLimitsRequest  true  "Limit values"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/accounts/{id}/limits [put]
func (h *AccountHandler) UpdateAccountLimits(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateAccountLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pbLimitsReq := &accountpb.UpdateAccountLimitsRequest{Id: id}
	if req.DailyLimit != nil {
		s := fmt.Sprintf("%.4f", *req.DailyLimit)
		pbLimitsReq.DailyLimit = &s
	}
	if req.MonthlyLimit != nil {
		s := fmt.Sprintf("%.4f", *req.MonthlyLimit)
		pbLimitsReq.MonthlyLimit = &s
	}
	resp, err := h.accountClient.UpdateAccountLimits(c.Request.Context(), pbLimitsReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

type updateAccountStatusRequest struct {
	Status string `json:"status" binding:"required"`
}

// @Summary      Update account status
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        id    path  int                         true  "Account ID"
// @Param        body  body  updateAccountStatusRequest  true  "New status"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/accounts/{id}/status [put]
func (h *AccountHandler) UpdateAccountStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateAccountStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.accountClient.UpdateAccountStatus(c.Request.Context(), &accountpb.UpdateAccountStatusRequest{
		Id:     id,
		Status: req.Status,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// @Summary      List currencies
// @Tags         accounts
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]string
// @Router       /api/currencies [get]
func (h *AccountHandler) ListCurrencies(c *gin.Context) {
	resp, err := h.accountClient.ListCurrencies(c.Request.Context(), &accountpb.ListCurrenciesRequest{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	currencies := make([]gin.H, 0, len(resp.Currencies))
	for _, cur := range resp.Currencies {
		currencies = append(currencies, gin.H{
			"code":   cur.Code,
			"name":   cur.Name,
			"symbol": cur.Symbol,
		})
	}
	c.JSON(http.StatusOK, gin.H{"currencies": currencies})
}

type createCompanyRequest struct {
	CompanyName        string `json:"company_name" binding:"required"`
	RegistrationNumber string `json:"registration_number" binding:"required"`
	TaxNumber          string `json:"tax_number"`
	ActivityCode       string `json:"activity_code"`
	Address            string `json:"address"`
	OwnerID            uint64 `json:"owner_id" binding:"required"`
}

// @Summary      Create company
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body  body  createCompanyRequest  true  "Company data"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/companies [post]
func (h *AccountHandler) CreateCompany(c *gin.Context) {
	var req createCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.accountClient.CreateCompany(c.Request.Context(), &accountpb.CreateCompanyRequest{
		CompanyName:        req.CompanyName,
		RegistrationNumber: req.RegistrationNumber,
		TaxNumber:          req.TaxNumber,
		ActivityCode:       req.ActivityCode,
		Address:            req.Address,
		OwnerId:            req.OwnerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":                  resp.Id,
		"company_name":        resp.CompanyName,
		"registration_number": resp.RegistrationNumber,
		"tax_number":          resp.TaxNumber,
		"activity_code":       resp.ActivityCode,
		"address":             resp.Address,
		"owner_id":            resp.OwnerId,
	})
}

func accountToJSON(acc *accountpb.AccountResponse) gin.H {
	return gin.H{
		"id":                acc.Id,
		"account_number":    acc.AccountNumber,
		"account_name":      acc.AccountName,
		"owner_id":          acc.OwnerId,
		"owner_name":        acc.OwnerName,
		"balance":           acc.Balance,
		"available_balance": acc.AvailableBalance,
		"employee_id":       acc.EmployeeId,
		"created_at":        acc.CreatedAt,
		"expires_at":        acc.ExpiresAt,
		"currency_code":     acc.CurrencyCode,
		"status":            acc.Status,
		"account_kind":      acc.AccountKind,
		"account_type":      acc.AccountType,
		"account_category":  acc.AccountCategory,
		"maintenance_fee":   acc.MaintenanceFee,
		"daily_limit":       acc.DailyLimit,
		"monthly_limit":     acc.MonthlyLimit,
		"daily_spending":    acc.DailySpending,
		"monthly_spending":  acc.MonthlySpending,
		"company_id":        acc.CompanyId,
	}
}
