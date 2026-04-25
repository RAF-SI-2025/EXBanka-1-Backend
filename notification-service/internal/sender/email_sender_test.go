package sender

import (
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailSender_Send_SuccessPropagatesArgs(t *testing.T) {
	var (
		gotAddr string
		gotFrom string
		gotTo   []string
		gotMsg  []byte
		gotAuth smtp.Auth
	)
	s := newEmailSenderWithTransport("smtp.example.com", "587", "user@example.com", "pw", "noreply@example.com",
		func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			gotAddr = addr
			gotFrom = from
			gotTo = to
			gotMsg = msg
			gotAuth = a
			return nil
		})

	err := s.Send("recipient@example.com", "Hello Subject", "<p>Hi</p>")
	require.NoError(t, err)

	assert.Equal(t, "smtp.example.com:587", gotAddr)
	assert.Equal(t, "noreply@example.com", gotFrom)
	assert.Equal(t, []string{"recipient@example.com"}, gotTo)
	require.NotNil(t, gotAuth, "auth should be PlainAuth, not nil")

	body := string(gotMsg)
	assert.Contains(t, body, "From: noreply@example.com")
	assert.Contains(t, body, "To: recipient@example.com")
	assert.Contains(t, body, "Subject: Hello Subject")
	assert.Contains(t, body, "MIME-Version: 1.0")
	assert.Contains(t, body, "Content-Type: text/html; charset=\"UTF-8\"")
	// Body separated from headers by a blank CRLF line.
	assert.True(t, strings.Contains(body, "\r\n\r\n<p>Hi</p>"), "body content must follow blank line")
}

func TestEmailSender_Send_TransportErrorIsReturned(t *testing.T) {
	wantErr := errors.New("smtp boom")
	s := newEmailSenderWithTransport("smtp.example.com", "587", "u", "p", "f@example.com",
		func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			return wantErr
		})

	err := s.Send("x@example.com", "subj", "body")
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}
