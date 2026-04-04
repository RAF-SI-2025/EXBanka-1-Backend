# Seeder & Bootstrapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the seeder to create a default test client alongside the existing admin employee.

**Architecture:** Add client-service gRPC client to seeder, replicate the employee creation flow for a client (create → read Kafka for activation token → activate).

**Tech Stack:** Go, gRPC, Kafka

---

## Background

The seeder (`seeder/cmd/main.go`) currently provisions only a default admin employee. The test client is needed so integration tests and manual QA have a ready-made client account. The client creation flow mirrors the employee flow:

1. `CreateClient` via client-service gRPC publishes a `client.created` Kafka event.
2. Auth-service's `ClientConsumer` picks up the event and calls `CreateAccountAndActivationToken` (principal_type=`"client"`).
3. Auth-service publishes an `ACTIVATION` email to `notification.send-email`.
4. The seeder reads the activation token from Kafka and calls `ActivateAccount`.

The single `Login` RPC in auth-service handles both employee and client logins (switches on `PrincipalType`), so the seeder can use the same `Login` call to check if the client is already active.

### Client email derivation

The client email is derived from `ADMIN_EMAIL` by inserting `+testclient` before the `@` sign:
- `admin@admin.com` → `admin+testclient@admin.com`

This keeps the seeder single-config (no separate `CLIENT_EMAIL` env var) while producing a deterministic, distinct email.

---

## Task 1: Add `CLIENT_GRPC_ADDR` to seeder config and docker-compose

**Files:**
- `seeder/cmd/main.go` (modify)
- `docker-compose.yml` (modify)

### Steps

- [ ] **1.1** In `seeder/cmd/main.go`, add the new config variable alongside the existing ones. Insert after line 61 (`password := getenv(...)`):

```go
clientAddr := getenv("CLIENT_GRPC_ADDR", "client-service:50054")
```

- [ ] **1.2** In `docker-compose.yml`, add `CLIENT_GRPC_ADDR` to the seeder environment block and `client-service` to `depends_on`. The seeder section (lines 588–607) becomes:

```yaml
  seeder:
    build:
      context: .
      dockerfile: seeder/Dockerfile
    environment:
      USER_GRPC_ADDR: "user-service:50052"
      AUTH_GRPC_ADDR: "auth-service:50051"
      CLIENT_GRPC_ADDR: "client-service:50054"
      KAFKA_BROKERS: "kafka:9092"
      ADMIN_EMAIL: "${ADMIN_EMAIL:-admin@admin.com}"
      ADMIN_PASSWORD: "${ADMIN_PASSWORD:-AdminAdmin2026!.}"
    depends_on:
      kafka:
        condition: service_healthy
      user-service:
        condition: service_started
      auth-service:
        condition: service_started
      notification-service:
        condition: service_started
      client-service:
        condition: service_started
    restart: on-failure
```

- [ ] **1.3** In `seeder/cmd/main.go`, update the top-of-file doc comment (lines 10-15) to list the new env var:

```go
//	CLIENT_GRPC_ADDR — client-service gRPC address (default: client-service:50054)
```

---

## Task 2: Add client-service gRPC client to seeder

**Files:**
- `seeder/cmd/main.go` (modify)

### Steps

- [ ] **2.1** Add the `clientpb` import. In the import block, add:

```go
clientpb "github.com/exbanka/contract/clientpb"
```

