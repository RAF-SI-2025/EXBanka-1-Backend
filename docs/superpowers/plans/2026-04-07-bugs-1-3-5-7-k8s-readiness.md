# Bugs 1/3/5/7 + Kubernetes Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix four production bugs (transfer preview, mobile verification inbox, docker-compose images, blueprint testing) and make all services Kubernetes-ready with health probes, gRPC retry logic, and a hardened seeder.

**Architecture:** Six workstreams: (1) Add a transfer preview endpoint that combines fee + exchange data in the gateway, (2) Decouple notification-service from auth-service by making device_id optional in inbox and removing the `GetDeviceInfo` gRPC call, (3) Update docker-compose image versions, (4) Audit and test blueprint limits, (5) Add HTTP readiness/liveness/startup probes and gRPC retry dial options to all services, (6) Harden the seeder for Kubernetes Job deployment.

**Tech Stack:** Go, gRPC, Kafka, GORM, PostgreSQL, Docker Compose, Kubernetes

---

## File Map

### Bug 1 — Transfer Preview Endpoint

| File | Action | Purpose |
|------|--------|---------|
| `contract/proto/transaction/transaction.proto` | Modify | Add `CalculateFee` RPC to `FeeService` |
| `contract/transactionpb/*.go` | Regenerate | `make proto` |
| `transaction-service/internal/handler/fee_handler.go` | Modify | Implement `CalculateFee` gRPC handler |
| `transaction-service/internal/service/fee_service.go` | No change | Already has `CalculateFee` method |
| `api-gateway/internal/handler/transaction_handler.go` | Modify | Add `PreviewTransfer` HTTP handler |
| `api-gateway/internal/router/router_v1.go` | Modify | Register `POST /api/v1/me/transfers/preview` |

### Bug 3 — Mobile Verification Inbox Fix

| File | Action | Purpose |
|------|--------|---------|
| `notification-service/internal/consumer/verification_consumer.go` | Modify | Remove auth-service dependency, make device_id optional |
| `notification-service/internal/repository/mobile_inbox_repository.go` | Modify | `MarkDelivered` — relax device_id filter |
| `notification-service/internal/config/config.go` | Modify | Remove `AuthGRPCAddr` |
| `notification-service/cmd/main.go` | Modify | Remove auth-service client wiring |
| `docker-compose.yml` | Modify | Remove `AUTH_GRPC_ADDR` + `auth-service` dep from notification-service |
| `docker-compose-images.yml` | Modify | Same cleanup |

### Bug 5 — Docker Compose Image Updates

| File | Action | Purpose |
|------|--------|---------|
| `docker-compose.yml` | Modify | Bump infrastructure image tags |
| `docker-compose-images.yml` | Modify | Bump infrastructure image tags |

### Bug 7 — Blueprint Audit

| File | Action | Purpose |
|------|--------|---------|
| `user-service/internal/service/blueprint_service.go` | Audit | Verify correctness |
| `user-service/internal/service/blueprint_service_test.go` | Audit + extend | Verify coverage, add missing cases |

### Kubernetes Readiness — Health Probes

| File | Action | Purpose |
|------|--------|---------|
| `contract/metrics/server.go` | Modify | Add `/livez`, `/readyz` (with DB ping), `/startupz` endpoints |
| Every service `cmd/main.go` | Modify | Register DB readiness check + signal startup complete |

### Kubernetes Readiness — gRPC Retry

| File | Action | Purpose |
|------|--------|---------|
| `contract/shared/grpc_dial.go` | Create | Shared retry dial helper |
| Every service `cmd/main.go` + gateway clients | Modify | Use retry dial |

### Kubernetes Readiness — Seeder Hardening

| File | Action | Purpose |
|------|--------|---------|
| `seeder/cmd/main.go` | Modify | Add 30s cooldown, health checks, better retry |

---

## Task 1: Add `CalculateFee` RPC to proto (Bug 1)

**Files:**
- Modify: `contract/proto/transaction/transaction.proto`

### Steps

- [ ] **1.1** Add `CalculateFee` RPC and messages to `contract/proto/transaction/transaction.proto`. Insert after the `DeleteFee` RPC in the `FeeService`:

```protobuf
  rpc CalculateFee(CalculateFeeRequest) returns (CalculateFeeResponse);
```

Add message definitions after `DeleteFeeResponse`:

