package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	ClientCreatedTotal = prom.NewCounter(prom.CounterOpts{
		Name: "client_created_total",
		Help: "Total number of clients created",
	})

	ClientLimitUpdatesTotal = prom.NewCounter(prom.CounterOpts{
		Name: "client_limit_updates_total",
		Help: "Total number of client limit updates",
	})
)

func init() {
	prom.MustRegister(ClientCreatedTotal)
	prom.MustRegister(ClientLimitUpdatesTotal)
}
