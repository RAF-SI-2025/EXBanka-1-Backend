package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	kafkamsg "github.com/exbanka/contract/kafka"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// CrossbankExerciseSaga drives the distributed cross-bank exercise saga
// (Spec 4 / Celina 5) on top of the shared saga runtime
// (`contract/shared/saga`). The legacy 5-phase BeginPhase / CompletePhase
// orchestration is replaced by a 6-step declarative saga whose ledger is
// persisted via stocksaga.CrossBankRecorder into inter_bank_saga_logs.
//
// Step layout (initiator role; initiator is the contract buyer who is
// exercising):
//
//  1. reserve_buyer_funds   — local: reserve the buyer's strike funds via
//                             account-service.
//  2. reserve_seller_shares — peer: ask seller's bank to reserve the
//                             shares about to be delivered.
//  3. debit_strike          — Spec 3 inter-bank transfer (Initiate). Moves
//                             the strike funds from buyer to seller.
//  4. credit_strike         — Records the credit-side completion of the
//                             same Spec 3 transfer (no extra business
//                             effect; emits a separate audit row).
//                             PIVOT: rollback walk halts here. Past this
//                             point the inter-bank funds movement has
//                             committed; downstream failures forward-
//                             recover, they do not roll back the funds.
//  5. transfer_ownership    — peer: ask seller's bank to consume the
//                             reservation and transfer share ownership.
//  6. finalize              — peer Finalize + local: mark contract
//                             EXERCISED. Best-effort; failures past the
//                             pivot do not roll back.
//
// Pivot-semantic note (vs. legacy CrossbankSagaExecutor):
// the legacy code, on phase-4 (transfer_ownership) failure, ran
// `transfers.Reverse + peer.RollbackShares + releaseStrike` to undo
// phases 1-3. Under the shared.Saga model, putting the pivot at step 4
// (credit_strike) means failures of steps 5-6 do NOT trigger that
// reversal — funds stay moved, ownership transfer is retried by the
// recovery cron. This is the documented behavior of the new model
// (per design doc) and intentional: once Spec 3 commits the inter-bank
// transfer there is no clean way to atomically reverse it AND the
// share-ownership change, so we forward-recover instead.
type CrossbankExerciseSaga struct {
	logsRepo    *repository.InterBankSagaLogRepository
	producer    *kafkaprod.Producer
	peers       CrossbankPeerRouter
	accounts    OTCAccountClient
	contracts   OptionContractWriter
	transfers   InterBankTransferer
	ownBankCode string

	// publisherFactory builds the LifecyclePublisher for each Run. Tests
	// override this to inject a fake; production callers leave it nil and
	// the saga uses its built-in Kafka adapter over s.producer.
	publisherFactory func(c *model.OptionContract, in CrossbankExerciseInput, txID string) sharedsaga.LifecyclePublisher

	// peerOpsOverride lets unit tests bypass the production peer-client
	// HTTP path and inject a fake implementing crossbankPeerOps directly.
	// Nil in production; non-nil only in tests via withPeerOps.
	peerOpsOverride crossbankPeerOps
}

// NewCrossbankExerciseSaga constructs the saga with direct references to the
// inter-bank ledger repository and the Kafka producer (the legacy
// CrossbankSagaExecutor is no longer needed — its responsibilities are
// split between the recorder and the lifecycle publisher).
func NewCrossbankExerciseSaga(
	logsRepo *repository.InterBankSagaLogRepository,
	producer *kafkaprod.Producer,
	peers CrossbankPeerRouter,
	accounts OTCAccountClient,
	contracts OptionContractWriter,
	transfers InterBankTransferer,
	ownBankCode string,
) *CrossbankExerciseSaga {
	return &CrossbankExerciseSaga{
		logsRepo: logsRepo, producer: producer, peers: peers, accounts: accounts,
		contracts: contracts, transfers: transfers, ownBankCode: ownBankCode,
	}
}

// CrossbankExerciseInput carries the accounts on each side plus the saga's
// canonical asset listing id so the peer can locate the security row.
type CrossbankExerciseInput struct {
	ContractID             uint64
	BuyerAccountID         uint64
	BuyerAccountNumber     string
	BuyerClientIDExternal  string
	SellerAccountNumber    string
	SellerClientIDExternal string
	AssetListingID         uint64
}

