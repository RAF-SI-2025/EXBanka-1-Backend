// Package shared — log.go provides the minimal default logger used by the
// shared utilities (grpc server bootstrap, kafka topic ensure, scheduled
// job runner). Centralised here so callers can override behaviour without
// every helper re-deriving it.
package shared

import "log"

// defaultLog is a thin wrapper over log.Printf with a "[shared]" prefix.
// Used as the default logger by the helpers in this package.
func defaultLog(format string, args ...any) {
	log.Printf("[shared] "+format, args...)
}
