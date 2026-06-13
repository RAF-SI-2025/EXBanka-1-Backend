# SP-1: Client-Profile Replica (card-service slice) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate card-service's synchronous `client.GetClient` hot-path reads by maintaining a local `client_replica` table fed by enriched `client.created`/`client.updated` Kafka events, with a lazy gRPC fallback on miss.

**Architecture:** Enrich the existing `ClientCreatedMessage` to carry full client state + a monotonic `version` (client-service already publishes it on create and update). Build card-service's first Kafka consumer (copying auth-service's consumer pattern) to upsert a version-guarded `client_replica` row per event. Swap the three `GetClient` call sites to read the replica, falling back to one sync `GetClient` + backfill on a miss. This is the reference slice for the whole denormalization program (spec: `docs/superpowers/specs/2026-06-08-service-decoupling-program-design.md`); SP-2..SP-6 reuse this exact pattern.

**Tech Stack:** Go, GORM (Postgres + SQLite for tests), segmentio/kafka-go, gRPC/protobuf, decimal. Module paths: `github.com/exbanka/contract`, `github.com/exbanka/client-service`, `github.com/exbanka/card-service`.

**Scope (this plan):** contract event enrichment + client-service publisher + card-service consumer/replica/read-swap. The other SP-1 consumers (account, credit, interbank, stock) are separate follow-on plans that copy this slice.

**Out of scope:** client `status` (lives in auth-service, not client-service — comes from a separate auth event later); no REST route changes (so no Swagger / `REST_API_v3.md` edits); no new cache.

---

## File structure

| File | Responsibility | Action |
|---|---|---|
| `contract/kafka/messages.go` | `ClientCreatedMessage` gains `JMBG`, `Version` | Modify |
| `contract/kafka/messages_test.go` | round-trip test for the enriched message | Modify/Create |
| `client-service/internal/service/client_service.go` | populate `JMBG`+`Version` on create (`:102`) and update (`:202`) publishes | Modify |
| `client-service/internal/service/client_service_test.go` | assert published message carries `JMBG`+`Version` | Modify |
| `card-service/internal/model/client_replica.go` | `ClientReplica` GORM model | Create |
| `card-service/internal/repository/client_replica_repository.go` | version-guarded upsert + get-by-id | Create |
| `card-service/internal/repository/client_replica_repository_test.go` | upsert/monotonicity/get tests | Create |
| `card-service/internal/consumer/client_replica_consumer.go` | consume `client.created`/`client.updated` → upsert replica | Create |
| `card-service/internal/consumer/client_replica_consumer_test.go` | event → repo upsert test | Create |
| `card-service/internal/handler/grpc_handler.go` | `resolveClientEmail` helper; swap 3 `GetClient` sites | Modify |
| `card-service/internal/handler/grpc_handler_test.go` | replica-hit (no gRPC) + miss-fallback-backfill tests | Modify/Create |
| `card-service/cmd/main.go` | EnsureTopics + wire repo/consumer + Close | Modify |
| `test-app/workflows/client_replica_test.go` | integration: event populates replica; card op uses it | Create |
| `VERSION`, `api-gateway/internal/version/version.go` | bump (MINOR) | Modify |
| `Specification.md` | §18 entity `ClientReplica`; §19 enriched `ClientCreatedMessage` | Modify |

---

## Task 1: Enrich `ClientCreatedMessage` (contract)

**Files:**
- Modify: `contract/kafka/messages.go:107-112`
- Test: `contract/kafka/messages_test.go`

- [ ] **Step 1: Write the failing test**

Add to `contract/kafka/messages_test.go` (create the file if absent, `package kafka`):

```go
package kafka

import (
	"encoding/json"
	"testing"
)

func TestClientCreatedMessage_CarriesJMBGAndVersion(t *testing.T) {
	in := ClientCreatedMessage{
		ClientID: 7, Email: "a@b.com", FirstName: "Ana", LastName: "Anic",
		JMBG: "1234567890123", Version: 5,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ClientCreatedMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.JMBG != "1234567890123" || out.Version != 5 {
		t.Fatalf("lost fields: %+v", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contract && go test ./kafka/ -run TestClientCreatedMessage_CarriesJMBGAndVersion -v`
