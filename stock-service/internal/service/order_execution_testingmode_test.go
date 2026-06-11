package service

import (
	"context"
	"testing"
	"time"
)

// trueTestingSettingRepo reports testing_mode = "true".
type trueTestingSettingRepo struct{}

func (trueTestingSettingRepo) Get(_ string) (string, error) { return "true", nil }
func (trueTestingSettingRepo) Set(_, _ string) error        { return nil }

func newEngineWith(setting SettingRepo) *OrderExecutionEngine {
	return NewOrderExecutionEngine(
		context.Background(),
		&fakeBaseCtxOrderRepo{}, // ListActiveApproved -> nil, so WakeAll resumes nothing
		&fakeBaseCtxTxRepo{},
		&fakeBaseCtxListingRepo{},
		setting,
		fakeBaseCtxPublisher{},
		&fakeBaseCtxFillHandler{},
	)
}

// TestEngine_WakeAll_BroadcastsAndReplacesChannel verifies the wake mechanism:
// the live broadcast channel is closed (unblocking every goroutine that selects
// on it) and replaced with a fresh one, so toggling testing mode on makes
// already-sleeping order goroutines re-evaluate immediately.
func TestEngine_WakeAll_BroadcastsAndReplacesChannel(t *testing.T) {
	e := newEngineWith(&fakeBaseCtxSettingRepo{})
	wake := e.currentWake()

	// Not closed before WakeAll.
	select {
	case <-wake:
		t.Fatal("broadcast channel closed before WakeAll")
	default:
	}

	e.WakeAll()

	// Closed after WakeAll (broadcast).
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("WakeAll did not close the broadcast channel")
	}
	// And replaced with a fresh channel.
	if e.currentWake() == wake {
		t.Fatal("WakeAll did not replace the broadcast channel")
	}
}

// TestEngine_TestingModeEnabled reflects the persisted setting.
func TestEngine_TestingModeEnabled(t *testing.T) {
	if newEngineWith(&fakeBaseCtxSettingRepo{}).testingModeEnabled() {
		t.Fatal("empty setting must read as testing mode off")
	}
	if !newEngineWith(trueTestingSettingRepo{}).testingModeEnabled() {
		t.Fatal("testing_mode=true must read as testing mode on")
	}
}
