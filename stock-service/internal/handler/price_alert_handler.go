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

type PriceAlertHandler struct {
	pb.UnimplementedPriceAlertServiceServer
	svc *service.PriceAlertService
}

func NewPriceAlertHandler(svc *service.PriceAlertService) *PriceAlertHandler {
	return &PriceAlertHandler{svc: svc}
}

func (h *PriceAlertHandler) CreateAlert(ctx context.Context, in *pb.CreatePriceAlertRequest) (*pb.PriceAlertResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	th, derr := decimal.NewFromString(in.GetThreshold())
	if derr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "threshold: %v", derr)
	}
	cooldown := int(in.GetCooldownSeconds())
	if cooldown == 0 {
		cooldown = 3600
	}
	a := &model.PriceAlert{
		OwnerType:   ot,
		OwnerID:     oid,
		ListingID:   in.GetListingId(),
		Condition:   model.PriceAlertCondition(in.GetCondition()),
		Threshold:   th,
		IsRecurring: in.GetIsRecurring(),
		Cooldown:    cooldown,
		EmailToo:    in.GetEmailToo(),
		Active:      true,
	}
	if err := h.svc.Create(a); err != nil {
		return nil, err
	}
	return alertToProto(a), nil
}

func (h *PriceAlertHandler) UpdateAlert(ctx context.Context, in *pb.UpdatePriceAlertRequest) (*pb.PriceAlertResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	a, err := h.svc.Get(in.GetId(), ot, oid)
	if err != nil {
		return nil, err
	}
	if c := in.GetCondition(); c != "" {
		a.Condition = model.PriceAlertCondition(c)
	}
	if t := in.GetThreshold(); t != "" {
		th, derr := decimal.NewFromString(t)
		if derr != nil {
			return nil, status.Errorf(codes.InvalidArgument, "threshold: %v", derr)
		}
		a.Threshold = th
	}
	a.IsRecurring = in.GetIsRecurring()
	if cs := int(in.GetCooldownSeconds()); cs > 0 {
		a.Cooldown = cs
	}
	a.EmailToo = in.GetEmailToo()
	a.Active = in.GetActive()
	if err := h.svc.Update(a); err != nil {
		return nil, err
	}
	return alertToProto(a), nil
}

func (h *PriceAlertHandler) GetAlert(ctx context.Context, in *pb.GetPriceAlertRequest) (*pb.PriceAlertResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	a, err := h.svc.Get(in.GetId(), ot, oid)
	if err != nil {
		return nil, err
	}
	return alertToProto(a), nil
}

func (h *PriceAlertHandler) DeleteAlert(ctx context.Context, in *pb.DeletePriceAlertRequest) (*pb.DeletePriceAlertResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(in.GetId(), ot, oid); err != nil {
		return nil, err
	}
	return &pb.DeletePriceAlertResponse{Deleted: true}, nil
}

func (h *PriceAlertHandler) ListMy(ctx context.Context, in *pb.ListMyPriceAlertsRequest) (*pb.ListMyPriceAlertsResponse, error) {
	ot, oid, err := ownerFromRequest(in.GetOwnerType(), in.GetOwnerId())
	if err != nil {
		return nil, err
	}
	rows, err := h.svc.ListMy(ot, oid)
	if err != nil {
		return nil, err
	}
	out := &pb.ListMyPriceAlertsResponse{Alerts: make([]*pb.PriceAlertResponse, 0, len(rows))}
	for i := range rows {
		out.Alerts = append(out.Alerts, alertToProto(&rows[i]))
	}
	return out, nil
}

func alertToProto(a *model.PriceAlert) *pb.PriceAlertResponse {
	var lastTriggered int64
	if a.LastTriggered != nil {
		lastTriggered = a.LastTriggered.Unix()
	}
	return &pb.PriceAlertResponse{
		Id:                 a.ID,
		ListingId:          a.ListingID,
		Condition:          string(a.Condition),
		Threshold:          a.Threshold.String(),
		IsRecurring:        a.IsRecurring,
		CooldownSeconds:    int32(a.Cooldown),
		EmailToo:           a.EmailToo,
		Active:             a.Active,
		LastTriggeredUnix:  lastTriggered,
		CreatedAtUnix:      a.CreatedAt.Unix(),
	}
}
