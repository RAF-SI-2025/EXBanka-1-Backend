package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// negTestEnv bundles the minimal in-memory rig used by every test in this file.
type negTestEnv struct {
	db        *gorm.DB
	svc       *OTCNegotiationService
	offerRepo *repository.OTCOfferRepository
	negRepo   *repository.OTCNegotiationRepository
}

func newNegTestEnv(t *testing.T) *negTestEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// SQLite :memory: isolates per-connection — Transaction()/WithContext()
	// can open a fresh connection that sees no tables. Force a single
	// connection so the migration is visible to every call path.
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.OTCOffer{}, &model.OTCNegotiation{}, &model.OTCNegotiationRevision{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	offerRepo := repository.NewOTCOfferRepository(db)
	negRepo := repository.NewOTCNegotiationRepository(db)
	return &negTestEnv{
		db: db, offerRepo: offerRepo, negRepo: negRepo,
		svc: NewOTCNegotiationService(db, offerRepo, negRepo),
	}
}

func u64p(v uint64) *uint64 { return &v }

func seedListing(t *testing.T, env *negTestEnv, posterID uint64, direction, status string) *model.OTCOffer {
	t.Helper()
	o := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            u64p(posterID),
		Direction:                   direction,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      status,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   posterID,
		InitiatorAccountID:          100,
		Public:                      true,
	}
	if err := env.offerRepo.Create(o); err != nil {
		t.Fatalf("seed listing: %v", err)
	}
	return o
}

func sampleOpenInput(parentOfferID, bidderID uint64) OpenNegotiationInput {
	return OpenNegotiationInput{
		ParentOfferID:       parentOfferID,
		BidderOwnerType:     model.OwnerClient,
		BidderOwnerID:       u64p(bidderID),
		BidderAccountID:     200,
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(150.0),
		Premium:             decimal.NewFromFloat(5.0),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   bidderID,
	}
}

func TestOpenNegotiation_HappyPath(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)

	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7 /*bidder*/))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if neg.Status != model.OTCNegotiationStatusOpen {
		t.Errorf("status=%s want open", neg.Status)
	}
	revs, _ := env.negRepo.ListRevisions(neg.ID)
	if len(revs) != 1 || revs[0].Action != model.OTCNegotiationActionBid {
		t.Errorf("expected one BID revision, got %d revisions", len(revs))
	}
}

func TestOpenNegotiation_RejectsBidOwnListing(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	_, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 1 /*self-bid*/))
	if !errors.Is(err, ErrOTCBidOwnListing) {
		t.Fatalf("want ErrOTCBidOwnListing, got %v", err)
	}
}

func TestOpenNegotiation_RejectsClosedListing(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusConsumed)
	_, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if !errors.Is(err, ErrOTCParentNotOpen) {
		t.Fatalf("want ErrOTCParentNotOpen on consumed parent, got %v", err)
	}
}

func TestOpenNegotiation_AcceptsLegacyPendingStatus(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusPending)
	_, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("legacy PENDING should be treated as open, got %v", err)
	}
}

func TestOpenNegotiation_OneChainPerBidderEnforced(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)

	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7)); err != nil {
		t.Fatalf("first open: %v", err)
	}
	_, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if !errors.Is(err, ErrOTCChainAlreadyExists) {
		t.Fatalf("want ErrOTCChainAlreadyExists, got %v", err)
	}
}

func TestCounterNegotiation_BidderCounters(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))

	updated, err := env.svc.CounterNegotiation(context.Background(), CounterNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1), // poster counters
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(155.0),
		Premium:             decimal.NewFromFloat(7.0),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if updated.Status != model.OTCNegotiationStatusCountered {
		t.Errorf("status=%s want countered", updated.Status)
	}
	if !updated.StrikePrice.Equal(decimal.NewFromFloat(155.0)) {
		t.Errorf("strike not updated to 155.0: %s", updated.StrikePrice)
	}
	revs, _ := env.negRepo.ListRevisions(neg.ID)
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions (BID + COUNTER), got %d", len(revs))
	}
	if revs[1].Action != model.OTCNegotiationActionCounter {
		t.Errorf("second revision should be COUNTER, got %s", revs[1].Action)
	}
}

