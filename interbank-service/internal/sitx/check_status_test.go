package sitx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exbanka/interbank-service/internal/sitx"
)

// TestCheckStatus_Success verifies the Celina-5 CHECK_STATUS GET hits
// /interbank/:txID/status, signs the request (X-Api-Key + X-Bank-Code), and
// parses the response body.
func TestCheckStatus_Success(t *testing.T) {
	var gotPath, gotMethod, gotAPIKey, gotBankCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotBankCode = r.Header.Get("X-Bank-Code")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transaction_id":"tx-7","state":"committed","our_role":"receiver","last_action_at":"2026-06-13T00:00:00Z","last_error":""}`))
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnBankCode: "111", OwnRouting: 111, RoutingNumber: 222}

	resp, err := client.CheckStatus(context.Background(), target, "tx-7")
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/interbank/tx-7/status" {
		t.Errorf("path = %q, want /interbank/tx-7/status", gotPath)
	}
	if gotAPIKey != "tok" {
		t.Errorf("X-Api-Key = %q, want tok", gotAPIKey)
	}
	if gotBankCode != "111" {
		t.Errorf("X-Bank-Code = %q, want 111 (our code)", gotBankCode)
	}
	if resp.TransactionID != "tx-7" || resp.State != "committed" || resp.OurRole != "receiver" {
		t.Errorf("parsed response = %+v", resp)
	}
}

// TestCheckStatus_TrimsTrailingSlash verifies a base_url with a trailing slash
// still produces a single /interbank/:txID/status path.
func TestCheckStatus_TrimsTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"state":"prepared"}`))
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL + "/", APIToken: "tok", OwnBankCode: "111", OwnRouting: 111, RoutingNumber: 222}
	if _, err := client.CheckStatus(context.Background(), target, "abc"); err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if gotPath != "/interbank/abc/status" {
		t.Errorf("path = %q, want /interbank/abc/status", gotPath)
	}
}

// TestCheckStatus_NonOK verifies a non-200 status returns an error carrying the
// HTTP code and body.
func TestCheckStatus_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnBankCode: "111", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.CheckStatus(context.Background(), target, "tx-x")
	if err == nil {
		t.Fatalf("expected error on HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should carry status + body, got %v", err)
	}
}

// TestCheckStatus_BadJSON verifies a 200 with an unparseable body surfaces a
// decode error.
func TestCheckStatus_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnBankCode: "111", OwnRouting: 111, RoutingNumber: 222}
	if _, err := client.CheckStatus(context.Background(), target, "tx-x"); err == nil {
		t.Fatalf("expected decode error on bad JSON")
	}
}

// TestCheckStatus_Unreachable verifies an unreachable peer returns a transport
// error.
func TestCheckStatus_Unreachable(t *testing.T) {
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: "http://127.0.0.1:0", APIToken: "tok", OwnBankCode: "111", OwnRouting: 111, RoutingNumber: 222}
	if _, err := client.CheckStatus(context.Background(), target, "tx-x"); err == nil {
		t.Fatalf("expected transport error on unreachable peer")
	}
}
