package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// portfolioSvcFacade is the narrow interface of PortfolioService used by PortfolioHandler.
type portfolioSvcFacade interface {
	ListHoldings(ownerType model.OwnerType, ownerID *uint64, filter service.HoldingFilter) ([]model.Holding, int64, error)
	GetCurrentPrice(listingID uint64) (decimal.Decimal, error)
	MakePublic(holdingID uint64, ownerType model.OwnerType, ownerID *uint64, quantity int64) (*model.Holding, error)
	ExerciseOption(holdingID uint64, ownerType model.OwnerType, ownerID *uint64) (*service.ExerciseResult, error)
	ListHoldingTransactions(holdingID uint64, ownerType model.OwnerType, ownerID *uint64, direction string, page, pageSize int) ([]repository.HoldingTransactionRow, int64, error)
	ExerciseOptionByOptionID(ctx context.Context, optionID uint64, ownerType model.OwnerType, ownerID *uint64, holdingID uint64) (*service.ExerciseResult, error)
}

// taxSvcFacade is the narrow interface of TaxService used by PortfolioHandler.
type taxSvcFacade interface {
	GetUserGainsAndTax(ownerType model.OwnerType, ownerID *uint64) (service.UserGainsAndTax, error)
}

type PortfolioHandler struct {
	pb.UnimplementedPortfolioGRPCServiceServer
	portfolioSvc portfolioSvcFacade
	taxSvc       taxSvcFacade
}

func NewPortfolioHandler(portfolioSvc *service.PortfolioService, taxSvc *service.TaxService) *PortfolioHandler {
	return &PortfolioHandler{portfolioSvc: portfolioSvc, taxSvc: taxSvc}
}

// newPortfolioHandlerForTest constructs a PortfolioHandler with interface-typed
// dependencies for use in unit tests.
func newPortfolioHandlerForTest(portfolioSvc portfolioSvcFacade, taxSvc taxSvcFacade) *PortfolioHandler {
	return &PortfolioHandler{portfolioSvc: portfolioSvc, taxSvc: taxSvc}
}