// withPublisherFactory is the test seam used by unit tests to inject a
// counting fake LifecyclePublisher. Production callers don't call this.
func (s *CrossbankExerciseSaga) withPublisherFactory(f func(c *model.OptionContract, in CrossbankExerciseInput, txID string) sharedsaga.LifecyclePublisher) *CrossbankExerciseSaga {
	cp := *s
	cp.publisherFactory = f
	return &cp
}

// withPeerOps is the test seam used by unit tests to inject a fake
// crossbankPeerOps and skip the production HTTP-based peer client.
func (s *CrossbankExerciseSaga) withPeerOps(ops crossbankPeerOps) *CrossbankExerciseSaga {
	cp := *s
	cp.peerOpsOverride = ops
	return &cp
}

// peerOpsFor returns the peer ops for the given bank code. In production
// it resolves through CrossbankPeerRouter; tests get the override set via
// withPeerOps.
func (s *CrossbankExerciseSaga) peerOpsFor(bankCode string) (crossbankPeerOps, error) {
	if s.peerOpsOverride != nil {
		return s.peerOpsOverride, nil
	}
	return s.peers.ClientFor(bankCode)
}

// Run executes the 6-step saga end to end. Returns the updated
// OptionContract on success. On failure runs the appropriate compensation
// chain (up to the pivot at step 4) and returns the underlying error from
// the failing step. Strike amount = qty * strike_price.
func (s *CrossbankExerciseSaga) Run(ctx context.Context, in CrossbankExerciseInput) (*model.OptionContract, error) {
	if s.peers == nil || s.accounts == nil || s.transfers == nil {
		return nil, errors.New("cross-bank exercise saga deps not wired")
	}
	c, err := s.contracts.GetByID(in.ContractID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.OptionContractStatusActive {
		return nil, errors.New("contract is not active")
	}
	if !c.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("contract has expired")
	}
	buyerCode := derefOr(c.BuyerBankCode, "")
	sellerCode := derefOr(c.SellerBankCode, "")
	if buyerCode == "" || sellerCode == "" || buyerCode == sellerCode {
		return nil, errors.New("contract is not cross-bank")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindExercise
	strikeAmt := c.Quantity.Mul(c.StrikePrice)
	currency := c.StrikeCurrency

	rec := stocksaga.NewCrossBankRecorder(s.logsRepo, role, sagaKind, sellerCode, &c.OfferID, &c.ID)
	pub := s.buildPublisher(c, in, txID)

	saga := sharedsaga.NewSagaWithID(txID, rec).WithPublisher(pub)

	// ---- Step 1: reserve_buyer_funds (local) ----
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepReserveBuyerFunds),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepReserveBuyerFunds, in)
			if _, err := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, txIDtoUint64(txID), strikeAmt, currency); err != nil {
				return fmt.Errorf("reserve_buyer_funds: %w", err)
			}
			return nil
		},
		Backward: func(ctx context.Context, _ *sharedsaga.State) error {
			if _, err := s.accounts.ReleaseReservation(ctx, txIDtoUint64(txID)); err != nil {
				log.Printf("WARN: cross-bank exercise saga=%s: release_reservation compensation: %v", txID, err)
				return err
			}
			return nil
		},
	})

	// ---- Step 2: reserve_seller_shares (peer call) ----
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepReserveSellerShares),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepReserveSellerShares, in)
			peer, err := s.peerOpsFor(sellerCode)
			if err != nil {
				return err
			}
			resp, err := peer.ReserveShares(ctx, PeerReserveSharesRequest{
				TxID: txID, SagaKind: sagaKind, OfferID: c.OfferID, ContractID: c.ID,
				AssetListingID: in.AssetListingID, Quantity: c.Quantity.String(),
				BuyerBankCode: buyerCode, SellerBankCode: sellerCode,
			})
			if err != nil {
				return err
			}
			if !resp.Confirmed {
				if resp.FailReason != "" {
					return errors.New(resp.FailReason)
				}
				return errors.New("reserve_seller_shares: peer did not confirm")
			}
			return nil
		},
		Backward: func(ctx context.Context, _ *sharedsaga.State) error {
			peer, err := s.peerOpsFor(sellerCode)
			if err != nil {
				return err
			}
			if _, err := peer.RollbackShares(ctx, PeerRollbackSharesRequest{TxID: txID, ContractID: c.ID}); err != nil {
				log.Printf("WARN: cross-bank exercise saga=%s: peer rollback_shares compensation: %v", txID, err)
				return err
			}
			return nil
		},
	})

	// ---- Step 3: debit_strike (Spec 3 inter-bank transfer initiate) ----
	// Mirrors accept's debit_buyer + credit_seller pattern: a single
	// transactions-service Initiate call yields BOTH the debit and the
	// credit. Step 3 issues the call and stashes the transfer tx id;
	// step 4 records the credit-side completion as a separate audit row
	// so the typed StepKind (debit_strike + credit_strike) surfaces in
	// the ledger as two distinct rows matching the plan's enum.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepDebitStrike),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepDebitStrike, in)
			transferTxID, committed, failReason, err := s.transfers.Initiate(ctx,
				in.BuyerAccountNumber, in.SellerAccountNumber,
				strikeAmt.String(), currency,
				"OTC exercise "+txID,
			)
			if err != nil {
				return fmt.Errorf("debit_strike: %w", err)
			}
			if !committed {
				if failReason == "" {
					failReason = "inter-bank transfer not committed"
				}
				return errors.New("debit_strike: " + failReason)
			}
			st.Set("transfer_tx_id", transferTxID)
			return nil
		},
		Backward: func(ctx context.Context, st *sharedsaga.State) error {
			// Pre-pivot compensation: only invoked when this step itself
			// fails (the peer transfer was not yet committed). If a later
			// pre-pivot step fails after debit_strike succeeded, the
			// shared.Saga executor walks here too — in that case we Reverse
			// the inter-bank transfer.
			if v, ok := st.Get("transfer_tx_id"); ok {
				if id, ok := v.(string); ok && id != "" {
					if err := s.transfers.Reverse(ctx, id, "OTC exercise compensation "+txID); err != nil {
						log.Printf("WARN: cross-bank exercise saga=%s: reverse transfer compensation: %v", txID, err)
						return err
					}
				}
			}
			return nil
		},
	})

	// ---- Step 4: credit_strike (records the credit-side completion). PIVOT. ----
	// Past the pivot. The corresponding ledger movement was committed by
	// step 3's Initiate call; this step exists so the typed StepKind's
	// debit_strike/credit_strike pair surfaces in the audit log as two
	// distinct rows, matching the plan's enum decomposition.
	saga.Add(sharedsaga.Step{
		Name:  sharedsaga.MustStep(sharedsaga.StepCreditStrike),
		Pivot: true,
		Forward: func(_ context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepCreditStrike, in)
			if v, ok := st.Get("transfer_tx_id"); ok {
				if id, ok := v.(string); ok {
					log.Printf("OTC exercise saga=%s credit_strike via inter-bank tx=%s", txID, id)
				}
			}
			return nil
		},
	})

	// ---- Step 5: transfer_ownership (peer call) ----
	// Past the pivot. No Backward — the rollback walk stops at step 4.
	// We retain StepKind = StepTransferOwnership (string "transfer_ownership")
	// because the responder side persists rows under this exact phase
	// string (model.PhaseTransferOwnership) and the CHECK_STATUS reconciler
	// queries the peer with row.Phase as the wire identifier; using
	// StepDeliverShares ("deliver_shares") would break that lookup.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepTransferOwnership),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepTransferOwnership, in)
			peer, err := s.peerOpsFor(sellerCode)
			if err != nil {
				return err
			}
			resp, err := peer.TransferOwnership(ctx, PeerTransferOwnershipRequest{
				TxID: txID, ContractID: c.ID,
				AssetListingID: in.AssetListingID, Quantity: c.Quantity.String(),
				FromBankCode: sellerCode, FromClientIDExternal: in.SellerClientIDExternal,
				ToBankCode: buyerCode, ToClientIDExternal: in.BuyerClientIDExternal,
			})
			if err != nil {
				return fmt.Errorf("transfer_ownership: %w", err)
			}
			if !resp.Confirmed {
				if resp.FailReason != "" {
					return errors.New("transfer_ownership: " + resp.FailReason)
				}
				return errors.New("transfer_ownership: peer did not confirm")
			}
			return nil
		},
	})

	// ---- Step 6: finalize (peer call + local mark EXERCISED) ----
	// Past the pivot. No Backward.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepFinalizeAccept),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExercisePayload(st, sharedsaga.StepFinalizeAccept, in)
			now := time.Now().UTC()
			c.Status = model.OptionContractStatusExercised
			c.ExercisedAt = &now
			c.CrossbankExerciseTxID = &txID
			if err := s.contracts.Save(c); err != nil {
				// Best-effort: log but don't fail the saga past the pivot;
				// recovery cron will reconcile.
				log.Printf("WARN: cross-bank exercise saga=%s: contracts.Save: %v", txID, err)
			}
			peer, err := s.peerOpsFor(sellerCode)
			if err != nil {
				return nil
			}
			// Best-effort peer call.
			_, _ = peer.Finalize(ctx, PeerFinalizeRequest{
				TxID: txID, ContractID: c.ID, OfferID: c.OfferID,
				BuyerBankCode: buyerCode, SellerBankCode: sellerCode,
				BuyerClientIDExternal: in.BuyerClientIDExternal, SellerClientIDExternal: in.SellerClientIDExternal,
				StrikePrice: c.StrikePrice.String(), Quantity: c.Quantity.String(),
				Premium: c.PremiumPaid.String(), Currency: currency,
				SettlementDate: c.SettlementDate.Format("2006-01-02"),
				SagaKind:       sagaKind,
			})
			return nil
		},
	})

	if err := saga.Execute(ctx, sharedsaga.NewState()); err != nil {
		return nil, err
	}
	return c, nil
}

