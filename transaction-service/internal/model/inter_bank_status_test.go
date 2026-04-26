package model

import "testing"

func TestIsValidTransition_Sender(t *testing.T) {
	cases := []struct {
		from, to string
		ok       bool
	}{
		{StatusInitiated, StatusPreparing, true},
		{StatusInitiated, StatusRolledBack, true},
		{StatusInitiated, StatusCommitted, false},
		{StatusPreparing, StatusReadyReceived, true},
		{StatusPreparing, StatusNotReadyReceived, true},
		{StatusPreparing, StatusReconciling, true},
		{StatusPreparing, StatusCommitted, false},
		{StatusReadyReceived, StatusCommitting, true},
		{StatusReadyReceived, StatusReconciling, true},
		{StatusReadyReceived, StatusRolledBack, false},
		{StatusCommitting, StatusCommitted, true},
		{StatusCommitting, StatusReconciling, true},
		{StatusCommitting, StatusRolledBack, false},
		{StatusReconciling, StatusCommitted, true},
		{StatusReconciling, StatusRolledBack, true},
		{StatusReconciling, StatusReconciling, true}, // self-loop allowed
	}
	for _, c := range cases {
		if got := IsValidTransition(c.from, c.to); got != c.ok {
			t.Errorf("IsValidTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestIsValidTransition_Receiver(t *testing.T) {
	cases := []struct {
		from, to string
		ok       bool
	}{
		{StatusPrepareReceived, StatusValidated, true},
		{StatusPrepareReceived, StatusNotReadySent, true},
		{StatusPrepareReceived, StatusReadySent, false},
		{StatusValidated, StatusReadySent, true},
		{StatusValidated, StatusCommitted, false},
		{StatusReadySent, StatusCommitReceived, true},
		{StatusReadySent, StatusAbandoned, true},
		{StatusReadySent, StatusCommitted, false},
		{StatusCommitReceived, StatusCommitted, true},
		{StatusNotReadySent, StatusFinalNotReady, true},
		{StatusFinalNotReady, StatusCommitted, false}, // illegal - terminal
	}
	for _, c := range cases {
		if got := IsValidTransition(c.from, c.to); got != c.ok {
			t.Errorf("IsValidTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestIsValidTransition_UnknownFrom_Rejected(t *testing.T) {
	if IsValidTransition("nonsense", StatusCommitted) {
		t.Error("transition from unknown status must not be allowed")
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{StatusCommitted, StatusRolledBack, StatusFinalNotReady, StatusAbandoned}
	for _, s := range terminal {
		if !IsTerminalStatus(s) {
			t.Errorf("%s should be terminal", s)
		}
	}
	nonTerminal := []string{StatusInitiated, StatusPreparing, StatusReadyReceived, StatusCommitting, StatusReconciling, StatusReadySent, StatusCommitReceived}
	for _, s := range nonTerminal {
		if IsTerminalStatus(s) {
			t.Errorf("%s should NOT be terminal", s)
		}
	}
}
