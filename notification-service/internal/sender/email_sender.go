package sender

import (
	"fmt"
	"net/smtp"
)

// MailTransport abstracts the SMTP send operation so callers can inject a
// stub for unit tests. The default implementation forwards to net/smtp.SendMail.
type MailTransport func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

type EmailSender struct {
	host      string
	port      string
	user      string
	password  string
	from      string
	transport MailTransport
}

func NewEmailSender(host, port, user, password, from string) *EmailSender {
	return &EmailSender{
		host:      host,
		port:      port,
		user:      user,
		password:  password,
		from:      from,
		transport: smtp.SendMail,
	}
}

// SetTransport overrides the SMTP transport. Intended for tests.
func (s *EmailSender) SetTransport(t MailTransport) {
	s.transport = t
}

func (s *EmailSender) Send(to, subject, body string) error {
	auth := smtp.PlainAuth("", s.user, s.password, s.host)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n%s",
		s.from, to, subject, body)

	addr := s.host + ":" + s.port
	return s.transport(addr, auth, s.from, []string{to}, []byte(msg))
}
