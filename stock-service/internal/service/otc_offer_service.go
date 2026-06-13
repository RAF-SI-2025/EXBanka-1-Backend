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
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/shared/outbox"
	"github.com/exbanka/contract/shared/svcerr"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCHoldingLookup is the minimal Holding read the offer service needs to
// run the seller-invariant check. Implemented by *repository.HoldingRepository.
type OTCHoldingLookup interface {
	GetByOwnerAndSecurity(ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64) (*model.Holding, error)
}

// otcNotifier is the narrow surface used to emit in-app notifications for OTC
// offer events. Satisfied by *kafkaprod.Producer (Task 1 gave it the method).
// Declared as an interface so tests can inject a recording stub — the concrete
// *kafkaprod.Producer is otherwise impossible to observe.
type otcNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg kafkamsg.GeneralNotificationMessage) error
}

// OTCOfferService owns negotiation flows: create, counter, reject, list, get.
// Money flow (premium payment, exercise) lives in separate sagas. The
// service-layer seller-invariant check (§4.6 of spec) ensures a seller
// cannot promise more shares than they hold across active offers + contracts.
type OTCOfferService struct {
	offers      *repository.OTCOfferRepository
	revisions   *repository.OTCOfferRevisionRepository
	contracts   *repository.OptionContractRepository
	holdings    OTCHoldingLookup
	holdingRepo OTCHoldingMutator
	receipts    *repository.OTCReadReceiptRepository
	producer    *kafkaprod.Producer

	// notifier emits in-app (push) notifications for OTC offer events. Set to
	// the same *kafkaprod.Producer as `producer` by NewOTCOfferService; tests
	// inject a recording stub.
	notifier otcNotifier

	// saga deps (optional; wired via WithSaga). Required by Accept and
	// ExerciseContract.
	sagaRepo   SagaLogRepo
	accounts   OTCAccountClient
	exchange   FundExchangeClient
	holdingRes *HoldingReservationService

	// stockMeta resolves (Name, ListingID) for a stock_id at exercise
	// time so the buyer-credit holding upsert carries the metadata the
	// FE needs (otherwise the new row appears with blank ticker/name/
	// listing_id and downstream "sell" / "make public" flows fail).
	// Optional — when nil, the upsert proceeds with c.Ticker only and
	// Name/ListingID stay empty (legacy behaviour). cmd/main.go wires
	// it via WithStockMeta.
	stockMeta OTCStockMetaResolver

	// Outbox: when wired (via WithOutbox), post-saga Kafka publishes
	// (otc.contract-created, otc.contract-exercised) go through the
	// transactional outbox instead of best-effort producer.PublishRaw.
	// The drainer goroutine asynchronously publishes pending rows so a
	// crash between business commit and Kafka send no longer drops events.
	// When nil, the legacy direct-publish path is used so unit tests that
	// don't wire a DB still work.
	outbox   *outbox.Outbox
	outboxDB *gorm.DB

	// capitalGainRepo records the seller's realised P/L when an option
	// contract is exercised — mirroring the CapitalGain row that
	// PortfolioService.recordCapitalGain writes on a normal sell fill and
	// OTCService.BuyOffer writes on a direct OTC stock sale. Optional —
	// when nil the saga still runs (shares + money move) and a WARN is
	// logged. Wired via WithCapitalGain.
	capitalGainRepo CapitalGainRepo

	// fundHoldingRepo is optional (E2, Plan E). When wired and the contract
	// has OnBehalfOfFundID set, the exercise saga credits fund_holdings
	// instead of the buyer's personal holdings. Without it, fund-owned
	// contracts exercise into the bank's standard holdings (fall-back).
	fundHoldingRepo FundHoldingUpsert
}

