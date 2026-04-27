package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	kafkamsg "github.com/exbanka/contract/kafka"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// CrossbankAcceptSaga drives the distributed cross-bank OTC accept saga
// (§5.1 of the design) on top of the shared saga runtime
// (`contract/shared/saga`). The legacy 5-phase BeginPhase / CompletePhase
// orchestration is replaced by a 7-step declarative saga whose ledger is
// persisted via stocksaga.CrossBankRecorder into inter_bank_saga_logs.
//
// Step layout (initiator role):
//
//  1. reserve_buyer_funds   — local reserve via account-service.
//  2. create_contract       — local: persist OptionContract row.
//  3. reserve_seller_shares — peer call: ask seller's bank to reserve shares.
//                             PIVOT: rollback walks stop here. Past this
//                             point both sides have committed; failures
//                             upstream are forward-recovered, not rolled
//                             back to the buyer's funds reservation.
//  4. debit_buyer           — Spec 3 inter-bank transfer (initiate).
//  5. credit_seller         — Spec 3 inter-bank transfer (finalized via the
//                             same Initiate call result; this step's
//                             Forward is a no-op that records the credit
//                             completion in the ledger so audit reads the
//                             saga as 7 distinct steps).
//  6. transfer_ownership    — peer call: ask seller's bank to mark
//                             ownership transferred.
//  7. finalize_accept       — peer call: tell peer to mark contract ACTIVE,
//                             mark local offer ACCEPTED.
//
// The 4-then-5 split mirrors the plan's typed StepKind enum which calls
// out debit_buyer + credit_seller as two separate transitions; the
// underlying transactions-service Initiate call is invoked once during
// step 4 and the result is stashed in saga state for step 5 to publish.
type CrossbankAcceptSaga struct {
	logsRepo    *repository.InterBankSagaLogRepository
	producer    *kafkaprod.Producer
	peers       CrossbankPeerRouter
	accounts    OTCAccountClient
	contracts   OptionContractWriter
	offers      OTCOfferWriter
	transfers   InterBankTransferer
	ownBankCode string

	// publisherFactory builds the LifecyclePublisher for each Run. Tests
	// override this to inject a fake; production callers leave it nil and
	// the saga uses its built-in Kafka adapter over s.producer.
	publisherFactory func(in CrossbankAcceptInput, txID string, contract **model.OptionContract) sharedsaga.LifecyclePublisher

	// peerOpsOverride lets unit tests bypass the production peer-client
	// HTTP path and inject a fake implementing crossbankPeerOps directly.
	// Nil in production; non-nil only in tests via withPeerOps.
	peerOpsOverride crossbankPeerOps
}

// OptionContractWriter is the narrow surface needed to create / update
// OptionContract rows during the saga.
type OptionContractWriter interface {
	Create(c *model.OptionContract) error
	Save(c *model.OptionContract) error
	GetByID(id uint64) (*model.OptionContract, error)
	Delete(id uint64) error
}

// OTCOfferWriter is the narrow surface for marking offer status.
type OTCOfferWriter interface {
	GetByID(id uint64) (*model.OTCOffer, error)
	Save(o *model.OTCOffer) error
}

// InterBankTransferer is the surface needed to drive the funds movement
// step via Spec 3's transaction-service. Implemented by an adapter over
// transactionpb.InterBankServiceClient.
type InterBankTransferer interface {
	Initiate(ctx context.Context, sender, receiver, amount, currency, memo string) (string /* tx_id */, bool /* committed */, string /* failReason */, error)
	Reverse(ctx context.Context, originalTxID, memo string) error
}

// crossbankPeerOps is the narrow surface a saga step needs from the peer
// bank. The production implementation calls through to a
// *CrossbankPeerClient resolved via the CrossbankPeerRouter; tests inject
// a fake directly via withPeerOps to skip the HTTP layer.
type crossbankPeerOps interface {
	ReserveShares(ctx context.Context, req PeerReserveSharesRequest) (*PeerReserveSharesResponse, error)
	TransferOwnership(ctx context.Context, req PeerTransferOwnershipRequest) (*PeerTransferOwnershipResponse, error)
	Finalize(ctx context.Context, req PeerFinalizeRequest) (*PeerFinalizeResponse, error)
	RollbackShares(ctx context.Context, req PeerRollbackSharesRequest) (*PeerRollbackSharesResponse, error)
}

