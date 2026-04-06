package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	AccountBalanceOperationsTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "account_balance_operations_total",
		Help: "Total number of balance operations (debit/credit)",
	}, []string{"type"})

	AccountsCreatedTotal = prom.NewCounter(prom.CounterOpts{
		Name: "accounts_created_total",
		Help: "Total number of accounts created",
	})

	AccountStatusChangesTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "account_status_changes_total",
		Help: "Total number of account status changes",
	}, []string{"new_status"})
)

func init() {
	prom.MustRegister(AccountBalanceOperationsTotal)
	prom.MustRegister(AccountsCreatedTotal)
	prom.MustRegister(AccountStatusChangesTotal)
}
