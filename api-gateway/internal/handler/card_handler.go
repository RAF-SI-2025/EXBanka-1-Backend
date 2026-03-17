package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	cardpb "github.com/exbanka/contract/cardpb"
)

type CardHandler struct {
	cardClient cardpb.CardServiceClient
}

func NewCardHandler(cardClient cardpb.CardServiceClient) *CardHandler {
	return &CardHandler{cardClient: cardClient}
}

type createCardRequest struct {
	AccountNumber string `json:"account_number" binding:"required"`
	OwnerID       uint64 `json:"owner_id" binding:"required"`
	OwnerType     string `json:"owner_type" binding:"required"`
	CardBrand     string `json:"card_brand"`
}

// CreateCard godoc
// @Summary      Create a payment card
// @Description  Issue a new payment card for an account
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createCardRequest  true  "Card data"
// @Success      201  {object}  map[string]interface{}  "Created card"
// @Failure      400  {object}  map[string]string       "Validation error"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards [post]
func (h *CardHandler) CreateCard(c *gin.Context) {
	var req createCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.cardClient.CreateCard(c.Request.Context(), &cardpb.CreateCardRequest{
		AccountNumber: req.AccountNumber,
		OwnerId:       req.OwnerID,
		OwnerType:     req.OwnerType,
		CardBrand:     req.CardBrand,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cardToJSON(resp))
}

// GetCard godoc
// @Summary      Get card by ID
// @Description  Retrieve a payment card by its ID
// @Tags         cards
// @Produce      json
// @Param        id  path  int  true  "Card ID"
// @Success      200  {object}  map[string]interface{}  "Card data"
// @Failure      400  {object}  map[string]string       "Invalid ID"
// @Failure      404  {object}  map[string]string       "Card not found"
// @Security     BearerAuth
// @Router       /api/cards/{id} [get]
func (h *CardHandler) GetCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.cardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// ListCardsByAccount godoc
// @Summary      List cards by account number
// @Description  Retrieve all payment cards linked to an account
// @Tags         cards
// @Produce      json
// @Param        account_number  path  string  true  "Account number"
// @Success      200  {object}  map[string]interface{}  "cards array"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/account/{account_number} [get]
func (h *CardHandler) ListCardsByAccount(c *gin.Context) {
	accountNumber := c.Param("account_number")
	resp, err := h.cardClient.ListCardsByAccount(c.Request.Context(), &cardpb.ListCardsByAccountRequest{
		AccountNumber: accountNumber,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cards := make([]gin.H, 0, len(resp.Cards))
	for _, card := range resp.Cards {
		cards = append(cards, cardToJSON(card))
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// ListCardsByClient godoc
// @Summary      List cards by client
// @Description  Retrieve all payment cards belonging to a client
// @Tags         cards
// @Produce      json
// @Param        client_id  path  int  true  "Client ID"
// @Success      200  {object}  map[string]interface{}  "cards array"
// @Failure      400  {object}  map[string]string       "Invalid client_id"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/client/{client_id} [get]
func (h *CardHandler) ListCardsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}

	resp, err := h.cardClient.ListCardsByClient(c.Request.Context(), &cardpb.ListCardsByClientRequest{
		ClientId: clientID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	cards := make([]gin.H, 0, len(resp.Cards))
	for _, card := range resp.Cards {
		cards = append(cards, cardToJSON(card))
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// BlockCard godoc
// @Summary      Block a card
// @Description  Temporarily block a payment card
// @Tags         cards
// @Produce      json
// @Param        id  path  int  true  "Card ID"
// @Success      200  {object}  map[string]interface{}  "Updated card"
// @Failure      400  {object}  map[string]string       "Invalid ID"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/{id}/block [put]
func (h *CardHandler) BlockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.cardClient.BlockCard(c.Request.Context(), &cardpb.BlockCardRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// UnblockCard godoc
// @Summary      Unblock a card
// @Description  Unblock a previously blocked payment card
// @Tags         cards
// @Produce      json
// @Param        id  path  int  true  "Card ID"
// @Success      200  {object}  map[string]interface{}  "Updated card"
// @Failure      400  {object}  map[string]string       "Invalid ID"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/{id}/unblock [put]
func (h *CardHandler) UnblockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.cardClient.UnblockCard(c.Request.Context(), &cardpb.UnblockCardRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// DeactivateCard godoc
// @Summary      Deactivate a card
// @Description  Permanently deactivate a payment card
// @Tags         cards
// @Produce      json
// @Param        id  path  int  true  "Card ID"
// @Success      200  {object}  map[string]interface{}  "Updated card"
// @Failure      400  {object}  map[string]string       "Invalid ID"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/{id}/deactivate [put]
func (h *CardHandler) DeactivateCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	resp, err := h.cardClient.DeactivateCard(c.Request.Context(), &cardpb.DeactivateCardRequest{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

type createAuthorizedPersonRequest struct {
	FirstName   string `json:"first_name" binding:"required"`
	LastName    string `json:"last_name" binding:"required"`
	DateOfBirth int64  `json:"date_of_birth"`
	Gender      string `json:"gender"`
	Email       string `json:"email"`
	Phone       string `json:"phone"`
	Address     string `json:"address"`
	AccountID   uint64 `json:"account_id" binding:"required"`
}

// CreateAuthorizedPerson godoc
// @Summary      Add authorized card person
// @Description  Add a person authorized to use cards on an account
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createAuthorizedPersonRequest  true  "Authorized person data"
// @Success      201  {object}  map[string]interface{}  "Created authorized person"
// @Failure      400  {object}  map[string]string       "Validation error"
// @Failure      500  {object}  map[string]string       "Internal error"
// @Security     BearerAuth
// @Router       /api/cards/authorized-person [post]
func (h *CardHandler) CreateAuthorizedPerson(c *gin.Context) {
	var req createAuthorizedPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.cardClient.CreateAuthorizedPerson(c.Request.Context(), &cardpb.CreateAuthorizedPersonRequest{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		AccountId:   req.AccountID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":            resp.Id,
		"first_name":    resp.FirstName,
		"last_name":     resp.LastName,
		"date_of_birth": resp.DateOfBirth,
		"gender":        resp.Gender,
		"email":         resp.Email,
		"phone":         resp.Phone,
		"address":       resp.Address,
		"account_id":    resp.AccountId,
	})
}

func cardToJSON(card *cardpb.CardResponse) gin.H {
	return gin.H{
		"id":               card.Id,
		"card_number":      card.CardNumber,
		"card_number_full": card.CardNumberFull,
		"card_type":        card.CardType,
		"card_name":        card.CardName,
		"card_brand":       card.CardBrand,
		"created_at":       card.CreatedAt,
		"expires_at":       card.ExpiresAt,
		"account_number":   card.AccountNumber,
		"cvv":              card.Cvv,
		"card_limit":       card.CardLimit,
		"status":           card.Status,
		"owner_type":       card.OwnerType,
		"owner_id":         card.OwnerId,
	}
}