```protobuf
message CalculateFeeRequest {
  string amount = 1;
  string transaction_type = 2;   // "payment" or "transfer"
  string currency_code = 3;      // e.g. "RSD", "EUR"
}

message CalculateFeeResponse {
  string total_fee = 1;
  repeated FeeBreakdown applied_fees = 2;
}

message FeeBreakdown {
  string name = 1;
  string fee_type = 2;
  string fee_value = 3;
  string calculated_amount = 4;
}
```

- [ ] **1.2** Regenerate protobuf:

```bash
make proto
```

- [ ] **1.3** Commit: `proto: add CalculateFee RPC to FeeService`

---

## Task 2: Implement `CalculateFee` gRPC handler (Bug 1)

**Files:**
- Modify: `transaction-service/internal/handler/fee_handler.go`
- Modify: `transaction-service/internal/service/fee_service.go`

### Steps

- [ ] **2.1** Add `CalculateFeeDetailed` to `transaction-service/internal/service/fee_service.go` that returns both total and breakdown:

```go
// FeeDetail holds a single fee rule's contribution.
type FeeDetail struct {
	Name             string
	FeeType          string
	FeeValue         decimal.Decimal
	CalculatedAmount decimal.Decimal
}

// CalculateFeeDetailed returns the total fee and per-rule breakdown.
func (s *FeeService) CalculateFeeDetailed(amount decimal.Decimal, txType, currency string) (decimal.Decimal, []FeeDetail, error) {
	fees, err := s.repo.GetApplicableFees(amount, txType, currency)
	if err != nil {
		return decimal.Zero, nil, fmt.Errorf("failed to determine applicable fees: %w", err)
	}
	total := decimal.Zero
	details := make([]FeeDetail, 0, len(fees))
	for _, fee := range fees {
		var thisFee decimal.Decimal
		if fee.FeeType == "percentage" {
			thisFee = amount.Mul(fee.FeeValue).Div(decimal.NewFromInt(100))
		} else {
			thisFee = fee.FeeValue
		}
		if fee.MaxFee.IsPositive() && thisFee.GreaterThan(fee.MaxFee) {
			thisFee = fee.MaxFee
		}
		total = total.Add(thisFee)
		details = append(details, FeeDetail{
			Name:             fee.Name,
			FeeType:          fee.FeeType,
			FeeValue:         fee.FeeValue,
			CalculatedAmount: thisFee,
		})
	}
	return total, details, nil
}
```

- [ ] **2.2** Add `CalculateFee` handler to `transaction-service/internal/handler/fee_handler.go`:

```go
func (h *FeeGRPCHandler) CalculateFee(ctx context.Context, req *pb.CalculateFeeRequest) (*pb.CalculateFeeResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() {
		return nil, status.Errorf(codes.InvalidArgument, "amount must be a positive decimal")
	}
	if req.TransactionType == "" {
		return nil, status.Errorf(codes.InvalidArgument, "transaction_type is required")
	}

	total, details, err := h.feeSvc.CalculateFeeDetailed(amount, req.TransactionType, req.CurrencyCode)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fee calculation failed: %v", err)
	}

	breakdown := make([]*pb.FeeBreakdown, len(details))
	for i, d := range details {
		breakdown[i] = &pb.FeeBreakdown{
			Name:             d.Name,
			FeeType:          d.FeeType,
			FeeValue:         d.FeeValue.StringFixed(4),
			CalculatedAmount: d.CalculatedAmount.StringFixed(4),
		}
	}
	return &pb.CalculateFeeResponse{
		TotalFee:    total.StringFixed(4),
		AppliedFees: breakdown,
	}, nil
}
```

- [ ] **2.3** Build and run tests:

```bash
cd transaction-service && go build ./... && go test ./internal/service/ -v
```

- [ ] **2.4** Commit: `feat(transaction): implement CalculateFee gRPC handler with fee breakdown`

---

## Task 3: Add `PreviewTransfer` gateway endpoint (Bug 1)

**Files:**
- Modify: `api-gateway/internal/handler/transaction_handler.go`
- Modify: `api-gateway/internal/router/router_v1.go`

### Steps

- [ ] **3.1** Add the `PreviewTransfer` handler to `api-gateway/internal/handler/transaction_handler.go`. Add a new struct and the `exchangeClient` field to `TransactionHandler`:

First, update the struct and constructor to include the exchange client:

