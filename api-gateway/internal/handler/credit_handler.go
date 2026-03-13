package handler

import (
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

func (h *CreditHandler) CreateLoanRequest(c *gin.Context) {
	var req createLoanRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.creditClient.CreateLoanRequest(c.Request.Context(), &creditpb.CreateLoanRequestReq{
		ClientId:         req.ClientID,
		LoanType:         req.LoanType,
		InterestType:     req.InterestType,
		Amount:           req.Amount,
		CurrencyCode:     req.CurrencyCode,
		Purpose:          req.Purpose,
		MonthlySalary:    req.MonthlySalary,
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

func (h *CreditHandler) ApproveLoanRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.creditClient.ApproveLoanRequest(c.Request.Context(), &creditpb.ApproveLoanRequestReq{RequestId: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, loanToJSON(resp))
}

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

func (h *CreditHandler) ListLoansByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
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
