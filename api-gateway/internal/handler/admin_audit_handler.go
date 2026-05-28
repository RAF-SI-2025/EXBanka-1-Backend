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
	notifpb "github.com/exbanka/contract/notificationpb"
	userpb "github.com/exbanka/contract/userpb"
)

// AdminAuditHandler serves the six /api/v3/admin/audit/* routes.
// Each route fans out to one service's changelog (or notification-service's
// admin_audit_logs table) and returns a paginated, filterable list.
type AdminAuditHandler struct {
	accountClient accountpb.AccountServiceClient
	cardClient    cardpb.CardServiceClient
	clientClient  clientpb.ClientServiceClient
	creditClient  creditpb.CreditServiceClient
	userClient    userpb.UserServiceClient
	notifClient   notifpb.NotificationServiceClient
}

// NewAdminAuditHandler wires the six downstream gRPC clients.
func NewAdminAuditHandler(
	accountClient accountpb.AccountServiceClient,
	cardClient cardpb.CardServiceClient,
	clientClient clientpb.ClientServiceClient,
	creditClient creditpb.CreditServiceClient,
	userClient userpb.UserServiceClient,
	notifClient notifpb.NotificationServiceClient,
) *AdminAuditHandler {
	return &AdminAuditHandler{
		accountClient: accountClient,
		cardClient:    cardClient,
		clientClient:  clientClient,
		creditClient:  creditClient,
		userClient:    userClient,
		notifClient:   notifClient,
	}
}

// auditQueryParams holds the parsed common query parameters for audit endpoints.
type auditQueryParams struct {
	page     int32
	pageSize int32
	since    int64 // unix seconds
	until    int64 // unix seconds
	actorID  int64
	action   string
}

// parseAuditParams extracts common audit query params from the Gin context.
// Returns (params, ok). On validation error it writes an apiError and returns ok=false.
func parseAuditParams(c *gin.Context) (auditQueryParams, bool) {
	var p auditQueryParams

	pageStr := c.DefaultQuery("page", "1")
	pageVal, err := strconv.Atoi(pageStr)
	if err != nil || pageVal < 1 {
		pageVal = 1
	}
	p.page = int32(pageVal)

	psStr := c.DefaultQuery("page_size", "50")
	psVal, err := strconv.Atoi(psStr)
	if err != nil || psVal < 1 {
		psVal = 50
	}
	if psVal > 200 {
		apiError(c, http.StatusBadRequest, ErrValidation, "page_size must be <= 200")
		return p, false
	}
	p.pageSize = int32(psVal)

	if sinceStr := c.Query("since"); sinceStr != "" {
		t, err := time.Parse("2006-01-02", sinceStr)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "since must be YYYY-MM-DD")
			return p, false
		}
		p.since = t.UTC().Unix()
	}

	if untilStr := c.Query("until"); untilStr != "" {
		t, err := time.Parse("2006-01-02", untilStr)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, "until must be YYYY-MM-DD")
			return p, false
		}
		// end of day
		p.until = t.UTC().Add(24*time.Hour - time.Second).Unix()
	}

	if actorStr := c.Query("actor_id"); actorStr != "" {
		actorVal, err := strconv.ParseInt(actorStr, 10, 64)
		if err != nil || actorVal <= 0 {
			apiError(c, http.StatusBadRequest, ErrValidation, "actor_id must be a positive integer")
			return p, false
		}
		p.actorID = actorVal
	}

	p.action = c.Query("action")
	return p, true
}

// auditEntryJSON is the JSON shape returned by all five changelog audit endpoints.
type auditEntryJSON struct {
	ID         uint64 `json:"id"`
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	Action     string `json:"action"`
	FieldName  string `json:"field_name"`
	OldValue   string `json:"old_value"`
	NewValue   string `json:"new_value"`
	ActorID    int64  `json:"actor_id"`
	Timestamp  string `json:"timestamp"`
	Reason     string `json:"reason"`
}

// cronAuditEntryJSON is the JSON shape returned by the cron-actions endpoint.
type cronAuditEntryJSON struct {
	ID         uint64 `json:"id"`
	Action     string `json:"action"`
	Service    string `json:"service"`
	CronName   string `json:"cron_name"`
	EmployeeID int64  `json:"employee_id"`
	Reason     string `json:"reason"`
	Timestamp  string `json:"timestamp"`
}

func buildAuditResponse(entries []auditEntryJSON, total int64, page, pageSize int32) gin.H {
	return gin.H{
		"entries":   entries,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}
}

