// Package handler — gateway routes for OTC option negotiation chains
// (Phase 2 marketplace; see
// docs/superpowers/plans/2026-05-16-otc-options-marketplace.md).
//
// Methods are attached to the existing OTCOptionsHandler so they share
// the OTCOptionsServiceClient + identity middleware wiring. The new
// routes:
//
//	POST   /api/v3/me/otc/options                       Create listing
//	POST   /api/v3/otc/options/:id/bid                  Open negotiation chain
//	GET    /api/v3/me/otc/options/negotiations          List my chains
//	POST   /api/v3/me/otc/options/:id/negotiations/:nid/counter
//	POST   /api/v3/me/otc/options/:id/negotiations/:nid/accept
//	POST   /api/v3/me/otc/options/:id/negotiations/:nid/reject
//	DELETE /api/v3/me/otc/options/:id/negotiations/:nid
//	DELETE /api/v3/me/otc/options/:id                   Cancel my listing
//
// All routes use AnyAuthMiddleware (client + employee tokens accepted)
// + ResolveIdentity. Ownership/authorization is enforced inside the
// service-layer first-accept-wins TX in stock-service.
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	stockpb "github.com/exbanka/contract/stockpb"
)

// ---------- request bodies ----------

type openNegotiationRequest struct {
	BidderAccountID uint64 `json:"bidder_account_id"`
	Quantity        string `json:"quantity"`
	StrikePrice     string `json:"strike_price"`
	Premium         string `json:"premium"`
	SettlementDate  string `json:"settlement_date"`
}

type counterNegotiationRequest struct {
	Quantity       string `json:"quantity"`
	StrikePrice    string `json:"strike_price"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlement_date"`
}

// ---------- handlers ----------

// OpenNegotiationChain godoc
// @Summary      Place a bid on an OTC option listing (opens a negotiation chain)
// @Description  Many bidders can each open their own chain against the same listing. First to accept wins atomically; siblings cascade-cancel.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "parent OTCOffer listing id"
// @Param        body body openNegotiationRequest true "initial bid terms + bidder's account"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{} "chain already open for caller on this listing"
// @Router       /api/v3/otc/options/{id}/bid [post]
func (h *OTCOptionsHandler) OpenNegotiationChain(c *gin.Context) {
	parentID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || parentID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req openNegotiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Quantity == "" || req.StrikePrice == "" || req.SettlementDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "quantity, strike_price, settlement_date are required")
		return
	}
	if req.BidderAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "bidder_account_id is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// Verify the bidder's account belongs to them before forwarding.
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.BidderAccountID, 0); err != nil {
		return
	}
	resp, err := h.client.OpenNegotiation(c.Request.Context(), &stockpb.OpenNegotiationRequest{
		ParentOfferId:       parentID,
		BidderOwnerType:     identity.OwnerType,
		BidderOwnerId:       derefU64(identity.OwnerID),
		BidderAccountId:     req.BidderAccountID,
		Quantity:            req.Quantity,
		StrikePrice:         req.StrikePrice,
		Premium:             req.Premium,
		SettlementDate:      req.SettlementDate,
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"negotiation": resp})
}

// CounterMyNegotiation godoc
// @Summary      Counter on one of your OTC option negotiation chains
// @Description  Either the bidder or the listing's poster may counter. Updates the chain's terms; status flips to "countered".
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "parent listing id (sanity check; not used)"
// @Param        nid path int true "negotiation chain id"
// @Param        body body counterNegotiationRequest true "new terms"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/{id}/negotiations/{nid}/counter [post]
func (h *OTCOptionsHandler) CounterMyNegotiation(c *gin.Context) {
	negID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || negID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid nid")
		return
	}
	var req counterNegotiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Quantity == "" || req.StrikePrice == "" || req.SettlementDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "quantity, strike_price, settlement_date are required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.CounterNegotiation(c.Request.Context(), &stockpb.CounterNegotiationRequest{
		NegotiationId:       negID,
		CallerOwnerType:     identity.OwnerType,
		CallerOwnerId:       derefU64(identity.OwnerID),
		Quantity:            req.Quantity,
		StrikePrice:         req.StrikePrice,
		Premium:             req.Premium,
		SettlementDate:      req.SettlementDate,
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"negotiation": resp})
}

type acceptNegotiationRequest struct {
	AcceptorAccountID uint64 `json:"acceptor_account_id"`
}