func TestCounterNegotiation_RejectsThirdPartyCaller(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))

	_, err := env.svc.CounterNegotiation(context.Background(), CounterNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(42), // unrelated user
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(155.0),
		Premium:             decimal.NewFromFloat(7.0),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   42,
	})
	if !errors.Is(err, ErrOTCCounterUnauthorized) {
		t.Fatalf("want ErrOTCCounterUnauthorized, got %v", err)
	}
}

func TestAcceptNegotiation_PosterAcceptsBidderTerms(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	// Now poster accepts the bidder's terms — bidder was last mover.

	result, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if result.WinningNegotiation.Status != model.OTCNegotiationStatusAccepted {
		t.Errorf("winning neg status=%s want accepted", result.WinningNegotiation.Status)
	}
	if result.ParentOffer.Status != model.OTCOfferStatusConsumed {
		t.Errorf("parent status=%s want consumed", result.ParentOffer.Status)
	}
	revs, _ := env.negRepo.ListRevisions(neg.ID)
	if len(revs) != 2 || revs[1].Action != model.OTCNegotiationActionAccept {
		t.Errorf("expected ACCEPT revision second, got revs=%+v", revs)
	}
}

// failingFormer always fails contract formation — exercises the
// restore-on-formation-failure path.
type failingFormer struct{}

func (failingFormer) MintContractFromAcceptedNegotiation(_ context.Context, _ MintFromNegotiationInput) (*model.OptionContract, error) {
	return nil, fmt.Errorf("insufficient available balance")
}

// TestAcceptNegotiation_FormationFailure_RestoresListing: when the
// contract-formation saga returns an error (no contract forms), the listing the
// accept consumed + the siblings it cascade-cancelled must be RESTORED — the
// seller must NOT lose their listing for a deal that never happened. Regression
// for the user-reported 2026-06-11 bug ("saga faulted, listing is deleted, no
// contract"). The winning chain is marked failed.
func TestAcceptNegotiation_FormationFailure_RestoresListing(t *testing.T) {
	env := newNegTestEnv(t)
	env.svc = env.svc.WithContractFormer(failingFormer{})
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	priorStatus := listing.Status
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	sib, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8))
	sibPrior := sib.Status

	_, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
		AcceptorAccountID:   17, // non-zero → reaches the mint saga (which fails)
	})
	if err == nil {
		t.Fatal("expected accept to fail when contract formation fails")
	}

	gotListing, _ := env.offerRepo.GetByID(listing.ID)
	if gotListing.Status != priorStatus {
		t.Errorf("listing status after formation failure = %q, want RESTORED to %q (not consumed)", gotListing.Status, priorStatus)
	}
	var gotNeg model.OTCNegotiation
	_ = env.db.First(&gotNeg, neg.ID).Error
	if gotNeg.Status != "failed" {
		t.Errorf("winning neg status = %q, want failed", gotNeg.Status)
	}
	var gotSib model.OTCNegotiation
	_ = env.db.First(&gotSib, sib.ID).Error
	if gotSib.Status != sibPrior {
		t.Errorf("sibling status after formation failure = %q, want RESTORED to %q (not cancelled)", gotSib.Status, sibPrior)
	}
}

