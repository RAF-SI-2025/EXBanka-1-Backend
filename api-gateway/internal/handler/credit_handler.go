package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	creditpb "github.com/exbanka/contract/creditpb"
)

type CreditHandler struct {
	creditClient creditpb.CreditServiceClient
}

func NewCreditHandler(creditClient creditpb.CreditServiceClient) *CreditHandler {
	return &CreditHandler{creditClient: creditClient}
}

type createLoanRequestBody struct {
	ClientID         uint64  `json:"client_id" binding:"required"`
	LoanType         string  `json:"loan_type" binding:"required"`
	InterestType     string  `json:"interest_type" binding:"required"`
	Amount           float64 `json:"amount" binding:"required"`
	CurrencyCode     string  `json:"currency_code" binding:"required"`
	Purpose          string  `json:"purpose"`
	MonthlySalary    float64 `json:"monthly_salary"`
	EmploymentStatus string  `json:"employment_status"`
	EmploymentPeriod int32   `json:"employment_period"`
	RepaymentPeriod  int32   `json:"repayment_period" binding:"required"`
	Phone            string  `json:"phone"`
	AccountNumber    string  `json:"account_number" binding:"required"`
}

// @Summary      Submit loan application
// @Tags         loans
// @Accept       json
// @Produce      json
// @Param        body  body  createLoanRequestBody  true  "Loan application data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/loans/requests [post]
func (h *CreditHandler) CreateLoanRequest(c *gin.Context) {
	var req createLoanRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	loanType, err := oneOf("loan_type", req.LoanType, "cash", "housing", "auto", "refinancing", "student")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	interestType, err := oneOf("interest_type", req.InterestType, "fixed", "variable")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := positive("amount", req.Amount); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	allowedPeriods := map[string][]int32{
		"cash":        {12, 24, 36, 48, 60, 72, 84},
		"housing":     {60, 120, 180, 240, 300, 360},
		"auto":        {12, 24, 36, 48, 60, 72, 84},
		"refinancing": {12, 24, 36, 48, 60, 72, 84},
		"student":     {12, 24, 36, 48, 60, 72, 84},
	}
	if periods, ok := allowedPeriods[loanType]; ok {
		valid := false
		for _, p := range periods {
			if req.RepaymentPeriod == p {
				valid = true
				break
			}
		}
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("repayment_period %d is not allowed for %s loans; allowed: %v", req.RepaymentPeriod, loanType, periods)})
			return
		}
	}
	resp, err := h.creditClient.CreateLoanRequest(c.Request.Context(), &creditpb.CreateLoanRequestReq{
		ClientId:         req.ClientID,
		LoanType:         loanType,
		InterestType:     interestType,
		Amount:           fmt.Sprintf("%.4f", req.Amount),
		CurrencyCode:     req.CurrencyCode,
		Purpose:          req.Purpose,
		MonthlySalary:    fmt.Sprintf("%.4f", req.MonthlySalary),
		EmploymentStatus: req.EmploymentStatus,
		EmploymentPeriod: req.EmploymentPeriod,
		RepaymentPeriod:  req.RepaymentPeriod,
		Phone:            req.Phone,
		AccountNumber:    req.AccountNumber,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, loanRequestToJSON(resp))
}

// @Summary      Get loan request by ID
// @Tags         loans
// @Produce      json
// @Param        id   path  int  true  "Loan request ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/loans/requests/{id} [get]
func (h *CreditHandler) GetLoanRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.GetLoanRequest(c.Request.Context(), &creditpb.GetLoanRequestReq{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "loan request not found"})
		return
	}
	c.JSON(http.StatusOK, loanRequestToJSON(resp))
}

