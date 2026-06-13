package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/exbanka/contract/logger"
)

func TestInit_SetsJSONHandler(t *testing.T) {
	logger.Init("test-service")

	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, nil)
	slog.SetDefault(slog.New(h).With("service", "test-service"))

	slog.Info("hello", "key", "value")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if record["msg"] != "hello" {
		t.Errorf("expected msg=hello, got %v", record["msg"])
	}
	if record["service"] != "test-service" {
		t.Errorf("expected service=test-service, got %v", record["service"])
	}
	if record["key"] != "value" {
		t.Errorf("expected key=value, got %v", record["key"])
	}
}
