package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	cardpb "github.com/exbanka/contract/cardpb"
)

type CardHandler struct {
	cardClient        cardpb.CardServiceClient
	virtualCardClient cardpb.VirtualCardServiceClient
}

func NewCardHandler(cardClient cardpb.CardServiceClient, virtualCardClient cardpb.VirtualCardServiceClient) *CardHandler {
	return &CardHandler{cardClient: cardClient, virtualCardClient: virtualCardClient}
}

// createVirtualCardBody is the swagger body for creating a virtual card.
type createVirtualCardBody struct {
	AccountNumber string `json:"account_number" binding:"required" example:"265-0000000001-00"`
	OwnerId       uint64 `json:"owner_id" binding:"required" example:"1"`
	CardBrand     string `json:"card_brand" binding:"required" example:"visa"`
	UsageType     string `json:"usage_type" binding:"required" example:"single_use"`
	MaxUses       int32  `json:"max_uses" example:"1"`
	ExpiryMonths  int32  `json:"expiry_months" binding:"required" example:"1"`
	CardLimit     string `json:"card_limit" binding:"required" example:"100000.0000"`
}

// setCardPinBody is the swagger body for setting a card PIN.
type setCardPinBody struct {
	Pin string `json:"pin" binding:"required" example:"1234"`
}

// verifyCardPinBody is the swagger body for verifying a card PIN.
type verifyCardPinBody struct {
	Pin string `json:"pin" binding:"required" example:"1234"`
}

// temporaryBlockCardBody is the swagger body for temporarily blocking a card.
type temporaryBlockCardBody struct {
	DurationHours int32  `json:"duration_hours" binding:"required" example:"24"`
	Reason        string `json:"reason" example:"Lost card"`
}

type createCardRequest struct {
	AccountNumber string `json:"account_number" binding:"required"`
	OwnerID       uint64 `json:"owner_id" binding:"required"`
	OwnerType     string `json:"owner_type" binding:"required"`
	CardBrand     string `json:"card_brand"`
}

// @Summary      Issue a card
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createCardRequest  true  "Card data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/cards [post]
func (h *CardHandler) CreateCard(c *gin.Context) {
	var req createCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ownerType, err := oneOf("owner_type", req.OwnerType, "client", "authorized_person")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cardBrand := req.CardBrand
	if cardBrand != "" {
		cardBrand, err = oneOf("card_brand", cardBrand, "visa", "mastercard", "dinacard", "amex")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	resp, err := h.cardClient.CreateCard(c.Request.Context(), &cardpb.CreateCardRequest{
		AccountNumber: req.AccountNumber,
		OwnerId:       req.OwnerID,
		OwnerType:     ownerType,
		CardBrand:     cardBrand,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, cardToJSON(resp))
}

// @Summary      Get card by ID
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      404  {object}  map[string]string
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

// @Summary      List cards by account
// @Tags         cards
// @Produce      json
// @Param        account_number  path  string  true  "Account number"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
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

// @Summary      List cards by client
// @Tags         cards
// @Produce      json
// @Param        client_id  path  int  true  "Client ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/cards/client/{client_id} [get]
func (h *CardHandler) ListCardsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid client_id"})
		return
	}
	if !enforceClientSelf(c, clientID) {
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

// @Summary      Block a card
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
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

// @Summary      Unblock a card
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
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

// @Summary      Deactivate a card
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
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

// @Summary      Create authorized person
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createAuthorizedPersonRequest  true  "Authorized person data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
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

// CreateVirtualCard godoc
// @Summary      Create virtual card
// @Description  Creates a virtual card for a client (single_use or multi_use, 1-3 month expiry)
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createVirtualCardBody  true  "Virtual card details"
// @Security     ClientBearerAuth
// @Success      201  {object}  map[string]interface{}  "created virtual card"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/cards/virtual [post]
func (h *CardHandler) CreateVirtualCard(c *gin.Context) {
	var body createVirtualCardBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	vcBrand, err := oneOf("card_brand", body.CardBrand, "visa", "mastercard", "dinacard", "amex")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	usageType, err := oneOf("usage_type", body.UsageType, "single_use", "multi_use")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := inRange("expiry_months", body.ExpiryMonths, 1, 3); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if usageType == "multi_use" && body.MaxUses < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multi_use cards must have max_uses >= 2"})
		return
	}
	resp, err := h.virtualCardClient.CreateVirtualCard(c.Request.Context(), &cardpb.CreateVirtualCardRequest{
		AccountNumber: body.AccountNumber,
		OwnerId:       body.OwnerId,
		CardBrand:     vcBrand,
		UsageType:     usageType,
		MaxUses:       body.MaxUses,
		ExpiryMonths:  body.ExpiryMonths,
		CardLimit:     body.CardLimit,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// SetCardPin godoc
// @Summary      Set card PIN
// @Description  Sets the 4-digit PIN for a card
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        id    path  int             true  "Card ID"
// @Param        body  body  setCardPinBody  true  "PIN"
// @Security     ClientBearerAuth
// @Success      200  {object}  map[string]interface{}  "PIN set"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/cards/{id}/pin [post]
func (h *CardHandler) SetCardPin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid card id"})
		return
	}
	var body setCardPinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validatePin(body.Pin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.virtualCardClient.SetCardPin(c.Request.Context(), &cardpb.SetCardPinRequest{
		Id:  id,
		Pin: body.Pin,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// VerifyCardPin godoc
// @Summary      Verify card PIN
// @Description  Verifies the 4-digit PIN for a card. Card is blocked after 3 failed attempts.
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        id    path  int                true  "Card ID"
// @Param        body  body  verifyCardPinBody  true  "PIN"
// @Security     ClientBearerAuth
// @Success      200  {object}  map[string]interface{}  "verification result"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/cards/{id}/verify-pin [post]
func (h *CardHandler) VerifyCardPin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid card id"})
		return
	}
	var body verifyCardPinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validatePin(body.Pin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.virtualCardClient.VerifyCardPin(c.Request.Context(), &cardpb.VerifyCardPinRequest{
		Id:  id,
		Pin: body.Pin,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// TemporaryBlockCard godoc
// @Summary      Temporarily block card
// @Description  Blocks a card temporarily for a specified duration in hours (1-720). Card is automatically unblocked when the duration expires.
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        id    path  int                      true  "Card ID"
// @Param        body  body  temporaryBlockCardBody   true  "Block duration and reason"
// @Security     ClientBearerAuth
// @Success      200  {object}  map[string]interface{}  "blocked card"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "card not found"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/cards/{id}/temporary-block [post]
func (h *CardHandler) TemporaryBlockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid card id"})
		return
	}
	var body temporaryBlockCardBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := inRange("duration_hours", body.DurationHours, 1, 720); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := h.virtualCardClient.TemporaryBlockCard(c.Request.Context(), &cardpb.TemporaryBlockCardRequest{
		Id:            id,
		DurationHours: body.DurationHours,
		Reason:        body.Reason,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
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
