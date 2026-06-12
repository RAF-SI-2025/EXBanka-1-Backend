// Package service — OTCNegotiationService owns the per-bidder negotiation
// chain lifecycle introduced by the OTC options marketplace refactor.
//
// Plan: docs/superpowers/plans/2026-05-16-otc-options-marketplace.md.
// The headline guarantee is **first-accept-wins**: many bidders can each
// open their own chain against the same OTCOffer listing, but only one
// chain can ever transition to "accepted" — the winning Accept call locks
// the parent row (SELECT FOR UPDATE), flips it to "consumed", and
// cascade-cancels every sibling chain inside the same transaction. A
// parallel Accept on a sibling chain blocks on the lock, then sees the
// parent is no longer open and rejects with ErrOTCParentNotOpen.
//
// This service is intentionally narrow: it owns NEGOTIATION STATE only.
// Contract minting + premium movement (the existing OTC accept saga) is
// the gRPC handler's concern in Phase 3, called immediately after Accept
// returns the winning negotiation.
package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	contractkafka "github.com/exbanka/contract/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// OTCNotifier is the narrow producer dependency for the in-app
// notification intents (notification.general). Optional — when nil,
// the service runs without emitting notifications (matches the legacy
// behaviour and keeps the test surface small).
type OTCNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg contractkafka.GeneralNotificationMessage) error
}

// ---------- Inputs ----------

// OpenNegotiationInput opens the first chain on a parent listing.
type OpenNegotiationInput struct {
	ParentOfferID   uint64
	BidderOwnerType model.OwnerType
	BidderOwnerID   *uint64
	BidderBankCode  *string
	BidderAccountID uint64
	// Initial bid terms. Quantity/StrikePrice/Premium typically match the
	// listing's posted terms (a "take it" bid) but may differ for an
	// immediate counter-bid.
	Quantity       decimal.Decimal
	StrikePrice    decimal.Decimal
	Premium        decimal.Decimal
	SettlementDate time.Time
	// Audit fields — the principal who actually made the call. May differ
	// from BidderOwnerType/ID when an employee acts on behalf of a client.
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
}

// CounterNegotiationInput proposes new terms on an existing chain. The
// caller may be either the bidder (replying to a counter from the
// listing's poster) or the listing's poster (responding to the most
// recent bid).
type CounterNegotiationInput struct {
	NegotiationID uint64
	// CallerOwnerType/ID identifies the responder. The service verifies
	// the caller is either the chain's bidder or the parent's poster.
	CallerOwnerType model.OwnerType
	CallerOwnerID   *uint64
	Quantity        decimal.Decimal
	StrikePrice     decimal.Decimal
	Premium         decimal.Decimal
	SettlementDate  time.Time
	// Audit.
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
}

// AcceptNegotiationInput finalises the chain. The caller must be the
// party OPPOSITE to the one who proposed the current terms.
type AcceptNegotiationInput struct {
	NegotiationID       uint64
	CallerOwnerType     model.OwnerType
	CallerOwnerID       *uint64
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
	// AcceptorAccountID (Phase 9) is the caller's account that pays
	// or receives the premium. Required — accept now mints a contract
	// and runs the premium-payment saga. Must be the caller's account.
	AcceptorAccountID uint64
	// OnBehalfOfFundID, when non-zero, places this accept on behalf of an
	// investment fund (E2, Plan E). The acceptor's premium debit comes from
	// the fund's RSD account; the minted contract records the fund ID so
	// that exercise credits fund_holdings. Caller must be the fund's manager
	// and AcceptorAccountID MUST equal fund.rsd_account_id.
	OnBehalfOfFundID uint64
}

// RejectNegotiationInput closes a chain without forming a contract.
// Either side may reject at any non-terminal point.
type RejectNegotiationInput struct {
	NegotiationID       uint64
	CallerOwnerType     model.OwnerType
	CallerOwnerID       *uint64
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
}

// CancelNegotiationInput withdraws the bidder's chain. Only the bidder
// can cancel their own chain (this is distinct from Reject which either
// party may issue).
type CancelNegotiationInput struct {
	NegotiationID       uint64
	CallerOwnerType     model.OwnerType
	CallerOwnerID       *uint64
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
}

// ---------- Outputs ----------

// AcceptNegotiationResult bundles the state the caller needs after a
// successful accept. Phase 9: now includes the minted OptionContract
// (the contract-formation saga runs immediately after the state TX).
// Cancelled siblings are listed so the handler can publish per-chain
// Kafka events.
type AcceptNegotiationResult struct {
	WinningNegotiation *model.OTCNegotiation
	ParentOffer        *model.OTCOffer
	CancelledSiblings  []model.OTCNegotiation
	Contract           *model.OptionContract // populated when mint succeeded; nil if the negotiation state flipped but contract formation failed
}

// ---------- Service ----------

// ContractFormer is the narrow dependency OTCNegotiationService uses
// to mint the OptionContract after a successful Accept state TX.
// Implemented by *OTCOfferService.MintContractFromAcceptedNegotiation.
// Defined here (not in OTCOfferService) so a test can swap in a fake.
type ContractFormer interface {
	MintContractFromAcceptedNegotiation(ctx context.Context, in MintFromNegotiationInput) (*model.OptionContract, error)
}

type OTCNegotiationService struct {
	db        *gorm.DB
	offerRepo *repository.OTCOfferRepository
	negRepo   *repository.OTCNegotiationRepository
	// former is optional — when nil, AcceptNegotiation falls back to
	// the legacy state-only behaviour (negotiation flips to accepted,
	// no contract minted). cmd/main.go wires it via WithContractFormer.
	former ContractFormer
	// notifier is optional — when nil, no in-app notifications fire.
	// cmd/main.go wires it via WithNotifier.
	notifier OTCNotifier
}

func NewOTCNegotiationService(
	db *gorm.DB,
	offerRepo *repository.OTCOfferRepository,
	negRepo *repository.OTCNegotiationRepository,
) *OTCNegotiationService {
	return &OTCNegotiationService{
		db:        db,
		offerRepo: offerRepo,
		negRepo:   negRepo,
	}
}

// WithContractFormer wires the post-accept contract-formation saga.
// Returns a copy so callers can chain wiring.
func (s *OTCNegotiationService) WithContractFormer(f ContractFormer) *OTCNegotiationService {
	cp := *s
	cp.former = f
	return &cp
}