// FundHoldingUpsert is the narrow surface the exercise saga needs to credit
// a fund holding. Implemented by *repository.FundHoldingRepository.
type FundHoldingUpsert interface {
	Upsert(h *model.FundHolding) error
	// DecrementForFundSecurity reverses an on-behalf-of-fund buyer credit
	// (exercise-saga backward step). No-op when the row is absent.
	DecrementForFundSecurity(fundID uint64, securityType string, securityID uint64, qty int64) error
	// UpsertIdempotent / DecrementForFundSecurityIdempotent are the
	// marker-guarded variants the exercise saga uses so a retry or
	// crash-recovery replay credits the fund's shares exactly once.
	UpsertIdempotent(h *model.FundHolding, idemKey string) error
	DecrementForFundSecurityIdempotent(fundID uint64, securityType string, securityID uint64, qty int64, idemKey string) error
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

// OTCAccountClient is the account-service surface the accept and exercise
// sagas use. Superset of FundAccountClient (adds reservation lifecycle).
type OTCAccountClient interface {
	FundAccountClient
	ReserveFunds(ctx context.Context, accountID, sagaOrderID uint64, amount decimal.Decimal, currency, idempotencyKey, orderKind string) (*accountpb.ReserveFundsResponse, error)
	ReleaseReservation(ctx context.Context, sagaOrderID uint64, idempotencyKey, orderKind string) (*accountpb.ReleaseReservationResponse, error)
	PartialSettleReservation(ctx context.Context, sagaOrderID, settleSeq uint64, amount decimal.Decimal, memo, idempotencyKey, orderKind string) (*accountpb.PartialSettleReservationResponse, error)
}

// OTCHoldingMutator is the surface needed to credit a buyer's holding on
// exercise. Implemented by *repository.HoldingRepository. Ctx carries
// saga_id / saga_step (set by the OTC exercise saga) so the new row gets
// stamped for cross-service audit.
type OTCHoldingMutator interface {
	Upsert(ctx context.Context, h *model.Holding) error
	// DecrementForOwner reverses an exercise buyer credit (exercise-saga
	// backward step), deleting the row at zero. No-op when the row is absent.
	DecrementForOwner(ctx context.Context, ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64, qty int64) error
	// UpsertIdempotent / DecrementForOwnerIdempotent are the marker-guarded
	// variants the exercise saga uses so a retry or crash-recovery replay
	// credits the buyer's shares exactly once.
	UpsertIdempotent(ctx context.Context, h *model.Holding, idemKey string) error
	DecrementForOwnerIdempotent(ctx context.Context, ownerType model.OwnerType, ownerID *uint64, securityType string, securityID uint64, qty int64, idemKey string) error
}

// OTCStockMetaResolver is the narrow lookup the exercise saga uses to
// resolve display metadata (Name) and the underlying ListingID for a
// stock_id. The exercise saga has the OptionContract's stock_id +
// ticker but not the corresponding Listing row id (different per bank)
// or the Stock display name — without these, the upserted buyer
// holding lacks the fields the FE needs to render and to construct
// downstream sell orders. (Fix for 2026-05-16: "user cant make public
// or sell stock acquired thru contract".)
type OTCStockMetaResolver interface {
	GetStockByID(id uint64) (*model.Stock, error)
	GetListingBySecurityIDAndType(securityID uint64, securityType string) (*model.Listing, error)
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

// WithCapitalGain wires the repository that records the seller's realised
// P/L on a successful exercise. Optional — without it the exercise saga
// still moves shares and money, but no CapitalGain row is written and the
// seller's portfolio reports zero gain on the sale (the pre-fix behaviour).
func (s *OTCOfferService) WithCapitalGain(repo CapitalGainRepo) *OTCOfferService {
	cp := *s
	cp.capitalGainRepo = repo
	return &cp
}

// WithFundHolding wires the fund-holding repository so exercise of fund-owned
// contracts routes to fund_holdings instead of personal holdings (E2).
func (s *OTCOfferService) WithFundHolding(repo FundHoldingUpsert) *OTCOfferService {
	cp := *s
	cp.fundHoldingRepo = repo
	return &cp
}

// WithStockMeta wires the lookup used by the exercise saga to fill the
// buyer-credit holding's display fields (Name, ListingID). Optional —
// without it, those fields are left empty (Ticker is still populated
// from the contract).
func (s *OTCOfferService) WithStockMeta(r OTCStockMetaResolver) *OTCOfferService {
	cp := *s
	cp.stockMeta = r
	return &cp
}

var errOTCSagaDepsNotWired = svcerr.New(codes.Internal, "OTC saga dependencies not wired")

func NewOTCOfferService(
	offers *repository.OTCOfferRepository,
	revisions *repository.OTCOfferRevisionRepository,
	contracts *repository.OptionContractRepository,
	holdings OTCHoldingLookup,
	receipts *repository.OTCReadReceiptRepository,
	producer *kafkaprod.Producer,
) *OTCOfferService {
	s := &OTCOfferService{
		offers: offers, revisions: revisions, contracts: contracts,
		holdings: holdings, receipts: receipts, producer: producer,
	}
	// Wire the notifier to the same producer. Guard against assigning a typed
	// nil into the interface (which would make s.notifier != nil but panic on
	// call) by only setting it when the producer is actually present.
	if producer != nil {
		s.notifier = producer
	}
	return s
}

// notifyOTCParty emits an in-app notification to one OTC party. No-op for bank
// parties (OwnerType != "client" or nil OwnerID) and best-effort. Delegates to
// the package-level notifyOTCPartyVia so the OTC expiry cron (a separate type)
// shares the same emit logic.
func (s *OTCOfferService) notifyOTCParty(ctx context.Context, party kafkamsg.OTCParty, notifType, refType string, refID uint64, data map[string]string) {
	notifyOTCPartyVia(ctx, s.notifier, party, notifType, refType, refID, data)
}

// CreateOfferInput captures the fields a new offer needs.
type CreateOfferInput struct {
	ActorUserID     int64
	ActorSystemType string
	// ActingEmployeeID is the employee principal who originated this action,
	// threaded from the gateway (identity.ActingEmployeeID). It is captured
	// onto the persisted OTCOffer ONLY when the resolved owner is the bank —
	// it is the stable SI-TX wire-identity source ("employee-<N>") for a bank
	// acting as a cross-bank OTC principal. nil for client-owned offers and
	// for bank offers created by a non-employee/system path.
	ActingEmployeeID       *uint64
	Direction              string
	StockID                uint64
	Ticker                 string
	Quantity               decimal.Decimal
	CounterpartyUserID     *int64
	CounterpartySystemType *string
	InitiatorAccountID     uint64
}

func (s *OTCOfferService) Create(ctx context.Context, in CreateOfferInput) (*model.OTCOffer, error) {
	if !in.Quantity.IsPositive() {
		return nil, fmt.Errorf("quantity must be positive: %w", ErrOTCOfferFieldInvalid)
	}
	switch in.Direction {
	case model.OTCDirectionSellInitiated, model.OTCDirectionBuyInitiated:
	default:
		return nil, fmt.Errorf("unknown direction: %w", ErrOTCOfferFieldInvalid)
	}
	// Phase 9 follow-up: the legacy single-chain model required a named
	// counterparty on buy_initiated offers. The new parallel-chains
	// marketplace lets anyone open a public buy_initiated LISTING for
	// other users to bid on (the bidder becomes the seller at accept
	// time via OTCNegotiationService). When a counterparty IS supplied
	// the offer is "directed" — only the named user sees it in their
	// list — but it's no longer required.
	if (in.CounterpartyUserID == nil) != (in.CounterpartySystemType == nil) {
		return nil, fmt.Errorf("counterparty user_id and system_type must both be set or both omitted: %w", ErrOTCOfferFieldInvalid)
	}

	if in.Direction == model.OTCDirectionSellInitiated {
		actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)
		if err := s.assertSellerHasShares(actorOwnerType, actorOwnerID, in.StockID, in.Quantity); err != nil {
			return nil, err
		}
	}

	initOwnerType, initOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserID), in.ActorSystemType)

	// One-open-offer-per-(owner,ticker,direction) invariant. Offers are
	// termless inventory; a duplicate open offer for the same ticker+direction
	// is rejected here (friendlier than relying on the DB partial unique index
	// ux_otc_offer_open_owner_ticker_dir, which is a backstop).
	existing, err := s.offers.CountOpenByOwnerTickerDirection(initOwnerType, initOwnerID, in.Ticker, in.Direction)
	if err != nil {
		return nil, fmt.Errorf("duplicate check: %w", err)
	}
	if existing > 0 {
		return nil, ErrOTCOfferDuplicateOpen
	}

	var cpOwnerType *model.OwnerType
	var cpOwnerID *uint64
	if in.CounterpartyUserID != nil {
		t, id := model.OwnerFromLegacy(uint64(*in.CounterpartyUserID), *in.CounterpartySystemType)
		cpOwnerType = &t
		cpOwnerID = id
	}

	// Capture the originating employee onto bank-owned offers only. This is the
	// stable SI-TX wire-identity source: the bank party publishes as
	// "employee-<ActingEmployeeID>" on every later wire action, regardless of
	// which employee performs it. nil for client-owned offers and for bank
	// offers created by a non-employee/system path (no acting employee).
	var actingEmployeeID *uint64
	if initOwnerType == model.OwnerBank && in.ActingEmployeeID != nil && *in.ActingEmployeeID > 0 {
		emp := *in.ActingEmployeeID
		actingEmployeeID = &emp
	}

	o := &model.OTCOffer{
		InitiatorOwnerType:          initOwnerType,
		InitiatorOwnerID:            initOwnerID,
		CounterpartyOwnerType:       cpOwnerType,
		CounterpartyOwnerID:         cpOwnerID,
		Direction:                   in.Direction,
		StockID:                     in.StockID,
		Ticker:                      in.Ticker,
		Quantity:                    in.Quantity,
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: in.ActorSystemType,
		LastModifiedByPrincipalID:   uint64(in.ActorUserID),
		InitiatorAccountID:          in.InitiatorAccountID,
		ActingEmployeeID:            actingEmployeeID,
	}
	if err := s.offers.Create(o); err != nil {
		// The partial unique index ux_otc_offer_open_owner_ticker_dir is the
		// authoritative backstop for the one-open-offer-per-(owner,ticker,
		// direction) invariant: the CountOpenByOwnerTickerDirection pre-check
		// above is non-transactional, so two concurrent creates can both read 0
		// and both reach here. Map the DB's unique violation to the same typed
		// sentinel the pre-check returns, closing the TOCTOU.
		if repository.IsUniqueViolation(err) {
			return nil, ErrOTCOfferDuplicateOpen
		}
		return nil, err
	}
	if err := s.revisions.Append(&model.OTCOfferRevision{
		OfferID:        o.ID,
		RevisionNumber: 1,
		Quantity:       o.Quantity,
		// Listings are termless inventory — the CREATE revision carries no
		// strike/premium/settlement; those are proposed later on the
		// negotiation chain.
		StrikePrice:             decimal.Zero,
		Premium:                 decimal.Zero,
		SettlementDate:          time.Time{},
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
			Counterparty: ptrCounterparty(o),
			StockID:      o.StockID,
			Quantity:     o.Quantity.String(),
			// Termless listing — no preset terms on the offer-created event.
			StrikePrice:    "",
			Premium:        "",
			SettlementDate: "",
		}
		if data, err := json.Marshal(payload); err == nil {
			s.publishViaOutboxOrDirect(ctx, kafkamsg.TopicOTCOfferCreated, data, "")
		}
	}
	if o.CounterpartyOwnerType != nil {
		s.notifyOTCParty(ctx, kafkamsg.OTCParty{
			OwnerType: string(*o.CounterpartyOwnerType), OwnerID: o.CounterpartyOwnerID,
		}, "OTC_OFFER_RECEIVED", "otc_offer", o.ID, map[string]string{
			"ticker": o.Ticker, "quantity": o.Quantity.String(),
			// Termless listing — no preset terms in the directed-offer notice.
			"strike_price": "", "premium": "",
		})
	}
	return o, nil
}

