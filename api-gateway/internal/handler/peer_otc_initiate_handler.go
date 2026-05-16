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

	"context"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
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
	peerOTC     stockpb.PeerOTCServiceClient // for RecordOutboundNegotiation + ListMyPeerNegotiations
	httpClient  *http.Client
	ownRouting  int64
	ownBankCode string
	hmacWindow  time.Duration
}

func NewPeerOTCInitiateHandler(peerAdmin transactionpb.PeerBankAdminServiceClient, peerOTC stockpb.PeerOTCServiceClient, ownRouting int64, ownBankCode string) *PeerOTCInitiateHandler {
	return &PeerOTCInitiateHandler{
		peerAdmin:   peerAdmin,
		peerOTC:     peerOTC,
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

	// Phase 10 — optional cross-bank cascade-cancel grouping key. When
	// the bidder discovered this listing via /public-option-offers,
	// they pass the listing's (routingNumber, id) here so the seller's
	// bank can group sibling chains and cascade-cancel them on accept.
	// Free-form bidders (no discovery) leave this unset; they're never
	// part of a sibling group.
	ParentOfferID *struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	} `json:"parent_offer_id,omitempty"`
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
	// Normalize seller_id to the SI-TX wire form ("client-<N>" / "employee-<N>"):
	// the seller's bank stores the row keyed on this string, and its own
	// /me/peer-otc/negotiations list query matches against "client-<jwt id>".
	// If the client sent a bare "1" the seller would never see the row.
	req.SellerID = normalizeSITXPrincipalID(req.SellerID)

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
	// Phase 10 — propagate the per-listing cascade-cancel key when the
	// bidder supplied one (discovered the listing via /public-option-
	// offers). Both banks' negotiation rows will store it; on accept,
	// the seller's bank cascade-cancels chains sharing the same key.
	if req.ParentOfferID != nil && req.ParentOfferID.ID != "" {
		offer["parentOfferId"] = map[string]interface{}{
			"routingNumber": req.ParentOfferID.RoutingNumber,
			"id":            req.ParentOfferID.ID,
		}
	}
	body, _ := json.Marshal(offer)

	// base_url already carries the peer's SI-TX path prefix (set by the
	// registering admin) so cohort banks with different gateway
	// layouts can all interop. We only append the leaf path.
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
	// Buyer-side mirror persistence: write a row into this bank's
	// peer_otc_negotiations table so the buyer can later list / drive
	// the negotiation via /me/peer-otc/negotiations. Best-effort —
	// failure here logs but does NOT roll back the peer-side creation
	// because the seller's bank already accepted; the user can resync
	// later by polling /api/v3/negotiations/:rid/:id from the gateway.
	if h.peerOTC != nil {
		// Build the local-mirror offer, including the parent_offer_id
		// when supplied so the buyer-side row carries the same cascade
		// grouping as the seller-side row stored on the peer bank.
		mirrorOffer := &stockpb.PeerOtcOffer{
			Ticker:          req.Stock.Ticker,
			Amount:          req.Amount,
			PricePerStock:   req.PricePerUnit.Amount,
			Currency:        req.PricePerUnit.Currency,
			Premium:         req.Premium.Amount,
			PremiumCurrency: req.Premium.Currency,
			SettlementDate:  req.SettlementDate,
			LastModifiedBy:  &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: buyerID},
		}
		if req.ParentOfferID != nil && req.ParentOfferID.ID != "" {
			mirrorOffer.ParentOfferId = &stockpb.PeerForeignBankId{
				RoutingNumber: req.ParentOfferID.RoutingNumber,
				Id:            req.ParentOfferID.ID,
			}
		}
		_, recErr := h.peerOTC.RecordOutboundNegotiation(c.Request.Context(), &stockpb.RecordOutboundNegotiationRequest{
			PeerBankCode:  req.SellerBankCode,
			NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: pr.RoutingNumber, Id: pr.ID},
			BuyerId:       &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: buyerID},
			SellerId:      &stockpb.PeerForeignBankId{RoutingNumber: peer.GetRoutingNumber(), Id: req.SellerID},
			Offer:         mirrorOffer,
		})
		if recErr != nil {
			// Surface to operator without failing the client request —
			// the peer already accepted.
			c.Writer.Header().Set("X-Mirror-Warning", "buyer-side persistence failed: "+recErr.Error())
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"routingNumber": pr.RoutingNumber,
		"id":            pr.ID,
	})
}