// ListHoldings returns the quantity-only view of a user's portfolio.
// Per-purchase price detail (average_price, current_price, profit) moved
// to GET /me/holdings/{id}/transactions in Part B so the list stays small
// and reflects the Part-A rollup (one row per (user, security)).
// PublicQuantity and AccountID are still populated so the UI can surface
// "X publicly offered" + the last-used account without a second round-trip.
func (h *PortfolioHandler) ListHoldings(ctx context.Context, req *pb.ListHoldingsRequest) (*pb.ListHoldingsResponse, error) {
	filter := service.HoldingFilter{
		SecurityType: req.SecurityType,
		Page:         int(req.Page),
		PageSize:     int(req.PageSize),
	}

	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	holdings, total, err := h.portfolioSvc.ListHoldings(ownerType, ownerID, filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pbHoldings := make([]*pb.Holding, len(holdings))
	for i, hld := range holdings {
		// Part C: strip average_price / current_price / profit. Use the
		// Part-B transactions endpoint for per-purchase details.
		pbHoldings[i] = &pb.Holding{
			Id:             hld.ID,
			SecurityType:   hld.SecurityType,
			Ticker:         hld.Ticker,
			Name:           hld.Name,
			Quantity:       hld.Quantity,
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
	// Compute unrealized profit across all current holdings. Summed in each
	// holding's native listing currency (no FX) — acceptable for display until
	// multi-currency holdings become common.
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	allHoldings, _, err := h.portfolioSvc.ListHoldings(ownerType, ownerID, service.HoldingFilter{
		Page: 1, PageSize: 10000,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	unrealized := decimal.Zero
	openPositions := int64(0)
	for _, holding := range allHoldings {
		if holding.Quantity <= 0 {
			continue
		}
		openPositions++
		currentPrice, priceErr := h.portfolioSvc.GetCurrentPrice(holding.ListingID)
		if priceErr != nil {
			continue
		}
		profit := currentPrice.Sub(holding.AveragePrice).Mul(decimal.NewFromInt(holding.Quantity))
		unrealized = unrealized.Add(profit)
	}

	// Rich gains + tax breakdown (converts every native-currency amount to RSD).
	gt, err := h.taxSvc.GetUserGainsAndTax(ownerType, ownerID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Backwards-compat: total_profit / total_profit_rsd combine lifetime
	// realised (RSD) + unrealised (native) so clients that only look at this
	// field still see a meaningful number after the user closes positions.
	totalProfit := gt.RealizedGainLifetimeRSD.Add(unrealized).Round(2)

	return &pb.PortfolioSummary{
		TotalProfit:                 totalProfit.StringFixed(2),
		TotalProfitRsd:              totalProfit.StringFixed(2),
		TaxPaidThisYear:             gt.TaxPaidThisYearRSD.StringFixed(2),
		TaxUnpaidThisMonth:          gt.TaxUnpaidThisMonthRSD.StringFixed(2),
		RealizedProfitThisMonthRsd:  gt.RealizedGainThisMonthRSD.StringFixed(2),
		RealizedProfitThisYearRsd:   gt.RealizedGainThisYearRSD.StringFixed(2),
		RealizedProfitLifetimeRsd:   gt.RealizedGainLifetimeRSD.StringFixed(2),
		UnrealizedProfit:            unrealized.Round(2).StringFixed(2),
		TaxUnpaidTotalRsd:           gt.TaxUnpaidTotalRSD.StringFixed(2),
		OpenPositionsCount:          openPositions,
		ClosedTradesThisYear:        gt.ClosedTradesThisYear,
	}, nil
}

func (h *PortfolioHandler) MakePublic(ctx context.Context, req *pb.MakePublicRequest) (*pb.Holding, error) {
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	holding, err := h.portfolioSvc.MakePublic(req.HoldingId, ownerType, ownerID, req.Quantity)
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
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	result, err := h.portfolioSvc.ExerciseOption(req.HoldingId, ownerType, ownerID)
	if err != nil {
		return nil, mapPortfolioError(err)
	}

	return toExerciseResultPB(result), nil
}

// ListHoldingTransactions surfaces the per-purchase history for a single
// holding (Part B). Ownership is enforced by the service layer; errors are
// mapped to gRPC codes via mapPortfolioError.
func (h *PortfolioHandler) ListHoldingTransactions(ctx context.Context, req *pb.ListHoldingTransactionsRequest) (*pb.ListHoldingTransactionsResponse, error) {
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	rows, total, err := h.portfolioSvc.ListHoldingTransactions(
		req.HoldingId, ownerType, ownerID, req.Direction, page, pageSize,
	)
	if err != nil {
		return nil, mapPortfolioError(err)
	}
	out := make([]*pb.HoldingTransaction, len(rows))
	for i, r := range rows {
		native := ""
		if r.NativeAmount != nil {
			native = r.NativeAmount.StringFixed(4)
		}
		converted := ""
		if r.ConvertedAmount != nil {
			converted = r.ConvertedAmount.StringFixed(4)
		}
		fx := ""
		if r.FxRate != nil {
			fx = r.FxRate.StringFixed(8)
		}
		out[i] = &pb.HoldingTransaction{
			Id:              r.ID,
			OrderId:         r.OrderID,
			ExecutedAt:      r.ExecutedAt.Format("2006-01-02T15:04:05Z"),
			Direction:       r.Direction,
			Quantity:        r.Quantity,
			PricePerUnit:    r.PricePerUnit.StringFixed(4),
			NativeAmount:    native,
			NativeCurrency:  r.NativeCurrency,
			ConvertedAmount: converted,
			AccountCurrency: r.AccountCurrency,
			FxRate:          fx,
			Commission:      r.Commission.StringFixed(4),
			AccountId:       r.AccountID,
			Ticker:          r.Ticker,
		}
	}
	return &pb.ListHoldingTransactionsResponse{Transactions: out, TotalCount: total}, nil
}

func (h *PortfolioHandler) ExerciseOptionByOptionID(ctx context.Context, req *pb.ExerciseOptionByOptionIDRequest) (*pb.ExerciseResult, error) {
	ownerType, ownerID := model.OwnerFromLegacy(req.UserId, req.SystemType)
	result, err := h.portfolioSvc.ExerciseOptionByOptionID(ctx, req.OptionId, ownerType, ownerID, req.HoldingId)
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

// mapPortfolioError is now a passthrough. Service-layer sentinels carry
// their own gRPC code via svcerr.SentinelError. See internal/service/errors.go
// for the portfolio/holding sentinel set.
func mapPortfolioError(err error) error {
	return err
}
