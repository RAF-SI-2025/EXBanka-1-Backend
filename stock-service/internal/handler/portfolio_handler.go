package handler

import (
	"context"
	"fmt"
	"time"

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
	GetHoldingByID(holdingID uint64) (*model.Holding, error)
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
	unifiedSvc   *service.UnifiedPortfolioService
}

func NewPortfolioHandler(portfolioSvc *service.PortfolioService, taxSvc *service.TaxService) *PortfolioHandler {
	return &PortfolioHandler{portfolioSvc: portfolioSvc, taxSvc: taxSvc}
}

// WithUnifiedPortfolioService wires the UnifiedPortfolioService.
// Call this immediately after NewPortfolioHandler in cmd/main.go.
func (h *PortfolioHandler) WithUnifiedPortfolioService(svc *service.UnifiedPortfolioService) *PortfolioHandler {
	h.unifiedSvc = svc
	return h
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
		TotalProfit:                totalProfit.StringFixed(2),
		TotalProfitRsd:             totalProfit.StringFixed(2),
		TaxPaidThisYear:            gt.TaxPaidThisYearRSD.StringFixed(2),
		TaxUnpaidThisMonth:         gt.TaxUnpaidThisMonthRSD.StringFixed(2),
		RealizedProfitThisMonthRsd: gt.RealizedGainThisMonthRSD.StringFixed(2),
		RealizedProfitThisYearRsd:  gt.RealizedGainThisYearRSD.StringFixed(2),
		RealizedProfitLifetimeRsd:  gt.RealizedGainLifetimeRSD.StringFixed(2),
		UnrealizedProfit:           unrealized.Round(2).StringFixed(2),
		TaxUnpaidTotalRsd:          gt.TaxUnpaidTotalRSD.StringFixed(2),
		OpenPositionsCount:         openPositions,
		ClosedTradesThisYear:       gt.ClosedTradesThisYear,
	}, nil
}

// GetHolding returns a single Holding row plus its owner pair so the
// gateway can perform the CLAUDE.md ownership pre-check before mutating
// ops. (Fix R5, 2026-05-16.)
func (h *PortfolioHandler) GetHolding(ctx context.Context, req *pb.GetHoldingRequest) (*pb.HoldingWithOwner, error) {
	if req.GetId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	holding, err := h.portfolioSvc.GetHoldingByID(req.GetId())
	if err != nil {
		return nil, mapPortfolioError(err)
	}
	ownerID := uint64(0)
	if holding.OwnerID != nil {
		ownerID = *holding.OwnerID
	}
	return &pb.HoldingWithOwner{
		Holding: &pb.Holding{
			Id:             holding.ID,
			SecurityType:   holding.SecurityType,
			Ticker:         holding.Ticker,
			Name:           holding.Name,
			Quantity:       holding.Quantity,
			AveragePrice:   holding.AveragePrice.StringFixed(2),
			PublicQuantity: holding.PublicQuantity,
			AccountId:      holding.AccountID,
			LastModified:   holding.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		},
		OwnerType: string(holding.OwnerType),
		OwnerId:   ownerID,
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

// GetUnifiedPortfolio returns a fully composed portfolio for the requested
// owner (client, bank, or investment_fund), including fund positions with P/L.
func (h *PortfolioHandler) GetUnifiedPortfolio(ctx context.Context, req *pb.GetUnifiedPortfolioRequest) (*pb.UnifiedPortfolioResponse, error) {
	if req.OwnerType == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_type required")
	}
	if h.unifiedSvc == nil {
		return nil, status.Error(codes.Internal, "unified portfolio service not configured")
	}

	var ownerID *uint64
	if req.OwnerId != 0 {
		id := req.OwnerId
		ownerID = &id
	}

	out, err := h.unifiedSvc.Get(ctx, req.OwnerType, ownerID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return mapUnifiedToProto(out), nil
}

func mapUnifiedToProto(p *service.UnifiedPortfolio) *pb.UnifiedPortfolioResponse {
	resp := &pb.UnifiedPortfolioResponse{
		OwnerType:      p.OwnerType,
		TotalValueRsd:  p.TotalValueRSD.StringFixed(4),
		TotalProfitRsd: p.TotalProfitRSD.StringFixed(4),
		TotalProfitPct: p.TotalProfitPct.StringFixed(4),
		Securities:     mapGroupToProto(p.Securities),
		Funds:          mapGroupToProto(p.Funds),
		OwnerName:      p.OwnerName,
	}
	if p.OwnerID != nil {
		resp.OwnerId = *p.OwnerID
		resp.PortfolioId = fmt.Sprintf("%s-%d", encodeOwnerTypePrefix(p.OwnerType), *p.OwnerID)
	} else {
		resp.PortfolioId = "bank"
	}
	return resp
}

func encodeOwnerTypePrefix(ownerType string) string {
	switch ownerType {
	case "investment_fund":
		return "fund"
	default:
		return ownerType
	}
}

func mapGroupToProto(g service.PortfolioGroup) *pb.PortfolioGroup {
	out := &pb.PortfolioGroup{
		TotalValueRsd:  g.TotalValueRSD.StringFixed(4),
		TotalProfitRsd: g.TotalProfitRSD.StringFixed(4),
		TotalProfitPct: g.TotalProfitPct.StringFixed(4),
	}
	for _, p := range g.Positions {
		pos := &pb.PortfolioPosition{
			AssetType:            p.AssetType,
			Symbol:               p.Symbol,
			HoldingId:            p.HoldingID,
			FundId:               p.FundID,
			FundName:             p.FundName,
			FundStatus:           p.FundStatus,
			ContractId:           p.ContractID,
			Quantity:             p.Quantity,
			AvgCostRsd:           p.AvgCostRSD.StringFixed(4),
			CurrentPriceRsd:      p.CurrentPriceRSD.StringFixed(4),
			CurrentValueRsd:      p.CurrentValueRSD.StringFixed(4),
			PLRsd:                p.PLRSD.StringFixed(4),
			PLPct:                p.PLPct.StringFixed(4),
			AmountInvestedRsd:    p.AmountInvestedRSD.StringFixed(4),
			PctOfFund:            p.PctOfFund.StringFixed(4),
			StrikeRsd:            p.StrikeRSD.StringFixed(4),
			PremiumPaidRsd:       p.PremiumPaidRSD.StringFixed(4),
			IntrinsicValueRsd:    p.IntrinsicValueRSD.StringFixed(4),
			LastUpdated:          p.LastUpdated.UTC().Format(time.RFC3339),
			DividendsReceivedRsd: p.DividendsReceivedRSD.StringFixed(2),
		}
		if p.SettlementDate != nil {
			pos.SettlementDate = p.SettlementDate.UTC().Format("2006-01-02")
		}
		out.Positions = append(out.Positions, pos)
	}
	return out
}