// ListMyPeerNegotiations godoc
// @Summary      List the caller's pending peer-OTC negotiations
// @Description  Returns rows from this bank's peer_otc_negotiations where the caller is either the buyer (this bank hosts the buyer) or the seller. Use `role=buyer` / `role=seller` to filter.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        role query string false "buyer|seller (default: both)"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/peer-otc/negotiations [get]
func (h *PeerOTCInitiateHandler) ListMyPeerNegotiations(c *gin.Context) {
	if h.peerOTC == nil {
		apiError(c, http.StatusServiceUnavailable, ErrInternal, "peer-otc client not wired")
		return
	}
	pid, _ := c.Get("principal_id")
	uid, ok := pid.(int64)
	if !ok || uid <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing principal id in token")
		return
	}
	role := c.Query("role")
	if role != "" && role != "buyer" && role != "seller" && role != "both" {
		apiError(c, http.StatusBadRequest, ErrValidation, "role must be buyer, seller, or both")
		return
	}
	resp, err := h.peerOTC.ListMyPeerNegotiations(c.Request.Context(), &stockpb.ListMyPeerNegotiationsRequest{
		OwnRoutingNumber: h.ownRouting,
		ClientId:         strconv.FormatInt(uid, 10),
		Role:             role,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": resp.GetItems()})
}

// proxyPeerNegotiation forwards a single-negotiation operation to the
// peer's /negotiations/{rid}/{id}[/...] route, using the same X-Api-Key
// + optional HMAC bundle as the init flow. The peerBankCode is the
// caller's COUNTERPARTY bank — for a buyer driving a negotiation, that's
// the seller's bank; for a seller responding, that's the buyer's bank.
//
// Returns the response body bytes + status code so callers can pass it
// through verbatim.
func (h *PeerOTCInitiateHandler) proxyPeerNegotiation(c *gin.Context, peerBankCode, rid, foreignID, method, subpath string, body []byte) ([]byte, int, error) {
	resolveResp, err := h.peerAdmin.ResolvePeerByBankCode(c.Request.Context(), &transactionpb.ResolvePeerByBankCodeRequest{BankCode: peerBankCode})
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("resolve peer: %w", err)
	}
	peer := resolveResp.GetPeerBank()
	if peer == nil || !peer.GetActive() {
		return nil, http.StatusFailedDependency, fmt.Errorf("peer bank %s inactive or unknown", peerBankCode)
	}
	url := strings.TrimRight(peer.GetBaseUrl(), "/") + "/negotiations/" + rid + "/" + foreignID + subpath
	httpReq, err := http.NewRequestWithContext(c.Request.Context(), method, url, bytes.NewReader(body))
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("X-Api-Key", peer.GetApiTokenPlaintext())
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
		return nil, http.StatusBadGateway, fmt.Errorf("peer dispatch: %w", err)
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(httpResp.Body)
	return respBody, httpResp.StatusCode, nil
}

