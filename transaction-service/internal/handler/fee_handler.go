package handler

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/service"
)

type FeeGRPCHandler struct {
	pb.UnimplementedFeeServiceServer
	feeSvc *service.FeeService
}

func NewFeeGRPCHandler(feeSvc *service.FeeService) *FeeGRPCHandler {
	return &FeeGRPCHandler{feeSvc: feeSvc}
}

func (h *FeeGRPCHandler) ListFees(ctx context.Context, req *pb.ListFeesRequest) (*pb.ListFeesResponse, error) {
	fees, err := h.feeSvc.ListFees()
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list fees: %v", err)
	}
	resp := &pb.ListFeesResponse{}
	for _, f := range fees {
		f := f
		resp.Fees = append(resp.Fees, toFeeResponse(&f))
	}
	return resp, nil
}

func (h *FeeGRPCHandler) CreateFee(ctx context.Context, req *pb.CreateFeeRequest) (*pb.TransferFeeResponse, error) {
	feeValue, err := decimal.NewFromString(req.FeeValue)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid fee_value: %v", err)
	}
	minAmount, _ := decimal.NewFromString(req.MinAmount)
	maxFee, _ := decimal.NewFromString(req.MaxFee)

	fee := &model.TransferFee{
		Name:            req.Name,
		FeeType:         req.FeeType,
		FeeValue:        feeValue,
		MinAmount:       minAmount,
		MaxFee:          maxFee,
		TransactionType: req.TransactionType,
		CurrencyCode:    req.CurrencyCode,
		Active:          true,
	}
	if err := h.feeSvc.CreateFee(fee); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create fee: %v", err)
	}
	return toFeeResponse(fee), nil
}

func (h *FeeGRPCHandler) UpdateFee(ctx context.Context, req *pb.UpdateFeeRequest) (*pb.TransferFeeResponse, error) {
	fee, err := h.feeSvc.GetFee(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "fee not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get fee: %v", err)
	}
	if req.Name != "" {
		fee.Name = req.Name
	}
	if req.FeeType != "" {
		fee.FeeType = req.FeeType
	}
	if req.FeeValue != "" {
		v, e := decimal.NewFromString(req.FeeValue)
		if e != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid fee_value")
		}
		fee.FeeValue = v
	}
	if req.MinAmount != "" {
		v, _ := decimal.NewFromString(req.MinAmount)
		fee.MinAmount = v
	}
	if req.MaxFee != "" {
		v, _ := decimal.NewFromString(req.MaxFee)
		fee.MaxFee = v
	}
	if req.TransactionType != "" {
		fee.TransactionType = req.TransactionType
	}
	if req.CurrencyCode != "" {
		fee.CurrencyCode = req.CurrencyCode
	}
	fee.Active = req.Active
	if err := h.feeSvc.UpdateFee(fee); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update fee: %v", err)
	}
	return toFeeResponse(fee), nil
}

func (h *FeeGRPCHandler) DeleteFee(ctx context.Context, req *pb.DeleteFeeRequest) (*pb.DeleteFeeResponse, error) {
	if err := h.feeSvc.DeactivateFee(req.Id); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to deactivate fee: %v", err)
	}
	return &pb.DeleteFeeResponse{Success: true, Message: "fee deactivated"}, nil
}

func toFeeResponse(f *model.TransferFee) *pb.TransferFeeResponse {
	return &pb.TransferFeeResponse{
		Id:              f.ID,
		Name:            f.Name,
		FeeType:         f.FeeType,
		FeeValue:        f.FeeValue.StringFixed(4),
		MinAmount:       f.MinAmount.StringFixed(4),
		MaxFee:          f.MaxFee.StringFixed(4),
		TransactionType: f.TransactionType,
		CurrencyCode:    f.CurrencyCode,
		Active:          f.Active,
	}
}
