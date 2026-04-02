package service

import (
	"testing"
	"time"
)

func TestParseTimezoneLocation_IANAString(t *testing.T) {
	loc, err := parseTimezoneLocation("America/New_York")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().In(loc)
	if now.Location().String() != "America/New_York" {
		t.Errorf("expected America/New_York, got %s", now.Location().String())
	}
}

func TestParseTimezoneLocation_OffsetString(t *testing.T) {
	loc, err := parseTimezoneLocation("-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != -5*3600 {
		t.Errorf("expected offset -18000, got %d", offset)
	}
}

func TestParseTimezoneLocation_PositiveOffset(t *testing.T) {
	loc, err := parseTimezoneLocation("+9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().In(loc)
	_, offset := now.Zone()
	if offset != 9*3600 {
		t.Errorf("expected offset 32400, got %d", offset)
	}
}

func TestParseTimezoneLocation_Empty(t *testing.T) {
	_, err := parseTimezoneLocation("")
	if err == nil {
		t.Fatal("expected error for empty timezone")
	}
}

func TestParseTimezoneLocation_InvalidIANA(t *testing.T) {
	_, err := parseTimezoneLocation("Not/A/Real/Zone")
	if err == nil {
		t.Fatal("expected error for invalid IANA timezone")
	}
}
