package sitx_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
)

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
		_ = json.NewEncoder(w).Encode(contractsitx.TransactionVote{Type: contractsitx.VoteYes})
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
				{RoutingNumber: 111, AccountID: "111-A", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionDebit},
				{RoutingNumber: 222, AccountID: "222-B", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: contractsitx.DirectionCredit},
			},
		},
	}
	resp, err := client.PostNewTx(context.Background(), target, envelope)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if resp.Type != contractsitx.VoteYes {
		t.Errorf("expected YES, got %+v", resp)
	}
}

func TestPeerHTTPClient_NewTx_NOPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(contractsitx.TransactionVote{
			Type:    contractsitx.VoteNo,
			NoVotes: []contractsitx.NoVote{{Reason: contractsitx.NoVoteReasonInsufficientAsset}},
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
	if resp.Type != contractsitx.VoteNo {
		t.Errorf("expected NO, got %+v", resp)
	}
	if len(resp.NoVotes) == 0 || resp.NoVotes[0].Reason != contractsitx.NoVoteReasonInsufficientAsset {
		t.Errorf("expected INSUFFICIENT_ASSET, got %+v", resp.NoVotes)
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
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "abc"},
		MessageType:    contractsitx.MessageTypeCommitTx,
		Message:        contractsitx.CommitTransaction{TransactionID: "tx-1"},
	}); err != nil {
		t.Fatalf("commit: %v", err)
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
