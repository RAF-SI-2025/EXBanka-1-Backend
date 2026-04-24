package handler

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
)

type ClientHandler struct {
	clientClient clientpb.ClientServiceClient
	authClient   authpb.AuthServiceClient
}

func NewClientHandler(clientClient clientpb.ClientServiceClient, authClient authpb.AuthServiceClient) *ClientHandler {
	return &ClientHandler{clientClient: clientClient, authClient: authClient}
}

type createClientRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth" binding:"required"`
	Gender      string `json:"gender"`
	Email       string `json:"email" binding:"required,email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	JMBG        string `json:"jmbg" binding:"required"`
}

// @Summary      Create client
// @Description  Creates a new bank client with login credentials. Requires clients.create permission.
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        body  body  createClientRequest  true  "Client data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/clients [post]
func (h *ClientHandler) CreateClient(c *gin.Context) {
	var req createClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.clientClient.CreateClient(c.Request.Context(), &clientpb.CreateClientRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		Jmbg:        req.JMBG,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, clientToJSONWithActive(resp, false))
}

// @Summary      List clients
// @Tags         clients
// @Produce      json
// @Param        page          query  int     false  "Page number (default 1)"
// @Param        page_size     query  int     false  "Items per page (default 20)"
// @Param        email_filter  query  string  false  "Filter by email"
// @Param        name_filter   query  string  false  "Filter by name"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/clients [get]
func (h *ClientHandler) ListClients(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.clientClient.ListClients(c.Request.Context(), &clientpb.ListClientsRequest{
		EmailFilter: c.Query("email_filter"),
		NameFilter:  c.Query("name_filter"),
		Page:        int32(page),
		PageSize:    int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	ids := make([]int64, 0, len(resp.Clients))
	for _, cl := range resp.Clients {
		ids = append(ids, int64(cl.Id))
	}
	statusMap := make(map[int64]bool)
	if len(ids) > 0 {
		if batchResp, batchErr := h.authClient.GetAccountStatusBatch(c.Request.Context(), &authpb.GetAccountStatusBatchRequest{
			PrincipalType: "client",
			PrincipalIds:  ids,
		}); batchErr == nil {
			for _, entry := range batchResp.Entries {
				statusMap[entry.PrincipalId] = entry.Active
			}
		} else {
			log.Printf("WARN: failed to fetch account statuses for clients: %v", batchErr)
		}
	}

	clients := make([]gin.H, 0, len(resp.Clients))
	for _, cl := range resp.Clients {
		clients = append(clients, clientToJSONWithActive(cl, statusMap[int64(cl.Id)]))
	}
	c.JSON(http.StatusOK, gin.H{
		"clients": clients,
		"total":   resp.Total,
	})
}

// @Summary      Get client by ID
// @Tags         clients
// @Produce      json
// @Param        id   path  int  true  "Client ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/v2/clients/{id} [get]
func (h *ClientHandler) GetClient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	active := false
	if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
		PrincipalType: "client",
		PrincipalId:   int64(id),
	}); statusErr == nil {
		active = statusResp.Active
	} else {
		log.Printf("WARN: failed to fetch account status for client %d: %v", id, statusErr)
	}
	c.JSON(http.StatusOK, clientToJSONWithActive(resp, active))
}

type updateClientRequest struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	DateOfBirth *int64  `json:"date_of_birth"`
	Gender      *string `json:"gender"`
	Email       *string `json:"email"`
	Phone       *string `json:"phone"`
	Address     *string `json:"address"`
	Active      *bool   `json:"active"`
}

// @Summary      Update client
// @Description  Updates client profile information. Requires clients.update permission.
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        id    path  int                  true  "Client ID"
// @Param        body  body  updateClientRequest  true  "Fields to update"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/clients/{id} [put]
func (h *ClientHandler) UpdateClient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	var req updateClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	pbReq := &clientpb.UpdateClientRequest{Id: id}
	pbReq.FirstName = req.FirstName
	pbReq.LastName = req.LastName
	pbReq.DateOfBirth = req.DateOfBirth
	pbReq.Gender = req.Gender
	pbReq.Email = req.Email
	pbReq.Phone = req.Phone
	pbReq.Address = req.Address

	resp, err := h.clientClient.UpdateClient(middleware.GRPCContextWithChangedBy(c), pbReq)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	if req.Active != nil {
		_, authErr := h.authClient.SetAccountStatus(c.Request.Context(), &authpb.SetAccountStatusRequest{
			PrincipalType: "client",
			PrincipalId:   int64(id),
			Active:        *req.Active,
		})
		if authErr != nil {
			handleGRPCError(c, authErr)
			return
		}
	}

	// Re-fetch active status to return accurate value
	active := false
	if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
		PrincipalType: "client",
		PrincipalId:   int64(id),
	}); statusErr == nil {
		active = statusResp.Active
	} else {
		log.Printf("WARN: failed to fetch account status for client %d: %v", id, statusErr)
	}
	c.JSON(http.StatusOK, clientToJSONWithActive(resp, active))
}

// @Summary      Get current client (me)
// @Tags         clients
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/v2/clients/me [get]
func (h *ClientHandler) GetCurrentClient(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		apiError(c, 401, ErrUnauthorized, "not authenticated")
		return
	}

	var id uint64
	switch v := userID.(type) {
	case int64:
		id = uint64(v)
	case uint64:
		id = v
	case string:
		parsedID, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			apiError(c, 401, ErrUnauthorized, "invalid user_id")
			return
		}
		id = parsedID
	default:
		apiError(c, 401, ErrUnauthorized, "invalid user_id")
		return
	}

	resp, err := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	active := false
	if statusResp, statusErr := h.authClient.GetAccountStatus(c.Request.Context(), &authpb.GetAccountStatusRequest{
		PrincipalType: "client",
		PrincipalId:   int64(id),
	}); statusErr == nil {
		active = statusResp.Active
	} else {
		log.Printf("WARN: failed to fetch account status for client %d: %v", id, statusErr)
	}
	c.JSON(http.StatusOK, clientToJSONWithActive(resp, active))
}

func clientToJSONWithActive(cl *clientpb.ClientResponse, active bool) gin.H {
	return gin.H{
		"id":            cl.Id,
		"first_name":    cl.FirstName,
		"last_name":     cl.LastName,
		"date_of_birth": cl.DateOfBirth,
		"gender":        cl.Gender,
		"email":         cl.Email,
		"phone":         cl.Phone,
		"address":       cl.Address,
		"jmbg":          cl.Jmbg,
		"active":        active,
		"created_at":    cl.CreatedAt,
	}
}
