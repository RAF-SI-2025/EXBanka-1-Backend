package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
)

// OTCOptionsHandler handles REST routes for the OTC options feature
// (intra-bank Celina 4 / Spec 2 + cross-bank Celina 5 SI-TX). The
// `client` is the intra-bank service; `peerOTC` is the cross-bank
// surface used by ExercisePeerContract. `security` resolves tickers to
// stock IDs; `accounts` backs the resource-ownership checks.
type OTCOptionsHandler struct {
	client   stockpb.OTCOptionsServiceClient
	peerOTC  stockpb.PeerOTCServiceClient
	security stockpb.SecurityGRPCServiceClient
	accounts accountpb.AccountServiceClient
}

func NewOTCOptionsHandler(
	client stockpb.OTCOptionsServiceClient,
	peerOTC stockpb.PeerOTCServiceClient,
	security stockpb.SecurityGRPCServiceClient,
	accounts accountpb.AccountServiceClient,
) *OTCOptionsHandler {
	return &OTCOptionsHandler{client: client, peerOTC: peerOTC, security: security, accounts: accounts}
}

type createOTCOfferRequest struct {
	Direction              string  `json:"direction"`
	Ticker                 string  `json:"ticker"`
	Quantity               string  `json:"quantity"`
	StrikePrice            string  `json:"strike_price"`
	Premium                string  `json:"premium"`
	SettlementDate         string  `json:"settlement_date"`
	AccountID              uint64  `json:"account_id"`
	CounterpartyUserID     *int64  `json:"counterparty_user_id,omitempty"`
	CounterpartySystemType *string `json:"counterparty_system_type,omitempty"`
	OnBehalfOfClientID     uint64  `json:"on_behalf_of_client_id,omitempty"`
}

// CreateOffer godoc
// @Summary      Create an OTC option offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createOTCOfferRequest true "offer details (ticker-keyed; account_id is the initiator's account)"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options [post]
func (h *OTCOptionsHandler) CreateOffer(c *gin.Context) {
	var req createOTCOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if _, err := oneOf("direction", req.Direction, "sell_initiated", "buy_initiated"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	if req.Ticker == "" || req.Quantity == "" || req.StrikePrice == "" || req.SettlementDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "ticker, quantity, strike_price and settlement_date are required")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.AccountID, req.OnBehalfOfClientID); err != nil {
		return
	}
	stock, err := h.security.GetStockByTicker(c.Request.Context(), &stockpb.GetStockByTickerRequest{Ticker: req.Ticker})
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "unknown ticker: "+req.Ticker)
		return
	}
	in := &stockpb.CreateOTCOfferRequest{
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		Direction:          req.Direction,
		StockId:            stock.Id,
		Quantity:           req.Quantity,
		StrikePrice:        req.StrikePrice,
		Premium:            req.Premium,
		SettlementDate:     req.SettlementDate,
		AccountId:          req.AccountID,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
		Ticker:             req.Ticker,
	}
	if req.CounterpartyUserID != nil && *req.CounterpartyUserID != 0 {
		stype := "client"
		if req.CounterpartySystemType != nil {
			stype = *req.CounterpartySystemType
		}
		in.Counterparty = &stockpb.PartyRef{UserId: *req.CounterpartyUserID, SystemType: stype}
	}
	resp, err := h.client.CreateOffer(c.Request.Context(), in)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"offer": resp})
}

