// Package handler — PeerOTCForwarder lets interbank-service serve the inbound OTC
// slice of the /cross-bank-protocol surface so the api-gateway has ONE cross-bank
// backend. interbank-service is the protocol boundary/coordinator; the OTC DOMAIN
// (negotiation chains, contracts, holdings) stays in stock-service, so each
// peer-facing PeerOTCService RPC is transparently forwarded to stock-service.
//
// Only the 7 peer-facing RPCs are forwarded. The internal option-leg RPCs
// (RecordOptionContract, ReserveSellerSharesForNewTx, ValidatePeerOptionMoneyLeg,
// LookupPeerOptionContract, …) are NEVER invoked on interbank-service via the
// gateway — interbank-service CALLS those on stock-service during settlement — so
// they stay Unimplemented here (embedded), never reached on this path.
package handler

import (
	"context"

	stockpb "github.com/exbanka/contract/stockpb"
)

// PeerOTCForwarder implements stockpb.PeerOTCServiceServer by delegating to a
// stockpb.PeerOTCServiceClient pointed at stock-service.
type PeerOTCForwarder struct {
	stockpb.UnimplementedPeerOTCServiceServer
	stock stockpb.PeerOTCServiceClient
}

// NewPeerOTCForwarder builds the forwarder over a stock-service OTC client.
func NewPeerOTCForwarder(stock stockpb.PeerOTCServiceClient) *PeerOTCForwarder {
	return &PeerOTCForwarder{stock: stock}
}

func (f *PeerOTCForwarder) GetPublicStocks(ctx context.Context, req *stockpb.GetPublicStocksRequest) (*stockpb.GetPublicStocksResponse, error) {
	return f.stock.GetPublicStocks(ctx, req)
}

func (f *PeerOTCForwarder) CreateNegotiation(ctx context.Context, req *stockpb.CreateNegotiationRequest) (*stockpb.CreateNegotiationResponse, error) {
	return f.stock.CreateNegotiation(ctx, req)
}

func (f *PeerOTCForwarder) UpdateNegotiation(ctx context.Context, req *stockpb.UpdateNegotiationRequest) (*stockpb.UpdateNegotiationResponse, error) {
	return f.stock.UpdateNegotiation(ctx, req)
}

func (f *PeerOTCForwarder) GetNegotiation(ctx context.Context, req *stockpb.GetNegotiationRequest) (*stockpb.GetNegotiationResponse, error) {
	return f.stock.GetNegotiation(ctx, req)
}

func (f *PeerOTCForwarder) DeleteNegotiation(ctx context.Context, req *stockpb.DeleteNegotiationRequest) (*stockpb.DeleteNegotiationResponse, error) {
	return f.stock.DeleteNegotiation(ctx, req)
}

func (f *PeerOTCForwarder) AcceptNegotiation(ctx context.Context, req *stockpb.AcceptNegotiationRequest) (*stockpb.AcceptNegotiationResponse, error) {
	return f.stock.AcceptNegotiation(ctx, req)
}
