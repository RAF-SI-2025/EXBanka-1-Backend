// contract/kafka/messages.go
package kafka

const (
	TopicSendEmail = "notification.send-email"
	TopicEmailSent = "notification.email-sent"
)

type EmailType string

const (
	EmailTypeActivation    EmailType = "ACTIVATION"
	EmailTypePasswordReset EmailType = "PASSWORD_RESET"
	EmailTypeConfirmation  EmailType = "CONFIRMATION"
)

type SendEmailMessage struct {
	To        string            `json:"to"`
	EmailType EmailType         `json:"email_type"`
	Data      map[string]string `json:"data"`
}

type EmailSentMessage struct {
	To        string    `json:"to"`
	EmailType EmailType `json:"email_type"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
}