// buildPublisher returns the LifecyclePublisher for this run. Production
// callers get the Kafka-emitting adapter; tests inject a counting fake via
// withPublisherFactory.
func (s *CrossbankExerciseSaga) buildPublisher(c *model.OptionContract, in CrossbankExerciseInput, txID string) sharedsaga.LifecyclePublisher {
	if s.publisherFactory != nil {
		return s.publisherFactory(c, in, txID)
	}
	buyerCode := derefOr(c.BuyerBankCode, "")
	sellerCode := derefOr(c.SellerBankCode, "")
	return &crossbankExercisePublisher{
		producer:   s.producer,
		sagaKind:   model.SagaKindExercise,
		txID:       txID,
		initiator:  buyerCode,
		responder:  sellerCode,
		offerID:    c.OfferID,
		contractID: c.ID,
	}
}

// crossbankExercisePublisher adapts sharedsaga.LifecyclePublisher to the
// existing Kafka topic vocabulary previously emitted by the legacy
// CrossbankSagaExecutor's Publish* methods. One adapter per saga.Run().
type crossbankExercisePublisher struct {
	producer   *kafkaprod.Producer
	sagaKind   string
	txID       string
	initiator  string
	responder  string
	offerID    uint64
	contractID uint64
}

func (p *crossbankExercisePublisher) OnStarted(ctx context.Context, _ string) {
	if p.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaStartedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, OfferID: p.offerID, ContractID: p.contractID,
		InitiatorBankCode: p.initiator, ResponderBankCode: p.responder,
		StartedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStarted, data)
	}
}