```go
type TransactionHandler struct {
	txClient       transactionpb.TransactionServiceClient
	feeClient      transactionpb.FeeServiceClient
	accountClient  accountpb.AccountServiceClient
	exchangeClient exchangepb.ExchangeServiceClient
}

func NewTransactionHandler(txClient transactionpb.TransactionServiceClient, feeClient transactionpb.FeeServiceClient, accountClient accountpb.AccountServiceClient, exchangeClient exchangepb.ExchangeServiceClient) *TransactionHandler {
	return &TransactionHandler{txClient: txClient, feeClient: feeClient, accountClient: accountClient, exchangeClient: exchangeClient}
}
```

Then add the handler:

```go
type previewTransferRequest struct {
	FromAccountNumber string  `json:"from_account_number" binding:"required"`
	ToAccountNumber   string  `json:"to_account_number" binding:"required"`
	Amount            float64 `json:"amount" binding:"required"`
}

// @Summary      Preview transfer costs
// @Description  Returns commission fees and exchange rate data for a transfer without creating it.
// @Tags         transfers
// @Accept       json
// @Produce      json
// @Param        body  body  previewTransferRequest  true  "Transfer preview data"
// @Security     BearerAuth
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  map[string]string
// @Failure      401   {object}  map[string]string
// @Failure      500   {object}  map[string]string
// @Router       /api/v1/me/transfers/preview [post]
func (h *TransactionHandler) PreviewTransfer(c *gin.Context) {
	var req previewTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}
	if err := positive("amount", req.Amount); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	amountStr := fmt.Sprintf("%.4f", req.Amount)
	ctx := c.Request.Context()

	// Look up account currencies
	fromAcc, err := h.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: req.FromAccountNumber})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	toAcc, err := h.accountClient.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: req.ToAccountNumber})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	fromCurrency := fromAcc.CurrencyCode
	toCurrency := toAcc.CurrencyCode
	if fromCurrency == "" {
		fromCurrency = "RSD"
	}
	if toCurrency == "" {
		toCurrency = "RSD"
	}

	// Calculate fees
	feeResp, err := h.feeClient.CalculateFee(ctx, &transactionpb.CalculateFeeRequest{
		Amount:          amountStr,
		TransactionType: "transfer",
		CurrencyCode:    fromCurrency,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	result := gin.H{
		"from_currency":      fromCurrency,
		"to_currency":        toCurrency,
		"input_amount":       amountStr,
		"total_fee":          feeResp.TotalFee,
		"fee_breakdown":      feeResp.AppliedFees,
	}

	// If cross-currency, get exchange rate info
	if fromCurrency != toCurrency {
		exchangeResp, err := h.exchangeClient.Calculate(ctx, &exchangepb.CalculateRequest{
			FromCurrency: fromCurrency,
			ToCurrency:   toCurrency,
			Amount:       amountStr,
		})
		if err != nil {
			handleGRPCError(c, err)
			return
		}
		result["converted_amount"] = exchangeResp.ConvertedAmount
		result["exchange_rate"] = exchangeResp.EffectiveRate
		result["exchange_commission_rate"] = exchangeResp.CommissionRate
	} else {
		result["converted_amount"] = amountStr
		result["exchange_rate"] = "1.0000"
		result["exchange_commission_rate"] = "0.0000"
	}

	c.JSON(http.StatusOK, result)
}
```

Add the `exchangepb` import to the file:

```go
	exchangepb "github.com/exbanka/contract/exchangepb"
```

- [ ] **3.2** Update all call sites that create `NewTransactionHandler` to pass the exchange client. There are three locations:

In `api-gateway/internal/router/router_v1.go` (line ~124):
```go
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient, exchangeClient)
```

In `api-gateway/internal/router/router.go` (find the equivalent line):
```go
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient, exchangeClient)
```

Both `Setup` and `SetupV1Routes` already receive `exchangeClient` as a parameter.

- [ ] **3.3** Register the route in `api-gateway/internal/router/router_v1.go`. Add after the existing `me.POST("/transfers", ...)` line (around line 183):

```go
			me.POST("/transfers/preview", txHandler.PreviewTransfer)
```

**Important:** This line MUST come BEFORE `me.GET("/transfers/:id", ...)` and `me.POST("/transfers/:id/execute", ...)` to avoid Gin treating "preview" as an `:id` parameter.

- [ ] **3.4** Build:

```bash
cd api-gateway && go build ./...
```

- [ ] **3.5** Commit: `feat(gateway): add POST /api/v1/me/transfers/preview for fee+exchange preview`

---

## Task 4: Decouple notification-service from auth-service (Bug 3)

