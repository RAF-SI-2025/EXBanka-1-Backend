package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	cardpb "github.com/exbanka/contract/cardpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

type AccountHandler struct {
	accountClient     accountpb.AccountServiceClient
	bankAccountClient accountpb.BankAccountServiceClient
	cardClient        cardpb.CardServiceClient
	transactionClient transactionpb.TransactionServiceClient
}

func NewAccountHandler(accountClient accountpb.AccountServiceClient, bankAccountClient accountpb.BankAccountServiceClient, cardClient cardpb.CardServiceClient, transactionClient transactionpb.TransactionServiceClient) *AccountHandler {
	return &AccountHandler{accountClient: accountClient, bankAccountClient: bankAccountClient, cardClient: cardClient, transactionClient: transactionClient}
}

type createBankAccountBody struct {
	CurrencyCode string `json:"currency_code" binding:"required" example:"RSD"`
	AccountKind  string `json:"account_kind" binding:"required" example:"current"`
	AccountName  string `json:"account_name" example:"EX Banka RSD Account"`
}

type createAccountRequest struct {
	OwnerID         uint64  `json:"owner_id" binding:"required"`
	AccountKind     string  `json:"account_kind" binding:"required"`
	AccountType     string  `json:"account_type" binding:"required"`
	AccountCategory string  `json:"account_category"`
	CurrencyCode    string  `json:"currency_code" binding:"required"`
	EmployeeID      uint64  `json:"employee_id"`
	InitialBalance  float64 `json:"initial_balance"`
	CreateCard      bool    `json:"create_card"`
	CardBrand       string  `json:"card_brand"`
	CompanyID       *uint64 `json:"company_id"`
}

