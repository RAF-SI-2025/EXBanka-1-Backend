package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
)

// setPrincipal mimics what AuthMiddleware sets after JWT validation.
// It writes the *new* key names (principal_type / principal_id) that
// ResolveIdentity reads. A separate test exercises the backward-compat
// shim that also accepts the legacy system_type / user_id keys.
func setPrincipal(pType string, pID uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("principal_type", pType)
		c.Set("principal_id", pID)
		c.Next()
	}
}

// setLegacyPrincipal exercises the temporary backward-compat shim:
// it writes the *old* JWT key names (system_type / user_id) that
// AuthMiddleware sets pre-Task 2.
func setLegacyPrincipal(pType string, pID uint64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("system_type", pType)
		c.Set("user_id", pID)
		c.Next()
	}
}

func TestResolveIdentity_OwnerIsPrincipal_Client(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x",
		setPrincipal("client", 42),
		middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" {
				t.Errorf("OwnerType: want client, got %q", id.OwnerType)
			}
			if id.OwnerID == nil || *id.OwnerID != 42 {
				t.Errorf("OwnerID: want &42, got %v", id.OwnerID)
			}
			if id.ActingEmployeeID != nil {
				t.Errorf("ActingEmployeeID: want nil, got %v", id.ActingEmployeeID)
			}
			if id.PrincipalType != "client" || id.PrincipalID != 42 {
				t.Errorf("Principal: want client/42, got %s/%d", id.PrincipalType, id.PrincipalID)
			}
			c.Status(http.StatusOK)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}

func TestResolveIdentity_OwnerIsBankIfEmployee_EmployeeBecomesBank(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x",
		setPrincipal("employee", 7),
		middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "bank" {
				t.Errorf("OwnerType: want bank, got %q", id.OwnerType)
			}
			if id.OwnerID != nil {
				t.Errorf("OwnerID: want nil, got %v", id.OwnerID)
			}
			if id.ActingEmployeeID == nil || *id.ActingEmployeeID != 7 {
				t.Errorf("ActingEmployeeID: want &7, got %v", id.ActingEmployeeID)
			}
			c.Status(http.StatusOK)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}

func TestResolveIdentity_OwnerIsBankIfEmployee_ClientStaysClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x",
		setPrincipal("client", 42),
		middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" {
				t.Errorf("OwnerType: want client, got %q", id.OwnerType)
			}
			if id.OwnerID == nil || *id.OwnerID != 42 {
				t.Errorf("OwnerID: want &42, got %v", id.OwnerID)
			}
			if id.ActingEmployeeID != nil {
				t.Errorf("ActingEmployeeID: want nil, got %v", id.ActingEmployeeID)
			}
			c.Status(http.StatusOK)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}

func TestResolveIdentity_OwnerFromURLParam_EmployeeOnBehalf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clients/:client_id/x",
		setPrincipal("employee", 7),
		middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.OwnerType != "client" {
				t.Errorf("OwnerType: want client, got %q", id.OwnerType)
			}
			if id.OwnerID == nil || *id.OwnerID != 99 {
				t.Errorf("OwnerID: want &99, got %v", id.OwnerID)
			}
			if id.ActingEmployeeID == nil || *id.ActingEmployeeID != 7 {
				t.Errorf("ActingEmployeeID: want &7, got %v", id.ActingEmployeeID)
			}
			c.Status(http.StatusOK)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clients/99/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}

func TestResolveIdentity_OwnerFromURLParam_NonNumericReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/clients/:client_id/x",
		setPrincipal("client", 1),
		middleware.ResolveIdentity(middleware.OwnerFromURLParam, "client_id"),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clients/abc/x", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
}

func TestResolveIdentity_NoPrincipalReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Note: no setPrincipal — neither principal_type nor system_type set.
	r.GET("/x",
		middleware.ResolveIdentity(middleware.OwnerIsPrincipal),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", w.Code)
	}
}

// TestResolveIdentity_LegacyKeys exercises the temporary backward-compat
// shim that reads legacy system_type / user_id keys when the new
// principal_type / principal_id keys are not set. Remove once Task 2
// renames the JWT/middleware keys.
func TestResolveIdentity_LegacyKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x",
		setLegacyPrincipal("employee", 7),
		middleware.ResolveIdentity(middleware.OwnerIsBankIfEmployee),
		func(c *gin.Context) {
			id := c.MustGet("identity").(*middleware.ResolvedIdentity)
			if id.PrincipalType != "employee" || id.PrincipalID != 7 {
				t.Errorf("Principal: want employee/7, got %s/%d", id.PrincipalType, id.PrincipalID)
			}
			if id.OwnerType != "bank" || id.OwnerID != nil {
				t.Errorf("Owner: want bank/nil, got %s/%v", id.OwnerType, id.OwnerID)
			}
			if id.ActingEmployeeID == nil || *id.ActingEmployeeID != 7 {
				t.Errorf("ActingEmployeeID: want &7, got %v", id.ActingEmployeeID)
			}
			c.Status(http.StatusOK)
		})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}
