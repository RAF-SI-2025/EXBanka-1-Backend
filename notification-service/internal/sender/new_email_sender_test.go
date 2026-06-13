package sender

import (
	"net/smtp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewEmailSender_UsesRealTransportAndConfig verifies the public constructor
// wires the provided SMTP settings and the default net/smtp transport. We swap
// the transport to a capture func (without changing the configured fields) to
// assert the host/port/from supplied to NewEmailSender are used on Send.
func TestNewEmailSender_WiresConfig(t *testing.T) {
	s := NewEmailSender("mail.local", "2525", "svc@local", "secret", "from@local")
	require.NotNil(t, s)
	require.NotNil(t, s.transport, "constructor must default the transport to net/smtp.SendMail")

	var gotAddr, gotFrom string
	var gotTo []string
	s.transport = func(addr string, _ smtp.Auth, from string, to []string, _ []byte) error {
		gotAddr, gotFrom, gotTo = addr, from, to
		return nil
	}

	require.NoError(t, s.Send("rcpt@local", "Subj", "<b>Body</b>"))
	require.Equal(t, "mail.local:2525", gotAddr)
	require.Equal(t, "from@local", gotFrom)
	require.Equal(t, []string{"rcpt@local"}, gotTo)
}
