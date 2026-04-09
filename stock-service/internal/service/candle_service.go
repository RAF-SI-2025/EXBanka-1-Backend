package service

import (
	"context"
	"fmt"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api"

	"github.com/exbanka/contract/influx"
)

// CandlePoint represents a single OHLCV candle bar.
type CandlePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    int64     `json:"volume"`
}

// CandleService queries InfluxDB for aggregated candle data.
type CandleService struct {
	influxClient *influx.Client
}

// NewCandleService creates a new CandleService.
func NewCandleService(influxClient *influx.Client) *CandleService {
	return &CandleService{influxClient: influxClient}
}

// GetCandles retrieves aggregated candle data for a listing.
// Returns empty slice (not error) if InfluxDB is not configured.
func (s *CandleService) GetCandles(ctx context.Context, listingID uint64, interval string, from, to time.Time) ([]CandlePoint, error) {
	if s.influxClient == nil {
		return []CandlePoint{}, nil
	}

	dur, err := parseInterval(interval)
	if err != nil {
		return nil, err
	}
	_ = dur // validated; we use the string form in Flux

	bucket := s.influxClient.Bucket()
	query := buildCandleFluxQuery(bucket, listingID, interval, from, to)

	result, err := s.influxClient.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("influx query failed: %w", err)
	}
	if result == nil {
		return []CandlePoint{}, nil
	}

	return parseCandleResult(result)
}

// parseInterval validates and converts interval strings to time.Duration.
var validIntervals = map[string]time.Duration{
	"1m":  1 * time.Minute,
	"5m":  5 * time.Minute,
	"15m": 15 * time.Minute,
	"1h":  1 * time.Hour,
	"4h":  4 * time.Hour,
	"1d":  24 * time.Hour,
}

func parseInterval(interval string) (time.Duration, error) {
	dur, ok := validIntervals[interval]
	if !ok {
		return 0, fmt.Errorf("invalid interval %q: must be one of 1m, 5m, 15m, 1h, 4h, 1d", interval)
	}
	return dur, nil
}

// buildCandleFluxQuery constructs a Flux query that aggregates security_price
// data into OHLCV candles. InfluxDB's aggregateWindow computes the aggregation.
//
// The query performs:
//  1. Filter by bucket, time range, measurement, and listing_id tag
//  2. Pivot fields (price, high, low, volume) into columns
//  3. Use aggregateWindow to bucket into the requested interval
//
// Since InfluxDB stores each write as a snapshot, we use:
//   - first(price) as Open
//   - max(high)    as High
//   - min(low)     as Low
//   - last(price)  as Close
//   - sum(volume)  as Volume
func buildCandleFluxQuery(bucket string, listingID uint64, interval string, from, to time.Time) string {
	return fmt.Sprintf(`
open = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "price")
  |> aggregateWindow(every: %s, fn: first, createEmpty: false)
  |> set(key: "_field", value: "open")

high = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "high")
  |> aggregateWindow(every: %s, fn: max, createEmpty: false)
  |> set(key: "_field", value: "high")

low = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "low")
  |> aggregateWindow(every: %s, fn: min, createEmpty: false)
  |> set(key: "_field", value: "low")

close = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "price")
  |> aggregateWindow(every: %s, fn: last, createEmpty: false)
  |> set(key: "_field", value: "close")

vol = from(bucket: "%s")
  |> range(start: %s, stop: %s)
  |> filter(fn: (r) => r["_measurement"] == "security_price")
  |> filter(fn: (r) => r["listing_id"] == "%d")
  |> filter(fn: (r) => r["_field"] == "volume")
  |> aggregateWindow(every: %s, fn: sum, createEmpty: false)
  |> set(key: "_field", value: "volume")

union(tables: [open, high, low, close, vol])
  |> pivot(rowKey: ["_time"], columnKey: ["_field"], valueColumn: "_value")
  |> sort(columns: ["_time"])
`,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
		bucket, from.Format(time.RFC3339), to.Format(time.RFC3339), listingID, interval,
	)
}

// parseCandleResult reads the InfluxDB query result and builds CandlePoint slices.
func parseCandleResult(result *api.QueryTableResult) ([]CandlePoint, error) {
	var candles []CandlePoint

	for result.Next() {
		record := result.Record()
		cp := CandlePoint{
			Timestamp: record.Time(),
		}

		if v := record.ValueByKey("open"); v != nil {
			cp.Open = toFloat64(v)
		}
		if v := record.ValueByKey("high"); v != nil {
			cp.High = toFloat64(v)
		}
		if v := record.ValueByKey("low"); v != nil {
			cp.Low = toFloat64(v)
		}
		if v := record.ValueByKey("close"); v != nil {
			cp.Close = toFloat64(v)
		}
		if v := record.ValueByKey("volume"); v != nil {
			cp.Volume = toInt64(v)
		}

		candles = append(candles, cp)
	}

	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("influx result error: %w", err)
	}

	return candles, nil
}

func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0
	}
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}
