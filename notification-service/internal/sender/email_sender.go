package sender

import (
	"fmt"
	"net/smtp"
)

type EmailSender struct {
	host     string
	port     string
	user     string
	password string
	from     string
}

func NewEmailSender(host, port, user, password, from string) *EmailSender {
	return &EmailSender{
		host:     host,
		port:     port,
		user:     user,
		password: password,
		from:     from,
	}
}

func (s *EmailSender) Send(to, subject, body string) error {
	auth := smtp.PlainAuth("", s.user, s.password, s.host)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n%s",
		s.from, to, subject, body)

	addr := s.host + ":" + s.port
	return smtp.SendMail(addr, auth, s.from, []string{to}, []byte(msg))
}
