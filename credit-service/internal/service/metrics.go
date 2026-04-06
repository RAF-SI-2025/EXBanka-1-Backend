package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	CreditLoanRequestTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "credit_loan_request_total",
		Help: "Total number of loan requests by status",
	}, []string{"status"})

	CreditInstallmentCollectionTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "credit_installment_collection_total",
		Help: "Total number of installment collections by status",
	}, []string{"status"})

	CreditLatePaymentPenaltiesTotal = prom.NewCounter(prom.CounterOpts{
		Name: "credit_late_payment_penalties_total",
		Help: "Total number of late payment penalties applied",
	})
)

func init() {
	prom.MustRegister(CreditLoanRequestTotal)
	prom.MustRegister(CreditInstallmentCollectionTotal)
	prom.MustRegister(CreditLatePaymentPenaltiesTotal)
}
