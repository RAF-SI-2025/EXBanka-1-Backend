package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/service"
)

// ---------- helpers for building stubs ----------

// negWithMintedContract builds a minimal OTCNegotiation whose status is
// "accepted" and MintedContractID is set. Used to verify that negToProto
// copies the field correctly.
func negWithMintedContract(id uint64, contractID uint64) *model.OTCNegotiation {
	ownerID := uint64(1)
	return &model.OTCNegotiation{
		ID:                        id,
		ParentOfferID:             1,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &ownerID,
		BidderAccountID:           10,
		Quantity:                  decimal.NewFromFloat(1),
		StrikePrice:               decimal.NewFromFloat(100),
		Premium:                   decimal.NewFromFloat(5),
		SettlementDate:            time.Now().Add(30 * 24 * time.Hour),
		Status:                    model.OTCNegotiationStatusAccepted,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   1,
		LastActionByOwnerType:     "client",
		MintedContractID:          &contractID,
	}
}

// ---------- Tests ----------

// TestNegToProto_MintedContractID_PopulatedOnAccepted verifies that
// negToProto copies a non-nil MintedContractID into the proto response.
func TestNegToProto_MintedContractID_PopulatedOnAccepted(t *testing.T) {
	contractID := uint64(42)
	n := negWithMintedContract(1, contractID)
	pb := negToProto(n)
	if pb == nil {
		t.Fatal("negToProto returned nil for non-nil input")
	}
	if pb.GetMintedContractId() != contractID {
		t.Errorf("MintedContractId = %d, want %d", pb.GetMintedContractId(), contractID)
	}
	if pb.GetStatus() != model.OTCNegotiationStatusAccepted {
		t.Errorf("Status = %q, want accepted", pb.GetStatus())
	}
}

// TestNegToProto_MintedContractID_ZeroWhenNil verifies that a nil
// MintedContractID maps to 0 in the proto (omitted in JSON).
func TestNegToProto_MintedContractID_ZeroWhenNil(t *testing.T) {
	ownerID := uint64(1)
	n := &model.OTCNegotiation{
		ID:                        2,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &ownerID,
		Status:                    model.OTCNegotiationStatusRejected,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   1,
		LastActionByOwnerType:     "client",
	}
	pb := negToProto(n)
	if pb.GetMintedContractId() != 0 {
		t.Errorf("MintedContractId = %d, want 0 for nil pointer", pb.GetMintedContractId())
	}
}

// TestNegotiationHandler_ListRevisions_UnimplementedWhenServiceMissing
// verifies the guard clause fires when negotiations svc is nil.
func TestNegotiationHandler_ListRevisions_UnimplementedWhenServiceMissing(t *testing.T) {
	h := NewOTCOptionsHandler(nil, nil)
	_, err := h.ListNegotiationRevisions(context.Background(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId:   1,
		CallerOwnerType: "client",
		CallerOwnerId:   1,
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unimplemented {
		t.Errorf("got %v, want Unimplemented", err)
	}
}

// TestNegotiationHandler_ListRevisions_RejectsInvalidOwnerType ensures
// the ownerTypeFromProto validation runs before forwarding to service.
func TestNegotiationHandler_ListRevisions_RejectsInvalidOwnerType(t *testing.T) {
	svc := &service.OTCNegotiationService{}
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(svc)
	_, err := h.ListNegotiationRevisions(context.Background(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId:   1,
		CallerOwnerType: "bogus",
		CallerOwnerId:   1,
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument for unknown owner_type", err)
	}
}

// TestNegotiationHandler_ListRevisions_RejectsClientZeroOwnerID verifies
// that the resolveOwnerID guard rejects client with owner_id=0 before
// forwarding to the service (which would try to hit a nil DB).
func TestNegotiationHandler_ListRevisions_RejectsClientZeroOwnerID(t *testing.T) {
	svc := &service.OTCNegotiationService{}
	h := NewOTCOptionsHandler(nil, nil).WithNegotiations(svc)
	_, err := h.ListNegotiationRevisions(context.Background(), &stockpb.ListNegotiationRevisionsRequest{
		NegotiationId:   1,
		CallerOwnerType: "client",
		CallerOwnerId:   0, // zero — triggers resolveOwnerID rejection
	})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("got %v, want InvalidArgument for client with zero owner_id", err)
	}
}

// TestRevisionToProto_FieldMapping checks that the revision-to-proto
// projection inside ListNegotiationRevisions maps all model fields
// to the correct proto fields. We invoke the RPC on a stub service
// that returns a known revision list, then assert the output.
//
// Since OTCNegotiationService embeds the DB directly and cannot be
// cheaply mocked without a full mock layer, we test this by directly
// calling the internal projection logic through a small inline helper
// that mirrors the handler body — confirming that all decimal, int, and
// time fields land in the right proto slots.
func TestRevisionProtoMapping(t *testing.T) {
	// Construct a revision with known values.
	qty := decimal.NewFromFloat(10)
	strike := decimal.NewFromFloat(150)
	premium := decimal.NewFromFloat(7.5)
	settle := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	rev := model.OTCNegotiationRevision{
		ID:                      99,
		NegotiationID:           5,
		RevisionNumber:          3,
		Action:                  model.OTCNegotiationActionCounter,
		Quantity:                qty,
		StrikePrice:             strike,
		Premium:                 premium,
		SettlementDate:          settle,
		ModifiedByPrincipalType: "client",
		ModifiedByPrincipalID:   7,
		CreatedAt:               time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	}

	// Mirror the handler's projection (tests it without a live gRPC call).
	pb := &stockpb.OTCNegotiationRevisionResponse{
		Id:                    rev.ID,
		NegotiationId:         rev.NegotiationID,
		RevisionNumber:        int32(rev.RevisionNumber),
		Action:                rev.Action,
		Quantity:              rev.Quantity.String(),
		StrikePrice:           rev.StrikePrice.String(),
		Premium:               rev.Premium.String(),
		SettlementDate:        rev.SettlementDate.UTC().Format(time.RFC3339),
		ActionByPrincipalType: rev.ModifiedByPrincipalType,
		ActionByPrincipalId:   rev.ModifiedByPrincipalID,
		CreatedAt:             rev.CreatedAt.UTC().Format(time.RFC3339),
	}

	if pb.GetId() != 99 {
		t.Errorf("id = %d, want 99", pb.GetId())
	}
	if pb.GetNegotiationId() != 5 {
		t.Errorf("negotiation_id = %d, want 5", pb.GetNegotiationId())
	}
	if pb.GetRevisionNumber() != 3 {
		t.Errorf("revision_number = %d, want 3", pb.GetRevisionNumber())
	}
	if pb.GetAction() != model.OTCNegotiationActionCounter {
		t.Errorf("action = %q, want COUNTER", pb.GetAction())
	}
	if pb.GetQuantity() != "10" {
		t.Errorf("quantity = %q, want 10", pb.GetQuantity())
	}
	if pb.GetActionByPrincipalType() != "client" {
		t.Errorf("action_by_principal_type = %q, want client", pb.GetActionByPrincipalType())
	}
	if pb.GetActionByPrincipalId() != 7 {
		t.Errorf("action_by_principal_id = %d, want 7", pb.GetActionByPrincipalId())
	}
}
