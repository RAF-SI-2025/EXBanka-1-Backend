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

type peerOtcOfferReq struct {
	Ticker          string `json:"ticker"`
	Amount          int64  `json:"amount"`
	PricePerStock   string `json:"pricePerStock"`
	Currency        string `json:"currency"`
	Premium         string `json:"premium"`
	PremiumCurrency string `json:"premiumCurrency"`
	SettlementDate  string `json:"settlementDate"`
	LastModifiedBy  struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	} `json:"lastModifiedBy"`
}

type createNegotiationReq struct {
	Offer   peerOtcOfferReq `json:"offer"`
	BuyerID struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	} `json:"buyerId"`
	SellerID struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	} `json:"sellerId"`
}

func (h *PeerOTCHandler) CreateNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	var req createNegotiationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	resp, err := h.client.CreateNegotiation(c.Request.Context(), &stockpb.CreateNegotiationRequest{
		PeerBankCode: peerCtxString(pbCode),
		Offer:        offerReqToProto(req.Offer),
		BuyerId:      &stockpb.PeerForeignBankId{RoutingNumber: req.BuyerID.RoutingNumber, Id: req.BuyerID.ID},
		SellerId:     &stockpb.PeerForeignBankId{RoutingNumber: req.SellerID.RoutingNumber, Id: req.SellerID.ID},
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"negotiationId": gin.H{"routingNumber": resp.GetNegotiationId().GetRoutingNumber(), "id": resp.GetNegotiationId().GetId()},
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
	c.JSON(http.StatusOK, gin.H{
		"id":        gin.H{"routingNumber": resp.GetId().GetRoutingNumber(), "id": resp.GetId().GetId()},
		"buyerId":   gin.H{"routingNumber": resp.GetBuyerId().GetRoutingNumber(), "id": resp.GetBuyerId().GetId()},
		"sellerId":  gin.H{"routingNumber": resp.GetSellerId().GetRoutingNumber(), "id": resp.GetSellerId().GetId()},
		"offer":     protoOfferToJSON(resp.GetOffer()),
		"status":    resp.GetStatus(),
		"updatedAt": resp.GetUpdatedAt(),
	})
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

func offerReqToProto(o peerOtcOfferReq) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker:          o.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerStock,
		Currency:        o.Currency,
		Premium:         o.Premium,
		PremiumCurrency: o.PremiumCurrency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
	}
}

func protoOfferToJSON(o *stockpb.PeerOtcOffer) gin.H {
	if o == nil {
		return gin.H{}
	}
	return gin.H{
		"ticker":          o.GetTicker(),
		"amount":          o.GetAmount(),
		"pricePerStock":   o.GetPricePerStock(),
		"currency":        o.GetCurrency(),
		"premium":         o.GetPremium(),
		"premiumCurrency": o.GetPremiumCurrency(),
		"settlementDate":  o.GetSettlementDate(),
		"lastModifiedBy":  gin.H{"routingNumber": o.GetLastModifiedBy().GetRoutingNumber(), "id": o.GetLastModifiedBy().GetId()},
	}
}

// peerCtxString safely extracts a string from a gin context value
// retrieved via c.Get(). Used for the peer_bank_code value injected
// by middleware.PeerAuth.
func peerCtxString(v interface{}) string {
	s, _ := v.(string)
	return s
}