// Compile-time assertion: the production peer client satisfies the
// narrow ops surface. Keeps the two in sync at build time.
var _ crossbankPeerOps = (*CrossbankPeerClient)(nil)

// CrossbankAcceptInput captures the parameters of a cross-bank accept call.
// Mirrors AcceptInput but with explicit bank codes and the buyer's external
// client id (used by the peer to identify the buyer).
type CrossbankAcceptInput struct {
	OfferID                uint64
	BuyerUserID            int64
	BuyerSystemType        string
	BuyerBankCode          string
	BuyerClientIDExternal  string
	BuyerAccountID         uint64
	BuyerAccountNumber     string
	SellerUserID           int64
	SellerSystemType       string
	SellerBankCode         string
	SellerClientIDExternal string
	SellerAccountNumber    string // canonical account number on seller's bank for fund transfer
	Premium                decimal.Decimal
	Currency               string
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	SettlementDate         time.Time
	AssetListingID         uint64 // shared listing id across cohort (faculty seed)
}

// NewCrossbankAcceptSaga constructs the saga with direct references to the
// inter-bank ledger repository and the Kafka producer (the legacy
// CrossbankSagaExecutor is no longer needed — its responsibilities are
// split between the recorder and the lifecycle publisher).
func NewCrossbankAcceptSaga(
	logsRepo *repository.InterBankSagaLogRepository,
	producer *kafkaprod.Producer,
	peers CrossbankPeerRouter,
	accounts OTCAccountClient,
	contracts OptionContractWriter,
	offers OTCOfferWriter,
	transfers InterBankTransferer,
	ownBankCode string,
) *CrossbankAcceptSaga {
	return &CrossbankAcceptSaga{
		logsRepo: logsRepo, producer: producer, peers: peers, accounts: accounts,
		contracts: contracts, offers: offers, transfers: transfers,
		ownBankCode: ownBankCode,
	}
}

// withPublisherFactory is the test seam used by unit tests to inject a
// counting fake LifecyclePublisher. Production callers don't call this.
func (s *CrossbankAcceptSaga) withPublisherFactory(f func(in CrossbankAcceptInput, txID string, contract **model.OptionContract) sharedsaga.LifecyclePublisher) *CrossbankAcceptSaga {
	cp := *s
	cp.publisherFactory = f
	return &cp
}

// withPeerOps is the test seam used by unit tests to inject a fake
// crossbankPeerOps and skip the production HTTP-based peer client.
func (s *CrossbankAcceptSaga) withPeerOps(ops crossbankPeerOps) *CrossbankAcceptSaga {
	cp := *s
	cp.peerOpsOverride = ops
	return &cp
}

// peerOpsFor returns the peer ops for the given bank code. In production
// it resolves through CrossbankPeerRouter; tests get the override set via
// withPeerOps.
func (s *CrossbankAcceptSaga) peerOpsFor(bankCode string) (crossbankPeerOps, error) {
	if s.peerOpsOverride != nil {
		return s.peerOpsOverride, nil
	}
	return s.peers.ClientFor(bankCode)
}

