package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	userpb "github.com/exbanka/contract/userpb"
)

// BootstrapHandler seeds the initial admin employee for test/dev environments.
// It is disabled (returns 404) when BOOTSTRAP_SECRET env var is not set.
type BootstrapHandler struct {
	userClient userpb.UserServiceClient
	authClient authpb.AuthServiceClient
	secret     string
}

func NewBootstrapHandler(
	userClient userpb.UserServiceClient,
	authClient authpb.AuthServiceClient,
	secret string,
) *BootstrapHandler {
	return &BootstrapHandler{userClient: userClient, authClient: authClient, secret: secret}
}

type bootstrapRequest struct {
	Secret string `json:"secret" binding:"required"`
	Email  string `json:"email"  binding:"required,email"`
}

// Bootstrap godoc
// @Summary      Seed the initial admin employee (test/dev only)
// @Description  Creates the admin employee in user-service and provisions an auth account +
// @Description  activation token in auth-service, which publishes the token to the
// @Description  notification.send-email Kafka topic. Protected by the BOOTSTRAP_SECRET env var;
// @Description  returns 404 when the secret is not configured (safe for production).
// @Description  Idempotent: safe to call multiple times (each call issues a fresh activation token).
// @Tags         bootstrap
// @Accept       json
// @Produce      json
// @Param        body  body      bootstrapRequest   true  "Bootstrap credentials"
// @Success      200   {object}  map[string]string  "message: bootstrap complete"
// @Failure      400   {object}  map[string]string  "bad request"
// @Failure      404   {object}  map[string]string  "not found (secret not set or wrong)"
// @Failure      500   {object}  map[string]string  "upstream error"
// @Router       /api/bootstrap [post]
func (h *BootstrapHandler) Bootstrap(c *gin.Context) {
	// Return 404 (not 403) so the endpoint is invisible in production.
	if h.secret == "" {
		apiError(c, 404, ErrNotFound, "not found")
		return
	}

	var req bootstrapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if req.Secret != h.secret {
		apiError(c, 404, ErrNotFound, "not found")
		return
	}

	ctx := c.Request.Context()

	// Create the admin employee. Ignore AlreadyExists (idempotent).
	_, createErr := h.userClient.CreateEmployee(ctx, &userpb.CreateEmployeeRequest{
		FirstName:   "System",
		LastName:    "Admin",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Gender:      "other",
		Email:       req.Email,
		Phone:       "+381000000000",
		Address:     "System Account",
		Jmbg:        "0101990000000",
		Username:    "admin",
		Position:    "System Administrator",
		Department:  "IT",
		Role:        "EmployeeAdmin",
	})
	if createErr != nil {
		st, _ := status.FromError(createErr)
		// Only AlreadyExists is acceptable (email or JMBG duplicate) — employee already exists.
		// Do NOT swallow codes.Internal: a real infrastructure error must surface as a 500.
		if st.Code() != codes.AlreadyExists {
			apiError(c, 500, ErrInternal, "create employee: "+createErr.Error())
			return
		}
	}

	// Lookup the employee by email filter to obtain the principal ID.
	// Use PageSize 50 to tolerate partial-match results; the loop below finds the exact match.
	listResp, err := h.userClient.ListEmployees(ctx, &userpb.ListEmployeesRequest{
		EmailFilter: req.Email,
		Page:        1,
		PageSize:    50,
	})
	if err != nil {
		apiError(c, 500, ErrInternal, "list employees: "+err.Error())
		return
	}
	// Find exact email match (filter is partial — ILIKE query in repo).
	var principalID int64
	for _, emp := range listResp.Employees {
		if emp.Email == req.Email {
			principalID = emp.Id
			break
		}
	}
	if principalID == 0 {
		apiError(c, 500, ErrInternal, "admin employee not found after create")
		return
	}

	// Provision auth account + activation token. This publishes an ACTIVATION message
	// to the notification.send-email Kafka topic, which test-app scans for the token.
	_, err = h.authClient.CreateAccount(ctx, &authpb.CreateAccountRequest{
		PrincipalId:   principalID,
		Email:         req.Email,
		FirstName:     "System",
		PrincipalType: "employee",
	})
	if err != nil {
		apiError(c, 500, ErrInternal, "provision auth account: "+err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "bootstrap complete"})
}