// TestAcceptNegotiation_BankAcceptsClientBid guards the regression where a
// BANK poster (sell_initiated) accepting a CLIENT bidder's chain failed with
// "acting_employee_id may only be set on a bank-owned resource". The accept
// path stamped neg.ActingEmployeeID from the (bank) caller onto the
// CLIENT-owned negotiation row, violating the ActingEmployee invariant in
// OTCNegotiation.BeforeSave and aborting the whole accept (500 to the client).
// The acting-employee id must only be written when the negotiation row itself
// is bank-owned (bidder is the bank); a bank action on a client-owned chain
// leaves it nil (the bank's wire identity lives on the bank-owned OTCOffer).
func TestAcceptNegotiation_BankAcceptsClientBid(t *testing.T) {
	env := newNegTestEnv(t)
	// Bank-owned listing (sell_initiated): poster is the bank.
	o := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerBank,
		InitiatorOwnerID:            nil,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     1,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "employee",
		LastModifiedByPrincipalID:   42,
		InitiatorAccountID:          100,
		ActingEmployeeID:            u64p(42),
		Public:                      true,
	}
	if err := env.offerRepo.Create(o); err != nil {
		t.Fatalf("seed bank listing: %v", err)
	}
	// Client bidder opens a chain.
	neg, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(o.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Bank (employee acting as bank) accepts the client's bid.
	result, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerBank,
		CallerOwnerID:       nil,
		ActingPrincipalType: "employee",
		ActingPrincipalID:   42,
		ActingEmployeeID:    u64p(42),
	})
	if err != nil {
		t.Fatalf("bank accept of client bid failed: %v", err)
	}
	if result.WinningNegotiation.Status != model.OTCNegotiationStatusAccepted {
		t.Errorf("winning neg status=%s want accepted", result.WinningNegotiation.Status)
	}
	// The client-owned negotiation must NOT carry the bank's acting_employee_id.
	if result.WinningNegotiation.ActingEmployeeID != nil {
		t.Errorf("client-owned negotiation got acting_employee_id=%v, want nil", *result.WinningNegotiation.ActingEmployeeID)
	}
}

func TestAcceptNegotiation_RejectsSameSideAccept(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	// Bidder cannot accept their own bid — they were the last mover.
	_, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(7),
		ActingPrincipalType: "client",
		ActingPrincipalID:   7,
	})
	if !errors.Is(err, ErrOTCAcceptUnauthorized) {
		t.Fatalf("want ErrOTCAcceptUnauthorized, got %v", err)
	}
}

func TestAcceptNegotiation_FirstAcceptWins_CascadeCancelsSiblings(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)

	// Three parallel bidders.
	negA, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	negB, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8))
	negC, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 9))

	// Poster accepts B's terms.
	result, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       negB.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("accept B: %v", err)
	}
	if result.WinningNegotiation.ID != negB.ID {
		t.Errorf("winner not B")
	}
	if len(result.CancelledSiblings) != 2 {
		t.Errorf("expected 2 cancelled siblings, got %d", len(result.CancelledSiblings))
	}
	// Verify A + C in DB are cancelled.
	a, _ := env.negRepo.GetByID(negA.ID)
	c, _ := env.negRepo.GetByID(negC.ID)
	if a.Status != model.OTCNegotiationStatusCancelled {
		t.Errorf("negA status=%s want cancelled", a.Status)
	}
	if c.Status != model.OTCNegotiationStatusCancelled {
		t.Errorf("negC status=%s want cancelled", c.Status)
	}
	// Parent must be consumed.
	parent, _ := env.offerRepo.GetByID(listing.ID)
	if parent.Status != model.OTCOfferStatusConsumed {
		t.Errorf("parent status=%s want consumed", parent.Status)
	}
}

