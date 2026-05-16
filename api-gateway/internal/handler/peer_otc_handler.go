package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
)

// PeerOTCHandler serves the peer-facing OTC routes:
//
//	GET    /api/v3/public-stock
//	POST   /api/v3/negotiations
//	PUT    /api/v3/negotiations/:rid/:id
//	GET    /api/v3/negotiations/:rid/:id
//	DELETE /api/v3/negotiations/:rid/:id
//	GET    /api/v3/negotiations/:rid/:id/accept
//
// Auth is provided upstream by middleware.PeerAuth (sets peer_bank_code
// on the gin context). Dispatches to stock-service.PeerOTCService via gRPC.
type PeerOTCHandler struct {
	client stockpb.PeerOTCServiceClient
}

func NewPeerOTCHandler(c stockpb.PeerOTCServiceClient) *PeerOTCHandler {
	return &PeerOTCHandler{client: c}
}

func (h *PeerOTCHandler) GetPublicStocks(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	resp, err := h.client.GetPublicStocks(c.Request.Context(), &stockpb.GetPublicStocksRequest{
		PeerBankCode: peerCtxString(pbCode),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.GetStocks()))
	for _, s := range resp.GetStocks() {
		out = append(out, gin.H{
			"ownerId":       gin.H{"routingNumber": s.GetOwnerId().GetRoutingNumber(), "id": s.GetOwnerId().GetId()},
			"ticker":        s.GetTicker(),
			"amount":        s.GetAmount(),
			"pricePerStock": s.GetPricePerStock(),
			"currency":      s.GetCurrency(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"stocks": out})
}

// GetPublicOptionOffers godoc
// @Summary      Peer-facing list of OPEN OTC option listings on this bank
// @Description  Phase 6 cross-bank discovery. Returns this bank's
//
//	OPEN, undirected option listings as PeerPublicOptionOffer
//	rows in SI-TX shape. Auth via X-Api-Key (PeerAuth);
//	X-Bank-Code is stamped into peer_bank_code so privately-
//	targeted listings are filtered per-caller.
//
// @Tags         PeerOTC
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      501 {object} map[string]interface{} "OTCOfferReader not wired"
// @Router       /api/v3/public-option-offers [get]
func (h *PeerOTCHandler) GetPublicOptionOffers(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	resp, err := h.client.GetPublicOptionOffers(c.Request.Context(), &stockpb.GetPublicOptionOffersRequest{
		PeerBankCode: peerCtxString(pbCode),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.GetOffers()))
	for _, o := range resp.GetOffers() {
		out = append(out, gin.H{
			"offerId":         gin.H{"routingNumber": o.GetOfferId().GetRoutingNumber(), "id": o.GetOfferId().GetId()},
			"ticker":          o.GetTicker(),
			"amount":          o.GetAmount(),
			"strikePrice":     o.GetStrikePrice(),
			"strikeCurrency":  o.GetStrikeCurrency(),
			"premium":         o.GetPremium(),
			"premiumCurrency": o.GetPremiumCurrency(),
			"settlementDate":  o.GetSettlementDate(),
			"sellerId":        gin.H{"routingNumber": o.GetSellerId().GetRoutingNumber(), "id": o.GetSellerId().GetId()},
			"direction":       o.GetDirection(),
			"createdAt":       o.GetCreatedAt(),
			"lastModifiedBy":  gin.H{"routingNumber": o.GetLastModifiedBy().GetRoutingNumber(), "id": o.GetLastModifiedBy().GetId()},
		})
	}
	c.JSON(http.StatusOK, gin.H{"offers": out})
}

// peerForeignBankIdReq is the SI-TX ForeignBankId on the wire.
type peerForeignBankIdReq struct {
	RoutingNumber int64  `json:"routingNumber"`
	ID            string `json:"id"`
}

// peerMonetaryValueReq is the SI-TX MonetaryValue on the wire.
type peerMonetaryValueReq struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// peerStockDescriptionReq is the SI-TX StockDescription on the wire.
type peerStockDescriptionReq struct {
	Ticker string `json:"ticker"`
}

// peerOtcOfferReq matches the SI-TX OtcOffer shape verbatim:
//
//	type OtcOffer = {
//	    stock: StockDescription;
//	    settlementDate: ISO8601DateTimeWithTimeZone;
//	    pricePerUnit: MonetaryValue;
//	    premium: MonetaryValue;
//	    buyerId: ForeignBankId;
//	    sellerId: ForeignBankId;
//	    amount: number;
//	    lastModifiedBy: ForeignBankId;
//	}
//
// Body shape for POST /negotiations (initial offer) and
// PUT /negotiations/{rid}/{id} (counter-offer). The handler translates
// this spec shape into the internal flat-fielded gRPC request — buyerId
// and sellerId are lifted from the offer body to the gRPC request's
// top-level fields.
type peerOtcOfferReq struct {
	Stock          peerStockDescriptionReq `json:"stock"`
	SettlementDate string                  `json:"settlementDate"`
	PricePerUnit   peerMonetaryValueReq    `json:"pricePerUnit"`
	Premium        peerMonetaryValueReq    `json:"premium"`
	BuyerID        peerForeignBankIdReq    `json:"buyerId"`
	SellerID       peerForeignBankIdReq    `json:"sellerId"`
	Amount         int64                   `json:"amount"`
	LastModifiedBy peerForeignBankIdReq    `json:"lastModifiedBy"`
}

func (h *PeerOTCHandler) CreateNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	var off peerOtcOfferReq
	if err := c.ShouldBindJSON(&off); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if off.BuyerID.ID == "" || off.SellerID.ID == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "buyerId and sellerId are required")
		return
	}
	resp, err := h.client.CreateNegotiation(c.Request.Context(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: peerCtxString(pbCode),
		Offer:        offerReqToProto(off),
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: off.BuyerID.RoutingNumber, Id: off.BuyerID.ID},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: off.SellerID.RoutingNumber, Id: off.SellerID.ID},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"routingNumber": resp.GetNegotiationId().GetRoutingNumber(),
		"id":            resp.GetNegotiationId().GetId(),
	})
}