**Files:**
- Modify: `notification-service/internal/consumer/verification_consumer.go`
- Modify: `notification-service/internal/repository/mobile_inbox_repository.go`
- Modify: `notification-service/internal/config/config.go`
- Modify: `notification-service/cmd/main.go`

### Steps

- [ ] **4.1** Rewrite `handleMobileDelivery` in `notification-service/internal/consumer/verification_consumer.go` to remove the auth-service lookup. When `device_id` is empty, store the inbox item with an empty device_id — the mobile app will find it by `user_id` regardless:

```go
func (c *VerificationConsumer) handleMobileDelivery(ctx context.Context, event kafkamsg.VerificationChallengeCreatedMessage) {
	expiresAt, err := time.Parse(time.RFC3339, event.ExpiresAt)
	if err != nil {
		log.Printf("verification consumer: invalid expires_at: %v", err)
		return
	}

	item := &model.MobileInboxItem{
		UserID:      event.UserID,
		DeviceID:    event.DeviceID, // may be empty — mobile app queries by user_id
		ChallengeID: event.ChallengeID,
		Method:      event.Method,
		DisplayData: datatypes.JSON(event.DisplayData),
		ExpiresAt:   expiresAt,
	}
	if err := c.inboxRepo.Create(item); err != nil {
		log.Printf("verification consumer: inbox create error: %v", err)
		return
	}

	// Publish to mobile-push topic for WebSocket delivery
	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"challenge_id": event.ChallengeID,
		"method":       event.Method,
		"display_data": event.DisplayData,
		"expires_at":   event.ExpiresAt,
	})
	pushMsg := kafkamsg.MobilePushMessage{
		UserID:   event.UserID,
		DeviceID: event.DeviceID,
		Type:     "verification_challenge",
		Payload:  string(payloadJSON),
	}
	if err := c.producer.Publish(ctx, kafkamsg.TopicMobilePush, pushMsg); err != nil {
		log.Printf("verification consumer: mobile push publish error: %v", err)
	} else {
		svc.NotificationMobilePushTotal.Inc()
	}
}
```

- [ ] **4.2** Remove auth-service from the `VerificationConsumer` struct and constructor. Update the struct:

```go
type VerificationConsumer struct {
	reader    *kafkago.Reader
	sender    *sender.EmailSender
	producer  *kafkaprod.Producer
	inboxRepo *repository.MobileInboxRepository
}

func NewVerificationConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, inboxRepo *repository.MobileInboxRepository) *VerificationConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicVerificationChallengeCreated,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &VerificationConsumer{
		reader:    reader,
		sender:    emailSender,
		producer:  producer,
		inboxRepo: inboxRepo,
	}
}
```

Remove the `authpb` import from the file.

- [ ] **4.3** Update `MarkDelivered` in `notification-service/internal/repository/mobile_inbox_repository.go` to make device_id matching optional — if the caller passes an empty device_id, match any:

```go
func (r *MobileInboxRepository) MarkDelivered(id uint64, deviceID string) error {
	now := time.Now()
	query := r.db.Model(&model.MobileInboxItem{}).Where("id = ? AND status = ?", id, "pending")
	if deviceID != "" {
		query = query.Where("device_id = ?", deviceID)
	}
	result := query.Updates(map[string]interface{}{
		"status":       "delivered",
		"delivered_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
```

- [ ] **4.4** Remove `AuthGRPCAddr` from `notification-service/internal/config/config.go`. Remove the field from the struct and the `Load()` function.

- [ ] **4.5** Remove auth-service client wiring from `notification-service/cmd/main.go`. Remove:
- The `authpb` import
- The `grpc.NewClient` call for auth-service (lines 76-82)
- The `authClient` variable
- Update the `NewVerificationConsumer` call to drop the `authClient` parameter:

```go
	verificationConsumer := consumer.NewVerificationConsumer(cfg.KafkaBrokers, emailSender, producer, inboxRepo)
```

Also remove the unused `"google.golang.org/grpc/credentials/insecure"` import if nothing else uses it.

- [ ] **4.6** Build:

```bash
cd notification-service && go build ./...
```

- [ ] **4.7** Commit: `refactor(notification): remove auth-service dependency, device_id now optional in inbox`

---

## Task 5: Update docker-compose files (Bug 3 + Bug 5)

**Files:**
- Modify: `docker-compose.yml`
- Modify: `docker-compose-images.yml`

### Steps

- [ ] **5.1** In both `docker-compose.yml` and `docker-compose-images.yml`, remove the notification-service dependency on auth-service:

