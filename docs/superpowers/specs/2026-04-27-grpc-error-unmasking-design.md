# Design: gRPC Error Unmasking

**Status:** Approved (Spec A)
**Date:** 2026-04-27
**Scope:** Clean-break refactor — no migration window, no backwards compatibility.

## Problem

The Login RPC at `auth-service/internal/handler/grpc_handler.go:91-101` collapses 7 distinct upstream failures into a single `codes.Unauthenticated, "invalid credentials"`:

- account locked (lockout window exceeded)
- employee gRPC RPC down
- account not found
- account pending or disabled
- bcrypt password mismatch
- JWT signing failure
- token generation failure

The original error is **never logged**. Debugging requires turning on verbose service logs and reproducing the failure.

The pattern repeats across services. Nine services (`auth`, `user`, `account`, `client`, `card`, `transaction`, `credit`, `verification`, `stock`) maintain nearly-identical `mapServiceError()` functions that substring-match error messages to gRPC codes — with subtle drift (e.g., `verification-service` maps to `Aborted` for optimistic-lock conflicts, others don't). Two services (`exchange`, `notification`) have no mapper at all and return `codes.Internal` for everything.

No typed sentinel errors exist anywhere in the codebase. No `var Err* = errors.New(...)` pattern. All errors are `fmt.Errorf` strings; handlers do fragile substring matching.

## Goal

Every distinct service-layer failure has a typed sentinel that carries its own gRPC status code. Handlers stop choosing codes. Original errors are always logged before being returned. No information is silently swallowed.

## Approach

**Per-service typed sentinels implementing the `GRPCStatus()` interface, plus a mandatory logging interceptor.**

`grpc-go` already honors any error implementing `interface{ GRPCStatus() *status.Status }` via `status.FromError`. Sentinels carry their own code and message; handlers do not need a central mapper.

### Sentinel shape (`contract/shared/svcerr/svcerr.go`)

```go
package svcerr

import (
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

type SentinelError struct {
    code codes.Code
    msg  string
}

func New(code codes.Code, msg string) *SentinelError {
    return &SentinelError{code: code, msg: msg}
}

func (e *SentinelError) Error() string {
    return e.msg
}

func (e *SentinelError) GRPCStatus() *status.Status {
    return status.New(e.code, e.msg)
}
```

### Per-service sentinels

Each service defines its sentinels in `internal/service/errors.go` (one file per service). Example for `auth-service`:

```go
package service

import (
    "google.golang.org/grpc/codes"
    "github.com/.../contract/shared/svcerr"
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

Service code wraps with `fmt.Errorf("Login: %w", ErrAccountLocked)` so the wrap chain preserves context for logs while `status.FromError` walks to the sentinel for the wire code.

### Mandatory logging interceptor (`contract/shared/grpcmw/logging.go`)

```go
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

Wired once per service in `cmd/main.go`:
```go
grpc.NewServer(grpc.UnaryInterceptor(grpcmw.UnaryLoggingInterceptor("auth-service")))
```

This is the single most important change. Even without typed sentinels, this alone would have caught the lockout-vs-wrong-password ambiguity.

### Handler shape after refactor

```go
func (h *grpcHandler) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
    resp, err := h.service.Login(ctx, req.Email, req.Password)
    if err != nil {
        return nil, err   // sentinel carries its own GRPCStatus; interceptor logs
    }
    return resp, nil
}
```

No `mapServiceError`. No substring matching. No magic.

### Login as the lighthouse

Spec mandates the Login handler is the first to be rewritten. The 7 failure modes each map to a distinct sentinel:

| Failure | Sentinel | gRPC Code |
|---|---|---|
| Email not found | `ErrInvalidCredentials` | `Unauthenticated` |
| Bcrypt mismatch | `ErrInvalidCredentials` | `Unauthenticated` |
| Account locked | `ErrAccountLocked` | `PermissionDenied` |
| Account pending | `ErrAccountPending` | `FailedPrecondition` |
| Account disabled | `ErrAccountDisabled` | `FailedPrecondition` |
| Employee RPC down | `ErrEmployeeRPCFailed` | `Unavailable` |
| JWT signing failed | `ErrTokenSignFailed` | `Internal` |
| Token generation | `ErrTokenGenFailed` | `Internal` |

Note: `email-not-found` and `bcrypt-mismatch` deliberately collapse to the same sentinel for **security** (prevents email enumeration). All other distinctions are exposed.

### Files DELETED

- All `mapServiceError()` functions across the 9 services that have one.
- All substring matching in `stock-service/internal/handler/order_handler.go:189-211`.

### Files ADDED

- `contract/shared/svcerr/svcerr.go` — sentinel type.
- `contract/shared/grpcmw/logging.go` — interceptor.
- One `internal/service/errors.go` per service.

### Files MODIFIED

- Every `internal/handler/grpc_handler.go` — handler bodies become passthrough.
- Every `cmd/main.go` — wire the interceptor.
- Every `internal/service/*.go` that returns `fmt.Errorf("...")` for a known failure mode — return wrapped sentinel instead.
- API gateway `validation.go:243-272` — `grpcToHTTPError()` already handles all the codes we'll be returning; only the docstring updates.

## Test Plan

### Unit tests

- `svcerr` package: `SentinelError.GRPCStatus()` returns the correct code; `status.Code(wrappedErr)` resolves through `fmt.Errorf("%w")` wrap chains.
- Logging interceptor: logs the original (wrapped) error and method name on non-OK responses; does not log on OK.
- For each service's sentinel file: every sentinel has a unique `(code, message)` pair within the service.

### Integration tests

- `test-app/workflows/auth_login_failure_modes_test.go` — six new cases, each asserting a distinct HTTP body code from the gateway:
  - Bad password → 401 `unauthorized`
  - Locked account (3 failed attempts then 4th) → 403 `forbidden`
  - Pending account → 409 `business_rule_violation`
  - Missing email → 401 `unauthorized` (same as bad password — security)
  - Employee RPC down (kill service mid-test) → 503 (or 500 — confirm gateway mapping for `Unavailable`)
  - Disabled account → 409 `business_rule_violation`
- Existing tests continue to pass.

## Out of Scope

- Stack traces in error messages. Use logs.
- Localized user-facing messages. Gateway's `apiError()` shape stays.
- gRPC `status.WithDetails(...)`. Overkill for this iteration.
- Streaming RPC interceptor (services don't use streaming today).
