package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	accountpb "github.com/exbanka/contract/accountpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	userpb "github.com/exbanka/contract/userpb"
)

// ChangelogHandler exposes audit-log (changelog) endpoints for the five
// services that track entity mutations in their own changelog tables.
type ChangelogHandler struct {
	accountClient accountpb.AccountServiceClient
	cardClient    cardpb.CardServiceClient
	clientClient  clientpb.ClientServiceClient
	creditClient  creditpb.CreditServiceClient
	userClient    userpb.UserServiceClient
}

// NewChangelogHandler wires the five downstream gRPC clients.
func NewChangelogHandler(
	accountClient accountpb.AccountServiceClient,
	cardClient cardpb.CardServiceClient,
	clientClient clientpb.ClientServiceClient,
	creditClient creditpb.CreditServiceClient,
	userClient userpb.UserServiceClient,
) *ChangelogHandler {
	return &ChangelogHandler{
		accountClient: accountClient,
		cardClient:    cardClient,
		clientClient:  clientClient,
		creditClient:  creditClient,
		userClient:    userClient,
	}
}

// changelogEntryJSON is the JSON shape returned by every changelog endpoint.
type changelogEntryJSON struct {
	ID         uint64 `json:"id"`
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	Action     string `json:"action"`
	FieldName  string `json:"field_name"`
	OldValue   string `json:"old_value"`
	NewValue   string `json:"new_value"`
	ChangedBy  int64  `json:"changed_by"`
	ChangedAt  string `json:"changed_at"`
	Reason     string `json:"reason"`
}

// parseChangelogParams extracts :id, ?page, ?page_size from a Gin context.
// Returns (entityID, page, pageSize, ok). On error it writes an apiError and
// returns ok=false; callers must return immediately when ok==false.
func parseChangelogParams(c *gin.Context) (int64, int32, int32, bool) {
	rawID := c.Param("id")
	entityID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || entityID <= 0 {
		apiError(c, http.StatusBadRequest, ErrValidation, "id must be a positive integer")
		return 0, 0, 0, false
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return entityID, int32(page), int32(pageSize), true
}

// @Summary      Get account changelog
// @Description  Returns paginated audit-log entries for a bank account.
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id         path   int  true   "Account ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20, max 200)"
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/accounts/{id}/changelog [get]
func (h *ChangelogHandler) GetAccountChangelog(c *gin.Context) {
	entityID, page, pageSize, ok := parseChangelogParams(c)
	if !ok {
		return
	}
	resp, err := h.accountClient.ListChangelog(c.Request.Context(), &accountpb.ListChangelogRequest{
		EntityType: "account",
		EntityId:   entityID,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildChangelogResponse(resp.GetEntries(), resp.GetTotal(), int(page), int(pageSize)))
}

// @Summary      Get card changelog
// @Description  Returns paginated audit-log entries for a payment card.
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id         path   int  true   "Card ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20, max 200)"
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/cards/{id}/changelog [get]
func (h *ChangelogHandler) GetCardChangelog(c *gin.Context) {
	entityID, page, pageSize, ok := parseChangelogParams(c)
	if !ok {
		return
	}
	resp, err := h.cardClient.ListChangelog(c.Request.Context(), &cardpb.ListChangelogRequest{
		EntityType: "card",
		EntityId:   entityID,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildChangelogResponse(resp.GetEntries(), resp.GetTotal(), int(page), int(pageSize)))
}

// @Summary      Get client changelog
// @Description  Returns paginated audit-log entries for a bank client.
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id         path   int  true   "Client ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20, max 200)"
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/clients/{id}/changelog [get]
func (h *ChangelogHandler) GetClientChangelog(c *gin.Context) {
	entityID, page, pageSize, ok := parseChangelogParams(c)
	if !ok {
		return
	}
	resp, err := h.clientClient.ListChangelog(c.Request.Context(), &clientpb.ListChangelogRequest{
		EntityType: "client",
		EntityId:   entityID,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildChangelogResponse(resp.GetEntries(), resp.GetTotal(), int(page), int(pageSize)))
}

// @Summary      Get loan changelog
// @Description  Returns paginated audit-log entries for a loan.
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id         path   int  true   "Loan ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20, max 200)"
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/loans/{id}/changelog [get]
func (h *ChangelogHandler) GetLoanChangelog(c *gin.Context) {
	entityID, page, pageSize, ok := parseChangelogParams(c)
	if !ok {
		return
	}
	resp, err := h.creditClient.ListChangelog(c.Request.Context(), &creditpb.ListChangelogRequest{
		EntityType: "loan",
		EntityId:   entityID,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildChangelogResponse(resp.GetEntries(), resp.GetTotal(), int(page), int(pageSize)))
}

// @Summary      Get employee changelog
// @Description  Returns paginated audit-log entries for an employee.
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id         path   int  true   "Employee ID"
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 20, max 200)"
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/employees/{id}/changelog [get]
func (h *ChangelogHandler) GetEmployeeChangelog(c *gin.Context) {
	entityID, page, pageSize, ok := parseChangelogParams(c)
	if !ok {
		return
	}
	resp, err := h.userClient.ListChangelog(c.Request.Context(), &userpb.ListChangelogRequest{
		EntityType: "employee",
		EntityId:   entityID,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildChangelogResponse(resp.GetEntries(), resp.GetTotal(), int(page), int(pageSize)))
}

// buildChangelogResponse is a generic helper that converts proto ChangelogEntry
// slices (from any service) into the canonical JSON envelope used by all five
// changelog endpoints.
//
// The helper is generic over the proto message type via an interface since each
// service generates its own ChangelogEntry struct from its own proto package.
// We use a duck-typed interface (changelogEntryProto) rather than a type
// parameter so the caller doesn't need to pass a type argument.
type changelogEntryProto interface {
	GetId() uint64
	GetEntityType() string
	GetEntityId() int64
	GetAction() string
	GetFieldName() string
	GetOldValue() string
	GetNewValue() string
	GetChangedBy() int64
	GetChangedAt() int64
	GetReason() string
}

func buildChangelogResponse[T changelogEntryProto](entries []T, total int64, page, pageSize int) gin.H {
	out := make([]changelogEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = changelogEntryJSON{
			ID:         e.GetId(),
			EntityType: e.GetEntityType(),
			EntityID:   e.GetEntityId(),
			Action:     e.GetAction(),
			FieldName:  e.GetFieldName(),
			OldValue:   e.GetOldValue(),
			NewValue:   e.GetNewValue(),
			ChangedBy:  e.GetChangedBy(),
			ChangedAt:  time.Unix(e.GetChangedAt(), 0).UTC().Format(time.RFC3339),
			Reason:     e.GetReason(),
		}
	}
	return gin.H{
		"entries":   out,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}
