package influx

import (
	"context"
	"log"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

// Client wraps the InfluxDB v2 client with nil-safe methods.
// If the client is nil, all operations are no-ops — this allows services
// to run without InfluxDB (graceful degradation).
type Client struct {
	inner    influxdb2.Client
	writeAPI api.WriteAPIBlocking
	queryAPI api.QueryAPI
	org      string
	bucket   string
}

// NewClient creates a new InfluxDB client. Returns nil if url is empty
// (enabling graceful degradation when InfluxDB is not configured).
func NewClient(url, token, org, bucket string) *Client {
	if url == "" {
		log.Println("WARN: InfluxDB URL not configured — time-series writes disabled")
		return nil
	}

	inner := influxdb2.NewClient(url, token)
	return &Client{
		inner:    inner,
		writeAPI: inner.WriteAPIBlocking(org, bucket),
		queryAPI: inner.QueryAPI(org),
		org:      org,
		bucket:   bucket,
	}
}

// WritePoint writes a single data point to InfluxDB.
// Nil-safe: does nothing if the client is nil.
func (c *Client) WritePoint(measurement string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	if c == nil {
		return
	}

	p := influxdb2.NewPoint(measurement, tags, fields, ts)
	if err := c.writeAPI.WritePoint(context.Background(), p); err != nil {
		log.Printf("WARN: InfluxDB write failed for %s: %v", measurement, err)
	}
}

// WritePointAsync writes a point without blocking. Errors are logged.
// Nil-safe: does nothing if the client is nil.
func (c *Client) WritePointAsync(measurement string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	if c == nil {
		return
	}
	go c.WritePoint(measurement, tags, fields, ts)
}

// Query executes a Flux query and returns the result table.
// Nil-safe: returns nil, nil if the client is nil.
func (c *Client) Query(ctx context.Context, flux string) (*api.QueryTableResult, error) {
	if c == nil {
		return nil, nil
	}
	return c.queryAPI.Query(ctx, flux)
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

// Close releases all resources held by the client.
// Nil-safe: does nothing if the client is nil.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.inner.Close()
}
