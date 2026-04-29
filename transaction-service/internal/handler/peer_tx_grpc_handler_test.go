package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/handler"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPeerTx_HandleNewTx_Unimplemented(t *testing.T) {
	h := handler.NewPeerTxGRPCHandler()
	_, err := h.HandleNewTx(context.Background(), &transactionpb.SiTxNewTxRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", st.Code())
	}
}

func TestPeerTx_HandleCommitTx_Unimplemented(t *testing.T) {
	h := handler.NewPeerTxGRPCHandler()
	_, err := h.HandleCommitTx(context.Background(), &transactionpb.SiTxCommitRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("got %v", st.Code())
	}
}

func TestPeerTx_HandleRollbackTx_Unimplemented(t *testing.T) {
	h := handler.NewPeerTxGRPCHandler()
	_, err := h.HandleRollbackTx(context.Background(), &transactionpb.SiTxRollbackRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("got %v", st.Code())
	}
}

func TestPeerTx_InitiateOutboundTx_Unimplemented(t *testing.T) {
	h := handler.NewPeerTxGRPCHandler()
	_, err := h.InitiateOutboundTx(context.Background(), &transactionpb.SiTxInitiateRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("got %v", st.Code())
	}
}
