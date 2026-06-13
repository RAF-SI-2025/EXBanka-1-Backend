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
// `client` is the intra-bank service; cross-bank dispatch (e.g. exercise)
// is decided inside stock-service behind the unified routes, so the
// gateway no longer holds a PeerOTC client here. `security` resolves
// tickers to stock IDs; `accounts` backs the resource-ownership checks.
type OTCOptionsHandler struct {
	client   stockpb.OTCOptionsServiceClient
	security stockpb.SecurityGRPCServiceClient
	accounts accountpb.AccountServiceClient
}

func NewOTCOptionsHandler(
	client stockpb.OTCOptionsServiceClient,
	security stockpb.SecurityGRPCServiceClient,
	accounts accountpb.AccountServiceClient,
) *OTCOptionsHandler {
	return &OTCOptionsHandler{client: client, security: security, accounts: accounts}
}

type createOTCOfferRequest struct {
	Direction              string  `json:"direction"`
	Ticker                 string  `json:"ticker"`
	Quantity               string  `json:"quantity"`
	AccountID              uint64  `json:"account_id"`
	CounterpartyUserID     *int64  `json:"counterparty_user_id,omitempty"`
	CounterpartySystemType *string `json:"counterparty_system_type,omitempty"`
	OnBehalfOfClientID     uint64  `json:"on_behalf_of_client_id,omitempty"`
}

// CreateOffer godoc
// @Summary      Create an OTC option offer
// @Description  Posts an open OTC option offer. Terms (strike_price, premium, settlement_date) are NOT set at creation — they are agreed during negotiation. Only one open offer per (owner, ticker, direction) is allowed; a duplicate returns 409.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createOTCOfferRequest true "offer details (ticker-keyed, open terms; account_id is the initiator's account)"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
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
	if req.Ticker == "" || req.Quantity == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "ticker and quantity are required")
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
	// Terms (strike_price/premium/settlement_date) are intentionally not set:
	// offers are posted open and the terms are agreed during negotiation. The
	// proto still carries those fields (cleanup is a later task); we simply
	// leave them unset here.
	in := &stockpb.CreateOTCOfferRequest{
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		ActingEmployeeId:   derefU64(identity.ActingEmployeeID),
		Direction:          req.Direction,
		StockId:            stock.Id,
		Quantity:           req.Quantity,
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

type updateOTCOptionRequest struct {
	Quantity string `json:"quantity"`
}

// UpdateMyOption godoc
// @Summary      Edit the total quantity of one of the caller's OTC option offers
// @Description  Sets the TOTAL quantity of an open option offer the caller owns (up or down). Option offers are termless inventory; since only one open offer per (owner, ticker, direction) is allowed, the owner edits the total instead of posting a second offer. The new quantity must be > 0, not below the shares already committed to formed/forming contracts on the offer, and not above the owner's holding for the ticker. Owner-only; the offer must be local and open.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "offer id"
// @Param        body body updateOTCOptionRequest true "new total quantity"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Failure      409 {object} map[string]interface{}
// @Router       /api/v3/me/otc/options/{id} [put]
func (h *OTCOptionsHandler) UpdateMyOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req updateOTCOptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if err := positiveDecimalString("quantity", req.Quantity); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// Ownership is enforced authoritatively in stock-service under the offer's
	// row lock (owner-only edit → PermissionDenied → 403); we forward the acting
	// owner identity. No cheap gateway pre-check exists without re-fetching the
	// offer, so we rely on the service gate (same as the per-chain ops).
	resp, err := h.client.UpdateOTCOfferQuantity(c.Request.Context(), &stockpb.UpdateOTCOfferQuantityRequest{
		OfferId:         id,
		Quantity:        req.Quantity,
		ActingOwnerType: identity.OwnerType,
		ActingOwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": resp})
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
// @Summary      Get an OTC option offer by surrogate id (local or remote)
// @Description  Resolves an OTC option offer by its stable surrogate id (the local_id from the discovery feed). Local offers return the {offer,revisions} body decorated with kind="local" + me_owner. If the id is not a local offer it is resolved from the remote (cross-bank) mirror and returned as a flat body with kind="remote" + me_owner=false. When the authenticated caller has an own (bidder) negotiation chain against the offer, the offer object also carries my_negotiation_id + my_negotiation_status so the FE can jump straight to its chain (absent/0 otherwise; a poster who never bid has no chain).
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "surrogate offer id"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
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
		ActingOwnerType: identity.OwnerType,
		ActingOwnerId:   derefU64(identity.OwnerID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListMyContracts godoc
// @Summary      List the caller's OTC contracts (unified local + remote)
// @Description  Returns the caller's formed option contracts, LOCAL and REMOTE merged into one contracts[] array. Each item carries kind/routing_number/bank_code and me_owner (true when the caller is the buyer/holder). peer_contracts[] has been removed — remote contracts appear in contracts[] with kind=remote. (SP-1 Task 8)
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        role query string false "buyer|seller|either"
// @Param        page query int false "page (default 1)"
// @Param        page_size query int false "page size (default 20)"
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
		"contracts": resp.Contracts,
		"total":     resp.Total,
	})
}

// GetContract godoc
// @Summary      Get an OTC contract (unified local + remote)
// @Description  Resolves an option contract by id. A LOCAL contract is returned with kind=local + own provenance + me_owner (true when caller is the buyer/holder). A non-local id falls back to the cross-bank mirror and returns kind=remote (me_owner=direction==CREDIT). 404 only when neither exists. (SP-1 Task 8)
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "contract id"
// @Success      200 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
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
	// BuyerAccountNumber is REQUIRED only for cross-bank (remote) contracts: the
	// buyer's currency account that pays the strike. The gateway validates the
	// caller owns it before forwarding (the only client-supplied resource on the
	// money path). LOCAL contracts ignore it — their accounts come from the
	// persisted contract. (SP-2b Task 5 — unified local+cross-bank exercise.)
	BuyerAccountNumber string `json:"buyer_account_number,omitempty"`
}

