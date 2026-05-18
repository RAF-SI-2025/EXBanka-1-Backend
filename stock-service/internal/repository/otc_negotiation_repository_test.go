package repository

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

func newOTCNegotiationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCNegotiation{}, &model.OTCNegotiationRevision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newSampleNegotiation(parentOfferID uint64, bidderID *uint64, status string) *model.OTCNegotiation {
	now := time.Now().UTC()
	return &model.OTCNegotiation{
		ParentOfferID:             parentOfferID,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             bidderID,
		BidderAccountID:           42,
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromFloat(150.50),
		Premium:                   decimal.NewFromFloat(5.00),
		SettlementDate:            now.AddDate(0, 1, 0),
		Status:                    status,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   *bidderID,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       bidderID,
		LastActionAt:              now,
	}
}

func TestOTCNegotiation_CreateAndGet(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(7)
	n := newSampleNegotiation(100, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.ID == 0 {
		t.Fatalf("expected non-zero ID")
	}

	got, err := r.GetByID(n.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ParentOfferID != 100 || *got.BidderOwnerID != 7 {
		t.Errorf("unexpected row: parent=%d bidder=%v", got.ParentOfferID, got.BidderOwnerID)
	}
}

func TestOTCNegotiation_ChainUniqueness_OnePerBidderPerOffer(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(7)
	if err := r.Create(newSampleNegotiation(100, &bidder, model.OTCNegotiationStatusOpen)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second chain by SAME bidder against SAME parent must fail.
	err := r.Create(newSampleNegotiation(100, &bidder, model.OTCNegotiationStatusOpen))
	if err == nil {
		t.Fatalf("expected unique-index violation on duplicate (parent, bidder)")
	}

	// Different parent — OK.
	if err := r.Create(newSampleNegotiation(101, &bidder, model.OTCNegotiationStatusOpen)); err != nil {
		t.Fatalf("different-parent create should succeed: %v", err)
	}
	// Different bidder on same parent — OK.
	other := uint64(8)
	if err := r.Create(newSampleNegotiation(100, &other, model.OTCNegotiationStatusOpen)); err != nil {
		t.Fatalf("different-bidder create should succeed: %v", err)
	}
}

func TestOTCNegotiation_ListOpenByParentOfferForUpdate_FiltersByStatus(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidders := []uint64{7, 8, 9, 10}
	statuses := []string{
		model.OTCNegotiationStatusOpen,
		model.OTCNegotiationStatusCountered,
		model.OTCNegotiationStatusAccepted,
		model.OTCNegotiationStatusCancelled,
	}
	for i, b := range bidders {
		bid := b
		if err := r.Create(newSampleNegotiation(100, &bid, statuses[i])); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		open, err := r.ListOpenByParentOfferForUpdate(tx, 100)
		if err != nil {
			return err
		}
		if len(open) != 2 {
			t.Errorf("expected 2 open chains (open+countered), got %d", len(open))
		}
		for _, n := range open {
			if n.IsTerminal() {
				t.Errorf("got terminal chain in open list: status=%s", n.Status)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

func TestOTCNegotiation_FindChainByBidder(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(7)
	created := newSampleNegotiation(100, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(created); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.FindChainByBidder(100, model.OwnerClient, &bidder)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("got id=%d want %d", got.ID, created.ID)
	}

	// Different bidder → not found.
	other := uint64(8)
	if _, err := r.FindChainByBidder(100, model.OwnerClient, &other); err == nil {
		t.Errorf("expected not-found for non-existent chain")
	}
}

func TestOTCNegotiation_Revisions_AppendAndList(t *testing.T) {
	db := newOTCNegotiationTestDB(t)
	r := NewOTCNegotiationRepository(db)

	bidder := uint64(7)
	n := newSampleNegotiation(100, &bidder, model.OTCNegotiationStatusOpen)
	if err := r.Create(n); err != nil {
		t.Fatalf("create neg: %v", err)
	}

	// Append 3 revisions: BID, COUNTER, ACCEPT.
	for i, action := range []string{
		model.OTCNegotiationActionBid,
		model.OTCNegotiationActionCounter,
		model.OTCNegotiationActionAccept,
	} {
		err := db.Transaction(func(tx *gorm.DB) error {
			num, err := r.NextRevisionNumber(tx, n.ID)
			if err != nil {
				return err
			}
			rev := &model.OTCNegotiationRevision{
				NegotiationID:           n.ID,
				RevisionNumber:          num,
				Quantity:                decimal.NewFromInt(10),
				StrikePrice:             decimal.NewFromFloat(150.50),
				Premium:                 decimal.NewFromFloat(decimal.NewFromInt(int64(i + 5)).InexactFloat64()),
				SettlementDate:          time.Now().UTC(),
				ModifiedByPrincipalType: "client",
				ModifiedByPrincipalID:   bidder,
				Action:                  action,
			}
			return r.AppendRevisionTx(tx, rev)
		})
		if err != nil {
			t.Fatalf("append revision %d: %v", i, err)
		}
	}

	revs, err := r.ListRevisions(n.ID)
	if err != nil {
		t.Fatalf("list revs: %v", err)
	}
	if len(revs) != 3 {
		t.Fatalf("expected 3 revisions, got %d", len(revs))
	}
	for i, rev := range revs {
		if rev.RevisionNumber != i+1 {
			t.Errorf("revision %d has number=%d, want %d", i, rev.RevisionNumber, i+1)
		}
	}
	if revs[0].Action != model.OTCNegotiationActionBid {
		t.Errorf("first revision should be BID, got %s", revs[0].Action)
	}
	if revs[2].Action != model.OTCNegotiationActionAccept {
		t.Errorf("third revision should be ACCEPT, got %s", revs[2].Action)
	}
}

func TestOTCNegotiation_IsTerminal(t *testing.T) {
	cases := []struct {
		status   string
		terminal bool
	}{
		{model.OTCNegotiationStatusOpen, false},
		{model.OTCNegotiationStatusCountered, false},
		{model.OTCNegotiationStatusAccepted, true},
		{model.OTCNegotiationStatusRejected, true},
		{model.OTCNegotiationStatusCancelled, true},
		{model.OTCNegotiationStatusExpired, true},
	}
	for _, c := range cases {
		n := &model.OTCNegotiation{Status: c.status}
		if n.IsTerminal() != c.terminal {
			t.Errorf("status=%s: IsTerminal=%v want %v", c.status, n.IsTerminal(), c.terminal)
		}
	}
}