Remove from notification-service `environment:`:
```yaml
      AUTH_GRPC_ADDR: "auth-service:50051"
```

Remove from notification-service `depends_on:`:
```yaml
      auth-service:
        condition: service_started
```

- [ ] **5.2** In both compose files, update infrastructure image tags:

| Current | Updated |
|---------|---------|
| `apache/kafka:3.7.0` | `apache/kafka:3.9.0` |
| `redis:7-alpine` | `redis:7.4-alpine` |
| `postgres:16-alpine` | `postgres:17-alpine` |
| `influxdb:2.7-alpine` | `influxdb:2.7-alpine` (keep — no newer stable alpine) |
| `prom/prometheus:v2.53.0` | `prom/prometheus:v2.54.1` |
| `grafana/grafana:11.1.0` | `grafana/grafana:11.4.0` |

- [ ] **5.3** Verify compose file syntax:

```bash
docker compose -f docker-compose.yml config --quiet
docker compose -f docker-compose-images.yml config --quiet
```

- [ ] **5.4** Commit: `chore: update docker-compose images, remove notification→auth dependency`

---

## Task 6: Audit and test blueprint limits (Bug 7)

**Files:**
- Audit: `user-service/internal/service/blueprint_service.go`
- Audit + Modify: `user-service/internal/service/blueprint_service_test.go`

### Steps

- [ ] **6.1** Read `user-service/internal/service/blueprint_service.go` and `user-service/internal/service/blueprint_service_test.go` in full. Check for:
  - All three apply paths (employee, actuary, client) are tested
  - Validation rejects missing/invalid fields
  - `SeedFromTemplates` is tested
  - Upsert semantics are correct (applying twice updates, not duplicates)
  - Error paths tested (blueprint not found, wrong type, invalid values)

- [ ] **6.2** Run existing tests and check output:

```bash
cd user-service && go test ./internal/service/ -run TestBlueprint -v
```

- [ ] **6.3** If any test cases are missing from the list above, add them. At minimum, ensure these test cases exist (add missing ones):

```go
func TestApplyBlueprint_Employee_SetsLimits(t *testing.T) {
	// Create an employee blueprint, apply it, verify the limit repo got the right values
}

func TestApplyBlueprint_Actuary_SetsLimits(t *testing.T) {
	// Create an actuary blueprint, apply it, verify actuary repo got the right values
}

func TestApplyBlueprint_Client_CallsGRPC(t *testing.T) {
	// Create a client blueprint, apply it, verify client gRPC was called with right values
}

func TestApplyBlueprint_NotFound(t *testing.T) {
	// Apply non-existent blueprint ID, expect error
}

func TestApplyBlueprint_Employee_UpsertOverwrites(t *testing.T) {
	// Apply once, then apply different blueprint, verify values are overwritten
}

func TestCreateBlueprint_InvalidType(t *testing.T) {
	// Type "invalid" should fail
}

func TestCreateBlueprint_InvalidValues_MissingField(t *testing.T) {
	// Employee blueprint with missing max_single_transaction should fail
}

func TestCreateBlueprint_InvalidValues_NonDecimal(t *testing.T) {
	// Employee blueprint with "abc" as a value should fail
}
```

- [ ] **6.4** Run tests:

```bash
cd user-service && go test ./internal/service/ -run TestBlueprint -v -count=1
```

- [ ] **6.5** Fix any issues found. If all passes, commit: `test(user): audit and complete blueprint limit test coverage`

---

## Task 7: Add liveness/readiness/startup probes to metrics server (K8s)

**Files:**
- Modify: `contract/metrics/server.go`
- Modify: `contract/metrics/server_test.go`

### Steps

- [ ] **7.1** Update `contract/metrics/server.go` to add `/livez`, `/readyz`, and `/startupz` probes. `/health` stays for Prometheus/backwards compat. Readiness checks both startup completion AND live dependency health (DB ping):

