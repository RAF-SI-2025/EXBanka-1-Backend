package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/creditpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/exbanka/credit-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "already "), strings.Contains(msg, "exceeds"),
		strings.Contains(msg, "insufficient funds"), strings.Contains(msg, "limit exceeded"),
		strings.Contains(msg, "spending limit"), strings.Contains(msg, "only pending"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

type CreditGRPCHandler struct {
	pb.UnimplementedCreditServiceServer
	loanRequestService *service.LoanRequestService
	loanService        *service.LoanService
	installmentService *service.InstallmentService
	rateConfigService  *service.RateConfigService
	loanRepo           *repository.LoanRepository
	installRepo        *repository.InstallmentRepository
	producer           *kafkaprod.Producer
}

func NewCreditGRPCHandler(
	loanRequestService *service.LoanRequestService,
	loanService *service.LoanService,
	installmentService *service.InstallmentService,
	rateConfigService *service.RateConfigService,
	loanRepo *repository.LoanRepository,
	installRepo *repository.InstallmentRepository,
	producer *kafkaprod.Producer,
) *CreditGRPCHandler {
	return &CreditGRPCHandler{
		loanRequestService: loanRequestService,
		loanService:        loanService,
		installmentService: installmentService,
		rateConfigService:  rateConfigService,
		loanRepo:           loanRepo,
		installRepo:        installRepo,
		producer:           producer,
	}
}

func (h *CreditGRPCHandler) CreateLoanRequest(ctx context.Context, req *pb.CreateLoanRequestReq) (*pb.LoanRequestResponse, error) {
	amount, _ := decimal.NewFromString(req.Amount)
	monthlySalary, _ := decimal.NewFromString(req.MonthlySalary)
	loanReq := &model.LoanRequest{
		ClientID:         req.ClientId,
		LoanType:         req.LoanType,
		InterestType:     req.InterestType,
		Amount:           amount,
		CurrencyCode:     req.CurrencyCode,
		Purpose:          req.Purpose,
		MonthlySalary:    monthlySalary,
		EmploymentStatus: req.EmploymentStatus,
		EmploymentPeriod: int(req.EmploymentPeriod),
		RepaymentPeriod:  int(req.RepaymentPeriod),
		Phone:            req.Phone,
		AccountNumber:    req.AccountNumber,
	}

	if err := h.loanRequestService.CreateLoanRequest(loanReq); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create loan request: %v", err)
	}

	_ = h.producer.PublishLoanRequested(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: loanReq.ID,
		LoanType:      loanReq.LoanType,
		Amount:        loanReq.Amount.StringFixed(4),
		Status:        loanReq.Status,
	})

	return toLoanRequestResponse(loanReq), nil
}

func (h *CreditGRPCHandler) GetLoanRequest(ctx context.Context, req *pb.GetLoanRequestReq) (*pb.LoanRequestResponse, error) {
	loanReq, err := h.loanRequestService.GetLoanRequest(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan request not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get loan request: %v", err)
	}
	return toLoanRequestResponse(loanReq), nil
}