// Run executes the 7-step saga end to end. Returns the new OptionContract
// on success. On failure runs the appropriate compensation chain (up to
// the pivot at step 3) and returns the underlying error from the failing
// step. The saga ID is persisted as the OptionContract.SagaID and used as
// every ledger row's tx_id.
func (s *CrossbankAcceptSaga) Run(ctx context.Context, in CrossbankAcceptInput) (*model.OptionContract, error) {
	if s.peers == nil || s.accounts == nil || s.transfers == nil {
		return nil, errors.New("cross-bank accept saga deps not wired")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindAccept
	remoteCode := in.SellerBankCode

	// contractRef is dereferenced by the publisher adapter so PublishCommitted
	// can include the contract.ID once step 2 (create_contract) runs. Until
	// then it points at nil and the publisher emits ContractID=0, matching
	// the legacy behaviour of starting publishes before contract creation.
	var contract *model.OptionContract
	contractRef := &contract

	rec := stocksaga.NewCrossBankRecorder(s.logsRepo, role, sagaKind, remoteCode, &in.OfferID, nil)
	pub := s.buildPublisher(in, txID, contractRef)

	saga := sharedsaga.NewSagaWithID(txID, rec).WithPublisher(pub)

	// ---- Step 1: reserve_buyer_funds (local) ----
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepReserveBuyerFunds),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepReserveBuyerFunds, in)
			if _, err := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, txIDtoUint64(txID), in.Premium, in.Currency,
				sharedsaga.IdempotencyKey(txID, sharedsaga.StepReserveBuyerFunds)); err != nil {
				return fmt.Errorf("reserve_funds: %w", err)
			}
			return nil
		},
		Backward: func(ctx context.Context, st *sharedsaga.State) error {
			if _, err := s.accounts.ReleaseReservation(ctx, txIDtoUint64(txID),
				sharedsaga.IdempotencyKey(txID, sharedsaga.StepReserveBuyerFunds)+":compensate"); err != nil {
				log.Printf("WARN: cross-bank accept saga=%s: release_reservation compensation: %v", txID, err)
				return err
			}
			return nil
		},
	})

	// ---- Step 2: create_contract (local) ----
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepCreateContract),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepCreateContract, in)
			c := &model.OptionContract{
				OfferID: in.OfferID,
				BuyerUserID: in.BuyerUserID, BuyerSystemType: in.BuyerSystemType,
				BuyerBankCode: ptrStr(in.BuyerBankCode),
				SellerUserID: in.SellerUserID, SellerSystemType: in.SellerSystemType,
				SellerBankCode:  ptrStr(in.SellerBankCode),
				Quantity:        in.Quantity,
				StrikePrice:     in.StrikePrice,
				PremiumPaid:     in.Premium,
				PremiumCurrency: in.Currency,
				StrikeCurrency:  in.Currency,
				SettlementDate:  in.SettlementDate,
				Status:          model.OptionContractStatusActive,
				SagaID:          txID,
				PremiumPaidAt:   time.Now().UTC(),
				CrossbankTxID:   &txID,
			}
			if err := s.contracts.Create(c); err != nil {
				return fmt.Errorf("create_contract: %w", err)
			}
			contract = c
			st.Set("contract_id", c.ID)
			return nil
		},
		Backward: func(ctx context.Context, st *sharedsaga.State) error {
			if contract == nil || contract.ID == 0 {
				return nil
			}
			return s.contracts.Delete(contract.ID)
		},
	})

	// ---- Step 3: reserve_seller_shares (peer call). PIVOT. ----
	saga.Add(sharedsaga.Step{
		Name:  sharedsaga.MustStep(sharedsaga.StepReserveSellerShares),
		Pivot: true,
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepReserveSellerShares, in)
			peer, err := s.peerOpsFor(in.SellerBankCode)
			if err != nil {
				return err
			}
			cid := uint64(0)
			if contract != nil {
				cid = contract.ID
			}
			resp, err := peer.ReserveShares(ctx, PeerReserveSharesRequest{
				TxID: txID, SagaKind: sagaKind, OfferID: in.OfferID, ContractID: cid,
				AssetListingID: in.AssetListingID, Quantity: in.Quantity.String(),
				BuyerBankCode: in.BuyerBankCode, SellerBankCode: in.SellerBankCode,
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
		// Pre-pivot Backward: only invoked when this step itself fails (not
		// when a downstream step fails — Pivot stops the rollback walk after
		// this point). When the peer rejected our ReserveShares call there
		// is nothing on the peer to undo; this Backward is intentionally a
		// no-op and the pre-pivot peer rollback path lives in the failure
		// branch of step 3's Forward (we never reach a state where peer
		// confirmed and a later pre-pivot step needs us to undo it).
	})

	// ---- Step 4: debit_buyer (Spec 3 inter-bank transfer initiate) ----
	// Past the pivot. No Backward — the rollback walk stops at step 3.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepDebitBuyer),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepDebitBuyer, in)
			transferTxID, committed, failReason, err := s.transfers.Initiate(ctx,
				in.BuyerAccountNumber, in.SellerAccountNumber,
				in.Premium.String(), in.Currency,
				"OTC accept "+txID,
			)
			if err != nil {
				return fmt.Errorf("debit_buyer: %w", err)
			}
			if !committed {
				if failReason == "" {
					failReason = "inter-bank transfer not committed"
				}
				return errors.New("debit_buyer: " + failReason)
			}
			st.Set("transfer_tx_id", transferTxID)
			return nil
		},
	})

	// ---- Step 5: credit_seller (records the credit-side completion) ----
	// Past the pivot. The corresponding ledger movement was committed by
	// step 4's Initiate call; this step exists so the typed StepKind's
	// debit_buyer/credit_seller pair surfaces in the audit log as two
	// distinct rows, matching the plan's 7-step layout.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepCreditSeller),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepCreditSeller, in)
			if v, ok := st.Get("transfer_tx_id"); ok {
				if id, ok := v.(string); ok {
					log.Printf("OTC accept saga=%s credit_seller via inter-bank tx=%s", txID, id)
				}
			}
			return nil
		},
	})

	// ---- Step 6: transfer_ownership (peer call) ----
	// Past the pivot. No Backward.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepTransferOwnership),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepTransferOwnership, in)
			peer, err := s.peerOpsFor(in.SellerBankCode)
			if err != nil {
				return err
			}
			cid := uint64(0)
			if contract != nil {
				cid = contract.ID
			}
			resp, err := peer.TransferOwnership(ctx, PeerTransferOwnershipRequest{
				TxID: txID, ContractID: cid,
				AssetListingID: in.AssetListingID, Quantity: in.Quantity.String(),
				FromBankCode: in.SellerBankCode, FromClientIDExternal: in.SellerClientIDExternal,
				ToBankCode: in.BuyerBankCode, ToClientIDExternal: in.BuyerClientIDExternal,
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

	// ---- Step 7: finalize_accept (peer call + local offer status) ----
	// Past the pivot. No Backward.
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepFinalizeAccept),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashStepPayload(st, sharedsaga.StepFinalizeAccept, in)
			peer, err := s.peerOpsFor(in.SellerBankCode)
			if err != nil {
				return err
			}
			cid := uint64(0)
			if contract != nil {
				cid = contract.ID
			}
			// Best-effort peer call — peer-side bookkeeping; failure here
			// must not roll back the saga (we're past the pivot).
			_, _ = peer.Finalize(ctx, PeerFinalizeRequest{
				TxID: txID, ContractID: cid, OfferID: in.OfferID,
				BuyerBankCode: in.BuyerBankCode, SellerBankCode: in.SellerBankCode,
				BuyerClientIDExternal: in.BuyerClientIDExternal, SellerClientIDExternal: in.SellerClientIDExternal,
				StrikePrice: in.StrikePrice.String(), Quantity: in.Quantity.String(),
				Premium: in.Premium.String(), Currency: in.Currency,
				SettlementDate: in.SettlementDate.Format("2006-01-02"),
				SagaKind:       sagaKind,
			})
			// Mark the offer ACCEPTED locally. Best-effort.
			if o, err := s.offers.GetByID(in.OfferID); err == nil && o != nil {
				o.Status = model.OTCOfferStatusAccepted
				_ = s.offers.Save(o)
			}
			return nil
		},
	})

	if err := saga.Execute(ctx, sharedsaga.NewState()); err != nil {
		return nil, err
	}
	return contract, nil
}