```go
// ReadinessCheck is a function that returns nil if a dependency is healthy.
// Services register checks for their critical dependencies (DB, Redis, etc.).
type ReadinessCheck func(ctx context.Context) error

// StartMetricsServer starts an HTTP server exposing:
//   /metrics  — Prometheus scrape endpoint
//   /health   — legacy liveness probe (always 200, kept for Prometheus/backwards compat)
//   /livez    — liveness probe (always 200)
//   /readyz   — readiness probe (503 until started AND all checks pass)
//   /startupz — startup probe (503 until started, then 200 — no dependency checks)
//
// Returns (markReady func(), addCheck func(ReadinessCheck), shutdown func(ctx) error).
//   - Call markReady() once initial startup is done (DB migrated, gRPC listening).
//   - Call addCheck() to register dependency health checks (DB ping, etc.).
//     These are evaluated on every /readyz request.
func StartMetricsServer(port string) (func(), func(ReadinessCheck), func(ctx context.Context) error) {
	started := &atomic.Bool{}
	var checks []ReadinessCheck
	var checksMu sync.Mutex

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// Liveness — always 200 if the process is running
	livenessHandler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
	mux.HandleFunc("/health", livenessHandler)
	mux.HandleFunc("/livez", livenessHandler)

	// Startup — 503 until markReady() called, then always 200
	mux.HandleFunc("/startupz", func(w http.ResponseWriter, _ *http.Request) {
		if started.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("STARTED"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("STARTING"))
		}
	})

	// Readiness — 503 until started AND all dependency checks pass
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if !started.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NOT STARTED"))
			return
		}

		checksMu.Lock()
		currentChecks := make([]ReadinessCheck, len(checks))
		copy(currentChecks, checks)
		checksMu.Unlock()

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		for _, check := range currentChecks {
			if err := check(ctx); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("NOT READY: " + err.Error()))
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		log.Printf("metrics server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()

	markReady := func() { started.Store(true) }

	addCheck := func(c ReadinessCheck) {
		checksMu.Lock()
		checks = append(checks, c)
		checksMu.Unlock()
	}

	shutdown := func(ctx context.Context) error {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		fmt.Println("shutting down metrics server...")
		return srv.Shutdown(shutdownCtx)
	}

	return markReady, addCheck, shutdown
}
```

Add imports: `"sync/atomic"`, `"sync"`

- [ ] **7.2** This changes the return signature of `StartMetricsServer`. Update every call site. Each service's `cmd/main.go` has:

```go
metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
```

Change to:

```go
markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
```

**For DB-backed services**, register a DB ping check right after `gorm.Open`:

```go
db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
if err != nil {
    log.Fatalf("failed to connect to database: %v", err)
}
sqlDB, _ := db.DB()
addReadinessCheck(func(ctx context.Context) error {
    return sqlDB.PingContext(ctx)
})
```

**For api-gateway** (no DB), no checks needed — just `markReady, _, metricsShutdown := ...`.

Then add `markReady()` right before the blocking `grpcServer.Serve(lis)` call (or before the HTTP server goroutine for api-gateway).

**Services to update** (each has `cmd/main.go`):

| Service | DB check? | Notes |
|---------|-----------|-------|
| `auth-service` | Yes | auth_db |
| `user-service` | Yes | user_db |
| `notification-service` | Yes | notification_db |
| `client-service` | Yes | client_db |
| `account-service` | Yes | account_db |
| `card-service` | Yes | card_db |
| `transaction-service` | Yes | transaction_db |
| `credit-service` | Yes | credit_db |
| `exchange-service` | Yes | exchange_db |
| `verification-service` | Yes | verification_db |
| `stock-service` | Yes | stock_db |
| `api-gateway` | No | No DB, just `markReady, _, metricsShutdown` |

- [ ] **7.3** Update test if exists:

```bash
cd contract && go test ./metrics/ -v
```

- [ ] **7.4** Build all:

```bash
make build
```

- [ ] **7.5** Commit: `feat(contract): add /livez /readyz /startupz probes with DB health checks`

---

## Task 8: Add gRPC retry dial helper (K8s)

**Files:**
- Create: `contract/shared/grpc_dial.go`

### Steps

- [ ] **8.1** Create `contract/shared/grpc_dial.go` with a retry-enabled gRPC dial helper:

```go
package shared

import (
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// DialGRPC creates a gRPC client connection with retry and keepalive settings
// suitable for Kubernetes where services may start in any order.
//
// The connection is lazy — it won't block or fail if the target is not yet
// available. gRPC's built-in retry/backoff will keep trying in the background.
func DialGRPC(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{
			"methodConfig": [{
				"name": [{}],
				"retryPolicy": {
					"MaxAttempts": 5,
					"InitialBackoff": "0.5s",
					"MaxBackoff": "5s",
					"BackoffMultiplier": 2.0,
					"RetryableStatusCodes": ["UNAVAILABLE", "DEADLINE_EXCEEDED"]
				}
			}]
		}`),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  500 * time.Millisecond,
				Multiplier: 2.0,
				MaxDelay:   10 * time.Second,
			},
			MinConnectTimeout: 5 * time.Second,
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
}

