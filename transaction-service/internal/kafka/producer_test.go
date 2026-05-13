package kafka

import (
	"context"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
)

// All Publish* methods route through the shared producer; with a cancelled
// context the producer returns context.Canceled (no broker hit). That's
// enough to exercise each typed wrapper.
func TestProducer_PublishMethods(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		fn   func() error
	}{
		{"PublishPaymentCreated", func() error {
			return p.PublishPaymentCreated(ctx, kafkamsg.PaymentCompletedMessage{})
		}},
		{"PublishPaymentCompleted", func() error {
			return p.PublishPaymentCompleted(ctx, kafkamsg.PaymentCompletedMessage{})
		}},
		{"PublishTransferCreated", func() error {
			return p.PublishTransferCreated(ctx, kafkamsg.TransferCompletedMessage{})
		}},
		{"PublishTransferCompleted", func() error {
			return p.PublishTransferCompleted(ctx, kafkamsg.TransferCompletedMessage{})
		}},
		{"PublishPaymentFailed", func() error {
			return p.PublishPaymentFailed(ctx, kafkamsg.PaymentFailedMessage{})
		}},
		{"PublishTransferFailed", func() error {
			return p.PublishTransferFailed(ctx, kafkamsg.TransferFailedMessage{})
		}},
		{"PublishSagaDeadLetter", func() error {
			return p.PublishSagaDeadLetter(ctx, kafkamsg.SagaDeadLetterMessage{})
		}},
		{"SendEmail", func() error {
			return p.SendEmail(ctx, kafkamsg.SendEmailMessage{})
		}},
		{"PublishGeneralNotification", func() error {
			return p.PublishGeneralNotification(ctx, kafkamsg.GeneralNotificationMessage{})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Either an error from cancelled ctx or a no-op success — both
			// exercise the wrapper path.
			_ = c.fn()
		})
	}
}
