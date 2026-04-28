package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	sharedsaga "github.com/exbanka/contract/shared/saga"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	stocksaga "github.com/exbanka/stock-service/internal/saga"
)

// CrossbankExpireSaga drives the cross-bank expire flow for OTC option
// contracts on top of the shared saga runtime (`contract/shared/saga`).
// The legacy 2-phase BeginPhase / CompletePhase orchestration is replaced
// by a 2-step declarative saga whose ledger is persisted via
// stocksaga.CrossBankRecorder into inter_bank_saga_logs.
//
// Step layout (initiator role; initiator is whichever bank holds the
// contract — typically the seller bank that issued the option):
//
//  1. refund_reservation — peer call: notify the OTHER bank that the
//     contract has expired so it releases its side.
//     BEST-EFFORT: peer notify failures do NOT fail
//     the saga (they are logged); the local cron
//     will retry. No Backward (nothing local to
//     undo if peer rejects).
//  2. mark_expired       — local: release the seller-side holding
//     reservation (if we are the seller) and mark
//     the OptionContract row EXPIRED.
//     PIVOT: this is the terminal commit; nothing
//     past it.
//
// Pivot-semantic note: marking step 2 as Pivot means a hypothetical
// future post-pivot step would not roll back step 1; today there is
// no post-pivot step so the pivot has no observable effect on
// compensation behavior. It is included to match the design plan
// (`StepMarkExpired (Pivot)`) and document the intended saga shape.
//
// Wire-protocol note: the new step strings (`refund_reservation`,
// `mark_expired`) differ from the legacy phase strings
// (`expire_notify`, `expire_apply`). No external system reads the
// legacy strings — the responder side has no row for either phase
// (the responder's HandleContractExpire performs a pure side-effect
// without writing the saga log) and the CHECK_STATUS reconciler only
// queries pending rows, which expire transitions through fast enough
// to never trigger reconciliation. Safe to rename for semantic
// clarity.
type CrossbankExpireSaga struct {
	logsRepo   *repository.InterBankSagaLogRepository
	producer   *kafkaprod.Producer
	peers      CrossbankPeerRouter
	contracts  OptionContractWriter
	holdingRes OTCHoldingReleaser
	ownBank    string

	// publisherFactory builds the LifecyclePublisher for each Run. Tests
	// override this to inject a fake; production callers leave it nil.
	publisherFactory func(c *model.OptionContract, txID, peerCode string) sharedsaga.LifecyclePublisher

	// peerOpsOverride lets unit tests bypass the production peer-client
	// HTTP path and inject a fake. Nil in production.
	peerOpsOverride crossbankPeerOps

	// Outbox: when wired (via WithOutbox), lifecycle Kafka publishes go
	// through the transactional outbox instead of best-effort
	// producer.PublishRaw. See CrossbankAcceptSaga.WithOutbox for details.
	outbox   *outbox.Outbox
	outboxDB *gorm.DB
}

// WithOutbox wires the transactional outbox + the GORM handle the saga
// uses to enqueue rows. Callers that don't wire this fall back to the
// legacy direct-publish path (best-effort, may drop on crash).
func (s *CrossbankExpireSaga) WithOutbox(ob *outbox.Outbox, db *gorm.DB) *CrossbankExpireSaga {
	cp := *s
	cp.outbox = ob
	cp.outboxDB = db
	return &cp
}

// OTCHoldingReleaser is the narrow surface needed by the expire saga to
// release a seller-side OTC holding reservation. Implemented by
// *HoldingReservationService; tests inject a fake.
type OTCHoldingReleaser interface {
	ReleaseForOTCContract(ctx context.Context, otcContractID uint64) (*ReleaseHoldingResult, error)
}

// Compile-time guard: the production HoldingReservationService satisfies
// the narrow surface used by the expire saga.
var _ OTCHoldingReleaser = (*HoldingReservationService)(nil)

func NewCrossbankExpireSaga(
	logsRepo *repository.InterBankSagaLogRepository,
	producer *kafkaprod.Producer,
	peers CrossbankPeerRouter,
	contracts OptionContractWriter,
	holdingRes OTCHoldingReleaser,
	ownBank string,
) *CrossbankExpireSaga {
	return &CrossbankExpireSaga{
		logsRepo: logsRepo, producer: producer, peers: peers,
		contracts: contracts, holdingRes: holdingRes, ownBank: ownBank,
	}
}

