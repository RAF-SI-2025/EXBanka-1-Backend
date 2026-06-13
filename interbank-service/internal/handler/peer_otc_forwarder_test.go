package handler_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/interbank-service/internal/handler"
)

// fakeStockOTC embeds the client interface (nil) so it satisfies the type while
// only overriding the peer-facing methods the forwarder calls.
type fakeStockOTC struct {
	stockpb.PeerOTCServiceClient
	gotCreate *stockpb.CreateNegotiationRequest
	gotAccept *stockpb.AcceptNegotiationRequest
	gotStocks *stockpb.GetPublicStocksRequest
}

func (f *fakeStockOTC) GetPublicStocks(_ context.Context, in *stockpb.GetPublicStocksRequest, _ ...grpc.CallOption) (*stockpb.GetPublicStocksResponse, error) {
	f.gotStocks = in
	return &stockpb.GetPublicStocksResponse{}, nil
}
func (f *fakeStockOTC) CreateNegotiation(_ context.Context, in *stockpb.CreateNegotiationRequest, _ ...grpc.CallOption) (*stockpb.CreateNegotiationResponse, error) {
	f.gotCreate = in
	return &stockpb.CreateNegotiationResponse{NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "neg-1"}}, nil
}
func (f *fakeStockOTC) AcceptNegotiation(_ context.Context, in *stockpb.AcceptNegotiationRequest, _ ...grpc.CallOption) (*stockpb.AcceptNegotiationResponse, error) {
	f.gotAccept = in
	return &stockpb.AcceptNegotiationResponse{TransactionId: "tx-9"}, nil
}

// TestPeerOTCForwarder_ForwardsToStock: the peer-facing PeerOTCService RPCs are
// transparently forwarded to stock-service, request in and response out.
func TestPeerOTCForwarder_ForwardsToStock(t *testing.T) {
	fake := &fakeStockOTC{}
	f := handler.NewPeerOTCForwarder(fake)

	if _, err := f.GetPublicStocks(context.Background(), &stockpb.GetPublicStocksRequest{PeerBankCode: "222"}); err != nil {
		t.Fatalf("GetPublicStocks: %v", err)
	}
	if fake.gotStocks == nil || fake.gotStocks.GetPeerBankCode() != "222" {
		t.Errorf("GetPublicStocks request not forwarded")
	}

	resp, err := f.CreateNegotiation(context.Background(), &stockpb.CreateNegotiationRequest{PeerBankCode: "222"})
	if err != nil {
		t.Fatalf("CreateNegotiation: %v", err)
	}
	if fake.gotCreate == nil || fake.gotCreate.GetPeerBankCode() != "222" {
		t.Errorf("CreateNegotiation request not forwarded")
	}
	if resp.GetNegotiationId().GetId() != "neg-1" {
		t.Errorf("CreateNegotiation response not passed back: %+v", resp.GetNegotiationId())
	}

	ar, err := f.AcceptNegotiation(context.Background(), &stockpb.AcceptNegotiationRequest{PeerBankCode: "222"})
	if err != nil {
		t.Fatalf("AcceptNegotiation: %v", err)
	}
	if fake.gotAccept == nil || ar.GetTransactionId() != "tx-9" {
		t.Errorf("AcceptNegotiation not forwarded/returned: %+v", ar)
	}
}