Expected: FAIL — `in.JMBG`/`in.Version` undefined (compile error).

- [ ] **Step 3: Add the fields**

In `contract/kafka/messages.go`, change `ClientCreatedMessage` to:

```go
type ClientCreatedMessage struct {
	ClientID  uint64 `json:"client_id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	JMBG      string `json:"jmbg,omitempty"`    // added: full-state for replica
	Version   int64  `json:"version,omitempty"` // added: monotonic ordering guard
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contract && go test ./kafka/ -run TestClientCreatedMessage_CarriesJMBGAndVersion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go contract/kafka/messages_test.go
git commit -m "feat(contract): enrich ClientCreatedMessage with JMBG + Version for replicas"
```

---

## Task 2: Publish full client state from client-service

**Files:**
- Modify: `client-service/internal/service/client_service.go:102-107` (create) and `:202-207` (update)
- Test: `client-service/internal/service/client_service_test.go`

- [ ] **Step 1: Write the failing test**

Add to `client-service/internal/service/client_service_test.go` (use the existing mock producer in that package; if the mock records published messages, assert on the last `ClientCreatedMessage`):

```go
func TestCreateClient_PublishesJMBGAndVersion(t *testing.T) {
	svc, mockProd := newTestClientService(t) // existing helper; returns svc + recording mock
	c, err := svc.CreateClient(context.Background(), validCreateInput(t)) // existing helper input w/ JMBG
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	msg := mockProd.LastClientCreated() // recording mock accessor
	if msg.JMBG != c.JMBG || msg.Version != c.Version {
		t.Fatalf("published msg missing full state: %+v (client jmbg=%s ver=%d)", msg, c.JMBG, c.Version)
	}
}
```

> If `newTestClientService`/`LastClientCreated` helpers don't exist, add a minimal recording mock that implements the producer interface and stores the last `ClientCreatedMessage`. Follow the existing producer-mock style in this test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd client-service && go test ./internal/service/ -run TestCreateClient_PublishesJMBGAndVersion -v`
Expected: FAIL — published `msg.JMBG`/`msg.Version` are empty/zero.

- [ ] **Step 3: Populate the fields at both publish sites**

`client-service/internal/service/client_service.go` create site (~:102):

```go
		if err := s.producer.PublishClientCreated(ctx, kafkamsg.ClientCreatedMessage{
			ClientID:  client.ID,
			Email:     client.Email,
			FirstName: client.FirstName,
			LastName:  client.LastName,
			JMBG:      client.JMBG,
			Version:   client.Version,
		}); err != nil {
```

Update site (~:202) — same three added lines (`JMBG`, `Version`) inside the `PublishClientUpdated(...)` `ClientCreatedMessage{...}` literal.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd client-service && go test ./internal/service/ -run TestCreateClient_PublishesJMBGAndVersion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add client-service/internal/service/client_service.go client-service/internal/service/client_service_test.go
git commit -m "feat(client): publish full client state (jmbg+version) on create/update"
```

---

## Task 3: `ClientReplica` model in card-service

**Files:**
- Create: `card-service/internal/model/client_replica.go`
- Modify: `card-service/cmd/main.go:39` (AutoMigrate list)
- Test: `card-service/internal/model/client_replica_test.go`

- [ ] **Step 1: Write the failing test**

`card-service/internal/model/client_replica_test.go`:

```go
package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestClientReplica_Migrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&ClientReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	r := ClientReplica{ID: 1, Email: "a@b.com", FirstName: "Ana", LastName: "Anic", JMBG: "1234567890123", Version: 1}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got ClientReplica
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Email != "a@b.com" {
		t.Fatalf("bad read: %+v", got)
	}
}
```

> Use the same SQLite driver import the other card-service `_test.go` files use (check an existing model/repository test; it may be `github.com/glebarez/sqlite` or `gorm.io/driver/sqlite`). Match it.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd card-service && go test ./internal/model/ -run TestClientReplica_Migrate -v`
Expected: FAIL — `ClientReplica` undefined.

- [ ] **Step 3: Create the model**

`card-service/internal/model/client_replica.go`:

