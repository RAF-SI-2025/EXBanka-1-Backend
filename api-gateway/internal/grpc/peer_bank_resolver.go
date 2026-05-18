package grpc

import (
	"context"

	"github.com/exbanka/api-gateway/internal/middleware"
	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerBankResolverAdapter wraps PeerBankAdminServiceClient to satisfy the
// middleware.PeerBankResolver interface. Phase 3 uses the internal
// ResolvePeerByAPIToken / ResolvePeerByBankCode RPCs that return the
// full peer-bank record (including HMAC keys + plaintext API token)
// without exposing those fields through the public admin REST surface.
type PeerBankResolverAdapter struct {
	Client transactionpb.PeerBankAdminServiceClient
}

func (a *PeerBankResolverAdapter) ResolveByBankCode(ctx context.Context, code string) (*middleware.PeerBankRecord, bool, error) {
	resp, err := a.Client.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: code})
	if err != nil {
		return nil, false, err
	}
	if !resp.GetFound() {
		return nil, false, nil
	}
	return fullToRecord(resp.GetPeerBank()), true, nil
}

func (a *PeerBankResolverAdapter) ResolveByAPIToken(ctx context.Context, token string) (*middleware.PeerBankRecord, bool, error) {
	resp, err := a.Client.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: token})
	if err != nil {
		return nil, false, err
	}
	if !resp.GetFound() {
		return nil, false, nil
	}
	return fullToRecord(resp.GetPeerBank()), true, nil
}

func fullToRecord(pb *transactionpb.PeerBankFull) *middleware.PeerBankRecord {
	return &middleware.PeerBankRecord{
		BankCode:          pb.GetBankCode(),
		RoutingNumber:     pb.GetRoutingNumber(),
		APITokenPlaintext: pb.GetApiTokenPlaintext(),
		HMACInboundKey:    pb.GetHmacInboundKey(),
		Active:            pb.GetActive(),
	}
}
