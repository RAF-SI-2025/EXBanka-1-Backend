package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func futureRFC3339(d time.Duration) string {
	return time.Now().Add(d).UTC().Format(time.RFC3339)
}

func TestVerificationConsumer_EmailDelivery_HappyPath(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	display, err := json.Marshal(map[string]string{
		"code":  "123456",
		"email": "user@example.com",
	})
	require.NoError(t, err)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          42,
		Method:          "code_pull",
		DisplayData:     string(display),
		DeliveryChannel: "email",
		ExpiresAt:       futureRFC3339(5 * time.Minute),
	}
	c.handleMessage(context.Background(), mustMarshal(t, event))

	require.Equal(t, 1, sender.sentCount())
	got, ok := sender.lastCall()
	require.True(t, ok)
	assert.Equal(t, "user@example.com", got.To)
	assert.Contains(t, got.Body, "123456", "verification code must appear in email body")

	// Email-delivery path must NOT touch the inbox or push topic.
	assert.Empty(t, repo.created)
	assert.Equal(t, 0, pub.callCount())
}

func TestVerificationConsumer_EmailDelivery_MissingEmailIsSkipped(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	display, err := json.Marshal(map[string]string{"code": "123456"})
	require.NoError(t, err)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          42,
		Method:          "code_pull",
		DisplayData:     string(display),
		DeliveryChannel: "email",
		ExpiresAt:       futureRFC3339(5 * time.Minute),
	}
	c.handleMessage(context.Background(), mustMarshal(t, event))

	assert.Equal(t, 0, sender.sentCount())
	assert.Empty(t, repo.created)
}

func TestVerificationConsumer_EmailDelivery_SendErrorDoesNotPanic(t *testing.T) {
	sender := &stubEmailSender{sendErr: errors.New("smtp")}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	display, err := json.Marshal(map[string]string{
		"code":  "999000",
		"email": "u@example.com",
	})
	require.NoError(t, err)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          1,
		Method:          "code_pull",
		DisplayData:     string(display),
		DeliveryChannel: "email",
		ExpiresAt:       futureRFC3339(5 * time.Minute),
	}
	require.NotPanics(t, func() {
		c.handleMessage(context.Background(), mustMarshal(t, event))
	})
	assert.Equal(t, 1, sender.sentCount())
}

func TestVerificationConsumer_MobileDelivery_HappyPath(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	display, err := json.Marshal(map[string]interface{}{
		"code":     "555888",
		"some_key": "some_val",
	})
	require.NoError(t, err)

	expiresAt := futureRFC3339(2 * time.Minute)
	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     77,
		UserID:          11,
		Method:          "qr_scan",
		DisplayData:     string(display),
		DeliveryChannel: "mobile",
		ExpiresAt:       expiresAt,
	}

	c.handleMessage(context.Background(), mustMarshal(t, event))

	// Inbox row created with all fields populated.
	require.Len(t, repo.created, 1)
	item := repo.created[0]
	assert.Equal(t, uint64(11), item.UserID)
	assert.Equal(t, uint64(77), item.ChallengeID)
	assert.Equal(t, "qr_scan", item.Method)
	assert.JSONEq(t, string(display), string(item.DisplayData))
	assert.WithinDuration(t, mustParseRFC(t, expiresAt), item.ExpiresAt, time.Second)

	// One push message published on the mobile-push topic.
	require.Equal(t, 1, pub.callCount())
	assert.Equal(t, kafkamsg.TopicMobilePush, pub.topics[0])
	pushMsg, ok := pub.payloads[0].(kafkamsg.MobilePushMessage)
	require.True(t, ok, "expected MobilePushMessage payload")
	assert.Equal(t, uint64(11), pushMsg.UserID)
	assert.Equal(t, "verification_challenge", pushMsg.Type)
	assert.NotEmpty(t, pushMsg.Payload)

	// Email path untouched.
	assert.Equal(t, 0, sender.sentCount())
}

func TestVerificationConsumer_MobileDelivery_InvalidExpiresAt(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          1,
		Method:          "code_pull",
		DisplayData:     `{"code":"x"}`,
		DeliveryChannel: "mobile",
		ExpiresAt:       "not-a-date",
	}
	c.handleMessage(context.Background(), mustMarshal(t, event))

	assert.Empty(t, repo.created)
	assert.Equal(t, 0, pub.callCount())
}

func TestVerificationConsumer_MobileDelivery_RepoErrorSkipsPush(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{createErr: errors.New("db unavailable")}
	c := newVerificationConsumerForTest(sender, pub, repo)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          1,
		Method:          "code_pull",
		DisplayData:     `{"code":"x"}`,
		DeliveryChannel: "mobile",
		ExpiresAt:       futureRFC3339(2 * time.Minute),
	}
	c.handleMessage(context.Background(), mustMarshal(t, event))

	assert.Empty(t, repo.created)
	assert.Equal(t, 0, pub.callCount(), "push must not be published if inbox create failed")
}

func TestVerificationConsumer_MobileDelivery_PushFailureDoesNotPanic(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{publishErr: errors.New("kafka")}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          1,
		Method:          "code_pull",
		DisplayData:     `{"code":"x"}`,
		DeliveryChannel: "mobile",
		ExpiresAt:       futureRFC3339(2 * time.Minute),
	}
	require.NotPanics(t, func() {
		c.handleMessage(context.Background(), mustMarshal(t, event))
	})
	require.Len(t, repo.created, 1, "inbox row created even when push publish fails")
}

func TestVerificationConsumer_HandleMessage_UnknownChannel(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	event := kafkamsg.VerificationChallengeCreatedMessage{
		ChallengeID:     1,
		UserID:          1,
		Method:          "code_pull",
		DisplayData:     `{"code":"x"}`,
		DeliveryChannel: "carrier_pigeon",
		ExpiresAt:       futureRFC3339(time.Minute),
	}
	c.handleMessage(context.Background(), mustMarshal(t, event))

	assert.Equal(t, 0, sender.sentCount())
	assert.Empty(t, repo.created)
	assert.Equal(t, 0, pub.callCount())
}

func TestVerificationConsumer_HandleMessage_MalformedJSON(t *testing.T) {
	sender := &stubEmailSender{}
	pub := &stubGenericPublisher{}
	repo := &stubInboxItemCreator{}
	c := newVerificationConsumerForTest(sender, pub, repo)

	c.handleMessage(context.Background(), []byte("{this is not"))

	assert.Equal(t, 0, sender.sentCount())
	assert.Empty(t, repo.created)
	assert.Equal(t, 0, pub.callCount())
}

func mustParseRFC(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}