// TestAcceptNegotiation_ConcurrentAcceptOnlyOneWins exercises the
// FIRST-ACCEPT-WINS guarantee via two parallel goroutines accepting two
// different sibling chains on the same parent. Exactly one must succeed;
// the other must fail with ErrOTCParentNotOpen because the SELECT FOR
// UPDATE on the parent serializes them.
//
// SQLite's in-memory mode is single-threaded so we can't test true
// concurrency here — we settle for sequential calls that prove the
// parent-status check fires correctly on the second attempt. The full
// concurrency proof lives in the integration suite where we have a real
// Postgres.
func TestAcceptNegotiation_SecondAcceptRejectedAfterFirstWins(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	negA, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	negB, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8))

	if _, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       negA.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	}); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Sibling B is now in `cancelled` so the IsTerminal check fires first.
	// To prove the parent-status check would fire if B were still open,
	// manually reset B and try again — the parent is still consumed.
	b, _ := env.negRepo.GetByID(negB.ID)
	b.Status = model.OTCNegotiationStatusOpen
	if err := env.negRepo.Save(b); err != nil {
		t.Fatalf("reset B: %v", err)
	}
	_, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       negB.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if !errors.Is(err, ErrOTCParentNotOpen) {
		t.Fatalf("want ErrOTCParentNotOpen on second accept, got %v", err)
	}
}

func TestAcceptNegotiation_RejectsTerminalChain(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	neg.Status = model.OTCNegotiationStatusCancelled
	_ = env.negRepo.Save(neg)

	_, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if !errors.Is(err, ErrOTCNegotiationTerminal) {
		t.Fatalf("want ErrOTCNegotiationTerminal, got %v", err)
	}
}

func TestRejectNegotiation_PosterRejects(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))

	updated, err := env.svc.RejectNegotiation(context.Background(), RejectNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if updated.Status != model.OTCNegotiationStatusRejected {
		t.Errorf("status=%s want rejected", updated.Status)
	}
	// Parent stays open — other chains may still negotiate.
	parent, _ := env.offerRepo.GetByID(listing.ID)
	if !parent.IsOpenListing() {
		t.Errorf("reject should NOT close the listing, parent status=%s", parent.Status)
	}
}

func TestCancelNegotiation_BidderOnly(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	neg, _ := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))

	// Poster CANNOT cancel a bidder's chain.
	_, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if !errors.Is(err, ErrOTCCounterUnauthorized) {
		t.Fatalf("poster cancel should be unauthorized, got %v", err)
	}

	// Bidder can.
	updated, err := env.svc.CancelNegotiation(context.Background(), CancelNegotiationInput{
		NegotiationID:       neg.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(7),
		ActingPrincipalType: "client",
		ActingPrincipalID:   7,
	})
	if err != nil {
		t.Fatalf("bidder cancel: %v", err)
	}
	if updated.Status != model.OTCNegotiationStatusCancelled {
		t.Errorf("status=%s want cancelled", updated.Status)
	}
}

func TestCancelListing_InitiatorOnly_CascadesChains(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	// Two bidders open chains so we can verify cascade-cancel.
	_, _ = env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	_, _ = env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8))

	// Non-poster cannot cancel.
	_, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID:             listing.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(99),
		ActingPrincipalType: "client",
		ActingPrincipalID:   99,
	})
	if !errors.Is(err, ErrOTCCancelListingUnauthorized) {
		t.Fatalf("non-poster cancel: want ErrOTCCancelListingUnauthorized got %v", err)
	}

	// Poster can. Returns the cancelled siblings.
	res, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID:             listing.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if err != nil {
		t.Fatalf("poster cancel: %v", err)
	}
	if res.Offer.Status != model.OTCOfferStatusCancelled {
		t.Errorf("parent status=%s want cancelled", res.Offer.Status)
	}
	if len(res.CancelledChains) != 2 {
		t.Fatalf("want 2 cascade-cancelled chains, got %d", len(res.CancelledChains))
	}
	for _, ch := range res.CancelledChains {
		if ch.Status != model.OTCNegotiationStatusCancelled {
			t.Errorf("chain %d status=%s want cancelled", ch.ID, ch.Status)
		}
	}

	// Second cancel on the same listing fails — no longer open.
	_, err = env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID:             listing.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if !errors.Is(err, ErrOTCListingNotOpen) {
		t.Fatalf("second cancel: want ErrOTCListingNotOpen got %v", err)
	}
}

