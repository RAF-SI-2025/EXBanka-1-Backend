package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)

// MeHandler serves /api/me routes for the currently authenticated user.
// It holds gRPC clients for client-service, user-service, AND auth-service
// (needed to fetch account activation status for the profile response).
type MeHandler struct {
	clientClient clientpb.ClientServiceClient
	userClient   userpb.UserServiceClient
	authClient   authpb.AuthServiceClient
}

func NewMeHandler(
	clientClient clientpb.ClientServiceClient,
	userClient userpb.UserServiceClient,
	authClient authpb.AuthServiceClient,
) *MeHandler {
	return &MeHandler{clientClient: clientClient, userClient: userClient, authClient: authClient}
}

// GetMe returns the full profile of the currently authenticated user.
// For clients (system_type == "client"), it calls client-service + auth-service.
// For employees (system_type == "employee"), it calls user-service + auth-service.
//
// @Summary      Get current user profile
// @Description  Returns the full profile of the authenticated user (client or employee).
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/me [get]
func (h *MeHandler) GetMe(c *gin.Context) {
	sysType, _ := c.Get("system_type")
	userID, _ := c.Get("user_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}

	switch sysType {
	case "client":
		// GetClient returns (*clientpb.ClientResponse, error) directly — no wrapper
		resp, err := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: uint64(uid)})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		active := false
		if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
			PrincipalType: "client",
			PrincipalId:   uid,
		}); statusErr == nil {
			active = statusResp.Active
		} else {
			log.Printf("WARN: failed to fetch account status for client %d: %v", uid, statusErr)
		}
		c.JSON(http.StatusOK, clientToJSONWithActive(resp, active))

	case "employee":
		// GetEmployee returns (*userpb.EmployeeResponse, error) directly — no wrapper
		resp, err := h.userClient.GetEmployee(c.Request.Context(), &userpb.GetEmployeeRequest{Id: uid})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		active := false
		if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
			PrincipalType: "employee",
			PrincipalId:   uid,
		}); statusErr == nil {
			active = statusResp.Active
		} else {
			log.Printf("WARN: failed to fetch account status for employee %d: %v", uid, statusErr)
		}
		c.JSON(http.StatusOK, employeeToJSONWithActive(resp, active))

	default:
		apiError(c, 403, ErrForbidden, "unknown system type")
	}
}
