package push

import (
	"context"
	"log"
)

// NoopProvider logs push notifications instead of sending them.
// Use during development until a real provider (FCM/APNs) is integrated.
type NoopProvider struct{}

func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

func (p *NoopProvider) Send(ctx context.Context, deviceToken string, title, body string, data map[string]string) error {
	log.Printf("[PUSH/NOOP] would send to %s: title=%q body=%q data=%v", deviceToken, title, body, data)
	return nil
}

func (p *NoopProvider) Name() string {
	return "noop"
}
