package source

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestExchangeDefaults_AtLeastTwoOpenAtAllTimes asserts the configured exchange
// trading hours keep >=2 exchanges open at EVERY UTC moment — sampled every 10
// minutes across a winter and a summer date so DST shifts are covered. The
// simulator requires at least two working exchanges at any time; the
// 21:00-00:00 UTC "Pacific dead zone" is the tight spot, covered by ASX (early
// open, Sydney) + BVMF (late close, São Paulo). If a future hours edit reopens a
// single-exchange (or zero) window, this test fails with the exact UTC slot.
func TestExchangeDefaults_AtLeastTwoOpenAtAllTimes(t *testing.T) {
	parse := func(s string) int {
		p := strings.SplitN(s, ":", 2)
		h, _ := strconv.Atoi(p[0])
		m, _ := strconv.Atoi(p[1])
		return h*60 + m
	}
	days := []time.Time{
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), // winter
		time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), // summer
		time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), // EU/US DST shoulder
	}
	for _, day := range days {
		for step := 0; step < 24*6; step++ { // every 10 minutes
			at := day.Add(time.Duration(step) * 10 * time.Minute)
			open := make([]string, 0, 4)
			for acr, d := range exchangeDefaults {
				loc, err := time.LoadLocation(d.TimeZone)
				if err != nil {
					t.Fatalf("bad timezone for %s: %v", acr, err)
				}
				lt := at.In(loc)
				cur := lt.Hour()*60 + lt.Minute()
				if parse(d.OpenTime) <= cur && cur < parse(d.CloseTime) {
					open = append(open, acr)
				}
			}
			if len(open) < 2 {
				t.Errorf("only %d exchange(s) open at %s UTC (%v) — need >=2",
					len(open), at.Format("2006-01-02 15:04"), open)
			}
		}
	}
}
