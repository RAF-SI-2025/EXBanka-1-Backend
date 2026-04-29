package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerBankAdminHandler serves the 5 admin routes under /api/v3/peer-banks.
// All routes are gated upstream by AuthMiddleware + RequirePermission(
// "peer_banks.manage.any").
type PeerBankAdminHandler struct {
	client transactionpb.PeerBankAdminServiceClient
}

func NewPeerBankAdminHandler(c transactionpb.PeerBankAdminServiceClient) *PeerBankAdminHandler {
	return &PeerBankAdminHandler{client: c}
}

func (h *PeerBankAdminHandler) List(c *gin.Context) {
	activeOnly := c.Query("active_only") == "true"
	resp, err := h.client.ListPeerBanks(c.Request.Context(), &transactionpb.ListPeerBanksRequest{ActiveOnly: activeOnly})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.GetPeerBanks()))
	for _, pb := range resp.GetPeerBanks() {
		out = append(out, peerBankToJSON(pb))
	}
	c.JSON(http.StatusOK, gin.H{"peer_banks": out})
}

func (h *PeerBankAdminHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	resp, err := h.client.GetPeerBank(c.Request.Context(), &transactionpb.GetPeerBankRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, peerBankToJSON(resp))
}

type createPeerBankRequest struct {
	BankCode        string `json:"bank_code"`
	RoutingNumber   int64  `json:"routing_number"`
	BaseURL         string `json:"base_url"`
	APIToken        string `json:"api_token"`
	HMACInboundKey  string `json:"hmac_inbound_key,omitempty"`
	HMACOutboundKey string `json:"hmac_outbound_key,omitempty"`
	Active          bool   `json:"active"`
}

func (h *PeerBankAdminHandler) Create(c *gin.Context) {
	var req createPeerBankRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.BankCode == "" || req.RoutingNumber == 0 || req.BaseURL == "" || req.APIToken == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "bank_code, routing_number, base_url, api_token required")
		return
	}
	resp, err := h.client.CreatePeerBank(c.Request.Context(), &transactionpb.CreatePeerBankRequest{
		BankCode: req.BankCode, RoutingNumber: req.RoutingNumber, BaseUrl: req.BaseURL,
		ApiToken: req.APIToken, HmacInboundKey: req.HMACInboundKey, HmacOutboundKey: req.HMACOutboundKey,
		Active: req.Active,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, peerBankToJSON(resp))
}

type updatePeerBankRequest struct {
	BaseURL         *string `json:"base_url,omitempty"`
	APIToken        *string `json:"api_token,omitempty"`
	HMACInboundKey  *string `json:"hmac_inbound_key,omitempty"`
	HMACOutboundKey *string `json:"hmac_outbound_key,omitempty"`
	Active          *bool   `json:"active,omitempty"`
}

func (h *PeerBankAdminHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	var req updatePeerBankRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	in := &transactionpb.UpdatePeerBankRequest{Id: id}
	if req.BaseURL != nil {
		in.BaseUrl = *req.BaseURL
		in.BaseUrlSet = true
	}
	if req.APIToken != nil {
		in.ApiToken = *req.APIToken
		in.ApiTokenSet = true
	}
	if req.HMACInboundKey != nil {
		in.HmacInboundKey = *req.HMACInboundKey
		in.HmacInboundKeySet = true
	}
	if req.HMACOutboundKey != nil {
		in.HmacOutboundKey = *req.HMACOutboundKey
		in.HmacOutboundKeySet = true
	}
	if req.Active != nil {
		in.Active = *req.Active
		in.ActiveSet = true
	}
	resp, err := h.client.UpdatePeerBank(c.Request.Context(), in)
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, peerBankToJSON(resp))
}

func (h *PeerBankAdminHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid id")
		return
	}
	if _, err := h.client.DeletePeerBank(c.Request.Context(), &transactionpb.DeletePeerBankRequest{Id: id}); err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func peerBankToJSON(pb *transactionpb.PeerBank) gin.H {
	return gin.H{
		"id":                pb.GetId(),
		"bank_code":         pb.GetBankCode(),
		"routing_number":    pb.GetRoutingNumber(),
		"base_url":          pb.GetBaseUrl(),
		"api_token_preview": pb.GetApiTokenPreview(),
		"hmac_enabled":      pb.GetHmacEnabled(),
		"active":            pb.GetActive(),
		"created_at":        pb.GetCreatedAt(),
		"updated_at":        pb.GetUpdatedAt(),
	}
}
