package main

import (
	"testing"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// TestBackfillLocalDiscriminator verifies the startup backfill corrects the
// explicit `local` column from routing_number for pre-existing rows that carry
// the wrong (or zero-default) value, and that it is idempotent.
func TestBackfillLocalDiscriminator(t *testing.T) {
	model.SetOwnRouting("111")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.OTCOffer{}, &model.OTCNegotiation{}, &model.OptionContract{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert rows with DELIBERATELY WRONG `local` values via raw SQL (bypassing
	// the BeforeCreate hook) to simulate pre-column rows that all default to
	// local=false, plus a remote row wrongly stamped local=true.
	client := uint64(1)
	mk := func(routing int64, native string, wrongLocal bool) *model.OTCOffer {
		o := &model.OTCOffer{
			RoutingNumber:               routing,
			InitiatorOwnerType:          model.OwnerClient,
			InitiatorOwnerID:            &client,
			Direction:                   model.OTCDirectionSellInitiated,
			StockID:                     1,
			Ticker:                      "TST",
			Quantity:                    decimal.NewFromInt(1),
			Status:                      model.OTCOfferStatusOpen,
			LastModifiedByPrincipalType: "client",
			LastModifiedByPrincipalID:   1,
			Local:                       wrongLocal,
		}
		if native != "" {
			o.NativeID = &native
		}
		return o
	}

	// Skip hooks so the wrong Local value is persisted verbatim.
	tx := db.Session(&gorm.Session{SkipHooks: true})
	localWrong := mk(111, "", false)   // local row wrongly stamped false
	remoteWrong := mk(222, "r1", true) // remote row wrongly stamped true
	if err := tx.Create(localWrong).Error; err != nil {
		t.Fatalf("seed localWrong: %v", err)
	}
	if err := tx.Create(remoteWrong).Error; err != nil {
		t.Fatalf("seed remoteWrong: %v", err)
	}

	// Run the backfill.
	backfillLocalDiscriminator(db, model.OwnRouting())

	var fixedLocal, fixedRemote model.OTCOffer
	if err := db.First(&fixedLocal, localWrong.ID).Error; err != nil {
		t.Fatalf("reload localWrong: %v", err)
	}
	if err := db.First(&fixedRemote, remoteWrong.ID).Error; err != nil {
		t.Fatalf("reload remoteWrong: %v", err)
	}
	if !fixedLocal.Local {
		t.Errorf("local row (routing 111): Local = false after backfill, want true")
	}
	if fixedRemote.Local {
		t.Errorf("remote row (routing 222): Local = true after backfill, want false")
	}

	// Invariant: every row now satisfies Local == (RoutingNumber == OwnRouting()).
	var all []model.OTCOffer
	if err := db.Find(&all).Error; err != nil {
		t.Fatalf("list offers: %v", err)
	}
	for _, o := range all {
		if o.Local != (o.RoutingNumber == model.OwnRouting()) {
			t.Errorf("offer id=%d: Local=%v but routing==own is %v", o.ID, o.Local, o.RoutingNumber == model.OwnRouting())
		}
	}

	// Idempotent: a second run touches nothing (no panic, values stable).
	backfillLocalDiscriminator(db, model.OwnRouting())
	var reLocal model.OTCOffer
	if err := db.First(&reLocal, localWrong.ID).Error; err != nil {
		t.Fatalf("reload after second backfill: %v", err)
	}
	if !reLocal.Local {
		t.Errorf("local row flipped by second backfill run; expected stable Local=true")
	}
}
