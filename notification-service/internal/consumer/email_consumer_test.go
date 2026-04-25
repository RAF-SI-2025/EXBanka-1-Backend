package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestEmailConsumer_HandleMessage_HappyPath(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubEmailSentPublisher{}
	c := newEmailConsumerForTest(sender, pub)

	payload := kafkamsg.SendEmailMessage{
		To:        "user@example.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data: map[string]string{
			"first_name": "Alice",
			"link":       "http://localhost:5173/activate?token=abc",
		},
	}

	c.handleMessage(context.Background(), mustMarshal(t, payload))

	assert.Equal(t, 1, sender.sentCount())
	last, ok := sender.lastCall()
	require.True(t, ok)
	assert.Equal(t, "user@example.com", last.To)
	assert.Equal(t, "Activate Your EXBanka Account", last.Subject)
	assert.Contains(t, last.Body, "Alice")

	confirm, ok := pub.lastConfirm()
	require.True(t, ok)
	assert.Equal(t, "user@example.com", confirm.To)
	assert.Equal(t, kafkamsg.EmailTypeActivation, confirm.EmailType)
	assert.True(t, confirm.Success)
	assert.Empty(t, confirm.Error)
}

func TestEmailConsumer_HandleMessage_TestAddressSkipsSend(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubEmailSentPublisher{}
	c := newEmailConsumerForTest(sender, pub)

	payload := kafkamsg.SendEmailMessage{
		To:        "alice+test@example.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data:      map[string]string{"first_name": "Alice"},
	}

	c.handleMessage(context.Background(), mustMarshal(t, payload))

	// Test addresses MUST NOT trigger an SMTP send …
	assert.Equal(t, 0, sender.sentCount())
	// … but a success confirmation is still published.
	confirm, ok := pub.lastConfirm()
	require.True(t, ok)
	assert.Equal(t, "alice+test@example.com", confirm.To)
	assert.True(t, confirm.Success)
}

func TestEmailConsumer_HandleMessage_SendFailurePublishesFailure(t *testing.T) {
	wantErr := errors.New("smtp down")
	sender := &stubEmailSender{sendErr: wantErr}
	pub := &stubEmailSentPublisher{}
	c := newEmailConsumerForTest(sender, pub)

	payload := kafkamsg.SendEmailMessage{
		To:        "user@example.com",
		EmailType: kafkamsg.EmailTypePasswordReset,
		Data:      map[string]string{"link": "http://localhost:5173/reset?token=x"},
	}

	c.handleMessage(context.Background(), mustMarshal(t, payload))

	assert.Equal(t, 1, sender.sentCount())
	confirm, ok := pub.lastConfirm()
	require.True(t, ok)
	assert.False(t, confirm.Success)
	assert.Contains(t, confirm.Error, "smtp down")
}

func TestEmailConsumer_HandleMessage_MalformedJSONIsIgnored(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubEmailSentPublisher{}
	c := newEmailConsumerForTest(sender, pub)

	c.handleMessage(context.Background(), []byte("{this is not json"))

	assert.Equal(t, 0, sender.sentCount())
	assert.Equal(t, 0, pub.callCounter)
}

func TestEmailConsumer_HandleMessage_PublishFailureDoesNotPanic(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubEmailSentPublisher{publishErr: errors.New("kafka unavailable")}
	c := newEmailConsumerForTest(sender, pub)

	payload := kafkamsg.SendEmailMessage{
		To:        "u@example.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data:      map[string]string{"first_name": "X", "link": "http://x"},
	}

	// Must not panic even when the kafka publish fails.
	require.NotPanics(t, func() {
		c.handleMessage(context.Background(), mustMarshal(t, payload))
	})
	assert.Equal(t, 1, sender.sentCount())
	assert.Equal(t, 1, pub.callCounter)
}

func TestEmailConsumer_HandleMessage_TestAddressPublishFailureDoesNotPanic(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubEmailSentPublisher{publishErr: errors.New("kafka unavailable")}
	c := newEmailConsumerForTest(sender, pub)

	payload := kafkamsg.SendEmailMessage{
		To:        "u+test@example.com",
		EmailType: kafkamsg.EmailTypeActivation,
		Data:      map[string]string{"first_name": "X", "link": "http://x"},
	}

	require.NotPanics(t, func() {
		c.handleMessage(context.Background(), mustMarshal(t, payload))
	})
	assert.Equal(t, 0, sender.sentCount())
	assert.Equal(t, 1, pub.callCounter)
}

func TestIsTestAddress(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"alice+test@example.com", true},
		{"a+test@x.io", true},
		{"+test@example.com", true}, // local part "+test" still contains "+test"
		{"alice@example.com", false},
		{"no-at-sign", false},
		{"", false},
		{"alice+nottest@example.com", false},
		{"@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, isTestAddress(tc.input))
		})
	}
}