// ListNegotiationHistory godoc
// @Summary      List the caller's terminal OTC negotiations (history)
// @Description  Returns OTC offers in terminal statuses (accepted, rejected, expired, failed). Filter by status, date range, and counterparty.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        status query []string false "ACCEPTED|REJECTED|EXPIRED|FAILED (repeatable)" collectionFormat(multi)
// @Param        since query string false "YYYY-MM-DD"
// @Param        until query string false "YYYY-MM-DD"
// @Param        counterparty_id query int false "owner id of the other party"
// @Param        page query int false "1-based page (default 1)"
// @Param        page_size query int false "1..100, default 20"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Router       /api/v3/me/otc/history [get]
func (h *OTCOptionsHandler) ListNegotiationHistory(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	statuses := c.QueryArray("status")
	for _, s := range statuses {
		if _, err := oneOf("status", s, "ACCEPTED", "REJECTED", "EXPIRED", "FAILED"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	var sinceUnix, untilUnix int64
	if v := c.Query("since"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "since must be YYYY-MM-DD")
			return
		}
		sinceUnix = t.UTC().Unix()
	}
	if v := c.Query("until"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "until must be YYYY-MM-DD")
			return
		}
		untilUnix = t.UTC().Unix()
	}
	if sinceUnix > 0 && untilUnix > 0 && sinceUnix > untilUnix {
		apiError(c, http.StatusBadRequest, ErrValidation, "since must be <= until")
		return
	}
	cp, _ := strconv.ParseUint(c.DefaultQuery("counterparty_id", "0"), 10, 64)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListNegotiationHistory(c.Request.Context(), &stockpb.ListNegotiationHistoryRequest{
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
		Statuses:        statuses,
		SinceUnix:       sinceUnix,
		UntilUnix:       untilUnix,
		CounterpartyId:  cp,
		Page:            int32(page),
		PageSize:        int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": resp.Offers, "total": resp.Total})
}

// ListMyOffers godoc
// @Summary      List the caller's OTC offers
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        role query string false "initiator|counterparty|either"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options [get]
func (h *OTCOptionsHandler) ListMyOffers(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListMyOffers(c.Request.Context(), &stockpb.ListMyOTCOffersRequest{
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
		Role:            c.DefaultQuery("role", "either"),
		Page:            int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": resp.Offers, "total": resp.Total})
}

// ListMyPostedOffers godoc
// @Summary      List every OTC option offer the caller has posted (any status)
// @Description  Returns the raw OTCOffer rows for listings where the caller is the initiator. Includes all statuses (open, consumed, accepted, rejected, expired, cancelled). Use this for a "my posted offers" history view. For the live-marketplace shape of my open involvement, use GET /api/v3/me/otc/options.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        statuses  query string false "comma-separated; omit for all"
// @Param        page      query int    false "1-based, default 1"
// @Param        page_size query int    false "default 20"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/posted [get]
func (h *OTCOptionsHandler) ListMyPostedOffers(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	var statuses []string
	if s := c.Query("statuses"); s != "" {
		statuses = splitCSV(s)
	}
	resp, err := h.client.ListMyOffers(c.Request.Context(), &stockpb.ListMyOTCOffersRequest{
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
		Role:            "initiator",
		Statuses:        statuses,
		Page:            int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": resp.Offers, "total": resp.Total})
}

// GetOffer godoc
// @Summary      Get an OTC offer with revisions
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "offer id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/options/{id} [get]
func (h *OTCOptionsHandler) GetOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.GetOffer(c.Request.Context(), &stockpb.GetOTCOfferRequest{
		OfferId:         id,
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type counterOTCOfferRequest struct {
	Quantity           string `json:"quantity"`
	StrikePrice        string `json:"strike_price"`
	Premium            string `json:"premium"`
	SettlementDate     string `json:"settlement_date"`
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
}

// CounterOffer — legacy single-chain counter handler. NOT ROUTED as of
// Phase 8 (the per-negotiation chain route at
// /api/v3/me/otc/options/:id/negotiations/:nid/counter replaces it).
// Method retained because the handler-layer test suite still exercises
// it; do not re-route without architecture review (frontends should
// only see the negotiation-chain routes).
func (h *OTCOptionsHandler) CounterOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req counterOTCOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.CounterOffer(c.Request.Context(), &stockpb.CounterOTCOfferRequest{
		OfferId:         id,
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
		Quantity:        req.Quantity, StrikePrice: req.StrikePrice, Premium: req.Premium,
		SettlementDate:     req.SettlementDate,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": resp})
}

type acceptOTCOfferRequest struct {
	AccountID          uint64 `json:"account_id"`
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
}

// AcceptOffer — legacy single-chain accept. NOT ROUTED as of Phase 8.
// Replaced by the per-chain route at
// /api/v3/me/otc/options/:id/negotiations/:nid/accept which is backed
// by the first-accept-wins TX in stock-service. Method retained for
// existing handler tests; do not re-route.
func (h *OTCOptionsHandler) AcceptOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req acceptOTCOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	if err := ResolveAndCheckAccount(c, h.accounts, identity, req.AccountID, req.OnBehalfOfClientID); err != nil {
		return
	}
	resp, err := h.client.AcceptOffer(c.Request.Context(), &stockpb.AcceptOTCOfferRequest{
		OfferId:            id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		AccountId:          req.AccountID,
		OnBehalfOfClientId: req.OnBehalfOfClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// RejectOffer — legacy single-chain reject. NOT ROUTED as of Phase 8.
// Replaced by /api/v3/me/otc/options/:id/negotiations/:nid/reject.
// Method retained for handler tests; do not re-route.
func (h *OTCOptionsHandler) RejectOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.RejectOffer(c.Request.Context(), &stockpb.RejectOTCOfferRequest{
		OfferId:         id,
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": resp})
}

// ListMyContracts godoc
// @Summary      List the caller's OTC contracts
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/contracts [get]
func (h *OTCOptionsHandler) ListMyContracts(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListMyContracts(c.Request.Context(), &stockpb.ListMyContractsRequest{
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
		Role:            c.DefaultQuery("role", "either"),
		Page:            int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"contracts":      resp.Contracts,
		"total":          resp.Total,
		"peer_contracts": resp.PeerContracts,
		"peer_total":     resp.PeerTotal,
	})
}

// GetContract godoc
// @Summary      Get an OTC contract
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "contract id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/contracts/{id} [get]
func (h *OTCOptionsHandler) GetContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.GetContract(c.Request.Context(), &stockpb.GetContractRequest{
		ContractId:      id,
		ActorUserId:     int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType: ownerToLegacySystemType(identity.OwnerType),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type exerciseRequest struct {
	OnBehalfOfClientID uint64 `json:"on_behalf_of_client_id,omitempty"`
	// OnBehalfOfFundID, when non-zero, exercises this contract on behalf of a fund (E2).
	// Caller must be the fund's manager (enforced in stock-service).
	OnBehalfOfFundID uint64 `json:"on_behalf_of_fund_id,omitempty"`
}

// ExerciseContract godoc
// @Summary      Exercise an OTC option contract
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "contract id"
// @Param        body body exerciseRequest false "optional on-behalf client id; accounts come from the contract"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/otc/contracts/{id}/exercise [post]
func (h *OTCOptionsHandler) ExerciseContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req exerciseRequest
	// Body is optional — only on_behalf_of_client_id may be present.
	_ = c.ShouldBindJSON(&req)
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.ExerciseContract(c.Request.Context(), &stockpb.ExerciseContractRequest{
		ContractId:         id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		OnBehalfOfClientId: req.OnBehalfOfClientID,
		OnBehalfOfFundId:   req.OnBehalfOfFundID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

type exercisePeerRequest struct {
	BuyerAccountNumber string `json:"buyer_account_number"`
}

// ExercisePeerContract godoc
// @Summary      Exercise a cross-bank OTC option contract
// @Description  Buyer-only. Initiates the SI-TX exercise flow: strike money buyer→seller + option markers carrying intent=exercise. Both banks transition the contract to status=exercised on COMMIT_TX, the seller's reservation is consumed and the buyer's holding is credited.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id    path int                  true "peer_option_contracts row id on this bank"
// @Param        body  body exercisePeerRequest  true "buyer's currency account number that pays the strike"
// @Success      200   {object} map[string]interface{}
// @Router       /api/v3/me/otc/contracts/peer/{id}/exercise [post]
func (h *OTCOptionsHandler) ExercisePeerContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req exercisePeerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.BuyerAccountNumber == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "buyer_account_number is required")
		return
	}
	resp, err := h.peerOTC.InitiateOptionExercise(c.Request.Context(), &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: id,
		BuyerAccountNumber:   req.BuyerAccountNumber,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"transaction_id": resp.GetTransactionId(),
		"status":         resp.GetStatus(),
	})
}

// --- OTC trader rating routes (Celina 3) ---------------------------------

type submitOTCRatingRequest struct {
	OfferID uint64 `json:"offer_id"`
	Score   int32  `json:"score"`
	Comment string `json:"comment"`
}

// SubmitRating godoc
// @Summary      Rate the counterparty of an ACCEPTED OTC offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body submitOTCRatingRequest true "offer_id + score (1..5) + optional comment"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v3/me/otc/ratings [post]
func (h *OTCOptionsHandler) SubmitRating(c *gin.Context) {
	var req submitOTCRatingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.OfferID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "offer_id is required")
		return
	}
	if req.Score < 1 || req.Score > 5 {
		apiError(c, http.StatusBadRequest, ErrValidation, "score must be 1..5")
		return
	}
	if len(req.Comment) > 1000 {
		apiError(c, http.StatusBadRequest, ErrValidation, "comment must be ≤ 1000 chars")
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	resp, err := h.client.SubmitRating(c.Request.Context(), &stockpb.SubmitOTCRatingRequest{
		OfferId:        req.OfferID,
		RaterOwnerType: identity.OwnerType,
		RaterOwnerId:   derefU64(identity.OwnerID),
		Score:          req.Score,
		Comment:        req.Comment,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"rating": resp})
}

// GetTraderProfile godoc
// @Summary      Get a trader's aggregate OTC rating + recent comments
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        owner_type path string true "client|bank"
// @Param        owner_id path int true "owner id (0 for bank)"
// @Param        recent_limit query int false "1..100, default 20"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/traders/{owner_type}/{owner_id}/rating [get]
func (h *OTCOptionsHandler) GetTraderProfile(c *gin.Context) {
	ownerType := c.Param("owner_type")
	if _, err := oneOf("owner_type", ownerType, "client", "bank"); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	ownerID, err := strconv.ParseUint(c.Param("owner_id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid owner_id")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("recent_limit", "20"))
	resp, err := h.client.GetTraderProfile(c.Request.Context(), &stockpb.GetTraderProfileRequest{
		OwnerType:   ownerType,
		OwnerId:     ownerID,
		RecentLimit: int32(limit),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListMyReceivedRatings godoc
// @Summary      List ratings the caller has received from OTC counterparties
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        limit query int false "1..100, default 20"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/ratings/received [get]
func (h *OTCOptionsHandler) ListMyReceivedRatings(c *gin.Context) {
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	resp, err := h.client.ListReceivedRatings(c.Request.Context(), &stockpb.ListReceivedRatingsRequest{
		OwnerType: identity.OwnerType,
		OwnerId:   derefU64(identity.OwnerID),
		Limit:     int32(limit),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ratings": resp.Ratings})
}
