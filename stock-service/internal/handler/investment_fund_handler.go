package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// InvestmentFundHandler implements the InvestmentFundService gRPC server.
// Methods that depend on follow-up tasks (position-reads, actuary-performance,
// liquidation) return empty responses or NotImplemented until those tasks
// land — see the docstring on each method.
type InvestmentFundHandler struct {
	stockpb.UnimplementedInvestmentFundServiceServer
	fundSvc      *service.FundService
	fundRepo     *repository.FundRepository
	positions    *repository.ClientFundPositionRepository
}

func NewInvestmentFundHandler(
	fundSvc *service.FundService,
	fundRepo *repository.FundRepository,
	positions *repository.ClientFundPositionRepository,
) *InvestmentFundHandler {
	return &InvestmentFundHandler{
		fundSvc:   fundSvc,
		fundRepo:  fundRepo,
		positions: positions,
	}
}

func (h *InvestmentFundHandler) CreateFund(ctx context.Context, in *stockpb.CreateFundRequest) (*stockpb.FundResponse, error) {
	min := decimal.Zero
	if in.MinimumContributionRsd != "" {
		var err error
		min, err = decimal.NewFromString(in.MinimumContributionRsd)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "minimum_contribution_rsd is not a valid decimal")
		}
	}
	out, err := h.fundSvc.Create(ctx, service.CreateFundInput{
		ActorEmployeeID:        in.ActorEmployeeId,
		Name:                   in.Name,
		Description:            in.Description,
		MinimumContributionRSD: min,
	})
	if err != nil {
		return nil, mapFundErr(err)
	}
	return toFundResponse(out), nil
}

func (h *InvestmentFundHandler) ListFunds(ctx context.Context, in *stockpb.ListFundsRequest) (*stockpb.ListFundsResponse, error) {
	var active *bool
	if in.ActiveOnly {
		t := true
		active = &t
	}
	rows, total, err := h.fundSvc.List(in.Search, active, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListFundsResponse{Total: total, Funds: make([]*stockpb.FundResponse, 0, len(rows))}
	for i := range rows {
		out.Funds = append(out.Funds, toFundResponse(&rows[i]))
	}
	return out, nil
}

func (h *InvestmentFundHandler) GetFund(ctx context.Context, in *stockpb.GetFundRequest) (*stockpb.FundDetailResponse, error) {
	f, err := h.fundSvc.GetByID(in.FundId)
	if err != nil {
		return nil, mapFundErr(err)
	}
	return &stockpb.FundDetailResponse{
		Fund:     toFundResponse(f),
		Holdings: nil, // populated by Task 20 (position-reads service)
	}, nil
}

func (h *InvestmentFundHandler) UpdateFund(ctx context.Context, in *stockpb.UpdateFundRequest) (*stockpb.FundResponse, error) {
	upd := service.UpdateFundInput{
		ActorEmployeeID: in.ActorEmployeeId,
		FundID:          in.FundId,
	}
	if in.Name != "" {
		upd.Name = &in.Name
	}
	if in.Description != "" {
		upd.Description = &in.Description
	}
	if in.MinimumContributionRsd != "" {
		d, err := decimal.NewFromString(in.MinimumContributionRsd)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "minimum_contribution_rsd is not a valid decimal")
		}
		upd.MinimumContributionRSD = &d
	}
	if in.ActiveSet {
		upd.Active = &in.Active
	}
	out, err := h.fundSvc.Update(ctx, upd)
	if err != nil {
		return nil, mapFundErr(err)
	}
	return toFundResponse(out), nil
}

func (h *InvestmentFundHandler) InvestInFund(ctx context.Context, in *stockpb.InvestInFundRequest) (*stockpb.ContributionResponse, error) {
	amt, err := decimal.NewFromString(in.Amount)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "amount is not a valid decimal")
	}
	obhfType := "self"
	if in.OnBehalfOf != nil && in.OnBehalfOf.Type != "" {
		obhfType = in.OnBehalfOf.Type
	}
	out, err := h.fundSvc.Invest(ctx, service.InvestInput{
		FundID:          in.FundId,
		ActorUserID:     in.ActorUserId,
		ActorSystemType: in.ActorSystemType,
		SourceAccountID: in.SourceAccountId,
		Amount:          amt,
		Currency:        in.Currency,
		OnBehalfOfType:  obhfType,
	})
	if err != nil {
		return nil, mapFundErr(err)
	}
	return toContribResponse(out), nil
}

