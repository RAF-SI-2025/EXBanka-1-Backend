package kafka

import (
	"context"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared"
)

// Producer wraps the shared Kafka producer with verification-service typed
// publish methods.
type Producer struct {
	inner *shared.Producer
}

func NewProducer(brokers string) *Producer {
	return &Producer{inner: shared.NewProducer(brokers)}
}

func (p *Producer) Close() error { return p.inner.Close() }

// PublishChallengeCreated publishes when a new challenge is created for mobile delivery.
func (p *Producer) PublishChallengeCreated(ctx context.Context, msg kafkamsg.VerificationChallengeCreatedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicVerificationChallengeCreated, msg)
}

// PublishChallengeVerified publishes when a challenge is successfully verified.
func (p *Producer) PublishChallengeVerified(ctx context.Context, msg kafkamsg.VerificationChallengeVerifiedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicVerificationChallengeVerified, msg)
}

// PublishChallengeFailed publishes when a challenge fails (max attempts or expired).
func (p *Producer) PublishChallengeFailed(ctx context.Context, msg kafkamsg.VerificationChallengeFailedMessage) error {
	return p.inner.Publish(ctx, kafkamsg.TopicVerificationChallengeFailed, msg)
}
