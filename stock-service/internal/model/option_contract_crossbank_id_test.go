// Package model — regression test for the cross-bank tx-id column width on
// OptionContract.
//
// The cross-bank OTC accept/exercise flows store a NAMESPACED transaction id of
// the form "<peerRouting>:<uuid>" (and the SI-TX correlation prefixes the peer
// routing, e.g. "222:0e35c029-a526-4965-83b3-b5748e3dff4c") into
// OptionContract.CrossbankTxID / CrossbankExerciseTxID. A bare UUID is 36 chars,
// but the namespaced form is up to ~56 chars (a 19-digit int64 routing + ":" +
// 36-char uuid). When these columns were sized 36 (UUID-only), persisting the
// remote contract row at COMMIT_TX time failed on Postgres with
// "value too long for type character varying(36)" (SQLSTATE 22001) — which
// aborted contract formation and left the cross-bank SI-TX stuck in "committing"
// with NO contract on either bank.
//
// This test pins the column widths so the namespaced id always fits.
package model

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// gormSize extracts the `size:N` directive from a struct field's gorm tag.
func gormSize(t *testing.T, field string) int {
	t.Helper()
	rt := reflect.TypeOf(OptionContract{})
	f, ok := rt.FieldByName(field)
	if !ok {
		t.Fatalf("OptionContract has no field %q", field)
	}
	tag := f.Tag.Get("gorm")
	for _, part := range strings.Split(tag, ";") {
		if strings.HasPrefix(part, "size:") {
			n, err := strconv.Atoi(strings.TrimPrefix(part, "size:"))
			if err != nil {
				t.Fatalf("field %s: bad size directive %q: %v", field, part, err)
			}
			return n
		}
	}
	t.Fatalf("field %s gorm tag %q has no size directive", field, tag)
	return 0
}

// maxNamespacedTxIDLen is the worst-case length of "<peerRouting>:<uuid>":
// a 19-digit int64 routing + ":" + a 36-char UUID.
const maxNamespacedTxIDLen = 19 + 1 + 36 // 56

func TestOptionContract_CrossbankTxIDColumns_FitNamespacedID(t *testing.T) {
	for _, field := range []string{"CrossbankTxID", "CrossbankExerciseTxID"} {
		if got := gormSize(t, field); got < maxNamespacedTxIDLen {
			t.Errorf("OptionContract.%s size=%d is too small for a namespaced cross-bank tx id "+
				"(\"<peerRouting>:<uuid>\" is up to %d chars); the COMMIT_TX contract write overflows the column",
				field, got, maxNamespacedTxIDLen)
		}
	}
}
