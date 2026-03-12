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
		link := data["link"]
		body = fmt.Sprintf(`<h2>Welcome, %s!</h2>
<p>Your account has been created. Click the link below to activate your account and set your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Activate Account</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 24 hours.</p>`, name, link, link)

	case kafkamsg.EmailTypePasswordReset:
		link := data["link"]
		subject = "Password Reset Request"
		body = fmt.Sprintf(`<h2>Password Reset</h2>
<p>Click the link below to reset your password:</p>
<p><a href="%s" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link into your browser:</p>
<p>%s</p>
<p>This link expires in 1 hour. If you did not request this, ignore this email.</p>`, link, link)

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