// WithNotifier wires the in-app notification producer. Returns a copy
// so wiring chains. nil ⇒ notifications disabled (legacy mode).
func (s *OTCNegotiationService) WithNotifier(n OTCNotifier) *OTCNegotiationService {
	cp := *s
	cp.notifier = n
	return &cp
}

// publishNotif is a best-effort notification publisher. A CLIENT recipient gets
// a per-user "client"-scoped notification; a BANK recipient gets one on the
// shared "employee" inbox (every bank employee sees it). Always returns
// nil-equivalent — a failed publish must NEVER fail the business action
// (CLAUDE.md Kafka rule).
func (s *OTCNegotiationService) publishNotif(
	ctx context.Context,
	recipientType model.OwnerType,
	recipientID *uint64,
	notifType string,
	data map[string]string,
	refType string,
	refID uint64,
) {
	if s.notifier == nil {
		return
	}
	msg := contractkafka.GeneralNotificationMessage{
		Type:    notifType,
		Data:    data,
		RefType: refType,
		RefID:   refID,
	}
	switch recipientType {
	case model.OwnerClient:
		if recipientID == nil {
			return
		}
		msg.UserID = *recipientID
		msg.SystemType = "client"
	case model.OwnerBank:
		// Bank-owned OTC resource → the shared employee/bank inbox. Read scoping
		// ignores user_id for employees, so a nil owner id is fine; carry it when
		// present for a forward-compatible per-employee model.
		if recipientID != nil {
			msg.UserID = *recipientID
		}
		msg.SystemType = "employee"
	default:
		return
	}
	if err := s.notifier.PublishGeneralNotification(ctx, msg); err != nil {
		log.Printf("WARN: otc notif %s (%s) failed: %v", notifType, recipientType, err)
	}
}

// NotifyOTCParticipant publishes a best-effort OTC general notification to a LOCAL
// client participant (no-op for bank/employee recipients or when the notifier is
// unwired). Exported so the cross-bank OUTBOUND accept handler
// (acceptRemoteNegotiation) can notify the local ACCEPTOR of an OTC_CONTRACT_CREATED
// — the counterparty is notified by their own bank's inbound AcceptNegotiation, so
// the two together match the local accept path's dual-party notify.
func (s *OTCNegotiationService) NotifyOTCParticipant(ctx context.Context, recipientType model.OwnerType, recipientID *uint64, notifType string, data map[string]string, refType string, refID uint64) {
	s.publishNotif(ctx, recipientType, recipientID, notifType, data, refType, refID)
}

// otherParty returns the (type, id) of whoever in the chain is NOT the
// caller. Used for Counter/Reject — the recipient is always the side
// that didn't act.
func otherParty(
	negBidderType model.OwnerType, negBidderID *uint64,
	parentInitType model.OwnerType, parentInitID *uint64,
	callerType model.OwnerType, callerID *uint64,
) (model.OwnerType, *uint64) {
	if ownerMatches(negBidderType, negBidderID, callerType, callerID) {
		return parentInitType, parentInitID
	}
	return negBidderType, negBidderID
}

// OpenNegotiation starts a fresh chain on a parent listing. Enforces:
//   - parent exists + is open (legacy PENDING/COUNTERED count as open)
//   - bidder is not the parent's poster (no self-trade)
//   - bidder does not already have a chain on this parent
//
// Inserts the OTCNegotiation row + the initial BID revision in one TX.
func (s *OTCNegotiationService) OpenNegotiation(ctx context.Context, in OpenNegotiationInput) (*model.OTCNegotiation, error) {
	var created *model.OTCNegotiation
	var parentSnapshot *model.OTCOffer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := s.offerRepo.LockByIDTx(tx, in.ParentOfferID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCOfferNotFound
			}
			return err
		}
		if !parent.IsOpenListing() {
			return ErrOTCParentNotOpen
		}
		if ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, in.BidderOwnerType, in.BidderOwnerID) {
			return ErrOTCBidOwnListing
		}

		// One-chain-per-bidder invariant (also enforced by the unique
		// index, but checked here so we return a typed sentinel instead
		// of a raw SQL conflict). Use the Tx variant so we don't try to
		// acquire a second connection while holding the TX lock.
		if _, err := s.negRepo.FindChainByBidderTx(tx, in.ParentOfferID, in.BidderOwnerType, in.BidderOwnerID); err == nil {
			return ErrOTCChainAlreadyExists
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		now := time.Now().UTC()
		neg := &model.OTCNegotiation{
			ParentOfferID:             in.ParentOfferID,
			BidderOwnerType:           in.BidderOwnerType,
			BidderOwnerID:             in.BidderOwnerID,
			BidderBankCode:            in.BidderBankCode,
			BidderAccountID:           in.BidderAccountID,
			Quantity:                  in.Quantity,
			StrikePrice:               in.StrikePrice,
			Premium:                   in.Premium,
			SettlementDate:            in.SettlementDate,
			Status:                    model.OTCNegotiationStatusOpen,
			LastActionByPrincipalType: in.ActingPrincipalType,
			LastActionByPrincipalID:   in.ActingPrincipalID,
			LastActionByOwnerType:     string(in.BidderOwnerType),
			LastActionByOwnerID:       in.BidderOwnerID,
			LastActionAt:              now,
			// acting_employee_id is valid only on a bank-owned row (bank bidder);
			// an employee-on-behalf-of-client bid leaves the client-owned chain's
			// acting_employee_id nil per the ActingEmployee invariant.
			ActingEmployeeID: actingEmpForOwner(in.BidderOwnerType, in.ActingEmployeeID),
		}
		if err := s.negRepo.CreateTx(tx, neg); err != nil {
			return err
		}
		rev := &model.OTCNegotiationRevision{
			NegotiationID:           neg.ID,
			RevisionNumber:          1,
			Quantity:                in.Quantity,
			StrikePrice:             in.StrikePrice,
			Premium:                 in.Premium,
			SettlementDate:          in.SettlementDate,
			ModifiedByPrincipalType: in.ActingPrincipalType,
			ModifiedByPrincipalID:   in.ActingPrincipalID,
			ActingEmployeeID:        in.ActingEmployeeID,
			Action:                  model.OTCNegotiationActionBid,
		}
		if err := s.negRepo.AppendRevisionTx(tx, rev); err != nil {
			return err
		}
		created = neg
		parentSnapshot = parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Notify the listing's poster that a new bid landed. Best-effort,
	// after-commit (CLAUDE.md Kafka rule).
	s.publishNotif(ctx,
		parentSnapshot.InitiatorOwnerType, parentSnapshot.InitiatorOwnerID,
		"OTC_OFFER_RECEIVED",
		map[string]string{
			"ticker":       parentSnapshot.Ticker,
			"quantity":     created.Quantity.String(),
			"strike_price": created.StrikePrice.String(),
			"premium":      created.Premium.String(),
		},
		"otc_negotiation", created.ID,
	)
	return created, nil
}

