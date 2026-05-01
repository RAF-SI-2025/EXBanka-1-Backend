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
// PeerOTCGRPCHandler needs for GetPublicStocks. Decoupled for testability.
type HoldingReader interface {
	ListPublic() ([]model.Holding, error)
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
// RecordOptionContract to lock the seller's underlying shares against
// a cross-bank option contract on the seller's bank.
type HoldingReserver interface {
	ReserveForPeerOptionContract(
		ctx context.Context,
		sellerOwnerType model.OwnerType,
		sellerOwnerID *uint64,
		securityType, ticker string,
		peerOptionContractID uint64,
		qty int64,
	) (*service.ReserveHoldingResult, error)
}

type PeerOTCGRPCHandler struct {
	stockpb.UnimplementedPeerOTCServiceServer
	negRepo         *repository.PeerOtcNegotiationRepository
	peerOptionRepo  *repository.PeerOptionContractRepository
	holdings        HoldingReader
	peerTx          transactionpb.PeerTxServiceClient
	ownRouting      int64
	holdingReserver HoldingReserver // optional; nil disables seller-side share locking
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
	return contractsitx.OtcOffer{
		Ticker:          p.GetTicker(),
		Amount:          p.GetAmount(),
		PricePerStock:   pricePerStock,
		Currency:        p.GetCurrency(),
		Premium:         premium,
		PremiumCurrency: p.GetPremiumCurrency(),
		SettlementDate:  p.GetSettlementDate(),
		LastModifiedBy:  lastModBy,
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
	}
}

// RecordOptionContract is called by transaction-service at COMMIT_TX
// time for each option-asset posting on this bank's routing. The
// option_description_json is the verbatim assetId string from the
// SI-TX posting (a JSON OptionDescription); we decode the contract
// terms and persist a peer_option_contracts row keyed on
// (crossbank_tx_id, posting_index) for idempotent retry safety.
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
