// Package handler — PeerEgressGRPCHandler is the centralized outbound HTTP egress
// to permitted peer banks. Domain services (e.g. stock-service for the OTC
// /negotiations, /public-stock, /public-option-offers, /accept calls) route their
// cross-bank HTTP through ProxyToPeer instead of dialing peers directly, so
// peer_banks resolution + X-Api-Key/HMAC signing live in ONE place.
package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/contract/sitxauth"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
)

// peerLivenessPath is the universal SI-TX endpoint every conformant peer serves
// (auth'd, returns 200 with the public-stock array). A signed GET here is the
// reachability/health probe; peerProbeTimeout bounds a slow/down peer.
const (
	peerLivenessPath = "/public-stock"
	peerProbeTimeout = 5 * time.Second
)

// PeerEgressGRPCHandler implements transactionpb.PeerEgressServiceServer.
type PeerEgressGRPCHandler struct {
	transactionpb.UnimplementedPeerEgressServiceServer
	peers       *repository.PeerBankRepository
	httpClient  *http.Client
	ownBankCode string
}

// NewPeerEgressGRPCHandler builds the egress handler over the peer-bank registry.
func NewPeerEgressGRPCHandler(peers *repository.PeerBankRepository, httpClient *http.Client, ownBankCode string) *PeerEgressGRPCHandler {
	return &PeerEgressGRPCHandler{peers: peers, httpClient: httpClient, ownBankCode: ownBankCode}
}

var allowedEgressMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodDelete: true,
}

// ProxyToPeer resolves the peer's base_url from the peer_banks registry, appends
// the caller-supplied leaf path, signs the request (X-Api-Key + optional HMAC via
// contract/sitxauth), performs the HTTP call, and returns the peer's status code
// and body verbatim. The path is opaque to us — the caller (a trusted internal
// domain service) builds it (e.g. "/negotiations/222/abc/accept").
func (h *PeerEgressGRPCHandler) ProxyToPeer(ctx context.Context, req *transactionpb.ProxyToPeerRequest) (*transactionpb.ProxyToPeerResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(req.GetMethod()))
	if !allowedEgressMethods[method] {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported method %q", req.GetMethod())
	}
	path := req.GetPath()
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, status.Error(codes.InvalidArgument, "path must be a non-empty leaf beginning with '/'")
	}

	peer, err := h.peers.GetByBankCode(req.GetPeerBankCode())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "peer bank %q not registered", req.GetPeerBankCode())
	}
	if !peer.Active {
		return nil, status.Errorf(codes.FailedPrecondition, "peer bank %q is inactive", req.GetPeerBankCode())
	}

	url := strings.TrimRight(peer.BaseURL, "/") + path
	body := req.GetBody()
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "build request: %v", err)
	}
	if len(body) > 0 {
		hreq.Header.Set("Content-Type", "application/json")
	}
	// Sign with the peer's outbound credentials (X-Api-Key always; HMAC bundle
	// when an outbound key is configured). Identical to the TX-layer egress.
	sitxauth.Sign(hreq, peer.APITokenPlaintext, peer.HMACOutboundKey, h.ownBankCode, body)

	resp, err := h.httpClient.Do(hreq)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "peer dispatch failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	return &transactionpb.ProxyToPeerResponse{
		StatusCode: int32(resp.StatusCode),
		Body:       respBody,
	}, nil
}

// CheckPeerReachability probes a single registered peer (signed GET /public-stock)
// and reports reachability + status + latency. Probes regardless of the active
// flag so an admin can verify a peer before/after activating it. Never mutates
// the registry.
func (h *PeerEgressGRPCHandler) CheckPeerReachability(ctx context.Context, req *transactionpb.CheckPeerReachabilityRequest) (*transactionpb.PeerReachability, error) {
	peer, err := h.peers.GetByBankCode(req.GetPeerBankCode())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "peer bank %q not registered", req.GetPeerBankCode())
	}
	return h.probePeer(ctx, peer), nil
}

// GetPeersState probes ALL registered peers concurrently and returns the fleet
// health view. Each peer is probed under its own bounded timeout, so one down
// peer cannot stall the others.
func (h *PeerEgressGRPCHandler) GetPeersState(ctx context.Context, _ *transactionpb.GetPeersStateRequest) (*transactionpb.GetPeersStateResponse, error) {
	peers, err := h.peers.List(false)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list peers: %v", err)
	}
	out := make([]*transactionpb.PeerReachability, len(peers))
	var wg sync.WaitGroup
	for i := range peers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = h.probePeer(ctx, &peers[i]) // distinct index per goroutine — no race
		}(i)
	}
	wg.Wait()
	return &transactionpb.GetPeersStateResponse{Peers: out}, nil
}

// probePeer performs the signed liveness GET against one peer and classifies the
// outcome. reachable = an HTTP response arrived (any status); status_code carries
// the peer's status (200 ⇒ healthy SI-TX peer, 401/403 ⇒ reachable but our token
// is wrong); a transport error/timeout ⇒ reachable=false with error set.
func (h *PeerEgressGRPCHandler) probePeer(ctx context.Context, peer *model.PeerBank) *transactionpb.PeerReachability {
	out := &transactionpb.PeerReachability{
		BankCode:      peer.BankCode,
		RoutingNumber: peer.RoutingNumber,
		BaseUrl:       peer.BaseURL,
		Active:        peer.Active,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	pctx, cancel := context.WithTimeout(ctx, peerProbeTimeout)
	defer cancel()

	url := strings.TrimRight(peer.BaseURL, "/") + peerLivenessPath
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sitxauth.Sign(req, peer.APITokenPlaintext, peer.HMACOutboundKey, h.ownBankCode, nil)

	start := time.Now()
	resp, err := h.httpClient.Do(req)
	out.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	out.Reachable = true
	out.StatusCode = int32(resp.StatusCode)
	return out
}