// CounterNegotiation appends a counter-offer to an existing chain. Either
// party (bidder or listing-poster) may counter. Updates the chain's
// snapshot terms to the new proposal and flips status to "countered".
func (s *OTCNegotiationService) CounterNegotiation(ctx context.Context, in CounterNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
	var parentSnapshot *model.OTCOffer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		neg, err := s.negRepo.LockByID(tx, in.NegotiationID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCNegotiationNotFound
			}
			return err
		}
		if neg.IsTerminal() {
			return ErrOTCNegotiationTerminal
		}
		parent, err := s.offerRepo.LockByIDTx(tx, neg.ParentOfferID)
		if err != nil {
			return err
		}
		if !parent.IsOpenListing() {
			return ErrOTCParentNotOpen
		}
		// Authorization: caller must be either the bidder or the listing's poster.
		isBidder := ownerMatches(neg.BidderOwnerType, neg.BidderOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		isPoster := ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		if !isBidder && !isPoster {
			return ErrOTCCounterUnauthorized
		}

		now := time.Now().UTC()
		neg.Quantity = in.Quantity
		neg.StrikePrice = in.StrikePrice
		neg.Premium = in.Premium
		neg.SettlementDate = in.SettlementDate
		neg.Status = model.OTCNegotiationStatusCountered
		neg.LastActionByPrincipalType = in.ActingPrincipalType
		neg.LastActionByPrincipalID = in.ActingPrincipalID
		neg.LastActionByOwnerType = string(in.CallerOwnerType)
		neg.LastActionByOwnerID = in.CallerOwnerID
		neg.LastActionAt = now
		neg.ActingEmployeeID = actingEmpForOwner(neg.BidderOwnerType, in.ActingEmployeeID)
		if err := s.negRepo.SaveTx(tx, neg); err != nil {
			return err
		}
		nextRev, err := s.negRepo.NextRevisionNumber(tx, neg.ID)
		if err != nil {
			return err
		}
		rev := &model.OTCNegotiationRevision{
			NegotiationID:           neg.ID,
			RevisionNumber:          nextRev,
			Quantity:                in.Quantity,
			StrikePrice:             in.StrikePrice,
			Premium:                 in.Premium,
			SettlementDate:          in.SettlementDate,
			ModifiedByPrincipalType: in.ActingPrincipalType,
			ModifiedByPrincipalID:   in.ActingPrincipalID,
			ActingEmployeeID:        in.ActingEmployeeID,
			Action:                  model.OTCNegotiationActionCounter,
		}
		if err := s.negRepo.AppendRevisionTx(tx, rev); err != nil {
			return err
		}
		updated = neg
		parentSnapshot = parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Notify the OTHER party in the chain — the side that didn't act.
	otherType, otherID := otherParty(
		updated.BidderOwnerType, updated.BidderOwnerID,
		parentSnapshot.InitiatorOwnerType, parentSnapshot.InitiatorOwnerID,
		in.CallerOwnerType, in.CallerOwnerID,
	)
	s.publishNotif(ctx, otherType, otherID, "OTC_OFFER_COUNTERED",
		map[string]string{
			"ticker":       parentSnapshot.Ticker,
			"quantity":     updated.Quantity.String(),
			"strike_price": updated.StrikePrice.String(),
			"premium":      updated.Premium.String(),
		},
		"otc_negotiation", updated.ID,
	)
	return updated, nil
}

