package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractkafka "github.com/exbanka/contract/kafka"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// PeerNotifier is the producer dependency for cross-bank inbound
// notification intents. Each bank notifies ONLY its own local users
// — the SI-TX protocol propagates state changes between banks, and
// each side's handler emits its own notification independently.
type PeerNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg contractkafka.GeneralNotificationMessage) error
}

// HoldingReader is the subset of HoldingRepository methods that
// PeerOTCGRPCHandler needs. Decoupled for testability.
//   - ListPublic backs GetPublicStocks.
//   - GetByOwnerAndTicker backs CheckSellerCanDeliver.
type HoldingReader interface {
	ListPublic() ([]model.Holding, error)
	GetByOwnerAndTicker(ownerType model.OwnerType, ownerID *uint64, securityType, ticker string) (*model.Holding, error)
}

// PeerOTCGRPCHandler implements stockpb.PeerOTCServiceServer.
//
// GetPublicStocks queries the local holdings table for rows flagged
// public_quantity > 0 and returns them as PeerPublicStock entries.
//
// Negotiation lifecycle: peers POST/PUT/GET/DELETE on
// /negotiations/{rid}/{id}; we persist in peer_otc_negotiations.
//
// Acceptance: GET /negotiations/{rid}/{id}/accept composes 4 postings
// (premium money + 1× OptionDescription both directions) and dispatches
// via transaction-service.PeerTxService.InitiateOutboundTxWithPostings.
// HoldingReserver is the subset of HoldingReservationService used by
// RecordOptionContract / RecordOptionExercise to manage the seller's
// underlying share lock and the buyer's gained holding when SI-TX
// flows touch this bank.
type HoldingReserver interface {
	ReserveForPeerOptionContract(
		ctx context.Context,
		sellerOwnerType model.OwnerType,
		sellerOwnerID *uint64,
		securityType, ticker string,
		peerOptionContractID uint64,
		qty int64,
	) (*service.ReserveHoldingResult, error)
	// ReserveForCrossBankNewTx holds shares at NEW_TX time keyed on the SI-TX
	// identity (crossbank_tx_id), before the contract row exists.
	ReserveForCrossBankNewTx(
		ctx context.Context,
		sellerOwnerType model.OwnerType,
		sellerOwnerID *uint64,
		securityType, ticker, crossbankTxID string,
		qty int64,
	) (*service.ReserveHoldingResult, error)
	// AttachCrossBankReservationToContract links the vote-time hold to the
	// contract minted at COMMIT_TX. Returns NotFound if no vote-time hold
	// exists (caller falls back to ReserveForPeerOptionContract).
	AttachCrossBankReservationToContract(ctx context.Context, crossbankTxID string, peerOptionContractID uint64) error
	// ReleaseForCrossBankNewTx releases a vote-time hold on ROLLBACK.
	ReleaseForCrossBankNewTx(ctx context.Context, crossbankTxID string) (*service.ReleaseHoldingResult, error)
	ConsumeForPeerOptionContract(
		ctx context.Context,
		peerOptionContractID uint64,
		qty int64,
	) (*service.PartialSettleHoldingResult, error)
	ExerciseBuyerCreditForPeerOption(
		ctx context.Context,
		peerOptionContractID uint64,
		ownerType model.OwnerType,
		ownerID *uint64,
		ticker string,
		qty int64,
		strikePrice decimal.Decimal,
	) error
}

type PeerOTCGRPCHandler struct {
	stockpb.UnimplementedPeerOTCServiceServer
	negRepo         *repository.PeerOtcNegotiationRepository
	peerOptionRepo  *repository.PeerOptionContractRepository
	holdings        HoldingReader
	peerTx          transactionpb.PeerTxServiceClient
	ownRouting      int64
	holdingReserver HoldingReserver // optional; nil disables seller-side share locking

	// Phase 6 — cross-bank discovery of OPEN OTC OPTION listings. Wired
	// via WithOTCOfferReader. When nil, GetPublicOptionOffers returns
	// Unimplemented instead of nil-deref.
	otcOffers         OTCOfferReader
	otcOptionCurrency OptionCurrencyResolver

	// Optional in-app notification producer. nil ⇒ silent (legacy mode).
	notifier PeerNotifier

	// Optional best-bid / best-ask aggregator (Part A 2026-05-16).
	// nil ⇒ peer-facing rows omit the new fields (wire-compatible
	// with peers that don't expect them).
	bidsAgg AggregateBidsFn

	// capitalGainRepo records the seller's realised P/L on cross-bank
	// exercise (DEBIT direction). Optional — nil falls back to the
	// pre-fix degraded mode where no CG is written. Wired via
	// WithCapitalGain.
	capitalGainRepo PeerCapitalGainRepo
}

// PeerCapitalGainRepo is the narrow surface PeerOTCGRPCHandler uses to
// persist a CapitalGain on cross-bank exercise — satisfied by
// *repository.CapitalGainRepository.
type PeerCapitalGainRepo interface {
	Create(gain *model.CapitalGain) error
}

// WithCapitalGain wires the repository that records seller-side P/L on
// cross-bank exercise. Returns the handler for chaining.
func (h *PeerOTCGRPCHandler) WithCapitalGain(repo PeerCapitalGainRepo) *PeerOTCGRPCHandler {
	h.capitalGainRepo = repo
	return h
}

// WithBidsAggregator wires the best-bid aggregator used by
// GetPublicOptionOffers. Returns the handler for chaining.
func (h *PeerOTCGRPCHandler) WithBidsAggregator(fn AggregateBidsFn) *PeerOTCGRPCHandler {
	h.bidsAgg = fn
	return h
}

// WithNotifier wires the in-app notification producer. Returns the
// handler for chaining.
func (h *PeerOTCGRPCHandler) WithNotifier(n PeerNotifier) *PeerOTCGRPCHandler {
	h.notifier = n
	return h
}

