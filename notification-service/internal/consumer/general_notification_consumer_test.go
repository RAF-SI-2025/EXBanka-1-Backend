package consumer

import (
	"errors"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubGeneralRenderer implements the templateRenderer interface used by
// GeneralNotificationConsumer (declared in email_consumer.go, same package).
type stubGeneralRenderer struct {
	subject, body string
	err           error
	gotType       string
	gotChannel    string
	gotData       map[string]string
}

func (s *stubGeneralRenderer) Render(typ, channel string, data map[string]string) (string, string, error) {
	s.gotType, s.gotChannel, s.gotData = typ, channel, data
	return s.subject, s.body, s.err
}

func TestGeneralNotificationConsumer_HandleMessage_HappyPath(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	c := newGeneralNotificationConsumerForTest(repo, &stubGeneralRenderer{subject: "T", body: "M"})

	payload := kafkamsg.GeneralNotificationMessage{
		UserID:  42,
		Type:    "money_received",
		Title:   "You received money",
		Message: "1000 RSD has been credited to your account",
		RefType: "payment",
		RefID:   1234,
	}

	c.handleMessage(mustMarshal(t, payload))

	require.Len(t, repo.created, 1)
	got := repo.created[0]
	assert.Equal(t, uint64(42), got.UserID)
	assert.Equal(t, "money_received", got.Type)
	assert.Equal(t, "You received money", got.Title)
	assert.Equal(t, "1000 RSD has been credited to your account", got.Message)
	assert.Equal(t, "payment", got.RefType)
	assert.Equal(t, uint64(1234), got.RefID)
}

func TestGeneralNotificationConsumer_HandleMessage_MalformedPayloadIsIgnored(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	c := newGeneralNotificationConsumerForTest(repo, &stubGeneralRenderer{subject: "T", body: "M"})

	c.handleMessage([]byte("not json"))
	assert.Empty(t, repo.created)
}

func TestGeneralNotificationConsumer_HandleMessage_RepoError_LogsAndDoesNotPanic(t *testing.T) {
	repo := &stubGeneralNotificationCreator{createErr: errors.New("db boom")}
	c := newGeneralNotificationConsumerForTest(repo, &stubGeneralRenderer{subject: "T", body: "M"})

	payload := kafkamsg.GeneralNotificationMessage{
		UserID:  7,
		Type:    "loan_approved",
		Title:   "Approved",
		Message: "Loan accepted",
	}

	require.NotPanics(t, func() {
		c.handleMessage(mustMarshal(t, payload))
	})
	// Repo error means nothing is recorded as "created".
	assert.Empty(t, repo.created)
}

func TestGeneralNotificationConsumer_HandleMessage_DataRendersViaRegistry(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{subject: "Order filled", body: "Your buy order for 10 AAPL filled."}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID:  42,
		Type:    "ORDER_FILLED",
		Data:    map[string]string{"ticker": "AAPL", "quantity": "10", "direction": "buy"},
		RefType: "order",
		RefID:   7,
	}
	c.handleMessage(mustMarshal(t, payload))

	require.Len(t, repo.created, 1)
	got := repo.created[0]
	assert.Equal(t, "ORDER_FILLED", got.Type)
	assert.Equal(t, "Order filled", got.Title)
	assert.Equal(t, "Your buy order for 10 AAPL filled.", got.Message)
	assert.Equal(t, "order", got.RefType)
	assert.Equal(t, uint64(7), got.RefID)
	assert.Equal(t, "ORDER_FILLED", rend.gotType)
	assert.Equal(t, "push", rend.gotChannel)
	assert.Equal(t, "AAPL", rend.gotData["ticker"])
}

func TestGeneralNotificationConsumer_HandleMessage_LegacyTitleMessageStillWorks(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{err: errors.New("renderer must not be called for legacy messages")}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID: 1, Type: "password_changed", Title: "Password changed", Message: "Your password was changed.",
	}
	c.handleMessage(mustMarshal(t, payload))

	require.Len(t, repo.created, 1)
	assert.Equal(t, "Password changed", repo.created[0].Title)
	assert.Equal(t, "", rend.gotType, "renderer should not be invoked when Data is empty")
}

func TestGeneralNotificationConsumer_HandleMessage_RenderErrorDropsMessage(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	rend := &stubGeneralRenderer{err: errors.New("unknown type")}
	c := newGeneralNotificationConsumerForTest(repo, rend)

	payload := kafkamsg.GeneralNotificationMessage{
		UserID: 5, Type: "NOPE", Data: map[string]string{"x": "y"},
	}
	require.NotPanics(t, func() { c.handleMessage(mustMarshal(t, payload)) })
	assert.Empty(t, repo.created, "a render error drops the message (nothing stored)")
}
