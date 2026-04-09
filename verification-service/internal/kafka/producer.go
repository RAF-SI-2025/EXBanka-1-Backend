package kafka

import (
	"context"
	"encoding/json"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafkago.Writer
}

func NewProducer(brokers string) *Producer {
	return &Producer{
		writer: &kafkago.Writer{
			Addr:     kafkago.TCP(brokers),
			Balancer: &kafkago.LeastBytes{},
		},
	}
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

func (p *Producer) publish(ctx context.Context, topic string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

// PublishChallengeCreated publishes when a new challenge is created for mobile delivery.
func (p *Producer) PublishChallengeCreated(ctx context.Context, msg kafkamsg.VerificationChallengeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeCreated, msg)
}

// PublishChallengeVerified publishes when a challenge is successfully verified.
func (p *Producer) PublishChallengeVerified(ctx context.Context, msg kafkamsg.VerificationChallengeVerifiedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeVerified, msg)
}

// PublishChallengeFailed publishes when a challenge fails (max attempts or expired).
func (p *Producer) PublishChallengeFailed(ctx context.Context, msg kafkamsg.VerificationChallengeFailedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeFailed, msg)
}

