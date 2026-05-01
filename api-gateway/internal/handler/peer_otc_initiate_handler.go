package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerOTCInitiateHandler is the client-facing entry point for
// CREATING cross-bank OTC negotiations. The PeerOTCHandler in
// peer_otc_handler.go services the INBOUND side (peer banks call us);
// this handler is the OUTBOUND side (we call peers on behalf of a
// local client who wants to make an offer).
//
// Lookup goes via PeerBankAdminService.ResolvePeerByBankCode → peer
// base URL + API token + HMAC keys → HTTP POST to
// {peer}/api/v3/negotiations carrying the SI-TX OtcOffer payload.
type PeerOTCInitiateHandler struct {
	peerAdmin   transactionpb.PeerBankAdminServiceClient
	httpClient  *http.Client
	ownRouting  int64
	ownBankCode string
	hmacWindow  time.Duration
}

func NewPeerOTCInitiateHandler(peerAdmin transactionpb.PeerBankAdminServiceClient, ownRouting int64, ownBankCode string) *PeerOTCInitiateHandler {
	return &PeerOTCInitiateHandler{
		peerAdmin:   peerAdmin,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		ownRouting:  ownRouting,
		ownBankCode: ownBankCode,
		hmacWindow:  5 * time.Minute,
	}
}

// initiateNegotiationRequest is the body the client sends. The
// gateway lifts the buyer identity from the JWT (so clients can't
// impersonate someone else) and asks the peer for a fresh
// negotiation_id.
type initiateNegotiationRequest struct {
	SellerBankCode string `json:"seller_bank_code"`
	SellerID       string `json:"seller_id"`
	Stock          struct {
		Ticker string `json:"ticker"`
	} `json:"stock"`
	SettlementDate string `json:"settlement_date"`
	PricePerUnit   struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"price_per_unit"`
	Premium struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"premium"`
	Amount int64 `json:"amount"`
}

// CreatePeerNegotiation godoc
// @Summary      Initiate a cross-bank OTC negotiation
// @Description  Buyer-side client-facing endpoint. Composes an SI-TX OtcOffer with buyerId from the caller's JWT and sellerId from the body, then HTTP POSTs to the seller's bank's /api/v3/negotiations. Returns the foreign negotiation id assigned by the seller's bank.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body initiateNegotiationRequest true "negotiation terms"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/me/peer-otc/negotiations [post]
func (h *PeerOTCInitiateHandler) CreatePeerNegotiation(c *gin.Context) {
	var req initiateNegotiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.SellerBankCode == "" || req.SellerID == "" || req.Stock.Ticker == "" || req.Amount <= 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "seller_bank_code, seller_id, stock.ticker, amount are required")
		return
	}
	if req.SellerBankCode == h.ownBankCode {
		apiError(c, http.StatusBadRequest, ErrValidation, "seller_bank_code must be a peer bank, not own bank")
		return
	}

	// Buyer identity: caller's principal_id from the JWT.
	pid, ok := c.Get("principal_id")
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing principal id in token")
		return
	}
	pidInt, _ := pid.(int64)
	if pidInt <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "invalid principal id")
		return
	}
	buyerID := "client-" + strconv.FormatInt(pidInt, 10)

	// Resolve the peer.
	resp, err := h.peerAdmin.ResolvePeerByBankCode(c.Request.Context(), &transactionpb.ResolvePeerByBankCodeRequest{BankCode: req.SellerBankCode})
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrInternal, "resolve peer: "+err.Error())
		return
	}
	if !resp.GetFound() {
		apiError(c, http.StatusNotFound, ErrNotFound, "peer bank not registered")
		return
	}
	peer := resp.GetPeerBank()
	if !peer.GetActive() {
		apiError(c, http.StatusFailedDependency, ErrInternal, "peer bank is inactive")
		return
	}

	// Compose the SI-TX OtcOffer payload. Fields match the wire spec
	// at https://arsen.srht.site/si-tx-proto/. We also include a
	// premiumCurrency field because our internal MonetaryValue is
	// split-shape; the spec uses MonetaryValue {amount, currency} so
	// the receiver decodes it from `premium.amount` / `premium.currency`.
	offer := map[string]interface{}{
		"stock":          map[string]interface{}{"ticker": req.Stock.Ticker},
		"settlementDate": req.SettlementDate,
		"pricePerUnit":   map[string]interface{}{"amount": req.PricePerUnit.Amount, "currency": req.PricePerUnit.Currency},
		"premium":        map[string]interface{}{"amount": req.Premium.Amount, "currency": req.Premium.Currency},
		"buyerId":        map[string]interface{}{"routingNumber": h.ownRouting, "id": buyerID},
		"sellerId":       map[string]interface{}{"routingNumber": peer.GetRoutingNumber(), "id": req.SellerID},
		"amount":         req.Amount,
		"lastModifiedBy": map[string]interface{}{"routingNumber": h.ownRouting, "id": buyerID},
	}
	body, _ := json.Marshal(offer)

	// Construct the request to {peer.base_url}/negotiations.
	url := strings.TrimRight(peer.GetBaseUrl(), "/") + "/negotiations"
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrInternal, "build peer request: "+err.Error())
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Key", peer.GetApiTokenPlaintext())

	// Optional HMAC bundle (only when an outbound key is registered).
	if k := peer.GetHmacOutboundKey(); k != "" {
		nonce := make([]byte, 16)
		_, _ = rand.Read(nonce)
		ts := time.Now().UTC().Format(time.RFC3339)
		mac := hmac.New(sha256.New, []byte(k))
		mac.Write(body)
		httpReq.Header.Set("X-Bank-Code", h.ownBankCode)
		httpReq.Header.Set("X-Bank-Signature", hex.EncodeToString(mac.Sum(nil)))
		httpReq.Header.Set("X-Timestamp", ts)
		httpReq.Header.Set("X-Nonce", hex.EncodeToString(nonce))
	}

	httpResp, err := h.httpClient.Do(httpReq)
	if err != nil {
		apiError(c, http.StatusBadGateway, ErrInternal, "peer dispatch: "+err.Error())
		return
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusCreated && httpResp.StatusCode != http.StatusOK {
		apiError(c, httpResp.StatusCode, ErrInternal, fmt.Sprintf("peer rejected: %s", string(respBody)))
		return
	}

	// The peer returns ForeignBankId directly: {routingNumber, id}.
	var pr struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &pr); err != nil {
		apiError(c, http.StatusBadGateway, ErrInternal, "decode peer response: "+err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"routingNumber": pr.RoutingNumber,
		"id":            pr.ID,
	})
}