// MustDialGRPC is like DialGRPC but logs and continues on error
// (the connection will retry in the background).
func MustDialGRPC(addr string) *grpc.ClientConn {
	conn, err := DialGRPC(addr)
	if err != nil {
		log.Printf("warn: initial gRPC dial to %s failed: %v (will retry)", addr, err)
	}
	return conn
}
```

- [ ] **8.2** Run `go mod tidy` in contract:

```bash
cd contract && go mod tidy
```

- [ ] **8.3** Commit: `feat(contract): add shared gRPC dial helper with retry and keepalive`

---

## Task 9: Migrate gateway gRPC clients to retry dial (K8s)

**Files:**
- Modify: all files in `api-gateway/internal/grpc/`

### Steps

- [ ] **9.1** Update every client factory in `api-gateway/internal/grpc/` to use `shared.DialGRPC` instead of raw `grpc.NewClient`. The pattern is the same for all files. Example for `auth_client.go`:

```go
package grpc

import (
	"google.golang.org/grpc"

	authpb "github.com/exbanka/contract/authpb"
	"github.com/exbanka/contract/shared"
)

func NewAuthClient(addr string) (authpb.AuthServiceClient, *grpc.ClientConn, error) {
	conn, err := shared.DialGRPC(addr)
	if err != nil {
		return nil, nil, err
	}
	return authpb.NewAuthServiceClient(conn), conn, nil
}
```

Apply the same change to all 13 client files in `api-gateway/internal/grpc/`:
- `auth_client.go`
- `user_client.go`
- `client_client.go`
- `account_client.go`
- `card_client.go`
- `transaction_client.go`
- `credit_client.go`
- `exchange_client.go`
- `notification_client.go`
- `verification_client.go`
- `stock_client.go`
- `limit_client.go`
- `blueprint_client.go`

Each one: replace `grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))` with `shared.DialGRPC(addr)`, add `"github.com/exbanka/contract/shared"` import, remove unused `"google.golang.org/grpc/credentials/insecure"` import.

- [ ] **9.2** Update backend services that create gRPC clients in their `cmd/main.go`:

Services with gRPC client connections to update:
- `auth-service/cmd/main.go` — user-service client, client-service client
- `notification-service/cmd/main.go` — (auth-service client already removed in Task 4)
- `client-service/cmd/main.go` — user-service client
- `account-service/cmd/main.go` — client-service client
- `card-service/cmd/main.go` — client-service client
- `transaction-service/cmd/main.go` — account-service, exchange-service, verification-service clients
- `credit-service/cmd/main.go` — account-service, client-service, user-service clients
- `verification-service/cmd/main.go` — auth-service client
- `stock-service/cmd/main.go` — user-service, account-service, exchange-service, client-service clients

For each, replace `grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))` with `shared.DialGRPC(addr)`.

- [ ] **9.3** Run `make tidy && make build`

- [ ] **9.4** Commit: `feat: migrate all gRPC clients to shared retry dial`

---

## Task 10: Harden seeder for Kubernetes Job deployment (K8s)

**Files:**
- Modify: `seeder/cmd/main.go`

### Steps

- [ ] **10.1** Add a startup cooldown and gRPC health checks. Replace the `dial` function and add a health-check helper:

```go
func dial(addr string) *grpc.ClientConn {
	for i := 0; i < 30; i++ {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			return conn
		}
		log.Printf("seeder: waiting for %s (%v)…", addr, err)
		time.Sleep(3 * time.Second)
	}
	log.Fatalf("seeder: cannot connect to %s after 90s", addr)
	return nil
}

// waitForHealthy checks the gRPC health endpoint until the service is SERVING.
func waitForHealthy(conn *grpc.ClientConn, name string, timeout time.Duration) error {
	client := grpc_health_v1.NewHealthClient(conn)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: name})
		cancel()
		if err == nil && resp.Status == grpc_health_v1.HealthCheckResponse_SERVING {
			log.Printf("seeder: %s is healthy", name)
			return nil
		}
		log.Printf("seeder: waiting for %s to be healthy…", name)
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("%s not healthy after %v", name, timeout)
}
```

Add imports:
```go
	"google.golang.org/grpc/health/grpc_health_v1"