func (h *CreditGRPCHandler) ListLoanRequests(ctx context.Context, req *pb.ListLoanRequestsReq) (*pb.ListLoanRequestsResponse, error) {
	requests, total, err := h.loanRequestService.ListLoanRequests(
		req.LoanTypeFilter, req.AccountNumberFilter, req.StatusFilter,
		req.ClientIdFilter, int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list loan requests: %v", err)
	}

	resp := &pb.ListLoanRequestsResponse{Total: total, Requests: make([]*pb.LoanRequestResponse, 0, len(requests))}
	for _, r := range requests {
		r := r
		resp.Requests = append(resp.Requests, toLoanRequestResponse(&r))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) ApproveLoanRequest(ctx context.Context, req *pb.ApproveLoanRequestReq) (*pb.LoanResponse, error) {
	loan, err := h.loanRequestService.ApproveLoanRequest(ctx, req.RequestId, req.EmployeeId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan request not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to approve loan request: %v", err)
	}

	_ = h.producer.PublishLoanApproved(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: req.RequestId,
		LoanType:      loan.LoanType,
		Amount:        loan.Amount.StringFixed(4),
		Status:        loan.Status,
	})

	// Only publish LoanDisbursed when the saga actually disbursed the money.
	// Saga success → loan.Status == "active". Nil-client fallback or partial
	// failure leaves the loan in "approved" or "disbursement_failed".
	if loan.Status == "active" {
		_ = h.producer.PublishLoanDisbursed(ctx, kafkamsg.LoanDisbursedMessage{
			LoanID:        loan.ID,
			LoanNumber:    loan.LoanNumber,
			BorrowerID:    loan.ClientID,
			AccountNumber: loan.AccountNumber,
			Amount:        loan.Amount.StringFixed(4),
			CurrencyCode:  loan.CurrencyCode,
			DisbursedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}

	if loanReq, lrErr := h.loanRequestService.GetLoanRequest(req.RequestId); lrErr == nil {
		_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
			UserID:  loanReq.ClientID,
			Type:    "loan_approved",
			Title:   "Loan Approved",
			Message: fmt.Sprintf("Your %s loan request for %s has been approved.", loan.LoanType, loan.Amount.StringFixed(2)),
			RefType: "loan",
			RefID:   loan.ID,
		})
	}

	return toLoanResponse(loan), nil
}

func (h *CreditGRPCHandler) RejectLoanRequest(ctx context.Context, req *pb.RejectLoanRequestReq) (*pb.LoanRequestResponse, error) {
	changedBy := changelog.ExtractChangedBy(ctx)
	loanReq, err := h.loanRequestService.RejectLoanRequest(req.RequestId, changedBy, "")
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan request not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to reject loan request: %v", err)
	}

	_ = h.producer.PublishLoanRejected(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: req.RequestId,
		LoanType:      loanReq.LoanType,
		Amount:        loanReq.Amount.StringFixed(4),
		Status:        loanReq.Status,
	})

	_ = h.producer.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{
		UserID:  loanReq.ClientID,
		Type:    "loan_rejected",
		Title:   "Loan Request Rejected",
		Message: fmt.Sprintf("Your %s loan request for %s has been rejected.", loanReq.LoanType, loanReq.Amount.StringFixed(2)),
		RefType: "loan_request",
		RefID:   loanReq.ID,
	})

	return toLoanRequestResponse(loanReq), nil
}

func (h *CreditGRPCHandler) GetLoan(ctx context.Context, req *pb.GetLoanReq) (*pb.LoanResponse, error) {
	loan, err := h.loanService.GetLoan(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get loan: %v", err)
	}
	return toLoanResponse(loan), nil
}

