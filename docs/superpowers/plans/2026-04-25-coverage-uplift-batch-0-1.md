# Test Coverage Uplift — Batch 0 + Batch 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the one failing test that blocks clean coverage measurement, then bring the three lowest-coverage microservices (notification, verification, auth) to ≥50% statement coverage on testable layers using only mocked dependencies.

**Architecture:** Batch 0 fixes one stale test by adding the previously-missing `listing_id` field. Batches 1.A-1.C add hand-written mock-based unit tests for `consumer/`, `sender/`, `push/`, and `service/` packages. No real database, Kafka, Redis, SMTP, or gRPC is touched — every external dependency is replaced by a mock struct defined in the same `_test.go` file or a `mocks_test.go` sibling, following the existing pattern in `client-service` and `transaction-service`.

**Tech Stack:** Go 1.22+, `testing` stdlib, `testify/require` and `testify/assert` (already in use), `httptest` for HTTP-based providers, hand-written interface mocks (no gomock), `miniredis` only if a Redis-backed unit emerges (none expected in Batch 1).

**Reference design:** `docs/superpowers/specs/2026-04-25-coverage-uplift-design.md`

---

## Conventions Used Throughout This Plan

**Test file location:** Each `<file>.go` gets a sibling `<file>_test.go` in the same package, unless an existing test file already covers the same unit. New mock structs go in either the same `_test.go` file (small, single-use) or `mocks_test.go` (shared by multiple test files in the same package).

**Mock pattern:** Define a small interface that captures only the methods the consumer uses. Implement a hand-written struct with public fields that record arguments and return canned values. Example:

```go
type stubAccountRepo struct {
    accountByID map[uint64]*model.Account
    saveErr     error
    saved       []*model.Account
}

func (s *stubAccountRepo) FindByID(id uint64) (*model.Account, error) {
    a, ok := s.accountByID[id]
    if !ok {
        return nil, gorm.ErrRecordNotFound
    }
    return a, nil
}

func (s *stubAccountRepo) Save(a *model.Account) error {
    s.saved = append(s.saved, a)
    return s.saveErr
}
```

**Per-package coverage check command** (after every batch of new tests in a package):

```bash
cd <service> && go test -covermode=set -coverprofile=/tmp/cov.out ./internal/<package>/... && go tool cover -func=/tmp/cov.out | tail -1
```

**Per-service coverage check** (used to verify ≥50% exit criterion):

```bash
cd <service> && go test -covermode=set -coverprofile=/tmp/cov.out ./... && \
  awk 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
       END {printf "Testable coverage: %.2f%% (%d/%d)\n", 100*cv/t, cv, t}' /tmp/cov.out
```

**Lint:** After tests pass for a service, run:

```bash
cd <service> && golangci-lint run ./...
```

**Commit format:**

```
test(<service>): add <package> tests for coverage uplift

Brings <service>/<package> from X% to Y% statement coverage. Uses
hand-written mocks for <dependencies>, no live infrastructure.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
```

---

## Batch 0 — Fix the failing api-gateway test

### Task 0.1: Repair `TestCreateOrder_Forex_SellRejected`

**Files:**
- Modify: `api-gateway/internal/handler/stock_order_handler_test.go:102-117`

**Context:** The handler in `stock_order_handler.go:89` validates `listing_id is required` *before* the forex direction check at line 135. The test sends a forex `sell` request without a `listing_id`, so it gets the wrong error message. The test's intent is to confirm that forex `sell` is rejected, not to test field-presence validation. Adding `listing_id` to the request body restores the intended assertion path.

- [ ] **Step 1: Apply the test fix**

In `api-gateway/internal/handler/stock_order_handler_test.go`, replace the body string at line 109:

Old:
```go
body := `{"security_type":"forex","direction":"sell","order_type":"market","quantity":1,"account_id":42,"holding_id":7}`
```

New:
```go
body := `{"security_type":"forex","direction":"sell","order_type":"market","quantity":1,"listing_id":5,"account_id":42,"holding_id":7}`
```

