package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	cardpb "github.com/exbanka/contract/cardpb"
)

type CardHandler struct {
	cardClient        cardpb.CardServiceClient
	virtualCardClient cardpb.VirtualCardServiceClient
	cardRequestClient cardpb.CardRequestServiceClient
	accountClient     accountpb.AccountServiceClient
}

func NewCardHandler(cardClient cardpb.CardServiceClient, virtualCardClient cardpb.VirtualCardServiceClient, cardRequestClient cardpb.CardRequestServiceClient, accountClient accountpb.AccountServiceClient) *CardHandler {
	return &CardHandler{cardClient: cardClient, virtualCardClient: virtualCardClient, cardRequestClient: cardRequestClient, accountClient: accountClient}
}

// createVirtualCardBody is the swagger body for creating a virtual card.
type createVirtualCardBody struct {
	AccountNumber string `json:"account_number" binding:"required" example:"265-0000000001-00"`
	OwnerId       uint64 `json:"owner_id" example:"1"` // ignored by gateway; owner is derived from JWT
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
// @Description  Issues a new physical card for a client or authorized person. Requires cards.create permission.
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createCardRequest  true  "Card data"
// @Security     BearerAuth
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v2/cards [post]
func (h *CardHandler) CreateCard(c *gin.Context) {
	var req createCardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	ownerType, err := oneOf("owner_type", req.OwnerType, "client", "authorized_person")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	cardBrand := req.CardBrand
	if cardBrand != "" {
		cardBrand, err = oneOf("card_brand", cardBrand, "visa", "mastercard", "dinacard", "amex")
		if err != nil {
			apiError(c, 400, ErrValidation, err.Error())
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
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/{id} [get]
func (h *CardHandler) GetCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.cardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/account/{account_number} [get]
func (h *CardHandler) ListCardsByAccount(c *gin.Context) {
	accountNumber := c.Param("account_number")
	resp, err := h.cardClient.ListCardsByAccount(c.Request.Context(), &cardpb.ListCardsByAccountRequest{
		AccountNumber: accountNumber,
	})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/client/{client_id} [get]
func (h *CardHandler) ListCardsByClient(c *gin.Context) {
	clientID, err := strconv.ParseUint(c.Param("client_id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid client_id")
		return
	}
	if !enforceClientSelf(c, clientID) {
		return
	}

	resp, err := h.cardClient.ListCardsByClient(c.Request.Context(), &cardpb.ListCardsByClientRequest{
		ClientId: clientID,
	})
	if err != nil {
		handleGRPCError(c, err)
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
// @Description  Blocks a card. Employees with cards.update permission can block any card. Clients can block their own cards via the client-authenticated endpoint.
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      401  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/cards/{id}/block [put]
func (h *CardHandler) BlockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.cardClient.BlockCard(middleware.GRPCContextWithChangedBy(c), &cardpb.BlockCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// ClientBlockCard blocks a client's own card.
// The card's owner_id must match the authenticated client's principal_id.
// This handler is mounted on the client-authenticated route group (ClientAuthMiddleware).
// Swagger documentation is combined with the employee BlockCard endpoint above.
func (h *CardHandler) ClientBlockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	// Fetch the card to verify ownership
	card, err := h.cardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	// Verify the card belongs to the authenticated client
	uid, _ := c.Get("principal_id")
	userID, ok := uid.(int64)
	if !ok || uint64(userID) != card.OwnerId {
		apiError(c, 403, ErrForbidden, "clients can only block their own cards")
		return
	}

	resp, err := h.cardClient.BlockCard(middleware.GRPCContextWithChangedBy(c), &cardpb.BlockCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// @Summary      Unblock a card
// @Description  Unblocks a previously blocked card. Requires cards.update permission.
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/cards/{id}/unblock [put]
func (h *CardHandler) UnblockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.cardClient.UnblockCard(middleware.GRPCContextWithChangedBy(c), &cardpb.UnblockCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// @Summary      Deactivate a card
// @Description  Permanently deactivates a card. Requires cards.update permission.
// @Tags         cards
// @Produce      json
// @Param        id   path  int  true  "Card ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /api/v2/cards/{id}/deactivate [put]
func (h *CardHandler) DeactivateCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.cardClient.DeactivateCard(middleware.GRPCContextWithChangedBy(c), &cardpb.DeactivateCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v3/cards/authorized-persons [post]
func (h *CardHandler) CreateAuthorizedPerson(c *gin.Context) {
	var req createAuthorizedPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
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
		handleGRPCError(c, err)
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
// @Summary      Create virtual card for authenticated client
// @Description  Creates a virtual card owned by the authenticated client (single_use or multi_use, 1-3 month expiry). owner_id in the body is ignored — the gateway derives the owner from the JWT.
// @Tags         cards
// @Accept       json
// @Produce      json
// @Param        body  body  createVirtualCardBody  true  "Virtual card details"
// @Security     ClientBearerAuth
// @Success      201  {object}  map[string]interface{}  "created virtual card"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "account does not belong to caller"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/me/cards/virtual [post]
func (h *CardHandler) CreateVirtualCard(c *gin.Context) {
	var body createVirtualCardBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	vcBrand, err := oneOf("card_brand", body.CardBrand, "visa", "mastercard", "dinacard", "amex")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	usageType, err := oneOf("usage_type", body.UsageType, "single_use", "multi_use")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := inRange("expiry_months", body.ExpiryMonths, 1, 3); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if usageType == "multi_use" && body.MaxUses < 2 {
		apiError(c, 400, ErrValidation, "multi_use cards must have max_uses >= 2")
		return
	}

	// Verify the account_number belongs to the caller. Body-supplied owner_id is ignored.
	acctResp, acctErr := h.accountClient.GetAccountByNumber(c.Request.Context(), &accountpb.GetAccountByNumberRequest{AccountNumber: body.AccountNumber})
	if acctErr != nil {
		handleGRPCError(c, acctErr)
		return
	}
	if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
		return
	}

	userID := uint64(c.GetInt64("principal_id"))
	resp, err := h.virtualCardClient.CreateVirtualCard(c.Request.Context(), &cardpb.CreateVirtualCardRequest{
		AccountNumber: body.AccountNumber,
		OwnerId:       userID, // forced from JWT — body value ignored
		CardBrand:     vcBrand,
		UsageType:     usageType,
		MaxUses:       body.MaxUses,
		ExpiryMonths:  body.ExpiryMonths,
		CardLimit:     body.CardLimit,
	})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/{id}/pin [post]
func (h *CardHandler) SetCardPin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid card id")
		return
	}
	var body setCardPinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := validatePin(body.Pin); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if card := h.loadCardAndEnforceOwnership(c, id); card == nil {
		return
	}
	resp, err := h.virtualCardClient.SetCardPin(c.Request.Context(), &cardpb.SetCardPinRequest{
		Id:  id,
		Pin: body.Pin,
	})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/{id}/verify-pin [post]
func (h *CardHandler) VerifyCardPin(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid card id")
		return
	}
	var body verifyCardPinBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := validatePin(body.Pin); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if card := h.loadCardAndEnforceOwnership(c, id); card == nil {
		return
	}
	resp, err := h.virtualCardClient.VerifyCardPin(c.Request.Context(), &cardpb.VerifyCardPinRequest{
		Id:  id,
		Pin: body.Pin,
	})
	if err != nil {
		handleGRPCError(c, err)
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
// @Router       /api/v2/cards/{id}/temporary-block [post]
func (h *CardHandler) TemporaryBlockCard(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid card id")
		return
	}
	var body temporaryBlockCardBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := inRange("duration_hours", body.DurationHours, 1, 720); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if card := h.loadCardAndEnforceOwnership(c, id); card == nil {
		return
	}
	resp, err := h.virtualCardClient.TemporaryBlockCard(middleware.GRPCContextWithChangedBy(c), &cardpb.TemporaryBlockCardRequest{
		Id:            id,
		DurationHours: body.DurationHours,
		Reason:        body.Reason,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// createCardRequestBody is the request body for creating a card request.
type createCardRequestBody struct {
	AccountNumber string `json:"account_number" binding:"required" example:"265-0000000001-00"`
	CardBrand     string `json:"card_brand" binding:"required" example:"visa"`
	CardType      string `json:"card_type" example:"debit"`
	CardName      string `json:"card_name" example:"My Visa Card"`
}

// rejectCardRequestBody is the request body for rejecting a card request.
type rejectCardRequestBody struct {
	Reason string `json:"reason" binding:"required" example:"Insufficient account history"`
}

// CreateCardRequest godoc
// @Summary      Create a card request
// @Description  Client submits a request to get a card for one of their accounts. Requires client authentication.
// @Tags         card-requests
// @Accept       json
// @Produce      json
// @Param        body  body  createCardRequestBody  true  "Card request details"
// @Security     ClientBearerAuth
// @Success      201  {object}  map[string]interface{}  "created card request"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests [post]
func (h *CardHandler) CreateCardRequest(c *gin.Context) {
	var body createCardRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	cardBrand, err := oneOf("card_brand", body.CardBrand, "visa", "mastercard", "dinacard", "amex")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	uid, _ := c.Get("principal_id")
	userID, ok := uid.(int64)
	if !ok || userID <= 0 {
		apiError(c, 401, ErrUnauthorized, "invalid client identity")
		return
	}

	resp, err := h.cardRequestClient.CreateCardRequest(c.Request.Context(), &cardpb.CreateCardRequestRequest{
		ClientId:      uint64(userID),
		AccountNumber: body.AccountNumber,
		CardBrand:     cardBrand,
		CardType:      body.CardType,
		CardName:      body.CardName,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, cardRequestToJSON(resp))
}

// ListMyCardRequests godoc
// @Summary      List my card requests
// @Description  Returns all card requests for the authenticated client.
// @Tags         card-requests
// @Produce      json
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20)"
// @Security     ClientBearerAuth
// @Success      200  {object}  map[string]interface{}  "list of card requests"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests/me [get]
func (h *CardHandler) ListMyCardRequests(c *gin.Context) {
	uid, _ := c.Get("principal_id")
	userID, ok := uid.(int64)
	if !ok || userID <= 0 {
		apiError(c, 401, ErrUnauthorized, "invalid client identity")
		return
	}

	page := int32(1)
	pageSize := int32(20)
	if p, err := strconv.ParseInt(c.Query("page"), 10, 32); err == nil && p > 0 {
		page = int32(p)
	}
	if ps, err := strconv.ParseInt(c.Query("page_size"), 10, 32); err == nil && ps > 0 {
		pageSize = int32(ps)
	}

	resp, err := h.cardRequestClient.ListCardRequestsByClient(c.Request.Context(), &cardpb.ListCardRequestsByClientRequest{
		ClientId: uint64(userID),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	requests := make([]gin.H, 0, len(resp.Requests))
	for _, r := range resp.Requests {
		requests = append(requests, cardRequestToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests, "total": resp.Total})
}

// ListCardRequests godoc
// @Summary      List all card requests
// @Description  Returns all card requests, optionally filtered by status. Requires employee authentication with cards.approve permission.
// @Tags         card-requests
// @Produce      json
// @Param        status     query  string  false  "Filter by status (pending, approved, rejected)"
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 20)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "list of card requests"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests [get]
func (h *CardHandler) ListCardRequests(c *gin.Context) {
	statusFilter := c.Query("status")
	if statusFilter != "" {
		var err error
		statusFilter, err = oneOf("status", statusFilter, "pending", "approved", "rejected")
		if err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	page := int32(1)
	pageSize := int32(20)
	if p, err := strconv.ParseInt(c.Query("page"), 10, 32); err == nil && p > 0 {
		page = int32(p)
	}
	if ps, err := strconv.ParseInt(c.Query("page_size"), 10, 32); err == nil && ps > 0 {
		pageSize = int32(ps)
	}

	resp, err := h.cardRequestClient.ListCardRequests(c.Request.Context(), &cardpb.ListCardRequestsRequest{
		Status:   statusFilter,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	requests := make([]gin.H, 0, len(resp.Requests))
	for _, r := range resp.Requests {
		requests = append(requests, cardRequestToJSON(r))
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests, "total": resp.Total})
}

// GetCardRequest godoc
// @Summary      Get a card request by ID
// @Description  Returns a single card request by ID. Accessible by both employees and the owning client.
// @Tags         card-requests
// @Produce      json
// @Param        id  path  int  true  "Card Request ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "card request"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests/{id} [get]
func (h *CardHandler) GetCardRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	resp, err := h.cardRequestClient.GetCardRequest(c.Request.Context(), &cardpb.GetCardRequestRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, cardRequestToJSON(resp))
}

// ApproveCardRequest godoc
// @Summary      Approve a card request
// @Description  Employee approves a pending card request, which creates the card. Requires cards.approve permission.
// @Tags         card-requests
// @Produce      json
// @Param        id  path  int  true  "Card Request ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "approved request and created card"
// @Failure      400  {object}  map[string]string       "invalid id"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      422  {object}  map[string]string       "already processed"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests/{id}/approve [post]
func (h *CardHandler) ApproveCardRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	uid, _ := c.Get("principal_id")
	employeeID, ok := uid.(int64)
	if !ok || employeeID <= 0 {
		apiError(c, 401, ErrUnauthorized, "invalid employee identity")
		return
	}

	resp, err := h.cardRequestClient.ApproveCardRequest(c.Request.Context(), &cardpb.ApproveCardRequestRequest{
		Id:         id,
		EmployeeId: uint64(employeeID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"request": cardRequestToJSON(resp.Request),
		"card":    cardToJSON(resp.Card),
	})
}

// RejectCardRequest godoc
// @Summary      Reject a card request
// @Description  Employee rejects a pending card request with a reason. Requires cards.approve permission.
// @Tags         card-requests
// @Accept       json
// @Produce      json
// @Param        id    path  int                    true  "Card Request ID"
// @Param        body  body  rejectCardRequestBody  true  "Rejection reason"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "rejected request"
// @Failure      400  {object}  map[string]string       "invalid input"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      404  {object}  map[string]string       "not found"
// @Failure      422  {object}  map[string]string       "already processed"
// @Failure      500  {object}  map[string]string       "error"
// @Router       /api/v2/cards/requests/{id}/reject [post]
func (h *CardHandler) RejectCardRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}

	var body rejectCardRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	uid, _ := c.Get("principal_id")
	employeeID, ok := uid.(int64)
	if !ok || employeeID <= 0 {
		apiError(c, 401, ErrUnauthorized, "invalid employee identity")
		return
	}

	resp, err := h.cardRequestClient.RejectCardRequest(c.Request.Context(), &cardpb.RejectCardRequestRequest{
		Id:         id,
		EmployeeId: uint64(employeeID),
		Reason:     body.Reason,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, cardRequestToJSON(resp))
}

// ListMyCards serves GET /api/me/cards.
func (h *CardHandler) ListMyCards(c *gin.Context) {
	userID, _ := c.Get("principal_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}
	resp, err := h.cardClient.ListCardsByClient(c.Request.Context(), &cardpb.ListCardsByClientRequest{ClientId: uint64(uid)})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	cards := make([]gin.H, 0, len(resp.Cards))
	for _, card := range resp.Cards {
		cards = append(cards, cardToJSON(card))
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

// GetMyCard serves GET /api/me/cards/:id — fetches card and verifies ownership.
func (h *CardHandler) GetMyCard(c *gin.Context) {
	userID, _ := c.Get("principal_id")
	uid, ok := userID.(int64)
	if !ok {
		apiError(c, 401, ErrUnauthorized, "invalid token claims")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid id")
		return
	}
	resp, err := h.cardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if resp.OwnerId != uint64(uid) {
		apiError(c, 403, ErrForbidden, "access denied")
		return
	}
	c.JSON(http.StatusOK, cardToJSON(resp))
}

// ListCards serves GET /api/cards — filters via ?client_id=X or ?account_number=X.
func (h *CardHandler) ListCards(c *gin.Context) {
	clientIDStr := c.Query("client_id")
	accountNumber := c.Query("account_number")

	if clientIDStr != "" && accountNumber != "" {
		apiError(c, 400, ErrValidation, "provide either client_id or account_number, not both")
		return
	}

	if clientIDStr != "" {
		clientID, err := strconv.ParseUint(clientIDStr, 10, 64)
		if err != nil {
			apiError(c, 400, ErrValidation, "invalid client_id")
			return
		}
		resp, err := h.cardClient.ListCardsByClient(c.Request.Context(), &cardpb.ListCardsByClientRequest{ClientId: clientID})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		cards := make([]gin.H, 0, len(resp.Cards))
		for _, card := range resp.Cards {
			cards = append(cards, cardToJSON(card))
		}
		c.JSON(http.StatusOK, gin.H{"cards": cards})
		return
	}

	if accountNumber != "" {
		resp, err := h.cardClient.ListCardsByAccount(c.Request.Context(), &cardpb.ListCardsByAccountRequest{AccountNumber: accountNumber})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		cards := make([]gin.H, 0, len(resp.Cards))
		for _, card := range resp.Cards {
			cards = append(cards, cardToJSON(card))
		}
		c.JSON(http.StatusOK, gin.H{"cards": cards})
		return
	}

	apiError(c, 400, ErrValidation, "provide client_id or account_number query parameter")
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

func cardRequestToJSON(r *cardpb.CardRequestResponse) gin.H {
	return gin.H{
		"id":             r.Id,
		"client_id":      r.ClientId,
		"account_number": r.AccountNumber,
		"card_brand":     r.CardBrand,
		"card_type":      r.CardType,
		"card_name":      r.CardName,
		"status":         r.Status,
		"reason":         r.Reason,
		"approved_by":    r.ApprovedBy,
		"created_at":     r.CreatedAt,
		"updated_at":     r.UpdatedAt,
	}
}

// loadCardAndEnforceOwnership fetches the card by ID and verifies the caller
// owns it. Returns the loaded card on success. On any failure it writes the
// error response and returns nil — callers must return on nil.
func (h *CardHandler) loadCardAndEnforceOwnership(c *gin.Context, id uint64) *cardpb.CardResponse {
	resp, err := h.cardClient.GetCard(c.Request.Context(), &cardpb.GetCardRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return nil
	}
	if ownErr := enforceOwnership(c, resp.OwnerId); ownErr != nil {
		return nil
	}
	return resp
}