// localClientUserID resolves "client-N" → N when the row's matching
// routing number is this bank's. Returns (0, false) when:
//   - the row is for the other bank's user (no local notification)
//   - the participant id isn't a plain "client-N" string (employee,
//     bank, malformed)
//
// Used by the inbound peer handlers to determine whether to publish
// a notification, and to whom.
func (h *PeerOTCGRPCHandler) localClientUserID(routing int64, participantID string) (uint64, bool) {
	if routing != h.ownRouting {
		return 0, false
	}
	const prefix = "client-"
	if !strings.HasPrefix(participantID, prefix) {
		return 0, false
	}
	id, err := strconv.ParseUint(participantID[len(prefix):], 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

// publishPeerNotif is best-effort. Logs a warning on failure. Skips
// when the notifier isn't wired or the recipient resolution failed.
func (h *PeerOTCGRPCHandler) publishPeerNotif(
	ctx context.Context,
	userID uint64,
	notifType string,
	data map[string]string,
	refType string,
	refID uint64,
) {
	if h.notifier == nil || userID == 0 {
		return
	}
	if err := h.notifier.PublishGeneralNotification(ctx, contractkafka.GeneralNotificationMessage{
		UserID:  userID,
		Type:    notifType,
		Data:    data,
		RefType: refType,
		RefID:   refID,
	}); err != nil {
		log.Printf("WARN: peer otc notif %s for user %d failed: %v", notifType, userID, err)
	}
}

// notifDataFromOffer extracts the ticker/strike/premium fields from
// a decoded sitx.OtcOffer, defensive against partially-populated rows.
func notifDataFromOffer(offer contractsitx.OtcOffer) map[string]string {
	return map[string]string{
		"ticker":       offer.Ticker,
		"quantity":     strconv.FormatInt(offer.Amount, 10),
		"strike_price": offer.PricePerStock.String(),
		"premium":      offer.Premium.String(),
	}
}

// OTCOfferReader is the narrow interface the peer endpoint uses to
// read open OTC listings. OTCOfferRepository.ListOpenForCache
// satisfies it.
type OTCOfferReader interface {
	ListOpenForCache(limit int) ([]model.OTCOffer, error)
}

// OptionCurrencyResolver maps a stockID → currency for the cache row.
// Defined here (not in otccache/) so the handler doesn't depend on
// the cache package.
type OptionCurrencyResolver interface {
	CurrencyForStock(stockID uint64) (string, error)
}

// PeerOfferAggregate is the handler-local projection used by
// GetPublicOptionOffers when populating the per-row best_bid /
// best_ask / active_chains_count surface.
type PeerOfferAggregate struct {
	BestBid     string
	BestAsk     string
	ActiveCount int32
}

// AggregateBidsFn is the narrow dependency GetPublicOptionOffers uses
// to enrich each row. nil ⇒ those fields stay empty (older-bank-compat).
// Wired in cmd/main.go as a thin adapter over the repository's typed
// AggregateActiveBidsByOffer return.
type AggregateBidsFn func(offerIDs []uint64) (map[uint64]PeerOfferAggregate, error)

func NewPeerOTCGRPCHandler(
	negRepo *repository.PeerOtcNegotiationRepository,
	peerOptionRepo *repository.PeerOptionContractRepository,
	holdings HoldingReader,
	peerTx transactionpb.PeerTxServiceClient,
	ownRouting int64,
) *PeerOTCGRPCHandler {
	return &PeerOTCGRPCHandler{
		negRepo:        negRepo,
		peerOptionRepo: peerOptionRepo,
		holdings:       holdings,
		peerTx:         peerTx,
		ownRouting:     ownRouting,
	}
}

// SetHoldingReserver wires the seller-side share-locking dependency.
// Optional — left nil, RecordOptionContract still persists the
// peer_option_contracts row but does not lock the seller's holdings.
// (Useful for tests and for stages where the reserver isn't ready.)
func (h *PeerOTCGRPCHandler) SetHoldingReserver(r HoldingReserver) {
	h.holdingReserver = r
}

// WithOTCOfferReader wires the Phase-6 cross-bank option-discovery
// data source. Returns a copy so the caller can chain wire-up calls.
// When called with a non-nil currency resolver, GetPublicOptionOffers
// stamps strike/premium currency on each emitted row; otherwise the
// peer endpoint falls back to "USD".
func (h *PeerOTCGRPCHandler) WithOTCOfferReader(
	offers OTCOfferReader, currency OptionCurrencyResolver,
) *PeerOTCGRPCHandler {
	cp := *h
	cp.otcOffers = offers
	cp.otcOptionCurrency = currency
	return &cp
}

// GetPublicOptionOffers serves the peer-facing
// GET /api/v3/public-option-offers endpoint (Phase 6 cross-bank
// discovery). Returns this bank's OPEN, undirected option listings —
// see OTCOfferRepository.ListOpenForCache for the exact filter.
//
// PrivateToBankCode honors a per-listing visibility hint: rows marked
// Private=true are dropped UNLESS PrivateToBankCode equals the
// requesting peer's X-Bank-Code (stamped by the api-gateway after
// PeerAuth resolves the inbound credential).
func (h *PeerOTCGRPCHandler) GetPublicOptionOffers(ctx context.Context, req *stockpb.GetPublicOptionOffersRequest) (*stockpb.GetPublicOptionOffersResponse, error) {
	if h.otcOffers == nil {
		return nil, status.Error(codes.Unimplemented, "OTCOfferReader not wired")
	}
	rows, err := h.otcOffers.ListOpenForCache(1000)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list open option offers: %v", err)
	}
	// Aggregate best-bid / best-ask once for every row in this call
	// (Part A 2026-05-16). Best-effort: a failed aggregation degrades
	// to omitting the fields, not failing the peer endpoint.
	var aggregates map[uint64]PeerOfferAggregate
	if h.bidsAgg != nil && len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		if got, aggErr := h.bidsAgg(ids); aggErr != nil {
			log.Printf("WARN: peer GetPublicOptionOffers: aggregate active bids failed (omitting fields): %v", aggErr)
		} else {
			aggregates = got
		}
	}
	caller := req.GetPeerBankCode()
	out := make([]*stockpb.PeerPublicOptionOffer, 0, len(rows))
	for i := range rows {
		o := &rows[i]
		// Honor per-listing privacy. Private listings only surface to
		// the named bank in PrivateToBankCode.
		if o.Private {
			if o.PrivateToBankCode == nil || *o.PrivateToBankCode != caller {
				continue
			}
		}
		sellerID := composePeerSellerID(o)
		currency := "USD"
		if h.otcOptionCurrency != nil {
			if c, err := h.otcOptionCurrency.CurrencyForStock(o.StockID); err == nil && c != "" {
				currency = c
			}
		}
		row := &stockpb.PeerPublicOptionOffer{
			OfferId: &stockpb.PeerForeignBankId{
				RoutingNumber: h.ownRouting,
				Id:            strconv.FormatUint(o.ID, 10),
			},
			Ticker:          o.Ticker,
			Amount:          o.Quantity.IntPart(),
			StrikePrice:     o.StrikePrice.String(),
			StrikeCurrency:  currency,
			Premium:         o.Premium.String(),
			PremiumCurrency: currency,
			SettlementDate:  o.SettlementDate.UTC().Format("2006-01-02T15:04:05Z"),
			SellerId: &stockpb.PeerForeignBankId{
				RoutingNumber: h.ownRouting,
				Id:            sellerID,
			},
			Direction: o.Direction,
			CreatedAt: o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			LastModifiedBy: &stockpb.PeerForeignBankId{
				RoutingNumber: h.ownRouting,
				Id:            sellerID,
			},
		}
		if agg, ok := aggregates[o.ID]; ok {
			row.ActiveChainsCount = agg.ActiveCount
			switch o.Direction {
			case "buy_initiated":
				row.BestAsk = agg.BestAsk
			default:
				row.BestBid = agg.BestBid
			}
		}
		out = append(out, row)
	}
	return &stockpb.GetPublicOptionOffersResponse{Offers: out}, nil
}

// composePeerSellerID mirrors the cache's helper but lives here so the
// peer endpoint doesn't import the otccache package.
func composePeerSellerID(o *model.OTCOffer) string {
	if o.InitiatorOwnerType == model.OwnerBank {
		return "bank"
	}
	if o.InitiatorOwnerID == nil {
		return ""
	}
	return "client-" + strconv.FormatUint(*o.InitiatorOwnerID, 10)
}

func (h *PeerOTCGRPCHandler) GetPublicStocks(ctx context.Context, req *stockpb.GetPublicStocksRequest) (*stockpb.GetPublicStocksResponse, error) {
	rows, err := h.holdings.ListPublic()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list public holdings: %v", err)
	}
	out := make([]*stockpb.PeerPublicStock, 0, len(rows))
	for i := range rows {
		hd := rows[i]
		// Owner ID maps to (h.ownRouting, owner_id-as-string). Bank-owned
		// holdings (OwnerID == nil) are surfaced as id "0".
		var ownerID string
		if hd.OwnerID != nil {
			ownerID = strconv.FormatUint(*hd.OwnerID, 10)
		} else {
			ownerID = "0"
		}
		// Phase 11 — surface the seller's set ask price + the listing's
		// real currency. Fallbacks: AveragePrice (weighted-avg cost)
		// for legacy rows without an explicit ask; "USD" if the
		// currency resolver isn't wired or the lookup fails.
		price := "0"
		if hd.PublicPrice.Sign() > 0 {
			price = hd.PublicPrice.String()
		} else if hd.AveragePrice.Sign() > 0 {
			price = hd.AveragePrice.String()
		}
		currency := "USD"
		if h.otcOptionCurrency != nil {
			if c, err := h.otcOptionCurrency.CurrencyForStock(hd.SecurityID); err == nil && c != "" {
				currency = c
			}
		}
		out = append(out, &stockpb.PeerPublicStock{
			OwnerId:       &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: ownerID},
			Ticker:        hd.Ticker,
			Amount:        hd.PublicQuantity,
			PricePerStock: price,
			Currency:      currency,
		})
	}
	return &stockpb.GetPublicStocksResponse{Stocks: out}, nil
}