func (p *crossbankExercisePublisher) OnCommitted(ctx context.Context, _ string) {
	if p.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaCommittedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, ContractID: p.contractID, CommittedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaCommitted, data)
	}
}

func (p *crossbankExercisePublisher) OnRolledBack(ctx context.Context, _ string, failingStep, reason string, compensated []string) {
	if p.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaRolledBackMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, FailingPhase: failingStep,
		Reason: reason, CompensatedPhases: compensated, RolledBackAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaRolledBack, data)
	}
}

func (p *crossbankExercisePublisher) OnStuck(ctx context.Context, _ string, failingStep, reason string) {
	if p.producer == nil {
		return
	}
	payload := kafkamsg.CrossBankSagaStuckRollbackMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: p.txID, ContractID: p.contractID,
		Reason: "stuck after " + failingStep + ": " + reason,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStuckRollback, data)
	}
}

// stashExercisePayload mirrors stashStepPayload but for the exercise saga
// input. Serializes the saga input under the key the CrossBankRecorder
// reads (`crossbank:<step>:payload`) so the recorder captures the full
// request body in the ledger row's payload_json column. Best-effort: a
// JSON marshal failure leaves the payload empty rather than abort the step.
var stashExerciseMu sync.Mutex

func stashExercisePayload(st *sharedsaga.State, step sharedsaga.StepKind, in CrossbankExerciseInput) {
	if st == nil {
		return
	}
	data, err := json.Marshal(in)
	if err != nil {
		return
	}
	stashExerciseMu.Lock()
	st.Set("crossbank:"+string(step)+":payload", data)
	stashExerciseMu.Unlock()
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
