package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// AccountInterBankClient is the slice of account-service RPCs needed by
// InterBankService. Defined as an interface so unit tests can stub it; the
// production implementation wraps accountpb.AccountServiceClient.
type AccountInterBankClient interface {
	GetAccountByNumber(ctx context.Context, accountNumber string) (*accountpb.AccountResponse, error)

	// DebitForInterbank atomically debits the sender's account by `amount`
	// (calling UpdateBalance with a negative amount and `update_available=true`)
	// and writes a ledger entry. Idempotent on `idempotencyKey` — a retry
	// after a crash reuses the existing ledger entry.
	DebitForInterbank(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) error

	// CreditBackForInterbank credits the sender back when the inter-bank
	// transfer is rolled back (NotReady, reconciler-initiated rollback).
	// Idempotent on `idempotencyKey`.
	CreditBackForInterbank(ctx context.Context, accountNumber string, amount decimal.Decimal, memo, idempotencyKey string) error

	// ReserveIncoming creates a pending credit reservation on the receiver
	// account without mutating its balance. Idempotent on reservationKey.
	ReserveIncoming(ctx context.Context, accountNumber string, amount decimal.Decimal, currency, reservationKey string) error

	// CommitIncoming finalizes a pending credit reservation, applying the
	// credit to Balance + AvailableBalance and writing a ledger entry.
	CommitIncoming(ctx context.Context, reservationKey string) error

	// ReleaseIncoming cancels a pending credit reservation. No-op if already
	// committed/released.
	ReleaseIncoming(ctx context.Context, reservationKey string) error
}

// ExchangeForInterBank is the slice of exchange-service RPCs needed at
// receiver-side Prepare time. Existing exchange client interface uses
// ConvertViaRSD with effective rate.
type ExchangeForInterBank interface {
	ConvertViaRSD(ctx context.Context, from, to string, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal, error)
}

// InterBankFeeRules computes the fee applied to an inbound transfer. Reuses
// the existing fee_service rules table.
type InterBankFeeRules interface {
	ComputeIncomingFee(amount decimal.Decimal, currency string) decimal.Decimal
}

// InterBankServiceConfig carries the timeouts/limits the service needs at
// runtime. Sourced from config.Config in main.go.
type InterBankServiceConfig struct {
	OwnBankCode    string
	ReceiverWait   time.Duration
}

// InterBankService owns the durable 2PC state machine for cross-bank
// transfers. Both the sender and receiver paths live here; role separation
// is by method, not by service instance.
type InterBankService struct {
	db        *gorm.DB
	tx        *repository.InterBankTxRepository
	banks     *repository.BanksRepository
	peers     PeerBankRouter
	accounts  AccountInterBankClient
	exchange  ExchangeForInterBank
	feeRules  InterBankFeeRules
	producer  *kafkaprod.Producer
	cfg       InterBankServiceConfig
}

// NewInterBankService constructs the service.
func NewInterBankService(
	db *gorm.DB,
	txRepo *repository.InterBankTxRepository,
	banksRepo *repository.BanksRepository,
	peers PeerBankRouter,
	accounts AccountInterBankClient,
	exchange ExchangeForInterBank,
	feeRules InterBankFeeRules,
	producer *kafkaprod.Producer,
	cfg InterBankServiceConfig,
) *InterBankService {
	if cfg.OwnBankCode == "" {
		cfg.OwnBankCode = "111"
	}
	return &InterBankService{
		db: db, tx: txRepo, banks: banksRepo,
		peers: peers, accounts: accounts, exchange: exchange,
		feeRules: feeRules, producer: producer, cfg: cfg,
	}
}

// InitiateInput is the input shape for InitiateOutgoing.
type InitiateInput struct {
	SenderAccountNumber   string
	ReceiverAccountNumber string
	Amount                decimal.Decimal
	Currency              string
	Memo                  string
}

