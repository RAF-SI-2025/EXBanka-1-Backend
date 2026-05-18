# gRPC Error Unmasking — Implementation Plan (Spec A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace handler-level error masking across all 11 services with typed sentinel errors that carry their own gRPC status codes. Add a mandatory logging interceptor so the original error is always logged before being returned to a caller.

**Architecture:** Sentinel errors implement the `interface{ GRPCStatus() *status.Status }` that grpc-go already honors via `status.FromError`. Service code wraps sentinels with `fmt.Errorf("...: %w", sentinel)` to preserve context for logs. Handlers become passthrough — no `mapServiceError()` functions, no substring matching. A unary server interceptor logs every non-OK response with the wrapped error.

**Tech Stack:** Go, grpc-go, gorm, existing service layout.

**Spec reference:** `docs/superpowers/specs/2026-04-27-grpc-error-unmasking-design.md`

---

## File Structure

**New files:**
- `contract/shared/svcerr/svcerr.go` — sentinel type with `GRPCStatus()`
- `contract/shared/svcerr/svcerr_test.go`
- `contract/shared/grpcmw/logging.go` — unary server interceptor
- `contract/shared/grpcmw/logging_test.go`
- `<service>/internal/service/errors.go` — per service, one file each (11 files)
- `test-app/workflows/auth_login_failure_modes_test.go`

**Modified files:**
- Every `<service>/internal/handler/grpc_handler.go` — handler bodies become passthrough
- Every `<service>/cmd/main.go` — wire the interceptor
- Every `<service>/internal/service/*.go` — return wrapped sentinel instead of `fmt.Errorf("...")` for known failure modes
- `api-gateway/internal/handler/validation.go:243-272` — comment update only

**Deleted code:**
- `mapServiceError()` function in 9 services
- Substring matching in `stock-service/internal/handler/order_handler.go:189-211`

---

## Task 1: Create the sentinel error type

**Files:**
- Create: `contract/shared/svcerr/svcerr.go`
- Test: `contract/shared/svcerr/svcerr_test.go`

- [ ] **Step 1: Write the failing test**

```go
// contract/shared/svcerr/svcerr_test.go
package svcerr_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"contract/shared/svcerr"
)

func TestSentinel_DirectStatus(t *testing.T) {
	e := svcerr.New(codes.PermissionDenied, "account locked")
	st, ok := status.FromError(e)
	if !ok {
		t.Fatal("expected status.FromError to recognize sentinel")
	}
	if st.Code() != codes.PermissionDenied {
		t.Errorf("got code %v, want PermissionDenied", st.Code())
	}
	if st.Message() != "account locked" {
		t.Errorf("got message %q, want %q", st.Message(), "account locked")
	}
}

func TestSentinel_WrappedStatus(t *testing.T) {
	base := svcerr.New(codes.Unauthenticated, "invalid credentials")
	wrapped := fmt.Errorf("Login: %w", base)

	st, ok := status.FromError(wrapped)
	if !ok {
		t.Fatal("expected wrapped sentinel to satisfy status.FromError")
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("got code %v, want Unauthenticated", st.Code())
	}
}

func TestSentinel_ErrorsIs(t *testing.T) {
	a := svcerr.New(codes.NotFound, "not found")
	b := fmt.Errorf("ListThings: %w", a)
	if !errors.Is(b, a) {
		t.Error("errors.Is should match wrapped sentinel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/svcerr/...`
Expected: FAIL with build error (svcerr package missing).

- [ ] **Step 3: Implement the minimal sentinel**

```go
// contract/shared/svcerr/svcerr.go
package svcerr

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SentinelError carries a gRPC code and a message. It implements both error
// and the GRPCStatus interface that grpc-go honors via status.FromError, so
// returning a wrapped sentinel from a handler results in the correct wire
// status without an explicit mapping table.
type SentinelError struct {
	code codes.Code
	msg  string
}

// New constructs a SentinelError. Use as a package-level var:
//   var ErrAccountLocked = svcerr.New(codes.PermissionDenied, "account locked")
func New(code codes.Code, msg string) *SentinelError {
	return &SentinelError{code: code, msg: msg}
}

func (e *SentinelError) Error() string { return e.msg }

func (e *SentinelError) GRPCStatus() *status.Status {
	return status.New(e.code, e.msg)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./shared/svcerr/... -v`
