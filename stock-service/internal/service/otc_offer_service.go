package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCHoldingLookup is the minimal Holding read the offer service needs to
// run the seller-invariant check. Implemented by *repository.HoldingRepository.
type OTCHoldingLookup interface {
	GetByOwnerAndSecurity(ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64) (*model.Holding, error)
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
	ReserveFunds(ctx context.Context, accountID, sagaOrderID uint64, amount decimal.Decimal, currency string) (*accountpb.ReserveFundsResponse, error)
	ReleaseReservation(ctx context.Context, sagaOrderID uint64) (*accountpb.ReleaseReservationResponse, error)
	PartialSettleReservation(ctx context.Context, sagaOrderID, settleSeq uint64, amount decimal.Decimal, memo string) (*accountpb.PartialSettleReservationResponse, error)
}

// OTCHoldingMutator is the surface needed to credit a buyer's holding on
// exercise. Implemented by *repository.HoldingRepository.
type OTCHoldingMutator interface {
	Upsert(h *model.Holding) error
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
		actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
		if err := s.assertSellerHasShares(actorOwnerType, actorOwnerID, in.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	initOwnerType, initOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
	var cpOwnerType *model.OwnerType
	var cpOwnerID *uint64
	if in.CounterpartyUserID != nil {
		t, id := model.OwnerFromLegacy(uint64(*in.CounterpartyUserID), *in.CounterpartySystemType)
		cpOwnerType = &t
		cpOwnerID = id
	}

	o := &model.OTCOffer{
		InitiatorOwnerType:          initOwnerType,
		InitiatorOwnerID:            initOwnerID,
		CounterpartyOwnerType:       cpOwnerType,
		CounterpartyOwnerID:         cpOwnerID,
		Direction:                   in.Direction,
		StockID:                     in.StockID,
		Quantity:                    in.Quantity,
		StrikePrice:                 in.StrikePrice,
		Premium:                     in.Premium,
		SettlementDate:              in.SettlementDate,
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: in.ActorSystemType,
		LastModifiedByPrincipalID:   uint64(in.ActorUserID),
	}
	if err := s.offers.Create(o); err != nil {
		return nil, err
	}
	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID:                 o.ID,
		RevisionNumber:          1,
		Quantity:                o.Quantity,
		StrikePrice:             o.StrikePrice,
		Premium:                 o.Premium,
		SettlementDate:          o.SettlementDate,
		ModifiedByPrincipalType: o.LastModifiedByPrincipalType,
		ModifiedByPrincipalID:   o.LastModifiedByPrincipalID,
		Action:                  model.OTCActionCreate,
	}); err != nil {
		return nil, err
	}

	if s.producer != nil {
		payload := kafkamsg.OTCOfferCreatedMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID:    o.ID,
			Initiator: kafkamsg.OTCParty{
				OwnerType: string(o.InitiatorOwnerType),
				OwnerID:   o.InitiatorOwnerID,
			},
			Counterparty:   ptrCounterparty(o),
			StockID:        o.StockID,
			Quantity:       o.Quantity.String(),
			StrikePrice:    o.StrikePrice.String(),
			Premium:        o.Premium.String(),
			SettlementDate: o.SettlementDate.Format("2006-01-02"),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferCreated, data)
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
	if o.LastModifiedByPrincipalType == in.ActorSystemType && o.LastModifiedByPrincipalID == uint64(in.ActorUserID) {
		return nil, errors.New("you cannot counter your own most recent terms")
	}
	if !in.Quantity.Equal(o.Quantity) {
		// Identify the seller's owner pair from the offer to validate share
		// availability. Seller is the initiator on sell_initiated offers,
		// otherwise the (required) counterparty.
		var sellerOwnerType model.OwnerType
		var sellerOwnerID *uint64
		if o.Direction == model.OTCDirectionSellInitiated {
			sellerOwnerType, sellerOwnerID = o.InitiatorOwnerType, o.InitiatorOwnerID
		} else if o.CounterpartyOwnerType != nil {
			sellerOwnerType, sellerOwnerID = *o.CounterpartyOwnerType, o.CounterpartyOwnerID
		} else {
			return nil, errors.New("cannot determine seller for invariant check")
		}
		if err := s.assertSellerHasShares(sellerOwnerType, sellerOwnerID, o.StockID, in.Quantity); err != nil {
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
	o.LastModifiedByPrincipalType = in.ActorSystemType
	o.LastModifiedByPrincipalID = uint64(in.ActorUserID)
	if err := s.offers.Save(o); err != nil {
		return nil, err
	}

	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByPrincipalType: in.ActorSystemType,
		ModifiedByPrincipalID:   uint64(in.ActorUserID),
		Action:                  model.OTCActionCounter,
	}); err != nil {
		return nil, err
	}

	if s.producer != nil {
		actorOwnerType, actorOwnerID := actorToOwnerParty(in.ActorUserID, in.ActorSystemType)
		payload := kafkamsg.OTCOfferCounteredMessage{
			MessageID:      uuid.NewString(),
			OccurredAt:     time.Now().UTC().Format(time.RFC3339),
			OfferID:        o.ID,
			RevisionNumber: revNum,
			ModifiedBy:     kafkamsg.OTCParty{OwnerType: actorOwnerType, OwnerID: actorOwnerID},
			OtherParty:     otcOtherParty(o, in.ActorUserID, in.ActorSystemType),
			Quantity:       o.Quantity.String(),
			StrikePrice:    o.StrikePrice.String(),
			Premium:        o.Premium.String(),
			SettlementDate: o.SettlementDate.Format("2006-01-02"),
			UpdatedAt:      o.UpdatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferCountered, data)
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
	o.LastModifiedByPrincipalType = in.ActorSystemType
	o.LastModifiedByPrincipalID = uint64(in.ActorUserID)
	if err := s.offers.Save(o); err != nil {
		return nil, err
	}
	_ = s.revisions.Append(&model.OTCOfferRevision{
		OfferID: o.ID, RevisionNumber: revNum,
		Quantity: o.Quantity, StrikePrice: o.StrikePrice, Premium: o.Premium, SettlementDate: o.SettlementDate,
		ModifiedByPrincipalType: in.ActorSystemType,
		ModifiedByPrincipalID:   uint64(in.ActorUserID),
		Action:                  model.OTCActionReject,
	})
	if s.producer != nil {
		actorOwnerType, actorOwnerID := actorToOwnerParty(in.ActorUserID, in.ActorSystemType)
		payload := kafkamsg.OTCOfferRejectedMessage{
			MessageID:  uuid.NewString(),
			OccurredAt: time.Now().UTC().Format(time.RFC3339),
			OfferID:    o.ID,
			RejectedBy: kafkamsg.OTCParty{OwnerType: actorOwnerType, OwnerID: actorOwnerID},
			OtherParty: otcOtherParty(o, in.ActorUserID, in.ActorSystemType),
			UpdatedAt:  o.UpdatedAt.Format(time.RFC3339),
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.producer.PublishRaw(ctx, kafkamsg.TopicOTCOfferRejected, data)
		}
	}
	return o, nil
}