// InitiateOutgoing is the sender-side entry point. State transitions follow
// Spec 3 §5.1. The function is synchronous — it returns when one of:
//   - Commit completed (status = committed)
//   - Peer responded NotReady (status = rolled_back)
//   - Network/timeout/5xx (status = reconciling — cron will resolve)
//   - Reservation failed (status = rolled_back, no peer call made)
func (s *InterBankService) InitiateOutgoing(ctx context.Context, in InitiateInput) (*model.InterBankTransaction, error) {
	if len(in.ReceiverAccountNumber) < 3 {
		return nil, status.Error(codes.InvalidArgument, "receiver_account too short")
	}
	receiverBankCode := in.ReceiverAccountNumber[:3]
	if receiverBankCode == s.cfg.OwnBankCode {
		return nil, status.Error(codes.InvalidArgument, "intra-bank transfers should not reach inter-bank service")
	}
	bank, err := s.banks.GetByCode(receiverBankCode)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "unknown_bank: %s", receiverBankCode)
	}
	if !bank.Active {
		return nil, status.Errorf(codes.FailedPrecondition, "bank_inactive: %s", receiverBankCode)
	}
	if !in.Amount.IsPositive() {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}

	txID := uuid.NewString()
	row := &model.InterBankTransaction{
		TxID:                  txID,
		Role:                  model.RoleSender,
		RemoteBankCode:        receiverBankCode,
		SenderAccountNumber:   in.SenderAccountNumber,
		ReceiverAccountNumber: in.ReceiverAccountNumber,
		AmountNative:          in.Amount,
		CurrencyNative:        in.Currency,
		Phase:                 model.PhasePrepare,
		Status:                model.StatusInitiated,
		IdempotencyKey:        txID + ":sender",
	}
	if err := s.tx.Create(row); err != nil {
		return nil, fmt.Errorf("create interbank row: %w", err)
	}

	// Sender debit. Funds are pulled out of AvailableBalance + Balance
	// immediately at initiate time. If the peer says NotReady or any
	// downstream call fails, we credit-back via a different idempotency
	// key in the rollback path.
	debitMemo := fmt.Sprintf("Inter-bank transfer to %s (tx=%s)", in.ReceiverAccountNumber, txID)
	debitKey := "interbank-out-debit-" + txID
	if err := s.accounts.DebitForInterbank(ctx, in.SenderAccountNumber, in.Amount, debitMemo, debitKey); err != nil {
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusInitiated, model.StatusRolledBack)
		_ = s.tx.SetErrorReason(txID, model.RoleSender, "reserve_funds: "+err.Error())
		row.Status = model.StatusRolledBack
		row.ErrorReason = "reserve_funds: " + err.Error()
		s.publishKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row)
		return row, nil
	}
	if err := s.tx.UpdateStatus(txID, model.RoleSender, model.StatusInitiated, model.StatusPreparing); err != nil {
		return row, err
	}
	row.Status = model.StatusPreparing

	client, err := s.peers.ClientFor(receiverBankCode)
	if err != nil {
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusReconciling)
		row.Status = model.StatusReconciling
		return row, nil
	}

	prepResp, err := client.SendPrepare(ctx, PrepareRequest{
		TransactionID:   txID,
		SenderAccount:   in.SenderAccountNumber,
		ReceiverAccount: in.ReceiverAccountNumber,
		Amount:          in.Amount.String(),
		Currency:        in.Currency,
		Memo:            in.Memo,
	})
	if err != nil {
		// Network / timeout / 5xx → reconciling. Funds remain debited until
		// the reconciler decides whether to commit or credit back.
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusReconciling)
		_ = s.tx.SetErrorReason(txID, model.RoleSender, "prepare: "+err.Error())
		row.Status = model.StatusReconciling
		return row, nil
	}
	if !prepResp.Ready {
		// NotReady — credit back, terminal failure.
		s.creditBackOnFailure(ctx, in, txID)
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusNotReadyReceived)
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusNotReadyReceived, model.StatusRolledBack)
		_ = s.tx.SetErrorReason(txID, model.RoleSender, prepResp.Reason)
		row.Status = model.StatusRolledBack
		row.ErrorReason = prepResp.Reason
		s.publishKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row)
		return row, nil
	}

	// Ready — record final terms, send Commit.
	finalAmt, _ := decimal.NewFromString(prepResp.FinalAmount)
	fxRate, _ := decimal.NewFromString(prepResp.FxRate)
	fees, _ := decimal.NewFromString(prepResp.Fees)
	_ = s.tx.SetFinalizedTerms(txID, model.RoleSender, finalAmt, prepResp.FinalCurrency, fxRate, fees)
	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusPreparing, model.StatusReadyReceived)
	row.Status = model.StatusReadyReceived
	row.AmountFinal = &finalAmt
	cf := prepResp.FinalCurrency
	row.CurrencyFinal = &cf
	row.FxRate = &fxRate
	row.FeesFinal = &fees
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankPrepared, row)

	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusReadyReceived, model.StatusCommitting)
	row.Status = model.StatusCommitting

	commitResp, err := client.SendCommit(ctx, CommitOutboundRequest{
		TransactionID: txID,
		FinalAmount:   prepResp.FinalAmount,
		FinalCurrency: prepResp.FinalCurrency,
		FxRate:        prepResp.FxRate,
		Fees:          prepResp.Fees,
	})
	if err != nil {
		// 5xx, network error, or 409 commit_mismatch — reconciler decides.
		_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusCommitting, model.StatusReconciling)
		_ = s.tx.SetErrorReason(txID, model.RoleSender, "commit: "+err.Error())
		row.Status = model.StatusReconciling
		return row, nil
	}
	_ = s.tx.UpdateStatus(txID, model.RoleSender, model.StatusCommitting, model.StatusCommitted)
	row.Status = model.StatusCommitted
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankCommitted, row)
	_ = commitResp
	return row, nil
}