```go
package model

import "time"

// ClientReplica is a local read-model of a client's profile, fed by
// client.created / client.updated Kafka events (SP-1). It is NOT authoritative —
// client-service owns the client. Used to avoid synchronous GetClient hot-path reads.
type ClientReplica struct {
	ID        uint64    `gorm:"primaryKey"` // == client-service Client.ID (no autoincrement)
	Email     string    `gorm:"not null"`
	FirstName string    `gorm:"not null"`
	LastName  string    `gorm:"not null"`
	JMBG      string    `gorm:"size:13"`
	Version   int64     `gorm:"not null;default:0"` // source Client.Version; ordering guard
	UpdatedAt time.Time
}
```

- [ ] **Step 4: Add to AutoMigrate**

`card-service/cmd/main.go:39` — append `&model.ClientReplica{}` to the `db.AutoMigrate(...)` argument list.

- [ ] **Step 5: Run test + build**

Run: `cd card-service && go test ./internal/model/ -run TestClientReplica_Migrate -v && go build ./...`
Expected: PASS, build OK.

- [ ] **Step 6: Commit**

```bash
git add card-service/internal/model/client_replica.go card-service/internal/model/client_replica_test.go card-service/cmd/main.go
git commit -m "feat(card): add ClientReplica read-model + automigrate"
```

---

## Task 4: `ClientReplicaRepository` (version-guarded upsert + get)

**Files:**
- Create: `card-service/internal/repository/client_replica_repository.go`
- Test: `card-service/internal/repository/client_replica_repository_test.go`

- [ ] **Step 1: Write the failing test**

`card-service/internal/repository/client_replica_repository_test.go`:

```go
package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite" // match existing repo tests' driver
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
)

func newReplicaDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.ClientReplica{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestClientReplicaRepo_UpsertAndGet(t *testing.T) {
	repo := NewClientReplicaRepository(newReplicaDB(t))
	ctx := context.Background()
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v1@b.com", FirstName: "A", LastName: "B", Version: 1}); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	got, err := repo.GetByID(ctx, 1)
	if err != nil || got.Email != "v1@b.com" {
		t.Fatalf("get1: %+v err=%v", got, err)
	}
	// Newer version applies.
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "v2@b.com", Version: 2}); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	got, _ = repo.GetByID(ctx, 1)
	if got.Email != "v2@b.com" {
		t.Fatalf("expected v2, got %+v", got)
	}
	// Older/equal version is ignored (monotonic).
	if err := repo.Upsert(ctx, model.ClientReplica{ID: 1, Email: "stale@b.com", Version: 1}); err != nil {
		t.Fatalf("upsert-stale: %v", err)
	}
	got, _ = repo.GetByID(ctx, 1)
	if got.Email != "v2@b.com" {
		t.Fatalf("stale event overwrote newer state: %+v", got)
	}
}

func TestClientReplicaRepo_GetMissing(t *testing.T) {
	repo := NewClientReplicaRepository(newReplicaDB(t))
	_, err := repo.GetByID(context.Background(), 999)
	if err == nil {
		t.Fatalf("expected error for missing replica")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd card-service && go test ./internal/repository/ -run TestClientReplicaRepo -v`
Expected: FAIL — `NewClientReplicaRepository` undefined.

- [ ] **Step 3: Implement the repository**

`card-service/internal/repository/client_replica_repository.go`:

```go
package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/card-service/internal/model"
)

// ErrReplicaNotFound is returned when a client replica row is absent.
var ErrReplicaNotFound = errors.New("client replica not found")

type ClientReplicaRepository struct{ db *gorm.DB }

func NewClientReplicaRepository(db *gorm.DB) *ClientReplicaRepository {
	return &ClientReplicaRepository{db: db}
}

// Upsert applies an event-sourced client state, but ONLY if its Version is
// strictly greater than the stored row's Version (monotonic; tolerates
// out-of-order / duplicate Kafka delivery). A first insert always wins.
func (r *ClientReplicaRepository) Upsert(ctx context.Context, in model.ClientReplica) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.ClientReplica
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, in.ID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return tx.Create(&in).Error
		case err != nil:
			return err
		}
		if in.Version <= existing.Version {
			return nil // stale or duplicate; ignore
		}
		return tx.Model(&existing).Select("Email", "FirstName", "LastName", "JMBG", "Version").Updates(&in).Error
	})
}

func (r *ClientReplicaRepository) GetByID(ctx context.Context, id uint64) (model.ClientReplica, error) {
	var c model.ClientReplica
	err := r.db.WithContext(ctx).First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.ClientReplica{}, ErrReplicaNotFound
	}
	return c, err
}
```

