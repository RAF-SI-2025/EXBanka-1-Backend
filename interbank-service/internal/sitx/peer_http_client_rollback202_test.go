package sitx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
)

// TestPeerHTTPClient_RollbackTx_202_RetryLater verifies a 202 Accepted on a
// ROLLBACK_TX maps to the ErrRetryLater sentinel (the only PostRollbackTx
// response branch not already exercised), so the caller retries rather than
// treating the rollback as final.
func TestPeerHTTPClient_RollbackTx_202_RetryLater(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	client := sitx.NewPeerHTTPClient(http.DefaultClient)
	target := &sitx.PeerHTTPTarget{BankCode: "222", BaseURL: srv.URL, APIToken: "tok", OwnRouting: 111, RoutingNumber: 222}
	err := client.PostRollbackTx(context.Background(), target, contractsitx.Message[contractsitx.RollbackTransaction]{
		IdempotenceKey: contractsitx.IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "M-rb"},
		MessageType:    contractsitx.MessageTypeRollbackTx,
		Message:        contractsitx.RollbackTransaction{TransactionID: contractsitx.ForeignBankId{RoutingNumber: 111, ID: "L-1"}},
	})
	if !errors.Is(err, sitx.ErrRetryLater) {
		t.Fatalf("expected ErrRetryLater on 202, got %v", err)
	}
}