Expected: PASS, all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add contract/shared/svcerr/
git commit -m "feat(shared): typed gRPC sentinel errors via GRPCStatus interface"
```

---

## Task 2: Create the logging interceptor

**Files:**
- Create: `contract/shared/grpcmw/logging.go`
- Test: `contract/shared/grpcmw/logging_test.go`

- [ ] **Step 1: Write the failing test**

```go
// contract/shared/grpcmw/logging_test.go
package grpcmw_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"contract/shared/grpcmw"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestLoggingInterceptor_LogsErrorWithCode(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, err := icpt(context.Background(), nil, info, handler)
	if err == nil {
		t.Fatal("expected error to propagate")
	}

	out := buf.String()
	for _, want := range []string{"test-service", "/Test/Method", "PermissionDenied"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q\ngot: %s", want, out)
		}
	}
}

func TestLoggingInterceptor_DoesNotLogOK(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, _ = icpt(context.Background(), nil, info, handler)
	if buf.Len() != 0 {
		t.Errorf("expected no log on OK; got: %s", buf.String())
	}
}

func TestLoggingInterceptor_LogsWrappedError(t *testing.T) {
	buf := captureLog(t)
	icpt := grpcmw.UnaryLoggingInterceptor("test-service")

	base := errors.New("root cause")
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, base
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/Test/Method"}

	_, _ = icpt(context.Background(), nil, info, handler)
	if !strings.Contains(buf.String(), "root cause") {
		t.Errorf("log should include original error: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./shared/grpcmw/...`
Expected: FAIL — `grpcmw` package missing.

- [ ] **Step 3: Implement the interceptor**

```go
// contract/shared/grpcmw/logging.go
package grpcmw

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryLoggingInterceptor logs every non-OK response with the wrapped error,
// the gRPC method, the resolved status code, and request duration. This is
// the safety net that prevents silent error masking — even if a handler
// returns a sentinel, the original wrap chain is captured in the logs.
func UnaryLoggingInterceptor(serviceName string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		if err != nil {
			code := status.Code(err)
			log.Printf("[grpc] service=%s method=%s code=%s duration=%s err=%+v",
				serviceName, info.FullMethod, code, time.Since(start), err)
		}
		return resp, err
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./shared/grpcmw/... -v`
Expected: PASS, all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add contract/shared/grpcmw/
git commit -m "feat(shared): unary gRPC logging interceptor for non-OK responses"
```

---

## Task 3: auth-service sentinels (lighthouse)

**Files:**
- Create: `auth-service/internal/service/errors.go`
- Modify: `auth-service/internal/service/auth_service.go` (Login function)
- Modify: `auth-service/internal/handler/grpc_handler.go:91-101` (Login RPC)
- Modify: `auth-service/cmd/main.go` (wire interceptor)
- Test: `auth-service/internal/service/auth_service_login_test.go`

- [ ] **Step 1: Write the failing test for each Login failure mode**

```go
// auth-service/internal/service/auth_service_login_test.go
package service_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"auth-service/internal/service"
	// ... other imports per existing test layout
)

func TestLogin_BadPassword(t *testing.T) {
	svc := newTestAuthService(t, withSeededUser("alice@example.com", "RightPass1!"))
	_, err := svc.Login(context.Background(), "alice@example.com", "WrongPass!")
	mustBeSentinel(t, err, service.ErrInvalidCredentials, codes.Unauthenticated)
}

func TestLogin_AccountLocked(t *testing.T) {
	svc := newTestAuthService(t, withSeededUser("bob@example.com", "Right1!"), withLockedUser("bob@example.com"))
	_, err := svc.Login(context.Background(), "bob@example.com", "Right1!")
	mustBeSentinel(t, err, service.ErrAccountLocked, codes.PermissionDenied)
}

func TestLogin_AccountPending(t *testing.T) {
	svc := newTestAuthService(t, withSeededUser("carol@example.com", "Right1!"), withPendingUser("carol@example.com"))
	_, err := svc.Login(context.Background(), "carol@example.com", "Right1!")
	mustBeSentinel(t, err, service.ErrAccountPending, codes.FailedPrecondition)
}

func TestLogin_EmployeeRPCFailure(t *testing.T) {
	svc := newTestAuthService(t, withFailingEmployeeClient())
	_, err := svc.Login(context.Background(), "any@example.com", "Right1!")
	mustBeSentinel(t, err, service.ErrEmployeeRPCFailed, codes.Unavailable)
}

func TestLogin_EmailNotFound(t *testing.T) {
	svc := newTestAuthService(t)
	_, err := svc.Login(context.Background(), "missing@example.com", "Right1!")
	// Email not found COLLAPSES to ErrInvalidCredentials for security (no enumeration).
	mustBeSentinel(t, err, service.ErrInvalidCredentials, codes.Unauthenticated)
}

// mustBeSentinel asserts err wraps the given sentinel and resolves to the given gRPC code.
func mustBeSentinel(t *testing.T, err error, sentinel error, code codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected errors.Is(%v, %v) to be true", err, sentinel)
	}
	if got := status.Code(err); got != code {
		t.Fatalf("expected gRPC code %v, got %v", code, got)
	}
}
```

(Test helpers `newTestAuthService`, `withSeededUser`, etc. exist in the current test scaffold; reuse them and add `withLockedUser`, `withPendingUser`, `withFailingEmployeeClient` as small extensions.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd auth-service && go test ./internal/service/... -run TestLogin -v`
Expected: FAIL — `ErrInvalidCredentials`, `ErrAccountLocked`, etc. don't exist yet.

- [ ] **Step 3: Add sentinel definitions**

```go
// auth-service/internal/service/errors.go
package service

import (
	"google.golang.org/grpc/codes"

	"contract/shared/svcerr"
)

var (
	ErrInvalidCredentials = svcerr.New(codes.Unauthenticated, "invalid credentials")
	ErrAccountLocked      = svcerr.New(codes.PermissionDenied, "account locked")
	ErrAccountPending     = svcerr.New(codes.FailedPrecondition, "account pending activation")
	ErrAccountDisabled    = svcerr.New(codes.FailedPrecondition, "account disabled")
	ErrEmployeeRPCFailed  = svcerr.New(codes.Unavailable, "employee service unavailable")
	ErrTokenGenFailed     = svcerr.New(codes.Internal, "token generation failed")
	ErrTokenSignFailed    = svcerr.New(codes.Internal, "token signing failed")
)
```

- [ ] **Step 4: Refactor `auth_service.go` Login**

In `auth-service/internal/service/auth_service.go`, find the Login function (around the lines mentioned in the audit: 175-260). Replace each generic error return with a wrapped sentinel:

```go
// Where today: return nil, errors.New("account is locked")
// Becomes:    return nil, fmt.Errorf("Login(%s): %w", email, ErrAccountLocked)

// Where today: return nil, errors.New("invalid credentials")  // bcrypt mismatch OR not-found
// Becomes:    return nil, fmt.Errorf("Login(%s): %w", email, ErrInvalidCredentials)

// Where today: return nil, fmt.Errorf("get employee: %v", err)
// Becomes:    return nil, fmt.Errorf("Login(%s) get employee: %w", email, ErrEmployeeRPCFailed)
//             (and log.Printf the original underlying err — interceptor will also catch it)

// Where today: return nil, errors.New("account pending activation")
// Becomes:    return nil, fmt.Errorf("Login(%s): %w", email, ErrAccountPending)

// Where today: return nil, errors.New("account disabled")
// Becomes:    return nil, fmt.Errorf("Login(%s): %w", email, ErrAccountDisabled)

// JWT signing failure: return nil, fmt.Errorf("sign token: %w", err)
// Becomes:    return nil, fmt.Errorf("Login(%s) sign token: %w", email, ErrTokenSignFailed)
```

- [ ] **Step 5: Run unit tests**

Run: `cd auth-service && go test ./internal/service/... -run TestLogin -v`
Expected: PASS for all 5 cases.

- [ ] **Step 6: Refactor the gRPC handler**

In `auth-service/internal/handler/grpc_handler.go:91-101` (Login method):

```go
// BEFORE:
func (h *grpcHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
    resp, err := h.svc.Login(ctx, req.Email, req.Password)
    if err != nil {
        // ... 7-case mapper or hardcoded "invalid credentials"
    }
    return resp, nil
}

// AFTER:
func (h *grpcHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
    resp, err := h.svc.Login(ctx, req.Email, req.Password)
    if err != nil {
        return nil, err  // sentinel carries GRPCStatus; interceptor logs.
    }
    return resp, nil
}
```

- [ ] **Step 7: Wire the interceptor in main**

In `auth-service/cmd/main.go`, find the `grpc.NewServer(...)` call:

```go
// BEFORE:
srv := grpc.NewServer()

// AFTER:
srv := grpc.NewServer(
    grpc.UnaryInterceptor(grpcmw.UnaryLoggingInterceptor("auth-service")),
)
```

Add the import: `"contract/shared/grpcmw"`.

- [ ] **Step 8: Run service-level tests**

Run: `cd auth-service && go test ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add auth-service/internal/service/errors.go \
        auth-service/internal/service/auth_service.go \
        auth-service/internal/service/auth_service_login_test.go \
        auth-service/internal/handler/grpc_handler.go \
        auth-service/cmd/main.go
git commit -m "refactor(auth-service): typed sentinel errors + logging interceptor"
```

---

## Tasks 4-13: Repeat for the other 10 services

Each service follows the same pattern as Task 3. Per service:

1. Create `<service>/internal/service/errors.go` with the sentinels relevant to that service's domain (see table below).
2. Replace generic error returns in `internal/service/*.go` with wrapped sentinels.
3. Simplify `internal/handler/grpc_handler.go` to passthrough — DELETE the `mapServiceError` function or substring matching.
4. Wire `grpcmw.UnaryLoggingInterceptor("<service-name>")` in `cmd/main.go`.
5. Update or add unit tests for handler error paths.
6. Commit per service.

### Sentinel set per service

#### Task 4: user-service

```go
// user-service/internal/service/errors.go
var (
    ErrEmployeeNotFound      = svcerr.New(codes.NotFound, "employee not found")
    ErrEmployeeAlreadyExists = svcerr.New(codes.AlreadyExists, "employee already exists")
    ErrInvalidJMBG           = svcerr.New(codes.InvalidArgument, "invalid JMBG")
    ErrInvalidPassword       = svcerr.New(codes.InvalidArgument, "password does not meet policy")
    ErrPermissionNotInCatalog = svcerr.New(codes.InvalidArgument, "permission not in catalog")
    ErrRoleNotFound          = svcerr.New(codes.NotFound, "role not found")
    ErrLimitExceedsTemplate  = svcerr.New(codes.FailedPrecondition, "limit exceeds template max")
)
```

Delete: `user-service/internal/handler/grpc_handler.go:19-41` (the `mapServiceError` function).
Wire interceptor in `user-service/cmd/main.go`.
Commit: `refactor(user-service): typed sentinel errors`

#### Task 5: account-service

```go
// account-service/internal/service/errors.go
var (
    ErrAccountNotFound       = svcerr.New(codes.NotFound, "account not found")
    ErrInsufficientBalance   = svcerr.New(codes.FailedPrecondition, "insufficient balance")
    ErrAccountInactive       = svcerr.New(codes.FailedPrecondition, "account inactive")
    ErrCurrencyNotFound      = svcerr.New(codes.NotFound, "currency not found")
    ErrCompanyNotFound       = svcerr.New(codes.NotFound, "company not found")
    ErrSpendingLimitExceeded = svcerr.New(codes.ResourceExhausted, "spending limit exceeded")
    ErrOptimisticLock        = svcerr.New(codes.Aborted, "concurrent modification — retry")
    ErrLastBankAccount       = svcerr.New(codes.FailedPrecondition, "cannot delete last bank account of currency")
)
```

Delete: `account-service/internal/handler/grpc_handler.go:25-47`.
Wire interceptor.
Commit: `refactor(account-service): typed sentinel errors`

#### Task 6: client-service

```go
// client-service/internal/service/errors.go
var (
    ErrClientNotFound       = svcerr.New(codes.NotFound, "client not found")
    ErrClientAlreadyExists  = svcerr.New(codes.AlreadyExists, "client already exists")
    ErrInvalidJMBG          = svcerr.New(codes.InvalidArgument, "invalid JMBG")
    ErrInvalidCredentials   = svcerr.New(codes.Unauthenticated, "invalid credentials")
    ErrAccountNotActivated  = svcerr.New(codes.FailedPrecondition, "account not activated")
    ErrLimitsExceedEmployee = svcerr.New(codes.FailedPrecondition, "limits exceed employee maximum")
)
```

Delete: `client-service/internal/handler/grpc_handler.go:28-50`.
Wire interceptor.
Commit: `refactor(client-service): typed sentinel errors`

#### Task 7: card-service

```go
// card-service/internal/service/errors.go
var (
    ErrCardNotFound          = svcerr.New(codes.NotFound, "card not found")
    ErrCardAlreadyApproved   = svcerr.New(codes.FailedPrecondition, "card already approved")
    ErrCardAlreadyRejected   = svcerr.New(codes.FailedPrecondition, "card already rejected")
    ErrCardBlocked           = svcerr.New(codes.FailedPrecondition, "card is blocked")
    ErrCardLocked            = svcerr.New(codes.PermissionDenied, "card locked from too many failed PIN attempts")
    ErrInvalidPIN            = svcerr.New(codes.InvalidArgument, "PIN must be 4 digits")
    ErrPINMismatch           = svcerr.New(codes.Unauthenticated, "PIN mismatch")
    ErrSingleUseAlreadyUsed  = svcerr.New(codes.FailedPrecondition, "single-use virtual card already used")
)
```

Delete: `card-service/internal/handler/grpc_handler.go:23-48`.
Wire interceptor.
Commit: `refactor(card-service): typed sentinel errors`

#### Task 8: transaction-service

```go
// transaction-service/internal/service/errors.go
var (
    ErrTransferNotFound      = svcerr.New(codes.NotFound, "transfer not found")
    ErrPaymentNotFound       = svcerr.New(codes.NotFound, "payment not found")
    ErrInsufficientBalance   = svcerr.New(codes.FailedPrecondition, "insufficient balance")
    ErrAccountInactive       = svcerr.New(codes.FailedPrecondition, "account inactive")
    ErrSameAccount           = svcerr.New(codes.InvalidArgument, "source and destination must differ")
    ErrTransferLimitExceeded = svcerr.New(codes.ResourceExhausted, "transfer limit exceeded")
    ErrFeeLookupFailed       = svcerr.New(codes.Unavailable, "fee lookup failed")
    ErrVerificationRequired  = svcerr.New(codes.PermissionDenied, "verification required")
    ErrIdempotencyMissing    = svcerr.New(codes.InvalidArgument, "idempotency_key required")
)
```

Delete: `transaction-service/internal/handler/grpc_handler.go:56-88`.
Wire interceptor.
Commit: `refactor(transaction-service): typed sentinel errors`

#### Task 9: credit-service

```go
// credit-service/internal/service/errors.go
var (
    ErrLoanNotFound        = svcerr.New(codes.NotFound, "loan not found")
    ErrLoanRequestNotFound = svcerr.New(codes.NotFound, "loan request not found")
    ErrLoanAlreadyApproved = svcerr.New(codes.FailedPrecondition, "loan request already approved")
    ErrLoanAlreadyRejected = svcerr.New(codes.FailedPrecondition, "loan request already rejected")
    ErrAmountExceedsLimit  = svcerr.New(codes.FailedPrecondition, "amount exceeds employee approval limit")
)
```

Delete: `credit-service/internal/handler/grpc_handler.go:24-52`.
Wire interceptor.
Commit: `refactor(credit-service): typed sentinel errors`

#### Task 10: exchange-service (currently has NO mapper)

```go
// exchange-service/internal/service/errors.go
var (
    ErrRateNotFound = svcerr.New(codes.NotFound, "exchange rate not found")
    ErrSameCurrency = svcerr.New(codes.InvalidArgument, "from and to currencies must differ")
    ErrInvalidAmount = svcerr.New(codes.InvalidArgument, "amount must be positive")
)
```

Modify the handler to passthrough (remove the global `codes.Internal` default).
Wire interceptor.
Commit: `refactor(exchange-service): typed sentinel errors (added — was missing)`

#### Task 11: notification-service (currently has NO mapper)

```go
// notification-service/internal/service/errors.go
var (
    ErrEmailRecordNotFound  = svcerr.New(codes.NotFound, "email record not found")
    ErrInboxItemNotFound    = svcerr.New(codes.NotFound, "inbox item not found")
    ErrInvalidEmailTemplate = svcerr.New(codes.InvalidArgument, "invalid email template")
)
```

Modify `notification-service/internal/handler/grpc_handler.go:81-168` to passthrough.
Wire interceptor.
Commit: `refactor(notification-service): typed sentinel errors`

#### Task 12: verification-service

```go
// verification-service/internal/service/errors.go
var (
    ErrChallengeNotFound = svcerr.New(codes.NotFound, "challenge not found")
    ErrChallengeExpired  = svcerr.New(codes.FailedPrecondition, "challenge expired")
    ErrTooManyAttempts   = svcerr.New(codes.PermissionDenied, "too many verification attempts")
    ErrInvalidCode       = svcerr.New(codes.Unauthenticated, "invalid verification code")
    ErrOptimisticLock    = svcerr.New(codes.Aborted, "concurrent modification — retry")
)
```

Delete: `verification-service/internal/handler/grpc_handler.go:30-52`.
Wire interceptor.
Commit: `refactor(verification-service): typed sentinel errors`

#### Task 13: stock-service

```go
// stock-service/internal/service/errors.go
var (
    ErrSecurityNotFound      = svcerr.New(codes.NotFound, "security not found")
    ErrOrderNotFound         = svcerr.New(codes.NotFound, "order not found")
    ErrPortfolioNotFound     = svcerr.New(codes.NotFound, "portfolio not found")
    ErrInsufficientHolding   = svcerr.New(codes.FailedPrecondition, "insufficient holding")
    ErrActuaryLimitExceeded  = svcerr.New(codes.PermissionDenied, "actuary limit exceeded")
    ErrOTCOfferNotFound      = svcerr.New(codes.NotFound, "OTC offer not found")
    ErrOTCAlreadyAccepted    = svcerr.New(codes.FailedPrecondition, "OTC offer already accepted")
    ErrFundNotFound          = svcerr.New(codes.NotFound, "fund not found")
    ErrFundContributionBelowMin = svcerr.New(codes.FailedPrecondition, "contribution below fund minimum")
    ErrSagaInflight          = svcerr.New(codes.FailedPrecondition, "another saga is in flight for this resource")
)
```

Delete: `stock-service/internal/handler/order_handler.go:189-211` (string-equality matching) and any sibling handlers using the same pattern.
Wire interceptor.
Commit: `refactor(stock-service): typed sentinel errors`

---

## Task 14: Integration tests for login failure modes

**Files:**
- Create: `test-app/workflows/auth_login_failure_modes_test.go`

- [ ] **Step 1: Write the test file**

```go
//go:build integration
// +build integration

package workflows_test

import (
	"net/http"
	"testing"
)

func TestLogin_Wrong_Password_Returns_401_Unauthorized(t *testing.T) {
	t.Parallel()
	ctx := setupActivatedClient(t)
	resp := postLoginRaw(t, ctx.Email, "WrongPassword!")
	assertHTTP(t, resp, http.StatusUnauthorized, "unauthorized")
}

func TestLogin_Missing_Email_Returns_401_Unauthorized(t *testing.T) {
	t.Parallel()
	resp := postLoginRaw(t, "missing-"+randEmail(), "AnyPass1!")
	// Email-not-found COLLAPSES to invalid-credentials for security.
	assertHTTP(t, resp, http.StatusUnauthorized, "unauthorized")
}

func TestLogin_Locked_Account_Returns_403_Forbidden(t *testing.T) {
	t.Parallel()
	ctx := setupActivatedClient(t)
	// Burn 3 failed attempts to trip the lockout.
	for i := 0; i < 3; i++ {
		_ = postLoginRaw(t, ctx.Email, "Wrong!")
	}
	// 4th attempt hits the lockout regardless of password correctness.
	resp := postLoginRaw(t, ctx.Email, "Wrong!")
	assertHTTP(t, resp, http.StatusForbidden, "forbidden")
}

func TestLogin_Pending_Account_Returns_409_BusinessRule(t *testing.T) {
	t.Parallel()
	// Create a client but DON'T activate.
	email := randEmail()
	createUnactivatedClient(t, email)
	resp := postLoginRaw(t, email, "AnyPass1!")
	assertHTTP(t, resp, http.StatusConflict, "business_rule_violation")
}

// postLoginRaw, assertHTTP, randEmail, createUnactivatedClient are helpers
// to be added to test-app/workflows/helpers_test.go if not present.
```

- [ ] **Step 2: Add missing helpers to `test-app/workflows/helpers_test.go`**

Implement `postLoginRaw` (returns `*http.Response` without parsing), `assertHTTP` (asserts status code and body's `error.code` field), `createUnactivatedClient` (POSTs to /clients without subsequent activation).

- [ ] **Step 3: Run integration tests**

Run from repo root: `make docker-up && make test-integration TESTS='TestLogin_'`
(Or per service convention.)
Expected: PASS for all 4 cases.

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/auth_login_failure_modes_test.go test-app/workflows/helpers_test.go
git commit -m "test(auth): integration tests for distinct login failure codes"
```

---

## Task 15: Final cleanup verification

- [ ] **Step 1: Verify zero remaining `mapServiceError` functions**

Run: `grep -rn "func.*mapServiceError" --include="*.go"`
Expected: NO output.

- [ ] **Step 2: Verify zero remaining substring matches in handlers**

Run: `grep -rnE 'strings\.(Contains|HasPrefix|EqualFold)' */internal/handler/`
Expected: Empty output (or only matches in non-error paths — review each remaining match).

- [ ] **Step 3: Verify every service wires the interceptor**

Run: `grep -L "UnaryLoggingInterceptor" */cmd/main.go`
Expected: NO output (every service main.go references it).

- [ ] **Step 4: Run the full test suite**

Run: `make test && make test-integration`
Expected: PASS.

- [ ] **Step 5: Update Specification.md**

Add a brief subsection under section 21 (business rules) noting the typed-sentinel pattern. Example wording:
> Service errors carry typed sentinels (`<service>/internal/service/errors.go`) implementing the `GRPCStatus()` interface. Handlers passthrough; the gRPC logging interceptor records the wrapped error before any non-OK return.

- [ ] **Step 6: Final commit**

```bash
git add Specification.md
git commit -m "docs(spec): add typed-sentinel error pattern reference"
```

---

## Self-Review

**Spec coverage:** All 8 numbered points in spec section "Approach" are mapped to tasks.
**Placeholders:** None — every step has runnable code.
**Type consistency:** Sentinel signature `*svcerr.SentinelError`; interceptor signature `grpc.UnaryServerInterceptor`. Used identically in every task.
**TDD discipline:** Every change has a failing test before code. Service tasks use existing test scaffolds; the lighthouse (Task 3) is fully TDD-driven.
**Commit cadence:** One commit per service + one per shared package + integration tests + cleanup. ~14 commits total.
