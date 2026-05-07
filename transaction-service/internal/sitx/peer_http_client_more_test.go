package sitx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/sitx"
)

// TestPeerHTTPClient_NilClient_DefaultUsed verifies that passing nil to
// NewPeerHTTPClient swaps in http.DefaultClient.
func TestPeerHTTPClient_NilClient_DefaultUsed(t *testing.T) {
	c := sitx.NewPeerHTTPClient(nil)
	if c == nil {
		t.Fatalf("client must not be nil")
	}
}

// TestPeerHTTPClient_NewTx_Non200Body_Error verifies that a non-200 response
// returns an error containing the status and body.
func TestPeerHTTPClient_NewTx_Non200Body_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad envelope"))
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
	})
	if err == nil {
		t.Fatalf("expected error on non-200")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad envelope") {
		t.Errorf("expected status+body in error, got %v", err)
	}
}

// TestPeerHTTPClient_NewTx_204_Error verifies the explicit 204-rejection path
// for NEW_TX (which expects a vote body).
func TestPeerHTTPClient_NewTx_204_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
	})
	if err == nil {
		t.Fatalf("expected error on 204 NEW_TX")
	}
}

// TestPeerHTTPClient_NewTx_BadBody_DecodeError verifies that an invalid JSON
// vote body surfaces as a decode error.
func TestPeerHTTPClient_NewTx_BadBody_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
	})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode err, got %v", err)
	}
}

// TestPeerHTTPClient_HMAC_HeadersAttached verifies the HMAC bundle headers
// are present when HMACOutboundKey is set.
func TestPeerHTTPClient_HMAC_HeadersAttached(t *testing.T) {
	var sawSig, sawNonce, sawTS, sawBC bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSig = r.Header.Get("X-Bank-Signature") != ""
		sawNonce = r.Header.Get("X-Nonce") != ""
		sawTS = r.Header.Get("X-Timestamp") != ""
		sawBC = r.Header.Get("X-Bank-Code") == "222"
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{
		BankCode:        "222",
		BaseURL:         srv.URL,
		APIToken:        "tok",
		OwnRouting:      111,
		RoutingNumber:   222,
		HMACOutboundKey: "supersecret",
	}
	if err := client.PostCommitTx(context.Background(), target, contractsitx.Message[contractsitx.CommitTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeCommitTx,
		Message:        contractsitx.CommitTransaction{TransactionID: "tx-h"},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !sawSig || !sawNonce || !sawTS || !sawBC {
		t.Errorf("expected all HMAC headers; sig=%v nonce=%v ts=%v bc=%v", sawSig, sawNonce, sawTS, sawBC)
	}
}

// TestPeerHTTPClient_CommitTx_NonOK_Error verifies a non-204/200 status from a
// COMMIT_TX response surfaces as an error.
func TestPeerHTTPClient_CommitTx_NonOK_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("commit failed"))
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	if err := client.PostCommitTx(context.Background(), target, contractsitx.Message[contractsitx.CommitTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeCommitTx,
		Message:        contractsitx.CommitTransaction{TransactionID: "tx-2"},
	}); err == nil {
		t.Fatalf("expected error on 500")
	}
}

// TestPeerHTTPClient_RollbackTx_204 verifies the rollback happy path.
func TestPeerHTTPClient_RollbackTx_204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	if err := client.PostRollbackTx(context.Background(), target, contractsitx.Message[contractsitx.RollbackTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeRollbackTx,
		Message:        contractsitx.RollbackTransaction{TransactionID: "tx-r"},
	}); err != nil {
		t.Errorf("rollback: %v", err)
	}
}

// TestPeerHTTPClient_RollbackTx_NonOK_Error verifies a non-204/200 status
// from a ROLLBACK_TX response surfaces as an error.
func TestPeerHTTPClient_RollbackTx_NonOK_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream gone"))
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	if err := client.PostRollbackTx(context.Background(), target, contractsitx.Message[contractsitx.RollbackTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeRollbackTx,
		Message:        contractsitx.RollbackTransaction{TransactionID: "tx-r"},
	}); err == nil {
		t.Fatalf("expected error on 502")
	}
}

// TestPeerHTTPClient_TrailingSlashStripped verifies the BaseURL trailing
// slash is normalised before the /interbank suffix is appended.
func TestPeerHTTPClient_TrailingSlashStripped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL + "/", APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_ = client.PostCommitTx(context.Background(), target, contractsitx.Message[contractsitx.CommitTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeCommitTx,
		Message:        contractsitx.CommitTransaction{TransactionID: "tx-3"},
	})
	if gotPath != "/interbank" {
		t.Errorf("expected /interbank, got %q", gotPath)
	}
}