// AcceptNegotiation is the first-accept-wins transaction. The TX:
//  1. SELECT FOR UPDATE on the winning negotiation.
//  2. SELECT FOR UPDATE on the parent listing.
//  3. Verifies parent.IsOpenListing() — if a parallel accept already won,
//     this returns ErrOTCParentNotOpen.
//  4. Verifies the caller is the party OPPOSITE to the one who proposed
//     the current terms (LastActionByOwnerType/ID).
//  5. Flips the winning negotiation to "accepted" + appends ACCEPT revision.
//  6. Flips the parent listing to "consumed".
//  7. SELECT FOR UPDATE on every sibling chain still in open/countered
//     status and flips them to "cancelled" (cascade-cancel).
//
// Returns the winning negotiation, the parent offer, and the cancelled
// siblings. The HANDLER mints the OptionContract and kicks off the
// premium-payment saga from this data — that's not in the TX because
// account-service is a separate DB; the contract row is inserted in a
// follow-up transaction by the handler, with compensation on failure.
func (s *OTCNegotiationService) AcceptNegotiation(ctx context.Context, in AcceptNegotiationInput) (*AcceptNegotiationResult, error) {
	result := &AcceptNegotiationResult{}
	// Captured inside the state TX so the contract-formation-failure path can
	// RESTORE the listing + siblings it consumed/cancelled (the contract never
	// formed) instead of leaving the seller's listing permanently consumed.
	var priorParentStatus string
	siblingPriorStatus := map[uint64]string{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		neg, err := s.negRepo.LockByID(tx, in.NegotiationID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCNegotiationNotFound
			}
			return err
		}
		if neg.IsTerminal() {
			return ErrOTCNegotiationTerminal
		}
		parent, err := s.offerRepo.LockByIDTx(tx, neg.ParentOfferID)
		if err != nil {
			return err
		}
		if !parent.IsOpenListing() {
			return ErrOTCParentNotOpen
		}

		// Authorization: caller must be the OPPOSITE party to whoever
		// proposed the current terms. That is, the last action's owner
		// cannot also be the acceptor — they'd just be "accepting their
		// own offer" which is meaningless.
		lastOwnerType := model.OwnerType(neg.LastActionByOwnerType)
		if ownerMatches(lastOwnerType, neg.LastActionByOwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCAcceptUnauthorized
		}
		// The caller also has to be ONE of the two parties.
		isBidder := ownerMatches(neg.BidderOwnerType, neg.BidderOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		isPoster := ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		if !isBidder && !isPoster {
			return ErrOTCAcceptUnauthorized
		}

		now := time.Now().UTC()
		neg.Status = model.OTCNegotiationStatusAccepted
		neg.LastActionByPrincipalType = in.ActingPrincipalType
		neg.LastActionByPrincipalID = in.ActingPrincipalID
		neg.LastActionByOwnerType = string(in.CallerOwnerType)
		neg.LastActionByOwnerID = in.CallerOwnerID
		neg.LastActionAt = now
		neg.ActingEmployeeID = actingEmpForOwner(neg.BidderOwnerType, in.ActingEmployeeID)
		if err := s.negRepo.SaveTx(tx, neg); err != nil {
			return err
		}
		nextRev, err := s.negRepo.NextRevisionNumber(tx, neg.ID)
		if err != nil {
			return err
		}
		acceptRev := &model.OTCNegotiationRevision{
			NegotiationID:           neg.ID,
			RevisionNumber:          nextRev,
			Quantity:                neg.Quantity,
			StrikePrice:             neg.StrikePrice,
			Premium:                 neg.Premium,
			SettlementDate:          neg.SettlementDate,
			ModifiedByPrincipalType: in.ActingPrincipalType,
			ModifiedByPrincipalID:   in.ActingPrincipalID,
			ActingEmployeeID:        in.ActingEmployeeID,
			Action:                  model.OTCNegotiationActionAccept,
		}
		if err := s.negRepo.AppendRevisionTx(tx, acceptRev); err != nil {
			return err
		}

		priorParentStatus = parent.Status
		parent.Status = model.OTCOfferStatusConsumed
		if err := s.offerRepo.SaveTx(tx, parent); err != nil {
			return err
		}

		// Cascade-cancel every other open chain on this parent.
		siblings, err := s.negRepo.ListOpenByParentOfferForUpdate(tx, parent.ID)
		if err != nil {
			return err
		}
		cancelled := make([]model.OTCNegotiation, 0, len(siblings))
		for i := range siblings {
			sib := &siblings[i]
			if sib.ID == neg.ID {
				continue
			}
			siblingPriorStatus[sib.ID] = sib.Status
			sib.Status = model.OTCNegotiationStatusCancelled
			sib.LastActionByPrincipalType = in.ActingPrincipalType
			sib.LastActionByPrincipalID = in.ActingPrincipalID
			sib.LastActionByOwnerType = string(in.CallerOwnerType)
			sib.LastActionByOwnerID = in.CallerOwnerID
			sib.LastActionAt = now
			if err := s.negRepo.SaveTx(tx, sib); err != nil {
				return err
			}
			cancelled = append(cancelled, *sib)
		}

		result.WinningNegotiation = neg
		result.ParentOffer = parent
		result.CancelledSiblings = cancelled
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 9: contract-formation saga runs AFTER the state TX commits.
	// Designed this way (not inside the state TX) because the saga
	// makes gRPC calls to account-service which can't be part of our
	// local DB transaction. If formation fails the negotiation flips
	// to "failed" so the front-end sees a coherent terminal state;
	// the parent stays consumed and siblings stay cancelled (they
	// were correctly out of the running regardless of whether the
	// winning chain ultimately materialised a contract).
	if s.former == nil {
		// No former wired — legacy state-only behaviour. Contract
		// remains nil in result; caller may surface a warning.
		return result, nil
	}
	if in.AcceptorAccountID == 0 {
		// Formation cannot run without an acceptor account binding. Restore the
		// listing + siblings consumed in the state TX (no contract formed).
		s.restoreListingOnFormationFailure(ctx, result.WinningNegotiation.ID, result.ParentOffer.ID, priorParentStatus, siblingPriorStatus)
		return nil, ErrOTCAcceptorAccountRequired
	}
	contract, mintErr := s.former.MintContractFromAcceptedNegotiation(ctx, MintFromNegotiationInput{
		Parent:             result.ParentOffer,
		Negotiation:        result.WinningNegotiation,
		AcceptorOwnerType:  in.CallerOwnerType,
		AcceptorOwnerID:    in.CallerOwnerID,
		AcceptorAccountID:  in.AcceptorAccountID,
		ActorPrincipalType: in.ActingPrincipalType,
		ActorPrincipalID:   in.ActingPrincipalID,
		OnBehalfOfFundID:   in.OnBehalfOfFundID,
	})
	if mintErr != nil {
		// The contract never formed (the mint saga already compensated its own
		// reservations — seller shares + buyer premium). RESTORE the listing +
		// siblings we consumed/cancelled in the state TX so the seller does NOT
		// lose their listing for a deal that didn't happen, and mark the winning
		// chain "failed". Previously the parent stayed consumed → the listing
		// vanished with no contract (user-reported 2026-06-11).
		s.restoreListingOnFormationFailure(ctx, result.WinningNegotiation.ID, result.ParentOffer.ID, priorParentStatus, siblingPriorStatus)
		return nil, fmt.Errorf("mint contract: %w", mintErr)
	}
	result.Contract = contract

	// Persist the contract link on the winning negotiation row so the
	// list endpoint can surface minted_contract_id. Versioned Save
	// (CLAUDE.md optimistic-lock pattern). Best-effort — a failure here
	// does NOT roll back the already-committed state TX or the minted
	// contract; the contract exists, the user can still find it; the
	// link field just stays nil until a future retry or background job
	// backfills it.
	if contract != nil {
		linkErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			neg, err := s.negRepo.LockByID(tx, result.WinningNegotiation.ID)
			if err != nil {
				return err
			}
			neg.MintedContractID = &contract.ID
			return s.negRepo.SaveTx(tx, neg)
		})
		if linkErr != nil {
			log.Printf("WARN: failed to set minted_contract_id on negotiation %d: %v", result.WinningNegotiation.ID, linkErr)
		} else {
			result.WinningNegotiation.MintedContractID = &contract.ID
		}
	}

	// Notify each cancelled sibling's bidder that their chain lost to
	// a competing accept. accepted_premium = the winning premium so
	// the bidder sees the going rate.
	acceptedPremium := result.WinningNegotiation.Premium.String()
	for i := range result.CancelledSiblings {
		sib := &result.CancelledSiblings[i]
		s.publishNotif(ctx,
			sib.BidderOwnerType, sib.BidderOwnerID,
			"OTC_OFFER_CASCADE_CANCELLED",
			map[string]string{
				"ticker":           result.ParentOffer.Ticker,
				"accepted_premium": acceptedPremium,
			},
			"otc_negotiation", sib.ID,
		)
	}
	// OTC_CONTRACT_CREATED for both parties — the contract entity
	// now exists and the premium has moved.
	s.publishNotif(ctx,
		result.WinningNegotiation.BidderOwnerType, result.WinningNegotiation.BidderOwnerID,
		"OTC_CONTRACT_CREATED",
		map[string]string{
			"ticker":       result.ParentOffer.Ticker,
			"quantity":     result.WinningNegotiation.Quantity.String(),
			"strike_price": result.WinningNegotiation.StrikePrice.String(),
			"premium_paid": acceptedPremium,
		},
		"otc_contract", contract.ID,
	)
	s.publishNotif(ctx,
		result.ParentOffer.InitiatorOwnerType, result.ParentOffer.InitiatorOwnerID,
		"OTC_CONTRACT_CREATED",
		map[string]string{
			"ticker":       result.ParentOffer.Ticker,
			"quantity":     result.WinningNegotiation.Quantity.String(),
			"strike_price": result.WinningNegotiation.StrikePrice.String(),
			"premium_paid": acceptedPremium,
		},
		"otc_contract", contract.ID,
	)
	return result, nil
}