// resolveMyRoleAndPeer figures out, for a (rid, id) negotiation row on
// this bank, whether the caller is the buyer or the seller and what
// the counterparty's bank code is. Returns ("", "", false) when the
// caller can't be matched — the route returns 404 in that case to
// avoid leaking existence.
func (h *PeerOTCInitiateHandler) resolveMyRoleAndPeer(ctx context.Context, callerPrincipalID int64, rid, foreignID string) (role, counterpartyBankCode string, ok bool) {
	if h.peerOTC == nil {
		return "", "", false
	}
	// List the caller's negotiations and look for a row whose foreign_id
	// matches the requested one. This is sufficient because the
	// (peer_bank_code, foreign_id) pair is globally unique within the
	// caller's negotiation set.
	resp, err := h.peerOTC.ListMyPeerNegotiations(ctx, &stockpb.ListMyPeerNegotiationsRequest{
		OwnRoutingNumber: h.ownRouting,
		ClientId:         strconv.FormatInt(callerPrincipalID, 10),
	})
	if err != nil {
		return "", "", false
	}
	for _, it := range resp.GetItems() {
		if it.GetId().GetId() != foreignID {
			continue
		}
		if rid != "" && strconv.FormatInt(it.GetId().GetRoutingNumber(), 10) != rid {
			continue
		}
		role = it.GetRole()
		// Counterparty bank code = the OTHER party's bank routing,
		// stringified. ResolvePeerByBankCode keys on bank_code which is
		// a 3-digit string equal to the routing number for SI-TX banks.
		if role == "buyer" {
			counterpartyBankCode = strconv.FormatInt(it.GetSellerId().GetRoutingNumber(), 10)
		} else {
			counterpartyBankCode = strconv.FormatInt(it.GetBuyerId().GetRoutingNumber(), 10)
		}
		return role, counterpartyBankCode, true
	}
	return "", "", false
}

// CounterPeerNegotiation godoc
// @Summary      Counter-offer on a cross-bank OTC negotiation
// @Description  Updates the negotiation offer on the counterparty's bank. Body is the same OtcOffer shape as the initial POST. The gateway proxies a PUT to the counterparty's /api/v3/negotiations/:rid/:id. The local mirror row is updated by the inbound webhook from the counterparty after they refetch.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        rid path string true "routing number of the bank that issued the negotiation id"
// @Param        id path string true "foreign id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/peer-otc/negotiations/{rid}/{id} [put]
func (h *PeerOTCInitiateHandler) CounterPeerNegotiation(c *gin.Context) {
	pid, _ := c.Get("principal_id")
	uid, ok := pid.(int64)
	if !ok || uid <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing principal id in token")
		return
	}
	rid := c.Param("rid")
	foreignID := c.Param("id")
	body, _ := io.ReadAll(c.Request.Body)
	_, peerBankCode, found := h.resolveMyRoleAndPeer(c.Request.Context(), uid, rid, foreignID)
	if !found {
		apiError(c, http.StatusNotFound, ErrNotFound, "negotiation not found")
		return
	}
	respBody, code, err := h.proxyPeerNegotiation(c, peerBankCode, rid, foreignID, http.MethodPut, "", body)
	if err != nil {
		apiError(c, code, ErrInternal, err.Error())
		return
	}
	// Mirror the counter on the caller's local row so their own list
	// reflects the new offer immediately. Best-effort — the
	// authoritative state lives on the counterparty's bank, and this
	// only updates the locally-readable copy. The local mirror row's
	// peer_bank_code is keyed by the counterparty.
	if h.peerOTC != nil && code >= 200 && code < 300 {
		offer, ok := parseCounterOfferBody(body)
		if ok {
			_, _ = h.peerOTC.UpdateNegotiation(c.Request.Context(), &stockpb.UpdateNegotiationRequest{
				PeerBankCode:  peerBankCode,
				NegotiationId: &stockpb.PeerForeignBankId{Id: foreignID},
				Offer:         offer,
			})
		}
	}
	c.Data(code, "application/json", respBody)
}

