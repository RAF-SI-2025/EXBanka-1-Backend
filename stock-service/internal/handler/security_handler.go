package handler

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/shopspring/decimal"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// SecurityListingRepo is the subset of the listing repository that the
// security handler needs to resolve listing IDs for securities. Exposed as an
// interface so tests can supply a fake without constructing a full repo.
type SecurityListingRepo interface {
	GetBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error)
	ListBySecurityIDsAndType(securityIDs []uint64, securityType string) ([]model.Listing, error)
}

type SecurityHandler struct {
	pb.UnimplementedSecurityGRPCServiceServer
	secSvc      *service.SecurityService
	listingSvc  *service.ListingService
	candleSvc   *service.CandleService
	listingRepo SecurityListingRepo
}

func NewSecurityHandler(secSvc *service.SecurityService, listingSvc *service.ListingService, candleSvc *service.CandleService, listingRepo SecurityListingRepo) *SecurityHandler {
	return &SecurityHandler{secSvc: secSvc, listingSvc: listingSvc, candleSvc: candleSvc, listingRepo: listingRepo}
}

// resolveListingIDs batch-resolves listing IDs for a slice of securities of the
// given type. Returns map[securityID]listingID; any security without a listing
// maps to 0 (logged as a warning). Used by list handlers to populate
// ListingInfo.Id on every item with a single DB query.
func (h *SecurityHandler) resolveListingIDs(securityIDs []uint64, securityType string) map[uint64]uint64 {
	out := make(map[uint64]uint64, len(securityIDs))
	if len(securityIDs) == 0 || h.listingRepo == nil {
		return out
	}
	listings, err := h.listingRepo.ListBySecurityIDsAndType(securityIDs, securityType)
	if err != nil {
		log.Printf("WARN: batch listing lookup failed for %s: %v", securityType, err)
		return out
	}
	for _, l := range listings {
		out[l.SecurityID] = l.ID
	}
	return out
}

// resolveListingID resolves a single listing ID for a security. Returns 0 and
// logs a warning if lookup fails. Used by single-item (Get*) handlers.
func (h *SecurityHandler) resolveListingID(securityID uint64, securityType string) uint64 {
	if h.listingRepo == nil {
		return 0
	}
	listing, err := h.listingRepo.GetBySecurityIDAndType(securityID, securityType)
	if err != nil {
		log.Printf("WARN: listing not found for %s %d: %v", securityType, securityID, err)
		return 0
	}
	return listing.ID
}

// --- Stocks ---