// creditBackOnFailure credits back the sender's debit. Used when the peer
// returns NotReady or the reconciler decides to roll back.
func (s *InterBankService) creditBackOnFailure(ctx context.Context, in InitiateInput, txID string) {
	memo := fmt.Sprintf("Inter-bank rollback for tx=%s", txID)
	key := "interbank-out-credit-back-" + txID
	if err := s.accounts.CreditBackForInterbank(ctx, in.SenderAccountNumber, in.Amount, memo, key); err != nil {
		// Best-effort — the reconciler will retry.
		_ = err
	}
}

// CreditBackByTx loads a sender row, computes the credit-back parameters,
// and credits the sender. Used by the reconciler when a stale transaction
// must be rolled back.
func (s *InterBankService) CreditBackByTx(ctx context.Context, txID string) error {
	row, err := s.tx.Get(txID, model.RoleSender)
	if err != nil {
		return err
	}
	memo := fmt.Sprintf("Inter-bank rollback for tx=%s", txID)
	key := "interbank-out-credit-back-" + txID
	return s.accounts.CreditBackForInterbank(ctx, row.SenderAccountNumber, row.AmountNative, memo, key)
}

// HandlePrepareInput is the input shape for HandlePrepare (receiver side).
type HandlePrepareInput struct {
	TransactionID   string
	SenderBankCode  string
	SenderAccount   string
	ReceiverAccount string
	Amount          decimal.Decimal
	Currency        string
	Memo            string
}

// HandlePrepareOutput is the output shape for HandlePrepare.
type HandlePrepareOutput struct {
	Ready         bool
	FinalAmount   decimal.Decimal
	FinalCurrency string
	FxRate        decimal.Decimal
	Fees          decimal.Decimal
	OriginalAmount decimal.Decimal
	OriginalCurrency string
	ValidUntil    time.Time
	Reason        string
}

