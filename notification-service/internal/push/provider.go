package push

import "context"

// Provider is the interface for sending push notifications.
// Implement this for Firebase Cloud Messaging, APNs, etc.
type Provider interface {
	// Send sends a push notification to a device.
	Send(ctx context.Context, deviceToken string, title, body string, data map[string]string) error
	// Name returns the provider name (e.g., "fcm", "apns").
	Name() string
}

// DeviceRegistry looks up device tokens for a user.
// Will be backed by a database when push notifications are activated.
type DeviceRegistry interface {
	GetTokens(ctx context.Context, userID int64) ([]string, error)
}