func (h *CreditGRPCHandler) ListLoansByClient(ctx context.Context, req *pb.ListLoansByClientReq) (*pb.ListLoansResponse, error) {
	loans, total, err := h.loanService.ListLoansByClient(req.ClientId, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list loans: %v", err)
	}

	resp := &pb.ListLoansResponse{Total: total, Loans: make([]*pb.LoanResponse, 0, len(loans))}
	for _, l := range loans {
		l := l
		resp.Loans = append(resp.Loans, toLoanResponse(&l))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) ListAllLoans(ctx context.Context, req *pb.ListAllLoansReq) (*pb.ListLoansResponse, error) {
	loans, total, err := h.loanService.ListAllLoans(
		req.LoanTypeFilter, req.AccountNumberFilter, req.StatusFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list all loans: %v", err)
	}

	resp := &pb.ListLoansResponse{Total: total, Loans: make([]*pb.LoanResponse, 0, len(loans))}
	for _, l := range loans {
		l := l
		resp.Loans = append(resp.Loans, toLoanResponse(&l))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) GetInstallmentsByLoan(ctx context.Context, req *pb.GetInstallmentsByLoanReq) (*pb.ListInstallmentsResponse, error) {
	installments, err := h.installmentService.GetInstallmentsByLoan(req.LoanId)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to get installments: %v", err)
	}

	resp := &pb.ListInstallmentsResponse{Installments: make([]*pb.InstallmentResponse, 0, len(installments))}
	for _, inst := range installments {
		inst := inst
		resp.Installments = append(resp.Installments, toInstallmentResponse(&inst))
	}
	return resp, nil
}

func toLoanRequestResponse(r *model.LoanRequest) *pb.LoanRequestResponse {
	return &pb.LoanRequestResponse{
		Id:               r.ID,
		ClientId:         r.ClientID,
		LoanType:         r.LoanType,
		InterestType:     r.InterestType,
		Amount:           r.Amount.StringFixed(4),
		CurrencyCode:     r.CurrencyCode,
		Purpose:          r.Purpose,
		MonthlySalary:    r.MonthlySalary.StringFixed(4),
		EmploymentStatus: r.EmploymentStatus,
		EmploymentPeriod: int32(r.EmploymentPeriod),
		RepaymentPeriod:  int32(r.RepaymentPeriod),
		Phone:            r.Phone,
		AccountNumber:    r.AccountNumber,
		Status:           r.Status,
		CreatedAt:        r.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toLoanResponse(l *model.Loan) *pb.LoanResponse {
	return &pb.LoanResponse{
		Id:                    l.ID,
		LoanNumber:            l.LoanNumber,
		LoanType:              l.LoanType,
		AccountNumber:         l.AccountNumber,
		Amount:                l.Amount.StringFixed(4),
		RepaymentPeriod:       int32(l.RepaymentPeriod),
		NominalInterestRate:   l.NominalInterestRate.StringFixed(4),
		EffectiveInterestRate: l.EffectiveInterestRate.StringFixed(4),
		ContractDate:          l.ContractDate.Format("2006-01-02T15:04:05Z"),
		MaturityDate:          l.MaturityDate.Format("2006-01-02T15:04:05Z"),
		NextInstallmentAmount: l.NextInstallmentAmount.StringFixed(4),
		NextInstallmentDate:   l.NextInstallmentDate.Format("2006-01-02T15:04:05Z"),
		RemainingDebt:         l.RemainingDebt.StringFixed(4),
		CurrencyCode:          l.CurrencyCode,
		Status:                l.Status,
		InterestType:          l.InterestType,
		CreatedAt:             l.CreatedAt.Format("2006-01-02T15:04:05Z"),
		ClientId:              l.ClientID,
	}
}

func toInstallmentResponse(inst *model.Installment) *pb.InstallmentResponse {
	resp := &pb.InstallmentResponse{
		Id:           inst.ID,
		LoanId:       inst.LoanID,
		Amount:       inst.Amount.StringFixed(4),
		InterestRate: inst.InterestRate.StringFixed(4),
		CurrencyCode: inst.CurrencyCode,
		ExpectedDate: inst.ExpectedDate.Format("2006-01-02T15:04:05Z"),
		Status:       inst.Status,
	}
	if inst.ActualDate != nil {
		resp.ActualDate = inst.ActualDate.Format("2006-01-02T15:04:05Z")
	}
	return resp
}

// --- Interest Rate Tier RPCs ---

func (h *CreditGRPCHandler) ListInterestRateTiers(ctx context.Context, req *pb.ListInterestRateTiersRequest) (*pb.ListInterestRateTiersResponse, error) {
	tiers, err := h.rateConfigService.ListTiers()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list interest rate tiers: %v", err)
	}

	resp := &pb.ListInterestRateTiersResponse{Tiers: make([]*pb.InterestRateTierResponse, 0, len(tiers))}
	for _, t := range tiers {
		resp.Tiers = append(resp.Tiers, toInterestRateTierResponse(&t))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) CreateInterestRateTier(ctx context.Context, req *pb.CreateInterestRateTierRequest) (*pb.InterestRateTierResponse, error) {
	amountFrom, _ := decimal.NewFromString(req.AmountFrom)
	amountTo, _ := decimal.NewFromString(req.AmountTo)
	fixedRate, _ := decimal.NewFromString(req.FixedRate)
	variableBase, _ := decimal.NewFromString(req.VariableBase)

	tier := &model.InterestRateTier{
		AmountFrom:   amountFrom,
		AmountTo:     amountTo,
		FixedRate:    fixedRate,
		VariableBase: variableBase,
	}

	if err := h.rateConfigService.CreateTier(tier); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create interest rate tier: %v", err)
	}

	return toInterestRateTierResponse(tier), nil
}

func (h *CreditGRPCHandler) UpdateInterestRateTier(ctx context.Context, req *pb.UpdateInterestRateTierRequest) (*pb.InterestRateTierResponse, error) {
	amountFrom, _ := decimal.NewFromString(req.AmountFrom)
	amountTo, _ := decimal.NewFromString(req.AmountTo)
	fixedRate, _ := decimal.NewFromString(req.FixedRate)
	variableBase, _ := decimal.NewFromString(req.VariableBase)

	tier := &model.InterestRateTier{
		ID:           req.Id,
		AmountFrom:   amountFrom,
		AmountTo:     amountTo,
		FixedRate:    fixedRate,
		VariableBase: variableBase,
	}

	if err := h.rateConfigService.UpdateTier(tier); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update interest rate tier: %v", err)
	}

	return toInterestRateTierResponse(tier), nil
}

func (h *CreditGRPCHandler) DeleteInterestRateTier(ctx context.Context, req *pb.DeleteInterestRateTierRequest) (*pb.DeleteResponse, error) {
	if err := h.rateConfigService.DeleteTier(req.Id); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to delete interest rate tier: %v", err)
	}
	return &pb.DeleteResponse{Success: true}, nil
}