// HandlePrepare is the receiver-side Prepare handler. State transitions
// follow Spec 3 §5.2. Idempotent on transactionId — a second call with the
// same id returns the original decision without re-running validation.
func (s *InterBankService) HandlePrepare(ctx context.Context, in HandlePrepareInput) (*HandlePrepareOutput, error) {
	if existing, err := s.tx.Get(in.TransactionID, model.RoleReceiver); err == nil {
		return s.replayPrepareDecision(existing), nil
	}

	row := &model.InterBankTransaction{
		TxID:                  in.TransactionID,
		Role:                  model.RoleReceiver,
		RemoteBankCode:        in.SenderBankCode,
		SenderAccountNumber:   in.SenderAccount,
		ReceiverAccountNumber: in.ReceiverAccount,
		AmountNative:          in.Amount,
		CurrencyNative:        in.Currency,
		Phase:                 model.PhasePrepare,
		Status:                model.StatusPrepareReceived,
		IdempotencyKey:        in.TransactionID + ":receiver",
	}
	// Persist the inbound payload for debugging.
	if pj, _ := json.Marshal(in); pj != nil {
		row.PayloadJSON = pj
	}
	if err := s.tx.Create(row); err != nil {
		return nil, fmt.Errorf("create receiver row: %w", err)
	}

	acct, err := s.accounts.GetAccountByNumber(ctx, in.ReceiverAccount)
	if err != nil {
		return s.markAndReturnNotReady(ctx, in.TransactionID, row, "account_not_found"), nil
	}
	if acct.Status != "active" {
		return s.markAndReturnNotReady(ctx, in.TransactionID, row, "account_inactive"), nil
	}

	finalAmt := in.Amount
	finalCcy := acct.CurrencyCode
	fxRate := decimal.NewFromInt(1)
	if acct.CurrencyCode != in.Currency {
		converted, rate, convErr := s.exchange.ConvertViaRSD(ctx, in.Currency, acct.CurrencyCode, in.Amount)
		if convErr != nil {
			return s.markAndReturnNotReady(ctx, in.TransactionID, row, "currency_not_supported"), nil
		}
		finalAmt = converted
		fxRate = rate
	}
	fees := decimal.Zero
	if s.feeRules != nil {
		fees = s.feeRules.ComputeIncomingFee(finalAmt, finalCcy)
	}
	finalAmt = finalAmt.Sub(fees)
	if !finalAmt.IsPositive() {
		return s.markAndReturnNotReady(ctx, in.TransactionID, row, "fees_exceed_amount"), nil
	}

	reserveKey := "interbank-in-" + in.TransactionID
	if err := s.accounts.ReserveIncoming(ctx, in.ReceiverAccount, finalAmt, finalCcy, reserveKey); err != nil {
		// "limit_exceeded" is the most likely reason; surface that label.
		return s.markAndReturnNotReady(ctx, in.TransactionID, row, "limit_exceeded"), nil
	}

	_ = s.tx.SetFinalizedTerms(in.TransactionID, model.RoleReceiver, finalAmt, finalCcy, fxRate, fees)
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusPrepareReceived, model.StatusValidated)
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusValidated, model.StatusReadySent)
	row.Status = model.StatusReadySent
	row.AmountFinal = &finalAmt
	row.CurrencyFinal = &finalCcy
	row.FxRate = &fxRate
	row.FeesFinal = &fees
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankPrepared, row)

	return &HandlePrepareOutput{
		Ready:            true,
		FinalAmount:      finalAmt,
		FinalCurrency:    finalCcy,
		FxRate:           fxRate,
		Fees:             fees,
		OriginalAmount:   in.Amount,
		OriginalCurrency: in.Currency,
		ValidUntil:       time.Now().UTC().Add(s.cfg.ReceiverWait),
	}, nil
}

func (s *InterBankService) markAndReturnNotReady(ctx context.Context, txID string, row *model.InterBankTransaction, reason string) *HandlePrepareOutput {
	_ = s.tx.UpdateStatus(txID, model.RoleReceiver, model.StatusPrepareReceived, model.StatusNotReadySent)
	_ = s.tx.UpdateStatus(txID, model.RoleReceiver, model.StatusNotReadySent, model.StatusFinalNotReady)
	_ = s.tx.SetErrorReason(txID, model.RoleReceiver, reason)
	row.Status = model.StatusFinalNotReady
	row.ErrorReason = reason
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankRolledBack, row)
	return &HandlePrepareOutput{Ready: false, Reason: reason}
}

func (s *InterBankService) replayPrepareDecision(row *model.InterBankTransaction) *HandlePrepareOutput {
	switch row.Status {
	case model.StatusReadySent, model.StatusCommitReceived, model.StatusCommitted:
		out := &HandlePrepareOutput{Ready: true}
		if row.AmountFinal != nil {
			out.FinalAmount = *row.AmountFinal
		}
		if row.CurrencyFinal != nil {
			out.FinalCurrency = *row.CurrencyFinal
		}
		if row.FxRate != nil {
			out.FxRate = *row.FxRate
		}
		if row.FeesFinal != nil {
			out.Fees = *row.FeesFinal
		}
		out.OriginalAmount = row.AmountNative
		out.OriginalCurrency = row.CurrencyNative
		return out
	default:
		return &HandlePrepareOutput{Ready: false, Reason: row.ErrorReason}
	}
}

