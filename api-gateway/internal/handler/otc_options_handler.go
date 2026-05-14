package handler

import (
	"net/http"
	"strconv"

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
// @Router       /api/v3/otc/offers [post]
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

// ListMyOffers godoc
// @Summary      List the caller's OTC offers
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        role query string false "initiator|counterparty|either"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/me/otc/offers [get]
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

// GetOffer godoc
// @Summary      Get an OTC offer with revisions
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "offer id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/offers/{id} [get]
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

// CounterOffer godoc
// @Summary      Counter an OTC offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "offer id"
// @Param        body body counterOTCOfferRequest true "new terms"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/offers/{id}/counter [post]
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
		OfferId:            id,
		ActorUserId:        int64(ownerToLegacyUserID(identity.OwnerID)),
		ActorSystemType:    ownerToLegacySystemType(identity.OwnerType),
		Quantity:           req.Quantity, StrikePrice: req.StrikePrice, Premium: req.Premium,
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

// AcceptOffer godoc
// @Summary      Accept an OTC offer (premium-payment saga)
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "offer id"
// @Param        body body acceptOTCOfferRequest true "acceptor's own account id"
// @Success      201 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/otc/offers/{id}/accept [post]
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

// RejectOffer godoc
// @Summary      Reject an OTC offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Produce      json
// @Param        id path int true "offer id"
// @Success      200 {object} map[string]interface{}
// @Router       /api/v3/otc/offers/{id}/reject [post]
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