// ExerciseContract godoc
// @Summary      Exercise an OTC option contract (unified local + cross-bank)
// @Description  Exercises an option contract. The dispatch (local saga vs cross-bank SI-TX) is decided in stock-service from the contract's routing — the frontend uses ONE route regardless of kind. For a cross-bank contract, supply buyer_account_number (the buyer's currency account that pays the strike); the gateway validates the caller owns it. For a local contract the accounts come from the persisted contract and buyer_account_number is ignored.
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "contract id"
// @Param        body body exerciseRequest false "optional on-behalf client/fund id; buyer_account_number required only for cross-bank contracts"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/otc/contracts/{id}/exercise [post]
func (h *OTCOptionsHandler) ExerciseContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req exerciseRequest
	// Body is optional — on_behalf_of_*  and buyer_account_number may be present.
	_ = c.ShouldBindJSON(&req)
	identity := c.MustGet("identity").(*middleware.ResolvedIdentity)
	// OWNERSHIP GATE (cross-bank path): when the caller supplies a settlement
	// account (the strike money is debited from it on the cross-bank exercise),
	// they MUST be entitled to it. Without this, a bank-acting employee could
	// exercise a contract and charge the strike to ANOTHER client's account of
	// the matching currency (the verified theft vector the bid/accept paths
	// already guard against). The buyer account is the ONLY client-supplied
	// resource on this path; the contract terms + counterparty come from the
	// persisted row in stock-service.
	//
	// We use ResolveAndCheckAccountByNumber (not enforceOwnership) so the gate is
	// authoritative for ALL principals, mirroring the bid/accept handlers:
	//   - client caller            → account.owner == client
	//   - employee, no on-behalf   → account must be a BANK account
	//   - employee on-behalf-client → account == that client's
	// enforceOwnership returned nil for any non-client principal, leaving the
	// bank-acting employee ungated — that is the gap this closes.
	if req.BuyerAccountNumber != "" {
		if ownErr := ResolveAndCheckAccountByNumber(c, h.accounts, identity, req.BuyerAccountNumber, req.OnBehalfOfClientID); ownErr != nil {
			return // ResolveAndCheckAccountByNumber already wrote the 403 / gRPC error
		}
	}
	resp, err := h.client.ExerciseContract(c.Request.Context(), &stockpb.ExerciseContractRequest{
		ContractId:         id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		OnBehalfOfClientId: req.OnBehalfOfClientID,
		OnBehalfOfFundId:   req.OnBehalfOfFundID,
		BuyerAccountNumber: req.BuyerAccountNumber,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
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
