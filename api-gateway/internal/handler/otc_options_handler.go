package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	stockpb "github.com/exbanka/contract/stockpb"
)

// OTCOptionsHandler handles REST routes for the intra-bank OTC options
// feature (Celina 4 / Spec 2). Each method delegates to the stock-service
// gRPC OTCOptionsService.
type OTCOptionsHandler struct {
	client stockpb.OTCOptionsServiceClient
}

func NewOTCOptionsHandler(client stockpb.OTCOptionsServiceClient) *OTCOptionsHandler {
	return &OTCOptionsHandler{client: client}
}

type createOTCOfferRequest struct {
	Direction      string  `json:"direction"`
	StockID        uint64  `json:"stock_id"`
	Quantity       string  `json:"quantity"`
	StrikePrice    string  `json:"strike_price"`
	Premium        string  `json:"premium"`
	SettlementDate string  `json:"settlement_date"`
	CounterpartyUserID     *int64  `json:"counterparty_user_id,omitempty"`
	CounterpartySystemType *string `json:"counterparty_system_type,omitempty"`
}

// CreateOffer godoc
// @Summary      Create an OTC option offer
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body createOTCOfferRequest true "offer details"
// @Success      201 {object} map[string]interface{}
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
	if req.StockID == 0 || req.Quantity == "" || req.StrikePrice == "" || req.SettlementDate == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "stock_id, quantity, strike_price and settlement_date are required")
		return
	}
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	in := &stockpb.CreateOTCOfferRequest{
		ActorUserId: int64(uid), ActorSystemType: st,
		Direction: req.Direction, StockId: req.StockID,
		Quantity: req.Quantity, StrikePrice: req.StrikePrice, Premium: req.Premium,
		SettlementDate: req.SettlementDate,
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListMyOffers(c.Request.Context(), &stockpb.ListMyOTCOffersRequest{
		ActorUserId: int64(uid), ActorSystemType: st,
		Role: c.DefaultQuery("role", "either"),
		Page: int32(page), PageSize: int32(pageSize),
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.GetOffer(c.Request.Context(), &stockpb.GetOTCOfferRequest{
		OfferId: id, ActorUserId: int64(uid), ActorSystemType: st,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type counterOTCOfferRequest struct {
	Quantity       string `json:"quantity"`
	StrikePrice    string `json:"strike_price"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlement_date"`
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.CounterOffer(c.Request.Context(), &stockpb.CounterOTCOfferRequest{
		OfferId: id, ActorUserId: int64(uid), ActorSystemType: st,
		Quantity: req.Quantity, StrikePrice: req.StrikePrice, Premium: req.Premium,
		SettlementDate: req.SettlementDate,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offer": resp})
}

type acceptOTCOfferRequest struct {
	BuyerAccountID  uint64 `json:"buyer_account_id"`
	SellerAccountID uint64 `json:"seller_account_id"`
}

// AcceptOffer godoc
// @Summary      Accept an OTC offer (premium-payment saga)
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "offer id"
// @Param        body body acceptOTCOfferRequest true "buyer + seller account IDs"
// @Success      201 {object} map[string]interface{}
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
	if req.BuyerAccountID == 0 || req.SellerAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "buyer_account_id and seller_account_id are required")
		return
	}
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.AcceptOffer(c.Request.Context(), &stockpb.AcceptOTCOfferRequest{
		OfferId: id, ActorUserId: int64(uid), ActorSystemType: st,
		BuyerAccountId: req.BuyerAccountID, SellerAccountId: req.SellerAccountID,
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.RejectOffer(c.Request.Context(), &stockpb.RejectOTCOfferRequest{
		OfferId: id, ActorUserId: int64(uid), ActorSystemType: st,
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	resp, err := h.client.ListMyContracts(c.Request.Context(), &stockpb.ListMyContractsRequest{
		ActorUserId: int64(uid), ActorSystemType: st,
		Role: c.DefaultQuery("role", "either"),
		Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"contracts": resp.Contracts, "total": resp.Total})
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
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.GetContract(c.Request.Context(), &stockpb.GetContractRequest{
		ContractId: id, ActorUserId: int64(uid), ActorSystemType: st,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

type exerciseRequest struct {
	BuyerAccountID  uint64 `json:"buyer_account_id"`
	SellerAccountID uint64 `json:"seller_account_id"`
}

// ExerciseContract godoc
// @Summary      Exercise an OTC option contract
// @Tags         OTCOptions
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path int true "contract id"
// @Param        body body exerciseRequest true "buyer + seller account IDs"
// @Success      201 {object} map[string]interface{}
// @Router       /api/v3/otc/contracts/{id}/exercise [post]
func (h *OTCOptionsHandler) ExerciseContract(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req exerciseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.BuyerAccountID == 0 || req.SellerAccountID == 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "buyer_account_id and seller_account_id are required")
		return
	}
	uid, st, ok := mePortfolioIdentity(c)
	if !ok {
		return
	}
	resp, err := h.client.ExerciseContract(c.Request.Context(), &stockpb.ExerciseContractRequest{
		ContractId: id, ActorUserId: int64(uid), ActorSystemType: st,
		BuyerAccountId: req.BuyerAccountID, SellerAccountId: req.SellerAccountID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}
