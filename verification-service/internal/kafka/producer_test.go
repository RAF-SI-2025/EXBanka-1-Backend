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

	_ = p.PublishChallengeCreated(ctx, kafkamsg.VerificationChallengeCreatedMessage{})
	_ = p.PublishChallengeVerified(ctx, kafkamsg.VerificationChallengeVerifiedMessage{})
	_ = p.PublishChallengeFailed(ctx, kafkamsg.VerificationChallengeFailedMessage{})
}