// @Summary      List loan requests
// @Tags         loans
// @Produce      json
// @Param        page                  query  int     false  "Page number (default 1)"
// @Param        page_size             query  int     false  "Items per page (default 20)"
// @Param        loan_type_filter      query  string  false  "Filter by loan type"
// @Param        account_number_filter query  string  false  "Filter by account number"
// @Param        status_filter         query  string  false  "Filter by status"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/requests [get]
func (h *CreditHandler) ListLoanRequests(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.creditClient.ListLoanRequests(c.Request.Context(), &creditpb.ListLoanRequestsReq{
		LoanTypeFilter:      c.Query("loan_type_filter"),
		AccountNumberFilter: c.Query("account_number_filter"),
		StatusFilter:        c.Query("status_filter"),
		Page:                int32(page),
		PageSize:            int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	requests := make([]gin.H, 0, len(resp.Requests))
	for _, r := range resp.Requests {
		requests = append(requests, loanRequestToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{
		"requests": requests,
		"total":    resp.Total,
	})
}

// @Summary      Approve loan request
// @Tags         loans
// @Produce      json
// @Param        id   path  int  true  "Loan request ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/requests/{id}/approve [put]
func (h *CreditHandler) ApproveLoanRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// Get employee ID from JWT context for limit enforcement
	uid, _ := c.Get("user_id")
	employeeID := uid.(int64)

	resp, err := h.creditClient.ApproveLoanRequest(c.Request.Context(), &creditpb.ApproveLoanRequestReq{
		RequestId:  id,
		EmployeeId: uint64(employeeID),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, loanToJSON(resp))
}

// @Summary      Reject loan request
// @Tags         loans
// @Produce      json
// @Param        id   path  int  true  "Loan request ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/requests/{id}/reject [put]
func (h *CreditHandler) RejectLoanRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.RejectLoanRequest(c.Request.Context(), &creditpb.RejectLoanRequestReq{RequestId: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, loanRequestToJSON(resp))
}

// @Summary      Get loan by ID
// @Tags         loans
// @Produce      json
// @Param        id   path  int  true  "Loan ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/loans/{id} [get]
func (h *CreditHandler) GetLoan(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.GetLoan(c.Request.Context(), &creditpb.GetLoanReq{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "loan not found"})
		return
	}
	c.JSON(http.StatusOK, loanToJSON(resp))
}

// @Summary      List loans by client
// @Tags         loans
// @Produce      json
// @Param        client_id  path   int  true   "Client ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Items per page (default 20)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/client/{client_id} [get]
func (h *CreditHandler) ListLoansByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	if !enforceClientSelf(c, clientID) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.creditClient.ListLoansByClient(c.Request.Context(), &creditpb.ListLoansByClientReq{
		ClientId: clientID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	loans := make([]gin.H, 0, len(resp.Loans))
	for _, l := range resp.Loans {
		loans = append(loans, loanToJSON(l))
	}
	c.JSON(http.StatusOK, gin.H{
		"loans": loans,
		"total": resp.Total,
	})
}

// @Summary      List all loans
// @Tags         loans
// @Produce      json
// @Param        page                  query  int     false  "Page number (default 1)"
// @Param        page_size             query  int     false  "Items per page (default 20)"
// @Param        loan_type_filter      query  string  false  "Filter by loan type"
// @Param        account_number_filter query  string  false  "Filter by account number"
// @Param        status_filter         query  string  false  "Filter by status"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans [get]
func (h *CreditHandler) ListAllLoans(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.creditClient.ListAllLoans(c.Request.Context(), &creditpb.ListAllLoansReq{
		LoanTypeFilter:      c.Query("loan_type_filter"),
		AccountNumberFilter: c.Query("account_number_filter"),
		StatusFilter:        c.Query("status_filter"),
		Page:                int32(page),
		PageSize:            int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	loans := make([]gin.H, 0, len(resp.Loans))
	for _, l := range resp.Loans {
		loans = append(loans, loanToJSON(l))
	}
	c.JSON(http.StatusOK, gin.H{
		"loans": loans,
		"total": resp.Total,
	})
}

// @Summary      Get installments by loan
// @Tags         loans
// @Produce      json
// @Param        id   path  int  true  "Loan ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/{id}/installments [get]
func (h *CreditHandler) GetInstallmentsByLoan(c *gin.Context) {
	loanID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.GetInstallmentsByLoan(c.Request.Context(), &creditpb.GetInstallmentsByLoanReq{LoanId: loanID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	installments := make([]gin.H, 0, len(resp.Installments))
	for _, inst := range resp.Installments {
		installments = append(installments, gin.H{
			"id":            inst.Id,
			"loan_id":       inst.LoanId,
			"amount":        inst.Amount,
			"interest_rate": inst.InterestRate,
			"currency_code": inst.CurrencyCode,
			"expected_date": inst.ExpectedDate,
			"actual_date":   inst.ActualDate,
			"status":        inst.Status,
		})
	}
	c.JSON(http.StatusOK, gin.H{"installments": installments})
}

// @Summary      List loan requests by client
// @Tags         loans
// @Produce      json
// @Param        client_id  path   int  true   "Client ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Items per page (default 20)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/loans/requests/client/{client_id} [get]
func (h *CreditHandler) ListLoanRequestsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	if !enforceClientSelf(c, clientID) {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.creditClient.ListLoanRequests(c.Request.Context(), &creditpb.ListLoanRequestsReq{
		ClientIdFilter: clientID,
		Page:           int32(page),
		PageSize:       int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	requests := make([]gin.H, 0, len(resp.Requests))
	for _, r := range resp.Requests {
		requests = append(requests, loanRequestToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{
		"requests": requests,
		"total":    resp.Total,
	})
}

func loanRequestToJSON(r *creditpb.LoanRequestResponse) gin.H {
	return gin.H{
		"id":                r.Id,
		"client_id":         r.ClientId,
		"loan_type":         r.LoanType,
		"interest_type":     r.InterestType,
		"amount":            r.Amount,
		"currency_code":     r.CurrencyCode,
		"purpose":           r.Purpose,
		"monthly_salary":    r.MonthlySalary,
		"employment_status": r.EmploymentStatus,
		"employment_period": r.EmploymentPeriod,
		"repayment_period":  r.RepaymentPeriod,
		"phone":             r.Phone,
		"account_number":    r.AccountNumber,
		"status":            r.Status,
		"created_at":        r.CreatedAt,
	}
}

// --- Interest Rate Tier endpoints ---

// @Summary      List interest rate tiers
// @Tags         rate-config
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/interest-rate-tiers [get]
func (h *CreditHandler) ListInterestRateTiers(c *gin.Context) {
	resp, err := h.creditClient.ListInterestRateTiers(c.Request.Context(), &creditpb.ListInterestRateTiersRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	tiers := make([]gin.H, 0, len(resp.Tiers))
	for _, t := range resp.Tiers {
		tiers = append(tiers, interestRateTierToJSON(t))
	}
	c.JSON(http.StatusOK, gin.H{"tiers": tiers})
}

type createInterestRateTierBody struct {
	AmountFrom   float64 `json:"amount_from"`
	AmountTo     float64 `json:"amount_to"`
	FixedRate    float64 `json:"fixed_rate" binding:"required"`
	VariableBase float64 `json:"variable_base" binding:"required"`
}

// @Summary      Create interest rate tier
// @Tags         rate-config
// @Accept       json
// @Produce      json
// @Param        body  body  createInterestRateTierBody  true  "Tier data"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/interest-rate-tiers [post]
func (h *CreditHandler) CreateInterestRateTier(c *gin.Context) {
	var req createInterestRateTierBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("amount_from", req.AmountFrom); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("amount_to", req.AmountTo); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("fixed_rate", req.FixedRate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("variable_base", req.VariableBase); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.creditClient.CreateInterestRateTier(c.Request.Context(), &creditpb.CreateInterestRateTierRequest{
		AmountFrom:   fmt.Sprintf("%.4f", req.AmountFrom),
		AmountTo:     fmt.Sprintf("%.4f", req.AmountTo),
		FixedRate:    fmt.Sprintf("%.4f", req.FixedRate),
		VariableBase: fmt.Sprintf("%.4f", req.VariableBase),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, interestRateTierToJSON(resp))
}

type updateInterestRateTierBody struct {
	AmountFrom   float64 `json:"amount_from"`
	AmountTo     float64 `json:"amount_to"`
	FixedRate    float64 `json:"fixed_rate" binding:"required"`
	VariableBase float64 `json:"variable_base" binding:"required"`
}

// @Summary      Update interest rate tier
// @Tags         rate-config
// @Accept       json
// @Produce      json
// @Param        id    path  int                         true  "Tier ID"
// @Param        body  body  updateInterestRateTierBody  true  "Tier data"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/interest-rate-tiers/{id} [put]
func (h *CreditHandler) UpdateInterestRateTier(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateInterestRateTierBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("amount_from", req.AmountFrom); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("amount_to", req.AmountTo); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("fixed_rate", req.FixedRate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("variable_base", req.VariableBase); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.creditClient.UpdateInterestRateTier(c.Request.Context(), &creditpb.UpdateInterestRateTierRequest{
		Id:           id,
		AmountFrom:   fmt.Sprintf("%.4f", req.AmountFrom),
		AmountTo:     fmt.Sprintf("%.4f", req.AmountTo),
		FixedRate:    fmt.Sprintf("%.4f", req.FixedRate),
		VariableBase: fmt.Sprintf("%.4f", req.VariableBase),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, interestRateTierToJSON(resp))
}

// @Summary      Delete interest rate tier
// @Tags         rate-config
// @Produce      json
// @Param        id   path  int  true  "Tier ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/interest-rate-tiers/{id} [delete]
func (h *CreditHandler) DeleteInterestRateTier(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	_, err = h.creditClient.DeleteInterestRateTier(c.Request.Context(), &creditpb.DeleteInterestRateTierRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Bank Margin endpoints ---

// @Summary      List bank margins
// @Tags         rate-config
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/bank-margins [get]
func (h *CreditHandler) ListBankMargins(c *gin.Context) {
	resp, err := h.creditClient.ListBankMargins(c.Request.Context(), &creditpb.ListBankMarginsRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	margins := make([]gin.H, 0, len(resp.Margins))
	for _, m := range resp.Margins {
		margins = append(margins, bankMarginToJSON(m))
	}
	c.JSON(http.StatusOK, gin.H{"margins": margins})
}

type updateBankMarginBody struct {
	Margin float64 `json:"margin" binding:"required"`
}

// @Summary      Update bank margin
// @Tags         rate-config
// @Accept       json
// @Produce      json
// @Param        id    path  int                   true  "Margin ID"
// @Param        body  body  updateBankMarginBody  true  "Margin data"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/bank-margins/{id} [put]
func (h *CreditHandler) UpdateBankMargin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateBankMarginBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := nonNegative("margin", req.Margin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.creditClient.UpdateBankMargin(c.Request.Context(), &creditpb.UpdateBankMarginRequest{
		Id:     id,
		Margin: fmt.Sprintf("%.4f", req.Margin),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, bankMarginToJSON(resp))
}

// @Summary      Apply variable rate update to active loans
// @Tags         rate-config
// @Produce      json
// @Param        id   path  int  true  "Interest Rate Tier ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/interest-rate-tiers/{id}/apply [post]
func (h *CreditHandler) ApplyVariableRateUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.ApplyVariableRateUpdate(c.Request.Context(), &creditpb.ApplyVariableRateUpdateRequest{TierId: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"affected_loans": resp.AffectedLoans})
}

func interestRateTierToJSON(t *creditpb.InterestRateTierResponse) gin.H {
	return gin.H{
		"id":            t.Id,
		"amount_from":   t.AmountFrom,
		"amount_to":     t.AmountTo,
		"fixed_rate":    t.FixedRate,
		"variable_base": t.VariableBase,
		"active":        t.Active,
		"created_at":    t.CreatedAt,
		"updated_at":    t.UpdatedAt,
	}
}

func bankMarginToJSON(m *creditpb.BankMarginResponse) gin.H {
	return gin.H{
		"id":         m.Id,
		"loan_type":  m.LoanType,
		"margin":     m.Margin,
		"active":     m.Active,
		"created_at": m.CreatedAt,
		"updated_at": m.UpdatedAt,
	}
}

func loanToJSON(l *creditpb.LoanResponse) gin.H {
	return gin.H{
		"id":                      l.Id,
		"loan_number":             l.LoanNumber,
		"loan_type":               l.LoanType,
		"account_number":          l.AccountNumber,
		"amount":                  l.Amount,
		"repayment_period":        l.RepaymentPeriod,
		"nominal_interest_rate":   l.NominalInterestRate,
		"effective_interest_rate": l.EffectiveInterestRate,
		"contract_date":           l.ContractDate,
		"maturity_date":           l.MaturityDate,
		"next_installment_amount": l.NextInstallmentAmount,
		"next_installment_date":   l.NextInstallmentDate,
		"remaining_debt":          l.RemainingDebt,
		"currency_code":           l.CurrencyCode,
		"status":                  l.Status,
		"interest_type":           l.InterestType,
		"created_at":              l.CreatedAt,
	}
}