The full import block becomes:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)
```

- [ ] **2.2** In `main()`, after `authConn` dial (line 70–71), add the client-service connection:

```go
clientConn := dial(clientAddr)
defer clientConn.Close()
```

- [ ] **2.3** After the `authClient` creation (line 74), add the client-service gRPC client:

```go
clientSvcClient := clientpb.NewClientServiceClient(clientConn)
```

---

## Task 3: Add `deriveClientEmail` helper function

**Files:**
- `seeder/cmd/main.go` (modify)

### Steps

- [ ] **3.1** Add the helper function after the `findEmployeeByEmail` function (after line 228):

```go
// deriveClientEmail inserts "+testclient" before the "@" in the admin email.
// e.g. "admin@admin.com" → "admin+testclient@admin.com"
func deriveClientEmail(adminEmail string) string {
	parts := strings.SplitN(adminEmail, "@", 2)
	if len(parts) != 2 {
		return "testclient@admin.com"
	}
	return parts[0] + "+testclient@" + parts[1]
}
```

Note: this requires adding `"strings"` to the import block (already included in Task 2.1).

---

## Task 4: Add `seedClient` function

**Files:**
- `seeder/cmd/main.go` (modify)

### Steps

- [ ] **4.1** Add the `seedClient` function after the `deriveClientEmail` function:

```go
// seedClient creates the default test client, waits for its activation token,
// and activates the account. Skips silently if the client is already active.
func seedClient(
	ctx context.Context,
	authClient authpb.AuthServiceClient,
	clientSvcClient clientpb.ClientServiceClient,
	kafkaBrokers string,
	clientEmail string,
	password string,
) {
	log.Printf("seeder: client email=%s", clientEmail)

	// ── 1. Try Login — already bootstrapped? ──────────────────────────────────
	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    clientEmail,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: test client already active — skipping")
		return
	}

	// ── 2. Check if client already exists by email ────────────────────────────
	getCtx, getCancel := context.WithTimeout(ctx, 10*time.Second)
	defer getCancel()
	existing, getErr := clientSvcClient.GetClientByEmail(getCtx, &clientpb.GetClientByEmailRequest{
		Email: clientEmail,
	})

	clientExists := getErr == nil && existing != nil && existing.Id > 0

	// ── 3. Create client if not found ─────────────────────────────────────────
	if !clientExists {
		createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
		defer createCancel()
		_, createErr := clientSvcClient.CreateClient(createCtx, &clientpb.CreateClientRequest{
			FirstName:   "Test",
			LastName:    "Client",
			DateOfBirth: time.Date(1995, 6, 15, 0, 0, 0, 0, time.UTC).Unix(),
			Gender:      "other",
			Email:       clientEmail,
			Phone:       "+381000000001",
			Address:     "Test Client Address",
			Jmbg:        "1506995000001",
		})
		if createErr != nil {
			st, _ := status.FromError(createErr)
			if st.Code() != codes.AlreadyExists {
				log.Fatalf("seeder: CreateClient failed: %v", createErr)
			}
			log.Println("seeder: client already exists (AlreadyExists)")
		} else {
			log.Println("seeder: client created")
		}
	} else {
		log.Println("seeder: client already exists, continuing to activation")
	}

	// ── 4. Wait for activation token on Kafka ─────────────────────────────────
	log.Println("seeder: waiting for client activation token on Kafka…")
	token := readActivationToken(kafkaBrokers, clientEmail)
	log.Printf("seeder: got client activation token (len=%d)", len(token))

	// ── 5. Activate account ───────────────────────────────────────────────────
	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	defer actCancel()
	_, err := authClient.ActivateAccount(actCtx, &authpb.ActivateAccountRequest{
		Token:           token,
		Password:        password,
		ConfirmPassword: password,
	})
	if err != nil {
		log.Fatalf("seeder: client ActivateAccount failed: %v", err)
	}

	log.Printf("seeder: test client account active — email=%s", clientEmail)
}
```

---

## Task 5: Call `seedClient` from `main()`

**Files:**
- `seeder/cmd/main.go` (modify)

### Steps

- [ ] **5.1** At the end of `main()`, after the existing admin activation log line (line 153), add the client seeding call:

```go
	// ── Client bootstrapping ──────────────────────────────────────────────────
	clientEmail := deriveClientEmail(email)
	seedClient(ctx, authClient, clientSvcClient, kafka, clientEmail, password)

	log.Println("seeder: all bootstrapping complete")
```

---

## Task 6: Verify build

**Files:** (none modified — verification only)

### Steps

- [ ] **6.1** Run `go mod tidy` for the seeder module:

```bash
cd seeder && go mod tidy
```

The `clientpb` package is already in the `contract` module (replace directive exists in `seeder/go.mod`), so no new dependencies should be added. The command should succeed silently.

- [ ] **6.2** Build the seeder:

```bash
cd seeder && go build -o bin/seeder ./cmd
```

Expected: clean build, no errors. The `bin/seeder` binary is produced.

- [ ] **6.3** Run the full project build to confirm nothing is broken:

```bash
make build
```

Expected: all services and seeder build successfully.

---

## Task 7: Commit

### Steps

- [ ] **7.1** Stage and commit the changes:

```bash
git add seeder/cmd/main.go docker-compose.yml
git commit -m "feat(seeder): add default test client bootstrapping