// buildPublisher returns the LifecyclePublisher for this run. Production
// callers get the Kafka-emitting adapter; tests inject a counting fake via
// withPublisherFactory.
func (s *CrossbankAcceptSaga) buildPublisher(in CrossbankAcceptInput, txID string, contractRef **model.OptionContract) sharedsaga.LifecyclePublisher {
	if s.publisherFactory != nil {
		return s.publisherFactory(in, txID, contractRef)
	}
	return &crossbankAcceptPublisher{
		producer:    s.producer,
		sagaKind:    model.SagaKindAccept,
		txID:        txID,
		initiator:   in.BuyerBankCode,
		responder:   in.SellerBankCode,
		offerID:     in.OfferID,
		contractRef: contractRef,
	}
}

// crossbankAcceptPublisher adapts sharedsaga.LifecyclePublisher to the
// existing Kafka topic vocabulary previously emitted by the legacy
// CrossbankSagaExecutor's Publish* methods. One adapter per saga.Run().
//
// contractRef is read at OnCommitted time so the contract.ID — created by
// step 2 — is available when the saga reports success. nil-safe.
type crossbankAcceptPublisher struct {
	producer    *kafkaprod.Producer
	sagaKind    string
	txID        string
	initiator   string
	responder   string
	offerID     uint64
	contractRef **model.OptionContract
}

