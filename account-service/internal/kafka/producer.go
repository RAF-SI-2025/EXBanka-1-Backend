package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// AccountStatusChangedMsg is the legacy in-service shape for status-changed
// events. Defined here (not in contract/kafka) because only account-service
// publishes this with the email enrichment.
type AccountStatusChangedMsg struct {
	AccountNumber string `json:"account_number"`
	Status        string `json:"status"`
	OwnerEmail    string `json:"owner_email"`
}

// Producer wraps the shared Kafka producer with account-service typed
// publish methods. Construct with NewProducer; defer Close in main.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishAccountCreated(ctx context.Context, msg kafkamsg.AccountCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicAccountCreated, msg)
}

func (p *Producer) PublishAccountStatusChanged(ctx context.Context, msg AccountStatusChangedMsg) error {
	return p.inner.Publish(ctx, kafkamsg.TopicAccountStatusChanged, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishAccountNameUpdated(ctx context.Context, msg kafkamsg.AccountNameUpdatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicAccountNameUpdated, msg)
}

func (p *Producer) PublishAccountLimitsUpdated(ctx context.Context, msg kafkamsg.AccountLimitsUpdatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicAccountLimitsUpdated, msg)
}

func (p *Producer) PublishMaintenanceFeeCharged(ctx context.Context, msg kafkamsg.MaintenanceFeeChargedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicMaintenanceFeeCharged, msg)
}

func (p *Producer) PublishSpendingReset(ctx context.Context, msg kafkamsg.SpendingResetMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSpendingReset, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}
