package influx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNilClientIsNoOp(t *testing.T) {
	var c *Client
	// All methods must be nil-safe — no panics
	assert.NotPanics(t, func() {
		c.WritePoint("test", map[string]string{"k": "v"}, map[string]interface{}{"f": 1.0}, time.Now())
	})
	assert.NotPanics(t, func() {
		c.Close()
	})
}

func TestNewClient_InvalidURL_ReturnsNil(t *testing.T) {
	// Empty URL should produce a nil client (graceful degradation)
	c := NewClient("", "token", "org", "bucket")
	assert.Nil(t, c)
}

func TestNewClient_ValidParams_ReturnsNonNil(t *testing.T) {
	// Valid params should produce a non-nil client even without a running InfluxDB
	c := NewClient("http://localhost:8086", "my-token", "exbanka", "stock_prices")
	assert.NotNil(t, c)
	if c != nil {
		c.Close()
	}
}

func TestQuery_NilClient_ReturnsEmptyResult(t *testing.T) {
	var c *Client
	result, err := c.Query(context.Background(), `from(bucket:"test") |> range(start: -1h)`)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestBucket_NilClient_ReturnsEmpty(t *testing.T) {
	var c *Client
	assert.Equal(t, "", c.Bucket())
}

func TestBucket_ValidClient_ReturnsBucket(t *testing.T) {
	c := NewClient("http://localhost:8086", "token", "org", "my-bucket")
	assert.Equal(t, "my-bucket", c.Bucket())
	c.Close()
}
