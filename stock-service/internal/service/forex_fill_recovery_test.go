package service

import (
	"context"
	"testing"
)

// TestRecoverForexBuySaga_ForwardResumeOnce proves the forex buy-fill saga
// re-drives to completion (settle quote + credit base) and, invoked repeatedly
// under the same sagaID, neither double-settles nor double-credits (completed
// steps skipped on resume; all money legs are idempotency-keyed).
func TestRecoverForexBuySaga_ForwardResumeOnce(t *testing.T) {
	svc, mocks := buildForexFillService()
	order := forexOrder(1)
	txn := forexTxn(900, 1, 100, 1.05, 1000)
	if err := mocks.txRepo.Create(txn); err != nil {
		t.Fatalf("create txn: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := svc.RecoverForexBuySaga(context.Background(), "forex-fill-1", order, txn, false); err != nil {
			t.Fatalf("RecoverForexBuySaga #%d: %v", i, err)
		}
	}

	if len(mocks.fillClient.partialSettleCalls) != 1 {
		t.Fatalf("recovery double-settled quote: %d calls, want 1", len(mocks.fillClient.partialSettleCalls))
	}
	if len(mocks.fillClient.creditAccountCalls) != 1 {
		t.Fatalf("recovery double-credited base: %d calls, want 1", len(mocks.fillClient.creditAccountCalls))
	}
}