func (h *SecurityHandler) ListStocks(ctx context.Context, req *pb.ListStocksRequest) (*pb.ListStocksResponse, error) {
	filter := repository.StockFilter{
		Search:          req.Search,
		ExchangeAcronym: req.ExchangeAcronym,
		SortBy:          req.SortBy,
		SortOrder:       req.SortOrder,
		Page:            int(req.Page),
		PageSize:        int(req.PageSize),
	}
	if req.MinPrice != "" {
		v, err := decimal.NewFromString(req.MinPrice)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid min_price")
		}
		filter.MinPrice = &v
	}
	if req.MaxPrice != "" {
		v, err := decimal.NewFromString(req.MaxPrice)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid max_price")
		}
		filter.MaxPrice = &v
	}
	if req.MinVolume > 0 {
		v := req.MinVolume
		filter.MinVolume = &v
	}
	if req.MaxVolume > 0 {
		v := req.MaxVolume
		filter.MaxVolume = &v
	}

	stocks, total, err := h.secSvc.ListStocks(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ids := make([]uint64, len(stocks))
	for i, s := range stocks {
		ids[i] = s.ID
	}
	listingMap := h.resolveListingIDs(ids, "stock")

	items := make([]*pb.StockItem, len(stocks))
	for i, s := range stocks {
		stock := s
		items[i] = toStockItem(&stock, listingMap[s.ID])
	}
	return &pb.ListStocksResponse{Stocks: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetStock(ctx context.Context, req *pb.GetStockRequest) (*pb.StockDetail, error) {
	stock, options, err := h.secSvc.GetStockWithOptions(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	lid := h.resolveListingID(stock.ID, "stock")
	return toStockDetail(stock, options, lid), nil
}

func (h *SecurityHandler) GetStockHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "stock", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toPriceHistoryResponse(history, total), nil
}

// --- Futures ---

func (h *SecurityHandler) ListFutures(ctx context.Context, req *pb.ListFuturesRequest) (*pb.ListFuturesResponse, error) {
	filter := repository.FuturesFilter{
		Search:          req.Search,
		ExchangeAcronym: req.ExchangeAcronym,
		SortBy:          req.SortBy,
		SortOrder:       req.SortOrder,
		Page:            int(req.Page),
		PageSize:        int(req.PageSize),
	}
	if req.MinPrice != "" {
		v, _ := decimal.NewFromString(req.MinPrice)
		filter.MinPrice = &v
	}
	if req.MaxPrice != "" {
		v, _ := decimal.NewFromString(req.MaxPrice)
		filter.MaxPrice = &v
	}
	if req.MinVolume > 0 {
		v := req.MinVolume
		filter.MinVolume = &v
	}
	if req.MaxVolume > 0 {
		v := req.MaxVolume
		filter.MaxVolume = &v
	}
	if req.SettlementDateFrom != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDateFrom)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date_from")
		}
		filter.SettlementDateFrom = &t
	}
	if req.SettlementDateTo != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDateTo)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date_to")
		}
		filter.SettlementDateTo = &t
	}

	futures, total, err := h.secSvc.ListFutures(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ids := make([]uint64, len(futures))
	for i, f := range futures {
		ids[i] = f.ID
	}
	listingMap := h.resolveListingIDs(ids, "futures")

	items := make([]*pb.FuturesItem, len(futures))
	for i, f := range futures {
		fc := f
		items[i] = toFuturesItem(&fc, listingMap[f.ID])
	}
	return &pb.ListFuturesResponse{Futures: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetFutures(ctx context.Context, req *pb.GetFuturesRequest) (*pb.FuturesDetail, error) {
	f, err := h.secSvc.GetFutures(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	lid := h.resolveListingID(f.ID, "futures")
	return toFuturesDetail(f, lid), nil
}

func (h *SecurityHandler) GetFuturesHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "futures", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toPriceHistoryResponse(history, total), nil
}

// --- Forex ---

func (h *SecurityHandler) ListForexPairs(ctx context.Context, req *pb.ListForexPairsRequest) (*pb.ListForexPairsResponse, error) {
	filter := repository.ForexFilter{
		Search:        req.Search,
		BaseCurrency:  req.BaseCurrency,
		QuoteCurrency: req.QuoteCurrency,
		Liquidity:     req.Liquidity,
		SortBy:        req.SortBy,
		SortOrder:     req.SortOrder,
		Page:          int(req.Page),
		PageSize:      int(req.PageSize),
	}

	pairs, total, err := h.secSvc.ListForexPairs(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	ids := make([]uint64, len(pairs))
	for i, fp := range pairs {
		ids[i] = fp.ID
	}
	listingMap := h.resolveListingIDs(ids, "forex")

	items := make([]*pb.ForexPairItem, len(pairs))
	for i, fp := range pairs {
		pair := fp
		items[i] = toForexPairItem(&pair, listingMap[fp.ID])
	}
	return &pb.ListForexPairsResponse{ForexPairs: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetForexPair(ctx context.Context, req *pb.GetForexPairRequest) (*pb.ForexPairDetail, error) {
	fp, err := h.secSvc.GetForexPair(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	lid := h.resolveListingID(fp.ID, "forex")
	return toForexPairDetail(fp, lid), nil
}

func (h *SecurityHandler) GetForexPairHistory(ctx context.Context, req *pb.GetPriceHistoryRequest) (*pb.PriceHistoryResponse, error) {
	history, total, err := h.listingSvc.GetPriceHistoryForSecurity(req.Id, "forex", req.Period, int(req.Page), int(req.PageSize))
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toPriceHistoryResponse(history, total), nil
}

// --- Options ---

func (h *SecurityHandler) ListOptions(ctx context.Context, req *pb.ListOptionsRequest) (*pb.ListOptionsResponse, error) {
	filter := repository.OptionFilter{
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}
	if req.StockId > 0 {
		v := req.StockId
		filter.StockID = &v
	}
	if req.OptionType != "" {
		filter.OptionType = req.OptionType
	}
	if req.SettlementDate != "" {
		t, err := time.Parse("2006-01-02", req.SettlementDate)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid settlement_date")
		}
		filter.SettlementDate = &t
	}
	if req.MinStrike != "" {
		v, _ := decimal.NewFromString(req.MinStrike)
		filter.MinStrike = &v
	}
	if req.MaxStrike != "" {
		v, _ := decimal.NewFromString(req.MaxStrike)
		filter.MaxStrike = &v
	}

	options, total, err := h.secSvc.ListOptions(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	items := make([]*pb.OptionItem, len(options))
	for i, o := range options {
		items[i] = toOptionItem(&o)
	}
	return &pb.ListOptionsResponse{Options: items, TotalCount: total}, nil
}

func (h *SecurityHandler) GetOption(ctx context.Context, req *pb.GetOptionRequest) (*pb.OptionDetail, error) {
	o, err := h.secSvc.GetOption(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return toOptionDetail(o), nil
}

// --- Candles ---

func (h *SecurityHandler) GetCandles(ctx context.Context, req *pb.GetCandlesRequest) (*pb.GetCandlesResponse, error) {
	if req.ListingId == 0 {
		return nil, status.Error(codes.InvalidArgument, "listing_id is required")
	}
	if req.Interval == "" {
		return nil, status.Error(codes.InvalidArgument, "interval is required")
	}

	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid from timestamp: use RFC3339 format")
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid to timestamp: use RFC3339 format")
	}

	candles, err := h.candleSvc.GetCandles(ctx, req.ListingId, req.Interval, from, to)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	pbCandles := make([]*pb.CandlePoint, len(candles))
	for i, cp := range candles {
		pbCandles[i] = &pb.CandlePoint{
			Timestamp: cp.Timestamp.Format(time.RFC3339),
			Open:      fmt.Sprintf("%.4f", cp.Open),
			High:      fmt.Sprintf("%.4f", cp.High),
			Low:       fmt.Sprintf("%.4f", cp.Low),
			Close:     fmt.Sprintf("%.4f", cp.Close),
			Volume:    cp.Volume,
		}
	}

	return &pb.GetCandlesResponse{
		Candles: pbCandles,
		Count:   int64(len(pbCandles)),
	}, nil
}

// --- Mapping helpers ---

func toListingInfo(
	listingID uint64,
	exchangeID uint64,
	exchangeAcronym string,
	price, high, low, change decimal.Decimal,
	volume int64,
	initialMarginCost decimal.Decimal,
	lastRefresh time.Time,
) *pb.ListingInfo {
	changePercent := service.StockChangePercent(price, change)
	return &pb.ListingInfo{
		Id:                listingID,
		ExchangeId:        exchangeID,
		ExchangeAcronym:   exchangeAcronym,
		Price:             price.StringFixed(4),
		High:              high.StringFixed(4),
		Low:               low.StringFixed(4),
		Change:            change.StringFixed(4),
		ChangePercent:     changePercent.StringFixed(2),
		Volume:            volume,
		InitialMarginCost: initialMarginCost.StringFixed(2),
		LastRefresh:       lastRefresh.Format(time.RFC3339),
	}
}

func toStockItem(s *model.Stock, listingID uint64) *pb.StockItem {
	return &pb.StockItem{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		Listing: toListingInfo(
			listingID,
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
	}
}

func toStockDetail(s *model.Stock, options []model.Option, listingID uint64) *pb.StockDetail {
	optItems := make([]*pb.OptionItem, len(options))
	for i, o := range options {
		optItems[i] = toOptionItem(&o)
	}
	return &pb.StockDetail{
		Id:                s.ID,
		Ticker:            s.Ticker,
		Name:              s.Name,
		OutstandingShares: s.OutstandingShares,
		DividendYield:     s.DividendYield.StringFixed(6),
		MarketCap:         s.MarketCap().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			s.ExchangeID, s.Exchange.Acronym,
			s.Price, s.High, s.Low, s.Change, s.Volume,
			s.InitialMarginCost(), s.LastRefresh,
		),
		Options: optItems,
	}
}

func toFuturesItem(f *model.FuturesContract, listingID uint64) *pb.FuturesItem {
	return &pb.FuturesItem{
		Id:             f.ID,
		Ticker:         f.Ticker,
		Name:           f.Name,
		ContractSize:   f.ContractSize,
		ContractUnit:   f.ContractUnit,
		SettlementDate: f.SettlementDate.Format("2006-01-02"),
		Listing: toListingInfo(
			listingID,
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toFuturesDetail(f *model.FuturesContract, listingID uint64) *pb.FuturesDetail {
	return &pb.FuturesDetail{
		Id:                f.ID,
		Ticker:            f.Ticker,
		Name:              f.Name,
		ContractSize:      f.ContractSize,
		ContractUnit:      f.ContractUnit,
		SettlementDate:    f.SettlementDate.Format("2006-01-02"),
		MaintenanceMargin: f.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			f.ExchangeID, f.Exchange.Acronym,
			f.Price, f.High, f.Low, f.Change, f.Volume,
			f.InitialMarginCost(), f.LastRefresh,
		),
	}
}

func toForexPairItem(fp *model.ForexPair, listingID uint64) *pb.ForexPairItem {
	return &pb.ForexPairItem{
		Id:            fp.ID,
		Ticker:        fp.Ticker,
		Name:          fp.Name,
		BaseCurrency:  fp.BaseCurrency,
		QuoteCurrency: fp.QuoteCurrency,
		ExchangeRate:  fp.ExchangeRate.StringFixed(8),
		Liquidity:     fp.Liquidity,
		ContractSize:  fp.ContractSizeValue(),
		Listing: toListingInfo(
			listingID,
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}

func toForexPairDetail(fp *model.ForexPair, listingID uint64) *pb.ForexPairDetail {
	return &pb.ForexPairDetail{
		Id:                fp.ID,
		Ticker:            fp.Ticker,
		Name:              fp.Name,
		BaseCurrency:      fp.BaseCurrency,
		QuoteCurrency:     fp.QuoteCurrency,
		ExchangeRate:      fp.ExchangeRate.StringFixed(8),
		Liquidity:         fp.Liquidity,
		ContractSize:      fp.ContractSizeValue(),
		MaintenanceMargin: fp.MaintenanceMargin().StringFixed(2),
		Listing: toListingInfo(
			listingID,
			fp.ExchangeID, fp.Exchange.Acronym,
			fp.ExchangeRate, fp.High, fp.Low, fp.Change, fp.Volume,
			fp.InitialMarginCost(), fp.LastRefresh,
		),
	}
}

func toOptionItem(o *model.Option) *pb.OptionItem {
	stockPrice := decimal.Zero
	if o.Stock.ID > 0 {
		stockPrice = o.Stock.Price
	}
	item := &pb.OptionItem{
		Id:                o.ID,
		Ticker:            o.Ticker,
		Name:              o.Name,
		StockTicker:       o.Stock.Ticker,
		StockListingId:    o.StockID,
		OptionType:        o.OptionType,
		StrikePrice:       o.StrikePrice.StringFixed(4),
		ImpliedVolatility: o.ImpliedVolatility.StringFixed(6),
		Premium:           o.Premium.StringFixed(2),
		OpenInterest:      o.OpenInterest,
		SettlementDate:    o.SettlementDate.Format("2006-01-02"),
		ContractSize:      o.ContractSizeValue(),
		InitialMarginCost: o.InitialMarginCost(stockPrice).StringFixed(2),
	}
	if o.ListingID != nil {
		lid := *o.ListingID
		item.ListingId = &lid
	}
	return item
}

func toPriceHistoryResponse(history []model.ListingDailyPriceInfo, total int64) *pb.PriceHistoryResponse {
	entries := make([]*pb.PriceHistoryEntry, len(history))
	for i, h := range history {
		entries[i] = &pb.PriceHistoryEntry{
			Date:   h.Date.Format("2006-01-02"),
			Price:  h.Price.StringFixed(4),
			High:   h.High.StringFixed(4),
			Low:    h.Low.StringFixed(4),
			Change: h.Change.StringFixed(4),
			Volume: h.Volume,
		}
	}
	return &pb.PriceHistoryResponse{History: entries, TotalCount: total}
}

func toOptionDetail(o *model.Option) *pb.OptionDetail {
	stockPrice := decimal.Zero
	if o.Stock.ID > 0 {
		stockPrice = o.Stock.Price
	}
	detail := &pb.OptionDetail{
		Id:                o.ID,
		Ticker:            o.Ticker,
		Name:              o.Name,
		StockTicker:       o.Stock.Ticker,
		StockListingId:    o.StockID,
		OptionType:        o.OptionType,
		StrikePrice:       o.StrikePrice.StringFixed(4),
		ImpliedVolatility: o.ImpliedVolatility.StringFixed(6),
		Premium:           o.Premium.StringFixed(2),
		OpenInterest:      o.OpenInterest,
		SettlementDate:    o.SettlementDate.Format("2006-01-02"),
		ContractSize:      o.ContractSizeValue(),
		MaintenanceMargin: o.MaintenanceMargin(stockPrice).StringFixed(2),
		InitialMarginCost: o.InitialMarginCost(stockPrice).StringFixed(2),
	}
	if o.ListingID != nil {
		lid := *o.ListingID
		detail.ListingId = &lid
	}
	return detail
}
