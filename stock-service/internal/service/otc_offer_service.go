package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCHoldingLookup is the minimal Holding read the offer service needs to
// run the seller-invariant check. Implemented by *repository.HoldingRepository.
type OTCHoldingLookup interface {
	GetByUserAndSecurity(userID uint64, systemType, securityType string, securityID uint64) (*model.Holding, error)
}

// OTCOfferService owns negotiation flows: create, counter, reject, list, get.
// Money flow (premium payment, exercise) lives in separate sagas. The
// service-layer seller-invariant check (§4.6 of spec) ensures a seller
// cannot promise more shares than they hold across active offers + contracts.
type OTCOfferService struct {
	offers    *repository.OTCOfferRepository
	revisions *repository.OTCOfferRevisionRepository
	contracts *repository.OptionContractRepository
	holdings  OTCHoldingLookup
	holdingRepo OTCHoldingMutator
	receipts  *repository.OTCReadReceiptRepository
	producer  *kafkaprod.Producer

	// saga deps (optional; wired via WithSaga). Required by Accept and
	// ExerciseContract.
	sagaRepo   SagaLogRepo
	accounts   OTCAccountClient
	exchange   FundExchangeClient
	holdingRes *HoldingReservationService

	// cross-bank dispatch (Spec 4 / Celina 5; wired via WithCrossbank).
	// When non-nil and the offer/contract is cross-bank from this bank's
	// perspective, Accept / ExerciseContract delegate to these dispatchers
	// instead of the intra-bank saga.
	ownBankCode       string
	crossbankAccept   func(ctx context.Context, in AcceptInput) (*model.OptionContract, error)
	crossbankExercise func(ctx context.Context, in ExerciseInput) (*model.OptionContract, error)

	// Outbox: when wired (via WithOutbox), post-saga Kafka publishes
	// (otc.contract-created, otc.contract-exercised) go through the
	// transactional outbox instead of best-effort producer.PublishRaw.
	// The drainer goroutine asynchronously publishes pending rows so a
	// crash between business commit and Kafka send no longer drops events.
	// When nil, the legacy direct-publish path is used so unit tests that
	// don't wire a DB still work.
	outbox    *outbox.Outbox
	outboxDB  *gorm.DB
}

// WithOutbox wires the transactional outbox + the GORM handle the saga
// uses to enqueue rows. Callers that don't wire this fall back to the
// legacy direct-publish path (best-effort, may drop on crash).
func (s *OTCOfferService) WithOutbox(ob *outbox.Outbox, db *gorm.DB) *OTCOfferService {
	cp := *s
	cp.outbox = ob
	cp.outboxDB = db
	return &cp
}

// publishViaOutboxOrDirect is the post-saga publish primitive used by
// Accept and ExerciseContract. When the outbox is wired, the payload is
// enqueued (durable). Otherwise the legacy producer.PublishRaw is used
// (best-effort). sagaID is stamped on the outbox row so cross-service
// audit can correlate Kafka events to the originating saga.
func (s *OTCOfferService) publishViaOutboxOrDirect(ctx context.Context, topic string, payload []byte, sagaID string) {
	if s.outbox != nil && s.outboxDB != nil {
		_ = s.outbox.Enqueue(s.outboxDB, topic, payload, sagaID)
		return
	}
	if s.producer != nil {
		_ = s.producer.PublishRaw(ctx, topic, payload)
	}
}

// WithCrossbank wires the cross-bank dispatch hooks. The supplied
// `acceptDispatch` is called by Accept when the offer's bank-code columns
// indicate a cross-bank trade; `exerciseDispatch` is called by
// ExerciseContract when the contract's bank codes differ. ownBank is the
// current bank's 3-digit code (e.g. "111").
func (s *OTCOfferService) WithCrossbank(
	ownBank string,
	acceptDispatch func(ctx context.Context, in AcceptInput) (*model.OptionContract, error),
	exerciseDispatch func(ctx context.Context, in ExerciseInput) (*model.OptionContract, error),
) *OTCOfferService {
	cp := *s
	cp.ownBankCode = ownBank
	cp.crossbankAccept = acceptDispatch
	cp.crossbankExercise = exerciseDispatch
	return &cp
}