// UpdateQuantity sets the offer's TOTAL quantity (edit up or down). An option
// offer is termless inventory (owner, ticker, quantity); since a user may hold
// only ONE open offer per (owner, ticker, direction) they edit the total rather
// than posting a second offer. Rejects a non-positive quantity, a quantity below
// the shares already committed to formed/forming contracts on this offer
// (OutstandingCommittedQuantityTx), or — for a sell offer — a quantity above the
// owner's holding for the ticker (net of the owner's OTHER active commitments).
// Owner-only; the offer must be LOCAL and open. Runs under SELECT FOR UPDATE and
// is optimistic-lock safe (SaveTx returns ErrOptimisticLock on a version race).
func (s *OTCOfferService) UpdateQuantity(ctx context.Context, offerID uint64, ownerType model.OwnerType, ownerID *uint64, qty decimal.Decimal) (*model.OTCOffer, error) {
	if !qty.IsPositive() {
		return nil, fmt.Errorf("quantity must be > 0: %w", ErrOTCOfferFieldInvalid)
	}
	var out *model.OTCOffer
	err := s.offers.DB().Transaction(func(tx *gorm.DB) error {
		// LockByIDTx does SELECT FOR UPDATE and treats a remote row as not-found,
		// so only LOCAL offers reach the edit path.
		o, err := s.offers.LockByIDTx(tx, offerID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCOfferNotFound
			}
			return err
		}
		if !o.IsOpenListing() {
			return fmt.Errorf("offer is not open for edit: %w", ErrOTCOfferFieldInvalid)
		}
		if o.InitiatorOwnerType != ownerType || !ownerIDEqual(o.InitiatorOwnerID, ownerID) {
			return ErrOTCNotOwner
		}
		committed, err := s.offers.OutstandingCommittedQuantityTx(tx, o.ID)
		if err != nil {
			return err
		}
		if qty.LessThan(committed) {
			return fmt.Errorf("quantity %s is below the %s shares already committed on this offer: %w", qty, committed, ErrOTCOfferFieldInvalid)
		}
		if o.Direction == model.OTCDirectionSellInitiated {
			if err := s.assertSellerHasSharesTx(tx, o.InitiatorOwnerType, o.InitiatorOwnerID, o.StockID, o.ID, qty); err != nil {
				return err
			}
		}
		o.Quantity = qty
		if err := s.offers.SaveTx(tx, o); err != nil {
			return err
		}
		out = o
		return nil
	})
	return out, err
}

