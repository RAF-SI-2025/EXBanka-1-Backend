package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	UserEmployeeCreatedTotal = prom.NewCounter(prom.CounterOpts{
		Name: "user_employee_created_total",
		Help: "Total number of employees created",
	})

	UserEmployeeLimitUpdatesTotal = prom.NewCounter(prom.CounterOpts{
		Name: "user_employee_limit_updates_total",
		Help: "Total number of employee limit updates",
	})

	UserRoleChangesTotal = prom.NewCounter(prom.CounterOpts{
		Name: "user_role_changes_total",
		Help: "Total number of employee role changes",
	})
)

func init() {
	prom.MustRegister(UserEmployeeCreatedTotal)
	prom.MustRegister(UserEmployeeLimitUpdatesTotal)
	prom.MustRegister(UserRoleChangesTotal)
}
