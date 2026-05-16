package handler

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/otccache"
	"github.com/exbanka/stock-service/internal/service"
)

// otcSvcFacade is the narrow interface of OTCService used by OTCHandler.
type otcSvcFacade interface {
	ListOffers(filter service.OTCFilter) ([]model.Holding, int64, error)
	BuyOffer(offerID, buyerID uint64, buyerSystemType string, quantity int64, buyerAccountID uint64) (*service.OTCBuyResult, error)
}

type OTCHandler struct {
	pb.UnimplementedOTCGRPCServiceServer
	otcSvc      otcSvcFacade
	cache       *otccache.Cache
	optionCache *otccache.OptionCache // optional; Phase 6 cross-bank option discovery
}

// NewOTCHandler keeps the prior signature for backwards compatibility
// with cmd/main.go callers that don't yet pass a cache. Pass a nil
// cache to disable the unified-offers RPC (it will return an empty
// list with peers_total=0).
func NewOTCHandler(otcSvc *service.OTCService) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}

// NewOTCHandlerWithCache wires the unified-offers cache. cmd/main.go
// uses this once the cache + refresher are constructed.
func NewOTCHandlerWithCache(otcSvc *service.OTCService, cache *otccache.Cache) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc, cache: cache}
}

// WithOptionCache wires the Phase-6 cross-bank option-discovery cache
// (parallel to the stocks-marketplace cache). Returns a copy so cmd/
// main.go can chain wire-up calls.
func (h *OTCHandler) WithOptionCache(c *otccache.OptionCache) *OTCHandler {
	cp := *h
	cp.optionCache = c
	return &cp
}

func newOTCHandlerForTest(otcSvc otcSvcFacade) *OTCHandler {
	return &OTCHandler{otcSvc: otcSvc}
}