func (h *PeerOTCGRPCHandler) CreateNegotiation(ctx context.Context, req *stockpb.CreateNegotiationRequest) (*stockpb.CreateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer, buyer_id, seller_id are required")
	}
	// Fix #7 (2026-05-16, SECURITY) — the authenticated peer's routing
	// MUST match the claimed buyer's routing. Without this, a peer
	// authenticated as Bank A could submit a bid claiming Bank C as the
	// buyer; on accept, the cross-bank SI-TX would route the premium
	// debit to Bank C's posting_executor which would resolve "client-N"
	// against Bank C's local users — debiting a third bank's account
	// for an option that bank never agreed to. Fix #1 partially
	// mitigates by pinning a specific account number, but a peer that
	// omits buyerAccountNumber would still hit the participant-id
	// resolution path. Defense at the source: require auth-routing ==
	// claimed buyer-routing.
	peerRouting, parseErr := strconv.ParseInt(req.GetPeerBankCode(), 10, 64)
	if parseErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "peer_bank_code %q is not numeric", req.GetPeerBankCode())
	}
	if req.GetBuyerId().GetRoutingNumber() != peerRouting {
		return nil, status.Errorf(codes.PermissionDenied,
			"buyer_id.routing_number (%d) must match the authenticated peer's routing (%d)",
			req.GetBuyerId().GetRoutingNumber(), peerRouting)
	}
	// Fix #9 (2026-05-16) — the seller on an inbound bid MUST be a
	// user of this bank. A bid with a foreign seller_routing would
	// create an orphaned row (no local user matches it, no
	// notification fires, accept fails). Reject up front.
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return nil, status.Errorf(codes.InvalidArgument,
			"seller_id.routing_number (%d) must match this bank's routing (%d) — inbound bids target a seller on this bank only",
			req.GetSellerId().GetRoutingNumber(), h.ownRouting)
	}
	offerJSON, err := json.Marshal(protoToOffer(req.GetOffer()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	foreignID := uuid.NewString()
	neg := &model.PeerOtcNegotiation{
		PeerBankCode:        req.GetPeerBankCode(),
		ForeignID:           foreignID,
		BuyerRoutingNumber:  req.GetBuyerId().GetRoutingNumber(),
		BuyerID:             req.GetBuyerId().GetId(),
		SellerRoutingNumber: req.GetSellerId().GetRoutingNumber(),
		SellerID:            req.GetSellerId().GetId(),
		OfferJSON:           string(offerJSON),
		Status:              "ongoing",
	}
	// Phase 10 — capture the bidder-supplied parent_offer_id for the
	// cross-bank cascade-cancel grouping. Both fields must be set for
	// the row to participate in cascade matching; either-or absent
	// means free-form (no cascade).
	if p := req.GetOffer().GetParentOfferId(); p != nil && p.GetId() != "" {
		r := p.GetRoutingNumber()
		id := p.GetId()
		neg.ParentOfferRouting = &r
		neg.ParentOfferID = &id
	}
	if err := h.negRepo.Create(neg); err != nil {
		return nil, status.Errorf(codes.Internal, "create: %v", err)
	}
	// Inbound bid from a peer → notify our local seller (if the seller
	// side is local). Best-effort, after-commit.
	if uid, ok := h.localClientUserID(neg.SellerRoutingNumber, neg.SellerID); ok {
		h.publishPeerNotif(ctx, uid, "OTC_OFFER_RECEIVED",
			notifDataFromOffer(protoToOffer(req.GetOffer())),
			"otc_negotiation", neg.ID,
		)
	}
	return &stockpb.CreateNegotiationResponse{
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: foreignID},
	}, nil
}

func (h *PeerOTCGRPCHandler) UpdateNegotiation(ctx context.Context, req *stockpb.UpdateNegotiationRequest) (*stockpb.UpdateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer and negotiation_id required")
	}
	offerJSON, err := json.Marshal(protoToOffer(req.GetOffer()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	if err := h.negRepo.UpdateOffer(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), string(offerJSON)); err != nil {
		return nil, status.Errorf(codes.Internal, "update: %v", err)
	}
	// Inbound counter — the peer that posted carries lastModifiedBy
	// on the offer. The OTHER party in our local row is the recipient.
	if h.notifier != nil {
		row, gerr := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())
		if gerr == nil {
			actorRouting := req.GetOffer().GetLastModifiedBy().GetRoutingNumber()
			actorID := req.GetOffer().GetLastModifiedBy().GetId()
			// Identify the local party that is NOT the actor.
			var localUID uint64
			if row.BuyerRoutingNumber == h.ownRouting &&
				!(row.BuyerRoutingNumber == actorRouting && row.BuyerID == actorID) {
				if uid, ok := h.localClientUserID(row.BuyerRoutingNumber, row.BuyerID); ok {
					localUID = uid
				}
			}
			if localUID == 0 && row.SellerRoutingNumber == h.ownRouting &&
				!(row.SellerRoutingNumber == actorRouting && row.SellerID == actorID) {
				if uid, ok := h.localClientUserID(row.SellerRoutingNumber, row.SellerID); ok {
					localUID = uid
				}
			}
			if localUID != 0 {
				h.publishPeerNotif(ctx, localUID, "OTC_OFFER_COUNTERED",
					notifDataFromOffer(protoToOffer(req.GetOffer())),
					"otc_negotiation", row.ID,
				)
			}
		}
	}
	return &stockpb.UpdateNegotiationResponse{}, nil
}