// --- Bank Margin RPCs ---

func (h *CreditGRPCHandler) ListBankMargins(ctx context.Context, req *pb.ListBankMarginsRequest) (*pb.ListBankMarginsResponse, error) {
	margins, err := h.rateConfigService.ListMargins()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list bank margins: %v", err)
	}

	resp := &pb.ListBankMarginsResponse{Margins: make([]*pb.BankMarginResponse, 0, len(margins))}
	for _, m := range margins {
		resp.Margins = append(resp.Margins, toBankMarginResponse(&m))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) UpdateBankMargin(ctx context.Context, req *pb.UpdateBankMarginRequest) (*pb.BankMarginResponse, error) {
	margin, _ := decimal.NewFromString(req.Margin)

	bm := &model.BankMargin{
		ID:     req.Id,
		Margin: margin,
	}

	if err := h.rateConfigService.UpdateMargin(bm); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update bank margin: %v", err)
	}

	return toBankMarginResponse(bm), nil
}

// --- Variable Rate Propagation RPC ---

func (h *CreditGRPCHandler) ApplyVariableRateUpdate(ctx context.Context, req *pb.ApplyVariableRateUpdateRequest) (*pb.ApplyVariableRateUpdateResponse, error) {
	affected, err := h.rateConfigService.ApplyVariableRateUpdate(req.TierId, h.loanRepo, h.installRepo)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to apply variable rate update: %v", err)
	}
	return &pb.ApplyVariableRateUpdateResponse{AffectedLoans: int32(affected)}, nil
}

func toInterestRateTierResponse(t *model.InterestRateTier) *pb.InterestRateTierResponse {
	return &pb.InterestRateTierResponse{
		Id:           t.ID,
		AmountFrom:   t.AmountFrom.StringFixed(4),
		AmountTo:     t.AmountTo.StringFixed(4),
		FixedRate:    t.FixedRate.StringFixed(4),
		VariableBase: t.VariableBase.StringFixed(4),
		Active:       t.Active,
		CreatedAt:    t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toBankMarginResponse(m *model.BankMargin) *pb.BankMarginResponse {
	return &pb.BankMarginResponse{
		Id:        m.ID,
		LoanType:  m.LoanType,
		Margin:    m.Margin.StringFixed(4),
		Active:    m.Active,
		CreatedAt: m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: m.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