// markNegotiationFailed flips a negotiation's status to a terminal
// "failed" state after the contract-formation saga returned an error.
// Best-effort: if this fails the negotiation remains "accepted" but
// no contract exists, which the front-end can detect by absence of
// a contract row.
func (s *OTCNegotiationService) markNegotiationFailed(ctx context.Context, negID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		neg, err := s.negRepo.LockByID(tx, negID)
		if err != nil {
			return err
		}
		neg.Status = "failed"
		neg.LastActionAt = time.Now().UTC()
		return s.negRepo.SaveTx(tx, neg)
	})
}

// restoreListingOnFormationFailure undoes the listing-consume + sibling-cancel
// that the accept state TX committed, when contract formation did NOT produce a
// contract. In one TX it re-opens the parent listing (to its exact pre-accept
// status) and the cascade-cancelled siblings (to their captured prior statuses),
// then marks the winning chain "failed". Without this, a formation failure left
// the listing CONSUMED with no contract — the seller lost their listing for a
// deal that never happened (user-reported 2026-06-11). Best-effort: on any error
// it falls back to just marking the chain failed (the prior behaviour), logging
// so the stuck listing is diagnosable. The mint saga independently compensated
// its own money/share reservations, so re-opening the listing is the only undo
// needed here.
func (s *OTCNegotiationService) restoreListingOnFormationFailure(ctx context.Context, negID, parentID uint64, priorParentStatus string, siblingPriorStatus map[uint64]string) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := s.offerRepo.LockByIDTx(tx, parentID)
		if err != nil {
			return err
		}
		parent.Status = priorParentStatus
		if err := s.offerRepo.SaveTx(tx, parent); err != nil {
			return err
		}
		for sibID, prior := range siblingPriorStatus {
			sib, err := s.negRepo.LockByID(tx, sibID)
			if err != nil {
				return err
			}
			sib.Status = prior
			if err := s.negRepo.SaveTx(tx, sib); err != nil {
				return err
			}
		}
		neg, err := s.negRepo.LockByID(tx, negID)
		if err != nil {
			return err
		}
		neg.Status = "failed"
		neg.LastActionAt = time.Now().UTC()
		return s.negRepo.SaveTx(tx, neg)
	})
	if err != nil {
		log.Printf("WARN: restore listing after formation failure (neg %d, parent %d) failed: %v — listing stays consumed, marking chain failed", negID, parentID, err)
		_ = s.markNegotiationFailed(ctx, negID)
	}
}

// RejectNegotiation closes a single chain without forming a contract.
// Either party may reject at any non-terminal point. The parent listing
// stays open — other chains (if any) continue.
func (s *OTCNegotiationService) RejectNegotiation(ctx context.Context, in RejectNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
	var parentSnapshot *model.OTCOffer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		neg, err := s.negRepo.LockByID(tx, in.NegotiationID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCNegotiationNotFound
			}
			return err
		}
		if neg.IsTerminal() {
			return ErrOTCNegotiationTerminal
		}
		parent, err := s.offerRepo.GetByIDTx(tx, neg.ParentOfferID)
		if err != nil {
			return err
		}
		isBidder := ownerMatches(neg.BidderOwnerType, neg.BidderOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		isPoster := ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, in.CallerOwnerType, in.CallerOwnerID)
		if !isBidder && !isPoster {
			return ErrOTCCounterUnauthorized
		}
		now := time.Now().UTC()
		neg.Status = model.OTCNegotiationStatusRejected
		neg.LastActionByPrincipalType = in.ActingPrincipalType
		neg.LastActionByPrincipalID = in.ActingPrincipalID
		neg.LastActionByOwnerType = string(in.CallerOwnerType)
		neg.LastActionByOwnerID = in.CallerOwnerID
		neg.LastActionAt = now
		neg.ActingEmployeeID = actingEmpForOwner(neg.BidderOwnerType, in.ActingEmployeeID)
		if err := s.negRepo.SaveTx(tx, neg); err != nil {
			return err
		}
		nextRev, err := s.negRepo.NextRevisionNumber(tx, neg.ID)
		if err != nil {
			return err
		}
		rev := &model.OTCNegotiationRevision{
			NegotiationID:           neg.ID,
			RevisionNumber:          nextRev,
			Quantity:                neg.Quantity,
			StrikePrice:             neg.StrikePrice,
			Premium:                 neg.Premium,
			SettlementDate:          neg.SettlementDate,
			ModifiedByPrincipalType: in.ActingPrincipalType,
			ModifiedByPrincipalID:   in.ActingPrincipalID,
			ActingEmployeeID:        in.ActingEmployeeID,
			Action:                  model.OTCNegotiationActionReject,
		}
		if err := s.negRepo.AppendRevisionTx(tx, rev); err != nil {
			return err
		}
		updated = neg
		parentSnapshot = parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	otherType, otherID := otherParty(
		updated.BidderOwnerType, updated.BidderOwnerID,
		parentSnapshot.InitiatorOwnerType, parentSnapshot.InitiatorOwnerID,
		in.CallerOwnerType, in.CallerOwnerID,
	)
	s.publishNotif(ctx, otherType, otherID, "OTC_OFFER_REJECTED",
		map[string]string{"ticker": parentSnapshot.Ticker},
		"otc_negotiation", updated.ID,
	)
	return updated, nil
}

