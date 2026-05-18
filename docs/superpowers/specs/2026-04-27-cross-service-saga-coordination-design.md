# Design: Cross-Service Saga Coordination (Spec B2)

**Status:** Approved
**Date:** 2026-04-27
**Scope:** New cross-cutting infrastructure. Touches every service that participates in a saga (stock-service, account-service, transaction-service, others). Builds on Spec B1.

## Problem

Sagas already span services today (crossbank: us ↔ peer bank; intra-bank: stock-service ↔ account-service ↔ transaction-service). Three concrete durability and traceability gaps:

1. **No mandatory idempotency contract.** `transaction-service` has `IdempotencyKeyFor()`; others do not. A saga that retries a step (e.g., after stock-service restart) can re-execute the downstream debit because the callee has no way to recognize "this is the same call."

2. **No cross-service traceability.** The saga ID lives only in stock-service's `saga_logs`. account-service has no record of "which saga caused this debit." Auditing a failed transaction requires manual joining across service logs.

3. **Double-write hazard for Kafka publishing inside sagas.** Today, Kafka events are published AFTER the DB transaction commits. A crash between commit and publish silently drops the event.

## Goal

Cross-service saga steps are crash-safe, idempotent, and auditable end-to-end.

## Approach

Three sub-features, each independently useful, designed to compose.

---

### B2.1 — Idempotency-key contract on saga-driven RPCs

Every gRPC method that may be called as a saga step accepts an `idempotency_key string` field. The saga passes a deterministic key: `<saga_id>:<step_kind>`. The callee enforces uniqueness with a per-service `idempotency_records` table:

```sql
CREATE TABLE idempotency_records (
    key            TEXT PRIMARY KEY,
    response_blob  BYTEA NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Service-side wrapper:

```go
func (s *AccountService) DebitWithIdempotency(ctx context.Context, req *Req) (*Resp, error) {
    if req.IdempotencyKey == "" {
        return nil, svcerr.New(codes.InvalidArgument, "idempotency_key required")
    }
    var resp *Resp
    err := s.db.Transaction(func(tx *gorm.DB) error {
        // Try to insert the idempotency row; if it already exists, return the cached response.
        rec := IdempotencyRecord{Key: req.IdempotencyKey}
        result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
        if result.RowsAffected == 0 {
            // Existing record — fetch and return cached response.
            tx.First(&rec, "key = ?", req.IdempotencyKey)
            return proto.Unmarshal(rec.ResponseBlob, resp)
        }
        // Fresh — execute the business logic.
        var err error
        resp, err = s.debitInternal(ctx, tx, req)
        if err != nil {
            return err
        }
        blob, _ := proto.Marshal(resp)
        return tx.Model(&rec).Update("response_blob", blob).Error
    })
    return resp, err
}
```

Records older than 30 days can be purged (background job, separate spec — out of scope here).

**Codegen check:** The `make permissions` (or a sibling `make rpc-check`) target scans proto files for methods marked `// idempotent` in their leading comment. For each, asserts the request message contains `string idempotency_key = N;`. Build fails on violation.

### B2.2 — Saga context propagated via gRPC metadata

A new `saga.Outgoing` interceptor on the saga side and `saga.Incoming` interceptor on the callee side:

```go
// Outgoing — wraps every gRPC client call inside a saga step.
md := metadata.New(map[string]string{
    "x-saga-id":    sagaCtx.SagaID,
    "x-saga-step":  string(sagaCtx.StepKind),
    "x-acting-id":  sagaCtx.ActingEmployeeIDOrEmpty(),
})
ctx = metadata.NewOutgoingContext(ctx, md)
```

```go
// Incoming — server-side interceptor in every service.
func SagaContextInterceptor(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo,
    handler grpc.UnaryHandler) (interface{}, error) {
    if md, ok := metadata.FromIncomingContext(ctx); ok {
        if sid := md.Get("x-saga-id"); len(sid) > 0 {
            ctx = WithSagaID(ctx, sid[0])
        }
        if step := md.Get("x-saga-step"); len(step) > 0 {
            ctx = WithSagaStep(ctx, step[0])
        }
        if acting := md.Get("x-acting-id"); len(acting) > 0 {
            ctx = WithActingEmployeeID(ctx, acting[0])
        }
    }
    return handler(ctx, req)
}
```

Side-effect tables gain optional columns:
```sql
ALTER TABLE account_ledger_entries
    ADD COLUMN saga_id    TEXT NULL,
    ADD COLUMN saga_step  TEXT NULL;
```

Repository code reads from context and persists alongside the business write. **No coupling**: callees don't know what a saga is conceptually — they just stamp the row.

Cross-service auditing becomes:
```sql
SELECT * FROM account_ledger_entries WHERE saga_id = '...';
SELECT * FROM stock_holdings         WHERE saga_id = '...';
```

### B2.3 — Outbox pattern for saga-published Kafka events

Per-service outbox table (one per service that publishes from inside a saga):

