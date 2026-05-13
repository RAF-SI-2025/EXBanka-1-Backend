package kafka

import (
	"context"
	"testing"

	kafkamsg "github.com/exbanka/contract/kafka"
)

func TestProducer_PublishMethods(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = p.Publish(ctx, "any.topic", map[string]string{"k": "v"})
	_ = p.PublishEmailSent(ctx, kafkamsg.EmailSentMessage{})
}