// CancelNegotiation lets the bidder withdraw their own chain. The
// listing-poster cannot cancel a bidder's chain (use Reject for that).
func (s *OTCNegotiationService) CancelNegotiation(ctx context.Context, in CancelNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
	var parentSnapshot *model.OTCOffer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		neg, err := s.negRepo.LockByID(tx, in.NegotiationID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCNegotiationNotFound
			}
			return err
		}
		if neg.IsTerminal() {
			return ErrOTCNegotiationTerminal
		}
		if !ownerMatches(neg.BidderOwnerType, neg.BidderOwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCCounterUnauthorized
		}
		parent, perr := s.offerRepo.GetByIDTx(tx, neg.ParentOfferID)
		if perr != nil {
			return perr
		}
		now := time.Now().UTC()
		neg.Status = model.OTCNegotiationStatusCancelled
		neg.LastActionByPrincipalType = in.ActingPrincipalType
		neg.LastActionByPrincipalID = in.ActingPrincipalID
		neg.LastActionByOwnerType = string(in.CallerOwnerType)
		neg.LastActionByOwnerID = in.CallerOwnerID
		neg.LastActionAt = now
		neg.ActingEmployeeID = actingEmpForOwner(neg.BidderOwnerType, in.ActingEmployeeID)
		if err := s.negRepo.SaveTx(tx, neg); err != nil {
			return err
		}
		updated = neg
		parentSnapshot = parent
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Bidder cancelled their own chain → notify the listing poster.
	s.publishNotif(ctx,
		parentSnapshot.InitiatorOwnerType, parentSnapshot.InitiatorOwnerID,
		"OTC_OFFER_CANCELLED",
		map[string]string{"ticker": parentSnapshot.Ticker},
		"otc_negotiation", updated.ID,
	)
	return updated, nil
}

// CancelListingInput closes a parent OTCOffer listing posted by the
// caller. Only the initiator may cancel; the listing must still be open.
// All still-open child chains are cascade-cancelled in the same TX.
type CancelListingInput struct {
	OfferID             uint64
	CallerOwnerType     model.OwnerType
	CallerOwnerID       *uint64
	ActingPrincipalType string
	ActingPrincipalID   uint64
	ActingEmployeeID    *uint64
}

// CancelListingResult bundles the post-cancel state so the handler can
// publish per-chain notifications outside the TX.
type CancelListingResult struct {
	Offer           *model.OTCOffer
	CancelledChains []model.OTCNegotiation
}

// CancelListing flips an open parent OTCOffer to "cancelled" and
// cascade-cancels every still-open child chain in the same transaction.
// Authorization: caller must match the listing's initiator owner.
//
// No reservations exist at the parent-listing level (the seller-can-
// deliver check at offer creation is advisory, not a lock — reservations
// are only taken inside the accept saga). So no money/share unwinding is
// needed here.
func (s *OTCNegotiationService) CancelListing(ctx context.Context, in CancelListingInput) (*CancelListingResult, error) {
	result := &CancelListingResult{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := s.offerRepo.LockByIDTx(tx, in.OfferID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOTCOfferNotFound
			}
			return err
		}
		if !parent.IsOpenListing() {
			return ErrOTCListingNotOpen
		}
		if !ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, in.CallerOwnerType, in.CallerOwnerID) {
			return ErrOTCCancelListingUnauthorized
		}

		now := time.Now().UTC()
		parent.Status = model.OTCOfferStatusCancelled
		parent.LastModifiedByPrincipalType = in.ActingPrincipalType
		parent.LastModifiedByPrincipalID = in.ActingPrincipalID
		parent.ActingEmployeeID = in.ActingEmployeeID
		if err := s.offerRepo.SaveTx(tx, parent); err != nil {
			return err
		}

		siblings, err := s.negRepo.ListOpenByParentOfferForUpdate(tx, parent.ID)
		if err != nil {
			return err
		}
		cancelled := make([]model.OTCNegotiation, 0, len(siblings))
		for i := range siblings {
			sib := &siblings[i]
			sib.Status = model.OTCNegotiationStatusCancelled
			sib.LastActionByPrincipalType = in.ActingPrincipalType
			sib.LastActionByPrincipalID = in.ActingPrincipalID
			sib.LastActionByOwnerType = string(in.CallerOwnerType)
			sib.LastActionByOwnerID = in.CallerOwnerID
			sib.LastActionAt = now
			sib.ActingEmployeeID = actingEmpForOwner(sib.BidderOwnerType, in.ActingEmployeeID)
			if err := s.negRepo.SaveTx(tx, sib); err != nil {
				return err
			}
			cancelled = append(cancelled, *sib)
		}
		result.Offer = parent
		result.CancelledChains = cancelled
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Notify each bidder whose chain was cancelled. Listing-poster
	// cancellation is a self-action so no notification to self.
	for _, ch := range result.CancelledChains {
		s.publishNotif(ctx, ch.BidderOwnerType, ch.BidderOwnerID,
			"OTC_OFFER_CASCADE_CANCELLED",
			map[string]string{"ticker": result.Offer.Ticker, "reason": "listing_cancelled"},
			"otc_negotiation", ch.ID,
		)
	}
	return result, nil
}

// LocalParentIsOpen reports whether the LOCAL OTCOffer with id offerID exists
// and is still an open listing. Used by the cross-bank (remote) accept path to
// reject an orphan accept against a listing the poster has CANCELLED/CONSUMED:
// a cross-bank child chain references its parent by (remote_parent_routing,
// remote_parent_native_id); when the routing is ours the native id is the local
// offer id, so the seller's bank (which hosts the accept + the listing) can
// authoritatively gate on the live parent status — mirroring the LOCAL accept
// path's ErrOTCParentNotOpen check. found=false (offer missing) is treated as
// NOT open. An offerID of 0 means "no resolvable local parent" → not open.
func (s *OTCNegotiationService) LocalParentIsOpen(offerID uint64) bool {
	if offerID == 0 {
		return false
	}
	parent, err := s.offerRepo.GetByID(offerID)
	if err != nil || parent == nil {
		return false
	}
	return parent.IsOpenListing()
}

