package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	CardCreatedTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "card_created_total",
		Help: "Total number of cards created",
	}, []string{"card_type"})

	CardStatusChangesTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "card_status_changes_total",
		Help: "Total number of card status changes",
	}, []string{"action"})

	CardPinAttemptsTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "card_pin_attempts_total",
		Help: "Total number of PIN verification attempts",
	}, []string{"result"})
)

func init() {
	prom.MustRegister(CardCreatedTotal)
	prom.MustRegister(CardStatusChangesTotal)
	prom.MustRegister(CardPinAttemptsTotal)
}
