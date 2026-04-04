package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	AuthLoginTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "auth_login_total",
		Help: "Total number of login attempts",
	}, []string{"status", "system_type"})

	AuthTokensIssuedTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "auth_tokens_issued_total",
		Help: "Total number of tokens issued",
	}, []string{"token_type"})

	AuthPasswordResetTotal = prom.NewCounter(prom.CounterOpts{
		Name: "auth_password_reset_total",
		Help: "Total number of password reset requests",
	})
)

func init() {
	prom.MustRegister(AuthLoginTotal)
	prom.MustRegister(AuthTokensIssuedTotal)
	prom.MustRegister(AuthPasswordResetTotal)
}
