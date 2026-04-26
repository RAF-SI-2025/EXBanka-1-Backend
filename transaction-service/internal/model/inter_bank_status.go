package model

// Inter-bank 2PC role / phase / status enums plus the transition matrix
// (Spec 3 §5.1 / §5.2 / §5.3).

const (
	// Roles
	RoleSender   = "sender"
	RoleReceiver = "receiver"

	// Phases
	PhasePrepare   = "prepare"
	PhaseCommit    = "commit"
	PhaseReconcile = "reconcile"
	PhaseDone      = "done"

	// Sender-side statuses
	StatusInitiated        = "initiated"
	StatusPreparing        = "preparing"
	StatusReadyReceived    = "ready_received"
	StatusNotReadyReceived = "notready_received"
	StatusCommitting       = "committing"
	StatusCommitted        = "committed"
	StatusRolledBack       = "rolled_back"
	StatusReconciling      = "reconciling"

	// Receiver-side statuses
	StatusPrepareReceived = "prepare_received"
	StatusValidated       = "validated"
	StatusReadySent       = "ready_sent"
	StatusNotReadySent    = "notready_sent"
	StatusFinalNotReady   = "final_notready"
	StatusCommitReceived  = "commit_received"
	StatusAbandoned       = "abandoned"
)

// validTransitions encodes the matrix from Spec 3 §5. Each entry maps a
// from-status to the set of legal to-statuses.
var validTransitions = map[string]map[string]struct{}{
	StatusInitiated:        toSet(StatusPreparing, StatusRolledBack),
	StatusPreparing:        toSet(StatusReadyReceived, StatusNotReadyReceived, StatusReconciling),
	StatusReadyReceived:    toSet(StatusCommitting, StatusReconciling),
	StatusNotReadyReceived: toSet(StatusRolledBack),
	StatusCommitting:       toSet(StatusCommitted, StatusReconciling),
	StatusReconciling:      toSet(StatusCommitted, StatusRolledBack, StatusReconciling),

	StatusPrepareReceived: toSet(StatusValidated, StatusNotReadySent),
	StatusValidated:       toSet(StatusReadySent),
	StatusReadySent:       toSet(StatusCommitReceived, StatusAbandoned),
	StatusNotReadySent:    toSet(StatusFinalNotReady),
	StatusCommitReceived:  toSet(StatusCommitted),
}

func toSet(vals ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out
}

// IsValidTransition reports whether the from→to transition is permitted by
// the spec matrix. Self-loop on `reconciling` is allowed (the cron stays in
// `reconciling` between retries).
func IsValidTransition(from, to string) bool {
	if from == to && from == StatusReconciling {
		return true
	}
	tos, ok := validTransitions[from]
	if !ok {
		return false
	}
	_, ok = tos[to]
	return ok
}

// IsTerminalStatus reports whether further state transitions are illegal.
func IsTerminalStatus(s string) bool {
	switch s {
	case StatusCommitted, StatusRolledBack, StatusFinalNotReady, StatusAbandoned:
		return true
	}
	return false
}
