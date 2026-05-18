package service

import (
	"testing"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

func TestParseTime(t *testing.T) {
	cases := []struct {
		in    string
		wantH int
		wantM int
	}{
		{"09:30", 9, 30},
		{"16:00", 16, 0},
		{"09:30:00", 9, 30},
		{"00:00", 0, 0},
		{"23:59", 23, 59},
		{"", 0, 0},
		{"bogus", 0, 0},
	}
	for _, c := range cases {
		h, m := parseTime(c.in)
		if h != c.wantH || m != c.wantM {
			t.Errorf("parseTime(%q): got (%d, %d), want (%d, %d)", c.in, h, m, c.wantH, c.wantM)
		}
	}
}

func TestIsWithinTradingHours_InvalidTimezone(t *testing.T) {
	ex := &model.StockExchange{TimeZone: "", OpenTime: "09:30", CloseTime: "16:00"}
	if isWithinTradingHours(ex) {
		t.Error("expected false for invalid timezone")
	}
}

func TestIsWithinTradingHours_AlwaysClosed(t *testing.T) {
	ex := &model.StockExchange{TimeZone: "0", OpenTime: "12:00", CloseTime: "12:00"}
	if isWithinTradingHours(ex) {
		t.Error("expected false when open=close")
	}
}

func TestIsWithinTradingHours_NeverClosed(t *testing.T) {
	ex := &model.StockExchange{TimeZone: "0", OpenTime: "00:00", CloseTime: "23:59"}
	now := time.Now().UTC()
	want := !(now.Hour() == 23 && now.Minute() == 59)
	if got := isWithinTradingHours(ex); got != want {
		t.Errorf("at %02d:%02d UTC: want %v, got %v", now.Hour(), now.Minute(), want, got)
	}
}

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
