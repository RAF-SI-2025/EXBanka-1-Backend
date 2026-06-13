package handler_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/interbank-service/internal/handler"
)

// fakeStockOTCMutators records the remaining three peer-facing OTC RPCs the base
// forwarder test does not exercise (Update/Get/Delete negotiation).
type fakeStockOTCMutators struct {
	stockpb.PeerOTCServiceClient
	gotUpdate *stockpb.UpdateNegotiationRequest
	gotGet    *stockpb.GetNegotiationRequest
	gotDelete *stockpb.DeleteNegotiationRequest
	deleteErr error
}

func (f *fakeStockOTCMutators) UpdateNegotiation(_ context.Context, in *stockpb.UpdateNegotiationRequest, _ ...grpc.CallOption) (*stockpb.UpdateNegotiationResponse, error) {
	f.gotUpdate = in
	return &stockpb.UpdateNegotiationResponse{}, nil
}
func (f *fakeStockOTCMutators) GetNegotiation(_ context.Context, in *stockpb.GetNegotiationRequest, _ ...grpc.CallOption) (*stockpb.GetNegotiationResponse, error) {
	f.gotGet = in
	return &stockpb.GetNegotiationResponse{}, nil
}
func (f *fakeStockOTCMutators) DeleteNegotiation(_ context.Context, in *stockpb.DeleteNegotiationRequest, _ ...grpc.CallOption) (*stockpb.DeleteNegotiationResponse, error) {
	f.gotDelete = in
	return &stockpb.DeleteNegotiationResponse{}, f.deleteErr
}

// TestPeerOTCForwarder_UpdateGetDelete verifies the Update/Get/Delete
// negotiation RPCs are transparently forwarded to stock-service, and that an
// error from stock-service propagates back to the caller.
func TestPeerOTCForwarder_UpdateGetDelete(t *testing.T) {
	fake := &fakeStockOTCMutators{}
	f := handler.NewPeerOTCForwarder(fake)
	ctx := context.Background()

	if _, err := f.UpdateNegotiation(ctx, &stockpb.UpdateNegotiationRequest{PeerBankCode: "222"}); err != nil {
		t.Fatalf("UpdateNegotiation: %v", err)
	}
	if fake.gotUpdate == nil || fake.gotUpdate.GetPeerBankCode() != "222" {
		t.Errorf("UpdateNegotiation not forwarded: %+v", fake.gotUpdate)
	}

	if _, err := f.GetNegotiation(ctx, &stockpb.GetNegotiationRequest{PeerBankCode: "333"}); err != nil {
		t.Fatalf("GetNegotiation: %v", err)
	}
	if fake.gotGet == nil || fake.gotGet.GetPeerBankCode() != "333" {
		t.Errorf("GetNegotiation not forwarded: %+v", fake.gotGet)
	}

	if _, err := f.DeleteNegotiation(ctx, &stockpb.DeleteNegotiationRequest{PeerBankCode: "444"}); err != nil {
		t.Fatalf("DeleteNegotiation: %v", err)
	}
	if fake.gotDelete == nil || fake.gotDelete.GetPeerBankCode() != "444" {
		t.Errorf("DeleteNegotiation not forwarded: %+v", fake.gotDelete)
	}

	// Error propagation: stock-service error flows straight back.
	fake.deleteErr = errors.New("stock down")
	if _, err := f.DeleteNegotiation(ctx, &stockpb.DeleteNegotiationRequest{PeerBankCode: "444"}); err == nil {
		t.Errorf("expected stock-service error to propagate")
	}
}
