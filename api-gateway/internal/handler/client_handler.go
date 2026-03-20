package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	clientpb "github.com/exbanka/contract/clientpb"
)

type ClientHandler struct {
	clientClient clientpb.ClientServiceClient
}

func NewClientHandler(clientClient clientpb.ClientServiceClient) *ClientHandler {
	return &ClientHandler{clientClient: clientClient}
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
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        body  body  createClientRequest  true  "Client data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/clients [post]
func (h *ClientHandler) CreateClient(c *gin.Context) {
	var req createClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, clientToJSON(resp))
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
// @Router       /api/clients [get]
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	clients := make([]gin.H, 0, len(resp.Clients))
	for _, cl := range resp.Clients {
		clients = append(clients, clientToJSON(cl))
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
// @Router       /api/clients/{id} [get]
func (h *ClientHandler) GetClient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "client not found"})
		return
	}
	c.JSON(http.StatusOK, clientToJSON(resp))
}

type updateClientRequest struct {
	FirstName   *string `json:"first_name"`
	LastName    *string `json:"last_name"`
	DateOfBirth *int64  `json:"date_of_birth"`
	Gender      *string `json:"gender"`
	Email       *string `json:"email"`
	Phone       *string `json:"phone"`
	Address     *string `json:"address"`
}

// @Summary      Update client
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
// @Router       /api/clients/{id} [put]
func (h *ClientHandler) UpdateClient(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	resp, err := h.clientClient.UpdateClient(c.Request.Context(), pbReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, clientToJSON(resp))
}

// @Summary      Get current client (me)
// @Tags         clients
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /api/clients/me [get]
func (h *ClientHandler) GetCurrentClient(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	id, ok := userID.(uint64)
	if !ok {
		// try string conversion
		idStr, ok2 := userID.(string)
		if !ok2 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user_id"})
			return
		}
		parsedID, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user_id"})
			return
		}
		id = parsedID
	}

	resp, err := h.clientClient.GetClient(c.Request.Context(), &clientpb.GetClientRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "client not found"})
		return
	}
	c.JSON(http.StatusOK, clientToJSON(resp))
}

type setPasswordRequest struct {
	UserID       uint64 `json:"user_id" binding:"required"`
	PasswordHash string `json:"password_hash" binding:"required"`
}

// @Summary      Set client password hash
// @Tags         clients
// @Accept       json
// @Produce      json
// @Param        body  body  setPasswordRequest  true  "Password hash data"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/clients/set-password [post]
func (h *ClientHandler) SetPassword(c *gin.Context) {
	var req setPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := h.clientClient.SetPassword(c.Request.Context(), &clientpb.SetClientPasswordRequest{
		UserId:       req.UserID,
		PasswordHash: req.PasswordHash,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func clientToJSON(cl *clientpb.ClientResponse) gin.H {
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
		"active":        cl.Active,
		"created_at":    cl.CreatedAt,
	}
}