- [ ] **Step 2: Verify the test now passes**

Run:
```bash
cd api-gateway && go test ./internal/handler/ -run TestCreateOrder_Forex_SellRejected -v
```

Expected: `--- PASS: TestCreateOrder_Forex_SellRejected`

- [ ] **Step 3: Verify the rest of the handler tests still pass**

Run:
```bash
cd api-gateway && go test ./internal/handler/...
```

Expected: `ok` for the package, no failures.

- [ ] **Step 4: Lint**

Run:
```bash
cd api-gateway && golangci-lint run ./internal/handler/...
```

Expected: zero new warnings.

- [ ] **Step 5: Commit**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
git add api-gateway/internal/handler/stock_order_handler_test.go
git commit -m "$(cat <<'EOF'
test(api-gateway): include listing_id in forex sell rejection test

The test asserts forex sell is rejected, but field-presence validation
(listing_id required) fires first and masks the intended error. Add
listing_id to the request body so the direction check is reached.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Batch 1.A — notification-service to ≥50%

**Current coverage:** 19.69% total. `service/` has only `inbox_cleanup.go` and `metrics.go`; the real logic is in `consumer/` (consumes Kafka, persists to inbox, dispatches push), `sender/` (SMTP composition), and `push/` (provider stubs).

**Coverage targets per package:**
- `consumer/` — bring from 0% to ≥70% (3 files, 328 LOC)
- `sender/email_sender.go` — bring sender package overall to ≥85% (currently 92.8% from `templates.go`; just need the 34-LOC sender file covered)
- `service/inbox_cleanup.go` — bring service package from 0% to ≥80%
- `push/noop_provider.go` — bring to 100% (it's 23 LOC, trivial)

### Task 1.A.1: Test `push/noop_provider.go`

**Files:**
- Create: `notification-service/internal/push/noop_provider_test.go`

- [ ] **Step 1: Read the source**

Run:
```bash
cat notification-service/internal/push/provider.go notification-service/internal/push/noop_provider.go
```

- [ ] **Step 2: Write the test file**

Create `notification-service/internal/push/noop_provider_test.go`:

```go
package push

import (
    "context"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestNoopProvider_Send_ReturnsNil(t *testing.T) {
    p := NewNoopProvider()
    err := p.Send(context.Background(), &Notification{
        UserID: 1,
        Title:  "test",
        Body:   "hello",
    })
    require.NoError(t, err)
}

func TestNoopProvider_Name(t *testing.T) {
    p := NewNoopProvider()
    require.Equal(t, "noop", p.Name())
}
```

(If the actual API differs — e.g. constructor name, struct fields — adjust to match. Run `cat noop_provider.go` first to confirm the exported names.)

- [ ] **Step 3: Run tests**

```bash
cd notification-service && go test ./internal/push/... -v
```

Expected: PASS for both tests.

- [ ] **Step 4: Verify coverage**

```bash
cd notification-service && go test -cover ./internal/push/...
```

Expected: ≥90%.

### Task 1.A.2: Test `sender/email_sender.go`

**Files:**
- Create: `notification-service/internal/sender/email_sender_test.go`

- [ ] **Step 1: Read the source**

```bash
cat notification-service/internal/sender/email_sender.go
```

Identify the exported `Sender` interface or `EmailSender` struct, the `Send` method's signature, and any SMTP transport interface. The 34-LOC file likely wraps `net/smtp` or `gomail`; the unit test should mock the transport.

- [ ] **Step 2: Define a transport interface if not present**

If `email_sender.go` already takes a transport interface, skip. If it calls `smtp.SendMail` directly, refactor to introduce a small interface:

```go
type SMTPTransport interface {
    SendMail(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}
```

Inject it into `EmailSender`. Default to a function adapter calling `smtp.SendMail` for production use.

- [ ] **Step 3: Write the test**

Create `notification-service/internal/sender/email_sender_test.go`:

```go
package sender

import (
    "errors"
    "testing"

    "github.com/stretchr/testify/require"
)

type stubTransport struct {
    called   bool
    lastTo   []string
    lastBody []byte
    err      error
}

func (s *stubTransport) SendMail(addr string, auth interface{}, from string, to []string, msg []byte) error {
    s.called = true
    s.lastTo = to
    s.lastBody = msg
    return s.err
}

func TestEmailSender_Send_Success(t *testing.T) {
    tr := &stubTransport{}
    s := NewEmailSenderWithTransport(/* host */"smtp.test", /* port */"587", /* user */"u", /* pass */"p", /* from */"sender@test", tr)
    err := s.Send([]string{"recipient@test"}, "subject", "body")
    require.NoError(t, err)
    require.True(t, tr.called)
    require.Equal(t, []string{"recipient@test"}, tr.lastTo)
    require.Contains(t, string(tr.lastBody), "subject")
}

func TestEmailSender_Send_TransportError(t *testing.T) {
    tr := &stubTransport{err: errors.New("smtp boom")}
    s := NewEmailSenderWithTransport("smtp.test", "587", "u", "p", "sender@test", tr)
    err := s.Send([]string{"r@test"}, "subject", "body")
    require.Error(t, err)
}
```

(Adjust the constructor name, parameter order, and method signatures to match the actual file. The test exists to exercise success and error paths, regardless of exact API shape.)

- [ ] **Step 4: Run tests and check coverage**

```bash
cd notification-service && go test -cover ./internal/sender/...
```

Expected: ≥85% on `sender/`.

### Task 1.A.3: Test `service/inbox_cleanup.go`

**Files:**
- Create: `notification-service/internal/service/inbox_cleanup_test.go`

- [ ] **Step 1: Read the source**

```bash
cat notification-service/internal/service/inbox_cleanup.go
```

Identify the function (likely `RunInboxCleanup(ctx, repo, retentionDays)` or a struct method). It will call a repository method to delete old messages.

- [ ] **Step 2: Write a stub repository and test**

Create `notification-service/internal/service/inbox_cleanup_test.go`:

```go
package service

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

type stubInboxRepo struct {
    cutoffSeen time.Time
    deleteErr  error
    deleted    int64
}

func (s *stubInboxRepo) DeleteOlderThan(cutoff time.Time) (int64, error) {
    s.cutoffSeen = cutoff
    return s.deleted, s.deleteErr
}

func TestInboxCleanup_DeletesOlderThanRetention(t *testing.T) {
    repo := &stubInboxRepo{deleted: 7}
    n, err := RunInboxCleanup(context.Background(), repo, 30*24*time.Hour)
    require.NoError(t, err)
    require.Equal(t, int64(7), n)
    require.WithinDuration(t, time.Now().Add(-30*24*time.Hour), repo.cutoffSeen, time.Minute)
}

func TestInboxCleanup_PropagatesRepoError(t *testing.T) {
    repo := &stubInboxRepo{deleteErr: errors.New("db down")}
    _, err := RunInboxCleanup(context.Background(), repo, time.Hour)
    require.Error(t, err)
}
```

(If the function takes a different signature, e.g. a struct constructor with the repo, adjust accordingly. The two test cases — success path and repo error — are non-negotiable.)

- [ ] **Step 3: Run tests and check coverage**

```bash
cd notification-service && go test -cover ./internal/service/...
```

Expected: ≥80% on `service/`.

### Task 1.A.4: Test `consumer/email_consumer.go`

**Files:**
- Create: `notification-service/internal/consumer/email_consumer_test.go`
- Create: `notification-service/internal/consumer/mocks_test.go` (shared mocks for all three consumer tests)

- [ ] **Step 1: Read the consumer source files**

```bash
cat notification-service/internal/consumer/email_consumer.go
cat notification-service/internal/consumer/general_notification_consumer.go
cat notification-service/internal/consumer/verification_consumer.go
```

Identify the dependencies each consumer takes (likely a `Sender` interface, a `Repository` interface, a Kafka reader, and possibly a producer for delivery confirmations).

- [ ] **Step 2: Write the shared mocks file**

Create `notification-service/internal/consumer/mocks_test.go`:

```go
package consumer

import (
    "context"
    "sync"

    "github.com/exbanka/contract/kafka"
)

type stubSender struct {
    mu        sync.Mutex
    sent      []sentEmail
    sendErr   error
}

type sentEmail struct {
    To      []string
    Subject string
    Body    string
}

func (s *stubSender) Send(to []string, subject, body string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.sendErr != nil {
        return s.sendErr
    }
    s.sent = append(s.sent, sentEmail{To: to, Subject: subject, Body: body})
    return nil
}

type stubInboxRepo struct {
    mu     sync.Mutex
    saved  []interface{}
    saveErr error
}

func (s *stubInboxRepo) Save(msg interface{}) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.saveErr != nil {
        return s.saveErr
    }
    s.saved = append(s.saved, msg)
    return nil
}

type stubProducer struct {
    mu        sync.Mutex
    published []kafka.Message
    err       error
}

func (s *stubProducer) Publish(ctx context.Context, topic string, key, value []byte) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.err != nil {
        return s.err
    }
    s.published = append(s.published, kafka.Message{Topic: topic, Key: key, Value: value})
    return nil
}
```

(Match interface signatures to what each consumer actually expects. Read the consumer constructors to derive them.)

- [ ] **Step 3: Write `email_consumer_test.go`**

Tests to include (read the consumer to get exact API):
1. `TestEmailConsumer_HandleMessage_SendsAndConfirms` — happy path: incoming email message gets sent via SMTP and a delivery confirmation is published.
2. `TestEmailConsumer_HandleMessage_SenderError_NoConfirmation` — sender returns error; consumer logs/returns error and does NOT publish confirmation.
3. `TestEmailConsumer_HandleMessage_MalformedJSON_Skipped` — bad payload produces an error (or skip), not a panic.
4. `TestEmailConsumer_HandleMessage_PersistsToInbox` — successful sends store in inbox repo.

Each test instantiates the consumer with stub sender/repo/producer, calls the message-handling method directly (not through Kafka), and asserts on stub state.

- [ ] **Step 4: Run tests and check coverage**

```bash
cd notification-service && go test -cover ./internal/consumer/...
```

Expected: ≥60% on `consumer/` after this task; will rise to ≥70% after the next two consumer tasks.

### Task 1.A.5: Test `consumer/general_notification_consumer.go`

**Files:**
- Create: `notification-service/internal/consumer/general_notification_consumer_test.go`

- [ ] **Step 1: Write tests**

Tests to include (one per code path in the file):
1. Happy path: notification message is persisted and (if applicable) push-dispatched.
2. Push provider error path.
3. Malformed payload path.

Reuse the stubs from `mocks_test.go`. Add a `stubPushProvider` if needed:

```go
type stubPushProvider struct {
    mu      sync.Mutex
    sent    []push.Notification
    err     error
}

func (s *stubPushProvider) Send(ctx context.Context, n *push.Notification) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.err != nil {
        return s.err
    }
    s.sent = append(s.sent, *n)
    return nil
}

func (s *stubPushProvider) Name() string { return "stub" }
```

- [ ] **Step 2: Run tests**

```bash
cd notification-service && go test -cover ./internal/consumer/...
```

### Task 1.A.6: Test `consumer/verification_consumer.go`

**Files:**
- Create: `notification-service/internal/consumer/verification_consumer_test.go`

Same pattern: enumerate code paths in the source, write one test per path using the shared mocks.

- [ ] **Step 1: Write tests**
- [ ] **Step 2: Run tests**

### Task 1.A.7: Verify notification-service hits ≥50%, lint, commit

- [ ] **Step 1: Run aggregate coverage**

```bash
cd notification-service && go test -covermode=set -coverprofile=/tmp/cov.out ./... && \
  awk 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
       END {printf "Testable coverage: %.2f%% (%d/%d)\n", 100*cv/t, cv, t}' /tmp/cov.out
```

Expected: ≥50%. If below, identify largest uncovered file with `go tool cover -func=/tmp/cov.out | sort -k3 -n` and add 1-2 more tests.

- [ ] **Step 2: Lint**

```bash
cd notification-service && golangci-lint run ./...
```

- [ ] **Step 3: Commit**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
git add notification-service/
git commit -m "$(cat <<'EOF'
test(notification-service): add unit tests for consumers, sender, push, cleanup

Brings notification-service to >=50% testable coverage. Hand-written
mocks for SMTP transport, inbox repo, Kafka producer, and push
provider — no live infrastructure required.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Batch 1.B — verification-service to ≥50%

**Current coverage:** 11.69% total, 14.7% in service/. The package has one main file (`verification_service.go`, 538 LOC) plus an existing partial test (`verification_service_test.go`).

### Task 1.B.1: Audit existing tests and source

- [ ] **Step 1: Read the existing test file**

```bash
cat verification-service/internal/service/verification_service_test.go
```

- [ ] **Step 2: List uncovered functions**

```bash
cd verification-service && go test -covermode=set -coverprofile=/tmp/cov.out ./internal/service/... && \
  go tool cover -func=/tmp/cov.out | grep -v "100.0%" | grep "verification_service.go"
```

Capture the list of functions below 100% — those are the targets.

### Task 1.B.2: Add tests for the largest uncovered functions

**Files:**
- Modify: `verification-service/internal/service/verification_service_test.go` (add new test funcs alongside existing ones)
- Possibly create: `verification-service/internal/service/mocks_test.go` if no mocks file exists

The verification_service.go file (538 LOC) likely contains:
- `CreateChallenge` — generates a challenge with code/QR/number-match data
- `Verify` — checks the user-supplied response against stored challenge, increments attempts
- `MarkConsumed` / `Expire` — state transitions
- Helpers for code generation, attempt tracking, expiry checks

For each uncovered function, write at minimum:
- Happy path test
- Boundary/error path test (e.g., expired challenge, max attempts exceeded, wrong code)

- [ ] **Step 1: For each uncovered function, write 1-3 tests using the established stub pattern**

The existing test file already shows the pattern — extend it.

- [ ] **Step 2: Run tests and re-check coverage**

```bash
cd verification-service && go test -cover ./internal/service/...
```

Iterate until service/ is ≥80%.

### Task 1.B.3: Add handler tests for `verification-service/internal/handler/`

**Files:**
- Read: `verification-service/internal/handler/*.go`
- Create: `verification-service/internal/handler/*_test.go` (one per handler file, or one combined `handler_test.go`)

- [ ] **Step 1: Read handler source**

```bash
ls verification-service/internal/handler/
cat verification-service/internal/handler/*.go
```

- [ ] **Step 2: Write gRPC handler tests**

Mock the verification service interface that the handler depends on; assert that:
- gRPC requests with valid input call the service correctly and return the expected response
- gRPC requests with invalid input return `codes.InvalidArgument`
- Service errors are mapped to the correct gRPC status codes

- [ ] **Step 3: Run tests**

```bash
cd verification-service && go test -cover ./internal/handler/...
```

### Task 1.B.4: Verify verification-service hits ≥50%, lint, commit

- [ ] **Step 1: Aggregate coverage**

```bash
cd verification-service && go test -covermode=set -coverprofile=/tmp/cov.out ./... && \
  awk 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
       END {printf "Testable coverage: %.2f%% (%d/%d)\n", 100*cv/t, cv, t}' /tmp/cov.out
```

Expected: ≥50%.

- [ ] **Step 2: Lint and commit**

```bash
cd verification-service && golangci-lint run ./...
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
git add verification-service/
git commit -m "$(cat <<'EOF'
test(verification-service): expand service + handler unit tests

Brings verification-service to >=50% testable coverage with new
service-layer tests for uncovered functions and a fresh handler
test suite using mocked service dependencies.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Batch 1.C — auth-service to ≥50%

**Current coverage:** 14.58% total, 21.5% in service/. The biggest file is `auth_service.go` (1006 LOC); existing tests cover JWT, mobile devices, TOTP, pepper, and account status partially.

### Task 1.C.1: Audit existing tests and uncovered functions

- [ ] **Step 1: Identify uncovered functions in service/**

```bash
cd auth-service && go test -covermode=set -coverprofile=/tmp/cov.out ./internal/service/... && \
  go tool cover -func=/tmp/cov.out | grep -v "100.0%" | sort -k3 -n
```

The output reveals the lowest-coverage functions in `auth_service.go` and other service files. These are the targets.

### Task 1.C.2: Test uncovered AuthService methods

**Files:**
- Modify: `auth-service/internal/service/auth_service_test.go` (or create `auth_service_extra_test.go` for new tests if the file is already large)

Likely uncovered methods (based on AuthService size):
- `Login` (employee + client variants), `RefreshToken`, `Logout`, `RevokeAllSessions`
- `RequestPasswordReset`, `CompletePasswordReset`
- `ChangePassword`, `ActivateAccount`
- TOTP enrollment + verification flows
- Mobile activation/refresh
- Login attempt tracking + lockout

For each uncovered method, write:
- Happy path
- Permission/role-denied path
- Locked-out path
- Token-expired path
- Invalid-input path

Reuse the existing mock structs (read `auth_service_test.go` to find them; add fields to existing stubs rather than duplicating).

- [ ] **Step 1: Add tests for the 5-10 largest uncovered methods**
- [ ] **Step 2: Run tests, re-check coverage**

```bash
cd auth-service && go test -cover ./internal/service/...
```

Iterate until service/ is ≥70%.

### Task 1.C.3: Add handler tests

**Files:**
- Read: `auth-service/internal/handler/*.go`
- Create: `auth-service/internal/handler/*_test.go`

Same pattern as verification-service handler tests: mock the service interface, assert gRPC request → response mapping and error code translation.

- [ ] **Step 1: Read handler files**
- [ ] **Step 2: Write tests for each gRPC method**
- [ ] **Step 3: Run and verify coverage**

### Task 1.C.4: Add cache tests using miniredis

**Files:**
- Read: `auth-service/internal/cache/*.go`
- Create: `auth-service/internal/cache/*_test.go`

`miniredis` is an in-memory Redis fake — counts as no-DB.

- [ ] **Step 1: Add miniredis dependency if not present**

```bash
cd auth-service && go get github.com/alicebob/miniredis/v2
```

- [ ] **Step 2: Write cache tests**

```go
package cache

import (
    "context"
    "testing"
    "time"

    "github.com/alicebob/miniredis/v2"
    "github.com/redis/go-redis/v9"
    "github.com/stretchr/testify/require"
)

func newTestCache(t *testing.T) *RedisCache {
    mr, err := miniredis.Run()
    require.NoError(t, err)
    t.Cleanup(mr.Close)
    client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
    return NewRedisCache(client)
}

func TestRedisCache_SetAndGet(t *testing.T) {
    c := newTestCache(t)
    ctx := context.Background()
    require.NoError(t, c.Set(ctx, "k", "v", time.Minute))
    got, err := c.Get(ctx, "k")
    require.NoError(t, err)
    require.Equal(t, "v", got)
}

func TestRedisCache_GetMissing_ReturnsErrCacheMiss(t *testing.T) {
    c := newTestCache(t)
    _, err := c.Get(context.Background(), "missing")
    require.ErrorIs(t, err, ErrCacheMiss)
}
```

(Adjust to actual cache method names and error types — read the source first.)

- [ ] **Step 3: Run tests**

```bash
cd auth-service && go test -cover ./internal/cache/...
```

### Task 1.C.5: Verify auth-service hits ≥50%, lint, commit

- [ ] **Step 1: Aggregate coverage**

```bash
cd auth-service && go test -covermode=set -coverprofile=/tmp/cov.out ./... && \
  awk 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
       END {printf "Testable coverage: %.2f%% (%d/%d)\n", 100*cv/t, cv, t}' /tmp/cov.out
```

Expected: ≥50%.

- [ ] **Step 2: Lint**

```bash
cd auth-service && golangci-lint run ./...
```

- [ ] **Step 3: Commit**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
git add auth-service/
git commit -m "$(cat <<'EOF'
test(auth-service): expand service, handler, cache unit tests

Brings auth-service to >=50% testable coverage. New tests cover the
previously-uncovered AuthService methods (login flows, password reset,
TOTP, mobile activation), all gRPC handlers (mocked service), and
the Redis cache wrapper (using miniredis in-memory fake).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Batch 1 Exit Verification

- [ ] **Step 1: Run full project coverage**

```bash
cd /Users/lukasavic/Desktop/Faks/Softversko\ inzenjerstvo/EXBanka-1-Backend
rm -f /tmp/cov_all.out && echo "mode: set" > /tmp/cov_all.out
for svc in account-service api-gateway auth-service card-service client-service credit-service exchange-service notification-service stock-service transaction-service user-service verification-service; do
  (cd "$svc" && go test -covermode=set -coverprofile=/tmp/cov_$svc.out ./... > /dev/null 2>&1)
  if [ -f /tmp/cov_$svc.out ]; then tail -n +2 /tmp/cov_$svc.out >> /tmp/cov_all.out; fi
done
awk 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
     END {printf "Project testable coverage: %.2f%% (%d/%d)\n", 100*cv/t, cv, t}' /tmp/cov_all.out
```

- [ ] **Step 2: Per-service summary**

```bash
for svc in account-service api-gateway auth-service card-service client-service credit-service exchange-service notification-service stock-service transaction-service user-service verification-service; do
  if [ -f /tmp/cov_$svc.out ]; then
    awk -v svc=$svc 'NR>1 && $1 !~ /\/(model|cmd|config|repository)\// {n=$2; c=$3; t+=n; if (c>0) cv+=n} \
                     END {if (t>0) printf "%-25s %6.2f%%\n", svc, 100*cv/t}' /tmp/cov_$svc.out
  fi
done
```

- [ ] **Step 3: Confirm exit conditions**

- notification-service ≥ 50% ✓
- verification-service ≥ 50% ✓
- auth-service ≥ 50% ✓
- All other services unchanged from baseline (no regressions)
- All tests pass; all lint clean

If any of the three Batch 1 services is below 50%, return to its task and add tests for the next-largest uncovered file.

---

## Batches 2-5 (Outline — Detailed Plans Written Per Batch)

Each subsequent batch follows the same pattern:

- **Batch 2** (card-service, credit-service, stock-service): plan written at start of session.
- **Batch 3** (account-service, user-service, client-service, transaction-service, exchange-service handler/middleware): plan written at start of session.
- **Batch 4** (api-gateway): plan written at start of session — largest single effort, expects to test all handlers and middleware.
- **Batch 5** (top-up if project total < 80%): plan written based on coverage gaps observed.

For each batch:
1. Read each target service's source files for in-scope packages.
2. Identify uncovered functions via `go tool cover -func`.
3. Write tests using the same hand-written-mock pattern.
4. Run coverage check and iterate until the per-service ≥50% bar is met.
5. Lint, commit per service.
6. Update the running results document `docs/superpowers/specs/2026-04-25-coverage-uplift-results.md` (created in Batch 5).

---

## Out of Scope for This Plan

- Tests requiring a live Postgres, Kafka, Redis (other than miniredis fakes), SMTP, or gRPC server.
- Refactoring of production code beyond minimal interface extraction needed to enable mocking.
- Changes to existing passing tests, other than the one fix in Task 0.1.
- Lint cleanup of pre-existing issues in untouched code.
- Integration tests in `test-app/workflows/`.