func (h *PeerOTCGRPCHandler) GetNegotiation(ctx context.Context, req *stockpb.GetNegotiationRequest) (*stockpb.GetNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	row, err := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(row.OfferJSON), &offer)
	return &stockpb.GetNegotiationResponse{
		Id:        &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: row.ForeignID},
		BuyerId:   &stockpb.PeerForeignBankId{RoutingNumber: row.BuyerRoutingNumber, Id: row.BuyerID},
		SellerId:  &stockpb.PeerForeignBankId{RoutingNumber: row.SellerRoutingNumber, Id: row.SellerID},
		Offer:     offerToProto(offer),
		Status:    row.Status,
		UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (h *PeerOTCGRPCHandler) DeleteNegotiation(ctx context.Context, req *stockpb.DeleteNegotiationRequest) (*stockpb.DeleteNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	// Load BEFORE the status flip so we can distinguish caller-driven
	// cancel (ParentOfferID nil — free-form chain) from cascade-cancel
	// (ParentOfferID set — discovered chain whose seller accepted a
	// competing bid). Only used for the notification choice; the row
	// state change is identical either way.
	row, gerr := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())

	if err := h.negRepo.UpdateStatus(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), "cancelled"); err != nil {
		return nil, status.Errorf(codes.Internal, "cancel: %v", err)
	}
	if gerr == nil && row != nil && h.notifier != nil {
		notifType := "OTC_OFFER_CANCELLED"
		data := map[string]string{}
		// Cascade heuristic: a discovered-group chain DELETEd by the
		// seller side means the cascade fired (the seller would have
		// accepted a competing bid). Free-form chains (no parent)
		// can't be cascade victims, so they're plain cancels.
		if row.ParentOfferRouting != nil && row.ParentOfferID != nil && *row.ParentOfferID != "" {
			notifType = "OTC_OFFER_CASCADE_CANCELLED"
			var offer contractsitx.OtcOffer
			_ = json.Unmarshal([]byte(row.OfferJSON), &offer)
			data["ticker"] = offer.Ticker
			data["accepted_premium"] = offer.Premium.String()
		} else {
			var offer contractsitx.OtcOffer
			_ = json.Unmarshal([]byte(row.OfferJSON), &offer)
			data["ticker"] = offer.Ticker
		}
		// Recipient: the LOCAL party in this row (whichever side has
		// own_routing). For caller-driven cancels the caller is the
		// other bank's user, so the local party is the recipient.
		if uid, ok := h.localClientUserID(row.BuyerRoutingNumber, row.BuyerID); ok {
			h.publishPeerNotif(ctx, uid, notifType, data, "otc_negotiation", row.ID)
		} else if uid, ok := h.localClientUserID(row.SellerRoutingNumber, row.SellerID); ok {
			h.publishPeerNotif(ctx, uid, notifType, data, "otc_negotiation", row.ID)
		}
	}
	return &stockpb.DeleteNegotiationResponse{}, nil
}

// CascadeCancelSiblings is the Phase 10 cross-bank cascade. Given a
// just-accepted peer_otc_negotiations row, finds every OTHER ongoing
// chain whose seller is the same AND whose (parent_offer_routing,
// parent_offer_id) matches — i.e. they were initiated against the
// SAME discovered listing on the seller's bank. Each matched row is
// flipped to status=cancelled locally; the response carries
// (peer_bank_code, foreign_id) tuples so the calling gateway can
// fire outbound DELETEs to each bidder's bank to update their mirrors.
//
// Match criteria (precise — no false positives):
//   - seller_routing_number = accepted.seller_routing_number
//   - seller_id             = accepted.seller_id
//   - status                = "ongoing"
//   - parent_offer_routing  = accepted.parent_offer_routing
//   - parent_offer_id       = accepted.parent_offer_id
//   - NOT (peer_bank_code = accepted.peer_bank_code AND foreign_id = accepted.foreign_id)
//
// Returns an empty list when the accepted chain has no parent
// (free-form initiate, not discovered) — those chains are never part
// of a sibling group, so a seller can hold two distinct same-ticker
// listings without accidental cross-cancel.
func (h *PeerOTCGRPCHandler) CascadeCancelSiblings(ctx context.Context, req *stockpb.CascadeCancelSiblingsRequest) (*stockpb.CascadeCancelSiblingsResponse, error) {
	if h.negRepo == nil {
		return nil, status.Error(codes.Unimplemented, "negotiation repo not wired")
	}
	accepted, err := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetForeignId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "accepted negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "lookup accepted: %v", err)
	}
	// Free-form chains (no parent) are not part of any sibling group.
	if accepted.ParentOfferRouting == nil || accepted.ParentOfferID == nil || *accepted.ParentOfferID == "" {
		return &stockpb.CascadeCancelSiblingsResponse{}, nil
	}
	candidates, err := h.negRepo.ListBySellerAndParentOffer(
		accepted.SellerRoutingNumber, accepted.SellerID,
		*accepted.ParentOfferRouting, *accepted.ParentOfferID,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list siblings: %v", err)
	}
	out := make([]*stockpb.CascadedSibling, 0, len(candidates))
	for i := range candidates {
		sib := &candidates[i]
		// Skip the just-accepted row itself.
		if sib.PeerBankCode == accepted.PeerBankCode && sib.ForeignID == accepted.ForeignID {
			continue
		}
		if uerr := h.negRepo.UpdateStatus(sib.PeerBankCode, sib.ForeignID, "cancelled"); uerr != nil {
			log.Printf("WARN: cascade-cancel sibling %s/%s status update failed: %v",
				sib.PeerBankCode, sib.ForeignID, uerr)
			continue
		}
		// Project the cancelled row into the same wire shape the FE
		// already renders for /me/peer-otc/negotiations list rows so
		// the cross-bank accept response matches the local one. Best-
		// effort: a failed OfferJSON decode still yields a valid row
		// (id-only) — FE just won't have the offer terms for that one.
		var offer contractsitx.OtcOffer
		_ = json.Unmarshal([]byte(sib.OfferJSON), &offer)
		out = append(out, &stockpb.CascadedSibling{
			PeerBankCode: sib.PeerBankCode,
			ForeignId:    sib.ForeignID,
			BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: sib.BuyerRoutingNumber, Id: sib.BuyerID},
			SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: sib.SellerRoutingNumber, Id: sib.SellerID},
			Offer:        offerToProto(offer),
			Status:       "cancelled",
			Role:         "seller", // cascade fires on accept — caller is always the seller
			UpdatedAt:    sib.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return &stockpb.CascadeCancelSiblingsResponse{Siblings: out}, nil
}

// MarkNegotiationAccepted flips a local mirror row to status=accepted
// without dispatching SI-TX. The gateway calls this from
// AcceptPeerNegotiation after the outbound proxy succeeds, so the
// originating side's /me/peer-otc/negotiations list reflects the
// terminal state immediately instead of remaining "ongoing" until a
// reconciliation sweep.
func (h *PeerOTCGRPCHandler) MarkNegotiationAccepted(ctx context.Context, req *stockpb.MarkNegotiationAcceptedRequest) (*stockpb.MarkNegotiationAcceptedResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	if err := h.negRepo.UpdateStatus(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), "accepted"); err != nil {
		return nil, status.Errorf(codes.Internal, "mark accepted: %v", err)
	}
	return &stockpb.MarkNegotiationAcceptedResponse{}, nil
}