// normalizeSITXPrincipalID coerces a bare numeric id ("1") into the
// prefixed form the SI-TX wire spec requires for ForeignBankId.id
// ("client-1"). Already-prefixed values pass through unchanged. Empty
// strings stay empty so the caller's validation can reject them.
//
// Convention: bare numbers default to the "client-" prefix because
// the buyer-side initiate flow is overwhelmingly client-to-client.
// Callers who genuinely mean "employee-<N>" must send the prefixed
// form themselves — bare numbers are NEVER interpreted as employees.
func normalizeSITXPrincipalID(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return in
	}
	if strings.HasPrefix(in, "client-") || strings.HasPrefix(in, "employee-") {
		return in
	}
	if _, err := strconv.ParseInt(in, 10, 64); err == nil {
		return "client-" + in
	}
	return in
}

// parseCounterOfferBody pulls the SI-TX OtcOffer fields out of the
// camelCase JSON body that clients send to PUT /me/peer-otc/negotiations.
// Returns (nil, false) on parse failure so callers can fall back to "no
// local mirror update" without erroring the request.
func parseCounterOfferBody(body []byte) (*stockpb.PeerOtcOffer, bool) {
	var w struct {
		Stock struct {
			Ticker string `json:"ticker"`
		} `json:"stock"`
		Amount       int64 `json:"amount"`
		PricePerUnit struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"pricePerUnit"`
		Premium struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"premium"`
		SettlementDate string `json:"settlementDate"`
		LastModifiedBy struct {
			RoutingNumber int64  `json:"routingNumber"`
			ID            string `json:"id"`
		} `json:"lastModifiedBy"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, false
	}
	if w.Stock.Ticker == "" {
		return nil, false
	}
	return &stockpb.PeerOtcOffer{
		Ticker:          w.Stock.Ticker,
		Amount:          w.Amount,
		PricePerStock:   w.PricePerUnit.Amount,
		Currency:        w.PricePerUnit.Currency,
		Premium:         w.Premium.Amount,
		PremiumCurrency: w.Premium.Currency,
		SettlementDate:  w.SettlementDate,
		LastModifiedBy:  &stockpb.PeerForeignBankId{RoutingNumber: w.LastModifiedBy.RoutingNumber, Id: w.LastModifiedBy.ID},
	}, true
}

// AcceptPeerNegotiation godoc
// @Summary      Accept a cross-bank OTC negotiation
// @Description  Calls the counterparty's /api/v3/negotiations/:rid/:id/accept which begins the option-formation SI-TX (4-posting NEW_TX). Returns the transaction_id assigned by the counterparty.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        rid path string true "routing number"
// @Param        id path string true "foreign id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/peer-otc/negotiations/{rid}/{id}/accept [post]
func (h *PeerOTCInitiateHandler) AcceptPeerNegotiation(c *gin.Context) {
	pid, _ := c.Get("principal_id")
	uid, ok := pid.(int64)
	if !ok || uid <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing principal id in token")
		return
	}
	rid := c.Param("rid")
	foreignID := c.Param("id")
	_, peerBankCode, found := h.resolveMyRoleAndPeer(c.Request.Context(), uid, rid, foreignID)
	if !found {
		apiError(c, http.StatusNotFound, ErrNotFound, "negotiation not found")
		return
	}
	// The SI-TX accept endpoint is a GET (spec §3.6) returning the
	// transaction id.
	respBody, code, err := h.proxyPeerNegotiation(c, peerBankCode, rid, foreignID, http.MethodGet, "/accept", nil)
	if err != nil {
		apiError(c, code, ErrInternal, err.Error())
		return
	}
	// Flip the local mirror to status=accepted so the caller's
	// /me/peer-otc/negotiations list reflects the terminal state
	// immediately. The counterparty's row was already flipped by its
	// AcceptNegotiation peer handler when the SI-TX dispatched.
	var cancelledSiblings []*stockpb.CascadedSibling
	if h.peerOTC != nil && code >= 200 && code < 300 {
		_, _ = h.peerOTC.MarkNegotiationAccepted(c.Request.Context(), &stockpb.MarkNegotiationAcceptedRequest{
			PeerBankCode:  peerBankCode,
			NegotiationId: &stockpb.PeerForeignBankId{Id: foreignID},
		})

		// Phase 10 — cross-bank cascade-cancel. Ask stock-service for
		// every sibling chain that shares the just-accepted chain's
		// parent_offer_id (atomic per-listing key), cancel them
		// locally on this bank, AND fire outbound DELETE on each
		// bidder's bank so their mirrors flip to cancelled too.
		// Best-effort: failures here don't reverse the accept.
		if cresp, cerr := h.peerOTC.CascadeCancelSiblings(c.Request.Context(), &stockpb.CascadeCancelSiblingsRequest{
			PeerBankCode: peerBankCode,
			ForeignId:    foreignID,
		}); cerr == nil && cresp != nil {
			cancelledSiblings = cresp.GetSiblings()
			for _, sib := range cancelledSiblings {
				// proxyPeerNegotiation expects rid as the routing of
				// the bank that ISSUED the foreign_id — that's the
				// SIBLING's PeerBankCode from our perspective (the
				// other bidder's bank). DELETE to /negotiations/.../
				// at that bank flips their bidder-side mirror.
				_, _, _ = h.proxyPeerNegotiation(c, sib.GetPeerBankCode(), sib.GetPeerBankCode(), sib.GetForeignId(), http.MethodDelete, "", nil)
			}
		}
	}

	// Compose a response that mirrors the intra-bank accept shape
	// (winning + parent_offer_id + parent_status + cancelled_siblings
	// + contract): the business logic is different, the presentation
	// is the same. The peer's /accept body is { transactionId,
	// status } — we splat those keys in alongside cancelled_siblings
	// so a single FE component handles both flows.
	var peerBody map[string]any
	if perr := json.Unmarshal(respBody, &peerBody); perr != nil || peerBody == nil {
		peerBody = map[string]any{}
	}
	peerBody["cancelled_siblings"] = cancelledSiblings
	c.JSON(code, peerBody)
}

// CancelPeerNegotiation godoc
// @Summary      Cancel a cross-bank OTC negotiation
// @Description  Soft-cancels the negotiation on the counterparty's bank (DELETE flips isOngoing to false; the row is preserved per SI-TX §3.5). The local mirror is updated by the inbound webhook from the counterparty after they refetch.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        rid path string true "routing number"
// @Param        id path string true "foreign id"
// @Success      204 {string} string ""
// @Router       /api/v3/me/peer-otc/negotiations/{rid}/{id} [delete]
func (h *PeerOTCInitiateHandler) CancelPeerNegotiation(c *gin.Context) {
	pid, _ := c.Get("principal_id")
	uid, ok := pid.(int64)
	if !ok || uid <= 0 {
		apiError(c, http.StatusUnauthorized, ErrUnauthorized, "missing principal id in token")
		return
	}
	rid := c.Param("rid")
	foreignID := c.Param("id")
	_, peerBankCode, found := h.resolveMyRoleAndPeer(c.Request.Context(), uid, rid, foreignID)
	if !found {
		apiError(c, http.StatusNotFound, ErrNotFound, "negotiation not found")
		return
	}
	_, code, err := h.proxyPeerNegotiation(c, peerBankCode, rid, foreignID, http.MethodDelete, "", nil)
	if err != nil {
		apiError(c, code, ErrInternal, err.Error())
		return
	}
	// Mirror the cancel on the local row so the caller's list shows
	// status=cancelled immediately. The local DeleteNegotiation handler
	// flips status to "cancelled" without physically deleting the row
	// (matching the peer-protocol semantics).
	if h.peerOTC != nil && code >= 200 && code < 300 {
		_, _ = h.peerOTC.DeleteNegotiation(c.Request.Context(), &stockpb.DeleteNegotiationRequest{
			PeerBankCode:  peerBankCode,
			NegotiationId: &stockpb.PeerForeignBankId{Id: foreignID},
		})
	}
	c.Status(code)
}
