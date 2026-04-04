package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateInterval_ValidIntervals(t *testing.T) {
	valid := []string{"1m", "5m", "15m", "1h", "4h", "1d"}
	for _, interval := range valid {
		_, err := parseInterval(interval)
		assert.NoError(t, err, "interval %s should be valid", interval)
	}
}

func TestValidateInterval_InvalidIntervals(t *testing.T) {
	invalid := []string{"", "2m", "3h", "1w", "invalid", "10m"}
	for _, interval := range invalid {
		_, err := parseInterval(interval)
		assert.Error(t, err, "interval %s should be invalid", interval)
	}
}

func TestBuildCandleFluxQuery(t *testing.T) {
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)

	query := buildCandleFluxQuery("stock_prices", uint64(42), "1h", from, to)
	assert.Contains(t, query, `r["listing_id"] == "42"`)
	assert.Contains(t, query, "aggregateWindow(every: 1h")
	assert.Contains(t, query, `bucket: "stock_prices"`)
}

func TestCandlePoint_ZeroValues(t *testing.T) {
	cp := CandlePoint{}
	assert.True(t, cp.Timestamp.IsZero())
	assert.Equal(t, float64(0), cp.Open)
	assert.Equal(t, float64(0), cp.High)
	assert.Equal(t, float64(0), cp.Low)
	assert.Equal(t, float64(0), cp.Close)
	assert.Equal(t, int64(0), cp.Volume)
}

func TestToFloat64(t *testing.T) {
	assert.Equal(t, 1.5, toFloat64(float64(1.5)))
	assert.Equal(t, float64(1), toFloat64(float32(1.0)))
	assert.Equal(t, float64(42), toFloat64(int64(42)))
	assert.Equal(t, float64(7), toFloat64(int(7)))
	assert.Equal(t, float64(0), toFloat64("unsupported"))
}

func TestToInt64(t *testing.T) {
	assert.Equal(t, int64(42), toInt64(int64(42)))
	assert.Equal(t, int64(3), toInt64(float64(3.7)))
	assert.Equal(t, int64(7), toInt64(int(7)))
	assert.Equal(t, int64(0), toInt64("unsupported"))
}
