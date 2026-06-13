package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/account-service/internal/model"
	kafkamsg "github.com/exbanka/contract/kafka"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakePolicyRepo struct {
	stored   *model.ClientLimitPolicy
	upsertFn func(model.ClientLimitPolicy) (bool, error)
	getFn    func(uint64) (model.ClientLimitPolicy, error)
}

func (f *fakePolicyRepo) Upsert(_ context.Context, in model.ClientLimitPolicy) (bool, error) {
	if f.upsertFn != nil {
		return f.upsertFn(in)
	}
	f.stored = &in
	return true, nil
}

func (f *fakePolicyRepo) GetByClientID(_ context.Context, id uint64) (model.ClientLimitPolicy, error) {
	if f.getFn != nil {
		return f.getFn(id)
	}
	if f.stored != nil && f.stored.ClientID == id {
		return *f.stored, nil
	}
	return model.ClientLimitPolicy{}, errors.New("not found")
}

type fakeApplier struct {
	calls         int
	failCount     int
	err           error
	lastClientID  uint64
	lastDaily     decimal.Decimal
	lastMonthly   decimal.Decimal
	lastChangedBy int64
}

func (f *fakeApplier) ApplyClientLimitPolicy(_ context.Context, clientID uint64, daily, monthly decimal.Decimal, changedBy int64) error {
	f.calls++
	f.lastClientID = clientID
	f.lastDaily = daily
	f.lastMonthly = monthly
	f.lastChangedBy = changedBy
	if f.calls <= f.failCount {
		return f.err
	}
	return nil
}

func newTestConsumer(repo policyUpserter, applier limitApplier) *ClientLimitConsumer {
	return &ClientLimitConsumer{repo: repo, applier: applier, backoff: defaultBackoff}
}

func marshalEvent(t *testing.T, evt kafkamsg.ClientLimitsUpdatedMessage) []byte {
	t.Helper()
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandle_NewerEvent_UpsertsAndApplies(t *testing.T) {
	repo := &fakePolicyRepo{}
	applier := &fakeApplier{}
	c := newTestConsumer(repo, applier)

	payload := marshalEvent(t, kafkamsg.ClientLimitsUpdatedMessage{
		ClientID:      42,
		SetByEmployee: 7,
		Action:        "set",
		DailyLimit:    "1000.0000",
		MonthlyLimit:  "10000.0000",
		Version:       3,
	})

	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if applier.calls != 1 {
		t.Fatalf("expected 1 applier call, got %d", applier.calls)
	}
	if applier.lastClientID != 42 {
		t.Fatalf("expected clientID=42, got %d", applier.lastClientID)
	}
	if !applier.lastDaily.Equal(decimal.RequireFromString("1000.0000")) {
		t.Fatalf("bad daily: %s", applier.lastDaily)
	}
	if !applier.lastMonthly.Equal(decimal.RequireFromString("10000.0000")) {
		t.Fatalf("bad monthly: %s", applier.lastMonthly)
	}
	if applier.lastChangedBy != 7 {
		t.Fatalf("expected changedBy=7, got %d", applier.lastChangedBy)
	}
}

func TestHandle_StaleEvent_Skips(t *testing.T) {
	// Upsert returns applied=false; GetByClientID returns stored version > evt.Version → skip.
	storedPolicy := model.ClientLimitPolicy{ClientID: 10, DailyLimit: decimal.NewFromInt(999), Version: 5}
	repo := &fakePolicyRepo{
		upsertFn: func(_ model.ClientLimitPolicy) (bool, error) { return false, nil },
		getFn:    func(_ uint64) (model.ClientLimitPolicy, error) { return storedPolicy, nil },
	}
	applier := &fakeApplier{}
	c := newTestConsumer(repo, applier)

	payload := marshalEvent(t, kafkamsg.ClientLimitsUpdatedMessage{
		ClientID: 10, DailyLimit: "500.0000", Version: 3, // stale: version 3 < stored 5
	})

	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if applier.calls != 0 {
		t.Fatalf("stale event must not call applier, got %d calls", applier.calls)
	}
}

func TestHandle_DuplicateCurrentVersion_Reapplies(t *testing.T) {
	// Upsert returns applied=false; GetByClientID returns same version as evt → re-apply.
	storedPolicy := model.ClientLimitPolicy{ClientID: 20, DailyLimit: decimal.NewFromInt(1000), Version: 4}
	repo := &fakePolicyRepo{
		upsertFn: func(_ model.ClientLimitPolicy) (bool, error) { return false, nil },
		getFn:    func(_ uint64) (model.ClientLimitPolicy, error) { return storedPolicy, nil },
	}
	applier := &fakeApplier{}
	c := newTestConsumer(repo, applier)

	payload := marshalEvent(t, kafkamsg.ClientLimitsUpdatedMessage{
		ClientID: 20, SetByEmployee: 3, DailyLimit: "1000.0000", MonthlyLimit: "5000.0000", Version: 4,
	})

	if err := c.handle(context.Background(), payload); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if applier.calls != 1 {
		t.Fatalf("duplicate current version must re-apply, got %d calls", applier.calls)
	}
}

func TestHandle_BadJSON(t *testing.T) {
	repo := &fakePolicyRepo{}
	applier := &fakeApplier{}
	c := newTestConsumer(repo, applier)

	err := c.handle(context.Background(), []byte("{bad json"))
	if err == nil {
		t.Fatalf("expected error on malformed json")
	}
	if !errors.Is(err, errMalformed) {
		t.Fatalf("expected errMalformed, got %v", err)
	}
	if applier.calls != 0 {
		t.Fatalf("applier must not be called on bad json, got %d", applier.calls)
	}
	if repo.stored != nil {
		t.Fatalf("repo must not be called on bad json")
	}
}

func TestHandleWithRetry_RetriesTransientApplyError(t *testing.T) {
	// applier fails twice then succeeds on 3rd attempt.
	repo := &fakePolicyRepo{}
	applier := &fakeApplier{err: context.DeadlineExceeded, failCount: 2}
	c := &ClientLimitConsumer{repo: repo, applier: applier, backoff: []time.Duration{0, 0}}

	payload := marshalEvent(t, kafkamsg.ClientLimitsUpdatedMessage{
		ClientID: 55, DailyLimit: "200.0000", Version: 1,
	})

	if err := c.handleWithRetry(context.Background(), payload); err != nil {
		t.Fatalf("expected eventual success: %v", err)
	}
	if applier.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", applier.calls)
	}
}

func TestParseDecimalOrZero_ClientLimit(t *testing.T) {
	if !parseDecimalOrZero("").IsZero() {
		t.Fatal("empty string must return zero")
	}
	if !parseDecimalOrZero("abc").IsZero() {
		t.Fatal("malformed non-empty must return zero")
	}
	if !parseDecimalOrZero("123.45").Equal(decimal.RequireFromString("123.45")) {
		t.Fatal("valid decimal must parse correctly")
	}
}
