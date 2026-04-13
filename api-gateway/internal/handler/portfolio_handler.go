package handler

import (
	"net/http"
	"strconv"

	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type PortfolioHandler struct {
	portfolioClient stockpb.PortfolioGRPCServiceClient
	otcClient       stockpb.OTCGRPCServiceClient
	accountClient   accountpb.AccountServiceClient
}

func NewPortfolioHandler(
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	accountClient accountpb.AccountServiceClient,
) *PortfolioHandler {
	return &PortfolioHandler{portfolioClient: portfolioClient, otcClient: otcClient, accountClient: accountClient}
}

func (h *PortfolioHandler) ListHoldings(c *gin.Context) {
	userID := c.GetInt64("user_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	secType := c.Query("security_type")
	if secType != "" {
		if _, err := oneOf("security_type", secType, "stock", "futures", "option"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.portfolioClient.ListHoldings(c.Request.Context(), &stockpb.ListHoldingsRequest{
		UserId: uint64(userID), SecurityType: secType, Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"holdings": emptyIfNil(resp.Holdings), "total_count": resp.TotalCount})
}

func (h *PortfolioHandler) GetPortfolioSummary(c *gin.Context) {
	userID := c.GetInt64("user_id")
	resp, err := h.portfolioClient.GetPortfolioSummary(c.Request.Context(), &stockpb.GetPortfolioSummaryRequest{
		UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *PortfolioHandler) MakePublic(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid holding id")
		return
	}
	var req struct {
		Quantity int64 `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	userID := c.GetInt64("user_id")

	resp, err := h.portfolioClient.MakePublic(c.Request.Context(), &stockpb.MakePublicRequest{
		HoldingId: id, UserId: uint64(userID), Quantity: req.Quantity,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *PortfolioHandler) ExerciseOption(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid holding id")
		return
	}
	userID := c.GetInt64("user_id")

	resp, err := h.portfolioClient.ExerciseOption(c.Request.Context(), &stockpb.ExerciseOptionRequest{
		HoldingId: id, UserId: uint64(userID),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// --- OTC ---

func (h *PortfolioHandler) ListOTCOffers(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	secType := c.Query("security_type")
	if secType != "" {
		if _, err := oneOf("security_type", secType, "stock", "futures"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.otcClient.ListOffers(c.Request.Context(), &stockpb.ListOTCOffersRequest{
		SecurityType: secType, Ticker: c.Query("ticker"), Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"offers": emptyIfNil(resp.Offers), "total_count": resp.TotalCount})
}

// BuyOTCOfferOnBehalf godoc
// @Summary      Buy an OTC offer on behalf of a client
// @Description  Employee-only. Gateway verifies the account belongs to the named client.
// @Tags         otc
// @Accept       json
// @Produce      json
// @Param        id path integer true "Offer ID"
// @Param        body body object true "Purchase"
// @Security     BearerAuth
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v1/otc/admin/offers/{id}/buy [post]
func (h *PortfolioHandler) BuyOTCOfferOnBehalf(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid offer id")
		return
	}
	var req struct {
		ClientID  uint64 `json:"client_id"`
		AccountID uint64 `json:"account_id"`
		Quantity  int64  `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.ClientID == 0 || req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "client_id and account_id are required")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}

	acctResp, acctErr := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if acctErr != nil {
		handleGRPCError(c, acctErr)
		return
	}
	if acctResp.OwnerId != req.ClientID {
		apiError(c, 403, ErrForbidden, "account does not belong to client")
		return
	}

	employeeID := uint64(c.GetInt64("user_id"))
	resp, err := h.otcClient.BuyOffer(c.Request.Context(), &stockpb.BuyOTCOfferRequest{
		OfferId:            id,
		BuyerId:            req.ClientID,
		SystemType:         "employee",
		Quantity:           req.Quantity,
		AccountId:          req.AccountID,
		ActingEmployeeId:   employeeID,
		OnBehalfOfClientId: req.ClientID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *PortfolioHandler) BuyOTCOffer(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid offer id")
		return
	}
	var req struct {
		Quantity  int64  `json:"quantity"`
		AccountID uint64 `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, "invalid request body")
		return
	}
	if req.Quantity <= 0 {
		apiError(c, 400, ErrValidation, "quantity must be positive")
		return
	}
	if req.AccountID == 0 {
		apiError(c, 400, ErrValidation, "account_id is required")
		return
	}

	acctResp, err := h.accountClient.GetAccount(c.Request.Context(), &accountpb.GetAccountRequest{Id: req.AccountID})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	if ownErr := enforceOwnership(c, acctResp.OwnerId); ownErr != nil {
		return
	}

	userID := c.GetInt64("user_id")
	systemType := c.GetString("system_type")

	resp, err := h.otcClient.BuyOffer(c.Request.Context(), &stockpb.BuyOTCOfferRequest{
		OfferId: id, BuyerId: uint64(userID), SystemType: systemType,
		Quantity: req.Quantity, AccountId: req.AccountID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