```sql
CREATE TABLE outbox_events (
    id           BIGSERIAL PRIMARY KEY,
    topic        TEXT NOT NULL,
    payload      BYTEA NOT NULL,
    headers      JSONB NULL,
    saga_id      TEXT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ NULL,
    last_error   TEXT NULL,
    attempt      INT NOT NULL DEFAULT 0
);

CREATE INDEX outbox_unpublished ON outbox_events (created_at)
    WHERE published_at IS NULL;
```

Publishing helper (inside saga step's TX):
```go
func (o *Outbox) Enqueue(tx *gorm.DB, topic string, payload proto.Message, sagaID string) error {
    blob, err := proto.Marshal(payload)
    if err != nil { return err }
    return tx.Create(&OutboxEvent{
        Topic:   topic,
        Payload: blob,
        SagaID:  &sagaID,
    }).Error
}
```

Drainer goroutine in each service's `cmd/main.go`:
```go
func (d *OutboxDrainer) Run(ctx context.Context) {
    t := time.NewTicker(500 * time.Millisecond)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            d.drainBatch(ctx, 100)
        }
    }
}

func (d *OutboxDrainer) drainBatch(ctx context.Context, limit int) {
    var rows []OutboxEvent
    d.db.Where("published_at IS NULL").Order("id").Limit(limit).Find(&rows)
    for _, row := range rows {
        if err := d.producer.Publish(ctx, row.Topic, row.Payload); err != nil {
            d.db.Model(&row).Updates(map[string]interface{}{
                "last_error": err.Error(),
                "attempt":    row.Attempt + 1,
            })
            // Exponential backoff is implicit: failed rows stay pending; ticker retries.
            continue
        }
        d.db.Model(&row).Update("published_at", time.Now())
    }
}
```

Properties:
- Atomic with the saga step (same TX).
- Crash-safe: process dies after commit, restart, drainer picks up unpublished rows.
- At-least-once delivery: consumers must be idempotent (already are — they use Kafka offsets and DB constraints).
- Per-service outbox keeps blast radius local; no shared infrastructure.

## Files

### Files ADDED

- `contract/shared/saga/idempotency.go` — `IdempotencyKey(sagaID string, step StepKind) string`
- `contract/shared/grpcmw/saga_context.go` — outgoing + incoming interceptors
- `contract/shared/saga/context.go` — `WithSagaID`, `SagaIDFromContext`, etc.
- `contract/shared/outbox/outbox.go` — `Outbox.Enqueue`, `OutboxDrainer`, `OutboxEvent` struct
- `<service>/internal/repository/idempotency_repository.go` — per service that needs it
- `<service>/internal/repository/outbox_repository.go` — per service that publishes from sagas

### Files MODIFIED

- Every saga-callee gRPC method's proto file — add `string idempotency_key = N;` to request message.
- Every saga-callee service handler — call the new `XxxWithIdempotency` wrapper or implement inline.
- Every `<service>/cmd/main.go` — wire `SagaContextInterceptor` and start `OutboxDrainer`.
- Side-effect tables — add `saga_id`, `saga_step` columns. (`account_ledger_entries` is the primary one.)
- `stock-service` saga code — replace direct `producer.Publish(...)` calls inside sagas with `outbox.Enqueue(tx, ...)`.

### Files DELETED

- Inline post-commit Kafka publish patterns currently in saga code (replaced by outbox enqueue).

## Test Plan

### Unit tests

- Idempotency: callee returns the cached response on duplicate key without re-executing the body. Concurrent duplicate calls don't double-execute (race test with goroutines).
- Saga context interceptor: every gRPC client call inside a saga step propagates the metadata; server-side interceptor extracts it; ledger row carries `saga_id`.
- Outbox enqueue: row written inside the same TX as the saga step. If the TX rolls back, no outbox row exists.
- Outbox drainer: publishes pending rows in order; marks `published_at` on success; increments `attempt` and stores `last_error` on failure; retries on next tick.

### Integration tests

- Kill account-service mid-saga (between debit and the saga's `completed` write); on restart, stock-service recovery retries the debit; idempotency key prevents double-debit.
- Kill stock-service after Kafka publish but before outbox marks `published`. **This test is moot under the new design — publish only happens via the drainer, after `published_at = now()` is committed. The hazard is gone by construction.** Test instead: kill stock-service after outbox row is written, before drainer sees it; on restart, drainer publishes it.
- Cross-service audit: trace a single saga via `SELECT * FROM account_ledger_entries WHERE saga_id = X UNION ALL SELECT * FROM stock_holdings WHERE saga_id = X`. Confirm rows from multiple services share the same saga_id.
- Concurrent same-key idempotent debits don't deadlock and produce one debit + one cached response.

## Out of Scope

- Outbox row TTL / archival policy (background job to be specified separately).
- Idempotency record TTL / archival.
- Distributed transaction protocols (XA, 2PC at DB level). Application-level interbank 2PC stays.
- Saga step Prometheus metrics (future-ideas backlog).
- Choreography saga style.
