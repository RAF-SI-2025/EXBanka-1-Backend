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
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

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
// successful accept. The handler uses these to (1) mint the option
// contract, (2) kick off the premium-payment saga, (3) publish Kafka
// events for the cascade-cancelled siblings.
type AcceptNegotiationResult struct {
	WinningNegotiation *model.OTCNegotiation
	ParentOffer        *model.OTCOffer
	CancelledSiblings  []model.OTCNegotiation
}

// ---------- Service ----------

type OTCNegotiationService struct {
	db        *gorm.DB
	offerRepo *repository.OTCOfferRepository
	negRepo   *repository.OTCNegotiationRepository
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

// OpenNegotiation starts a fresh chain on a parent listing. Enforces:
//   - parent exists + is open (legacy PENDING/COUNTERED count as open)
//   - bidder is not the parent's poster (no self-trade)
//   - bidder does not already have a chain on this parent
//
// Inserts the OTCNegotiation row + the initial BID revision in one TX.
func (s *OTCNegotiationService) OpenNegotiation(ctx context.Context, in OpenNegotiationInput) (*model.OTCNegotiation, error) {
	var created *model.OTCNegotiation
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
			ActingEmployeeID:          in.ActingEmployeeID,
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CounterNegotiation appends a counter-offer to an existing chain. Either
// party (bidder or listing-poster) may counter. Updates the chain's
// snapshot terms to the new proposal and flips status to "countered".
func (s *OTCNegotiationService) CounterNegotiation(ctx context.Context, in CounterNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
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
		neg.ActingEmployeeID = in.ActingEmployeeID
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
		return nil
	})
	if err != nil {
		return nil, err
	}
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
		neg.ActingEmployeeID = in.ActingEmployeeID
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
	return result, nil
}

// RejectNegotiation closes a single chain without forming a contract.
// Either party may reject at any non-terminal point. The parent listing
// stays open — other chains (if any) continue.
func (s *OTCNegotiationService) RejectNegotiation(ctx context.Context, in RejectNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
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
		neg.ActingEmployeeID = in.ActingEmployeeID
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// CancelNegotiation lets the bidder withdraw their own chain. The
// listing-poster cannot cancel a bidder's chain (use Reject for that).
func (s *OTCNegotiationService) CancelNegotiation(ctx context.Context, in CancelNegotiationInput) (*model.OTCNegotiation, error) {
	var updated *model.OTCNegotiation
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
		now := time.Now().UTC()
		neg.Status = model.OTCNegotiationStatusCancelled
		neg.LastActionByPrincipalType = in.ActingPrincipalType
		neg.LastActionByPrincipalID = in.ActingPrincipalID
		neg.LastActionByOwnerType = string(in.CallerOwnerType)
		neg.LastActionByOwnerID = in.CallerOwnerID
		neg.LastActionAt = now
		neg.ActingEmployeeID = in.ActingEmployeeID
		if err := s.negRepo.SaveTx(tx, neg); err != nil {
			return err
		}
		updated = neg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
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
// Used by the listing's poster to see all incoming bids.
func (s *OTCNegotiationService) ListByParentOffer(ctx context.Context, parentOfferID uint64) ([]model.OTCNegotiation, error) {
	return s.negRepo.ListByParentOffer(parentOfferID)
}

// ---------- helpers ----------

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
