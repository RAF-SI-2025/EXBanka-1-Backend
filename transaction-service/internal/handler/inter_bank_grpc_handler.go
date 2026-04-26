package handler

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/service"
)

// InterBankGRPCHandler bridges the InterBankService methods to the gRPC
// surface defined in contract/proto/transaction/transaction.proto.
type InterBankGRPCHandler struct {
	pb.UnimplementedInterBankServiceServer
	svc *service.InterBankService
}

// NewInterBankGRPCHandler constructs the handler.
func NewInterBankGRPCHandler(svc *service.InterBankService) *InterBankGRPCHandler {
	return &InterBankGRPCHandler{svc: svc}
}

// InitiateInterBankTransfer is the sender-side entry point; called from the
// public POST /api/v1/transfers handler in api-gateway when the receiver
// account's prefix is not OWN_BANK_CODE.
func (h *InterBankGRPCHandler) InitiateInterBankTransfer(ctx context.Context, req *pb.InitiateInterBankRequest) (*pb.InitiateInterBankResponse, error) {
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	row, err := h.svc.InitiateOutgoing(ctx, service.InitiateInput{
		SenderAccountNumber:   req.SenderAccountNumber,
		ReceiverAccountNumber: req.ReceiverAccountNumber,
		Amount:                amt,
		Currency:              req.Currency,
		Memo:                  req.Memo,
	})
	if err != nil {
		if s, ok := status.FromError(err); ok {
			return nil, s.Err()
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.InitiateInterBankResponse{
		TransactionId: row.TxID,
		Status:        row.Status,
		ErrorReason:   row.ErrorReason,
	}, nil
}

// HandlePrepare is the receiver-side Prepare handler.
func (h *InterBankGRPCHandler) HandlePrepare(ctx context.Context, req *pb.InterBankPrepareRequest) (*pb.InterBankPrepareResponse, error) {
	amt, err := decimal.NewFromString(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	out, err := h.svc.HandlePrepare(ctx, service.HandlePrepareInput{
		TransactionID:   req.TransactionId,
		SenderBankCode:  req.SenderBankCode,
		SenderAccount:   req.SenderAccount,
		ReceiverAccount: req.ReceiverAccount,
		Amount:          amt,
		Currency:        req.Currency,
		Memo:            req.Memo,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &pb.InterBankPrepareResponse{
		TransactionId: req.TransactionId,
		Ready:         out.Ready,
		Reason:        out.Reason,
	}
	if out.Ready {
		resp.FinalAmount = out.FinalAmount.String()
		resp.FinalCurrency = out.FinalCurrency
		resp.FxRate = out.FxRate.String()
		resp.Fees = out.Fees.String()
		if !out.ValidUntil.IsZero() {
			resp.ValidUntil = out.ValidUntil.Format(time.RFC3339)
		}
	}
	return resp, nil
}

// HandleCommit is the receiver-side Commit handler.
func (h *InterBankGRPCHandler) HandleCommit(ctx context.Context, req *pb.InterBankCommitRequest) (*pb.InterBankCommitResponse, error) {
	amt, err := decimal.NewFromString(req.FinalAmount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "final_amount: %v", err)
	}
	fxRate := decimal.Zero
	if req.FxRate != "" {
		if d, perr := decimal.NewFromString(req.FxRate); perr == nil {
			fxRate = d
		}
	}
	fees := decimal.Zero
	if req.Fees != "" {
		if d, perr := decimal.NewFromString(req.Fees); perr == nil {
			fees = d
		}
	}
	out, err := h.svc.HandleCommit(ctx, service.HandleCommitInput{
		TransactionID: req.TransactionId,
		FinalAmount:   amt,
		FinalCurrency: req.FinalCurrency,
		FxRate:        fxRate,
		Fees:          fees,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &pb.InterBankCommitResponse{
		TransactionId:  req.TransactionId,
		Committed:      out.Committed,
		NotFound:       out.NotFound,
		MismatchReason: out.MismatchReason,
	}
	if out.Committed {
		resp.CreditedAt = out.CreditedAt.Format(time.RFC3339)
		resp.CreditedAmount = out.CreditedAmount.String()
		resp.CreditedCurrency = out.CreditedCurrency
	}
	return resp, nil
}

// HandleCheckStatus is the bidirectional CheckStatus handler.
func (h *InterBankGRPCHandler) HandleCheckStatus(ctx context.Context, req *pb.InterBankCheckStatusRequest) (*pb.InterBankCheckStatusResponse, error) {
	out, err := h.svc.HandleCheckStatus(ctx, req.TransactionId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &pb.InterBankCheckStatusResponse{
		TransactionId: req.TransactionId,
		NotFound:      out.NotFound,
	}
	if !out.NotFound {
		resp.Role = out.Role
		resp.Status = out.Status
		resp.FinalAmount = out.FinalAmount
		resp.FinalCurrency = out.FinalCurrency
		resp.UpdatedAt = out.UpdatedAt.Format(time.RFC3339)
	}
	return resp, nil
}

// GetInterBankTransfer surfaces row state to the gateway's read-side
// helper.
func (h *InterBankGRPCHandler) GetInterBankTransfer(ctx context.Context, req *pb.GetInterBankTransferRequest) (*pb.GetInterBankTransferResponse, error) {
	row, err := h.svc.GetInterBankTransfer(ctx, req.TransactionId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &pb.GetInterBankTransferResponse{Found: false, TransactionId: req.TransactionId}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &pb.GetInterBankTransferResponse{
		Found:           true,
		TransactionId:   row.TxID,
		Role:            row.Role,
		Status:          row.Status,
		RemoteBankCode:  row.RemoteBankCode,
		SenderAccount:   row.SenderAccountNumber,
		ReceiverAccount: row.ReceiverAccountNumber,
		AmountNative:    row.AmountNative.String(),
		CurrencyNative:  row.CurrencyNative,
		ErrorReason:     row.ErrorReason,
		CreatedAt:       row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
	}
	if row.AmountFinal != nil {
		resp.AmountFinal = row.AmountFinal.String()
	}
	if row.CurrencyFinal != nil {
		resp.CurrencyFinal = *row.CurrencyFinal
	}
	if row.FxRate != nil {
		resp.FxRate = row.FxRate.String()
	}
	if row.FeesFinal != nil {
		resp.FeesFinal = row.FeesFinal.String()
	}
	return resp, nil
}

// ReverseInterBankTransfer drives a reverse-direction transfer over the
// existing 2PC channel — Spec 4 / Celina 5 addendum. Used by the cross-bank
// OTC accept saga's compensation path. Returns:
//   - reverse_tx_id: the new InterBankTransaction's tx_id
//   - committed: true if the reverse landed COMMITTED end-to-end
//   - fail_reason: empty on success; "insufficient-funds-at-responder" or
//     similar when the peer's PREPARE returned NotReady.
func (h *InterBankGRPCHandler) ReverseInterBankTransfer(ctx context.Context, req *pb.ReverseInterBankTransferRequest) (*pb.ReverseInterBankTransferResponse, error) {
	if req.OriginalTxId == "" {
		return nil, status.Error(codes.InvalidArgument, "original_tx_id is required")
	}
	out, err := h.svc.ReverseInterBankTransfer(ctx, req.OriginalTxId, req.Memo)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.ReverseInterBankTransferResponse{
		ReverseTxId: out.ReverseTxID,
		Committed:   out.Committed,
		FailReason:  out.FailReason,
	}, nil
}