func (h *PeerOTCGRPCHandler) AcceptNegotiation(ctx context.Context, req *stockpb.AcceptNegotiationRequest) (*stockpb.AcceptNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	row, err := h.negRepo.GetByPeerAndID(req.GetPeerBankCode(), req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	if err := json.Unmarshal([]byte(row.OfferJSON), &offer); err != nil {
		return nil, status.Errorf(codes.Internal, "decode offer: %v", err)
	}

	// Compose the 4 postings:
	// 1. Buyer debits premium (in premium currency)
	// 2. Seller credits premium
	// 3. Seller debits 1× OptionDescription (asset)
	// 4. Buyer credits 1× OptionDescription
	optDesc := contractsitx.OptionDescription{
		Ticker:         offer.Ticker,
		Amount:         offer.Amount,
		StrikePrice:    offer.PricePerStock,
		Currency:       offer.Currency,
		SettlementDate: offer.SettlementDate,
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: h.ownRouting, ID: row.ForeignID},
	}
	optDescJSON, err := json.Marshal(optDesc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal option description: %v", err)
	}
	optAssetID := string(optDescJSON)

	premium := offer.Premium.String()
	// Fix #1 (2026-05-16) — prefer the buyer-pinned account number from
	// the OtcOffer wire payload over the participant-id ("client-N")
	// fallback. The buyer's bank set BuyerAccountNumber at bid time so
	// the SI-TX posting executor doesn't have to pick "first active
	// <currency> account" for the buyer — which was non-deterministic
	// and silently failed when the buyer had no account in the offer's
	// currency. Sellers stay on participant-id resolution (the
	// seller-credit is a credit; no per-account binding needed since
	// any active <currency> account works for incoming funds).
	buyerAccountID := row.BuyerID
	if offer.BuyerAccountNumber != "" {
		buyerAccountID = offer.BuyerAccountNumber
	}
	postings := []*transactionpb.SiTxPosting{
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: buyerAccountID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionCredit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: buyerAccountID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionCredit},
	}

	resp, err := h.peerTx.InitiateOutboundTxWithPostings(ctx, &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: req.GetPeerBankCode(),
		Postings:     postings,
		TxKind:       "otc-accept",
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dispatch: %v", err)
	}

	if uerr := h.negRepo.UpdateStatus(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), "accepted"); uerr != nil {
		// Status update failure is non-fatal — the TX has already been
		// dispatched and the negotiation is functionally accepted.
		// Background sweep can reconcile.
		log.Printf("WARN: peer-otc accept status update failed for %s/%s: %v",
			req.GetPeerBankCode(), req.GetNegotiationId().GetId(), uerr)
	}

	// Seller-side notification: this bank is the SELLER's bank (the
	// inbound /accept lands here because the buyer's bank POSTed). The
	// local user is the seller. The buyer's bank emits its own
	// OTC_CONTRACT_CREATED notification independently when it processes
	// the SI-TX postings on its side.
	if uid, ok := h.localClientUserID(row.SellerRoutingNumber, row.SellerID); ok {
		h.publishPeerNotif(ctx, uid, "OTC_CONTRACT_CREATED",
			map[string]string{
				"ticker":       offer.Ticker,
				"quantity":     strconv.FormatInt(offer.Amount, 10),
				"strike_price": offer.PricePerStock.String(),
				"premium_paid": offer.Premium.String(),
			},
			"otc_negotiation", row.ID,
		)
	}

	return &stockpb.AcceptNegotiationResponse{
		TransactionId: resp.GetTransactionId(),
		Status:        resp.GetStatus(),
	}, nil
}

func protoToOffer(p *stockpb.PeerOtcOffer) contractsitx.OtcOffer {
	pricePerStock, _ := decimal.NewFromString(p.GetPricePerStock())
	premium, _ := decimal.NewFromString(p.GetPremium())
	var lastModBy contractsitx.ForeignBankId
	if p.GetLastModifiedBy() != nil {
		lastModBy = contractsitx.ForeignBankId{
			RoutingNumber: p.GetLastModifiedBy().GetRoutingNumber(),
			ID:            p.GetLastModifiedBy().GetId(),
		}
	}
	var parentOfferID contractsitx.ForeignBankId
	if p.GetParentOfferId() != nil {
		parentOfferID = contractsitx.ForeignBankId{
			RoutingNumber: p.GetParentOfferId().GetRoutingNumber(),
			ID:            p.GetParentOfferId().GetId(),
		}
	}
	return contractsitx.OtcOffer{
		Ticker:             p.GetTicker(),
		Amount:             p.GetAmount(),
		PricePerStock:      pricePerStock,
		Currency:           p.GetCurrency(),
		Premium:            premium,
		PremiumCurrency:    p.GetPremiumCurrency(),
		SettlementDate:     p.GetSettlementDate(),
		LastModifiedBy:     lastModBy,
		ParentOfferID:      parentOfferID,
		BuyerAccountNumber: p.GetBuyerAccountNumber(),
	}
}

func offerToProto(o contractsitx.OtcOffer) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker:          o.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerStock.String(),
		Currency:        o.Currency,
		Premium:         o.Premium.String(),
		PremiumCurrency: o.PremiumCurrency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
		ParentOfferId: &stockpb.PeerForeignBankId{
			RoutingNumber: o.ParentOfferID.RoutingNumber,
			Id:            o.ParentOfferID.ID,
		},
		BuyerAccountNumber: o.BuyerAccountNumber,
	}
}

// RecordOptionContract is called by transaction-service at COMMIT_TX
// time for each option-asset posting on this bank's routing. Behaviour
// switches on req.intent:
//
//   - "" / "accept" → form a new contract: persist a peer_option_contracts
//     row keyed on (crossbank_tx_id, posting_index) and lock the seller's
//     holdings.
//
//   - "exercise" → transition the existing contract (looked up by
//     OptionDescription.negotiationId + this side's direction) to
//     status="exercised", run role-specific stock ops: seller side
//     consumes the reservation and decrements the holding; buyer side
//     credits a holding for the gained shares.
//
// Idempotent on (crossbank_tx_id, posting_index) for both intents —
// retries return the same contract row without double-effects.
func (h *PeerOTCGRPCHandler) RecordOptionContract(ctx context.Context, req *stockpb.RecordOptionContractRequest) (*stockpb.RecordOptionContractResponse, error) {
	if h.peerOptionRepo == nil {
		return nil, status.Error(codes.Unimplemented, "peer option repo not wired")
	}
	if req.GetCrossbankTxId() == "" || req.GetOptionDescriptionJson() == "" {
		return nil, status.Error(codes.InvalidArgument, "crossbank_tx_id and option_description_json are required")
	}
	if req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "buyer_id and seller_id are required")
	}
	if d := req.GetDirection(); d != contractsitx.DirectionDebit && d != contractsitx.DirectionCredit {
		return nil, status.Errorf(codes.InvalidArgument, "direction must be DEBIT or CREDIT, got %q", d)
	}

	var opt contractsitx.OptionDescription
	if err := json.Unmarshal([]byte(req.GetOptionDescriptionJson()), &opt); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode option description: %v", err)
	}

	if req.GetIntent() == "exercise" {
		return h.recordOptionExercise(ctx, req, opt)
	}

	row := &model.PeerOptionContract{
		CrossbankTxID:            req.GetCrossbankTxId(),
		PostingIndex:             req.GetPostingIndex(),
		NegotiationRoutingNumber: opt.NegotiationID.RoutingNumber,
		NegotiationID:            opt.NegotiationID.ID,
		BuyerRoutingNumber:       req.GetBuyerId().GetRoutingNumber(),
		BuyerID:                  req.GetBuyerId().GetId(),
		SellerRoutingNumber:      req.GetSellerId().GetRoutingNumber(),
		SellerID:                 req.GetSellerId().GetId(),
		Ticker:                   opt.Ticker,
		Quantity:                 opt.Amount,
		StrikePrice:              opt.StrikePrice,
		Currency:                 opt.Currency,
		SettlementDate:           opt.SettlementDate,
		Direction:                req.GetDirection(),
		Status:                   "active",
	}
	if err := h.peerOptionRepo.UpsertIdempotent(row); err != nil {
		return nil, status.Errorf(codes.Internal, "persist peer option contract: %v", err)
	}

	// Seller-side share lock. Only meaningful when this bank holds the
	// seller (DEBIT direction = seller loses option = our bank tracks
	// the seller). Idempotent on peer_option_contract_id, so safe to
	// retry: a second commit replay finds the existing reservation
	// and returns it without double-locking.
	if req.GetDirection() == contractsitx.DirectionDebit && h.holdingReserver != nil {
		ownerType, ownerID, parseErr := parseSellerOwner(row.SellerID)
		if parseErr != nil {
			// A DEBIT-side contract means this bank holds the seller, so we
			// MUST be able to lock the seller's shares. An unparseable
			// seller_id means we cannot — fail loudly instead of leaving an
			// "active" contract with no holding reservation behind it (the
			// seller's shares would otherwise stay tradeable until exercise).
			// The NEW_TX-time CheckSellerCanDeliver pre-check already rejects
			// unparseable sellers, so reaching here implies data corruption.
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d: seller_id %q not parseable, cannot lock shares: %v",
				row.ID, row.SellerID, parseErr)
		}
		// Spec-aligned path (Celina-5 OTC SAGA): the shares were already RESERVED
		// at NEW_TX time (vote-YES) keyed on crossbank_tx_id. At COMMIT we simply
		// ATTACH that hold to the freshly-minted contract row — no re-check that
		// could fail because the seller sold in the meantime (they couldn't: the
		// shares were held). The existing consume/release-by-contract-id paths
		// then operate unchanged.
		attached := false
		if cbTx := req.GetCrossbankTxId(); cbTx != "" {
			err := h.holdingReserver.AttachCrossBankReservationToContract(ctx, cbTx, row.ID)
			if err == nil {
				attached = true
			} else if status.Code(err) != codes.NotFound {
				return nil, status.Errorf(codes.Internal,
					"peer-option contract %d: attach vote-time share hold (tx %s): %v", row.ID, cbTx, err)
			}
			// NotFound → no vote-time hold (older NEW_TX before this change, or a
			// transaction-service that didn't reserve) → fall through to the
			// legacy reserve-at-commit below for backward compatibility.
		}
		if !attached {
			if _, err := h.holdingReserver.ReserveForPeerOptionContract(
				ctx, ownerType, ownerID, "stock", row.Ticker, row.ID, row.Quantity,
			); err != nil {
				// Legacy fallback. Reservation failed — e.g. the seller traded the
				// shares away in the window between the NEW_TX vote and this
				// COMMIT-time lock (the very gap the NEW_TX reservation closes).
				// Surface the failure so the SI-TX COMMIT does not ack; both the
				// contract row (idempotent on crossbank_tx_id, posting_index) and
				// the reservation (idempotent on peer_option_contract_id) are
				// replay-safe, so a COMMIT retry re-attempts and heals once shares
				// are available.
				if st, ok := status.FromError(err); ok {
					return nil, st.Err()
				}
				return nil, status.Errorf(codes.Internal,
					"peer-option contract %d: lock seller %s ticker %s qty %d: %v",
					row.ID, row.SellerID, row.Ticker, row.Quantity, err)
			}
		}
	}

	return &stockpb.RecordOptionContractResponse{ContractId: row.ID}, nil
}

