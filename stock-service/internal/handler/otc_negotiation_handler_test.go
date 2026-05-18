package handler

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

// TestNegotiationRPCs_UnimplementedWhenServiceMissing verifies that
// every Phase-2 negotiation RPC returns Unimplemented when the
// OTCNegotiationService was not wired (WithNegotiations never called).
// Without these checks, the handler would nil-deref on the first call.
func TestNegotiationRPCs_UnimplementedWhenServiceMissing(t *testing.T) {
	h := NewOTCOptionsHandler(nil, nil) // no negotiations svc

	cases := []struct {
		name string
		call func() error
	}{
		{"OpenNegotiation", func() error {
			_, err := h.OpenNegotiation(context.Background(), &stockpb.OpenNegotiationRequest{})
			return err
		}},
		{"CounterNegotiation", func() error {
			_, err := h.CounterNegotiation(context.Background(), &stockpb.CounterNegotiationRequest{})
			return err
		}},
		{"AcceptNegotiationChain", func() error {
			_, err := h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{})
			return err
		}},
		{"RejectNegotiation", func() error {
			_, err := h.RejectNegotiation(context.Background(), &stockpb.RejectNegotiationRequest{})
			return err
		}},
		{"CancelNegotiation", func() error {
			_, err := h.CancelNegotiation(context.Background(), &stockpb.CancelNegotiationRequest{})
			return err
		}},
		{"ListMyNegotiations", func() error {
			_, err := h.ListMyNegotiations(context.Background(), &stockpb.ListMyNegotiationsRequest{})
			return err
		}},
		{"ListNegotiationsByListing", func() error {
			_, err := h.ListNegotiationsByListing(context.Background(), &stockpb.ListNegotiationsByListingRequest{})
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			st, ok := status.FromError(err)
			if !ok || st.Code() != codes.Unimplemented {
				t.Errorf("got %v want Unimplemented", err)
			}
		})
	}
}

// TestNegotiationHandler_ValidatesOwnerType verifies the handler
// rejects invalid owner_type strings BEFORE forwarding to the service.
func TestNegotiationHandler_ValidatesOwnerType(t *testing.T) {
	// Wire just enough — a non-nil service so the Unimplemented check
	// passes and we get to the validation path.
	svc := &service.OTCNegotiationService{}
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(svc)

	_, err := h.OpenNegotiation(context.Background(), &stockpb.OpenNegotiationRequest{
		BidderOwnerType: "not-a-real-type",
		BidderOwnerId:   1,
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("invalid owner_type: got %v want InvalidArgument", err)
	}
}

// TestNegotiationHandler_RejectsClientWithZeroOwnerID guards against
// the common "0 means bank" bug: client owner with owner_id=0 should
// be rejected up-front instead of writing a non-conformant row.
func TestNegotiationHandler_RejectsClientWithZeroOwnerID(t *testing.T) {
	svc := &service.OTCNegotiationService{}
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(svc)

	_, err := h.OpenNegotiation(context.Background(), &stockpb.OpenNegotiationRequest{
		BidderOwnerType: "client",
		BidderOwnerId:   0,
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("zero owner_id for client: got %v want InvalidArgument", err)
	}
}

// TestNegotiationHandler_RejectsBadDecimal guards the parseDecimalArg
// helper on the OpenNegotiation path.
func TestNegotiationHandler_RejectsBadDecimal(t *testing.T) {
	svc := &service.OTCNegotiationService{}
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(svc)

	_, err := h.OpenNegotiation(context.Background(), &stockpb.OpenNegotiationRequest{
		BidderOwnerType: "client",
		BidderOwnerId:   7,
		Quantity:        "not-a-decimal",
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("bad decimal quantity: got %v want InvalidArgument", err)
	}
}

// Sanity check that the OptionalPtr helper roundtrips correctly: 0 → nil,
// non-zero → &v. Critical because acting_employee_id=0 should NOT be
// persisted as employee id 0 (which would be a real foreign key target
// in some setups).
func TestOptionalPtr(t *testing.T) {
	if optionalPtr(0) != nil {
		t.Errorf("optionalPtr(0) should return nil")
	}
	p := optionalPtr(42)
	if p == nil || *p != 42 {
		t.Errorf("optionalPtr(42) = %v want &42", p)
	}
}

// Sanity that a typed sentinel error still parses as a gRPC status
// (the embedded UnimplementedOTCOptionsServiceServer path).
func TestUnimplementedSentinelIsGRPCError(t *testing.T) {
	h := NewOTCOptionsHandler(nil, nil)
	_, err := h.OpenNegotiation(context.Background(), &stockpb.OpenNegotiationRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := status.FromError(err); !ok {
		t.Errorf("error %v is not a gRPC status error", err)
	}
	// Also assert errors.Is unwraps to a Go error.
	if errors.Is(err, nil) {
		t.Errorf("err.Is(nil) returned true — sentinel chain broken")
	}
}
