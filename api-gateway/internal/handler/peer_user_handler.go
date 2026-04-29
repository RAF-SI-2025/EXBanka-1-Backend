package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)

// PeerUserHandler serves GET /api/v3/user/{rid}/{id} — peer banks call
// this to fetch identity info for a counterparty user when displaying
// OTC negotiations or transfer history. Returns 404 if rid is not our
// own routing number.
type PeerUserHandler struct {
	clientClient clientpb.ClientServiceClient
	userClient   userpb.UserServiceClient
	ownRouting   int64
}

func NewPeerUserHandler(c clientpb.ClientServiceClient, u userpb.UserServiceClient, ownRouting int64) *PeerUserHandler {
	return &PeerUserHandler{clientClient: c, userClient: u, ownRouting: ownRouting}
}

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
	if rid != h.ownRouting {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	// Try client lookup first. Convention: id format "client-<n>" maps to
	// client-service; "employee-<n>" maps to user-service.
	if strings.HasPrefix(id, "client-") {
		if clientID, parseErr := strconv.ParseUint(strings.TrimPrefix(id, "client-"), 10, 64); parseErr == nil {
			resp, lookupErr := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: clientID})
			if lookupErr == nil && resp != nil {
				c.JSON(http.StatusOK, gin.H{
					"id":        gin.H{"routingNumber": h.ownRouting, "id": id},
					"firstName": resp.GetFirstName(),
					"lastName":  resp.GetLastName(),
				})
				return
			}
			if grpcStatus, ok := status.FromError(lookupErr); ok && grpcStatus.Code() != codes.NotFound {
				handleGRPCError(c, lookupErr)
				return
			}
		}
	}
	// Try employee lookup.
	if strings.HasPrefix(id, "employee-") {
		if empID, parseErr := strconv.ParseInt(strings.TrimPrefix(id, "employee-"), 10, 64); parseErr == nil {
			resp, lookupErr := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: empID})
			if lookupErr == nil && resp != nil {
				c.JSON(http.StatusOK, gin.H{
					"id":        gin.H{"routingNumber": h.ownRouting, "id": id},
					"firstName": resp.GetFirstName(),
					"lastName":  resp.GetLastName(),
				})
				return
			}
			if grpcStatus, ok := status.FromError(lookupErr); ok && grpcStatus.Code() != codes.NotFound {
				handleGRPCError(c, lookupErr)
				return
			}
		}
	}
	c.AbortWithStatus(http.StatusNotFound)
}
