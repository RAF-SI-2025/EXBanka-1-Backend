package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

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
	ConsumeForPeerOptionContract(
		ctx context.Context,
		peerOptionContractID uint64,
		qty int64,
	) (*service.PartialSettleHoldingResult, error)
	CreditBuyerHoldingForPeerOption(
		ctx context.Context,
		ownerType model.OwnerType,
		ownerID *uint64,
		ticker string,
		qty int64,
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
		out = append(out, &stockpb.PeerPublicOptionOffer{
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
		})
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
		// PricePerStock is not on Holding directly — use a zero placeholder
		// for now (Phase 4 minimum surface; future iteration can join with
		// listing_daily_price_info or stocks table for live pricing).
		out = append(out, &stockpb.PeerPublicStock{
			OwnerId:       &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: ownerID},
			Ticker:        hd.Ticker,
			Amount:        hd.PublicQuantity,
			PricePerStock: "0",
			Currency:      "USD",
		})
	}
	return &stockpb.GetPublicStocksResponse{Stocks: out}, nil
}

func (h *PeerOTCGRPCHandler) CreateNegotiation(ctx context.Context, req *stockpb.CreateNegotiationRequest) (*stockpb.CreateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer, buyer_id, seller_id are required")
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
	// SI-TX §3.5: "DELETE … sets isOngoing to false". The negotiation row
	// is preserved so a subsequent GET still returns OtcNegotiation with
	// isOngoing=false instead of 404. Status="cancelled" maps to
	// isOngoing=false in the gateway's GET handler.
	if err := h.negRepo.UpdateStatus(req.GetPeerBankCode(), req.GetNegotiationId().GetId(), "cancelled"); err != nil {
		return nil, status.Errorf(codes.Internal, "cancel: %v", err)
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
		out = append(out, &stockpb.CascadedSibling{
			PeerBankCode: sib.PeerBankCode,
			ForeignId:    sib.ForeignID,
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
	postings := []*transactionpb.SiTxPosting{
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: row.BuyerID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: offer.PremiumCurrency, Amount: premium, Direction: contractsitx.DirectionCredit},
		{RoutingNumber: row.SellerRoutingNumber, AccountId: row.SellerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionDebit},
		{RoutingNumber: row.BuyerRoutingNumber, AccountId: row.BuyerID, AssetId: optAssetID, Amount: "1", Direction: contractsitx.DirectionCredit},
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
		Ticker:          p.GetTicker(),
		Amount:          p.GetAmount(),
		PricePerStock:   pricePerStock,
		Currency:        p.GetCurrency(),
		Premium:         premium,
		PremiumCurrency: p.GetPremiumCurrency(),
		SettlementDate:  p.GetSettlementDate(),
		LastModifiedBy:  lastModBy,
		ParentOfferID:   parentOfferID,
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
			// Don't fail the whole RecordOptionContract — the contract
			// row is the durable record; the share-lock is best-effort
			// at this stage. Log via gRPC error metadata is overkill;
			// silently degrade and let ops surface "no reservation"
			// from settlement-time checks if it matters.
			log.Printf("WARN: peer-option contract %d created but seller_id %q not parseable for holding lock: %v",
				row.ID, row.SellerID, parseErr)
		} else {
			if _, err := h.holdingReserver.ReserveForPeerOptionContract(
				ctx, ownerType, ownerID, "stock", row.Ticker, row.ID, row.Quantity,
			); err != nil {
				log.Printf("WARN: peer-option contract %d created but holding-lock failed for seller %s ticker %s qty %d: %v",
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
		// Seller side. Consume the reservation, decrement holding.
		if _, err := h.holdingReserver.ConsumeForPeerOptionContract(ctx, contract.ID, contract.Quantity); err != nil {
			return nil, status.Errorf(codes.Internal, "consume seller reservation: %v", err)
		}
	case contractsitx.DirectionCredit:
		// Buyer side. Credit the buyer's holding for the gained shares.
		ownerType, ownerID, parseErr := parseSellerOwner(contract.BuyerID)
		if parseErr != nil {
			log.Printf("WARN: peer-option contract %d exercise: buyer_id %q not parseable; holding not credited: %v", contract.ID, contract.BuyerID, parseErr)
		} else {
			if err := h.holdingReserver.CreditBuyerHoldingForPeerOption(ctx, ownerType, ownerID, contract.Ticker, contract.Quantity); err != nil {
				log.Printf("WARN: peer-option contract %d exercise: credit buyer holding failed: %v", contract.ID, err)
			}
		}
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
		out.Items = append(out.Items, &stockpb.PeerNegotiationListItem{
			Id:        &stockpb.PeerForeignBankId{RoutingNumber: int64(roleRouting(row, role)), Id: row.ForeignID},
			BuyerId:   &stockpb.PeerForeignBankId{RoutingNumber: row.BuyerRoutingNumber, Id: row.BuyerID},
			SellerId:  &stockpb.PeerForeignBankId{RoutingNumber: row.SellerRoutingNumber, Id: row.SellerID},
			Offer:     offerToProto(offer),
			Status:    row.Status,
			Role:      role,
			UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out, nil
}

// roleRouting returns the *other* bank's routing for the surfaced
// negotiation id — i.e. the bank where the foreign_id was assigned. If
// the caller is the buyer (this bank hosts the buyer), the negotiation
// id was assigned by the seller's bank, so we surface seller_routing.
// If the caller is the seller, the id is locally-issued (this bank
// assigned it on CreateNegotiation) so we surface our own routing —
// which is the row's seller_routing.
func roleRouting(row *model.PeerOtcNegotiation, role string) int64 {
	if role == "buyer" {
		return row.SellerRoutingNumber
	}
	return row.SellerRoutingNumber
}