// withPublisherFactory is the test seam used by unit tests.
func (s *CrossbankExpireSaga) withPublisherFactory(f func(c *model.OptionContract, txID, peerCode string) sharedsaga.LifecyclePublisher) *CrossbankExpireSaga {
	cp := *s
	cp.publisherFactory = f
	return &cp
}

// withPeerOps injects a fake peer ops for tests.
func (s *CrossbankExpireSaga) withPeerOps(ops crossbankPeerOps) *CrossbankExpireSaga {
	cp := *s
	cp.peerOpsOverride = ops
	return &cp
}

// crossbankExpireOps is the narrow peer-call surface used by the expire
// saga (only ContractExpire). Wider crossbankPeerOps would force test
// fakes to implement methods the saga never invokes.
type crossbankExpireOps interface {
	ContractExpire(ctx context.Context, req PeerContractExpireRequest) (*PeerContractExpireResponse, error)
}

// peerExpireFor adapts the saga's broader peer ops to the narrow expire
// surface so the same withPeerOps test seam works for both peer-call sites.
func (s *CrossbankExpireSaga) peerExpireFor(bankCode string) (crossbankExpireOps, error) {
	if s.peerOpsOverride != nil {
		// crossbankPeerOps doesn't expose ContractExpire (the production
		// CrossbankPeerClient does). When tests override peer ops they
		// pass a fake that implements both surfaces; we type-assert so
		// the saga can ask for the narrower surface.
		if e, ok := s.peerOpsOverride.(crossbankExpireOps); ok {
			return e, nil
		}
		return nil, errors.New("test peer override does not implement ContractExpire")
	}
	return s.peers.ClientFor(bankCode)
}

// ExpireContract drives the 2-step expire saga for a cross-bank contract.
// Caller is the bank that holds the EXPIRED side (typically the seller —
// the bank that issued the option). Idempotent: re-running on an already
// EXPIRED contract is a no-op.
func (s *CrossbankExpireSaga) ExpireContract(ctx context.Context, contractID uint64) error {
	c, err := s.contracts.GetByID(contractID)
	if err != nil {
		return err
	}
	if c.Status == model.OptionContractStatusExpired {
		return nil
	}
	if c.Status != model.OptionContractStatusActive {
		return errors.New("contract is not active")
	}

	buyerCode := derefOr(c.BuyerBankCode, "")
	sellerCode := derefOr(c.SellerBankCode, "")
	if buyerCode == "" || sellerCode == "" || buyerCode == sellerCode {
		return errors.New("contract is not cross-bank")
	}

	// Determine the peer (the OTHER bank) — we send the notify to them.
	var peerCode string
	switch s.ownBank {
	case sellerCode:
		peerCode = buyerCode
	case buyerCode:
		peerCode = sellerCode
	default:
		return errors.New("own bank is neither buyer nor seller for this contract")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindExpire

	rec := stocksaga.NewCrossBankRecorder(s.logsRepo, role, sagaKind, peerCode, &c.OfferID, &c.ID)
	pub := s.buildPublisher(c, txID, peerCode)

	saga := sharedsaga.NewSagaWithID(txID, rec).WithPublisher(pub)

	// ---- Step 1: refund_reservation (peer notify; best-effort) ----
	saga.Add(sharedsaga.Step{
		Name: sharedsaga.MustStep(sharedsaga.StepRefundReservation),
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExpirePayload(st, sharedsaga.StepRefundReservation, contractID)
			peer, err := s.peerExpireFor(peerCode)
			if err != nil {
				// Best-effort: log and continue.
				log.Printf("WARN: cross-bank expire saga=%s: peer router: %v", txID, err)
				return nil
			}
			if _, err := peer.ContractExpire(ctx, PeerContractExpireRequest{TxID: txID, ContractID: c.ID}); err != nil {
				// Best-effort: peer notify failure is not fatal. Cron
				// will retry the peer.
				log.Printf("WARN: cross-bank expire saga=%s: peer notify: %v", txID, err)
			}
			return nil
		},
		// No Backward — peer notify is best-effort and idempotent on the
		// peer side; nothing local to undo.
	})

	// ---- Step 2: mark_expired (local: release reservation + flip status). PIVOT. ----
	saga.Add(sharedsaga.Step{
		Name:  sharedsaga.MustStep(sharedsaga.StepMarkExpired),
		Pivot: true,
		Forward: func(ctx context.Context, st *sharedsaga.State) error {
			stashExpirePayload(st, sharedsaga.StepMarkExpired, contractID)
			if s.ownBank == sellerCode && s.holdingRes != nil {
				if _, err := s.holdingRes.ReleaseForOTCContract(ctx, c.ID); err != nil {
					return err
				}
			}
			now := time.Now().UTC()
			c.Status = model.OptionContractStatusExpired
			c.ExpiredAt = &now
			return s.contracts.Save(c)
		},
	})

	return saga.Execute(ctx, sharedsaga.NewState())
}