// ConsumeLocalSellOfferForSeller marks this bank's open LOCAL option listing for
// (ownerType, ownerID, ticker, sell_initiated) as consumed. The cross-bank
// accept path (Direction 2: we host the seller) calls this once an option
// contract forms against the listing so it stops advertising inventory already
// under contract. Because the termless /public-stock model carries no offer id
// on the wire, the listing is resolved by its (owner, ticker, direction) unique
// key — the partial unique index guarantees at most one open row. Idempotent: a
// missing/already-consumed listing is a no-op (no error).
// acceptedQty is the quantity the forming contract took from the listing. When
// it is less than the listing's quantity (a PARTIAL accept) the unsold remainder
// is re-listed as a FRESH open listing in the SAME transaction — mirroring the
// local accept path's relistAcceptRemainder — so the seller's remaining inventory
// keeps trading as a new negotiation surface instead of vanishing with the
// consumed listing. (A re-listed offer shares the (seller,ticker) composite key,
// but the my-negotiation index only stamps ONGOING chains, so an old accepted bid
// never resurfaces on it; and a second accept of a cascade-cancelled sibling is
// rejected by the chain-status guard in acceptRemoteNegotiation.) Pass 0 to
// consume without re-listing.
func (s *OTCNegotiationService) ConsumeLocalSellOfferForSeller(ownerType model.OwnerType, ownerID *uint64, ticker string, acceptedQty int64) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		parent, err := s.offerRepo.GetOpenSellListingForUpdate(tx, ownerType, ownerID, ticker)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // idempotent: nothing open to consume (already consumed/cancelled)
			}
			return err
		}
		prevQty := parent.Quantity
		parent.Status = model.OTCOfferStatusConsumed
		if err := s.offerRepo.SaveTx(tx, parent); err != nil {
			return err
		}
		if acceptedQty <= 0 {
			return nil
		}
		remainder := prevQty.Sub(decimal.NewFromInt(acceptedQty))
		if !remainder.IsPositive() {
			return nil
		}
		fresh := &model.OTCOffer{
			InitiatorOwnerType:          parent.InitiatorOwnerType,
			InitiatorOwnerID:            parent.InitiatorOwnerID,
			CounterpartyOwnerType:       parent.CounterpartyOwnerType,
			CounterpartyOwnerID:         parent.CounterpartyOwnerID,
			Direction:                   parent.Direction,
			StockID:                     parent.StockID,
			Ticker:                      parent.Ticker,
			Quantity:                    remainder,
			Status:                      model.OTCOfferStatusPending,
			LastModifiedByPrincipalType: parent.LastModifiedByPrincipalType,
			LastModifiedByPrincipalID:   parent.LastModifiedByPrincipalID,
			InitiatorAccountID:          parent.InitiatorAccountID,
			ActingEmployeeID:            parent.ActingEmployeeID,
			Public:                      parent.Public,
			Private:                     parent.Private,
			PrivateToBankCode:           parent.PrivateToBankCode,
		}
		return s.offerRepo.SaveTx(tx, fresh)
	})
}

// LocalSellOfferOpenForSeller reports whether this bank has an OPEN local option
// listing for (ownerType, ownerID, ticker, sell_initiated). The cross-bank accept
// path (Direction 2) calls it BEFORE forming a contract to reject an accept
// against a listing the seller already CONSUMED (a prior accept) or CANCELLED —
// the termless /public-stock model carries no offer id on the wire, so the
// listing is resolved by its (owner, ticker, direction) unique key rather than by
// parent_offer_id. This is the key-based analogue of LocalParentIsOpen and the
// precondition that keeps one listing backing exactly one accepted contract
// (without it, every still-"ongoing" sibling bid could form another contract and
// over-commit the seller's shares). A DB error returns false (fail closed — do
// not form a contract on an unverifiable listing), mirroring LocalParentIsOpen.
func (s *OTCNegotiationService) LocalSellOfferOpenForSeller(ownerType model.OwnerType, ownerID *uint64, ticker string) bool {
	n, err := s.offerRepo.CountOpenByOwnerTickerDirection(ownerType, ownerID, ticker, model.OTCDirectionSellInitiated)
	if err != nil {
		return false
	}
	return n > 0
}

// ListMyNegotiations returns negotiation chains where the caller is the
// bidder. The listing-poster sees their chains via a different code path
// (list all chains on offers they posted), surfaced from the handler.
func (s *OTCNegotiationService) ListMyNegotiations(
	ctx context.Context, ownerType model.OwnerType, ownerID *uint64, statuses []string, page, pageSize int,
) ([]model.OTCNegotiation, int64, error) {
	return s.negRepo.ListByBidder(ownerType, ownerID, statuses, page, pageSize)
}

// ListByParentOffer returns every chain (any status) for a given listing.
// Authorization mirrors the timeline view: only the listing's poster (a
// client matching the initiator) or a permission-gated employee
// (owner_type="bank", already gated on otc.read.all at the gateway) may
// see all incoming bids. A competing bidder receives PermissionDenied —
// they see only their own chain via ListMyNegotiations.
//
// Returns the parent OTCOffer alongside the chain slice so the handler can
// stamp me_owner from the offer's InitiatorOwnerType/InitiatorOwnerID without
// a second DB fetch (authorizeListingAudience already loaded it).
func (s *OTCNegotiationService) ListByParentOffer(
	ctx context.Context,
	parentOfferID uint64,
	callerOwnerType model.OwnerType,
	callerOwnerID *uint64,
) (*model.OTCOffer, []model.OTCNegotiation, error) {
	parent, err := s.authorizeListingAudience(parentOfferID, callerOwnerType, callerOwnerID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.negRepo.ListByParentOffer(parentOfferID)
	if err != nil {
		return nil, nil, err
	}
	return parent, rows, nil
}

// authorizeListingAudience verifies the caller may see the full set of
// chains on a listing (all bids, or the cross-chain timeline). Returns the
// parent offer on success so callers can reuse it without a second fetch.
//
//   - owner_type="bank"  → employee; the gateway already enforced the
//     otc.read.all permission, so trust it (gateway-enforces-permissions,
//     service-enforces-ownership split, same as the rest of OTC).
//   - owner_type="client" → must equal the listing's initiator (the poster).
//   - anyone else (competing bidder) → ErrOTCListingAudienceForbidden.
func (s *OTCNegotiationService) authorizeListingAudience(
	parentOfferID uint64,
	callerOwnerType model.OwnerType,
	callerOwnerID *uint64,
) (*model.OTCOffer, error) {
	parent, err := s.offerRepo.GetByIDTx(s.db, parentOfferID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOTCOfferNotFound
		}
		return nil, err
	}
	// A folded-in REMOTE mirror offer (local=false) is NOT a local listing this
	// audience check governs — it lives in the same otc_offers table but is
	// peer-hosted. getByID (unlike LockByIDTx/GetRemoteByID) does not filter
	// `local`, so without this guard a remote listing id resolves here and the
	// audience check returns Forbidden (client caller) or succeeds with an empty
	// local-chains set (bank caller), short-circuiting the handler's remote
	// fallback (remoteListingOwnChains / remoteOfferTimeline). Treat it as
	// not-found so that fallback fires and surfaces the caller's own cross-bank
	// chain(s) on the peer-hosted listing (per-listing + timeline parity).
	if !parent.Local {
		return nil, ErrOTCOfferNotFound
	}
	if callerOwnerType == model.OwnerBank {
		return parent, nil
	}
	if ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, callerOwnerType, callerOwnerID) {
		return parent, nil
	}
	return nil, ErrOTCListingAudienceForbidden
}

