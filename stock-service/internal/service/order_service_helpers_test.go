package service

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDefaultOrderSettings(t *testing.T) {
	s := defaultOrderSettings{}
	if s.CommissionRate().IsZero() {
		t.Error("expected non-zero commission rate")
	}
	if s.MarketSlippagePct().IsZero() {
		t.Error("expected non-zero slippage")
	}
}

func TestParseTimeHM(t *testing.T) {
	cases := []struct {
		in    string
		wantH int
		wantM int
	}{
		{"09:30", 9, 30},
		{"16:00", 16, 0},
		{"00:00", 0, 0},
		{"23:59", 23, 59},
		{"abc", 0, 0},
		{"", 0, 0},
	}
	for _, c := range cases {
		h, m := parseTimeHM(c.in)
		if h != c.wantH || m != c.wantM {
			t.Errorf("parseTimeHM(%q): got (%d, %d), want (%d, %d)", c.in, h, m, c.wantH, c.wantM)
		}
	}
}

func TestParseTimezoneOffsetSafe(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"-5", -5},
		{"+9", 9},
		{"0", 0},
		{"+12", 12},
		{"-3", -3},
		{"", 0},
	}
	for _, c := range cases {
		got := parseTimezoneOffsetSafe(c.in)
		if got != c.want {
			t.Errorf("parseTimezoneOffsetSafe(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCalculateCommission_StopOrder(t *testing.T) {
	// stop falls under "default" (market-like): 14% × price capped at $7
	c := calculateCommission("stop", decimal.NewFromInt(20))
	expected := decimal.NewFromFloat(2.8)
	if !c.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, c)
	}
}

func TestCalculateCommission_StopLimitOrder(t *testing.T) {
	// stop_limit follows limit branch: 24% × price capped at $12
	c := calculateCommission("stop_limit", decimal.NewFromInt(20))
	expected := decimal.NewFromFloat(4.8)
	if !c.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, c)
	}
}