// OTCAccountClient is the account-service surface the accept and exercise
// sagas use. Superset of FundAccountClient (adds reservation lifecycle).
type OTCAccountClient interface {
	FundAccountClient
	ReserveFunds(ctx context.Context, accountID, sagaOrderID uint64, amount decimal.Decimal, currency, idempotencyKey string) (*accountpb.ReserveFundsResponse, error)
	ReleaseReservation(ctx context.Context, sagaOrderID uint64, idempotencyKey string) (*accountpb.ReleaseReservationResponse, error)
	PartialSettleReservation(ctx context.Context, sagaOrderID, settleSeq uint64, amount decimal.Decimal, memo, idempotencyKey string) (*accountpb.PartialSettleReservationResponse, error)
}

// OTCHoldingMutator is the surface needed to credit a buyer's holding on
// exercise. Implemented by *repository.HoldingRepository. Ctx carries
// saga_id / saga_step (set by the OTC exercise saga) so the new row gets
// stamped for cross-service audit.
type OTCHoldingMutator interface {
	Upsert(ctx context.Context, h *model.Holding) error
}

// WithSaga wires the dependencies needed by Accept / ExerciseContract.
// Without it, those methods reject with errOTCSagaDepsNotWired. Pass nil
// for `exchange` to disable cross-currency support; same-currency flows
// still work.
func (s *OTCOfferService) WithSaga(
	sagaRepo SagaLogRepo,
	accounts OTCAccountClient,
	exchange FundExchangeClient,
	holdingRes *HoldingReservationService,
	holdingRepo OTCHoldingMutator,
) *OTCOfferService {
	cp := *s
	cp.sagaRepo = sagaRepo
	cp.accounts = accounts
	cp.exchange = exchange
	cp.holdingRes = holdingRes
	cp.holdingRepo = holdingRepo
	return &cp
}

var errOTCSagaDepsNotWired = errors.New("OTC saga dependencies not wired")

func NewOTCOfferService(
	offers *repository.OTCOfferRepository,
	revisions *repository.OTCOfferRevisionRepository,
	contracts *repository.OptionContractRepository,
	holdings OTCHoldingLookup,
	receipts *repository.OTCReadReceiptRepository,
	producer *kafkaprod.Producer,
) *OTCOfferService {
	return &OTCOfferService{
		offers: offers, revisions: revisions, contracts: contracts,
		holdings: holdings, receipts: receipts, producer: producer,
	}
}

// CreateOfferInput captures the fields a new offer needs.
type CreateOfferInput struct {
	ActorUserID            int64
	ActorSystemType        string
	Direction              string
	StockID                uint64
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	Premium                decimal.Decimal
	SettlementDate         time.Time
	CounterpartyUserID     *int64
	CounterpartySystemType *string
}

func (s *OTCOfferService) Create(ctx context.Context, in CreateOfferInput) (*model.OTCOffer, error) {
	if !in.Quantity.IsPositive() || !in.StrikePrice.IsPositive() {
		return nil, errors.New("quantity and strike_price must be positive")
	}
	if in.Premium.IsNegative() {
		return nil, errors.New("premium must be non-negative")
	}
	if !in.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("settlement_date must be in the future")
	}
	switch in.Direction {
	case model.OTCDirectionSellInitiated, model.OTCDirectionBuyInitiated:
	default:
		return nil, errors.New("unknown direction")
	}
	if in.Direction == model.OTCDirectionBuyInitiated && in.CounterpartyUserID == nil {
		return nil, errors.New("buy_initiated offers require a named counterparty")
	}
	if (in.CounterpartyUserID == nil) != (in.CounterpartySystemType == nil) {
		return nil, errors.New("counterparty user_id and system_type must both be set or both omitted")
	}

	if in.Direction == model.OTCDirectionSellInitiated {
		if err := s.assertSellerHasShares(in.ActorUserID, in.ActorSystemType, in.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	o := &model.OTCOffer{
		InitiatorUserID:          in.ActorUserID,
		InitiatorSystemType:      in.ActorSystemType,
		CounterpartyUserID:       in.CounterpartyUserID,
		CounterpartySystemType:   in.CounterpartySystemType,
		Direction:                in.Direction,
		StockID:                  in.StockID,
		Quantity:                 in.Quantity,
		StrikePrice:              in.StrikePrice,
		Premium:                  in.Premium,
		SettlementDate:           in.SettlementDate,
		Status:                   model.OTCOfferStatusPending,
		LastModifiedByUserID:     in.ActorUserID,
		LastModifiedBySystemType: in.ActorSystemType,
	}
	if err := s.offers.Create(o); err != nil {
		return nil, err
	}
	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID:              o.ID,
		RevisionNumber:       1,
		Quantity:             o.Quantity,
		StrikePrice:          o.StrikePrice,
		Premium:              o.Premium,
		SettlementDate:       o.SettlementDate,
		ModifiedByUserID:     o.LastModifiedByUserID,
		ModifiedBySystemType: o.LastModifiedBySystemType,
		Action:               model.OTCActionCreate,
	}); err != nil {
		return nil, err
	}

	if s.producer != nil {
		payload := kafkamsg.OTCOfferCreatedMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			OfferID:        o.ID,
			Initiator:      kafkamsg.OTCParty{UserID: o.InitiatorUserID, SystemType: o.InitiatorSystemType},
			Counterparty:   ptrCounterparty(o),
			StockID:        o.StockID,
			Quantity:       o.Quantity.String(),
			StrikePrice:    o.StrikePrice.String(),
			Premium:        o.Premium.String(),
			SettlementDate: o.SettlementDate.Format("2006-01-02"),
		}
		if data, err := json.Marshal(payload); err == nil {
			s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferCreated, data, "")
		}
	}
	return o, nil
}

