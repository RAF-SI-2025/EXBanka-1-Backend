package handler

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
)

// ReservationHandler implements the four reservation RPCs on AccountService.
// It's composed into AccountGRPCHandler so its methods override the
// UnimplementedAccountServiceServer defaults.
type ReservationHandler struct {
	svc *service.ReservationService
}

// NewReservationHandler constructs a ReservationHandler around the given
// ReservationService.
func NewReservationHandler(svc *service.ReservationService) *ReservationHandler {
	return &ReservationHandler{svc: svc}
}

// ReserveFunds parses the decimal amount and delegates to the service layer.
// Returns codes.InvalidArgument on malformed amount; all other errors come
// from the service already wrapped in gRPC status codes.
func (h *ReservationHandler) ReserveFunds(ctx context.Context, req *pb.ReserveFundsRequest) (*pb.ReserveFundsResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid amount")
	}
	res, err := h.svc.ReserveFunds(ctx, req.GetOrderId(), req.GetAccountId(), amount, req.GetCurrencyCode())
	if err != nil {
		return nil, err
	}
	return &pb.ReserveFundsResponse{
		ReservationId:    res.ReservationID,
		ReservedBalance:  res.ReservedBalance.String(),
		AvailableBalance: res.AvailableBalance.String(),
	}, nil
}

// ReleaseReservation delegates to the service layer. Service returns zero
// ReleasedAmount for missing / non-active reservations rather than an error.
func (h *ReservationHandler) ReleaseReservation(ctx context.Context, req *pb.ReleaseReservationRequest) (*pb.ReleaseReservationResponse, error) {
	res, err := h.svc.ReleaseReservation(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}
	return &pb.ReleaseReservationResponse{
		ReleasedAmount:  res.ReleasedAmount.String(),
		ReservedBalance: res.ReservedBalance.String(),
	}, nil
}

// PartialSettleReservation parses the decimal amount and delegates to the
// service layer. Returns codes.InvalidArgument on malformed amount.
func (h *ReservationHandler) PartialSettleReservation(ctx context.Context, req *pb.PartialSettleReservationRequest) (*pb.PartialSettleReservationResponse, error) {
	amount, err := decimal.NewFromString(req.GetAmount())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid amount")
	}
	res, err := h.svc.PartialSettleReservation(ctx, req.GetOrderId(), req.GetOrderTransactionId(), amount, req.GetMemo())
	if err != nil {
		return nil, err
	}
	return &pb.PartialSettleReservationResponse{
		SettledAmount:     res.SettledAmount.String(),
		RemainingReserved: res.RemainingReserved.String(),
		BalanceAfter:      res.BalanceAfter.String(),
		LedgerEntryId:     res.LedgerEntryID,
	}, nil
}

// GetReservation is read-only. If the reservation does not exist, the
// response Exists field is false and all decimal/status fields are the
// zero values.
func (h *ReservationHandler) GetReservation(ctx context.Context, req *pb.GetReservationRequest) (*pb.GetReservationResponse, error) {
	st, amount, settled, ids, exists, err := h.svc.GetReservation(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}
	return &pb.GetReservationResponse{
		Exists:                exists,
		Status:                st,
		Amount:                amount.String(),
		SettledTotal:          settled.String(),
		SettledTransactionIds: ids,
	}, nil
}
