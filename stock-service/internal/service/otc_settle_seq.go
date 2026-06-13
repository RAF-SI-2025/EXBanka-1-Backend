package service

// computeSettleSeq derives a deterministic, collision-resistant order_transaction_id
// from a saga's identity for the OTC option premium/strike settle steps. It is the
// idempotency key the option accept/exercise sagas (otc_accept_saga.go,
// otc_exercise_saga.go) hand to account-service's PartialSettleReservation, so a
// replayed settle never double-applies.
//
// (Relocated 2026-06-11 from the retired otc_stock_service.go — the in-bank OTC
// stock marketplace was removed, but its settle-sequence helper is shared by the
// kept option sagas.)
func computeSettleSeq(sagaID string, offerID uint64, qty int64) uint64 {
	// FNV-1a over sagaID, then XOR-fold offer/qty into the low bits.
	const offset = uint64(14695981039346656037)
	const prime = uint64(1099511628211)
	h := offset
	for i := 0; i < len(sagaID); i++ {
		h ^= uint64(sagaID[i])
		h *= prime
	}
	// Mask to 63 bits. This value is sent to account-service as an
	// order_transaction_id, whose column is a signed PG bigint (int64); a
	// value above math.MaxInt64 fails to encode and 500s the fill — and since
	// sagaID is a random UUID it happens ~50% of the time. Masking keeps the
	// id non-negative while preserving its idempotency role.
	return (h ^ (offerID*1_000_003 + uint64(qty))) & 0x7FFFFFFFFFFFFFFF
}