func (h *PeerOTCHandler) UpdateNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	rid, idStr, ok := parseRidID(c)
	if !ok {
		return
	}
	var req peerOtcOfferReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := h.client.UpdateNegotiation(c.Request.Context(), &stockpb.UpdateNegotiationRequest{
		PeerBankCode:  peerCtxString(pbCode),
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: rid, Id: idStr},
		Offer:         offerReqToProto(req),
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

func (h *PeerOTCHandler) GetNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	rid, idStr, ok := parseRidID(c)
	if !ok {
		return
	}
	resp, err := h.client.GetNegotiation(c.Request.Context(), &stockpb.GetNegotiationRequest{
		PeerBankCode:  peerCtxString(pbCode),
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: rid, Id: idStr},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	// SI-TX OtcNegotiation = OtcOffer & { isOngoing: boolean }. We compose
	// the response by merging the spec-shaped offer with one extra boolean,
	// derived from the internal status field (anything other than "ongoing"
	// means the negotiation is closed — accepted, cancelled, or expired).
	body := protoOfferToJSON(resp.GetOffer())
	body["buyerId"] = gin.H{"routingNumber": resp.GetBuyerId().GetRoutingNumber(), "id": resp.GetBuyerId().GetId()}
	body["sellerId"] = gin.H{"routingNumber": resp.GetSellerId().GetRoutingNumber(), "id": resp.GetSellerId().GetId()}
	body["isOngoing"] = resp.GetStatus() == "ongoing"
	c.JSON(http.StatusOK, body)
}

func (h *PeerOTCHandler) DeleteNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	rid, idStr, ok := parseRidID(c)
	if !ok {
		return
	}
	if _, err := h.client.DeleteNegotiation(c.Request.Context(), &stockpb.DeleteNegotiationRequest{
		PeerBankCode:  peerCtxString(pbCode),
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: rid, Id: idStr},
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *PeerOTCHandler) AcceptNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	rid, idStr, ok := parseRidID(c)
	if !ok {
		return
	}
	resp, err := h.client.AcceptNegotiation(c.Request.Context(), &stockpb.AcceptNegotiationRequest{
		PeerBankCode:  peerCtxString(pbCode),
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: rid, Id: idStr},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transactionId": resp.GetTransactionId(),
		"status":        resp.GetStatus(),
	})
}

func parseRidID(c *gin.Context) (int64, string, bool) {
	ridStr := c.Param("rid")
	rid, err := strconv.ParseInt(ridStr, 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid rid")
		return 0, "", false
	}
	id := c.Param("id")
	if id == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "missing id")
		return 0, "", false
	}
	return rid, id, true
}

// offerReqToProto translates the SI-TX wire shape (stock.ticker,
// pricePerUnit{amount,currency}, premium{amount,currency}, ...) into the
// internal flat-fielded gRPC PeerOtcOffer (ticker, pricePerStock,
// currency, premium, premiumCurrency, ...).
func offerReqToProto(o peerOtcOfferReq) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker:          o.Stock.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerUnit.Amount,
		Currency:        o.PricePerUnit.Currency,
		Premium:         o.Premium.Amount,
		PremiumCurrency: o.Premium.Currency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
	}
}

// protoOfferToJSON renders the internal flat-fielded gRPC PeerOtcOffer
// as the SI-TX OtcOffer wire shape (stock.ticker, pricePerUnit{amount,
// currency}, premium{amount,currency}, ...). Returned as gin.H so the
// caller can splice in additional fields like isOngoing or buyerId.
func protoOfferToJSON(o *stockpb.PeerOtcOffer) gin.H {
	if o == nil {
		return gin.H{}
	}
	return gin.H{
		"stock":          gin.H{"ticker": o.GetTicker()},
		"settlementDate": o.GetSettlementDate(),
		"pricePerUnit":   gin.H{"amount": o.GetPricePerStock(), "currency": o.GetCurrency()},
		"premium":        gin.H{"amount": o.GetPremium(), "currency": o.GetPremiumCurrency()},
		"amount":         o.GetAmount(),
		"lastModifiedBy": gin.H{"routingNumber": o.GetLastModifiedBy().GetRoutingNumber(), "id": o.GetLastModifiedBy().GetId()},
	}
}

// peerCtxString safely extracts a string from a gin context value
// retrieved via c.Get(). Used for the peer_bank_code value injected
// by middleware.PeerAuth.
func peerCtxString(v interface{}) string {
	s, _ := v.(string)
	return s
}
