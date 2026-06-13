// Package peeregress implements the OTC negotiation egress dispatcher backed
// by interbank-service's PeerEgressService.ProxyToPeer.
//
// As of the 2026-06-07 cutover, stock-service no longer resolves peer_banks
// credentials or signs SI-TX requests itself — interbank-service is the single
// outbound HTTP egress to permitted peer banks. This dispatcher composes the
// leaf /negotiations paths and hands them to ProxyToPeer; peer resolution,
// X-Api-Key/HMAC signing, and the actual HTTP call all happen inside
// interbank-service. It replaces the deleted internal/peerotc package and
// satisfies the same handler.PeerNegotiationDispatcher contract
// (CreateNegotiation + Proxy).
package peeregress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// Dispatcher routes outbound OTC negotiation calls through interbank-service.
type Dispatcher struct {
	egress transactionpb.PeerEgressServiceClient
}

// NewDispatcher builds a Dispatcher over an interbank PeerEgressService client.
func NewDispatcher(egress transactionpb.PeerEgressServiceClient) *Dispatcher {
	return &Dispatcher{egress: egress}
}

// CreateNegotiation POSTs the composed SI-TX OtcOffer to the peer's
// /negotiations endpoint (via interbank ProxyToPeer) and returns the
// peer-assigned (routingNumber, id) pair.
func (d *Dispatcher) CreateNegotiation(ctx context.Context, peerBankCode string, offer map[string]any) (int64, string, error) {
	body, err := json.Marshal(offer)
	if err != nil {
		return 0, "", fmt.Errorf("marshal offer: %w", err)
	}
	resp, err := d.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peerBankCode,
		Method:       http.MethodPost,
		Path:         "/negotiations",
		Body:         body,
	})
	if err != nil {
		return 0, "", fmt.Errorf("peer dispatch: %w", err)
	}
	if resp.GetStatusCode() != http.StatusCreated && resp.GetStatusCode() != http.StatusOK {
		return 0, "", fmt.Errorf("peer rejected (%d): %s", resp.GetStatusCode(), strings.TrimSpace(string(resp.GetBody())))
	}
	var pr struct {
		RoutingNumber int64  `json:"routingNumber"`
		ID            string `json:"id"`
	}
	if err := json.Unmarshal(resp.GetBody(), &pr); err != nil {
		return 0, "", fmt.Errorf("decode peer response: %w", err)
	}
	return pr.RoutingNumber, pr.ID, nil
}

// Proxy forwards a single-negotiation operation to
// /negotiations/{rid}/{foreignID}{subpath} on the peer (via interbank
// ProxyToPeer).
//
// method is an HTTP verb (GET, PUT, DELETE, …). subpath is appended verbatim
// after foreignID (e.g. "/accept", ""). body is nil/empty for GET, DELETE.
//
// Non-2xx peer statuses are passed through as (body, status, nil) — the caller
// relays them to the originating client. A gRPC/transport failure reaching
// interbank is returned as (nil, 502, err).
// PublicStock fetches the peer's /public-stock listing (SI-TX §3.1 bare array)
// via interbank ProxyToPeer. Used by the freshness guard in openRemoteNegotiation
// to verify a shell offer is still live before dispatching a doomed bid.
// Unlike Proxy, this method routes directly to /public-stock and does not
// attempt to build /negotiations/{rid}/{foreignID}{subpath}.
func (d *Dispatcher) PublicStock(ctx context.Context, peerBankCode string) ([]byte, int, error) {
	resp, err := d.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peerBankCode,
		Method:       http.MethodGet,
		Path:         "/public-stock",
	})
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("peer dispatch: %w", err)
	}
	return resp.GetBody(), int(resp.GetStatusCode()), nil
}

func (d *Dispatcher) Proxy(ctx context.Context, peerBankCode, rid, foreignID, method, subpath string, body []byte) ([]byte, int, error) {
	resp, err := d.egress.ProxyToPeer(ctx, &transactionpb.ProxyToPeerRequest{
		PeerBankCode: peerBankCode,
		Method:       method,
		Path:         "/negotiations/" + rid + "/" + foreignID + subpath,
		Body:         body,
	})
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("peer dispatch: %w", err)
	}
	return resp.GetBody(), int(resp.GetStatusCode()), nil
}
