// contract/kafka/messages.go
package kafka

const (
	TopicSendEmail = "notification.send-email"
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