> Note: `ClientReplica` has no `Version int64` GORM optimistic-lock `BeforeUpdate` hook, so `Updates` is safe here. Do not add a hook — the consumer is the single writer and ordering is enforced explicitly by the `in.Version <= existing.Version` guard.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd card-service && go test ./internal/repository/ -run TestClientReplicaRepo -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add card-service/internal/repository/client_replica_repository.go card-service/internal/repository/client_replica_repository_test.go
git commit -m "feat(card): version-guarded ClientReplica repository"
```

---

## Task 5: Client replica consumer

**Files:**
- Create: `card-service/internal/consumer/client_replica_consumer.go`
- Test: `card-service/internal/consumer/client_replica_consumer_test.go`

- [ ] **Step 1: Write the failing test**

`card-service/internal/consumer/client_replica_consumer_test.go`:

```go
package consumer

import (
	"context"
	"encoding/json"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/card-service/internal/model"
)

type fakeReplicaRepo struct{ last model.ClientReplica; calls int }

func (f *fakeReplicaRepo) Upsert(_ context.Context, in model.ClientReplica) error {
	f.last = in
	f.calls++
	return nil
}

func TestHandleClientEvent_UpsertsReplica(t *testing.T) {
	repo := &fakeReplicaRepo{}
	c := &ClientReplicaConsumer{repo: repo}
	payload, _ := json.Marshal(kafkamsg.ClientCreatedMessage{
		ClientID: 42, Email: "x@y.com", FirstName: "X", LastName: "Y", JMBG: "9999999999999", Version: 3,
	})
	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if repo.calls != 1 || repo.last.ID != 42 || repo.last.Email != "x@y.com" || repo.last.Version != 3 {
		t.Fatalf("bad upsert: %+v calls=%d", repo.last, repo.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd card-service && go test ./internal/consumer/ -run TestHandleClientEvent_UpsertsReplica -v`
Expected: FAIL — `ClientReplicaConsumer`/`handle` undefined.

- [ ] **Step 3: Implement the consumer**

`card-service/internal/consumer/client_replica_consumer.go` (mirrors `auth-service/internal/consumer/client_consumer.go`):

```go
package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	"github.com/exbanka/card-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// replicaUpserter is the subset of ClientReplicaRepository the consumer needs.
type replicaUpserter interface {
	Upsert(ctx context.Context, in model.ClientReplica) error
}

// ClientReplicaConsumer maintains card-service's local client_replica from
// client.created / client.updated events (SP-1).
type ClientReplicaConsumer struct {
	reader *kafka.Reader
	repo   replicaUpserter
}

func NewClientReplicaConsumer(brokers string, repo replicaUpserter) *ClientReplicaConsumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{brokers},
		// One reader, both topics: GroupTopics keeps a single consumer group.
		GroupTopics: []string{kafkamsg.TopicClientCreated, kafkamsg.TopicClientUpdated},
		GroupID:     "card-service-client-replica",
	})
	return &ClientReplicaConsumer{reader: r, repo: repo}
}

func (c *ClientReplicaConsumer) handle(ctx context.Context, value []byte) error {
	var evt kafkamsg.ClientCreatedMessage
	if err := json.Unmarshal(value, &evt); err != nil {
		return err
	}
	return c.repo.Upsert(ctx, model.ClientReplica{
		ID:        evt.ClientID,
		Email:     evt.Email,
		FirstName: evt.FirstName,
		LastName:  evt.LastName,
		JMBG:      evt.JMBG,
		Version:   evt.Version,
	})
}

func (c *ClientReplicaConsumer) Start(ctx context.Context) {
	go func() {
		for {
			msg, err := c.reader.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("client-replica consumer read error: %v", err)
				continue
			}
			if err := c.handle(ctx, msg.Value); err != nil {
				log.Printf("client-replica consumer handle error (offset %d): %v", msg.Offset, err)
			}
		}
	}()
}

