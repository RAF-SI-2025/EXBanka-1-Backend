package handler

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	exchangepb "github.com/exbanka/contract/exchangepb"
	stockpb "github.com/exbanka/contract/stockpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// InvestmentFundHandler implements the InvestmentFundService gRPC server.
// Methods that depend on follow-up tasks (position-reads, liquidation) return
// empty responses or NotImplemented until those tasks land.
type InvestmentFundHandler struct {
	stockpb.UnimplementedInvestmentFundServiceServer
	fundSvc        *service.FundService
	fundRepo       *repository.FundRepository
	positions      *repository.ClientFundPositionRepository
	capitalGains   *repository.CapitalGainRepository
	userClient     userpb.UserServiceClient
	exchangeClient exchangepb.ExchangeServiceClient
	// optional fund-detail deps (Celina-4 §Detaljan prikaz fonda holdings list)
	fundHoldings *repository.FundHoldingRepository
	listings     *repository.ListingRepository
	stocks       *repository.StockRepository
	// E4 dividend service
	dividendSvc *service.DividendService
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

// WithActuaryDeps wires the repositories and clients needed by the actuary
// performance read. Call after constructing the handler. Without these
// dependencies GetActuaryPerformance returns an empty list.
func (h *InvestmentFundHandler) WithActuaryDeps(
	capitalGains *repository.CapitalGainRepository,
	userClient userpb.UserServiceClient,
	exchangeClient exchangepb.ExchangeServiceClient,
) *InvestmentFundHandler {
	cp := *h
	cp.capitalGains = capitalGains
	cp.userClient = userClient
	cp.exchangeClient = exchangeClient
	return &cp
}

// WithFundDetailDeps wires the repos used to populate the holdings list
// in GetFund (per Celina-4 §Detaljan prikaz fonda — "Lista hartija:
// Ticker, Price, Change, Volume, initialMarginCost, acquisitionDate").
func (h *InvestmentFundHandler) WithFundDetailDeps(
	fundHoldings *repository.FundHoldingRepository,
	listings *repository.ListingRepository,
	stocks *repository.StockRepository,
) *InvestmentFundHandler {
	cp := *h
	cp.fundHoldings = fundHoldings
	cp.listings = listings
	cp.stocks = stocks
	return &cp
}

// WithDividendService wires the DividendService so the E4 dividend RPCs
// (DeclareDividend, PayoutDividend, ListMyDividends, ListFundDividends) work.
func (h *InvestmentFundHandler) WithDividendService(svc *service.DividendService) *InvestmentFundHandler {
	cp := *h
	cp.dividendSvc = svc
	return &cp
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
	resp := &stockpb.FundDetailResponse{Fund: toFundResponse(f)}

	// E1: compute fund statistics (investor count, balances, P&L).
	stat, _ := h.fundSvc.Statistics(ctx, f)
	resp.InvestorCount = stat.InvestorCount
	resp.TotalContributedRsd = stat.TotalContributedRSD.StringFixed(2)
	resp.LiquidRsdBalance = stat.LiquidRSDBal.StringFixed(2)
	resp.TotalHoldingsValueRsd = stat.TotalHoldingsValueRSD.StringFixed(2)
	resp.TotalValueRsd = stat.TotalValueRSD.StringFixed(2)
	resp.TotalDividendsPaidRsd = stat.TotalDividendsPaidRSD.StringFixed(2)
	resp.ProfitRsd = stat.ProfitRSD.StringFixed(2)
	resp.ProfitPct = stat.ProfitPct.StringFixed(4)

	// Holdings list: use service snapshot (includes current_value_rsd per item).
	if h.fundHoldings != nil {
		holdings, hErr := h.fundHoldings.ListByFundFIFO(f.ID)
		if hErr == nil {
			resp.Holdings = make([]*stockpb.FundHoldingItem, 0, len(holdings))
			for i := range holdings {
				h2 := &holdings[i]
				item := &stockpb.FundHoldingItem{
					SecurityType:    h2.SecurityType,
					SecurityId:      h2.SecurityID,
					Quantity:        h2.Quantity,
					AveragePriceRsd: h2.AveragePriceRSD.String(),
					AcquiredAt:      h2.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
				}
				if h.listings != nil {
					if listing, lerr := h.listings.GetBySecurityIDAndType(h2.SecurityID, h2.SecurityType); lerr == nil && listing != nil {
						item.CurrentPriceRsd = listing.Price.String()
						// current_value_rsd = quantity × current_price (E1 new field)
						curVal := listing.Price.Mul(decimal.NewFromInt(h2.Quantity))
						item.CurrentValueRsd = curVal.String()
					}
				}
				if h.stocks != nil && h2.SecurityType == "stock" {
					if stock, serr := h.stocks.GetByID(h2.SecurityID); serr == nil && stock != nil {
						item.Ticker = stock.Ticker
					}
				}
				resp.Holdings = append(resp.Holdings, item)
			}
		}
	}
	return resp, nil
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

// ListMyPositions returns the caller's contributions across active funds,
// enriched with derived current value / profit / percentage when the
// position-reads dependencies are wired (listingRepo + holdings + exchange).
func (h *InvestmentFundHandler) ListMyPositions(ctx context.Context, in *stockpb.ListMyPositionsRequest) (*stockpb.ListPositionsResponse, error) {
	ownerType, ownerID := model.OwnerFromLegacy(in.ActorUserId, in.ActorSystemType)
	rows, err := h.fundSvc.ListMyPositionsDTO(ctx, ownerType, ownerID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &stockpb.ListPositionsResponse{Positions: positionDTOsToProto(rows)}, nil
}

// ListBankPositions returns positions where the bank itself is the owner.
func (h *InvestmentFundHandler) ListBankPositions(ctx context.Context, _ *stockpb.ListBankPositionsRequest) (*stockpb.ListPositionsResponse, error) {
	rows, err := h.fundSvc.ListBankPositionsDTO(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &stockpb.ListPositionsResponse{Positions: positionDTOsToProto(rows)}, nil
}

func positionDTOsToProto(rows []service.PositionDTO) []*stockpb.PositionItem {
	out := make([]*stockpb.PositionItem, 0, len(rows))
	for _, p := range rows {
		out = append(out, &stockpb.PositionItem{
			FundId:          p.FundID,
			FundName:        p.FundName,
			ManagerFullName: p.ManagerFullName,
			ContributionRsd: p.ContributionRSD.String(),
			PercentageFund:  p.PercentageFund.String(),
			CurrentValueRsd: p.CurrentValueRSD.String(),
			ProfitRsd:       p.ProfitRSD.String(),
			LastChangedAt:   p.LastChangedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out
}

// GetActuaryPerformance sums realised capital gains per acting employee,
// converts non-RSD gains to RSD via exchange-service, and decorates the
// result with full names from user-service. Returns empty when the actuary
// dependencies (capitalGains / userClient / exchangeClient) are not wired.
func (h *InvestmentFundHandler) GetActuaryPerformance(ctx context.Context, _ *stockpb.GetActuaryPerformanceRequest) (*stockpb.GetActuaryPerformanceResponse, error) {
	if h.capitalGains == nil {
		return &stockpb.GetActuaryPerformanceResponse{Actuaries: nil}, nil
	}
	rows, err := h.capitalGains.SumByActingEmployee()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	totals := make(map[int64]decimal.Decimal, len(rows))
	for _, r := range rows {
		amt := r.TotalGain
		if r.Currency != "RSD" && h.exchangeClient != nil {
			conv, err := h.exchangeClient.Convert(ctx, &exchangepb.ConvertRequest{
				FromCurrency: r.Currency,
				ToCurrency:   "RSD",
				Amount:       amt.String(),
			})
			if err == nil {
				if d, err := decimal.NewFromString(conv.ConvertedAmount); err == nil {
					amt = d
				}
			}
		}
		totals[r.EmployeeID] = totals[r.EmployeeID].Add(amt)
	}

	names := map[int64]string{}
	if h.userClient != nil && len(totals) > 0 {
		ids := make([]int64, 0, len(totals))
		for id := range totals {
			ids = append(ids, id)
		}
		resp, err := h.userClient.ListEmployeeFullNames(ctx, &userpb.ListEmployeeFullNamesRequest{EmployeeIds: ids})
		if err == nil && resp != nil {
			names = resp.NamesById
		}
	}

	out := &stockpb.GetActuaryPerformanceResponse{Actuaries: make([]*stockpb.ActuaryPerformance, 0, len(totals))}
	for id, total := range totals {
		out.Actuaries = append(out.Actuaries, &stockpb.ActuaryPerformance{
			EmployeeId:        id,
			FullName:          names[id],
			Role:              "actuary",
			RealizedProfitRsd: total.String(),
		})
	}
	return out, nil
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

// ── E4 Dividend RPCs ──────────────────────────────────────────────────────────

func (h *InvestmentFundHandler) DeclareDividend(ctx context.Context, in *stockpb.DeclareDividendRequest) (*stockpb.DividendPaymentResponse, error) {
	if h.dividendSvc == nil {
		return nil, status.Error(codes.Unimplemented, "dividend service not wired")
	}
	amtPerShare, err := decimal.NewFromString(in.AmountPerShareRsd)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid amount_per_share_rsd")
	}
	paymentDate, err := time.Parse("2006-01-02", in.PaymentDate)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "payment_date must be ISO date (2006-01-02)")
	}
	dp, err := h.dividendSvc.Declare(ctx, in.SecurityId, in.Ticker, amtPerShare, paymentDate, in.DeclaredByEmployeeId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toDividendPaymentResponse(dp), nil
}

func (h *InvestmentFundHandler) PayoutDividend(ctx context.Context, in *stockpb.PayoutDividendRequest) (*stockpb.PayoutDividendResponse, error) {
	if h.dividendSvc == nil {
		return nil, status.Error(codes.Unimplemented, "dividend service not wired")
	}
	summary, err := h.dividendSvc.Payout(ctx, in.DividendPaymentId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &stockpb.PayoutDividendResponse{
		PayoutsCreated: int32(summary.PayoutsCreated),
		FundPayouts:    int32(summary.FundPayouts),
		TotalAmountRsd: summary.TotalAmountRSD.StringFixed(2),
	}, nil
}

func (h *InvestmentFundHandler) ListMyDividends(ctx context.Context, in *stockpb.ListMyDividendsRequest) (*stockpb.ListDividendPayoutsResponse, error) {
	if h.dividendSvc == nil {
		return &stockpb.ListDividendPayoutsResponse{}, nil
	}
	page, pageSize := int(in.Page), int(in.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	var ownerID *uint64
	if in.OwnerId != 0 {
		v := in.OwnerId
		ownerID = &v
	}
	rows, total, err := h.dividendSvc.ListMyDividends(in.OwnerType, ownerID, page, pageSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListDividendPayoutsResponse{Total: total}
	for _, r := range rows {
		item := &stockpb.DividendPayoutItem{
			Id:                r.ID,
			DividendPaymentId: r.DividendPaymentID,
			HoldingOwnerType:  r.HoldingOwnerType,
			HoldingId:         r.HoldingID,
			Shares:            r.Shares,
			GrossAmountRsd:    r.GrossAmountRSD.StringFixed(2),
			TaxAmountRsd:      r.TaxAmountRSD.StringFixed(2),
			NetAmountRsd:      r.NetAmountRSD.StringFixed(2),
			CreditedAccountId: r.CreditedAccountID,
			CreatedAt:         r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.HoldingOwnerID != nil {
			item.HoldingOwnerId = *r.HoldingOwnerID
		}
		out.Payouts = append(out.Payouts, item)
	}
	return out, nil
}

func (h *InvestmentFundHandler) ListFundDividends(ctx context.Context, in *stockpb.ListFundDividendsRequest) (*stockpb.ListFundDividendPaymentsResponse, error) {
	if h.dividendSvc == nil {
		return &stockpb.ListFundDividendPaymentsResponse{}, nil
	}
	page, pageSize := int(in.Page), int(in.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	rows, total, err := h.dividendSvc.ListFundDividends(in.FundId, page, pageSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListFundDividendPaymentsResponse{Total: total}
	for _, r := range rows {
		out.Payments = append(out.Payments, &stockpb.FundDividendPaymentItem{
			Id:                  r.ID,
			DividendPaymentId:   r.DividendPaymentID,
			FundId:              r.FundID,
			AmountRsd:           r.AmountRSD.StringFixed(2),
			PerInvestorSnapshot: r.PerInvestorSnapshot,
			CreatedAt:           r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func toDividendPaymentResponse(dp *model.DividendPayment) *stockpb.DividendPaymentResponse {
	r := &stockpb.DividendPaymentResponse{
		Id:                   dp.ID,
		SecurityId:           dp.SecurityID,
		Ticker:               dp.Ticker,
		AmountPerShareRsd:    dp.AmountPerShareRSD.StringFixed(4),
		PaymentDate:          dp.PaymentDate.Format("2006-01-02"),
		Status:               dp.Status,
		DeclaredByEmployeeId: dp.DeclaredByEmployeeID,
		CreatedAt:            dp.CreatedAt.UTC().Format(time.RFC3339),
	}
	if dp.PaidOutAt != nil {
		r.PaidOutAt = dp.PaidOutAt.UTC().Format(time.RFC3339)
	}
	return r
}

// mapFundErr is now a passthrough. Service-layer sentinels carry their own
// gRPC code via svcerr.SentinelError (see internal/service/errors.go).
// repository.ErrFundNameInUse and service.ErrInsufficientFundCash are also
// typed sentinels. The bare gorm.ErrRecordNotFound branch remains because
// some legacy paths still surface the raw GORM error.
func mapFundErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	return err
}
