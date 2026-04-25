package consumer

import (
	"errors"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneralNotificationConsumer_HandleMessage_HappyPath(t *testing.T) {
	repo := &stubGeneralNotificationCreator{}
	c := newGeneralNotificationConsumerForTest(repo)

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
	c := newGeneralNotificationConsumerForTest(repo)

	c.handleMessage([]byte("not json"))
	assert.Empty(t, repo.created)
}

func TestGeneralNotificationConsumer_HandleMessage_RepoErrorSwallowed(t *testing.T) {
	repo := &stubGeneralNotificationCreator{createErr: errors.New("db boom")}
	c := newGeneralNotificationConsumerForTest(repo)

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
