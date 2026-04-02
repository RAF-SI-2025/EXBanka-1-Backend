package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

// isWithinTradingHours checks if the current moment falls within the exchange's
// open and close times, accounting for its time zone.
// Supports both IANA timezone strings ("America/New_York") and UTC offset strings ("-5").
func isWithinTradingHours(ex *model.StockExchange) bool {
	loc, err := parseTimezoneLocation(ex.TimeZone)
	if err != nil {
		return false
	}
	now := time.Now().In(loc)

	openH, openM := parseTime(ex.OpenTime)
	closeH, closeM := parseTime(ex.CloseTime)

	nowMinutes := now.Hour()*60 + now.Minute()
	openMinutes := openH*60 + openM
	closeMinutes := closeH*60 + closeM

	return nowMinutes >= openMinutes && nowMinutes < closeMinutes
}

// parseTimezoneLocation parses a timezone string. It first tries IANA format
// (e.g. "America/New_York"), then falls back to numeric UTC offset (e.g. "-5", "+9").
func parseTimezoneLocation(tz string) (*time.Location, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return nil, fmt.Errorf("empty timezone")
	}

	// Try IANA format first (contains a slash like "America/New_York")
	if strings.Contains(tz, "/") {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("invalid IANA timezone %q: %w", tz, err)
		}
		return loc, nil
	}

	// Fall back to numeric offset ("-5", "+9", "0")
	offset, err := strconv.Atoi(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone offset %q: %w", tz, err)
	}
	return time.FixedZone(fmt.Sprintf("UTC%+d", offset), offset*3600), nil
}

// parseTime parses "09:30" or "09:30:00" to (9, 30).
func parseTime(t string) (int, int) {
	parts := strings.Split(t, ":")
	if len(parts) < 2 {
		return 0, 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h, m
}