// buildPublisher returns the LifecyclePublisher for this run. Production
// callers get the Kafka-emitting adapter; tests inject a counting fake via
// withPublisherFactory.
func (s *CrossbankExpireSaga) buildPublisher(c *model.OptionContract, txID, peerCode string) sharedsaga.LifecyclePublisher {
	if s.publisherFactory != nil {
		return s.publisherFactory(c, txID, peerCode)
	}
	return &crossbankExpirePublisher{
		producer:   s.producer,
		ob:         s.outbox,
		obDB:       s.outboxDB,
		sagaKind:   model.SagaKindExpire,
		txID:       txID,
		initiator:  s.ownBank,
		responder:  peerCode,
		offerID:    c.OfferID,
		contractID: c.ID,
	}
}

// crossbankExpirePublisher adapts sharedsaga.LifecyclePublisher to the
// existing Kafka topic vocabulary previously emitted by the legacy
// CrossbankSagaExecutor's Publish* methods. One adapter per ExpireContract.
type crossbankExpirePublisher struct {
	producer   *kafkaprod.Producer
	ob         *outbox.Outbox
	obDB       *gorm.DB
	sagaKind   string
	txID       string
	initiator  string
	responder  string
	offerID    uint64
	contractID uint64
}

func (p *crossbankExpirePublisher) OnStarted(ctx context.Context, _ string) {
	if p.producer == nil && p.ob == nil {
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
		publishSagaEvent(ctx, p.ob, p.obDB, p.producer, kafkamsg.TopicOTCCrossbankSagaStarted, data, p.txID)
	}
}

func (p *crossbankExpirePublisher) OnCommitted(ctx context.Context, _ string) {
	if p.producer == nil && p.ob == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaCommittedMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, ContractID: p.contractID, CommittedAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		publishSagaEvent(ctx, p.ob, p.obDB, p.producer, kafkamsg.TopicOTCCrossbankSagaCommitted, data, p.txID)
	}
}

func (p *crossbankExpirePublisher) OnRolledBack(ctx context.Context, _ string, failingStep, reason string, compensated []string) {
	if p.producer == nil && p.ob == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	payload := kafkamsg.CrossBankSagaRolledBackMessage{
		MessageID: uuid.NewString(), OccurredAt: now,
		TxID: p.txID, SagaKind: p.sagaKind, FailingPhase: failingStep,
		Reason: reason, CompensatedPhases: compensated, RolledBackAt: now,
	}
	if data, err := json.Marshal(payload); err == nil {
		publishSagaEvent(ctx, p.ob, p.obDB, p.producer, kafkamsg.TopicOTCCrossbankSagaRolledBack, data, p.txID)
	}
}

func (p *crossbankExpirePublisher) OnStuck(ctx context.Context, _ string, failingStep, reason string) {
	if p.producer == nil && p.ob == nil {
		return
	}
	payload := kafkamsg.CrossBankSagaStuckRollbackMessage{
		MessageID: uuid.NewString(), OccurredAt: time.Now().UTC().Format(time.RFC3339),
		TxID: p.txID, ContractID: p.contractID,
		Reason: "stuck after " + failingStep + ": " + reason,
	}
	if data, err := json.Marshal(payload); err == nil {
		publishSagaEvent(ctx, p.ob, p.obDB, p.producer, kafkamsg.TopicOTCCrossbankSagaStuckRollback, data, p.txID)
	}
}

// stashExpirePayload mirrors stashStepPayload / stashExercisePayload but
// for the expire saga input (just the contract id). Best-effort serialize.
var stashExpireMu sync.Mutex

func stashExpirePayload(st *sharedsaga.State, step sharedsaga.StepKind, contractID uint64) {
	if st == nil {
		return
	}
	type expirePayload struct {
		ContractID uint64 `json:"contract_id"`
	}
	data, err := json.Marshal(expirePayload{ContractID: contractID})
	if err != nil {
		return
	}
	stashExpireMu.Lock()
	st.Set("crossbank:"+string(step)+":payload", data)
	stashExpireMu.Unlock()
}
