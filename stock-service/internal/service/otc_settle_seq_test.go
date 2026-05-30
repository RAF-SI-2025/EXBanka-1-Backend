package service

import (
	"math"
	"testing"
)

// TestComputeSettleSeq_FitsSignedBigint guards the regression where the FNV-1a
// settle sequence exceeded math.MaxInt64 and failed to encode into
// account-service's signed bigint order_transaction_id column (intermittent
// 500s on OTC buy-offer fills, since sagaID is a random UUID). The masked
// result must always be a non-negative int64.
func TestComputeSettleSeq_FitsSignedBigint(t *testing.T) {
	ids := []string{
		"00000000-0000-0000-0000-000000000000",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
		"a1b2c3d4-e5f6-7890-abcd-ef0123456789",
		"deadbeef-dead-beef-dead-beefdeadbeef",
		"7f3e9a21-0c5d-4b8e-9f12-3456789abcde",
	}
	for _, id := range ids {
		for off := uint64(0); off < 2000; off++ {
			v := computeSettleSeq(id, off, int64(off))
			if v > math.MaxInt64 {
				t.Fatalf("computeSettleSeq(%q, %d) = %d exceeds MaxInt64", id, off, v)
			}
		}
	}
}