// AcceptMyNegotiation godoc
// @Summary      Accept the current terms on an OTC option negotiation chain
// @Description  Caller must be the party OPPOSITE to whoever proposed the current terms. After the negotiation state TX (which flips this chain to "accepted", parent to "consumed", and cascade-cancels every sibling chain), the contract-formation saga runs: mints OptionContract from the negotiation's snapshot terms, reserves seller's underlying shares, reserves+settles buyer's premium, credits the seller. If the saga fails (e.g. seller no longer has the shares, buyer is short on premium), the negotiation flips to "failed" and the parent stays consumed.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "parent listing id"
// @Param        nid path int true "negotiation chain id"
// @Param        body body acceptNegotiationRequest true "acceptor_account_id — caller's account that pays the premium (if accepter is the buyer) or receives it (if accepter is the seller)"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{} "acceptor_account_id missing or not owned"
// @Failure      403 {object} map[string]interface{} "caller proposed current terms or not a party"
// @Failure      409 {object} map[string]interface{} "parent listing already consumed"
// @Failure      412 {object} map[string]interface{} "contract-formation saga rejected (seller short on shares OR buyer short on premium)"
// @Router       /api/v3/me/otc/options/{id}/negotiations/{nid}/accept [post]
func (h *OTCOptionsHandler) AcceptMyNegotiation(c *gin.Context) {
	negID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || negID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid nid")
		return
	}
	var req acceptNegotiationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.AcceptorAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "acceptor_account_id is required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// Verify the acceptor's account belongs to them BEFORE forwarding —
	// the service-side saga would reject too, but front-end gets a
	// cleaner 403 vs an opaque InvalidArgument.
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.AcceptorAccountID, 0); err != nil {
		return
	}
	resp, err := h.client.AcceptNegotiationChain(c.Request.Context(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       negID,
		CallerOwnerType:     identity.OwnerType,
		CallerOwnerId:       derefU64(identity.OwnerID),
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
		AcceptorAccountId:   req.AcceptorAccountID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"winning":            resp.GetWinning(),
		"parent_offer_id":    resp.GetParentOfferId(),
		"parent_status":      resp.GetParentStatus(),
		"cancelled_siblings": resp.GetCancelledSiblings(),
		"contract":           resp.GetContract(),
	})
}

// RejectMyNegotiation godoc
// @Summary      Reject an OTC option negotiation chain
// @Description  Either party may reject. The chain ends without forming a contract; parent listing stays open.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id  path int true "parent listing id (sanity check)"
// @Param        nid path int true "negotiation chain id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/{id}/negotiations/{nid}/reject [post]
func (h *OTCOptionsHandler) RejectMyNegotiation(c *gin.Context) {
	negID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || negID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid nid")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.RejectNegotiation(c.Request.Context(), &stockpb.RejectNegotiationRequest{
		NegotiationId:       negID,
		CallerOwnerType:     identity.OwnerType,
		CallerOwnerId:       derefU64(identity.OwnerID),
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"negotiation": resp})
}