// changelogEntryProtoAudit matches the proto ChangelogEntry interface across services.
type changelogEntryProtoAudit interface {
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

func protoChangelogToAuditJSON[T changelogEntryProtoAudit](entries []T) []auditEntryJSON {
	out := make([]auditEntryJSON, len(entries))
	for i, e := range entries {
		out[i] = auditEntryJSON{
			ID:         e.GetId(),
			EntityType: e.GetEntityType(),
			EntityID:   e.GetEntityId(),
			Action:     e.GetAction(),
			FieldName:  e.GetFieldName(),
			OldValue:   e.GetOldValue(),
			NewValue:   e.GetNewValue(),
			ActorID:    e.GetChangedBy(),
			Timestamp:  time.Unix(e.GetChangedAt(), 0).UTC().Format(time.RFC3339),
			Reason:     e.GetReason(),
		}
	}
	return out
}

// ListClientsChangelog godoc
// @Summary      Global clients changelog (admin)
// @Description  Returns all changelog entries for the client-service across all clients. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee (actor) ID"
// @Param        action     query  string  false  "Filter by action string (exact match)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/clients-changelog [get]
func (h *AdminAuditHandler) ListClientsChangelog(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.clientClient.ListAllChangelogs(c.Request.Context(), &clientpb.ListAllChangelogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildAuditResponse(protoChangelogToAuditJSON(resp.GetEntries()), resp.GetTotal(), p.page, p.pageSize))
}

// ListAccountsChangelog godoc
// @Summary      Global accounts changelog (admin)
// @Description  Returns all changelog entries for the account-service across all accounts. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee (actor) ID"
// @Param        action     query  string  false  "Filter by action string (exact match)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/accounts-changelog [get]
func (h *AdminAuditHandler) ListAccountsChangelog(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.accountClient.ListAllChangelogs(c.Request.Context(), &accountpb.ListAllChangelogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildAuditResponse(protoChangelogToAuditJSON(resp.GetEntries()), resp.GetTotal(), p.page, p.pageSize))
}

// ListCardsChangelog godoc
// @Summary      Global cards changelog (admin)
// @Description  Returns all changelog entries for the card-service across all cards. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee (actor) ID"
// @Param        action     query  string  false  "Filter by action string (exact match)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/cards-changelog [get]
func (h *AdminAuditHandler) ListCardsChangelog(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.cardClient.ListAllChangelogs(c.Request.Context(), &cardpb.ListAllChangelogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildAuditResponse(protoChangelogToAuditJSON(resp.GetEntries()), resp.GetTotal(), p.page, p.pageSize))
}

// ListLoansChangelog godoc
// @Summary      Global loans changelog (admin)
// @Description  Returns all changelog entries for the credit-service across all loans. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee (actor) ID"
// @Param        action     query  string  false  "Filter by action string (exact match)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/loans-changelog [get]
func (h *AdminAuditHandler) ListLoansChangelog(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.creditClient.ListAllChangelogs(c.Request.Context(), &creditpb.ListAllChangelogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildAuditResponse(protoChangelogToAuditJSON(resp.GetEntries()), resp.GetTotal(), p.page, p.pageSize))
}

// ListEmployeesChangelog godoc
// @Summary      Global employees changelog (admin)
// @Description  Returns all changelog entries for the user-service across all employees. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee (actor) ID"
// @Param        action     query  string  false  "Filter by action string (exact match)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/employees-changelog [get]
func (h *AdminAuditHandler) ListEmployeesChangelog(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.userClient.ListAllChangelogs(c.Request.Context(), &userpb.ListAllChangelogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildAuditResponse(protoChangelogToAuditJSON(resp.GetEntries()), resp.GetTotal(), p.page, p.pageSize))
}

// ListCronActions godoc
// @Summary      Global admin cron-action audit log (admin)
// @Description  Returns all admin cron-control action entries (trigger/pause/resume) persisted by notification-service. Filterable by date range, actor, and action. Admin-only (requires admin.audit.view).
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 50, max 200)"
// @Param        since      query  string  false  "Filter from date YYYY-MM-DD (inclusive)"
// @Param        until      query  string  false  "Filter to date YYYY-MM-DD (inclusive)"
// @Param        actor_id   query  int     false  "Filter by employee ID"
// @Param        action     query  string  false  "Filter by action string (trigger|pause|resume)"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/audit/cron-actions [get]
func (h *AdminAuditHandler) ListCronActions(c *gin.Context) {
	p, ok := parseAuditParams(c)
	if !ok {
		return
	}
	resp, err := h.notifClient.ListAdminAuditLogs(c.Request.Context(), &notifpb.ListAdminAuditLogsRequest{
		Page:     p.page,
		PageSize: p.pageSize,
		Since:    p.since,
		Until:    p.until,
		ActorId:  p.actorID,
		Action:   p.action,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	rawEntries := resp.GetEntries()
	entries := make([]cronAuditEntryJSON, len(rawEntries))
	for i, e := range rawEntries {
		entries[i] = cronAuditEntryJSON{
			ID:         e.GetId(),
			Action:     e.GetAction(),
			Service:    e.GetService(),
			CronName:   e.GetCronName(),
			EmployeeID: e.GetEmployeeId(),
			Reason:     e.GetReason(),
			Timestamp:  time.Unix(e.GetTimestamp(), 0).UTC().Format(time.RFC3339),
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"entries":   entries,
		"total":     resp.GetTotal(),
		"page":      p.page,
		"page_size": p.pageSize,
	})
}
