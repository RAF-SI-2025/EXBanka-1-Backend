package service

import (
	"context"
	"fmt"

	"github.com/exbanka/transaction-service/internal/model"
)

// ReverseTransferOutput is the result of ReverseInterBankTransfer.
type ReverseTransferOutput struct {
	ReverseTxID string
	Committed   bool
	FailReason  string
}

// ReverseInterBankTransfer is the Spec 4 / Celina 5 addendum that drives a
// reverse-direction transfer over the existing 2PC channel. Used by the
// cross-bank OTC accept saga's compensation path when seller-side
// ownership transfer fails after the buyer's strike funds have already
// landed on the seller's bank.
//
// Semantics: the caller asserts that an earlier inter-bank transfer
// `original_tx_id` was COMMITTED with this bank as sender (i.e. funds left
// our books, landed on the peer). The reverse asks the peer to send the
// same amount back. Implementation detail: we look up the local sender row,
// build a new InitiateInput with sender / receiver swapped, and drive a
// fresh PREPARE / COMMIT through the existing 2PC machinery. Idempotency
// key is "reverse-" + original_tx_id so retries land on the same row.
//
// Note: this is the *initiator-side* method invoked locally on a bank that
// needs to roll back. The HTTP variant (peer asks us to reverse one of THEIR
// outgoing transfers) is wired through the same code path with the
// sender/receiver perspective flipped — see HandleReverseInterBankTransfer.
func (s *InterBankService) ReverseInterBankTransfer(ctx context.Context, originalTxID, memo string) (*ReverseTransferOutput, error) {
	original, err := s.tx.Get(originalTxID, model.RoleSender)
	if err != nil {
		// Try the receiver role — peer-initiated reverse where we were the
		// original receiver but are now the one reversing.
		original, err = s.tx.Get(originalTxID, model.RoleReceiver)
		if err != nil {
			return nil, fmt.Errorf("original transfer %s not found: %w", originalTxID, err)
		}
	}
	if original.Status != model.StatusCommitted {
		return &ReverseTransferOutput{
			ReverseTxID: "",
			Committed:   false,
			FailReason:  "original transfer is not committed (status=" + original.Status + ")",
		}, nil
	}

	// Already reversed? Look up the reverse idempotency-keyed row.
	reverseKey := "reverse-" + originalTxID
	if existing, err := s.tx.GetByIdempotencyKey(reverseKey); err == nil && existing != nil {
		return &ReverseTransferOutput{
			ReverseTxID: existing.TxID,
			Committed:   existing.Status == model.StatusCommitted,
			FailReason:  existing.ErrorReason,
		}, nil
	}

	// Build the reverse with sender/receiver swapped and amount cloned. We
	// reuse the original's native amount + currency — fees on the reverse
	// are computed fresh by the peer's PREPARE handler.
	reverseIn := InitiateInput{
		SenderAccountNumber:   original.ReceiverAccountNumber,
		ReceiverAccountNumber: original.SenderAccountNumber,
		Amount:                original.AmountNative,
		Currency:              original.CurrencyNative,
		Memo:                  memo,
	}
	if reverseIn.Memo == "" {
		reverseIn.Memo = "Reverse of " + originalTxID
	}

	row, err := s.InitiateOutgoing(ctx, reverseIn)
	if err != nil {
		return &ReverseTransferOutput{FailReason: err.Error()}, nil
	}
	// Stamp the canonical reverse idempotency key so subsequent calls land here.
	_ = s.tx.SetReverseKey(row.TxID, reverseKey)

	out := &ReverseTransferOutput{
		ReverseTxID: row.TxID,
		Committed:   row.Status == model.StatusCommitted,
		FailReason:  row.ErrorReason,
	}
	return out, nil
}