Extend the seeder to create a test client (admin+testclient@admin.com)
alongside the existing admin employee. Flow: CreateClient via
client-service gRPC → Kafka activation token → ActivateAccount.

Adds CLIENT_GRPC_ADDR to seeder config and docker-compose."
```

---

## Summary of changes

| File | Change |
|---|---|
| `seeder/cmd/main.go` | Add `clientpb` import, `CLIENT_GRPC_ADDR` config, client-service gRPC dial + client, `deriveClientEmail` helper, `seedClient` function, call from `main()` |
| `docker-compose.yml` | Add `CLIENT_GRPC_ADDR` env var and `client-service` dependency to seeder service |

## Final state of `seeder/cmd/main.go`

For reference, here is the complete expected file after all tasks are applied:

```go
// Seeder provisions a default admin employee and test client on first startup.
//
// Flow (admin employee):
//  1. Try Login — if it succeeds, the admin already exists and is active → skip.
//  2. CreateEmployee via user-service gRPC (idempotent, ignores AlreadyExists).
//  3. auth-service Kafka consumer picks up user.employee-created → publishes ACTIVATION token.
//  4. Read the activation token from the notification.send-email Kafka topic.
//  5. ActivateAccount via auth-service gRPC with the token + desired password.
//
// Flow (test client):
//  1. Try Login — if it succeeds, the client already exists and is active → skip.
//  2. Check if client exists by email via client-service gRPC GetClientByEmail.
//  3. CreateClient via client-service gRPC (idempotent, ignores AlreadyExists).
//  4. auth-service Kafka consumer picks up client.created → publishes ACTIVATION token.
//  5. Read the activation token from the notification.send-email Kafka topic.
//  6. ActivateAccount via auth-service gRPC with the token + desired password.
//
// Environment variables:
//
//	USER_GRPC_ADDR   — user-service gRPC address   (default: user-service:50052)
//	AUTH_GRPC_ADDR   — auth-service gRPC address   (default: auth-service:50051)
//	CLIENT_GRPC_ADDR — client-service gRPC address (default: client-service:50054)
//	KAFKA_BROKERS    — comma-separated broker list  (default: kafka:9092)
//	ADMIN_EMAIL      — admin email                  (default: admin@admin.com)
//	ADMIN_PASSWORD   — admin password               (default: Admin1234!)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
	userpb "github.com/exbanka/contract/userpb"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func dial(addr string) *grpc.ClientConn {
	for i := 0; i < 20; i++ {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			return conn
		}
		log.Printf("seeder: waiting for %s (%v)…", addr, err)
		time.Sleep(3 * time.Second)
	}
	log.Fatalf("seeder: cannot connect to %s", addr)
	return nil
}

