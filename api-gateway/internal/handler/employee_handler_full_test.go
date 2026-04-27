// api-gateway/internal/handler/employee_handler_full_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	userpb "github.com/exbanka/contract/userpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func employeeRouter(h *handler.EmployeeHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(99)) }
	r.GET("/employees", withCtx, h.ListEmployees)
	r.GET("/employees/:id", withCtx, h.GetEmployee)
	r.POST("/employees", withCtx, h.CreateEmployee)
	r.PUT("/employees/:id", withCtx, h.UpdateEmployee)
	return r
}

func TestEmployee_ListEmployees_Empty(t *testing.T) {
	user := &stubUserClient{
		listEmployeesFn: func(_ *userpb.ListEmployeesRequest) (*userpb.ListEmployeesResponse, error) {
			return &userpb.ListEmployeesResponse{Employees: nil, TotalCount: 0}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "total_count")
}

func TestEmployee_ListEmployees_WithEntries(t *testing.T) {
	user := &stubUserClient{
		listEmployeesFn: func(_ *userpb.ListEmployeesRequest) (*userpb.ListEmployeesResponse, error) {
			return &userpb.ListEmployeesResponse{
				Employees: []*userpb.EmployeeResponse{{Id: 1, FirstName: "A"}, {Id: 2, FirstName: "B"}},
				TotalCount: 2,
			}, nil
		},
	}
	auth := &stubAuthClient{
		getStatusBatchFn: func(req *authpb.GetAccountStatusBatchRequest) (*authpb.GetAccountStatusBatchResponse, error) {
			require.Equal(t, "employee", req.PrincipalType)
			require.Equal(t, []int64{1, 2}, req.PrincipalIds)
			return &authpb.GetAccountStatusBatchResponse{
				Entries: []*authpb.AccountStatusEntry{{PrincipalId: 1, Active: true}, {PrincipalId: 2, Active: false}},
			}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, auth)
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees?page=2&page_size=5", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"total_count":2`)
}

func TestEmployee_ListEmployees_GRPCError(t *testing.T) {
	user := &stubUserClient{
		listEmployeesFn: func(_ *userpb.ListEmployeesRequest) (*userpb.ListEmployeesResponse, error) {
			return nil, status.Error(codes.Internal, "db down")
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestEmployee_GetEmployee_Success(t *testing.T) {
	h := handler.NewEmployeeHandler(&stubUserClient{}, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees/42", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"active":true`)
}

func TestEmployee_GetEmployee_BadID(t *testing.T) {
	h := handler.NewEmployeeHandler(&stubUserClient{}, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEmployee_GetEmployee_NotFound(t *testing.T) {
	user := &stubUserClient{
		getEmployeeFn: func(_ *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return nil, status.Error(codes.NotFound, "no such employee")
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("GET", "/employees/999", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestEmployee_CreateEmployee_Success(t *testing.T) {
	user := &stubUserClient{
		createEmployeeFn: func(req *userpb.CreateEmployeeRequest) (*userpb.EmployeeResponse, error) {
			require.Equal(t, "John", req.FirstName)
			require.Equal(t, "1234567890123", req.Jmbg)
			return &userpb.EmployeeResponse{Id: 1, FirstName: req.FirstName, LastName: req.LastName}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	body := `{"first_name":"John","last_name":"Doe","date_of_birth":946684800,"email":"j@d.com","jmbg":"1234567890123","username":"jdoe","role":"EmployeeBasic"}`
	req := httptest.NewRequest("POST", "/employees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"first_name":"John"`)
	require.Contains(t, rec.Body.String(), `"active":false`)
}

func TestEmployee_CreateEmployee_ValidationFailure(t *testing.T) {
	h := handler.NewEmployeeHandler(&stubUserClient{}, &stubAuthClient{})
	r := employeeRouter(h)
	body := `{"first_name":"John"}`
	req := httptest.NewRequest("POST", "/employees", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEmployee_UpdateEmployee_Success(t *testing.T) {
	user := &stubUserClient{
		getEmployeeFn: func(_ *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return &userpb.EmployeeResponse{Id: 1, Role: "EmployeeBasic"}, nil
		},
		updateEmployeeFn: func(req *userpb.UpdateEmployeeRequest) (*userpb.EmployeeResponse, error) {
			require.NotNil(t, req.LastName)
			require.Equal(t, "Updated", *req.LastName)
			return &userpb.EmployeeResponse{Id: 1, LastName: "Updated"}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	body := `{"last_name":"Updated"}`
	req := httptest.NewRequest("PUT", "/employees/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"last_name":"Updated"`)
}

func TestEmployee_UpdateEmployee_AdminBlocked(t *testing.T) {
	user := &stubUserClient{
		getEmployeeFn: func(_ *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return &userpb.EmployeeResponse{Id: 1, Role: "EmployeeAdmin"}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, &stubAuthClient{})
	r := employeeRouter(h)
	body := `{"last_name":"X"}`
	req := httptest.NewRequest("PUT", "/employees/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "cannot edit admin employees")
}

func TestEmployee_UpdateEmployee_BadID(t *testing.T) {
	h := handler.NewEmployeeHandler(&stubUserClient{}, &stubAuthClient{})
	r := employeeRouter(h)
	req := httptest.NewRequest("PUT", "/employees/abc", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEmployee_UpdateEmployee_OnlyActive(t *testing.T) {
	// No profile fields, only "active"
	user := &stubUserClient{
		getEmployeeFn: func(_ *userpb.GetEmployeeRequest) (*userpb.EmployeeResponse, error) {
			return &userpb.EmployeeResponse{Id: 1, Role: "EmployeeBasic"}, nil
		},
	}
	auth := &stubAuthClient{
		setAccountStatusFn: func(req *authpb.SetAccountStatusRequest) (*authpb.SetAccountStatusResponse, error) {
			require.Equal(t, true, req.Active)
			return &authpb.SetAccountStatusResponse{}, nil
		},
	}
	h := handler.NewEmployeeHandler(user, auth)
	r := employeeRouter(h)
	body := `{"active":true}`
	req := httptest.NewRequest("PUT", "/employees/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}