// HandleCommitInput is the input shape for HandleCommit.
type HandleCommitInput struct {
	TransactionID string
	FinalAmount   decimal.Decimal
	FinalCurrency string
	FxRate        decimal.Decimal
	Fees          decimal.Decimal
}

// HandleCommitOutput is the output shape for HandleCommit.
type HandleCommitOutput struct {
	Committed        bool
	NotFound         bool
	CreditedAt       time.Time
	CreditedAmount   decimal.Decimal
	CreditedCurrency string
	MismatchReason   string
}

// HandleCommit is the receiver-side Commit handler. Verifies that the
// final terms match what we proposed at Prepare; on match, applies the
// credit and transitions to committed.
func (s *InterBankService) HandleCommit(ctx context.Context, in HandleCommitInput) (*HandleCommitOutput, error) {
	row, err := s.tx.Get(in.TransactionID, model.RoleReceiver)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &HandleCommitOutput{NotFound: true}, nil
		}
		return nil, err
	}
	if row.Status == model.StatusCommitted {
		// Idempotent replay.
		out := &HandleCommitOutput{
			Committed:  true,
			CreditedAt: row.UpdatedAt,
		}
		if row.AmountFinal != nil {
			out.CreditedAmount = *row.AmountFinal
		}
		if row.CurrencyFinal != nil {
			out.CreditedCurrency = *row.CurrencyFinal
		}
		return out, nil
	}
	if row.Status != model.StatusReadySent && row.Status != model.StatusCommitReceived {
		// Receiver has moved past ready_sent (abandoned, final_notready,
		// etc.) — surface as not_found per Spec 3 §9.5.
		return &HandleCommitOutput{NotFound: true}, nil
	}
	if !termsMatch(row, in) {
		return &HandleCommitOutput{
			Committed: false,
			MismatchReason: fmt.Sprintf("expected %s %s; got %s %s",
				strDecPtr(row.AmountFinal), strPtr(row.CurrencyFinal),
				in.FinalAmount.String(), in.FinalCurrency),
		}, nil
	}

	// Move to commit_received before applying the credit so a crash
	// between status-update and CommitIncoming is recoverable: the
	// startup-recovery routine retries CommitIncoming for any
	// commit_received row (idempotent on reservationKey).
	if row.Status == model.StatusReadySent {
		if err := s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver,
			model.StatusReadySent, model.StatusCommitReceived); err != nil {
			return nil, err
		}
	}
	reservationKey := "interbank-in-" + in.TransactionID
	if err := s.accounts.CommitIncoming(ctx, reservationKey); err != nil {
		// Stay in commit_received; receiver-timeout cron / startup recovery
		// will retry. Surface as a 5xx to the sender so it reconciles.
		return nil, fmt.Errorf("commit_incoming: %w", err)
	}
	_ = s.tx.UpdateStatus(in.TransactionID, model.RoleReceiver, model.StatusCommitReceived, model.StatusCommitted)
	row.Status = model.StatusCommitted
	s.publishKafka(ctx, kafkamsg.TopicTransferInterbankReceived, row)
	now := time.Now().UTC()
	out := &HandleCommitOutput{
		Committed:  true,
		CreditedAt: now,
	}
	if row.AmountFinal != nil {
		out.CreditedAmount = *row.AmountFinal
	}
	if row.CurrencyFinal != nil {
		out.CreditedCurrency = *row.CurrencyFinal
	}
	return out, nil
}

func termsMatch(row *model.InterBankTransaction, in HandleCommitInput) bool {
	if row.AmountFinal == nil || row.CurrencyFinal == nil {
		return false
	}
	if !row.AmountFinal.Equal(in.FinalAmount) {
		return false
	}
	if *row.CurrencyFinal != in.FinalCurrency {
		return false
	}
	if row.FxRate != nil && !row.FxRate.Equal(in.FxRate) {
		return false
	}
	if row.FeesFinal != nil && !row.FeesFinal.Equal(in.Fees) {
		return false
	}
	return true
}

