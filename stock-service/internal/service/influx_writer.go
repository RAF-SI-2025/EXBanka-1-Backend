package service

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/contract/influx"
)

// writeSecurityPricePoint writes a single security price data point to InfluxDB.
// Non-blocking: if influxClient is nil, this is a no-op.
func writeSecurityPricePoint(
	influxClient *influx.Client,
	listingID uint64,
	securityType string,
	ticker string,
	exchangeAcronym string,
	price, high, low, change decimal.Decimal,
	volume int64,
	ts time.Time,
) {
	if influxClient == nil {
		return
	}

	tags := map[string]string{
		"listing_id":    fmt.Sprintf("%d", listingID),
		"security_type": securityType,
		"ticker":        ticker,
		"exchange":      exchangeAcronym,
	}

	fields := map[string]interface{}{
		"price":  priceToFloat(price),
		"high":   priceToFloat(high),
		"low":    priceToFloat(low),
		"change": priceToFloat(change),
		"volume": volume,
	}

	influxClient.WritePointAsync("security_price", tags, fields, ts)
}

// priceToFloat converts a decimal.Decimal to float64 for InfluxDB field storage.
func priceToFloat(d decimal.Decimal) float64 {
	f, _ := d.Float64()
	return f
}
