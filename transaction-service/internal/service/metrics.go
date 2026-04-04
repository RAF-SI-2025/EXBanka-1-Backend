package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	TransactionTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "transaction_total",
		Help: "Total number of transactions",
	}, []string{"type", "status"})

	TransactionAmountRSDSum = prom.NewCounterVec(prom.CounterOpts{
		Name: "transaction_amount_rsd_sum",
		Help: "Cumulative transaction amounts in RSD",
	}, []string{"type"})

	TransactionProcessingDuration = prom.NewHistogramVec(prom.HistogramOpts{
		Name:    "transaction_processing_duration_seconds",
		Help:    "Time taken to process transactions",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"type"})

	TransactionSagaCompensationsTotal = prom.NewCounter(prom.CounterOpts{
		Name: "transaction_saga_compensations_total",
		Help: "Total number of saga compensations triggered",
	})
)

func init() {
	prom.MustRegister(TransactionTotal)
	prom.MustRegister(TransactionAmountRSDSum)
	prom.MustRegister(TransactionProcessingDuration)
	prom.MustRegister(TransactionSagaCompensationsTotal)
}
