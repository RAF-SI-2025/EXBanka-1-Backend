package handler

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/service"
)

// mapOTCError is now a passthrough; the wire status is determined by the
// sentinel embedded in the wrapped error.
func TestMapOTCError_SentinelPassthrough(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		want     codes.Code
	}{
		{"OTCOfferNotFound", service.ErrOTCOfferNotFound, codes.NotFound},
		{"OTCBuyOwnOffer", service.ErrOTCBuyOwnOffer, codes.PermissionDenied},
		{"OTCInsufficientPublicQuantity", service.ErrOTCInsufficientPublicQuantity, codes.FailedPrecondition},
		{"OTCBuyerAccountNotFound", service.ErrOTCBuyerAccountNotFound, codes.FailedPrecondition},
		{"OTCSellerAccountNotFound", service.ErrOTCSellerAccountNotFound, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Op: %w", tc.sentinel)
			err := mapOTCError(wrapped)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("errors.Is should match")
			}
			if got := status.Code(err); got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}