// CounterInput captures fields a counter call needs.
type CounterInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
	Quantity        decimal.Decimal
	StrikePrice     decimal.Decimal
	Premium         decimal.Decimal
	SettlementDate  time.Time
}

func (s *OTCOfferService) Counter(ctx context.Context, in CounterInput) (*model.OTCOffer, error) {
	o, err := s.offers.GetByID(in.OfferID)
	if err != nil {
		return nil, err
	}
	if o.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}
	if o.LastModifiedByUserID == in.ActorUserID && o.LastModifiedBySystemType == in.ActorSystemType {
		return nil, errors.New("you cannot counter your own most recent terms")
	}
	if !in.Quantity.Equal(o.Quantity) {
		var sellerID int64
		var sellerType string
		if o.Direction == model.OTCDirectionSellInitiated {
			sellerID, sellerType = o.InitiatorUserID, o.InitiatorSystemType
		} else if o.CounterpartyUserID != nil {
			sellerID, sellerType = *o.CounterpartyUserID, *o.CounterpartySystemType
		} else {
			return nil, errors.New("cannot determine seller for invariant check")
		}
		if err := s.assertSellerHasShares(sellerID, sellerType, o.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	revNum, err := s.revisions.NextRevisionNumber(o.ID)
	if err != nil {
		return nil, err
	}

	o.Quantity = in.Quantity
	o.StrikePrice = in.StrikePrice
	o.Premium = in.Premium
	o.SettlementDate = in.SettlementDate
	o.Status = model.OTCOfferStatusCountered
	o.LastModifiedByUserID = in.ActorUserID
	o.LastModifiedBySystemType = in.ActorSystemType
	if err := s.offers.Save(o); err != nil {
		return nil, err
	}

	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType,
		Action: model.OTCActionCounter,
	}); err != nil {
		return nil, err
	}

	if s.producer != nil {
		payload := kafkamsg.OTCOfferCounteredMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			OfferID:        o.ID,
			RevisionNumber: revNum,
			ModifiedBy:     kafkamsg.OTCParty{UserID: in.ActorUserID, SystemType: in.ActorSystemType},
			OtherParty:     otcOtherParty(o, in.ActorUserID, in.ActorSystemType),
			Quantity:       o.Quantity.String(),
			StrikePrice:    o.StrikePrice.String(),
			Premium:        o.Premium.String(),
			SettlementDate: o.SettlementDate.Format("2006-01-02"),
			UpdatedAt:      o.UpdatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferCountered, data, "")
		}
	}
	return o, nil
}

// RejectInput captures fields a reject call needs.
type RejectInput struct {
	OfferID         uint64
	ActorUserID     int64
	ActorSystemType string
}