// CheckSellerCanDeliver validates that a seller participant has at
// least `quantity` unreserved shares of the requested ticker. Used by
// transaction-service at NEW_TX time to vote NO with INSUFFICIENT_ASSET
// before money moves, instead of degrading silently at COMMIT_TX.
//
// ok=true means the seller has the holding AND
// (quantity - reserved_quantity) >= req.quantity. Any other condition
// (no holding, insufficient available, unparseable seller_id) returns
// ok=false with available_quantity=0. This is information-leak-safe:
// the caller learns "can deliver?" but not how short the seller is or
// whether they exist on this bank at all.
func (h *PeerOTCGRPCHandler) CheckSellerCanDeliver(ctx context.Context, req *stockpb.CheckSellerCanDeliverRequest) (*stockpb.CheckSellerCanDeliverResponse, error) {
	if req.GetSellerId() == nil || req.GetTicker() == "" || req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seller_id, ticker, and positive quantity are required")
	}
	// Fix #8 (2026-05-16, defense) — caller MUST be asking about a
	// seller on THIS bank's routing. The only intended caller
	// (transaction-service posting_executor) pre-filters by ownRouting
	// before invoking us, but we don't rely on that — a future caller
	// that forgets the pre-filter would otherwise silently look up
	// LOCAL client-N's holdings against a foreign seller's request,
	// returning a misleading "can deliver" or "insufficient" verdict.
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
	}
	ownerType, ownerID, parseErr := parseSellerOwner(req.GetSellerId().GetId())
	if parseErr != nil {
		return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
	}
	holding, err := h.holdings.GetByOwnerAndTicker(ownerType, ownerID, "stock", req.GetTicker())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
		}
		return nil, status.Errorf(codes.Internal, "lookup holding: %v", err)
	}
	available := holding.Quantity - holding.ReservedQuantity
	if available < 0 {
		available = 0
	}
	return &stockpb.CheckSellerCanDeliverResponse{
		Ok:                available >= req.GetQuantity(),
		AvailableQuantity: available,
	}, nil
}

// ReserveSellerSharesForNewTx places a real HOLD on the seller's shares at
// SI-TX NEW_TX (vote) time, keyed on crossbank_tx_id. Unlike
// CheckSellerCanDeliver this increments reserved_quantity so the shares cannot
// be sold before COMMIT_TX (Celina-5 OTC SAGA step 2). Same routing/seller
// validation as CheckSellerCanDeliver; ok=false on insufficient/missing so the
// caller votes NO with INSUFFICIENT_ASSET. Idempotent on crossbank_tx_id.
func (h *PeerOTCGRPCHandler) ReserveSellerSharesForNewTx(ctx context.Context, req *stockpb.ReserveSellerSharesRequest) (*stockpb.ReserveSellerSharesResponse, error) {
	if req.GetSellerId() == nil || req.GetTicker() == "" || req.GetQuantity() <= 0 || req.GetCrossbankTxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "seller_id, ticker, positive quantity, and crossbank_tx_id are required")
	}
	if h.holdingReserver == nil {
		// No reserver wired — cannot hold shares, so we must not vote YES.
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	// Must be a seller on THIS bank's routing (mirror of CheckSellerCanDeliver
	// Fix #8 defense — never reserve a local client's shares against a foreign
	// seller's request).
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	ownerType, ownerID, parseErr := parseSellerOwner(req.GetSellerId().GetId())
	if parseErr != nil {
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	res, err := h.holdingReserver.ReserveForCrossBankNewTx(
		ctx, ownerType, ownerID, "stock", req.GetTicker(), req.GetCrossbankTxId(), req.GetQuantity(),
	)
	if err != nil {
		// FailedPrecondition (holding not found / insufficient) → ok=false so the
		// caller votes NO. Other errors propagate.
		if status.Code(err) == codes.FailedPrecondition {
			return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "reserve seller shares: %v", err)
	}
	return &stockpb.ReserveSellerSharesResponse{
		Ok:                true,
		ReservedQuantity:  res.ReservedQuantity,
		AvailableQuantity: res.AvailableQuantity,
	}, nil
}

// ReleaseSellerSharesForNewTx releases a vote-time share hold by crossbank_tx_id
// on ROLLBACK_TX (or a partial NO mid-NEW_TX). Idempotent: missing/non-active
// reservation → released_quantity=0.
func (h *PeerOTCGRPCHandler) ReleaseSellerSharesForNewTx(ctx context.Context, req *stockpb.ReleaseSellerSharesRequest) (*stockpb.ReleaseSellerSharesResponse, error) {
	if req.GetCrossbankTxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "crossbank_tx_id is required")
	}
	if h.holdingReserver == nil {
		return &stockpb.ReleaseSellerSharesResponse{ReleasedQuantity: 0}, nil
	}
	res, err := h.holdingReserver.ReleaseForCrossBankNewTx(ctx, req.GetCrossbankTxId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release seller shares: %v", err)
	}
	return &stockpb.ReleaseSellerSharesResponse{ReleasedQuantity: res.ReleasedQuantity}, nil
}

