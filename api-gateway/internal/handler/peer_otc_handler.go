package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
)

// PeerOTCHandler serves the peer-facing OTC routes under /api/v3/cross-bank-protocol:
//
//	GET    /api/v3/cross-bank-protocol/public-stock
//	POST   /api/v3/cross-bank-protocol/negotiations
//	PUT    /api/v3/cross-bank-protocol/negotiations/:rid/:id
//	GET    /api/v3/cross-bank-protocol/negotiations/:rid/:id
//	DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id
//	GET    /api/v3/cross-bank-protocol/negotiations/:rid/:id/accept
//
// Auth is provided upstream by middleware.PeerAuth (sets peer_bank_code
// on the gin context). Dispatches to stock-service.PeerOTCService via gRPC.
type PeerOTCHandler struct {
	client stockpb.PeerOTCServiceClient
}

func NewPeerOTCHandler(c stockpb.PeerOTCServiceClient) *PeerOTCHandler {
	return &PeerOTCHandler{client: c}
}

// GetPublicStocks godoc
// @Summary      Peer-to-peer: list public stock holdings
// @Description  Inbound from a peer bank. Returns this bank's holdings flagged public_quantity > 0 — the candidate OTC sellers a discovering bank can negotiate against. SI-TX §3.2.
// @Tags         PeerOTC
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/public-stock [get]
func (h *PeerOTCHandler) GetPublicStocks(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	resp, err := h.client.GetPublicStocks(c.Request.Context(), &stockpb.GetPublicStocksRequest{
		PeerBankCode: peerCtxString(pbCode),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	// §3.1 wire shape: bare array, sellers grouped by ticker.
	// The gRPC layer returns one PeerPublicStock per (owner, ticker) row;
	// aggregate into a map keyed by ticker to produce the spec shape.
	type seller struct {
		Seller gin.H `json:"seller"`
		Amount int64 `json:"amount"`
	}
	type publicStock struct {
		Stock   gin.H    `json:"stock"`
		Sellers []seller `json:"sellers"`
	}
	grouped := make(map[string]*publicStock)
	order := make([]string, 0)
	for _, s := range resp.GetStocks() {
		ticker := s.GetTicker()
		if _, ok := grouped[ticker]; !ok {
			grouped[ticker] = &publicStock{
				Stock:   gin.H{"ticker": ticker},
				Sellers: nil,
			}
			order = append(order, ticker)
		}
		grouped[ticker].Sellers = append(grouped[ticker].Sellers, seller{
			Seller: gin.H{"routingNumber": s.GetOwnerId().GetRoutingNumber(), "id": s.GetOwnerId().GetId()},
			Amount: s.GetAmount(),
		})
	}
	out := make([]publicStock, 0, len(order))
	for _, ticker := range order {
		out = append(out, *grouped[ticker])
	}
	c.JSON(http.StatusOK, out)
}

// peerForeignBankIdReq is the SI-TX ForeignBankId on the wire.
type peerForeignBankIdReq struct {
	RoutingNumber int64  `json:"routingNumber"`
	ID            string `json:"id"`
}

// peerMonetaryValueReq is the SI-TX MonetaryValue on the wire. Per
// SI-TX §2.5 the amount is a JSON number; DecimalNumber parses a number
// (and tolerates a quoted string from peers that still quote) without
// float64 rounding.
type peerMonetaryValueReq struct {
	Currency string             `json:"currency"`
	Amount   sitx.DecimalNumber `json:"amount"`
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
	// Phase 10 — cross-bank cascade-cancel grouping key. The bidder's bank
	// captures the discovered listing's (routingNumber, native_id) and sends
	// it here so the seller's bank can (1) correlate the inbound chain to the
	// listing it bids on — required for surfacing inbound chains on a
	// BANK-owned listing — and (2) cascade-cancel sibling chains on accept.
	// Optional; absent (zero routingNumber + empty id) means "no parent group".
	// Dropping it silently breaks both behaviors, so it MUST be forwarded.
	ParentOfferID *peerForeignBankIdReq `json:"parentOfferId,omitempty"`
	// Fix #1 (2026-05-16) — the buyer's 18-digit account number,
	// optionally pinned by the buyer's bank so the seller's bank uses
	// this exact account for the buyer-debit posting on accept.
	// Empty string ⇒ legacy path (participant-id resolution).
	BuyerAccountNumber string `json:"buyerAccountNumber,omitempty"`
}

// sitxForeignIDMaxBytes is the SI-TX §2.3 maximum length of a
// ForeignBankId.id field (and §2.2 IdempotenceKey.locallyGeneratedKey).
const sitxForeignIDMaxBytes = 64

// validateInboundOtcOffer enforces the SI-TX OtcOffer invariants that are
// the receiving bank's to enforce: currency codes must be ISO 4217 codes the
// bank supports, participant ids must respect the §2.3 ForeignBankId.id bound
// (non-empty, ≤ 64 bytes), and amount must be > 0. Returns a non-empty string
// with a human-readable reason on failure; the caller wraps it in apiError.
//
// We deliberately do NOT format-check the participant ids against a
// "client-<N>"/"employee-<N>" regex. Per SI-TX §2.3 a ForeignBankId.id is an
// OPAQUE string and "Banks (other than those whose routing numbers equal
// routingNumber) MUST NOT interpret the id string":
//   - buyerId.id belongs to the PEER (routingNumber = the authenticated peer).
//     It may be any opaque scheme (UUID, "acc-42", …); we store it verbatim
//     and round-trip it untouched, so the only valid checks are the §2.3
//     length bound (the prior regex was a spec violation that broke interop).
//   - sellerId.id is OURS (routingNumber must equal this bank — checked
//     downstream). Its real validation is that it RESOLVES to a local seller,
//     which the stock-service resolver (parseSellerOwner) already performs,
//     accepting "client-<N>"/"employee-<N>"/"bank". A non-resolvable seller id
//     surfaces as a clean 4xx from downstream — not a gateway format reject.
//
// (Supersedes Fix R6, 2026-05-16, which added the regex to protect downstream
// lookups; downstream now resolves/echoes safely, so the regex is dropped.)
func validateInboundOtcOffer(off peerOtcOfferReq) string {
	if !knownCurrency(off.PricePerUnit.Currency) {
		return "pricePerUnit.currency must be one of the bank's supported ISO 4217 codes"
	}
	if !knownCurrency(off.Premium.Currency) {
		return "premium.currency must be one of the bank's supported ISO 4217 codes"
	}
	if reason := validateForeignID("buyerId.id", off.BuyerID.ID); reason != "" {
		return reason
	}
	if reason := validateForeignID("sellerId.id", off.SellerID.ID); reason != "" {
		return reason
	}
	if off.Amount <= 0 {
		return "amount must be > 0"
	}
	return ""
}

// validateForeignID enforces the SI-TX §2.3 ForeignBankId.id bound on an
// opaque participant id: non-empty and at most 64 bytes. It does NOT
// interpret the id's internal structure (spec §2.3). Returns "" when valid.
func validateForeignID(field, id string) string {
	if id == "" {
		return field + " is required"
	}
	if len(id) > sitxForeignIDMaxBytes {
		return field + " must be at most 64 bytes (SI-TX §2.3)"
	}
	return ""
}

// knownCurrency mirrors account-service's SeedCurrencies set. Keep in
// sync (account-service is the SoR; this list is a defense-in-depth
// gate at the gateway so malformed peer requests fail fast instead of
// landing in account-service and getting rejected with currency_mismatch
// at settle time).
func knownCurrency(c string) bool {
	switch c {
	case "RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD":
		return true
	}
	return false
}

// CreateNegotiation godoc
// @Summary      Peer-to-peer: create OTC option negotiation
// @Description  Inbound from a peer bank's SI-TX layer. Authenticated via PeerAuth (X-Api-Key or HMAC). Persists a peer_otc_negotiations row keyed on (peer_bank_code, foreign_id). buyerId.routingNumber MUST match the authenticated peer's routing and sellerId.routingNumber MUST equal this bank.
// @Tags         PeerOTC
// @Accept       json
// @Produce      json
// @Param        body body peerOtcOfferReq true "SI-TX OtcOffer wire shape"
// @Success      201 {object} map[string]interface{} "ForeignBankId of the newly-created negotiation"
// @Failure      400 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/negotiations [post]
func (h *PeerOTCHandler) CreateNegotiation(c *gin.Context) {
	pbCode, _ := c.Get("peer_bank_code")
	var off peerOtcOfferReq
	if err := c.ShouldBindJSON(&off); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if reason := validateInboundOtcOffer(off); reason != "" {
		apiError(c, http.StatusBadRequest, ErrValidation, reason)
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

// UpdateNegotiation godoc
// @Summary      Peer-to-peer: counter-offer on an existing OTC negotiation
// @Description  Inbound from a peer bank. Updates the offer JSON on a peer_otc_negotiations row. Only the peer that created the row (matched by peer_bank_code) can update it — peer-auth + (peer_bank_code, negotiation_id) lookup combo enforces this.
// @Tags         PeerOTC
// @Accept       json
// @Produce      json
// @Param        rid path int true "routing number (this bank's, that issued the foreign_id)"
// @Param        id  path string true "foreign negotiation id"
// @Param        body body peerOtcOfferReq true "new SI-TX OtcOffer terms"
// @Success      200
// @Failure      400 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{} "out of turn or negotiation closed (SI-TX §3.3)"
// @Router       /api/v3/cross-bank-protocol/negotiations/{rid}/{id} [put]
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
	if reason := validateInboundOtcOffer(req); reason != "" {
		apiError(c, http.StatusBadRequest, ErrValidation, reason)
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

// GetNegotiation godoc
// @Summary      Peer-to-peer: read an OTC negotiation
// @Description  Inbound from a peer bank. Returns the full SI-TX OtcNegotiation record for the (peer_bank_code, foreign_id) pair.
// @Tags         PeerOTC
// @Produce      json
// @Param        rid path int true "routing number"
// @Param        id path string true "foreign negotiation id"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/negotiations/{rid}/{id} [get]
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

// DeleteNegotiation godoc
// @Summary      Peer-to-peer: cancel an OTC negotiation
// @Description  Inbound from a peer bank. Soft-cancels the negotiation row (status → cancelled). The row is preserved for audit per SI-TX §3.5.
// @Tags         PeerOTC
// @Produce      json
// @Param        rid path int true "routing number"
// @Param        id path string true "foreign negotiation id"
// @Success      204
// @Failure      401 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/negotiations/{rid}/{id} [delete]
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

// AcceptNegotiation godoc
// @Summary      Peer-to-peer: accept an OTC negotiation (triggers option-formation SI-TX)
// @Description  Inbound from the buyer's bank. The seller's bank composes the 4-posting NEW_TX (buyer-debit premium, seller-credit premium, seller-debit option asset, buyer-credit option asset) and dispatches via PeerTxService. SI-TX §3.6 specifies GET semantics.
// @Tags         PeerOTC
// @Produce      json
// @Param        rid path int true "routing number"
// @Param        id path string true "foreign negotiation id"
// @Success      200 {object} map[string]interface{} "transactionId + status"
// @Failure      401 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/negotiations/{rid}/{id}/accept [get]
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
	out := &stockpb.PeerOtcOffer{
		Ticker:          o.Stock.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerUnit.Amount.Decimal.String(),
		Currency:        o.PricePerUnit.Currency,
		Premium:         o.Premium.Amount.Decimal.String(),
		PremiumCurrency: o.Premium.Currency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
		BuyerAccountNumber: o.BuyerAccountNumber,
	}
	// Forward the cross-bank cascade-cancel grouping key when present. The
	// receiver treats a zero routing + empty id as "no parent group", so only
	// set it when the bidder actually supplied one.
	if o.ParentOfferID != nil && (o.ParentOfferID.RoutingNumber != 0 || o.ParentOfferID.ID != "") {
		out.ParentOfferId = &stockpb.PeerForeignBankId{
			RoutingNumber: o.ParentOfferID.RoutingNumber,
			Id:            o.ParentOfferID.ID,
		}
	}
	return out
}

// numJSON renders a decimal-string monetary amount as a bare JSON
// number token (SI-TX §2.5 requires monetary amounts to be JSON numbers,
// not quoted strings). encoding/json and gin emit json.RawMessage
// verbatim, so the validated decimal string lands unquoted. A malformed
// or empty string degrades to 0 rather than producing invalid JSON.
func numJSON(s string) json.RawMessage {
	if _, err := decimal.NewFromString(s); err != nil || s == "" {
		return json.RawMessage("0")
	}
	return json.RawMessage(s)
}

// protoOfferToJSON renders the internal flat-fielded gRPC PeerOtcOffer
// as the SI-TX OtcOffer wire shape (stock.ticker, pricePerUnit{amount,
// currency}, premium{amount,currency}, ...). Returned as gin.H so the
// caller can splice in additional fields like isOngoing or buyerId.
func protoOfferToJSON(o *stockpb.PeerOtcOffer) gin.H {
	if o == nil {
		return gin.H{}
	}
	out := gin.H{
		"stock":          gin.H{"ticker": o.GetTicker()},
		"settlementDate": o.GetSettlementDate(),
		"pricePerUnit":   gin.H{"amount": numJSON(o.GetPricePerStock()), "currency": o.GetCurrency()},
		"premium":        gin.H{"amount": numJSON(o.GetPremium()), "currency": o.GetPremiumCurrency()},
		"amount":         o.GetAmount(),
		"lastModifiedBy": gin.H{"routingNumber": o.GetLastModifiedBy().GetRoutingNumber(), "id": o.GetLastModifiedBy().GetId()},
	}
	if n := o.GetBuyerAccountNumber(); n != "" {
		out["buyerAccountNumber"] = n
	}
	if p := o.GetParentOfferId(); p != nil && (p.GetRoutingNumber() != 0 || p.GetId() != "") {
		out["parentOfferId"] = gin.H{"routingNumber": p.GetRoutingNumber(), "id": p.GetId()}
	}
	return out
}

// peerCtxString safely extracts a string from a gin context value
// retrieved via c.Get(). Used for the peer_bank_code value injected
// by middleware.PeerAuth.
func peerCtxString(v interface{}) string {
	s, _ := v.(string)
	return s
}
