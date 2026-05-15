package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

type RecurringOrderHandler struct {
	pb.UnimplementedRecurringOrderServiceServer
	svc *service.RecurringOrderService
}

func NewRecurringOrderHandler(svc *service.RecurringOrderService) *RecurringOrderHandler {
	return &RecurringOrderHandler{svc: svc}
}

func (h *RecurringOrderHandler) CreateOrder(ctx context.Context, in *pb.CreateRecurringOrderRequest) (*pb.RecurringOrderResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if in.GetListingId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id is required")
	}
	if in.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	row := &model.RecurringOrder{
		OwnerType: ot,
		OwnerID:   oid,
		ListingID: in.GetListingId(),
		Side:      in.GetSide(),
		Quantity:  in.GetQuantity(),
		AccountID: in.GetAccountId(),
		Interval:  model.RecurrenceInterval(in.GetInterval()),
		Status:    model.RecurringOrderStatusActive,
	}
	if in.GetDayOfWeek() != 0 || row.Interval == model.RecurrenceWeekly {
		dw := int(in.GetDayOfWeek())
		row.DayOfWeek = &dw
	}
	if in.GetDayOfMonth() != 0 || row.Interval == model.RecurrenceMonthly {
		dm := int(in.GetDayOfMonth())
		row.DayOfMonth = &dm
	}
	if in.GetStartDateUnix() > 0 {
		row.StartDate = time.Unix(in.GetStartDateUnix(), 0).UTC()
	} else {
		row.StartDate = time.Now().UTC()
	}
	if in.GetEndDateUnix() > 0 {
		t := time.Unix(in.GetEndDateUnix(), 0).UTC()
		row.EndDate = &t
	}
	if err := h.svc.Create(row); err != nil {
		return nil, err
	}
	return recurringOrderToProto(row), nil
}

func (h *RecurringOrderHandler) GetOrder(ctx context.Context, in *pb.GetRecurringOrderRequest) (*pb.RecurringOrderResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	r, err := h.svc.Get(in.GetId(), ot, oid)
	if err != nil {
		return nil, err
	}
	return recurringOrderToProto(r), nil
}

func (h *RecurringOrderHandler) PauseOrder(ctx context.Context, in *pb.GetRecurringOrderRequest) (*pb.RecurringOrderResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Pause(in.GetId(), ot, oid); err != nil {
		return nil, err
	}
	r, _ := h.svc.Get(in.GetId(), ot, oid)
	return recurringOrderToProto(r), nil
}

func (h *RecurringOrderHandler) ResumeOrder(ctx context.Context, in *pb.GetRecurringOrderRequest) (*pb.RecurringOrderResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Resume(in.GetId(), ot, oid); err != nil {
		return nil, err
	}
	r, _ := h.svc.Get(in.GetId(), ot, oid)
	return recurringOrderToProto(r), nil
}

func (h *RecurringOrderHandler) CancelOrder(ctx context.Context, in *pb.GetRecurringOrderRequest) (*pb.RecurringOrderResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Cancel(in.GetId(), ot, oid); err != nil {
		return nil, err
	}
	r, _ := h.svc.Get(in.GetId(), ot, oid)
	return recurringOrderToProto(r), nil
}

func (h *RecurringOrderHandler) ListMy(ctx context.Context, in *pb.ListMyRecurringOrdersRequest) (*pb.ListMyRecurringOrdersResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	rows, err := h.svc.ListMy(ot, oid)
	if err != nil {
		return nil, err
	}
	out := &pb.ListMyRecurringOrdersResponse{Items: make([]*pb.RecurringOrderResponse, 0, len(rows))}
	for i := range rows {
		out.Items = append(out.Items, recurringOrderToProto(&rows[i]))
	}
	return out, nil
}

func recurringOrderToProto(r *model.RecurringOrder) *pb.RecurringOrderResponse {
	out := &pb.RecurringOrderResponse{
		Id:             r.ID,
		ListingId:      r.ListingID,
		Side:           r.Side,
		Quantity:       r.Quantity,
		AccountId:      r.AccountID,
		Interval:       string(r.Interval),
		Status:         r.Status,
		StartDateUnix:  r.StartDate.Unix(),
		NextRunUnix:    r.NextRun.Unix(),
		CreatedAtUnix:  r.CreatedAt.Unix(),
	}
	if r.DayOfWeek != nil {
		out.DayOfWeek = int32(*r.DayOfWeek)
	}
	if r.DayOfMonth != nil {
		out.DayOfMonth = int32(*r.DayOfMonth)
	}
	if r.EndDate != nil {
		out.EndDateUnix = r.EndDate.Unix()
	}
	if r.LastRun != nil {
		out.LastRunUnix = r.LastRun.Unix()
	}
	return out
}
