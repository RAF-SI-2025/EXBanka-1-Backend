package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerUserHandler serves GET /api/v3/cross-bank-protocol/user/{rid}/{id} — peer
// banks call this to resolve a counterparty user's display name (SI-TX §9).
//
// As of the 2026-06-07 cutover this just forwards to interbank-service's
// PeerUserService.ResolvePeerUser, which owns the resolution (parses
// client-N/employee-N, calls client/user-service, composes the name, and gates
// on own-routing). The gateway stays a thin REST↔gRPC translator.
type PeerUserHandler struct {
	client transactionpb.PeerUserServiceClient
}

func NewPeerUserHandler(c transactionpb.PeerUserServiceClient) *PeerUserHandler {
	return &PeerUserHandler{client: c}
}

// GetUser godoc
// @Summary      Peer-to-peer: resolve a foreign user to display name
// @Description  Inbound from a peer bank. Forwards to interbank-service which looks up the local (client-N / employee-N) user and returns first+last name. Routing number in path MUST match this bank's routing — others return 404.
// @Tags         PeerOTC
// @Produce      json
// @Param        rid path int true "routing number (must equal this bank's routing)"
// @Param        id path string true "principal id, e.g. client-1 or employee-3"
// @Success      200 {object} map[string]interface{}
// @Failure      401 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/cross-bank-protocol/user/{rid}/{id} [get]
func (h *PeerUserHandler) GetUser(c *gin.Context) {
	rid, err := strconv.ParseInt(c.Param("rid"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid rid")
		return
	}
	id := c.Param("id")
	if id == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "missing id")
		return
	}

	resp, err := h.client.ResolvePeerUser(c.Request.Context(), &transactionpb.ResolvePeerUserRequest{
		RoutingNumber: rid,
		Id:            id,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if !resp.GetFound() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"bankDisplayName": resp.GetBankDisplayName(),
		"displayName":     resp.GetDisplayName(),
	})
}
