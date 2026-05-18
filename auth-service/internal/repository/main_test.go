package repository

import (
	"database/sql/driver"
	"os"
	"testing"

	sqlite3 "github.com/glebarez/go-sqlite"
)

// TestMain registers a no-op pg_advisory_xact_lock UDF on the SQLite driver
// before any tests open a connection, so production code that calls
// `SELECT pg_advisory_xact_lock(?)` (a Postgres builtin) can run under
// SQLite-backed unit tests. Real Postgres has the function natively.
//
// Tests that exercise this UDF must open their gorm DB through a pre-opened
// *sql.DB (see setupPgCompatTestDB). When gorm opens its own connection via
// sqlite.Open(dsn) the UDF is not picked up reliably; passing an explicit
// Conn works around that.
func TestMain(m *testing.M) {
	_ = sqlite3.RegisterDeterministicScalarFunction(
		"pg_advisory_xact_lock", 1,
		func(_ *sqlite3.FunctionContext, _ []driver.Value) (driver.Value, error) {
			return nil, nil
		},
	)
	os.Exit(m.Run())
}