// @Summary      Create account
// @Description  Creates a new bank account for a client. Requires accounts.create permission.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        body  body  createAccountRequest  true  "Account data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/accounts [post]
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	accountKind, err := oneOf("account_kind", req.AccountKind, "current", "foreign")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	var accountCategory string
	if req.AccountCategory != "" {
		accountCategory, err = oneOf("account_category", req.AccountCategory, "personal", "business")
		if err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	if err := nonNegative("initial_balance", req.InitialBalance); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	pbReq := &accountpb.CreateAccountRequest{
		OwnerId:         req.OwnerID,
		AccountKind:     accountKind,
		AccountType:     req.AccountType,
		AccountCategory: accountCategory,
		CurrencyCode:    req.CurrencyCode,
		EmployeeId:      req.EmployeeID,
		InitialBalance:  fmt.Sprintf("%.4f", req.InitialBalance),
		CreateCard:      req.CreateCard,
		CompanyId:       req.CompanyID,
	}

	resp, err := h.accountClient.CreateAccount(c.Request.Context(), pbReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	result := accountToJSON(resp)

	if req.CreateCard && h.cardClient != nil {
		brand := req.CardBrand
		if brand == "" {
			brand = "visa"
		}
		brand, err = oneOf("card_brand", brand, "visa", "mastercard", "dinacard", "amex")
		if err != nil {
			result["card_error"] = err.Error()
			c.JSON(http.StatusCreated, result)
			return
		}
		cardResp, cardErr := h.cardClient.CreateCard(c.Request.Context(), &cardpb.CreateCardRequest{
			AccountNumber: resp.AccountNumber,
			OwnerId:       req.OwnerID,
			OwnerType:     "client",
			CardBrand:     brand,
		})
		if cardErr != nil {
			log.Printf("warn: account created but card creation failed: %v", cardErr)
			result["card_error"] = cardErr.Error()
		} else {
			result["card"] = cardToJSON(cardResp)
		}
	}

	c.JSON(http.StatusCreated, result)
}

// @Summary      List accounts
// @Tags         accounts
// @Produce      json
// @Param        page                  query  int     false  "Page number (default 1)"
// @Param        page_size             query  int     false  "Items per page (default 20)"
// @Param        client_id             query  int     false  "Filter by client owner ID (mutually exclusive with account_number)"
// @Param        account_number        query  string  false  "Filter by exact account number (mutually exclusive with client_id)"
// @Param        name_filter           query  string  false  "Filter by account name"
// @Param        account_number_filter query  string  false  "Filter by account number substring"
// @Param        type_filter           query  string  false  "Filter by account type"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v3/accounts [get]
func (h *AccountHandler) ListAllAccounts(c *gin.Context) {
	clientIDStr := c.Query("client_id")
	accountNumber := c.Query("account_number")

	// Mutually exclusive filter params — both set is a client error.
	if clientIDStr != "" && accountNumber != "" {
		apiError(c, 400, ErrValidation, "client_id and account_number are mutually exclusive")
		return
	}

	// Query-param filtering: ?account_number=X — exact-match single lookup.
	// Wraps GetAccountByNumber to keep the response shape consistent with the
	// list endpoint. NotFound → empty result rather than a hard 404.
	if accountNumber != "" {
		resp, err := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{
			AccountNumber: accountNumber,
		})
		if err != nil {
			if isNotFound(err) {
				c.JSON(http.StatusOK, gin.H{"accounts": []gin.H{}, "total": 0})
				return
			}
			handleGRPCError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"accounts": []gin.H{accountToJSON(resp)}, "total": 1})
		return
	}

	// Query-param filtering: ?client_id=X
	if clientIDStr != "" {
		clientID, err := strconv.ParseUint(clientIDStr, 10, 64)
		if err != nil {
			apiError(c, 400, ErrValidation, "invalid client_id query parameter")
			return
		}
		page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
		pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "50"), 10, 32)
		resp, err := h.accountClient.ListAccountsByClient(c.Request.Context(), &accountpb.ListAccountsByClientRequest{
			ClientId: clientID,
			Page:     int32(page),
			PageSize: int32(pageSize),
		})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		accounts := make([]gin.H, 0, len(resp.Accounts))
		for _, acc := range resp.Accounts {
			accounts = append(accounts, accountToJSON(acc))
		}
		c.JSON(http.StatusOK, gin.H{"accounts": accounts, "total": resp.Total})
		return
	}

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
		handleGRPCError(c, err)
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
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/v2/accounts/{id} [get]
func (h *AccountHandler) GetAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
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
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/accounts/client/{client_id} [get]
func (h *AccountHandler) ListAccountsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid client_id")
		return
	}
	if !enforceClientSelf(c, clientID) {
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
		handleGRPCError(c, err)
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
// @Description  Updates the display name of a bank account. Requires accounts.update permission.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        id    path  int                       true  "Account ID"
// @Param        body  body  updateAccountNameRequest  true  "New name"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/accounts/{id}/name [put]
func (h *AccountHandler) UpdateAccountName(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	var req updateAccountNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.accountClient.UpdateAccountName(middleware.GRPCContextWithChangedBy(c), &accountpb.UpdateAccountNameRequest{
		Id:       id,
		ClientId: req.ClientID,
		NewName:  req.NewName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

type updateAccountLimitsRequest struct {
	DailyLimit   *float64 `json:"daily_limit"`
	MonthlyLimit *float64 `json:"monthly_limit"`
}

// @Summary      Update account limits
// @Description  Updates the daily/monthly/transfer limits for a bank account. Requires accounts.update permission.
// @Tags         accounts
// @Accept       json
// @Produce      json
// @Param        id    path  int                         true  "Account ID"
// @Param        body  body  updateAccountLimitsRequest  true  "Limit values"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/accounts/{id}/limits [put]
func (h *AccountHandler) UpdateAccountLimits(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	var req updateAccountLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	if req.DailyLimit != nil {
		if err := nonNegative("daily_limit", *req.DailyLimit); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}
	if req.MonthlyLimit != nil {
		if err := nonNegative("monthly_limit", *req.MonthlyLimit); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
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
	resp, err := h.accountClient.UpdateAccountLimits(middleware.GRPCContextWithChangedBy(c), pbLimitsReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// setAccountStatus is the shared core for the activate/deactivate action pair.
// The HTTP verb is POST and the desired status is hard-coded by the caller —
// no body is read.
func (h *AccountHandler) setAccountStatus(c *gin.Context, status string) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.accountClient.UpdateAccountStatus(middleware.GRPCContextWithChangedBy(c), &accountpb.UpdateAccountStatusRequest{
		Id:     id,
		Status: status,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// ActivateAccount godoc
// @Summary      Activate an account
// @Description  Marks the account as active. Idempotent. Requires accounts.deactivate permission (status changes cluster under deactivate).
// @Tags         accounts
// @Produce      json
// @Param        id    path  int  true  "Account ID"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      404   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v3/accounts/{id}/activate [post]
func (h *AccountHandler) ActivateAccount(c *gin.Context) {
	h.setAccountStatus(c, "active")
}

// DeactivateAccount godoc
// @Summary      Deactivate an account
// @Description  Marks the account as inactive. Idempotent. Requires accounts.deactivate permission.
// @Tags         accounts
// @Produce      json
// @Param        id    path  int  true  "Account ID"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      404   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v3/accounts/{id}/deactivate [post]
func (h *AccountHandler) DeactivateAccount(c *gin.Context) {
	h.setAccountStatus(c, "inactive")
}

// @Summary      List currencies
// @Tags         accounts
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/currencies [get]
func (h *AccountHandler) ListCurrencies(c *gin.Context) {
	resp, err := h.accountClient.ListCurrencies(c.Request.Context(), &accountpb.ListCurrenciesRequest{})
	if err != nil {
		handleGRPCError(c, err)
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
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/companies [post]
func (h *AccountHandler) CreateCompany(c *gin.Context) {
	var req createCompanyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	if req.ActivityCode != "" {
		if err := validateActivityCode(req.ActivityCode); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
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
		handleGRPCError(c, err)
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

// ListBankAccounts godoc
// @Summary      List bank accounts
// @Description  Returns all bank-owned accounts. Requires bank-accounts.manage permission.
// @Tags         bank-accounts
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "bank accounts"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/bank-accounts [get]
func (h *AccountHandler) ListBankAccounts(c *gin.Context) {
	resp, err := h.bankAccountClient.ListBankAccounts(c.Request.Context(), &accountpb.ListBankAccountsRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// CreateBankAccount godoc
// @Summary      Create bank account
// @Description  Creates a bank-owned account for fee collection. Requires bank-accounts.manage permission.
// @Tags         bank-accounts
// @Accept       json
// @Produce      json
// @Param        body  body  createBankAccountBody  true  "Bank account details"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created bank account"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/bank-accounts [post]
func (h *AccountHandler) CreateBankAccount(c *gin.Context) {
	var body createBankAccountBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	bankAccountKind, err := oneOf("account_kind", body.AccountKind, "current", "foreign")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	resp, err := h.bankAccountClient.CreateBankAccount(c.Request.Context(), &accountpb.CreateBankAccountRequest{
		CurrencyCode: body.CurrencyCode,
		AccountKind:  bankAccountKind,
		AccountName:  body.AccountName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// DeleteBankAccount godoc
// @Summary      Delete bank account
// @Description  Deletes a bank-owned account. Requires bank-accounts.manage permission. Fails if it's the last RSD or last foreign currency bank account.
// @Tags         bank-accounts
// @Produce      json
// @Param        id   path   int  true  "Bank Account ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "deleted"
// @Failure      400  {object}  map[string]string       "cannot delete (last account)"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/bank-accounts/{id} [delete]
func (h *AccountHandler) DeleteBankAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.bankAccountClient.DeleteBankAccount(c.Request.Context(), &accountpb.DeleteBankAccountRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListMyAccounts serves GET /api/me/accounts.
func (h *AccountHandler) ListMyAccounts(c *gin.Context) {
	userID, _ := c.Get("principal_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 32)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("page_size", "50"), 10, 32)

	resp, err := h.accountClient.ListAccountsByClient(c.Request.Context(), &accountpb.ListAccountsByClientRequest{
		ClientId: uint64(uid),
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	accounts := make([]gin.H, 0, len(resp.Accounts))
	for _, acc := range resp.Accounts {
		accounts = append(accounts, accountToJSON(acc))
	}
	c.JSON(http.StatusOK, gin.H{"accounts": accounts, "total": resp.Total})
}

// GetMyAccount serves GET /api/me/accounts/:id — fetches account and verifies ownership.
func (h *AccountHandler) GetMyAccount(c *gin.Context) {
	userID, _ := c.Get("principal_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if resp.OwnerId != uint64(uid) {
		apiError(c, 403, ErrForbidden, "access denied")
		return
	}
	c.JSON(http.StatusOK, accountToJSON(resp))
}

// GetMyAccountActivity godoc
// @Summary      List activity on one of my accounts
// @Description  Returns every balance-affecting event on the account in reverse-chronological order. Includes securities buys/sells (`reference_type=order`), tax collection (`reference_type=tax`), commission debits, transfers, payments, and interest. Ownership enforced against the JWT.
// @Tags         accounts
// @Produce      json
// @Param        id         path   int true  "Account ID"
// @Param        page       query  int false "Page number (default 1)"
// @Param        page_size  query  int false "Page size (default 20, max 200)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "entries, total_count"
// @Failure      400  {object}  map[string]interface{}  "validation_error"
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}  "account does not belong to caller"
// @Failure      404  {object}  map[string]interface{}
// @Router       /api/v2/me/accounts/{id}/activity [get]
func (h *AccountHandler) GetMyAccountActivity(c *gin.Context) {
	userID, _ := c.Get("principal_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize > 200 {
		pageSize = 200
	}

	acct, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if acct.OwnerId != uint64(uid) {
		apiError(c, 403, ErrForbidden, "access denied")
		return
	}

	resp, err := h.accountClient.GetLedgerEntries(c.Request.Context(), &accountpb.GetLedgerEntriesRequest{
		AccountNumber: acct.AccountNumber,
		Page:          int32(page),
		PageSize:      int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	out := make([]gin.H, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, ledgerEntryToJSON(e, acct.CurrencyCode))
	}
	c.JSON(http.StatusOK, gin.H{
		"entries":     out,
		"total_count": resp.TotalCount,
	})
}

// ledgerEntryToJSON trims the raw LedgerEntryResponse to fields the client
// cares about. Internal fields like idempotency keys are not exposed. The
// ledger entry's account currency isn't carried on the entry itself — we
// stamp it from the parent account so consumers can format amounts without
// a second lookup.
func ledgerEntryToJSON(e *accountpb.LedgerEntryResponse, currency string) gin.H {
	return gin.H{
		"id":             e.Id,
		"entry_type":     e.EntryType, // "debit" or "credit"
		"amount":         e.Amount,
		"currency":       currency,
		"balance_before": e.BalanceBefore,
		"balance_after":  e.BalanceAfter,
		"description":    e.Description,
		"reference_id":   e.ReferenceId,
		"reference_type": e.ReferenceType, // e.g. "order", "tax", "commission", "transfer", "payment", "interest"
		"occurred_at":    e.CreatedAt,     // unix seconds
	}
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
		"reserved_balance":  acc.ReservedBalance,
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