func (h *InvestmentFundHandler) RedeemFromFund(ctx context.Context, in *stockpb.RedeemFromFundRequest) (*stockpb.ContributionResponse, error) {
	amt, err := decimal.NewFromString(in.AmountRsd)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "amount_rsd is not a valid decimal")
	}
	obhfType := "self"
	if in.OnBehalfOf != nil && in.OnBehalfOf.Type != "" {
		obhfType = in.OnBehalfOf.Type
	}
	out, err := h.fundSvc.Redeem(ctx, service.RedeemInput{
		FundID:          in.FundId,
		ActorUserID:     in.ActorUserId,
		ActorSystemType: in.ActorSystemType,
		AmountRSD:       amt,
		TargetAccountID: in.TargetAccountId,
		OnBehalfOfType:  obhfType,
	})
	if err != nil {
		return nil, mapFundErr(err)
	}
	return toContribResponse(out), nil
}

// ListMyPositions returns the caller's contributions across active funds.
// Derived value/profit/percentage fields land with Task 20.
func (h *InvestmentFundHandler) ListMyPositions(ctx context.Context, in *stockpb.ListMyPositionsRequest) (*stockpb.ListPositionsResponse, error) {
	if h.positions == nil {
		return &stockpb.ListPositionsResponse{}, nil
	}
	rows, err := h.positions.ListByOwner(in.ActorUserId, in.ActorSystemType)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListPositionsResponse{Positions: make([]*stockpb.PositionItem, 0, len(rows))}
	for _, p := range rows {
		fund, err := h.fundRepo.GetByID(p.FundID)
		if err != nil {
			continue
		}
		out.Positions = append(out.Positions, &stockpb.PositionItem{
			FundId:           p.FundID,
			FundName:         fund.Name,
			ContributionRsd:  p.TotalContributedRSD.String(),
			LastChangedAt:    p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out, nil
}

// ListBankPositions returns positions where the bank itself is the owner.
func (h *InvestmentFundHandler) ListBankPositions(ctx context.Context, _ *stockpb.ListBankPositionsRequest) (*stockpb.ListPositionsResponse, error) {
	return h.ListMyPositions(ctx, &stockpb.ListMyPositionsRequest{
		ActorUserId:     1_000_000_000,
		ActorSystemType: "employee",
	})
}

// GetActuaryPerformance is wired in Task 21. Until then the gRPC method
// returns an empty list so the gateway endpoint behaves predictably.
func (h *InvestmentFundHandler) GetActuaryPerformance(ctx context.Context, _ *stockpb.GetActuaryPerformanceRequest) (*stockpb.GetActuaryPerformanceResponse, error) {
	return &stockpb.GetActuaryPerformanceResponse{Actuaries: nil}, nil
}

func toFundResponse(f *model.InvestmentFund) *stockpb.FundResponse {
	return &stockpb.FundResponse{
		Id:                     f.ID,
		Name:                   f.Name,
		Description:            f.Description,
		ManagerEmployeeId:      f.ManagerEmployeeID,
		MinimumContributionRsd: f.MinimumContributionRSD.String(),
		RsdAccountId:           f.RSDAccountID,
		Active:                 f.Active,
		CreatedAt:              f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:              f.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toContribResponse(c *model.FundContribution) *stockpb.ContributionResponse {
	fxStr := ""
	if c.FxRate != nil {
		fxStr = c.FxRate.String()
	}
	return &stockpb.ContributionResponse{
		Id:             c.ID,
		FundId:         c.FundID,
		Direction:      c.Direction,
		AmountNative:   c.AmountNative.String(),
		NativeCurrency: c.NativeCurrency,
		AmountRsd:      c.AmountRSD.String(),
		FxRate:         fxStr,
		FeeRsd:         c.FeeRSD.String(),
		Status:         c.Status,
	}
}

func mapFundErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	if errors.Is(err, repository.ErrFundNameInUse) {
		return status.Error(codes.AlreadyExists, err.Error())
	}
	if errors.Is(err, service.ErrInsufficientFundCash) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "minimum_contribution_not_met"):
		return status.Error(codes.FailedPrecondition, msg)
	case strings.Contains(msg, "is inactive"),
		strings.Contains(msg, "exceeds position"),
		strings.Contains(msg, "must be"),
		strings.Contains(msg, "is not the fund manager"):
		return status.Error(codes.InvalidArgument, msg)
	default:
		return status.Error(codes.Internal, msg)
	}
}