// HandleCheckStatusOutput is the output shape for HandleCheckStatus.
type HandleCheckStatusOutput struct {
	NotFound      bool
	TransactionID string
	Role          string
	Status        string
	FinalAmount   string
	FinalCurrency string
	UpdatedAt     time.Time
}

// HandleCheckStatus searches receiver-side rows first (the most common
// inbound CheckStatus is "did we receive a Prepare from this peer?"), then
// falls back to sender-side. Used by the reconciler on the peer's side.
func (s *InterBankService) HandleCheckStatus(ctx context.Context, txID string) (*HandleCheckStatusOutput, error) {
	if row, err := s.tx.Get(txID, model.RoleReceiver); err == nil {
		return toCheckStatus(row), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if row, err := s.tx.Get(txID, model.RoleSender); err == nil {
		return toCheckStatus(row), nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &HandleCheckStatusOutput{NotFound: true, TransactionID: txID}, nil
}

func toCheckStatus(row *model.InterBankTransaction) *HandleCheckStatusOutput {
	out := &HandleCheckStatusOutput{
		TransactionID: row.TxID,
		Role:          row.Role,
		Status:        row.Status,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.AmountFinal != nil {
		out.FinalAmount = row.AmountFinal.String()
	}
	if row.CurrencyFinal != nil {
		out.FinalCurrency = *row.CurrencyFinal
	}
	return out
}

// GetInterBankTransfer is the read-side helper for the public GET
// /api/v1/me/transfers/{id} route. Searches sender-side first (gateway
// looks up by tx_id only), then receiver-side.
func (s *InterBankService) GetInterBankTransfer(ctx context.Context, txID string) (*model.InterBankTransaction, error) {
	if row, err := s.tx.Get(txID, model.RoleSender); err == nil {
		return row, nil
	}
	if row, err := s.tx.Get(txID, model.RoleReceiver); err == nil {
		return row, nil
	}
	return nil, gorm.ErrRecordNotFound
}

// PublishStatusKafka is exposed so crons can publish status-change events
// when they advance a row (reconciler-completed, abandoned-by-timeout, etc.).
func (s *InterBankService) PublishStatusKafka(ctx context.Context, topic string, row *model.InterBankTransaction) {
	s.publishKafka(ctx, topic, row)
}

// publishKafka renders an InterBankTransaction row to the standard message
// shape and emits it on the given topic. Errors are swallowed — Kafka is
// best-effort here.
func (s *InterBankService) publishKafka(ctx context.Context, topic string, row *model.InterBankTransaction) {
	if s.producer == nil {
		return
	}
	msg := kafkamsg.TransferInterbankMessage{
		MessageID:      uuid.NewString(),
		OccurredAt:     time.Now().UTC().Format(time.RFC3339),
		TransactionID:  row.TxID,
		Role:           row.Role,
		RemoteBankCode: row.RemoteBankCode,
		Status:         row.Status,
		AmountNative:   row.AmountNative.String(),
		CurrencyNative: row.CurrencyNative,
		ErrorReason:    row.ErrorReason,
	}
	if row.AmountFinal != nil {
		msg.AmountFinal = row.AmountFinal.String()
	}
	if row.CurrencyFinal != nil {
		msg.CurrencyFinal = *row.CurrencyFinal
	}
	if row.FxRate != nil {
		msg.FxRate = row.FxRate.String()
	}
	if row.FeesFinal != nil {
		msg.Fees = row.FeesFinal.String()
	}
	_ = s.producer.PublishInterbank(ctx, topic, msg)
}

// Repo exposes the underlying repository to package-internal callers
// (reconciler / timeout cron / recovery), keeping the dependency surface
// small while letting them iterate rows directly.
func (s *InterBankService) Repo() *repository.InterBankTxRepository { return s.tx }

// PeerRouter exposes the peer router to crons.
func (s *InterBankService) PeerRouter() PeerBankRouter { return s.peers }

// Accounts exposes the account client to crons (for credit-back during
// reconciler-initiated rollbacks).
func (s *InterBankService) Accounts() AccountInterBankClient { return s.accounts }

func strDecPtr(p *decimal.Decimal) string {
	if p == nil {
		return ""
	}
	return p.String()
}

func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