func TestCancelListing_NotFound(t *testing.T) {
	env := newNegTestEnv(t)
	_, err := env.svc.CancelListing(context.Background(), CancelListingInput{
		OfferID:             9999,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	})
	if !errors.Is(err, ErrOTCOfferNotFound) {
		t.Fatalf("want ErrOTCOfferNotFound got %v", err)
	}
}

func TestListMyNegotiations_FiltersByBidder(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	_, _ = env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	_, _ = env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8))

	rows, total, err := env.svc.ListMyNegotiations(context.Background(),
		model.OwnerClient, u64p(7), nil, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("want bidder 7 total=1 got total=%d len=%d", total, len(rows))
	}
	if rows[0].BidderOwnerID == nil || *rows[0].BidderOwnerID != 7 {
		t.Errorf("got wrong bidder back: %+v", rows[0].BidderOwnerID)
	}
}

// Smoke test the helper directly because it's used in every authorization
// check and must handle every (type, id) edge case.
func TestOwnerMatches(t *testing.T) {
	cases := []struct {
		name           string
		t1, t2         model.OwnerType
		id1Set, id2Set bool
		id1, id2       uint64
		want           bool
	}{
		{"both bank nil", model.OwnerBank, model.OwnerBank, false, false, 0, 0, true},
		{"bank vs client", model.OwnerBank, model.OwnerClient, false, false, 0, 0, false},
		{"same client", model.OwnerClient, model.OwnerClient, true, true, 7, 7, true},
		{"different clients", model.OwnerClient, model.OwnerClient, true, true, 7, 8, false},
		{"mixed nil", model.OwnerClient, model.OwnerClient, true, false, 7, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var id1, id2 *uint64
			if c.id1Set {
				id1 = u64p(c.id1)
			}
			if c.id2Set {
				id2 = u64p(c.id2)
			}
			if got := ownerMatches(c.t1, id1, c.t2, id2); got != c.want {
				t.Errorf("ownerMatches(%v,%v,%v,%v)=%v want %v", c.t1, id1, c.t2, id2, got, c.want)
			}
		})
	}
}

// True concurrency cannot be exercised on SQLite — the parallel
// first-accept-wins proof lives in the integration suite (Postgres,
// real MVCC). What we cover here serially: TestAcceptNegotiation_
// SecondAcceptRejectedAfterFirstWins (parent FOR UPDATE serializes
// accepts; second sees consumed status) and TestOpenNegotiation_
// OneChainPerBidderEnforced (unique-index sentinel).

// ---- Listing-audience authorization + cross-chain timeline ----

// seedTwoChains opens chains for bidders 7 and 9 against a poster=1 listing,
// then has the poster counter bidder 7's chain. Returns the listing.
func seedTwoChainsWithCounter(t *testing.T, env *negTestEnv) *model.OTCOffer {
	t.Helper()
	ctx := context.Background()
	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	negA, err := env.svc.OpenNegotiation(ctx, sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open chain A: %v", err)
	}
	if _, err := env.svc.OpenNegotiation(ctx, sampleOpenInput(listing.ID, 9)); err != nil {
		t.Fatalf("open chain B: %v", err)
	}
	if _, err := env.svc.CounterNegotiation(ctx, CounterNegotiationInput{
		NegotiationID:       negA.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1), // poster counters chain A
		Quantity:            decimal.NewFromInt(10),
		StrikePrice:         decimal.NewFromFloat(155.0),
		Premium:             decimal.NewFromFloat(7.0),
		SettlementDate:      time.Now().UTC().AddDate(0, 1, 0),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
	}); err != nil {
		t.Fatalf("poster counter: %v", err)
	}
	return listing
}

