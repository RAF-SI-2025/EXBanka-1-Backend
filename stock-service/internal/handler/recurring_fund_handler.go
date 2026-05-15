package handler

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type RecurringFundHandler struct {
	pb.UnimplementedRecurringFundServiceServer
	svc *service.RecurringFundService
}

func NewRecurringFundHandler(svc *service.RecurringFundService) *RecurringFundHandler {
	return &RecurringFundHandler{svc: svc}
}

func (h *RecurringFundHandler) Create(ctx context.Context, in *pb.CreateRecurringFundRequest) (*pb.RecurringFundResponse, error) {
	if in.GetClientId() == 0 || in.GetFundId() == 0 || in.GetSourceAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "client_id, fund_id, source_account_id are required")
	}
	amt, err := decimal.NewFromString(in.GetAmountRsd())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount_rsd: %v", err)
	}
	row := &model.RecurringFundInvestment{
		ClientID:        in.GetClientId(),
		FundID:          in.GetFundId(),
		AmountRSD:       amt,
		SourceAccountID: in.GetSourceAccountId(),
		DayOfMonth:      int(in.GetDayOfMonth()),
		Active:          true,
	}
	if err := h.svc.Create(row); err != nil {
		return nil, err
	}
	return recurringFundToProto(row), nil
}

func (h *RecurringFundHandler) Get(ctx context.Context, in *pb.GetRecurringFundRequest) (*pb.RecurringFundResponse, error) {
	row, err := h.svc.Get(in.GetId(), in.GetClientId())
	if err != nil {
		return nil, err
	}
	return recurringFundToProto(row), nil
}

func (h *RecurringFundHandler) Pause(ctx context.Context, in *pb.GetRecurringFundRequest) (*pb.RecurringFundResponse, error) {
	if err := h.svc.Pause(in.GetId(), in.GetClientId()); err != nil {
		return nil, err
	}
	row, _ := h.svc.Get(in.GetId(), in.GetClientId())
	return recurringFundToProto(row), nil
}

func (h *RecurringFundHandler) Resume(ctx context.Context, in *pb.GetRecurringFundRequest) (*pb.RecurringFundResponse, error) {
	if err := h.svc.Resume(in.GetId(), in.GetClientId()); err != nil {
		return nil, err
	}
	row, _ := h.svc.Get(in.GetId(), in.GetClientId())
	return recurringFundToProto(row), nil
}

func (h *RecurringFundHandler) Cancel(ctx context.Context, in *pb.GetRecurringFundRequest) (*pb.CancelRecurringFundResponse, error) {
	if err := h.svc.Cancel(in.GetId(), in.GetClientId()); err != nil {
		return nil, err
	}
	return &pb.CancelRecurringFundResponse{Cancelled: true}, nil
}

func (h *RecurringFundHandler) ListMy(ctx context.Context, in *pb.ListMyRecurringFundsRequest) (*pb.ListMyRecurringFundsResponse, error) {
	rows, err := h.svc.ListMy(in.GetClientId())
	if err != nil {
		return nil, err
	}
	out := &pb.ListMyRecurringFundsResponse{Items: make([]*pb.RecurringFundResponse, 0, len(rows))}
	for i := range rows {
		out.Items = append(out.Items, recurringFundToProto(&rows[i]))
	}
	return out, nil
}

func recurringFundToProto(r *model.RecurringFundInvestment) *pb.RecurringFundResponse {
	out := &pb.RecurringFundResponse{
		Id:              r.ID,
		FundId:          r.FundID,
		AmountRsd:       r.AmountRSD.String(),
		SourceAccountId: r.SourceAccountID,
		DayOfMonth:      int32(r.DayOfMonth),
		Active:          r.Active,
		NextRunUnix:     r.NextRun.Unix(),
		CreatedAtUnix:   r.CreatedAt.Unix(),
	}
	if r.LastRun != nil {
		out.LastRunUnix = r.LastRun.Unix()
	}
	return out
}
