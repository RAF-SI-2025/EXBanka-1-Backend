package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// PeerTxGRPCHandler implements transactionpb.PeerTxServiceServer. Phase 2
// stubs every RPC as Unimplemented so the gateway envelope-decoding path
// can be wired and exercised end-to-end before Phase 3 fills in posting
// execution.
type PeerTxGRPCHandler struct {
	transactionpb.UnimplementedPeerTxServiceServer
}

func NewPeerTxGRPCHandler() *PeerTxGRPCHandler { return &PeerTxGRPCHandler{} }

func (h *PeerTxGRPCHandler) HandleNewTx(ctx context.Context, req *transactionpb.SiTxNewTxRequest) (*transactionpb.SiTxVoteResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NEW_TX handling pending Phase 3")
}

func (h *PeerTxGRPCHandler) HandleCommitTx(ctx context.Context, req *transactionpb.SiTxCommitRequest) (*transactionpb.SiTxAckResponse, error) {
	return nil, status.Error(codes.Unimplemented, "COMMIT_TX handling pending Phase 3")
}

func (h *PeerTxGRPCHandler) HandleRollbackTx(ctx context.Context, req *transactionpb.SiTxRollbackRequest) (*transactionpb.SiTxAckResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ROLLBACK_TX handling pending Phase 3")
}

func (h *PeerTxGRPCHandler) InitiateOutboundTx(ctx context.Context, req *transactionpb.SiTxInitiateRequest) (*transactionpb.SiTxInitiateResponse, error) {
	return nil, status.Error(codes.Unimplemented, "outbound TX initiation pending Phase 3")
}
