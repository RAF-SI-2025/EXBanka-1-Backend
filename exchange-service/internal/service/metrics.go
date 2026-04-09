package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	ExchangeRateSyncTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "exchange_rate_sync_total",
		Help: "Total number of exchange rate sync operations",
	}, []string{"status"})

	ExchangeRateSyncDuration = prom.NewHistogram(prom.HistogramOpts{
		Name:    "exchange_rate_sync_duration_seconds",
		Help:    "Time taken to sync exchange rates",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60},
	})

	ExchangeConversionsTotal = prom.NewCounter(prom.CounterOpts{
		Name: "exchange_conversions_total",
		Help: "Total number of currency conversions performed",
	})
)

func init() {
	prom.MustRegister(ExchangeRateSyncTotal)
	prom.MustRegister(ExchangeRateSyncDuration)
	prom.MustRegister(ExchangeConversionsTotal)
}