// assertSellerHasSharesTx is the tx-aware variant of assertSellerHasShares used
// by UpdateQuantity: it reads the seller's holding and their OTHER active
// commitments inside tx (under the offer's FOR UPDATE lock) and rejects when the
// requested total exceeds the available shares. excludeOfferID is the offer being
// resized, kept out of the committed sum so it never counts against itself.
// Reuses the same sentinels as assertSellerHasShares (ErrOTCSellerNoHolding /
// ErrOTCInsufficientShares) — no new error class is invented.
func (s *OTCOfferService) assertSellerHasSharesTx(tx *gorm.DB, ownerType model.OwnerType, ownerID *uint64, stockID, excludeOfferID uint64, requested decimal.Decimal) error {
	var holding model.Holding
	q := tx.Where("security_type = ? AND security_id = ?", "stock", stockID)
	if ownerID == nil {
		q = q.Where("owner_type = ? AND owner_id IS NULL", ownerType)
	} else {
		q = q.Where("owner_type = ? AND owner_id = ?", ownerType, *ownerID)
	}
	if err := q.First(&holding).Error; err != nil {
		return fmt.Errorf("seller has no holding for stock %d: %w", stockID, ErrOTCSellerNoHolding)
	}
	heldQty := decimal.NewFromInt(holding.Quantity)
	committed, err := s.offers.SumActiveQuantityForSellerExcludingOfferTx(tx, ownerType, ownerID, stockID, excludeOfferID)
	if err != nil {
		return err
	}
	available := heldQty.Sub(committed)
	if requested.GreaterThan(available) {
		return fmt.Errorf("insufficient available shares for this seller (held %s, committed %s, requested %s): %w", heldQty, committed, requested, ErrOTCInsufficientShares)
	}
	return nil
}

