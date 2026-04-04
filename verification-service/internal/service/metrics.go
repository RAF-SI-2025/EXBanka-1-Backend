package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	VerificationChallengesCreatedTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "verification_challenges_created_total",
		Help: "Total number of verification challenges created",
	}, []string{"method"})

	VerificationAttemptsTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "verification_attempts_total",
		Help: "Total number of verification attempts",
	}, []string{"result"})

	VerificationChallengesExpiredTotal = prom.NewCounter(prom.CounterOpts{
		Name: "verification_challenges_expired_total",
		Help: "Total number of verification challenges expired",
	})
)

func init() {
	prom.MustRegister(VerificationChallengesCreatedTotal)
	prom.MustRegister(VerificationAttemptsTotal)
	prom.MustRegister(VerificationChallengesExpiredTotal)
}