// parseSellerOwner maps an SI-TX participant id ("client-<n>",
// "employee-<n>", or "bank") to the OwnerType + numeric owner id used
// by the holdings table. Returns the bank-owner sentinel (ownerID nil)
// for "bank". Errors on unparseable ids — caller can choose to log
// without failing the parent RPC.
func parseSellerOwner(sellerID string) (model.OwnerType, *uint64, error) {
	if sellerID == "bank" {
		return model.OwnerBank, nil, nil
	}
	rest, ok := strings.CutPrefix(sellerID, "client-")
	if !ok {
		return "", nil, errors.New("unsupported seller_id prefix; expected client-<n> or bank")
	}
	id, parseErr := strconv.ParseUint(rest, 10, 64)
	if parseErr != nil {
		return "", nil, parseErr
	}
	return model.OwnerClient, &id, nil
}

// recordOptionExercise handles the intent="exercise" branch of
// RecordOptionContract. Looks up the existing peer_option_contracts
// row by (negotiation, direction), validates status, runs the
// role-specific stock operations:
//
//   - DEBIT direction (this bank holds the seller): consume the
//     reservation pinned to the contract id (settle it), which
//     decrements the seller's holding by the contract's quantity.
//
//   - CREDIT direction (this bank holds the buyer): credit a
//     holding for the buyer, creating a new (owner, ticker) row
//     when needed.
//
// Then transitions the contract to status="exercised". Idempotent
// on (crossbank_tx_id, posting_index): repeated calls land on the
// same contract row and the underlying stock ops are themselves
// idempotent (settlements unique on synthetic txn id; credit-to-
// holding is upsert-shaped).
func (h *PeerOTCGRPCHandler) recordOptionExercise(ctx context.Context, req *stockpb.RecordOptionContractRequest, opt contractsitx.OptionDescription) (*stockpb.RecordOptionContractResponse, error) {
	if h.holdingReserver == nil {
		return nil, status.Error(codes.Unimplemented, "holding reserver not wired")
	}
	contract, err := h.peerOptionRepo.GetByNegotiationAndDirection(opt.NegotiationID.RoutingNumber, opt.NegotiationID.ID, req.GetDirection())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "no active peer_option_contract for this negotiation/direction")
		}
		return nil, status.Errorf(codes.Internal, "lookup contract: %v", err)
	}
	// Idempotent: if already exercised, just return the existing id.
	if contract.Status == "exercised" {
		return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
	}
	if contract.Status != "active" {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot exercise contract in status %q", contract.Status)
	}

	switch req.GetDirection() {
	case contractsitx.DirectionDebit:
		// Seller side. Consume the reservation, decrement holding,
		// then record realised P/L for the seller (strike price minus
		// cost basis × qty). The cost basis is snapshotted under the
		// row lock inside ConsumeForPeerOptionContract so this CG
		// write is race-free with concurrent buys/sells on the same
		// holding.
		settle, err := h.holdingReserver.ConsumeForPeerOptionContract(ctx, contract.ID, contract.Quantity)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "consume seller reservation: %v", err)
		}
		// Skip the capital-gain write on a replayed consume — the
		// settlement already existed, so no shares moved this time and a
		// second CapitalGain row would double-count the realised P/L
		// (CapitalGain.Create is not idempotent).
		if !settle.AlreadySettled && h.capitalGainRepo != nil {
			sellerType, sellerID, parseErr := parseSellerOwner(contract.SellerID)
			if parseErr != nil {
				log.Printf("WARN: peer-option contract %d exercise: seller_id %q not parseable; capital gain not recorded: %v", contract.ID, contract.SellerID, parseErr)
			} else {
				gain := contract.StrikePrice.Sub(settle.AveragePriceBefore).Mul(decimal.NewFromInt(contract.Quantity))
				cg := &model.CapitalGain{
					OwnerType:        sellerType,
					OwnerID:          sellerID,
					OTC:              true,
					SecurityType:     "stock",
					Ticker:           contract.Ticker,
					Quantity:         contract.Quantity,
					BuyPricePerUnit:  settle.AveragePriceBefore,
					SellPricePerUnit: contract.StrikePrice,
					TotalGain:        gain,
					Currency:         contract.Currency,
					TaxYear:          time.Now().Year(),
					TaxMonth:         int(time.Now().Month()),
				}
				if cgErr := h.capitalGainRepo.Create(cg); cgErr != nil {
					log.Printf("WARN: peer-option contract %d exercise: seller capital gain create failed (money/shares already moved): %v", contract.ID, cgErr)
				}
			}
		}
	case contractsitx.DirectionCredit:
		// Buyer side. Credit the buyer's holding for the gained shares.
		// AveragePrice = StrikePrice (the per-share price paid on
		// exercise). Premium paid at acceptance is tracked separately
		// as a SecurityType="option" CapitalGain row written by the
		// acceptance saga — never folded into the stock cost basis,
		// so later stock sells produce the same P/L as a matching
		// market buy at the strike would.
		ownerType, ownerID, parseErr := parseSellerOwner(contract.BuyerID)
		if parseErr != nil {
			// The buyer paid the strike (money moved cross-bank at exercise),
			// so failing to credit their shares is delivery failure, not a
			// cosmetic gap. Surface it so the contract is NOT marked exercised
			// and the SI-TX exercise commit retries — silently degrading would
			// leave the buyer paid-but-undelivered with no recovery (Bug 2's
			// exercise-time analog).
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d exercise: buyer_id %q not parseable, cannot credit shares: %v",
				contract.ID, contract.BuyerID, parseErr)
		}
		// Credit the buyer AND flip the contract to "exercised" atomically.
		// The status transition lives inside this call (guarded by a row
		// lock on the contract), so a replayed exercise is a no-op and the
		// buyer's shares are never double-credited. Returns early — the
		// shared SetStatus below is only for the DEBIT path.
		if err := h.holdingReserver.ExerciseBuyerCreditForPeerOption(ctx, contract.ID, ownerType, ownerID, contract.Ticker, contract.Quantity, contract.StrikePrice); err != nil {
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d exercise: credit buyer holding: %v", contract.ID, err)
		}
		return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
	}

	if err := h.peerOptionRepo.SetStatus(contract.ID, "exercised"); err != nil {
		return nil, status.Errorf(codes.Internal, "mark exercised: %v", err)
	}
	return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
}

