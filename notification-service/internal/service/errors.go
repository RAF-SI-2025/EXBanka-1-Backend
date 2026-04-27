// Package service: typed sentinel errors for notification-service operations.
//
// Each sentinel embeds a gRPC code via svcerr.SentinelError. Wrapping it
// with fmt.Errorf("...: %w", err, sentinel) preserves the code through
// status.FromError. Handlers therefore become passthrough — no
// string-matching is required to map service errors back to gRPC status.
//
// Note: notification-service has both a gRPC API surface and a Kafka
// consumer. These sentinels apply only to the gRPC surface; the Kafka
// consumer's error handling is internal and out of scope.
package service

import (
	"google.golang.org/grpc/codes"

	"github.com/exbanka/contract/shared/svcerr"
)

var (
	// ErrInboxItemNotFound — the requested mobile inbox item could not be
	// found (or was already delivered).
	ErrInboxItemNotFound = svcerr.New(codes.NotFound, "inbox item not found")

	// ErrNotificationNotFound — the requested general notification could
	// not be found for the given user.
	ErrNotificationNotFound = svcerr.New(codes.NotFound, "notification not found")

	// ErrInboxLookupFailed — DB error while reading the mobile inbox.
	ErrInboxLookupFailed = svcerr.New(codes.Internal, "failed to fetch pending inbox items")

	// ErrNotificationLookupFailed — DB error while listing or counting
	// general notifications.
	ErrNotificationLookupFailed = svcerr.New(codes.Internal, "failed to fetch notifications")

	// ErrNotificationUpdateFailed — DB error while marking notifications
	// as read.
	ErrNotificationUpdateFailed = svcerr.New(codes.Internal, "failed to update notifications")

	// ErrInvalidEmailRequest — required SendEmail field missing or
	// malformed (e.g., empty recipient).
	ErrInvalidEmailRequest = svcerr.New(codes.InvalidArgument, "invalid email request")

	// ErrDeliveryStatusUnimplemented — GetDeliveryStatus is not yet
	// backed by storage.
	ErrDeliveryStatusUnimplemented = svcerr.New(codes.Unimplemented, "delivery status tracking not yet implemented")
)