func TestListByParentOffer_PosterAllowed(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedTwoChainsWithCounter(t, env)
	parent, rows, err := env.svc.ListByParentOffer(context.Background(), listing.ID, model.OwnerClient, u64p(1))
	if err != nil {
		t.Fatalf("poster ListByParentOffer: %v", err)
	}
	if parent == nil || parent.ID != listing.ID {
		t.Errorf("parent offer missing or wrong id: %+v", parent)
	}
	if len(rows) != 2 {
		t.Errorf("want 2 chains, got %d", len(rows))
	}
}

func TestListByParentOffer_BidderForbidden(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedTwoChainsWithCounter(t, env)
	// Bidder 7 is a party to one chain but is NOT the listing poster — they
	// must not see every chain on the offer.
	parent, rows, err := env.svc.ListByParentOffer(context.Background(), listing.ID, model.OwnerClient, u64p(7))
	if !errors.Is(err, ErrOTCListingAudienceForbidden) {
		t.Fatalf("want ErrOTCListingAudienceForbidden, got %v", err)
	}
	if parent != nil || rows != nil {
		t.Errorf("expected nil parent+rows on forbidden")
	}
}

func TestListByParentOffer_EmployeeBankAllowed(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedTwoChainsWithCounter(t, env)
	// Employee identity (owner_type="bank"); gateway already enforced
	// otc.read.all, so the service trusts it.
	parent, rows, err := env.svc.ListByParentOffer(context.Background(), listing.ID, model.OwnerBank, nil)
	if err != nil {
		t.Fatalf("employee ListByParentOffer: %v", err)
	}
	if parent == nil || parent.ID != listing.ID {
		t.Errorf("parent offer missing or wrong id: %+v", parent)
	}
	if len(rows) != 2 {
		t.Errorf("want 2 chains, got %d", len(rows))
	}
}

func TestListByParentOffer_OfferNotFound(t *testing.T) {
	env := newNegTestEnv(t)
	_, _, err := env.svc.ListByParentOffer(context.Background(), 999, model.OwnerBank, nil)
	if !errors.Is(err, ErrOTCOfferNotFound) {
		t.Fatalf("want ErrOTCOfferNotFound, got %v", err)
	}
}

func TestOfferTimeline_MergesAllChainsAndSorts(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedTwoChainsWithCounter(t, env)
	offer, items, err := env.svc.OfferTimeline(context.Background(), listing.ID, model.OwnerClient, u64p(1))
	if err != nil {
		t.Fatalf("OfferTimeline: %v", err)
	}
	if offer == nil || offer.ID != listing.ID {
		t.Fatalf("offer mismatch: %+v", offer)
	}
	// Chain A: BID + COUNTER (2). Chain B: BID (1). Total 3 across all chains.
	if len(items) != 3 {
		t.Fatalf("want 3 timeline entries across all chains, got %d", len(items))
	}
	// Non-decreasing CreatedAt ordering.
	for i := 1; i < len(items); i++ {
		if items[i].Revision.CreatedAt.Before(items[i-1].Revision.CreatedAt) {
			t.Errorf("timeline not sorted ascending at index %d", i)
		}
	}
	// Every entry carries its chain's bidder identity (7 or 9), never the poster.
	seenBidders := map[uint64]bool{}
	for _, it := range items {
		if it.Negotiation.BidderOwnerID == nil {
			t.Fatalf("nil bidder id in timeline entry")
		}
		seenBidders[*it.Negotiation.BidderOwnerID] = true
	}
	if !seenBidders[7] || !seenBidders[9] {
		t.Errorf("expected both chains (bidders 7 and 9) represented, got %v", seenBidders)
	}
}

func TestOfferTimeline_BidderForbidden(t *testing.T) {
	env := newNegTestEnv(t)
	listing := seedTwoChainsWithCounter(t, env)
	_, _, err := env.svc.OfferTimeline(context.Background(), listing.ID, model.OwnerClient, u64p(7))
	if !errors.Is(err, ErrOTCListingAudienceForbidden) {
		t.Fatalf("want ErrOTCListingAudienceForbidden, got %v", err)
	}
}
