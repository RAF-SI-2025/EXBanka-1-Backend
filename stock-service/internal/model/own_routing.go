package model

import (
	"log"
	"strconv"
	"sync/atomic"
)

// ownRouting is this bank's routing number, set once at startup from
// OWN_BANK_CODE. Local OTC rows are stamped with it by BeforeCreate hooks
// so local-vs-remote is `routing_number == ownRouting`.
var ownRouting atomic.Int64

// SetOwnRouting is called once at startup (cmd/main.go) with OWN_BANK_CODE.
// A non-numeric bank code leaves ownRouting at 0; we log loudly rather than
// swallow it silently, since a 0 own-routing would stamp every local OTC row
// with routing_number=0 (main.go also fatals on a bad OWN_BANK_CODE).
func SetOwnRouting(bankCode string) {
	n, err := strconv.ParseInt(bankCode, 10, 64)
	if err != nil {
		log.Printf("model.SetOwnRouting: OWN_BANK_CODE %q is not numeric (%v); own routing left at 0", bankCode, err)
		return
	}
	ownRouting.Store(n)
}

// OwnRouting returns the configured own routing number.
func OwnRouting() int64 { return ownRouting.Load() }