// ListMyOffers returns offers where the user is initiator/counterparty/either.
func (s *OTCOfferService) ListMyOffers(userID int64, systemType, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	ownerType, ownerID := model.OwnerFromLegacy(uint64(userID), systemType)
	return s.offers.ListByOwner(ownerType, ownerID, role, statuses, stockID, page, pageSize)
}

// LastReadReceipt returns the read-receipt for (userID, systemType, offerID),
// or nil if the user has never opened the offer. Used by the gateway to
// compute the `unread` flag on list responses (Celina-4 §Aktivne ponude).
func (s *OTCOfferService) LastReadReceipt(userID int64, systemType string, offerID uint64) (*model.OTCOfferReadReceipt, error) {
	if s.receipts == nil {
		return nil, nil
	}
	ownerType, ownerID := model.OwnerFromLegacy(uint64(userID), systemType)
	return s.receipts.GetReceipt(ownerType, model.OwnerIDOrZero(ownerID), offerID)
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
		actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(actorUserID), actorSystemType)
		_ = s.receipts.Upsert(actorOwnerType, model.OwnerIDOrZero(actorOwnerID), o.ID, o.UpdatedAt)
	}
	return o, revs, nil
}

func (s *OTCOfferService) isParticipant(o *model.OTCOffer, userID int64, systemType string) bool {
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(userID), systemType)
	if o.InitiatorOwnerType == actorOwnerType && ownerIDEqual(o.InitiatorOwnerID, actorOwnerID) {
		return true
	}
	if o.CounterpartyOwnerType != nil && *o.CounterpartyOwnerType == actorOwnerType &&
		ownerIDEqual(o.CounterpartyOwnerID, actorOwnerID) {
		return true
	}
	return false
}

