package service

import prom "github.com/prometheus/client_golang/prometheus"

var (
	NotificationEmailsSentTotal = prom.NewCounterVec(prom.CounterOpts{
		Name: "notification_emails_sent_total",
		Help: "Total number of emails sent",
	}, []string{"status"})

	NotificationEmailSendDuration = prom.NewHistogram(prom.HistogramOpts{
		Name:    "notification_email_send_duration_seconds",
		Help:    "Time taken to send emails",
		Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30},
	})

	NotificationMobilePushTotal = prom.NewCounter(prom.CounterOpts{
		Name: "notification_mobile_push_total",
		Help: "Total number of mobile push notifications sent",
	})
)

func init() {
	prom.MustRegister(NotificationEmailsSentTotal)
	prom.MustRegister(NotificationEmailSendDuration)
	prom.MustRegister(NotificationMobilePushTotal)
}