func (c *ClientReplicaConsumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("client-replica consumer close error: %v", err)
	}
}
```

> A unit-version mismatch in the consumer is non-fatal: if `evt.Version == 0` (event published before Task 2 rolled out), the first insert still wins and later versioned events upgrade it. The repo's guard handles ordering.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd card-service && go test ./internal/consumer/ -run TestHandleClientEvent_UpsertsReplica -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add card-service/internal/consumer/client_replica_consumer.go card-service/internal/consumer/client_replica_consumer_test.go
git commit -m "feat(card): client.created/updated consumer feeding ClientReplica"
```

---

## Task 6: Wire consumer + topics in main

**Files:**
- Modify: `card-service/cmd/main.go` (EnsureTopics ~:49, construction ~:88, lifecycle)

- [ ] **Step 1: Add the two topics to EnsureTopics**

In the `shared.EnsureTopics(cfg.KafkaBrokers, ...)` call, add:

```go
		"client.created",
		"client.updated",
```

- [ ] **Step 2: Construct repo + consumer and start it**

After `cardRepo := repository.NewCardRepository(db)` (~:83), add:

```go
	clientReplicaRepo := repository.NewClientReplicaRepository(db)
```

After the metrics/cron setup but before `RunGRPCServer`, add (reuse the existing `cronCtx` for cancellation):

```go
	clientReplicaConsumer := consumer.NewClientReplicaConsumer(cfg.KafkaBrokers, clientReplicaRepo)
	clientReplicaConsumer.Start(cronCtx)
	defer clientReplicaConsumer.Close()
```

- [ ] **Step 3: Inject the replica repo into the handler**

Change the handler constructor call (~:91) to pass `clientReplicaRepo` (constructor signature updated in Task 7):

```go
	grpcHandler := handler.NewCardGRPCHandler(cardService, producer, clientClient, changelogSvc, clientReplicaRepo)
```

Add the `consumer` import to `card-service/cmd/main.go`:
`"github.com/exbanka/card-service/internal/consumer"`.

- [ ] **Step 4: Build (expect handler-signature failure until Task 7)**

Run: `cd card-service && go build ./... 2>&1 | head`
Expected: FAIL referencing `NewCardGRPCHandler` arity — resolved in Task 7. (Do not commit yet; commit at the end of Task 7 so the tree builds.)

---

## Task 7: Swap `GetClient` → replica-with-fallback in the card handler

**Files:**
- Modify: `card-service/internal/handler/grpc_handler.go` (struct, constructor, 3 sites at ~:143/:193/:243, add helper)
- Test: `card-service/internal/handler/grpc_handler_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `card-service/internal/handler/grpc_handler_test.go`:

```go
func TestResolveClientEmail_ReplicaHit_NoGRPC(t *testing.T) {
	repo := &stubReplicaRepo{email: "cached@b.com"}                 // GetByID returns a row
	gc := &countingClientClient{}                                  // GetClient must NOT be called
	h := &CardGRPCHandler{clientClient: gc, clientReplica: repo}
	got := h.resolveClientEmail(context.Background(), 1)
	if got != "cached@b.com" {
		t.Fatalf("want cached@b.com got %q", got)
	}
	if gc.calls != 0 {
		t.Fatalf("expected no gRPC fallback, got %d calls", gc.calls)
	}
}

func TestResolveClientEmail_Miss_FallsBackAndBackfills(t *testing.T) {
	repo := &stubReplicaRepo{missing: true}                        // GetByID -> ErrReplicaNotFound
	gc := &countingClientClient{email: "live@b.com"}              // GetClient returns live row
	h := &CardGRPCHandler{clientClient: gc, clientReplica: repo}
	got := h.resolveClientEmail(context.Background(), 1)
	if got != "live@b.com" {
		t.Fatalf("want live@b.com got %q", got)
	}
	if gc.calls != 1 {
		t.Fatalf("expected exactly one gRPC fallback, got %d", gc.calls)
	}
	if repo.upserts != 1 {
		t.Fatalf("expected backfill upsert, got %d", repo.upserts)
	}
}
```

Add the stubs (in the test file): `stubReplicaRepo` implements `GetByID`/`Upsert`; `countingClientClient` implements the `clientpb.ClientServiceClient` subset (`GetClient`) and counts calls returning `&clientpb.ClientResponse{Email: ...}`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd card-service && go test ./internal/handler/ -run TestResolveClientEmail -v`
Expected: FAIL — `clientReplica` field / `resolveClientEmail` undefined.

