package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with credit-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

func (p *Producer) PublishLoanRequested(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicLoanRequested, msg)
}

func (p *Producer) PublishLoanApproved(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicLoanApproved, msg)
}

func (p *Producer) PublishLoanRejected(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicLoanRejected, msg)
}

func (p *Producer) PublishLoanDisbursed(ctx context.Context, msg kafkamsg.LoanDisbursedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicLoanDisbursed, msg)
}

func (p *Producer) PublishInstallmentCollected(ctx context.Context, msg kafkamsg.InstallmentResultMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicInstallmentCollected, msg)
}

func (p *Producer) PublishInstallmentFailed(ctx context.Context, msg kafkamsg.InstallmentResultMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicInstallmentFailed, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishVariableRateAdjusted(ctx context.Context, msg kafkamsg.VariableRateAdjustedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicVariableRateAdjusted, msg)
}

func (p *Producer) PublishLatePenaltyApplied(ctx context.Context, msg kafkamsg.LatePenaltyAppliedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicLatePenaltyApplied, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}
