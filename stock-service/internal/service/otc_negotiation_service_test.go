package service

import (
	"context"
	"errors"
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
		StrikePrice:                 decimal.NewFromFloat(150.0),
		Premium:                     decimal.NewFromFloat(5.0),
		SettlementDate:              time.Now().UTC().AddDate(0, 1, 0),
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
		name             string
		t1, t2           model.OwnerType
		id1Set, id2Set   bool
		id1, id2         uint64
		want             bool
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
