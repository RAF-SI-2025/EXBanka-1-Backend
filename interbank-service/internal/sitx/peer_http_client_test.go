package sitx_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestPeerHTTPClient_NewTx_YESPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify X-Api-Key was set.
		if r.Header.Get("X-Api-Key") != "test-token" {
			t.Errorf("expected X-Api-Key=test-token, got %q", r.Header.Get("X-Api-Key"))
		}
		var msg contractsitx.Message[contractsitx.Transaction]
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg.MessageType != contractsitx.MessageTypeNewTx {
			t.Errorf("messageType: %q", msg.MessageType)
		}
		_ = json.NewEncoder(w).Encode(contractsitx.TransactionVote{Vote: contractsitx.VoteYes})
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{
		BankCode:      "222",
		BaseURL:       srv.URL,
		APIToken:      "test-token",
		OwnRouting:    111,
		RoutingNumber: 222,
	}
	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message: contractsitx.Transaction{
			Postings: []contractsitx.Posting{
				{Account: contractsitx.TxAccount{Type: "ACCOUNT", Num: "111-A"}, Amount: contractsitx.DecimalNumber{Decimal: dec("-100")}, Asset: contractsitx.Asset{Type: "MONAS", Asset: contractsitx.MonetaryAsset{Currency: "RSD"}}},
				{Account: contractsitx.TxAccount{Type: "ACCOUNT", Num: "222-B"}, Amount: contractsitx.DecimalNumber{Decimal: dec("100")}, Asset: contractsitx.Asset{Type: "MONAS", Asset: contractsitx.MonetaryAsset{Currency: "RSD"}}},
			},
			TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "abc"},
		},
	}
	resp, err := client.PostNewTx(context.Background(), target, envelope)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.Vote != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", resp)
	}
}

func TestPeerHTTPClient_NewTx_NOPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(contractsitx.TransactionVote{
			Vote:    contractsitx.VoteNo,
			Reasons: []contractsitx.NoVoteReason{{Reason: contractsitx.NoVoteReasonInsufficientAsset}},
		})
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	resp, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message:        contractsitx.Transaction{},
	})
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.Vote != contractsitx.VoteNo {
		t.Errorf("expected NO, got %+v", resp)
	}
	if len(resp.Reasons) == 0 || resp.Reasons[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("expected INSUFFICIENT_ASSET, got %+v", resp.Reasons)
	}
}

// TestPeerHTTPClient_NewTx_EnvelopeShape asserts the outbound NEW_TX JSON
// carries the SPEC posting shape (account.num, signed numeric amount,
// asset.type), a populated transactionId, and the message metadata.
func TestPeerHTTPClient_NewTx_EnvelopeShape(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		_ = json.NewEncoder(w).Encode(contractsitx.TransactionVote{Vote: contractsitx.VoteYes})
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	envelope := contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "L-1"},
		MessageType:    contractsitx.MessageTypeNewTx,
		Message: contractsitx.Transaction{
			Postings: []contractsitx.Posting{
				{Account: contractsitx.TxAccount{Type: "ACCOUNT", Num: "111000000000000001"}, Amount: contractsitx.DecimalNumber{Decimal: dec("-100")}, Asset: contractsitx.Asset{Type: "MONAS", Asset: contractsitx.MonetaryAsset{Currency: "RSD"}}},
				{Account: contractsitx.TxAccount{Type: "ACCOUNT", Num: "222000000000000002"}, Amount: contractsitx.DecimalNumber{Decimal: dec("100")}, Asset: contractsitx.Asset{Type: "MONAS", Asset: contractsitx.MonetaryAsset{Currency: "RSD"}}},
			},
			TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "L-1"},
			Message:       "Cross-bank payment",
		},
	}
	if _, err := client.PostNewTx(context.Background(), target, envelope); err != nil {
		t.Fatalf("post: %v", err)
	}
	msg, _ := raw["message"].(map[string]any)
	postings, _ := msg["postings"].([]any)
	if len(postings) != 2 {
		t.Fatalf("expected 2 postings on wire, got %d (%+v)", len(postings), msg)
	}
	p0, _ := postings[0].(map[string]any)
	acc, _ := p0["account"].(map[string]any)
	if acc["num"] != "111000000000000001" {
		t.Errorf("postings[0].account.num = %v, want sender account number", acc["num"])
	}
	// Signed numeric amount: sender leg is negative (asset leaves).
	if amt, ok := p0["amount"].(float64); !ok || amt != -100 {
		t.Errorf("postings[0].amount = %v, want -100 (signed numeric)", p0["amount"])
	}
	asset, _ := p0["asset"].(map[string]any)
	if asset["type"] != "MONAS" {
		t.Errorf("postings[0].asset.type = %v, want MONAS", asset["type"])
	}
	txid, _ := msg["transactionId"].(map[string]any)
	if txid["id"] != "L-1" {
		t.Errorf("transactionId.id = %v, want L-1", txid["id"])
	}
}

// TestPeerHTTPClient_NewTx_202_RetryLater verifies a 202 Accepted maps to the
// ErrRetryLater sentinel so the caller leaves the row pending.
func TestPeerHTTPClient_NewTx_202_RetryLater(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		MessageType: contractsitx.MessageTypeNewTx,
		Message:     contractsitx.Transaction{TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "x"}},
	})
	if !errors.Is(err, sitx.ErrRetryLater) {
		t.Fatalf("expected ErrRetryLater on 202, got %v", err)
	}
}

func TestPeerHTTPClient_CommitTx_204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	if err := client.PostCommitTx(context.Background(), target, contractsitx.Message[contractsitx.CommitTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "M-1"},
		MessageType:    contractsitx.MessageTypeCommitTx,
		Message:        contractsitx.CommitTransaction{TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "L-1"}},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestPeerHTTPClient_CommitTx_202_RetryLater verifies 202 on COMMIT → ErrRetryLater.
func TestPeerHTTPClient_CommitTx_202_RetryLater(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	err := client.PostCommitTx(context.Background(), target, contractsitx.Message[contractsitx.CommitTransaction]{
		MessageType: contractsitx.MessageTypeCommitTx,
		Message:     contractsitx.CommitTransaction{TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "L-1"}},
	})
	if !errors.Is(err, sitx.ErrRetryLater) {
		t.Fatalf("expected ErrRetryLater on 202, got %v", err)
	}
}

func TestPeerHTTPClient_NetworkError(t *testing.T) {
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: "http://127.0.0.1:0", APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	_, err := client.PostNewTx(context.Background(), target, contractsitx.Message[contractsitx.Transaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeNewTx,
	})
	if err == nil {
		t.Fatalf("expected network error, got nil")
	}
}
