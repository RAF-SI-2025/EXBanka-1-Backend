package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/api-gateway/internal/handler"
)

func adminAuditHandler() *handler.AdminAuditHandler {
	return handler.NewAdminAuditHandler(
		&accountFullStub{},
		&stubCardClient{},
		&stubClientClient{},
		&stubCreditClient{},
		&stubUserClient{},
		&stubNotificationClient{},
		&stubTransactionClient{},
	)
}

func adminAuditRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := adminAuditHandler()
	r := gin.New()
	r.GET("/api/v3/admin/audit/clients-changelog", h.ListClientsChangelog)
	r.GET("/api/v3/admin/audit/accounts-changelog", h.ListAccountsChangelog)
	r.GET("/api/v3/admin/audit/cards-changelog", h.ListCardsChangelog)
	r.GET("/api/v3/admin/audit/loans-changelog", h.ListLoansChangelog)
	r.GET("/api/v3/admin/audit/employees-changelog", h.ListEmployeesChangelog)
	r.GET("/api/v3/admin/audit/cron-actions", h.ListCronActions)
	r.GET("/api/v3/admin/audit/saga-logs", h.ListSagaLogs)
	return r
}

func auditGet(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	adminAuditRouter().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestAdminAudit_ClientsChangelog_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?page=2&page_size=25")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"entries"`)
	require.Contains(t, rec.Body.String(), `"page":2`)
	require.Contains(t, rec.Body.String(), `"page_size":25`)
}

func TestAdminAudit_ClientsChangelog_WithDateAndActorFilters(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?since=2026-01-01&until=2026-12-31&actor_id=5&action=update")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminAudit_PageSizeTooLarge(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?page_size=500")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "page_size must be")
}

func TestAdminAudit_BadSince(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?since=01-01-2026")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "since must be YYYY-MM-DD")
}

func TestAdminAudit_BadUntil(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?until=nope")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "until must be YYYY-MM-DD")
}

func TestAdminAudit_BadActorID(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/clients-changelog?actor_id=-3")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "actor_id must be a positive integer")
}

func TestAdminAudit_DefaultsWhenInvalidPageAndSize(t *testing.T) {
	// page<1 and page_size<1 fall back to defaults (1 / 50) rather than erroring.
	rec := auditGet("/api/v3/admin/audit/clients-changelog?page=0&page_size=0")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"page":1`)
	require.Contains(t, rec.Body.String(), `"page_size":50`)
}

func TestAdminAudit_AccountsChangelog_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/accounts-changelog")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"entries"`)
}

func TestAdminAudit_CardsChangelog_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/cards-changelog")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminAudit_LoansChangelog_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/loans-changelog")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminAudit_EmployeesChangelog_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/employees-changelog")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminAudit_CronActions_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/cron-actions?page=1&page_size=10")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"entries"`)
	require.Contains(t, rec.Body.String(), `"page_size":10`)
}

func TestAdminAudit_SagaLogs_Success(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/saga-logs?status=completed&transaction_type=transfer&saga_id=abc")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"logs"`)
}

func TestAdminAudit_SagaLogs_PageSizeTooLarge(t *testing.T) {
	rec := auditGet("/api/v3/admin/audit/saga-logs?page_size=9999")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