func main() {
	userAddr := getenv("USER_GRPC_ADDR", "user-service:50052")
	authAddr := getenv("AUTH_GRPC_ADDR", "auth-service:50051")
	clientAddr := getenv("CLIENT_GRPC_ADDR", "client-service:50054")
	kafka := getenv("KAFKA_BROKERS", "kafka:9092")
	email := getenv("ADMIN_EMAIL", "admin@admin.com")
	password := getenv("ADMIN_PASSWORD", "Admin1234!")

	log.Printf("seeder: admin email=%s", email)

	// ── 1. Connect to services ────────────────────────────────────────────────

	userConn := dial(userAddr)
	defer userConn.Close()
	authConn := dial(authAddr)
	defer authConn.Close()
	clientConn := dial(clientAddr)
	defer clientConn.Close()

	userClient := userpb.NewUserServiceClient(userConn)
	authClient := authpb.NewAuthServiceClient(authConn)
	clientSvcClient := clientpb.NewClientServiceClient(clientConn)

	ctx := context.Background()

	// ── 2. Try Login — already bootstrapped? ──────────────────────────────────

	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    email,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: admin already active — nothing to do")
	} else {
		// ── 3. Look up existing employee by email (may already exist) ────────────

		principalID := findEmployeeByEmail(ctx, userClient, email)

		// ── 4. Create employee if not found ───────────────────────────────────────

		if principalID == 0 {
			createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
			defer createCancel()
			_, createErr := userClient.CreateEmployee(createCtx, &userpb.CreateEmployeeRequest{
				FirstName:   "System",
				LastName:    "Admin",
				DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
				Gender:      "other",
				Email:       email,
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
				if st.Code() != codes.AlreadyExists {
					log.Fatalf("seeder: CreateEmployee failed: %v", createErr)
				}
			}
			log.Println("seeder: employee created")
			principalID = findEmployeeByEmail(ctx, userClient, email)
			if principalID == 0 {
				log.Fatalf("seeder: admin employee not found after create (email=%s)", email)
			}
		} else {
			log.Println("seeder: employee already exists, continuing")
		}
		log.Printf("seeder: principal_id=%d", principalID)

		// ── 5. Wait for auth-service to pick up user.employee-created and publish activation token ──

		log.Println("seeder: employee created, waiting for activation token on Kafka…")

		// ── 6. Read activation token from Kafka ───────────────────────────────────

		token := readActivationToken(kafka, email)
		log.Printf("seeder: got activation token (len=%d)", len(token))

		// ── 7. Activate account with desired password ─────────────────────────────

		actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
		defer actCancel()
		_, err := authClient.ActivateAccount(actCtx, &authpb.ActivateAccountRequest{
			Token:           token,
			Password:        password,
			ConfirmPassword: password,
		})
		if err != nil {
			log.Fatalf("seeder: ActivateAccount failed: %v", err)
		}

		log.Printf("seeder: admin account active — email=%s", email)
	}

	// ── Client bootstrapping ──────────────────────────────────────────────────
	clientEmail := deriveClientEmail(email)
	seedClient(ctx, authClient, clientSvcClient, kafka, clientEmail, password)

	log.Println("seeder: all bootstrapping complete")
}

// readActivationToken scans the notification.send-email Kafka topic for an
// ACTIVATION message addressed to email and returns the token.
// Retries for up to 60 seconds to tolerate startup ordering.
func readActivationToken(brokers, email string) string {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r := kafkalib.NewReader(kafkalib.ReaderConfig{
			Brokers:     []string{brokers},
			Topic:       "notification.send-email",
			Partition:   0,
			StartOffset: kafkalib.FirstOffset,
			MaxWait:     500 * time.Millisecond,
		})

		scanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		token := scanOnce(r, scanCtx, email)
		cancel()
		r.Close()

		if token != "" {
			return token
		}
		log.Println("seeder: activation token not yet in Kafka, retrying…")
		time.Sleep(2 * time.Second)
	}
	log.Fatalf("seeder: timed out waiting for activation token for %s", email)
	return ""
}

