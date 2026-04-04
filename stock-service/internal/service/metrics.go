package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	StockOrderTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "stock_order_total",
		Help: "Total number of stock orders",
	}, []string{"order_type", "status"})

	StockPriceRefreshDuration = prom.NewHistogram(prom.HistogramOpts{
		Name:    "stock_price_refresh_duration_seconds",
		Help:    "Time taken to refresh stock prices",
		Buckets: []float64{1, 5, 10, 30, 60, 120},
	})

	StockOTCTradesTotal = prom.NewCounter(prom.CounterOpts{
		Name: "stock_otc_trades_total",
		Help: "Total number of OTC trades completed",
	})

	StockTaxCollectedTotal = prom.NewCounter(prom.CounterOpts{
		Name: "stock_tax_collected_total",
		Help: "Total number of tax collection runs",
	})
)

func init() {
	prom.MustRegister(StockOrderTotal)
	prom.MustRegister(StockPriceRefreshDuration)
	prom.MustRegister(StockOTCTradesTotal)
	prom.MustRegister(StockTaxCollectedTotal)
}
