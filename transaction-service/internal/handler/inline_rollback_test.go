package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// TestInitiateOutboundTx_PeerVotesNO_ReleaseFails_StaysPending verifies
// that when the inline NO-vote hold release fails (e.g. account-service is
// transiently down), the outbound row is NOT parked in the terminal
// `rolled_back` state — it stays `pending` so OutboundReplayCron retries the
// reversal. Marking it rolled_back here would strand the held money in a
// row nothing ever revisits.
func TestInitiateOutboundTx_PeerVotesNO_ReleaseFails_StaysPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"NO","noVotes":[{"reason":"INSUFFICIENT_ASSET"}]}`))
	}))
	defer srv.Close()

	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	_ = db.AutoMigrate(&model.PeerIdempotenceRecord{}, &model.OutboundPeerTx{})
	stub := &stubAccountForHandler{}
	stub.releaseOutFn = func(_ context.Context, in *accountpb.ReleaseOutgoingRequest, _ ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error) {
		if strings.HasPrefix(in.GetIdempotencyKey(), "peer-out-release-") {
			return nil, errors.New("account-service down")
		}
		return &accountpb.ReleaseOutgoingResponse{Released: true}, nil
	}
	idemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)
	exec := sitx.NewPostingExecutor(stub, 111)
	httpClient := sitx.NewPeerHTTPClient(http.DefaultClient)
	peerLookup := func(_ context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		return &sitx.PeerHTTPTarget{BankCode: code, BaseURL: srv.URL, APIToken: "t", OwnRouting: 111, RoutingNumber: 222}, nil
	}
	h := handler.NewPeerTxGRPCHandler(idemRepo, exec, stub, outRepo, httpClient, handler.PeerLookupFunc(peerLookup), 111)

	resp, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{
		FromAccountNumber: "111-A",
		ToAccountNumber:   "222-recipient",
		Amount:            "50",
		Currency:          "RSD",
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}

	row, gerr := outRepo.GetByIdempotenceKey(resp.GetTransactionId())
	if gerr != nil {
		t.Fatalf("get row: %v", gerr)
	}
	if row.Status != "pending" {
		t.Errorf("expected row to stay pending after credit-back failure (so the cron retries), got %q", row.Status)
	}
}