```

- [ ] **10.2** Update `main()` to add a 30s initial cooldown and health checks before proceeding. Insert at the start of `main()`, before the `dial` calls:

```go
	// Initial cooldown — in Kubernetes, services may be starting simultaneously.
	cooldown := getenv("SEEDER_COOLDOWN", "30s")
	cooldownDuration, _ := time.ParseDuration(cooldown)
	if cooldownDuration > 0 {
		log.Printf("seeder: initial cooldown %v (waiting for services to start)…", cooldownDuration)
		time.Sleep(cooldownDuration)
	}
```

After the `dial` calls (after `clientConn := dial(clientAddr)`), add health checks:

```go
	// Wait for all services to be healthy before seeding
	healthTimeout := 120 * time.Second
	if err := waitForHealthy(userConn, "user-service", healthTimeout); err != nil {
		log.Fatalf("seeder: %v", err)
	}
	if err := waitForHealthy(authConn, "auth-service", healthTimeout); err != nil {
		log.Fatalf("seeder: %v", err)
	}
	if err := waitForHealthy(clientConn, "client-service", healthTimeout); err != nil {
		log.Fatalf("seeder: %v", err)
	}
```

- [ ] **10.3** Make `readActivationToken` more resilient. Increase the deadline from 60s to 120s and add Kafka broker connectivity check:

```go
func readActivationToken(brokers, email string) string {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		r := kafkalib.NewReader(kafkalib.ReaderConfig{
			Brokers:     strings.Split(brokers, ","),
			Topic:       "notification.send-email",
			Partition:   0,
			StartOffset: kafkalib.FirstOffset,
			MaxWait:     500 * time.Millisecond,
		})

		scanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		token := scanOnce(r, scanCtx, email)
		cancel()
		r.Close()

		if token != "" {
			return token
		}
		log.Println("seeder: activation token not yet in Kafka, retrying…")
		time.Sleep(3 * time.Second)
	}
	log.Fatalf("seeder: timed out waiting for activation token for %s", email)
	return ""
}
```

- [ ] **10.4** Add the `SEEDER_COOLDOWN` env to docker-compose files (both `docker-compose.yml` and `docker-compose-images.yml`) in the seeder's environment:

```yaml
      SEEDER_COOLDOWN: "10s"
```

(Use 10s for Docker Compose where depends_on exists; Kubernetes manifests will use 30s+.)

- [ ] **10.5** Build:

```bash
cd seeder && go mod tidy && go build -o bin/seeder ./cmd
```

- [ ] **10.6** Commit: `feat(seeder): add cooldown, health checks, and extended timeouts for k8s`

---

## Task 11: Build + Full Test

### Steps

- [ ] **11.1** Run tidy:

```bash
make tidy
```

- [ ] **11.2** Build all:

```bash
make build
```

- [ ] **11.3** Run full test suite:

```bash
make test
```

- [ ] **11.4** Fix any failures and commit.

---

## Task 12: Update Specification and Documentation

### Steps

- [ ] **12.1** Update `docs/Specification.md`:
  - Add `POST /api/v1/me/transfers/preview` endpoint
  - Document that notification-service no longer depends on auth-service
  - Note the new `CalculateFee` RPC in `FeeService`
  - Add health probe endpoints (`/livez`, `/readyz`, `/startupz`) to service documentation
  - Document seeder `SEEDER_COOLDOWN` env variable

- [ ] **12.2** Update `docs/api/REST_API_v1.md`:
  - Add `POST /api/v1/me/transfers/preview` section with request/response examples

- [ ] **12.3** Regenerate swagger docs:

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

- [ ] **12.4** Commit: `docs: add transfer preview endpoint, k8s probes, notification decoupling`

---

## Summary of Changes

| Area | Change |
|------|--------|
| **Bug 1** | New `POST /api/v1/me/transfers/preview` — returns fee breakdown + exchange rate in one call |
| **Bug 3** | notification-service no longer calls auth-service; inbox items stored with whatever device_id is in the event (or empty); mobile app finds them by user_id |
| **Bug 5** | Docker image tags bumped (postgres 17, redis 7.4, kafka 3.9, prometheus 2.54.1, grafana 11.4) |
| **Bug 7** | Blueprint service audited, test coverage verified and extended |
| **K8s probes** | All services expose `/livez` (liveness), `/readyz` (readiness), `/startupz` (startup) on metrics port; `/health` kept for backwards compat |
| **K8s retry** | All gRPC clients use `shared.DialGRPC` with retry policy (5 attempts, exponential backoff, UNAVAILABLE/DEADLINE_EXCEEDED retryable) + keepalive |
| **K8s seeder** | 30s cooldown, gRPC health checks before seeding, 120s Kafka token timeout |
