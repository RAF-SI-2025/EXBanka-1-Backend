package grpc

import (
	"context"

	"github.com/exbanka/api-gateway/internal/middleware"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerBankResolverAdapter wraps PeerBankAdminServiceClient to satisfy the
// middleware.PeerBankResolver interface.
//
// Phase 2 limitation: the admin RPC's PeerBank.api_token_preview returns
// only the last 4 chars of the token; the gateway has no way to look up
// a peer by full plaintext token via this RPC. ResolveByAPIToken is
// therefore stubbed to always return "not found" — the X-Api-Key auth
// path is effectively disabled in Phase 2. Phase 3 will add a dedicated
// internal RPC (e.g. ResolvePeerByAPIToken) that does server-side
// bcrypt-compare and returns the bank record without leaking plaintext.
//
// Similarly, ResolveByBankCode cannot return the actual HMACInboundKey
// because the admin RPC does not expose it (only hmac_enabled bool).
// HMAC auth therefore authenticates structurally but fails signature
// comparison until the same Phase-3 internal RPC is added.
type PeerBankResolverAdapter struct {
	Client transactionpb.PeerBankAdminServiceClient
}

func (a *PeerBankResolverAdapter) ResolveByBankCode(ctx context.Context, code string) (*middleware.PeerBankRecord, bool, error) {
	resp, err := a.Client.ListPeerBanks(ctx, &transactionpb.ListPeerBanksRequest{ActiveOnly: false})
	if err != nil {
		return nil, false, err
	}
	for _, pb := range resp.GetPeerBanks() {
		if pb.GetBankCode() == code {
			return &middleware.PeerBankRecord{
				BankCode:       pb.GetBankCode(),
				RoutingNumber:  pb.GetRoutingNumber(),
				HMACInboundKey: "", // not exposed by admin RPC; Phase 3 internal RPC will populate
				Active:         pb.GetActive(),
			}, true, nil
		}
	}
	return nil, false, nil
}

func (a *PeerBankResolverAdapter) ResolveByAPIToken(ctx context.Context, _ string) (*middleware.PeerBankRecord, bool, error) {
	// Phase 2 stub — see type doc. Phase 3 fills in.
	return nil, false, nil
}