func (p *crossbankAcceptPublisher) OnStarted(ctx context.Context, _ string) {
	if p.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaStartedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, OfferID: p.offerID,
		InitiatorBankCode: p.initiator, ResponderBankCode: p.responder,
		StartedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStarted, data)
	}
}

func (p *crossbankAcceptPublisher) OnCommitted(ctx context.Context, _ string) {
	if p.producer == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	cid := uint64(0)
	if p.contractRef != nil && *p.contractRef != nil {
		cid = (*p.contractRef).ID
	}
	payload := kafkamsg.CrossBankSagaCommittedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, ContractID: cid, CommittedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaCommitted, data)
	}
}

func (p *crossbankAcceptPublisher) OnRolledBack(ctx context.Context, _ string, failingStep, reason string, compensated []string) {
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

func (p *crossbankAcceptPublisher) OnStuck(ctx context.Context, _ string, failingStep, reason string) {
	if p.producer == nil {
		return
	}
	cid := uint64(0)
	if p.contractRef != nil && *p.contractRef != nil {
		cid = (*p.contractRef).ID
	}
	payload := kafkamsg.CrossBankSagaStuckRollbackMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: p.txID, ContractID: cid,
		Reason: "stuck after " + failingStep + ": " + reason,
	}
	if data, err := json.Marshal(payload); err == nil {
		_ = p.producer.PublishRaw(ctx, kafkamsg.TopicOTCCrossbankSagaStuckRollback, data)
	}
}

// stashStepPayload serializes the saga input under the key the
// CrossBankRecorder reads (`crossbank:<step>:payload`) so the recorder
// captures the full request body in the ledger row's payload_json column.
// Best-effort: a JSON marshal failure leaves the payload empty rather than
// abort the step (the audit row remains useful even without payload).
//
// We use a tiny mutex to keep the State writes safe under future concurrent
// step execution; today the executor is single-goroutine but the saga's
// State is documented as thread-safe for that exact reason.
var stashMu sync.Mutex

func stashStepPayload(st *sharedsaga.State, step sharedsaga.StepKind, in CrossbankAcceptInput) {
	if st == nil {
		return
	}
	data, err := json.Marshal(in)
	if err != nil {
		return
	}
	stashMu.Lock()
	st.Set("crossbank:"+string(step)+":payload", data)
	stashMu.Unlock()
}

// txIDtoUint64 hashes the saga UUID into a uint64 idempotency key for the
// account-service's reservation API which still uses uint64 keys.
//
// Trivial fold over the UUID's hex digits — we just need a stable
// non-colliding mapping. Same UUID always produces the same uint64.
func txIDtoUint64(txID string) uint64 {
	// Strip dashes and parse the first 16 hex chars as a uint64.
	clean := ""
	for _, r := range txID {
		if r != '-' {
			clean += string(r)
		}
		if len(clean) >= 16 {
			break
		}
	}
	if len(clean) < 16 {
		clean = (clean + "0000000000000000")[:16]
	}
	v, err := strconv.ParseUint(clean, 16, 64)
	if err != nil {
		return 0
	}
	return v
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