- [ ] **Step 3: Add the field, constructor param, helper, and swap the 3 sites**

In `card-service/internal/handler/grpc_handler.go`:

1) Add a narrow interface + struct field:

```go
// clientReplicaReader is the read-model the handler consults before falling back to gRPC.
type clientReplicaReader interface {
	GetByID(ctx context.Context, id uint64) (model.ClientReplica, error)
	Upsert(ctx context.Context, in model.ClientReplica) error
}
```

Add `clientReplica clientReplicaReader` to the `CardGRPCHandler` struct, and a trailing param to `NewCardGRPCHandler(...)` that assigns it.

2) Add the helper:

```go
// resolveClientEmail returns the client's email from the local replica, falling
// back to a single synchronous GetClient on a miss and backfilling the replica
// (SP-1 hybrid lazy fallback). Returns "" only if both sources fail.
func (h *CardGRPCHandler) resolveClientEmail(ctx context.Context, ownerID uint64) string {
	if h.clientReplica != nil {
		if rep, err := h.clientReplica.GetByID(ctx, ownerID); err == nil {
			return rep.Email
		}
	}
	if h.clientClient == nil {
		return ""
	}
	resp, err := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: ownerID})
	if err != nil {
		log.Printf("CardGRPCHandler: client resolve fallback failed for %d: %v", ownerID, err)
		return ""
	}
	if h.clientReplica != nil {
		_ = h.clientReplica.Upsert(ctx, model.ClientReplica{
			ID: ownerID, Email: resp.Email, FirstName: resp.FirstName, LastName: resp.LastName,
			Version: resp.Version, // ClientResponse must expose Version; if not, leave 0 (first insert wins)
		})
	}
	return resp.Email
}
```

3) Replace each of the three `GetClient` blocks (~:143/:193/:243). Each currently does:
```go
clientResp, clientErr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: card.OwnerID})
if clientErr == nil { ... To: clientResp.Email ... }
```
Replace with:
```go
if email := h.resolveClientEmail(ctx, card.OwnerID); email != "" {
	emailErr := h.producer.SendEmail(ctx, kafkamsg.SendEmailMessage{
		To:        email,
		EmailType: kafkamsg.EmailType<SameAsBefore>,
		Data:      map[string]string{ /* unchanged keys for this site */ },
	})
	if emailErr != nil {
		log.Printf("CardGRPCHandler: failed to send email for card %d: %v", card.ID, emailErr)
	}
}
```
Keep each site's existing `EmailType` and `Data` map exactly as they were.

> If `clientpb.ClientResponse` has no `Version` field, the backfill upsert sets `Version: 0`; that's correct — a later versioned event overwrites it via the repo guard. (Optionally add `Version` to the `GetClient` proto response in a follow-up; not required for this slice.)

Ensure imports include `"github.com/exbanka/card-service/internal/model"`.

- [ ] **Step 4: Run handler tests + build**

Run: `cd card-service && go test ./internal/handler/ -run TestResolveClientEmail -v && go build ./...`
Expected: PASS + build OK (Task 6 wiring now compiles).

- [ ] **Step 5: Run the whole card-service + contract + client-service test suites**

Run:
```bash
cd contract && CGO_ENABLED=1 go test ./... -count=1
cd ../client-service && CGO_ENABLED=1 go test ./... -count=1
cd ../card-service && CGO_ENABLED=1 go test ./... -count=1
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add card-service/cmd/main.go card-service/internal/handler/grpc_handler.go card-service/internal/handler/grpc_handler_test.go
git commit -m "feat(card): read client email from replica with gRPC fallback + backfill; wire consumer"
```

---

## Task 8: Integration test (test-app)

**Files:**
- Create: `test-app/workflows/client_replica_test.go`

- [ ] **Step 1: Write the integration test**

Use the shared helpers in `test-app/workflows/helpers_test.go` (client creation, Kafka wait helpers). The test must assert *spec behavior*, not just status codes:

