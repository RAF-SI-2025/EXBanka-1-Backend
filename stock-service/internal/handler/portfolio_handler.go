package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

type PortfolioHandler struct {
	pb.UnimplementedPortfolioGRPCServiceServer
	portfolioSvc *service.PortfolioService
	taxSvc       *service.TaxService
}

func NewPortfolioHandler(portfolioSvc *service.PortfolioService, taxSvc *service.TaxService) *PortfolioHandler {
	return &PortfolioHandler{portfolioSvc: portfolioSvc, taxSvc: taxSvc}
}

func (h *PortfolioHandler) ListHoldings(ctx context.Context, req *pb.ListHoldingsRequest) (*pb.ListHoldingsResponse, error) {
	filter := service.HoldingFilter{
		SecurityType: req.SecurityType,
		Page:         int(req.Page),
		PageSize:     int(req.PageSize),
	}

	holdings, total, err := h.portfolioSvc.ListHoldings(req.UserId, req.SystemType, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pbHoldings := make([]*pb.Holding, len(holdings))
	for i, hld := range holdings {
		currentPrice, _ := h.portfolioSvc.GetCurrentPrice(hld.ListingID)
		profit := currentPrice.Sub(hld.AveragePrice).Mul(decimal.NewFromInt(hld.Quantity))

		pbHoldings[i] = &pb.Holding{
			Id:             hld.ID,
			SecurityType:   hld.SecurityType,
			Ticker:         hld.Ticker,
			Name:           hld.Name,
			Quantity:       hld.Quantity,
			AveragePrice:   hld.AveragePrice.StringFixed(2),
			CurrentPrice:   currentPrice.StringFixed(2),
			Profit:         profit.StringFixed(2),
			PublicQuantity: hld.PublicQuantity,
			AccountId:      hld.AccountID,
			LastModified:   hld.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return &pb.ListHoldingsResponse{
		Holdings:   pbHoldings,
		TotalCount: total,
	}, nil
}

func (h *PortfolioHandler) GetPortfolioSummary(ctx context.Context, req *pb.GetPortfolioSummaryRequest) (*pb.PortfolioSummary, error) {
	// Compute total unrealized profit across all holdings
	allHoldings, _, err := h.portfolioSvc.ListHoldings(req.UserId, req.SystemType, service.HoldingFilter{
		Page: 1, PageSize: 10000,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	totalProfit := decimal.Zero
	for _, holding := range allHoldings {
		currentPrice, priceErr := h.portfolioSvc.GetCurrentPrice(holding.ListingID)
		if priceErr != nil {
			continue
		}
		profit := currentPrice.Sub(holding.AveragePrice).Mul(decimal.NewFromInt(holding.Quantity))
		totalProfit = totalProfit.Add(profit)
	}

	// Get tax info
	taxPaidYear, taxUnpaidMonth, err := h.taxSvc.GetUserTaxSummary(req.UserId, req.SystemType)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &pb.PortfolioSummary{
		TotalProfit:        totalProfit.StringFixed(2),
		TotalProfitRsd:     totalProfit.StringFixed(2), // TODO: convert via exchange-service if multi-currency
		TaxPaidThisYear:    taxPaidYear.StringFixed(2),
		TaxUnpaidThisMonth: taxUnpaidMonth.StringFixed(2),
	}, nil
}

func (h *PortfolioHandler) MakePublic(ctx context.Context, req *pb.MakePublicRequest) (*pb.Holding, error) {
	holding, err := h.portfolioSvc.MakePublic(req.HoldingId, req.UserId, req.SystemType, req.Quantity)
	if err != nil {
		return nil, mapPortfolioError(err)
	}

	return &pb.Holding{
		Id:             holding.ID,
		SecurityType:   holding.SecurityType,
		Ticker:         holding.Ticker,
		Name:           holding.Name,
		Quantity:       holding.Quantity,
		AveragePrice:   holding.AveragePrice.StringFixed(2),
		PublicQuantity: holding.PublicQuantity,
		AccountId:      holding.AccountID,
		LastModified:   holding.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (h *PortfolioHandler) ExerciseOption(ctx context.Context, req *pb.ExerciseOptionRequest) (*pb.ExerciseResult, error) {
	result, err := h.portfolioSvc.ExerciseOption(req.HoldingId, req.UserId, req.SystemType)
	if err != nil {
		return nil, mapPortfolioError(err)
	}

	return toExerciseResultPB(result), nil
}

func (h *PortfolioHandler) ExerciseOptionByOptionID(ctx context.Context, req *pb.ExerciseOptionByOptionIDRequest) (*pb.ExerciseResult, error) {
	result, err := h.portfolioSvc.ExerciseOptionByOptionID(ctx, req.OptionId, req.UserId, req.SystemType, req.HoldingId)
	if err != nil {
		return nil, mapPortfolioError(err)
	}

	return toExerciseResultPB(result), nil
}

func toExerciseResultPB(result *service.ExerciseResult) *pb.ExerciseResult {
	return &pb.ExerciseResult{
		Id:                result.ID,
		OptionTicker:      result.OptionTicker,
		ExercisedQuantity: result.ExercisedQuantity,
		SharesAffected:    result.SharesAffected,
		Profit:            result.Profit.StringFixed(2),
	}
}

func mapPortfolioError(err error) error {
	switch err.Error() {
	case "holding not found", "option not found", "stock listing not found for option's underlying", "option holding not found":
		return status.Error(codes.NotFound, err.Error())
	case "holding does not belong to user":
		return status.Error(codes.PermissionDenied, err.Error())
	case "only stocks can be made public for OTC trading",
		"invalid public quantity",
		"holding is not an option",
		"option has expired (settlement date passed)",
		"call option is not in the money",
		"put option is not in the money",
		"insufficient stock holdings to exercise put option":
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
