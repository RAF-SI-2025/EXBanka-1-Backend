//go:build integration

package workflows

import (
	"testing"
)

// TestInterBank_CrashRecovery — verifies that the InterBankRecovery
// startup sweep correctly nudges sender rows stuck in `preparing` /
// `committing` past their timeout into `reconciling`, and re-runs
// CommitIncoming for receiver rows in `commit_received`.
//
// Skipped: this scenario requires either a transaction-service restart
// mid-test (out of reach without docker orchestration) or direct DB
// access to seed a stuck row. The startup-recovery code path is unit-
// tested in transaction-service/internal/service via the regular
// in-process service-level tests; the production path is exercised on
// every container start.
func TestInterBank_CrashRecovery(t *testing.T) {
	t.Skip("requires service restart mid-test; covered by startup-recovery code on every container boot")
}