func (s *OTCOfferService) assertSellerHasShares(ownerType model.OwnerType, ownerID *uint64, stockID uint64, requested decimal.Decimal) error {
	if s.holdings == nil {
		return errors.New("holding lookup not configured")
	}
	holding, err := s.holdings.GetByOwnerAndSecurity(ownerType, ownerID, "stock", stockID)
	if err != nil {
		return fmt.Errorf("seller has no holding for stock %d: %w", stockID, err)
	}
	heldQty := decimal.NewFromInt(holding.Quantity)
	committed, err := s.offers.SumActiveQuantityForSeller(ownerType, ownerID, stockID)
	if err != nil {
		return err
	}
	available := heldQty.Sub(committed)
	if requested.GreaterThan(available) {
		return fmt.Errorf("insufficient available shares for this seller (held %s, committed %s, requested %s)", heldQty, committed, requested)
	}
	return nil
}

// ptrCounterparty maps the offer's counterparty owner pair to the OTCParty
// Kafka shape, returning nil when there is no counterparty yet.
func ptrCounterparty(o *model.OTCOffer) *kafkamsg.OTCParty {
	if o.CounterpartyOwnerType == nil {
		return nil
	}
	return &kafkamsg.OTCParty{
		OwnerType: string(*o.CounterpartyOwnerType),
		OwnerID:   o.CounterpartyOwnerID,
	}
}

// otcOtherParty returns the OTCParty representation of the participant on
// the offer who is NOT the supplied actor. Used to populate Kafka counterparty
// fields after a counter / reject event.
func otcOtherParty(o *model.OTCOffer, actorID int64, actorType string) kafkamsg.OTCParty {
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(actorID), actorType)
	if o.InitiatorOwnerType == actorOwnerType && ownerIDEqual(o.InitiatorOwnerID, actorOwnerID) {
		if o.CounterpartyOwnerType != nil {
			return kafkamsg.OTCParty{
				OwnerType: string(*o.CounterpartyOwnerType),
				OwnerID:   o.CounterpartyOwnerID,
			}
		}
		return kafkamsg.OTCParty{}
	}
	return kafkamsg.OTCParty{
		OwnerType: string(o.InitiatorOwnerType),
		OwnerID:   o.InitiatorOwnerID,
	}
}

// actorToOwnerParty maps an OTC actor (the JWT principal who issued a
// counter/reject) onto the (OwnerType, OwnerID) pair that the Kafka payload
// describes. Employee principals are recorded as OwnerBank with a nil OwnerID;
// client principals carry their own user id. Bank actors (already encoded as
// systemType=="bank") map straight through.
func actorToOwnerParty(actorID int64, actorSystemType string) (string, *uint64) {
	switch actorSystemType {
	case "employee", string(model.OwnerBank):
		return string(model.OwnerBank), nil
	default:
		uid := uint64(actorID)
		return string(model.OwnerClient), &uid
	}
}