func scanOnce(r *kafkalib.Reader, ctx context.Context, email string) string {
	var latest string
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			break
		}
		var body struct {
			To        string            `json:"to"`
			EmailType string            `json:"email_type"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(msg.Value, &body) != nil {
			continue
		}
		if body.To == email && body.EmailType == "ACTIVATION" {
			if t := body.Data["token"]; t != "" {
				latest = t
			}
		}
	}
	return latest
}

// findEmployeeByEmail does a partial-match list and returns the first exact match.
// Returns 0 if not found.
func findEmployeeByEmail(ctx context.Context, c userpb.UserServiceClient, email string) int64 {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.ListEmployees(listCtx, &userpb.ListEmployeesRequest{
		EmailFilter: email,
		Page:        1,
		PageSize:    50,
	})
	if err != nil {
		return 0
	}
	for _, emp := range resp.Employees {
		if emp.Email == email {
			return emp.Id
		}
	}
	return 0
}

// deriveClientEmail inserts "+testclient" before the "@" in the admin email.
// e.g. "admin@admin.com" → "admin+testclient@admin.com"
func deriveClientEmail(adminEmail string) string {
	parts := strings.SplitN(adminEmail, "@", 2)
	if len(parts) != 2 {
		return "testclient@admin.com"
	}
	return parts[0] + "+testclient@" + parts[1]
}

// seedClient creates the default test client, waits for its activation token,
// and activates the account. Skips silently if the client is already active.
func seedClient(
	ctx context.Context,
	authClient authpb.AuthServiceClient,
	clientSvcClient clientpb.ClientServiceClient,
	kafkaBrokers string,
	clientEmail string,
	password string,
) {
	log.Printf("seeder: client email=%s", clientEmail)

	// ── 1. Try Login — already bootstrapped? ──────────────────────────────────
	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    clientEmail,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: test client already active — skipping")
		return
	}

	// ── 2. Check if client already exists by email ────────────────────────────
	getCtx, getCancel := context.WithTimeout(ctx, 10*time.Second)
	defer getCancel()
	existing, getErr := clientSvcClient.GetClientByEmail(getCtx, &clientpb.GetClientByEmailRequest{
		Email: clientEmail,
	})

	clientExists := getErr == nil && existing != nil && existing.Id > 0

	// ── 3. Create client if not found ─────────────────────────────────────────
	if !clientExists {
		createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
		defer createCancel()
		_, createErr := clientSvcClient.CreateClient(createCtx, &clientpb.CreateClientRequest{
			FirstName:   "Test",
			LastName:    "Client",
			DateOfBirth: time.Date(1995, 6, 15, 0, 0, 0, 0, time.UTC).Unix(),
			Gender:      "other",
			Email:       clientEmail,
			Phone:       "+381000000001",
			Address:     "Test Client Address",
			Jmbg:        "1506995000001",
		})
		if createErr != nil {
			st, _ := status.FromError(createErr)
			if st.Code() != codes.AlreadyExists {
				log.Fatalf("seeder: CreateClient failed: %v", createErr)
			}
			log.Println("seeder: client already exists (AlreadyExists)")
		} else {
			log.Println("seeder: client created")
		}
	} else {
		log.Println("seeder: client already exists, continuing to activation")
	}

	// ── 4. Wait for activation token on Kafka ─────────────────────────────────
	log.Println("seeder: waiting for client activation token on Kafka…")
	token := readActivationToken(kafkaBrokers, clientEmail)
	log.Printf("seeder: got client activation token (len=%d)", len(token))

	// ── 5. Activate account ───────────────────────────────────────────────────
	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	defer actCancel()
	_, err := authClient.ActivateAccount(actCtx, &authpb.ActivateAccountRequest{
		Token:           token,
		Password:        password,
		ConfirmPassword: password,
	})
	if err != nil {
		log.Fatalf("seeder: client ActivateAccount failed: %v", err)
	}

	log.Printf("seeder: test client account active — email=%s", clientEmail)
}

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	_ = fmt.Sprintf // avoid unused import
}
```

## Design decisions

1. **Single `Login` RPC for existence check.** Auth-service's `Login` handles both employee and client accounts (switches on `PrincipalType`). No separate `ClientLogin` RPC is needed.

2. **`GetClientByEmail` before `CreateClient`.** Unlike the employee flow which uses `ListEmployees` with a filter, the client proto has a dedicated `GetClientByEmail` RPC that returns a single exact match. This is cleaner and avoids false positives from partial matches.

3. **Reuse `readActivationToken`.** The existing Kafka scanning function works for both flows because it matches on the `to` email field. No changes needed.

4. **Admin flow wrapped in else-block.** The original code had an early `return` when admin login succeeded. This is changed to an `if/else` so execution continues to the client seeding step regardless. If the admin already exists, the employee creation is skipped but the client flow still runs.

5. **JMBG for test client.** The value `1506995000001` encodes the birth date (15-06-995 in JMBG encoding) and uses a unique suffix. This avoids collisions with the admin employee JMBG `0101990000000`.

6. **No new env vars for client details.** The client email is derived from `ADMIN_EMAIL` and the password reuses `ADMIN_PASSWORD`. Only `CLIENT_GRPC_ADDR` is a new env var (required to reach the client-service).

## Testing

- [ ] **Manual test:** Run `docker-compose up` with a fresh database. Verify seeder logs show both admin and client creation + activation.
- [ ] **Idempotency test:** Run `docker-compose up` a second time (no fresh DB). Verify seeder logs show "admin already active" and "test client already active" with no errors.
- [ ] **Login test:** After seeding, use the API gateway to log in with both `admin@admin.com` and `admin+testclient@admin.com` and verify tokens are returned.