func (s *OTCOfferService) Reject(ctx context.Context, in RejectInput) (*model.OTCOffer, error) {
	o, err := s.offers.GetByID(in.OfferID)
	if err != nil {
		return nil, err
	}
	if o.IsTerminal() {
		return nil, errors.New("offer is in a terminal state")
	}
	revNum, err := s.revisions.NextRevisionNumber(o.ID)
	if err != nil {
		return nil, err
	}
	o.Status = model.OTCOfferStatusRejected
	o.LastModifiedByUserID = in.ActorUserID
	o.LastModifiedBySystemType = in.ActorSystemType
	if err := s.offers.Save(o); err != nil {
		return nil, err
	}
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByUserID: in.ActorUserID, ModifiedBySystemType: in.ActorSystemType, Action: model.OTCActionReject,
	})
	if s.producer != nil {
		payload := kafkamsg.OTCOfferRejectedMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID:    o.ID,
			RejectedBy: kafkamsg.OTCParty{UserID: in.ActorUserID, SystemType: in.ActorSystemType},
			OtherParty: otcOtherParty(o, in.ActorUserID, in.ActorSystemType),
			UpdatedAt:  o.UpdatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferRejected, data, "")
		}
	}
	return o, nil
}

// ListMyOffers returns offers where the user is initiator/counterparty/either.
func (s *OTCOfferService) ListMyOffers(userID int64, systemType, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	return s.offers.ListByOwner(userID, systemType, role, statuses, stockID, page, pageSize)
}

// LastReadReceipt returns the read-receipt for (userID, systemType, offerID),
// or nil if the user has never opened the offer. Used by the gateway to
// compute the `unread` flag on list responses (Celina-4 §Aktivne ponude).
func (s *OTCOfferService) LastReadReceipt(userID int64, systemType string, offerID uint64) (*model.OTCOfferReadReceipt, error) {
	if s.receipts == nil {
		return nil, nil
	}
	return s.receipts.GetReceipt(userID, systemType, offerID)
}

// GetOffer returns the offer + its revisions, scoped to participants only.
func (s *OTCOfferService) GetOffer(offerID uint64, actorUserID int64, actorSystemType string) (*model.OTCOffer, []model.OTCOfferRevision, error) {
	o, err := s.offers.GetByID(offerID)
	if err != nil {
		return nil, nil, err
	}
	if !s.isParticipant(o, actorUserID, actorSystemType) {
		return nil, nil, errors.New("not a participant in this offer")
	}
	revs, err := s.revisions.ListByOffer(o.ID)
	if err != nil {
		return nil, nil, err
	}
	// Mark read.
	if s.receipts != nil {
		_ = s.receipts.Upsert(actorUserID, actorSystemType, o.ID, o.UpdatedAt)
	}
	return o, revs, nil
}

func (s *OTCOfferService) isParticipant(o *model.OTCOffer, userID int64, systemType string) bool {
	if o.InitiatorUserID == userID && o.InitiatorSystemType == systemType {
		return true
	}
	if o.CounterpartyUserID != nil && *o.CounterpartyUserID == userID &&
		o.CounterpartySystemType != nil && *o.CounterpartySystemType == systemType {
		return true
	}
	return false
}

func (s *OTCOfferService) assertSellerHasShares(userID int64, systemType string, stockID uint64, requested decimal.Decimal) error {
	if s.holdings == nil {
		return errors.New("holding lookup not configured")
	}
	holding, err := s.holdings.GetByUserAndSecurity(uint64(userID), systemType, "stock", stockID)
	if err != nil {
		return fmt.Errorf("seller has no holding for stock %d: %w", stockID, err)
	}
	heldQty := decimal.NewFromInt(holding.Quantity)
	committed, err := s.offers.SumActiveQuantityForSeller(userID, systemType, stockID)
	if err != nil {
		return err
	}
	available := heldQty.Sub(committed)
	if requested.GreaterThan(available) {
		return fmt.Errorf("insufficient available shares for this seller (held %s, committed %s, requested %s)", heldQty, committed, requested)
	}
	return nil
}

func ptrCounterparty(o *model.OTCOffer) *kafkamsg.OTCParty {
	if o.CounterpartyUserID == nil {
		return nil
	}
	return &kafkamsg.OTCParty{UserID: *o.CounterpartyUserID, SystemType: *o.CounterpartySystemType}
}

func otcOtherParty(o *model.OTCOffer, actorID int64, actorType string) kafkamsg.OTCParty {
	if o.InitiatorUserID == actorID && o.InitiatorSystemType == actorType {
		if o.CounterpartyUserID != nil {
			return kafkamsg.OTCParty{UserID: *o.CounterpartyUserID, SystemType: *o.CounterpartySystemType}
		}
		return kafkamsg.OTCParty{}
	}
	return kafkamsg.OTCParty{UserID: o.InitiatorUserID, SystemType: o.InitiatorSystemType}
}