func (h *OTCHandler) ListOffers(ctx context.Context, req *pb.ListOTCOffersRequest) (*pb.ListOTCOffersResponse, error) {
	filter := service.OTCFilter{
		SecurityType: req.SecurityType,
		Ticker:       req.Ticker,
		Page:         int(req.Page),
		PageSize:     int(req.PageSize),
	}

	holdings, total, err := h.otcSvc.ListOffers(filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	offers := make([]*pb.OTCOffer, len(holdings))
	for i, hld := range holdings {
		offers[i] = &pb.OTCOffer{
			Id:           hld.ID,
			SellerId:     model.OwnerIDOrZero(hld.OwnerID),
			SellerName:   hld.UserFirstName + " " + hld.UserLastName,
			SecurityType: hld.SecurityType,
			Ticker:       hld.Ticker,
			Name:         hld.Name,
			Quantity:     hld.PublicQuantity,
			PricePerUnit: hld.AveragePrice.StringFixed(2),
			CreatedAt:    hld.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return &pb.ListOTCOffersResponse{
		Offers:     offers,
		TotalCount: total,
	}, nil
}

func (h *OTCHandler) BuyOffer(ctx context.Context, req *pb.BuyOTCOfferRequest) (*pb.OTCTransaction, error) {
	// Both the buyer ID and system_type must flip to the client when an
	// employee acts on behalf, so the resulting holding lands in the
	// client's portfolio. Same rule as order placement; see resolveOrderOwner.
	effectiveBuyerID, effectiveSystemType, rerr := resolveOrderOwner(req.BuyerId, req.SystemType, req.ActingEmployeeId, req.OnBehalfOfClientId)
	if rerr != nil {
		return nil, rerr
	}

	result, err := h.otcSvc.BuyOffer(
		req.OfferId,
		effectiveBuyerID,
		effectiveSystemType,
		req.Quantity,
		req.AccountId,
	)
	if err != nil {
		return nil, mapOTCError(err)
	}

	return &pb.OTCTransaction{
		Id:           result.ID,
		OfferId:      result.OfferID,
		Quantity:     result.Quantity,
		PricePerUnit: result.PricePerUnit.StringFixed(2),
		TotalPrice:   result.TotalPrice.StringFixed(2),
		Commission:   result.Commission.StringFixed(2),
	}, nil
}

// mapOTCError is now a passthrough. Service-layer sentinels carry their own
// gRPC code via svcerr.SentinelError. See internal/service/errors.go for the
// OTC sentinel set.
func mapOTCError(err error) error {
	return err
}

// ListUnifiedOffers serves the cached union of local + cross-bank OTC
// offers. Filters and pagination are applied in-memory over the cached
// snapshot. peers_total / peers_reached / partial reflect the most
// recent refresh cycle so the UI can surface "showing 1 of 2 peers".
func (h *OTCHandler) ListUnifiedOffers(ctx context.Context, req *pb.ListUnifiedOTCOffersRequest) (*pb.ListUnifiedOTCOffersResponse, error) {
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 {
		pageSize = 10
	}
	secType := req.GetSecurityType()
	if secType != "" && secType != "stock" && secType != "futures" {
		return nil, status.Error(codes.InvalidArgument, "security_type must be 'stock' or 'futures'")
	}
	kind := req.GetKind()
	if kind != "" && kind != "local" && kind != "remote" {
		return nil, status.Error(codes.InvalidArgument, "kind must be 'local' or 'remote'")
	}
	ticker := strings.ToUpper(req.GetTicker())
	bankFilter := req.GetBankCode()

	if h.cache == nil {
		return &pb.ListUnifiedOTCOffersResponse{}, nil
	}
	snap := h.cache.Get()

	filtered := make([]otccache.Offer, 0, len(snap.Offers))
	for _, o := range snap.Offers {
		if secType != "" && o.SecurityType != secType {
			continue
		}
		if ticker != "" && strings.ToUpper(o.Ticker) != ticker {
			continue
		}
		if kind != "" && o.Kind != kind {
			continue
		}
		if bankFilter != "" && o.BankCode != bankFilter {
			continue
		}
		filtered = append(filtered, o)
	}

	total := int64(len(filtered))
	start := (page - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}

	out := make([]*pb.UnifiedOTCOffer, 0, end-start)
	for _, o := range filtered[start:end] {
		out = append(out, &pb.UnifiedOTCOffer{
			Kind:         o.Kind,
			BankCode:     o.BankCode,
			Id:           o.ID,
			SellerId:     o.SellerID,
			SellerName:   o.SellerName,
			Name:         o.Name,
			CreatedAt:    o.CreatedAt,
			OwnerId:      o.OwnerID,
			SecurityType: o.SecurityType,
			Ticker:       o.Ticker,
			Quantity:     o.Quantity,
			PricePerUnit: o.PricePerUnit,
			Currency:     o.Currency,
		})
	}

	var lastRefreshUnix int64
	if !snap.LastRefresh.IsZero() {
		lastRefreshUnix = snap.LastRefresh.Unix()
	}
	// Suppress unused-var lint when ctx isn't used; ctx is reserved for
	// future cancellation in case we add per-request peer fan-out.
	_ = ctx
	return &pb.ListUnifiedOTCOffersResponse{
		Offers:          out,
		TotalCount:      total,
		PeersTotal:      int32(snap.PeersTotal),
		PeersReached:    int32(snap.PeersReached),
		Partial:         snap.PeersTotal > 0 && snap.PeersReached < snap.PeersTotal,
		LastRefreshUnix: lastRefreshUnix,
	}, nil
}

// _ explicit static check that OTCHandler still implements every
// generated server interface method (caught at compile time).
var _ pb.OTCGRPCServiceServer = (*OTCHandler)(nil)

// ListUnifiedOptionOffers serves the Phase-6 cross-bank discovery view
// of open OTC OPTION listings. Backed by OptionCache (refreshed every
// ~5 s by OptionRefresher). Filters by ticker, kind (local|remote),
// bank_code, and direction (sell_initiated|buy_initiated); paginates
// in-memory over the cached snapshot. partial=true reflects the most
// recent refresh missing one or more peers.
func (h *OTCHandler) ListUnifiedOptionOffers(ctx context.Context, req *pb.ListUnifiedOptionOffersRequest) (*pb.ListUnifiedOptionOffersResponse, error) {
	page := int(req.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(req.GetPageSize())
	if pageSize < 1 {
		pageSize = 10
	}
	kind := req.GetKind()
	if kind != "" && kind != "local" && kind != "remote" {
		return nil, status.Error(codes.InvalidArgument, "kind must be 'local' or 'remote'")
	}
	direction := req.GetDirection()
	if direction != "" && direction != "sell_initiated" && direction != "buy_initiated" {
		return nil, status.Error(codes.InvalidArgument, "direction must be 'sell_initiated' or 'buy_initiated'")
	}
	if h.optionCache == nil {
		return &pb.ListUnifiedOptionOffersResponse{}, nil
	}
	snap := h.optionCache.Get()
	ticker := strings.ToUpper(req.GetTicker())
	bankFilter := req.GetBankCode()

	filtered := make([]otccache.OptionOffer, 0, len(snap.Offers))
	for _, o := range snap.Offers {
		if ticker != "" && strings.ToUpper(o.Ticker) != ticker {
			continue
		}
		if kind != "" && o.Kind != kind {
			continue
		}
		if bankFilter != "" && o.BankCode != bankFilter {
			continue
		}
		if direction != "" && o.Direction != direction {
			continue
		}
		filtered = append(filtered, o)
	}
	total := int64(len(filtered))
	start := (page - 1) * pageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	out := make([]*pb.UnifiedOptionOffer, 0, end-start)
	for _, o := range filtered[start:end] {
		out = append(out, &pb.UnifiedOptionOffer{
			Kind:            o.Kind,
			BankCode:        o.BankCode,
			RoutingNumber:   o.RoutingNumber,
			OfferId:         o.OfferID,
			SellerId:        o.SellerID,
			SellerName:      o.SellerName,
			Direction:       o.Direction,
			Ticker:          o.Ticker,
			Amount:          o.Amount,
			StrikePrice:     o.StrikePrice,
			StrikeCurrency:  o.StrikeCurrency,
			Premium:         o.Premium,
			PremiumCurrency: o.PremiumCurrency,
			SettlementDate:  o.SettlementDate,
			CreatedAt:       o.CreatedAt,
		})
	}
	var lastRefreshUnix int64
	if !snap.LastRefresh.IsZero() {
		lastRefreshUnix = snap.LastRefresh.Unix()
	}
	_ = ctx
	return &pb.ListUnifiedOptionOffersResponse{
		Offers:          out,
		TotalCount:      total,
		PeersTotal:      int32(snap.PeersTotal),
		PeersReached:    int32(snap.PeersReached),
		Partial:         snap.PeersTotal > 0 && snap.PeersReached < snap.PeersTotal,
		LastRefreshUnix: lastRefreshUnix,
	}, nil
}
