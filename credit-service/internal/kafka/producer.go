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

func (p *Producer) publish(ctx context.Context, topic string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

func (p *Producer) PublishLoanRequested(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.publish(ctx, kafkamsg.TopicLoanRequested, msg)
}

func (p *Producer) PublishLoanApproved(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.publish(ctx, kafkamsg.TopicLoanApproved, msg)
}

func (p *Producer) PublishLoanRejected(ctx context.Context, msg kafkamsg.LoanStatusMessage) error {
	return p.publish(ctx, kafkamsg.TopicLoanRejected, msg)
}

func (p *Producer) PublishLoanDisbursed(ctx context.Context, msg kafkamsg.LoanDisbursedMessage) error {
	return p.publish(ctx, kafkamsg.TopicLoanDisbursed, msg)
}

func (p *Producer) PublishInstallmentCollected(ctx context.Context, msg kafkamsg.InstallmentResultMessage) error {
	return p.publish(ctx, kafkamsg.TopicInstallmentCollected, msg)
}

func (p *Producer) PublishInstallmentFailed(ctx context.Context, msg kafkamsg.InstallmentResultMessage) error {
	return p.publish(ctx, kafkamsg.TopicInstallmentFailed, msg)
}

func (p *Producer) SendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
}

func (p *Producer) PublishVariableRateAdjusted(ctx context.Context, msg kafkamsg.VariableRateAdjustedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVariableRateAdjusted, msg)
}

func (p *Producer) PublishLatePenaltyApplied(ctx context.Context, msg kafkamsg.LatePenaltyAppliedMessage) error {
	return p.publish(ctx, kafkamsg.TopicLatePenaltyApplied, msg)
}

func (p *Producer) PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error {
	return p.publish(ctx, kafkamsg.TopicGeneralNotification, msg)
}

func (p *Producer) Close() error {
	return p.writer.Close()
}