// CancelMyNegotiation godoc
// @Summary      Cancel (withdraw) your own OTC option negotiation chain
// @Description  Bidder-only — the listing's poster cannot cancel a bidder's chain (use reject instead).
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id  path int true "parent listing id (sanity check)"
// @Param        nid path int true "negotiation chain id"
// @Success      204 {string} string ""
// @Router       /api/v3/me/otc/options/{id}/negotiations/{nid} [delete]
func (h *OTCOptionsHandler) CancelMyNegotiation(c *gin.Context) {
	negID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || negID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid nid")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if _, err := h.client.CancelNegotiation(c.Request.Context(), &stockpb.CancelNegotiationRequest{
		NegotiationId:       negID,
		CallerOwnerType:     identity.OwnerType,
		CallerOwnerId:       derefU64(identity.OwnerID),
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// CancelMyListing godoc
// @Summary      Cancel (withdraw) your own OTC option listing
// @Description  Initiator-only. Status flips to "cancelled" and all open child negotiation chains cascade-cancel. Returns 204 on success, 403 if caller is not the listing's poster, 409 if listing is not open.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "parent listing id"
// @Success      204 {string} string ""
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/{id} [delete]
func (h *OTCOptionsHandler) CancelMyListing(c *gin.Context) {
	offerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || offerID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// Gateway-level ownership pre-check: fetch the offer and verify the
	// caller is the initiator. The service-layer also checks (defense in
	// depth) but per CLAUDE.md ownership must be verified before the gRPC
	// call. GetOffer returns NotFound for non-participants which gives
	// us a clean 404 for "offer doesn't exist or isn't mine".
	detail, gerr := h.client.GetOffer(c.Request.Context(), &stockpb.GetOTCOfferRequest{
		OfferId:         offerID,
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if gerr != nil {
		handleGRPCError(c, gerr)
		return
	}
	off := detail.GetOffer()
	if off == nil {
		apiError(c, http.StatusNotFound, ErrNotFound, "offer not found")
		return
	}
	init := off.GetInitiator()
	if init == nil ||
		init.GetSystemType() != ownerToLegacySystemType(identity.OwnerType) ||
		uint64(init.GetUserId()) != ownerToLegacyUserID(identity.OwnerID) {
		apiError(c, http.StatusForbidden, ErrForbidden, "only the listing's poster can cancel it")
		return
	}
	if _, err := h.client.CancelListing(c.Request.Context(), &stockpb.CancelListingRequest{
		OfferId:             offerID,
		CallerOwnerType:     identity.OwnerType,
		CallerOwnerId:       derefU64(identity.OwnerID),
		ActingPrincipalType: principalTypeFromIdentity(identity),
		ActingPrincipalId:   identity.PrincipalID,
		ActingEmployeeId:    derefU64(identity.ActingEmployeeID),
	}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMyNegotiations godoc
// @Summary      List the caller's OTC option negotiation chains
// @Description  Returns chains where the caller is the bidder. Filter with `?statuses=open,countered,accepted,rejected,cancelled,expired`.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        statuses  query string false "comma-separated; omit for all"
// @Param        page      query int    false "1-based, default 1"
// @Param        page_size query int    false "default 20, max 200"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/negotiations [get]
func (h *OTCOptionsHandler) ListMyNegotiations(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize > 200 {
		pageSize = 200
	}
	var statuses []string
	if s := c.Query("statuses"); s != "" {
		statuses = splitCSV(s)
	}
	resp, err := h.client.ListMyNegotiations(c.Request.Context(), &stockpb.ListMyNegotiationsRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
		Statuses:  statuses,
		Page:      int32(page),
		PageSize:  int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"negotiations": resp.GetNegotiations(),
		"total":        resp.GetTotal(),
	})
}

// ListMyNegotiationRevisions godoc
// @Summary      List the full revision chain of an OTC option negotiation
// @Description  Returns all bid/counter/accept/reject revisions for a negotiation chain in revision_number order. Caller must be either the bidder or the parent listing's poster.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        nid path int true "negotiation chain id"
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{} "caller is not a party to this negotiation"
// @Failure      404 {object} map[string]interface{} "negotiation not found"
// @Router       /api/v3/me/otc/options/negotiations/{nid}/revisions [get]
func (h *OTCOptionsHandler) ListMyNegotiationRevisions(c *gin.Context) {
	negID, err := strconv.ParseUint(c.Param("nid"), 10, 64)
	if err != nil || negID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid nid")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ListNegotiationRevisions(c.Request.Context(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId:   negID,
		CallerOwnerType: identity.OwnerType,
		CallerOwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revisions": resp.GetRevisions()})
}

// ListNegotiationsOnListing godoc
// @Summary      List every negotiation chain against a given OTC option listing
// @Description  Used by the listing's poster to see all incoming bids. Returns chains in any status (active + terminal).
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "parent OTCOffer listing id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/options/{id}/negotiations [get]
func (h *OTCOptionsHandler) ListNegotiationsOnListing(c *gin.Context) {
	parentID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || parentID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	resp, err := h.client.ListNegotiationsByListing(c.Request.Context(), &stockpb.ListNegotiationsByListingRequest{
		ParentOfferId: parentID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"negotiations": resp.GetNegotiations(),
		"total":        resp.GetTotal(),
	})
}

// ---------- small helpers ----------

// principalTypeFromIdentity maps the resolved identity's principal kind
// onto the "client" / "employee" strings the service-layer expects in
// LastActionByPrincipalType. Bank acting via employee → "employee";
// client → "client".
func principalTypeFromIdentity(id *middleware.ResolvedIdentity) string {
	if id.ActingEmployeeID != nil {
		return "employee"
	}
	return "client"
}

func splitCSV(s string) []string {
	out := make([]string, 0, 4)
	cur := ""
	for _, ch := range s {
		if ch == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