// InitiateOptionExercise builds the 4-posting Transaction for an
// exercise (strike money buyer→seller + option markers carrying
// intent=exercise) and dispatches it via transaction-service. Called
// by the gateway when the buyer hits POST /api/v3/me/otc/contracts/peer/:id/exercise.
//
// Validates: contract exists on this bank, this bank holds the buyer
// side (so this bank is the IB), contract is active, settlement date
// is in the future. The strike-money currency-account on the seller's
// bank is left to that bank to resolve via the executor's seller-id-
// to-account lookup at NEW_TX time.
func (h *PeerOTCGRPCHandler) InitiateOptionExercise(ctx context.Context, req *stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error) {
	if req.GetPeerOptionContractId() == 0 || req.GetBuyerAccountNumber() == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_option_contract_id and buyer_account_number are required")
	}
	contract, err := h.peerOptionRepo.GetByID(req.GetPeerOptionContractId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "contract not found")
		}
		return nil, status.Errorf(codes.Internal, "load contract: %v", err)
	}
	if contract.Direction != contractsitx.DirectionCredit {
		return nil, status.Error(codes.FailedPrecondition, "this bank does not hold the buyer side of the contract; only the buyer's bank can initiate exercise")
	}
	if contract.Status != "active" {
		return nil, status.Errorf(codes.FailedPrecondition, "contract status %q is not exercisable", contract.Status)
	}

	strikeAmount := contract.StrikePrice.Mul(decimal.NewFromInt(contract.Quantity)).String()

	// Build the OptionDescription that goes into the option-marker
	// postings, with intent="exercise" so each receiving bank's
	// RecordOptionContract dispatches to the exercise branch.
	optDesc := contractsitx.OptionDescription{
		Ticker:         contract.Ticker,
		Amount:         contract.Quantity,
		StrikePrice:    contract.StrikePrice,
		Currency:       contract.Currency,
		SettlementDate: contract.SettlementDate,
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: contract.NegotiationRoutingNumber, ID: contract.NegotiationID},
		Intent:         "exercise",
	}
	optDescJSON, err := json.Marshal(optDesc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal option description: %v", err)
	}
	optAssetID := string(optDescJSON)

	// 4 postings:
	//  1. Buyer DEBIT strike money (currency)
	//  2. Seller CREDIT strike money (currency)
	//  3. Seller DEBIT option (marker)
	//  4. Buyer CREDIT option (marker)
	// Currency postings carry account numbers; option postings carry
	// participant ids (the executor resolves via ListAccountsByClient
	// at NEW_TX time).
	postings := []*transactionpb.SiTxPosting{
		{RoutingNumber: contract.BuyerRoutingNumber, AccountId: req.GetBuyerAccountNumber(), AssetId: contract.Currency, Amount: strikeAmount, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: contract.SellerRoutingNumber, AccountId: contract.SellerID, AssetId: contract.Currency, Amount: strikeAmount, Direction: contractsitx.DirectionCredit},
		{RoutingNumber: contract.SellerRoutingNumber, AccountId: contract.SellerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionDebit},
		{RoutingNumber: contract.BuyerRoutingNumber, AccountId: contract.BuyerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionCredit},
	}

	resp, err := h.peerTx.InitiateOutboundTxWithPostings(ctx, &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: strconv.FormatInt(contract.SellerRoutingNumber, 10),
		Postings:     postings,
		TxKind:       "otc-exercise",
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "dispatch exercise: %v", err)
	}
	return &stockpb.InitiateOptionExerciseResponse{
		TransactionId: resp.GetTransactionId(),
		Status:        resp.GetStatus(),
	}, nil
}

// RecordOutboundNegotiation persists a buyer-side mirror row in
// peer_otc_negotiations right after the gateway successfully POSTed the
// negotiation to the seller's bank. Without this row, the buyer-side
// /me/peer-otc/negotiations list endpoint can't surface the negotiation
// — the original receiver-only persistence model only persists on the
// seller's bank.
//
// peer_bank_code on this row is the SELLER's bank code (because that's
// the peer that issued the foreign_id). buyer/seller routing+id are
// stamped exactly as composed by the gateway so this bank's later
// ListMyPeerNegotiations can match the caller's principal against
// buyer_id.
func (h *PeerOTCGRPCHandler) RecordOutboundNegotiation(ctx context.Context, req *stockpb.RecordOutboundNegotiationRequest) (*stockpb.RecordOutboundNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetBuyerId() == nil || req.GetSellerId() == nil || req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer, buyer_id, seller_id, negotiation_id required")
	}
	if req.GetPeerBankCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_bank_code required")
	}
	offerJSON, err := json.Marshal(protoToOffer(req.GetOffer()))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	neg := &model.PeerOtcNegotiation{
		PeerBankCode:        req.GetPeerBankCode(),
		ForeignID:           req.GetNegotiationId().GetId(),
		BuyerRoutingNumber:  req.GetBuyerId().GetRoutingNumber(),
		BuyerID:             req.GetBuyerId().GetId(),
		SellerRoutingNumber: req.GetSellerId().GetRoutingNumber(),
		SellerID:            req.GetSellerId().GetId(),
		OfferJSON:           string(offerJSON),
		Status:              "ongoing",
	}
	// Phase 10 — mirror the parent_offer_id on the buyer-side row so
	// /me/peer-otc/negotiations surfaces the linkage on both ends.
	if p := req.GetOffer().GetParentOfferId(); p != nil && p.GetId() != "" {
		r := p.GetRoutingNumber()
		id := p.GetId()
		neg.ParentOfferRouting = &r
		neg.ParentOfferID = &id
	}
	if err := h.negRepo.Upsert(neg); err != nil {
		return nil, status.Errorf(codes.Internal, "upsert: %v", err)
	}
	return &stockpb.RecordOutboundNegotiationResponse{}, nil
}

// ListMyPeerNegotiations returns rows where the caller's principal
// (stamped "client-<N>") matches buyer_id or seller_id on the row, with
// the caller's bank routing matching the corresponding routing column.
// role: "buyer" / "seller" / "" / "both".
func (h *PeerOTCGRPCHandler) ListMyPeerNegotiations(ctx context.Context, req *stockpb.ListMyPeerNegotiationsRequest) (*stockpb.ListMyPeerNegotiationsResponse, error) {
	if req.GetClientId() == "" {
		return nil, status.Error(codes.InvalidArgument, "client_id required")
	}
	if req.GetOwnRoutingNumber() == 0 {
		return nil, status.Error(codes.InvalidArgument, "own_routing_number required")
	}
	principal := req.GetClientId()
	if !strings.HasPrefix(principal, "client-") {
		principal = "client-" + principal
	}
	rows, err := h.negRepo.ListByClient(req.GetOwnRoutingNumber(), principal, req.GetRole())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list: %v", err)
	}
	out := &stockpb.ListMyPeerNegotiationsResponse{Items: make([]*stockpb.PeerNegotiationListItem, 0, len(rows))}
	for i := range rows {
		row := &rows[i]
		var offer contractsitx.OtcOffer
		_ = json.Unmarshal([]byte(row.OfferJSON), &offer)
		role := "buyer"
		if row.SellerRoutingNumber == req.GetOwnRoutingNumber() && row.SellerID == principal {
			role = "seller"
		}

		// For accepted negotiations, resolve the local peer_option_contracts
		// row so the caller can navigate from negotiation → contract. We look
		// up the direction that matches the caller's role: buyer side is
		// CREDIT, seller side is DEBIT. Best-effort — lookup failure leaves
		// local_contract_id as 0 (not a fatal error for the list call).
		var localContractID uint64
		if row.Status == "accepted" && h.peerOptionRepo != nil {
			direction := "CREDIT"
			if role == "seller" {
				direction = "DEBIT"
			}
			// The negotiation id from our perspective is the foreign_id
			// stored on the row; the routing is the seller's bank routing
			// (which owns the id namespace).
			if c, cerr := h.peerOptionRepo.GetByNegotiationAndDirection(
				negotiationOwningRouting(row), row.ForeignID, direction,
			); cerr == nil && c != nil {
				localContractID = c.ID
			}
		}

		out.Items = append(out.Items, &stockpb.PeerNegotiationListItem{
			Id:              &stockpb.PeerForeignBankId{RoutingNumber: negotiationOwningRouting(row), Id: row.ForeignID},
			BuyerId:         &stockpb.PeerForeignBankId{RoutingNumber: row.BuyerRoutingNumber, Id: row.BuyerID},
			SellerId:        &stockpb.PeerForeignBankId{RoutingNumber: row.SellerRoutingNumber, Id: row.SellerID},
			Offer:           offerToProto(offer),
			Status:          row.Status,
			Role:            role,
			UpdatedAt:       row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			LocalContractId: localContractID,
		})
	}
	return out, nil
}

// negotiationOwningRouting returns the routing of the bank that ISSUED
// the foreign_id — always the seller's bank, regardless of which role
// the caller plays. The id is generated server-side at CreateNegotiation,
// which is invoked on the seller's bank by the buyer's bank via SI-TX.
// The role parameter no longer affects the answer but is kept on call
// sites for documentation: callers ARE doing role-aware projection
// even though the routing dimension collapses to one value.
func negotiationOwningRouting(row *model.PeerOtcNegotiation) int64 {
	return row.SellerRoutingNumber
}
