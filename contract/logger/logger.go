// Package logger initialises the process-wide structured JSON logger.
//
// Call Init once at the top of main() before any other setup. After that,
// every slog.Info/Warn/Error call emits a JSON line, and every log.Printf /
// log.Println call is automatically routed through the same JSON handler
// (Go 1.21+ slog.SetDefault behaviour).
package logger

import (
	"log/slog"
	"os"
)

// Init configures the global slog default to a JSON handler writing to stdout
// and injects "service" into every log record. It also redirects the legacy
// log package through the same handler so existing log.Printf calls emit JSON
// without any further changes.
func Init(service string) {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(h).With("service", service))
}
