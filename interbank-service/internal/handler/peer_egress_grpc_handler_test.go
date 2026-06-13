package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/interbank-service/internal/handler"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
)

func newEgressFixture(t *testing.T, peer *model.PeerBank) *handler.PeerEgressGRPCHandler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerBank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewPeerBankRepository(db)
	if peer != nil {
		if err := repo.Create(peer); err != nil {
			t.Fatalf("seed peer: %v", err)
		}
	}
	return handler.NewPeerEgressGRPCHandler(repo, &http.Client{}, "111")
}

// TestProxyToPeer_SignsAndPassesThrough: a registered, active peer → the request
// reaches the peer with the right method/path + X-Api-Key, and the peer's status
// + body are returned verbatim.
func TestProxyToPeer_SignsAndPassesThrough(t *testing.T) {
	var gotMethod, gotPath, gotAPIKey string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"routingNumber":222,"id":"neg-1"}`))
	}))
	defer srv.Close()

	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: srv.URL,
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "token-222", Active: true,
	})

	resp, err := h.ProxyToPeer(context.Background(), &transactionpb.ProxyToPeerRequest{
		PeerBankCode: "222", Method: "POST", Path: "/negotiations",
		Body: []byte(`{"amount":5}`),
	})
	if err != nil {
		t.Fatalf("ProxyToPeer: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/negotiations" {
		t.Errorf("peer saw %s %s, want POST /negotiations", gotMethod, gotPath)
	}
	if gotAPIKey != "token-222" {
		t.Errorf("X-Api-Key = %q want token-222 (signing missing)", gotAPIKey)
	}
	if string(gotBody) != `{"amount":5}` {
		t.Errorf("peer body = %q want passthrough", string(gotBody))
	}
	if resp.GetStatusCode() != http.StatusCreated {
		t.Errorf("status = %d want 201", resp.GetStatusCode())
	}
	if string(resp.GetBody()) != `{"routingNumber":222,"id":"neg-1"}` {
		t.Errorf("body not passed through verbatim: %q", string(resp.GetBody()))
	}
}

func TestProxyToPeer_UnknownPeer_NotFound(t *testing.T) {
	h := newEgressFixture(t, nil)
	_, err := h.ProxyToPeer(context.Background(), &transactionpb.ProxyToPeerRequest{
		PeerBankCode: "999", Method: "GET", Path: "/public-stock",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound for unregistered peer, got %v", err)
	}
}

func TestProxyToPeer_InactivePeer_FailedPrecondition(t *testing.T) {
	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: "http://unused",
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "t", Active: false,
	})
	_, err := h.ProxyToPeer(context.Background(), &transactionpb.ProxyToPeerRequest{
		PeerBankCode: "222", Method: "GET", Path: "/public-stock",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition for inactive peer, got %v", err)
	}
}

// newEgressFixtureMulti seeds several peers and returns the handler + repo so
// the fleet-state test can register peers pointing at distinct test servers.
func newEgressFixtureMulti(t *testing.T, peers ...*model.PeerBank) *handler.PeerEgressGRPCHandler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.PeerBank{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := repository.NewPeerBankRepository(db)
	for _, p := range peers {
		if err := repo.Create(p); err != nil {
			t.Fatalf("seed peer %s: %v", p.BankCode, err)
		}
	}
	return handler.NewPeerEgressGRPCHandler(repo, &http.Client{}, "111")
}

// TestCheckPeerReachability_Healthy: a live peer returning 200 on /public-stock
// is reachable with status 200.
func TestCheckPeerReachability_Healthy(t *testing.T) {
	var gotPath, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: srv.URL,
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "tok", Active: true,
	})
	res, err := h.CheckPeerReachability(context.Background(), &transactionpb.CheckPeerReachabilityRequest{PeerBankCode: "222"})
	if err != nil {
		t.Fatalf("CheckPeerReachability: %v", err)
	}
	if !res.GetReachable() || res.GetStatusCode() != 200 {
		t.Errorf("got reachable=%v status=%d, want true/200", res.GetReachable(), res.GetStatusCode())
	}
	if gotPath != "/public-stock" || gotAPIKey != "tok" {
		t.Errorf("probe hit %q with key %q, want /public-stock + signed", gotPath, gotAPIKey)
	}
	if res.GetError() != "" {
		t.Errorf("error = %q want empty", res.GetError())
	}
}

// TestCheckPeerReachability_Unauthorized: a peer up but rejecting our token is
// REACHABLE (got a response) with status 401 — distinguishes "down" from "bad token".
func TestCheckPeerReachability_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: srv.URL,
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "bad", Active: true,
	})
	res, _ := h.CheckPeerReachability(context.Background(), &transactionpb.CheckPeerReachabilityRequest{PeerBankCode: "222"})
	if !res.GetReachable() || res.GetStatusCode() != 401 {
		t.Errorf("got reachable=%v status=%d, want true/401", res.GetReachable(), res.GetStatusCode())
	}
}

// TestCheckPeerReachability_Unreachable: a peer at a dead address is NOT reachable
// and carries an error; status is 0.
func TestCheckPeerReachability_Unreachable(t *testing.T) {
	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: "http://127.0.0.1:1", // nothing listens
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "tok", Active: true,
	})
	res, _ := h.CheckPeerReachability(context.Background(), &transactionpb.CheckPeerReachabilityRequest{PeerBankCode: "222"})
	if res.GetReachable() || res.GetStatusCode() != 0 || res.GetError() == "" {
		t.Errorf("got reachable=%v status=%d err=%q, want false/0/non-empty", res.GetReachable(), res.GetStatusCode(), res.GetError())
	}
}

func TestCheckPeerReachability_UnknownPeer_NotFound(t *testing.T) {
	h := newEgressFixture(t, nil)
	_, err := h.CheckPeerReachability(context.Background(), &transactionpb.CheckPeerReachabilityRequest{PeerBankCode: "999"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

// TestGetPeersState_Fleet: probes all registered peers concurrently — one healthy,
// one down — and returns a per-peer snapshot.
func TestGetPeersState_Fleet(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer up.Close()
	h := newEgressFixtureMulti(t,
		&model.PeerBank{BankCode: "222", RoutingNumber: 222, BaseURL: up.URL, APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "t", Active: true},
		&model.PeerBank{BankCode: "333", RoutingNumber: 333, BaseURL: "http://127.0.0.1:1", APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "t", Active: false},
	)
	resp, err := h.GetPeersState(context.Background(), &transactionpb.GetPeersStateRequest{})
	if err != nil {
		t.Fatalf("GetPeersState: %v", err)
	}
	if len(resp.GetPeers()) != 2 {
		t.Fatalf("want 2 peers in fleet view, got %d", len(resp.GetPeers()))
	}
	byCode := map[string]*transactionpb.PeerReachability{}
	for _, p := range resp.GetPeers() {
		byCode[p.GetBankCode()] = p
	}
	if p := byCode["222"]; p == nil || !p.GetReachable() || p.GetStatusCode() != 200 {
		t.Errorf("peer 222 = %+v, want reachable/200", p)
	}
	if p := byCode["333"]; p == nil || p.GetReachable() || p.GetActive() {
		t.Errorf("peer 333 = %+v, want unreachable + inactive", p)
	}
}

func TestProxyToPeer_BadMethodOrPath_InvalidArgument(t *testing.T) {
	h := newEgressFixture(t, &model.PeerBank{
		BankCode: "222", RoutingNumber: 222, BaseURL: "http://unused",
		APITokenBcrypt: "$2a$10$x", APITokenPlaintext: "t", Active: true,
	})
	if _, err := h.ProxyToPeer(context.Background(), &transactionpb.ProxyToPeerRequest{
		PeerBankCode: "222", Method: "PATCH", Path: "/x",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for unsupported method, got %v", err)
	}
	if _, err := h.ProxyToPeer(context.Background(), &transactionpb.ProxyToPeerRequest{
		PeerBankCode: "222", Method: "GET", Path: "no-leading-slash",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("want InvalidArgument for bad path, got %v", err)
	}
}
