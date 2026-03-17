package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/creditpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/service"
)

type CreditGRPCHandler struct {
	pb.UnimplementedCreditServiceServer
	loanRequestService *service.LoanRequestService
	loanService        *service.LoanService
	installmentService *service.InstallmentService
	producer           *kafkaprod.Producer
}

func NewCreditGRPCHandler(
	loanRequestService *service.LoanRequestService,
	loanService *service.LoanService,
	installmentService *service.InstallmentService,
	producer *kafkaprod.Producer,
) *CreditGRPCHandler {
	return &CreditGRPCHandler{
		loanRequestService: loanRequestService,
		loanService:        loanService,
		installmentService: installmentService,
		producer:           producer,
	}
}

func (h *CreditGRPCHandler) CreateLoanRequest(ctx context.Context, req *pb.CreateLoanRequestReq) (*pb.LoanRequestResponse, error) {
	loanReq := &model.LoanRequest{
		ClientID:         req.ClientId,
		LoanType:         req.LoanType,
		InterestType:     req.InterestType,
		Amount:           req.Amount,
		CurrencyCode:     req.CurrencyCode,
		Purpose:          req.Purpose,
		MonthlySalary:    req.MonthlySalary,
		EmploymentStatus: req.EmploymentStatus,
		EmploymentPeriod: int(req.EmploymentPeriod),
		RepaymentPeriod:  int(req.RepaymentPeriod),
		Phone:            req.Phone,
		AccountNumber:    req.AccountNumber,
	}

	if err := h.loanRequestService.CreateLoanRequest(loanReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to create loan request: %v", err)
	}

	_ = h.producer.PublishLoanRequested(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: loanReq.ID,
		LoanType:      loanReq.LoanType,
		Amount:        loanReq.Amount,
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
		return nil, status.Errorf(codes.Internal, "failed to get loan request: %v", err)
	}
	return toLoanRequestResponse(loanReq), nil
}

func (h *CreditGRPCHandler) ListLoanRequests(ctx context.Context, req *pb.ListLoanRequestsReq) (*pb.ListLoanRequestsResponse, error) {
	requests, total, err := h.loanRequestService.ListLoanRequests(
		req.LoanTypeFilter, req.AccountNumberFilter, req.StatusFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list loan requests: %v", err)
	}

	resp := &pb.ListLoanRequestsResponse{Total: total}
	for _, r := range requests {
		r := r
		resp.Requests = append(resp.Requests, toLoanRequestResponse(&r))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) ApproveLoanRequest(ctx context.Context, req *pb.ApproveLoanRequestReq) (*pb.LoanResponse, error) {
	loan, err := h.loanRequestService.ApproveLoanRequest(req.RequestId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan request not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to approve loan request: %v", err)
	}

	_ = h.producer.PublishLoanApproved(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: req.RequestId,
		LoanType:      loan.LoanType,
		Amount:        loan.Amount,
		Status:        loan.Status,
	})

	return toLoanResponse(loan), nil
}

func (h *CreditGRPCHandler) RejectLoanRequest(ctx context.Context, req *pb.RejectLoanRequestReq) (*pb.LoanRequestResponse, error) {
	loanReq, err := h.loanRequestService.RejectLoanRequest(req.RequestId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan request not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to reject loan request: %v", err)
	}

	_ = h.producer.PublishLoanRejected(ctx, kafkamsg.LoanStatusMessage{
		LoanRequestID: req.RequestId,
		LoanType:      loanReq.LoanType,
		Amount:        loanReq.Amount,
		Status:        loanReq.Status,
	})

	return toLoanRequestResponse(loanReq), nil
}

func (h *CreditGRPCHandler) GetLoan(ctx context.Context, req *pb.GetLoanReq) (*pb.LoanResponse, error) {
	loan, err := h.loanService.GetLoan(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "loan not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get loan: %v", err)
	}
	return toLoanResponse(loan), nil
}

func (h *CreditGRPCHandler) ListLoansByClient(ctx context.Context, req *pb.ListLoansByClientReq) (*pb.ListLoansResponse, error) {
	loans, total, err := h.loanService.ListLoansByClient(req.ClientId, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list loans: %v", err)
	}

	resp := &pb.ListLoansResponse{Total: total}
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
		return nil, status.Errorf(codes.Internal, "failed to list all loans: %v", err)
	}

	resp := &pb.ListLoansResponse{Total: total}
	for _, l := range loans {
		l := l
		resp.Loans = append(resp.Loans, toLoanResponse(&l))
	}
	return resp, nil
}

func (h *CreditGRPCHandler) GetInstallmentsByLoan(ctx context.Context, req *pb.GetInstallmentsByLoanReq) (*pb.ListInstallmentsResponse, error) {
	installments, err := h.installmentService.GetInstallmentsByLoan(req.LoanId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get installments: %v", err)
	}

	resp := &pb.ListInstallmentsResponse{}
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
		Amount:           r.Amount,
		CurrencyCode:     r.CurrencyCode,
		Purpose:          r.Purpose,
		MonthlySalary:    r.MonthlySalary,
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
		Amount:                l.Amount,
		RepaymentPeriod:       int32(l.RepaymentPeriod),
		NominalInterestRate:   l.NominalInterestRate,
		EffectiveInterestRate: l.EffectiveInterestRate,
		ContractDate:          l.ContractDate.Format("2006-01-02T15:04:05Z"),
		MaturityDate:          l.MaturityDate.Format("2006-01-02T15:04:05Z"),
		NextInstallmentAmount: l.NextInstallmentAmount,
		NextInstallmentDate:   l.NextInstallmentDate.Format("2006-01-02T15:04:05Z"),
		RemainingDebt:         l.RemainingDebt,
		CurrencyCode:          l.CurrencyCode,
		Status:                l.Status,
		InterestType:          l.InterestType,
		CreatedAt:             l.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func toInstallmentResponse(inst *model.Installment) *pb.InstallmentResponse {
	resp := &pb.InstallmentResponse{
		Id:           inst.ID,
		LoanId:       inst.LoanID,
		Amount:       inst.Amount,
		InterestRate: inst.InterestRate,
		CurrencyCode: inst.CurrencyCode,
		ExpectedDate: inst.ExpectedDate.Format("2006-01-02T15:04:05Z"),
		Status:       inst.Status,
	}
	if inst.ActualDate != nil {
		resp.ActualDate = inst.ActualDate.Format("2006-01-02T15:04:05Z")
	}
	return resp
}
