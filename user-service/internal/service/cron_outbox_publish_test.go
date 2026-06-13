// cron_outbox_publish_test.go
//
// Coverage for the polling/cron/relay surface and the Kafka-publish branches
// that the mocked-nil-producer tests never reach:
//   - OutboxRelay (NewOutboxRelay defaulting, processBatch happy/error/skip,
//     Start + admin-trigger drain),
//   - ActuaryCronService + LimitCronService admin-trigger reset branch,
//   - LimitService template-event publishing,
//   - ActuaryService / BlueprintService best-effort publish error logging.
//
// The crons' 23:59 timer branch is intentionally not exercised (it would
// require waiting until end-of-day or mocking the clock); the admin-trigger
// branch runs the identical reset body.
package service

import (
	"context"
	"testing"
	"time"

	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// cancelledCtx returns a context that's already done so the segmentio writer
// fails fast instead of dialing a (nonexistent) broker.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// newOutboxDB hand-creates the outbox_events table (BLOB payload isn't
// AutoMigrate-friendly on SQLite, mirroring the repository package's helper).
func newOutboxDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE outbox_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		aggregate_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload BLOB NOT NULL,
		created_at DATETIME NOT NULL,
		published_at DATETIME
	)`).Error)
	return db
}

// -----------------------------------------------------------------------------
// OutboxRelay
// -----------------------------------------------------------------------------

func TestNewOutboxRelay_DefaultsTickWhenZero(t *testing.T) {
	repo := repository.NewOutboxRepository(newOutboxDB(t))
	relay := NewOutboxRelay(repo, nil, 0, nilRegistry())
	require.NotNil(t, relay)
	assert.Equal(t, 2*time.Second, relay.tick, "zero tick must default to 2s")
}

func TestOutboxRelay_ProcessBatch_NilProducerMarksPublished(t *testing.T) {
	repo := repository.NewOutboxRepository(newOutboxDB(t))
	require.NoError(t, repo.Insert(&model.OutboxEvent{AggregateID: "a:1", EventType: "user.x", Payload: []byte(`{"k":1}`)}))
	require.NoError(t, repo.Insert(&model.OutboxEvent{AggregateID: "a:2", EventType: "user.y", Payload: []byte(`{"k":2}`)}))

	relay := NewOutboxRelay(repo, nil, time.Second, nilRegistry())
	relay.processBatch(context.Background())

	// With a nil producer the relay still marks rows published.
	remaining, err := repo.ClaimUnpublished(10)
	require.NoError(t, err)
	assert.Empty(t, remaining, "all rows should be marked published")
}

func TestOutboxRelay_ProcessBatch_PublishErrorLeavesRowUnpublished(t *testing.T) {
	repo := repository.NewOutboxRepository(newOutboxDB(t))
	require.NoError(t, repo.Insert(&model.OutboxEvent{AggregateID: "a:1", EventType: "user.x", Payload: []byte(`{}`)}))

	// Real producer + already-cancelled context → PublishRaw errors → the row is
	// skipped (continue) and stays unpublished for the next sweep.
	prod := kafkaprod.NewProducer("localhost:9999")
	defer func() { _ = prod.Close() }()
	relay := NewOutboxRelay(repo, prod, time.Second, nilRegistry())

	relay.processBatch(cancelledCtx())

	remaining, err := repo.ClaimUnpublished(10)
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "publish failure must NOT mark the row published")
}

func TestOutboxRelay_ProcessBatch_ClaimErrorIsHandled(t *testing.T) {
	db := newOutboxDB(t)
	repo := repository.NewOutboxRepository(db)
	// Drop the table so ClaimUnpublished errors; processBatch must log+return.
	require.NoError(t, db.Exec(`DROP TABLE outbox_events`).Error)

	relay := NewOutboxRelay(repo, nil, time.Second, nilRegistry())
	relay.processBatch(context.Background()) // must not panic
}

// cronRunCount reads the registry's in-memory run counter (race-free, unlike
// polling the SQLite handle that the relay goroutine is concurrently writing).
func cronRunCount(t *testing.T, registry *cronreg.Registry, name string) int64 {
	t.Helper()
	info, err := registry.Get(name)
	require.NoError(t, err)
	return info.RunCount
}

func TestOutboxRelay_Start_AdminTriggerDrains(t *testing.T) {
	repo := repository.NewOutboxRepository(newOutboxDB(t))
	require.NoError(t, repo.Insert(&model.OutboxEvent{AggregateID: "a:1", EventType: "user.x", Payload: []byte(`{}`)}))

	registry := cronreg.NewRegistry("test", nil)
	relay := NewOutboxRelay(repo, nil, time.Hour, registry) // long tick → only the trigger fires

	ctx, cancel := context.WithCancel(context.Background())
	relay.Start(ctx)

	require.NoError(t, registry.Trigger("outbox-relay", false, 0))

	// Wait for the run to FINISH via the in-memory run counter — this avoids
	// touching the SQLite handle while the relay goroutine writes to it.
	require.Eventually(t, func() bool {
		return cronRunCount(t, registry, "outbox-relay") >= 1
	}, 2*time.Second, 10*time.Millisecond, "admin trigger should run the relay once")

	// Stop the goroutine, then read the DB exactly once with no concurrent writer.
	cancel()
	rows, err := repo.ClaimUnpublished(10)
	require.NoError(t, err)
	assert.Empty(t, rows, "the triggered run should have drained the outbox")
}

// -----------------------------------------------------------------------------
// ActuaryCronService — admin-trigger reset
// -----------------------------------------------------------------------------

func TestActuaryCronService_AdminTriggerResetsUsedLimits(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ActuaryLimit{}))
	repo := repository.NewActuaryRepository(db)
	require.NoError(t, repo.Create(&model.ActuaryLimit{EmployeeID: 1, Limit: decimal.NewFromInt(1000), UsedLimit: decimal.NewFromInt(250)}))

	registry := cronreg.NewRegistry("test", nil)
	svc := NewActuaryCronService(repo, registry)
	require.NotNil(t, svc)

	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)

	require.NoError(t, registry.Trigger("actuary-daily-limit-reset", false, 0))

	// Wait for the run to finish via the registry counter (race-free), so the
	// test never reads SQLite while the cron goroutine writes to it.
	require.Eventually(t, func() bool {
		return cronRunCount(t, registry, "actuary-daily-limit-reset") >= 1
	}, 2*time.Second, 10*time.Millisecond, "trigger should run the reset once")

	cancel()
	row, gErr := repo.GetByEmployeeID(1)
	require.NoError(t, gErr)
	assert.True(t, row.UsedLimit.IsZero(), "trigger should reset used_limit to zero")
}

// -----------------------------------------------------------------------------
// LimitCronService — admin-trigger reset
// -----------------------------------------------------------------------------

// signalingLimitRepo wraps the package mock and signals every ResetDailyUsedLimits.
type signalingLimitRepo struct {
	mockEmployeeLimitRepo
	reset chan struct{}
}

func (s *signalingLimitRepo) ResetDailyUsedLimits() error {
	select {
	case s.reset <- struct{}{}:
	default:
	}
	return nil
}

func TestLimitCronService_AdminTriggerInvokesReset(t *testing.T) {
	repo := &signalingLimitRepo{
		mockEmployeeLimitRepo: *newMockEmployeeLimitRepo(),
		reset:                 make(chan struct{}, 1),
	}
	registry := cronreg.NewRegistry("test", nil)
	svc := NewLimitCronService(repo, registry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	require.NoError(t, registry.Trigger("employee-daily-limit-reset", false, 0))

	select {
	case <-repo.reset:
		// reset was invoked by the trigger branch
	case <-time.After(2 * time.Second):
		t.Fatal("trigger did not invoke ResetDailyUsedLimits")
	}
}

// -----------------------------------------------------------------------------
// LimitService — template create/update/delete event publishing
// -----------------------------------------------------------------------------

// fakeLimitPublisher captures LimitEventPublisher calls.
type fakeLimitPublisher struct {
	templates []kafkamsg.LimitTemplateMessage
	limits    []kafkamsg.EmployeeLimitsUpdatedMessage
}

func (f *fakeLimitPublisher) PublishEmployeeLimitsUpdated(_ context.Context, msg kafkamsg.EmployeeLimitsUpdatedMessage) error {
	f.limits = append(f.limits, msg)
	return nil
}

func (f *fakeLimitPublisher) PublishLimitTemplate(_ context.Context, msg kafkamsg.LimitTemplateMessage) error {
	f.templates = append(f.templates, msg)
	return nil
}

func TestLimitService_TemplateCRUD_PublishesEvents(t *testing.T) {
	pub := &fakeLimitPublisher{}
	svc := NewLimitService(newMockEmployeeLimitRepo(), newMockLimitTemplateRepo(), newMockHierarchyEmpRepo(), pub)

	created, err := svc.CreateTemplate(context.Background(), model.LimitTemplate{
		Name: "Custom", MaxLoanApprovalAmount: decimal.NewFromInt(1000),
	})
	require.NoError(t, err)

	created.Description = "updated"
	_, err = svc.UpdateTemplate(context.Background(), *created)
	require.NoError(t, err)

	require.NoError(t, svc.DeleteTemplate(context.Background(), created.ID))

	require.Len(t, pub.templates, 3)
	assert.Equal(t, "created", pub.templates[0].Action)
	assert.Equal(t, "updated", pub.templates[1].Action)
	assert.Equal(t, "deleted", pub.templates[2].Action)
	assert.Equal(t, created.ID, pub.templates[2].TemplateID)
}

// -----------------------------------------------------------------------------
// ActuaryService.publishActuaryEvent — best-effort publish error is logged
// -----------------------------------------------------------------------------

func TestActuaryService_PublishActuaryEvent_ErrorIsLoggedNotPropagated(t *testing.T) {
	actuaryRepo := newMockActuaryRepo()
	require.NoError(t, actuaryRepo.Create(&model.ActuaryLimit{EmployeeID: 3, UsedLimit: decimal.NewFromInt(5)}))
	limit, _ := actuaryRepo.GetByEmployeeID(3)

	prod := kafkaprod.NewProducer("localhost:9999")
	defer func() { _ = prod.Close() }()
	svc := NewActuaryService(actuaryRepo, newMockActuaryEmpRepo(), prod)

	// UpdateUsedLimit ends in publishActuaryEvent; the cancelled context makes the
	// publish fail, but the operation itself must still succeed.
	got, err := svc.UpdateUsedLimit(cancelledCtx(), limit.ID, decimal.NewFromInt(2))
	require.NoError(t, err)
	assert.True(t, got.UsedLimit.Equal(decimal.NewFromInt(7)))
}

// -----------------------------------------------------------------------------
// BlueprintService.publishEvent — best-effort publish error is logged
// -----------------------------------------------------------------------------

func TestBlueprintService_PublishEvent_ErrorIsLoggedNotPropagated(t *testing.T) {
	prod := kafkaprod.NewProducer("localhost:9999")
	defer func() { _ = prod.Close() }()
	svc := NewBlueprintService(newMockBlueprintRepo(), nil, nil, prod, nil)

	created, err := svc.CreateBlueprint(cancelledCtx(), model.LimitBlueprint{
		Name: "P", Type: model.BlueprintTypeEmployee,
		Values: mustMarshal(model.EmployeeBlueprintValues{
			MaxLoanApprovalAmount: "1", MaxSingleTransaction: "1",
			MaxDailyTransaction: "1", MaxClientDailyLimit: "1", MaxClientMonthlyLimit: "1",
		}),
	})
	require.NoError(t, err, "publish failure must not fail CreateBlueprint")
	assert.NotZero(t, created.ID)
}
