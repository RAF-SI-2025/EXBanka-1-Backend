package sender

import (
	"fmt"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func BuildEmail(emailType kafkamsg.EmailType, data map[string]string) (subject, body string) {
	switch emailType {
	case kafkamsg.EmailTypeActivation:
		subject = "Activate Your EXBanka Account"
		name := data["first_name"]
		token := data["token"]
		body = fmt.Sprintf(`<h2>Welcome, %s!</h2>
<p>Your account has been created. Use the following token to activate your account:</p>
<p><strong>%s</strong></p>
<p>This token expires in 24 hours.</p>`, name, token)

	case kafkamsg.EmailTypePasswordReset:
		token := data["token"]
		subject = "Password Reset Request"
		body = fmt.Sprintf(`<h2>Password Reset</h2>
<p>Use the following token to reset your password:</p>
<p><strong>%s</strong></p>
<p>This token expires in 1 hour. If you did not request this, ignore this email.</p>`, token)

	case kafkamsg.EmailTypeConfirmation:
		name := data["first_name"]
		subject = "Account Activated Successfully"
		body = fmt.Sprintf(`<h2>Welcome aboard, %s!</h2>
<p>Your EXBanka account has been successfully activated.</p>`, name)

	default:
		subject = "EXBanka Notification"
		body = "<p>You have a new notification from EXBanka.</p>"
	}
	return subject, body
}