```go
//go:build integration

package workflows

import "testing"

func TestSP1_ClientReplica_PopulatedFromEventAndUsedByCard(t *testing.T) {
	// 1. Create a client via the gateway (publishes client.created with jmbg+version).
	client := createTestClient(t) // existing helper

	// 2. Wait until card-service's replica reflects it: trigger a card op whose
	//    notification path resolves the client email, and assert it succeeds
	//    AND that no error is logged about a failed client fetch.
	card := createCardForClient(t, client.ID) // existing helper
	blockCardAndExpectEmail(t, card.ID, client.Email) // helper: asserts notification.send-email To == client.Email

	// 3. Update the client's email (publishes client.updated, higher version),
	//    wait for propagation, and assert the new email is used.
	newEmail := updateClientEmail(t, client.ID)
	blockCardAndExpectEmail(t, card.ID, newEmail)
}
```

> If `blockCardAndExpectEmail`/`updateClientEmail` helpers don't exist, add them to `helpers_test.go` following the existing Kafka-scan helper pattern (never inline Kafka scanning — per the testing requirement). Allow a bounded wait (poll up to ~5s) for eventual replica propagation.

- [ ] **Step 2: Run the integration suite**

Run the integration workflow suite the way the repo documents it (see `docs/superpowers/specs/2026-04-04-comprehensive-testing-design.md`), e.g.:
`cd test-app && go test -tags=integration ./workflows/ -run TestSP1_ClientReplica -v`
Expected: PASS (requires docker infra up).

- [ ] **Step 3: Commit**

```bash
git add test-app/workflows/client_replica_test.go test-app/workflows/helpers_test.go
git commit -m "test(integration): SP-1 client replica populated by events and used by card-service"
```

---

## Task 9: Docs, version bump, full CI

**Files:**
- Modify: `Specification.md`, `VERSION`, `api-gateway/internal/version/version.go`

- [ ] **Step 1: Update Specification.md**

- §18 (entities): add `ClientReplica` (card_db read-model: id, email, first_name, last_name, jmbg, version, updated_at — fed by events, non-authoritative).
- §19 (Kafka): note `ClientCreatedMessage` now carries `jmbg` + `version` and is consumed by card-service's `card-service-client-replica` group on `client.created`/`client.updated`.
- No new REST route, gRPC service, permission, or enum → those sections unchanged.

- [ ] **Step 2: Bump VERSION (MINOR — new backward-compatible feature)**

```bash
cd "<repo root>"
# e.g. 2.16.21 -> 2.17.0
printf '2.17.0' > VERSION
```
Edit `api-gateway/internal/version/version.go` `var Version = "2.17.0"` to match.

- [ ] **Step 3: Run the full CI pipeline locally and make it green**

Run: `make ci`
Expected: all five jobs pass (build, unit tests, lint, gofmt clean, go mod tidy no-diff). Fix anything it surfaces — including repo-wide `gofmt` and `go mod tidy` in `contract`, `client-service`, `card-service`, `test-app`.

- [ ] **Step 4: Commit**

```bash
git add Specification.md VERSION api-gateway/internal/version/version.go
git commit -m "docs+chore: document SP-1 client replica; bump VERSION 2.16.21->2.17.0"
```

---

## Self-review notes (addressed)

- **Spec coverage:** implements SP-1's shared pattern (enrich event → replica → consumer → read-with-fallback) for the card-service consumer; the other four SP-1 consumers are explicitly deferred to copy-plans. Cache verdict honored (no new cache). `status` correctly excluded (auth-owned).
- **Type consistency:** `ClientReplica{ID,Email,FirstName,LastName,JMBG,Version,UpdatedAt}` used identically in model, repo, consumer, handler; repo methods `Upsert`/`GetByID`; consumer field `repo`; handler field `clientReplica` (interface `clientReplicaReader`); message fields `JMBG`/`Version` (`json:"jmbg"/"version"`).
- **Ordering/idempotency:** enforced in `Upsert` via `in.Version <= existing.Version` guard inside a `FOR UPDATE` tx — duplicate/out-of-order events are safe.
- **Rollout safety:** events predating Task 2 carry `Version 0`; first insert wins, later versioned events upgrade — no break. The handler keeps the gRPC client as the miss/fallback path, so the feature is safe before the replica warms.