// ListMyOffers returns offers where the user is initiator/counterparty/either.
func (s *OTCOfferService) ListMyOffers(userID int64, systemType, role string, statuses []string, stockID uint64, page, pageSize int) ([]model.OTCOffer, int64, error) {
	ownerType, ownerID := model.OwnerFromLegacy(uint64(userID), systemType)
	return s.offers.ListByOwner(ownerType, ownerID, role, statuses, stockID, page, pageSize)
}

// ListNegotiationHistory returns the caller's terminal OTC negotiations
// (accepted/rejected/expired/failed) — the read-only "history" view per
// Celina-3. Callers can narrow by status, date range, and counterparty.
func (s *OTCOfferService) ListNegotiationHistory(userID int64, systemType string, f repository.HistoryFilter) ([]model.OTCOffer, int64, error) {
	ownerType, ownerID := model.OwnerFromLegacy(uint64(userID), systemType)
	return s.offers.ListNegotiationHistory(ownerType, ownerID, f)
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

// GetOffer returns the offer to any authenticated caller, mirroring the public
// discovery list (GET /api/v3/otc/options) which lists every open offer to
// everyone. The offer body itself carries no negotiation history; the handler
// stamps me_owner=false for a non-owner. Sensitive sub-data stays gated:
// revisions (the negotiation history) are returned ONLY to a participant
// (empty slice otherwise), and the read-receipt is upserted only for a
// participant. A non-participant therefore sees the offer but never its
// counter/bid history.
func (s *OTCOfferService) GetOffer(offerID uint64, actorUserID int64, actorSystemType string) (*model.OTCOffer, []model.OTCOfferRevision, error) {
	o, err := s.offers.GetByID(offerID)
	if err != nil {
		return nil, nil, err
	}
	if !s.isParticipant(o, actorUserID, actorSystemType) {
		// Public discovery: return the offer with no revisions and no
		// mark-read. Do not reject — a caller can see this offer in the
		// unified list, so the detail must be readable too (SP-1 me_owner).
		return o, nil, nil
	}
	revs, err := s.revisions.ListByOffer(o.ID)
	if err != nil {
		return nil, nil, err
	}
	// Mark read (participants only).
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
		return svcerr.New(codes.Internal, "holding lookup not configured")
	}
	holding, err := s.holdings.GetByOwnerAndSecurity(ownerType, ownerID, "stock", stockID)
	if err != nil {
		// No holding row (or lookup failure) for a covered-call seller is a
		// business-rule rejection, not an internal error — surface it as a
		// typed FailedPrecondition (→ 409) rather than leaking the raw DB
		// record-not-found that the gateway maps to 500.
		return fmt.Errorf("seller has no holding for stock %d: %w", stockID, ErrOTCSellerNoHolding)
	}
	heldQty := decimal.NewFromInt(holding.Quantity)
	committed, err := s.offers.SumActiveQuantityForSeller(ownerType, ownerID, stockID)
	if err != nil {
		return err
	}
	available := heldQty.Sub(committed)
	if requested.GreaterThan(available) {
		return fmt.Errorf("insufficient available shares for this seller (held %s, committed %s, requested %s): %w", heldQty, committed, requested, ErrOTCInsufficientShares)
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