// OTCTimelineItem pairs a revision with its owning chain's bidder identity.
// The poster's cross-chain audit view is a slice of these, merged across
// every chain on the listing and sorted ascending by revision CreatedAt.
type OTCTimelineItem struct {
	Negotiation model.OTCNegotiation
	Revision    model.OTCNegotiationRevision
}

// OfferTimeline returns the parent offer plus every chain's revisions
// merged into a single chronological stream (oldest first). Authorization
// is identical to ListByParentOffer: poster or permission-gated employee
// only. Ties on CreatedAt break by (negotiation_id, revision_number) so the
// ordering is deterministic.
func (s *OTCNegotiationService) OfferTimeline(
	ctx context.Context,
	parentOfferID uint64,
	callerOwnerType model.OwnerType,
	callerOwnerID *uint64,
) (*model.OTCOffer, []OTCTimelineItem, error) {
	parent, err := s.authorizeListingAudience(parentOfferID, callerOwnerType, callerOwnerID)
	if err != nil {
		return nil, nil, err
	}
	chains, err := s.negRepo.ListByParentOffer(parentOfferID)
	if err != nil {
		return nil, nil, err
	}
	var items []OTCTimelineItem
	for i := range chains {
		chain := chains[i]
		revs, rerr := s.negRepo.ListRevisions(chain.ID)
		if rerr != nil {
			return nil, nil, rerr
		}
		for j := range revs {
			items = append(items, OTCTimelineItem{Negotiation: chain, Revision: revs[j]})
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		ra, rb := items[a].Revision, items[b].Revision
		if !ra.CreatedAt.Equal(rb.CreatedAt) {
			return ra.CreatedAt.Before(rb.CreatedAt)
		}
		if ra.NegotiationID != rb.NegotiationID {
			return ra.NegotiationID < rb.NegotiationID
		}
		return ra.RevisionNumber < rb.RevisionNumber
	})
	return parent, items, nil
}

// ListRevisions returns the full revision history for a negotiation chain,
// ordered by revision_number ascending. Either party (bidder or listing
// poster) may call this — authorization is verified against the negotiation
// row and its parent offer. A third-party caller receives PermissionDenied.
func (s *OTCNegotiationService) ListRevisions(
	ctx context.Context,
	negotiationID uint64,
	callerOwnerType model.OwnerType,
	callerOwnerID *uint64,
) ([]model.OTCNegotiationRevision, error) {
	neg, err := s.negRepo.GetByID(negotiationID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOTCNegotiationNotFound
		}
		return nil, err
	}
	// Authorization: caller must be the bidder OR the parent listing's poster.
	isBidder := ownerMatches(neg.BidderOwnerType, neg.BidderOwnerID, callerOwnerType, callerOwnerID)
	if !isBidder {
		parent, perr := s.offerRepo.GetByIDTx(s.db, neg.ParentOfferID)
		if perr != nil {
			return nil, ErrOTCNegotiationNotFound
		}
		isPoster := ownerMatches(parent.InitiatorOwnerType, parent.InitiatorOwnerID, callerOwnerType, callerOwnerID)
		if !isPoster {
			return nil, ErrOTCRevisionsUnauthorized
		}
	}
	return s.negRepo.ListRevisions(negotiationID)
}

// ListRevisionsUnchecked returns a chain's revision history with NO authorization
// check. The caller MUST have already authorized access (e.g. the remote read
// paths match the caller against the chain's Remote* hosted party / parent lot
// key before calling). Used to expand a REMOTE chain into its full per-move
// timeline.
func (s *OTCNegotiationService) ListRevisionsUnchecked(negotiationID uint64) ([]model.OTCNegotiationRevision, error) {
	return s.negRepo.ListRevisions(negotiationID)
}

// ---------- helpers ----------

// actingEmpForOwner returns the acting-employee id ONLY when the resource
// being written is bank-owned. The ActingEmployee invariant (see
// model.ValidateActingEmployee) forbids stamping acting_employee_id on a
// client-owned row. A bank principal acting on a CLIENT-owned negotiation
// chain (e.g. the bank poster accepting/countering/rejecting a client
// bidder's chain) must therefore leave the negotiation's acting_employee_id
// nil — the bank's wire identity is recorded on the bank-owned OTCOffer, not
// on the counterparty's chain. Returns nil for client-owned rows regardless
// of the caller, preventing the save-time invariant violation that otherwise
// aborts the whole action with a 500.
func actingEmpForOwner(rowOwnerType model.OwnerType, actingEmployeeID *uint64) *uint64 {
	if rowOwnerType == model.OwnerBank {
		return actingEmployeeID
	}
	return nil
}

// ownerMatches reports whether two (owner_type, owner_id) tuples refer
// to the same principal. Handles nil owner_id for OwnerBank correctly:
// (bank, nil) matches (bank, nil) but does NOT match (client, nil).
func ownerMatches(t1 model.OwnerType, id1 *uint64, t2 model.OwnerType, id2 *uint64) bool {
	if t1 != t2 {
		return false
	}
	if id1 == nil && id2 == nil {
		return true
	}
	if id1 == nil || id2 == nil {
		return false
	}
	return *id1 == *id2
}
