// Package repository — local-discriminator guard tests.
//
// Every method that feeds LOCAL-ONLY money paths (accept, cascade, expiry,
// exercise) must filter to local == true so remote rows that land in the
// unified tables (Tasks 4-6) can NEVER enter those paths; the remote-scoped
// methods filter to local == false. The `local` column is THE authoritative
// discriminator (stamped once in BeforeCreate as routing_number == OwnRouting()).
//
// Setup: sqlite :memory:, OwnRouting = 111.
// Seed one LOCAL offer/negotiation/contract (routing 111 via BeforeCreate)
// and one REMOTE offer/negotiation/contract (routing 222, set explicitly).
package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
)

// newGuardTestDB opens a sqlite :memory: DB and auto-migrates the three
// models under test. It also calls model.SetOwnRouting("111") so that
// BeforeCreate hooks stamp local rows with routing_number = 111.
func newGuardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	model.SetOwnRouting("111")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("guard test: open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.OTCOffer{},
		&model.OTCNegotiation{},
		&model.OptionContract{},
	); err != nil {
		t.Fatalf("guard test: migrate: %v", err)
	}
	return db
}

// seedGuardFixtures inserts:
//   - localOffer  — routing 111 (via BeforeCreate), status open
//   - remoteOffer — routing 222 (explicit), status open, distinct NativeID
//   - localNeg    — routing 111, bidder=client/1, status open
//   - remoteNeg   — routing 222, bidder=client/2, status open, distinct NativeID
//   - localContract  — routing 111, status ACTIVE
//   - remoteContract — routing 222, status ACTIVE, distinct NativeID
//
// Returns the IDs in that order so tests can reference them directly.
func seedGuardFixtures(t *testing.T, db *gorm.DB) (
	localOfferID, remoteOfferID uint64,
	localNegID, remoteNegID uint64,
	localContractID, remoteContractID uint64,
) {
	t.Helper()

	futureDate := time.Now().UTC().AddDate(0, 3, 0)
	pastDate := time.Now().UTC().AddDate(0, -1, -1) // already past → eligible for expiry

	bidder1 := uint64(1)
	bidder2 := uint64(2)
	stockID := uint64(99)

	// ---- OTCOffer: LOCAL (BeforeCreate stamps 111) ----
	localOffer := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder1,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Ticker:                      "TST",
		Quantity:                    decimal.NewFromInt(10), // past settlement (offers no longer auto-expire)
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   1,
	}
	if err := db.Create(localOffer).Error; err != nil {
		t.Fatalf("seed localOffer: %v", err)
	}
	localOfferID = localOffer.ID

	// ---- OTCOffer: REMOTE (explicit routing 222) ----
	remoteNativeID := "remote-offer-1"
	remoteOffer := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &remoteNativeID,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder2,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Ticker:                      "TST",
		Quantity:                    decimal.NewFromInt(10), // past settlement (offers no longer auto-expire)
		Status:                      model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   2,
	}
	if err := db.Create(remoteOffer).Error; err != nil {
		t.Fatalf("seed remoteOffer: %v", err)
	}
	remoteOfferID = remoteOffer.ID

	// Also insert an open-status offer for the ListOpenForCache tests.
	// Use same local/remote distinction.
	localOpenNativeID := "local-open-offer"
	localOpenOffer := &model.OTCOffer{
		NativeID:                    &localOpenNativeID,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder1,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Ticker:                      "TST",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   1,
	}
	if err := db.Create(localOpenOffer).Error; err != nil {
		t.Fatalf("seed localOpenOffer: %v", err)
	}

	remoteOpenNativeID := "remote-open-offer"
	remoteOpenOffer := &model.OTCOffer{
		RoutingNumber:               222,
		NativeID:                    &remoteOpenNativeID,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &bidder2,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     stockID,
		Ticker:                      "TST",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusOpen,
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   2,
	}
	if err := db.Create(remoteOpenOffer).Error; err != nil {
		t.Fatalf("seed remoteOpenOffer: %v", err)
	}

	// ---- OTCNegotiation: LOCAL (BeforeCreate stamps 111) ----
	localNeg := &model.OTCNegotiation{
		ParentOfferID:             localOffer.ID,
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bidder1,
		BidderAccountID:           10,
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromFloat(100),
		Premium:                   decimal.NewFromFloat(5),
		SettlementDate:            futureDate,
		Status:                    model.OTCNegotiationStatusOpen,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   1,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bidder1,
		LastActionAt:              time.Now().UTC(),
	}
	if err := db.Create(localNeg).Error; err != nil {
		t.Fatalf("seed localNeg: %v", err)
	}
	localNegID = localNeg.ID

	// ---- OTCNegotiation: REMOTE (explicit routing 222) ----
	// Uses a different NativeID to avoid the unique index on (routing, native_id).
	remoteNegNativeID := "remote-neg-1"
	remoteNeg := &model.OTCNegotiation{
		RoutingNumber:             222,
		NativeID:                  &remoteNegNativeID,
		ParentOfferID:             localOffer.ID, // same parent so cascade tests work
		BidderOwnerType:           model.OwnerClient,
		BidderOwnerID:             &bidder2,
		BidderAccountID:           20,
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromFloat(100),
		Premium:                   decimal.NewFromFloat(5),
		SettlementDate:            futureDate,
		Status:                    model.OTCNegotiationStatusOpen,
		LastActionByPrincipalType: "client",
		LastActionByPrincipalID:   2,
		LastActionByOwnerType:     "client",
		LastActionByOwnerID:       &bidder2,
		LastActionAt:              time.Now().UTC(),
	}
	if err := db.Create(remoteNeg).Error; err != nil {
		t.Fatalf("seed remoteNeg: %v", err)
	}
	remoteNegID = remoteNeg.ID

	// ---- OptionContract: LOCAL (BeforeCreate stamps 111) ----
	localOfferIDPtr := localOffer.ID
	localContract := &model.OptionContract{
		OfferID:         &localOfferIDPtr,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bidder1,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &bidder2,
		StockID:         stockID,
		Ticker:          "TST",
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromFloat(100),
		PremiumPaid:     decimal.NewFromFloat(5),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  pastDate, // past → eligible for ListExpiring
		BuyerAccountID:  10,
		SellerAccountID: 20,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-local-1",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(localContract).Error; err != nil {
		t.Fatalf("seed localContract: %v", err)
	}
	localContractID = localContract.ID

	// ---- OptionContract: REMOTE (explicit routing 222) ----
	remoteContractNativeID := "remote-contract-1"
	remoteContract := &model.OptionContract{
		RoutingNumber:   222,
		NativeID:        &remoteContractNativeID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bidder1,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &bidder2,
		StockID:         stockID,
		Ticker:          "TST",
		Quantity:        decimal.NewFromInt(10),
		StrikePrice:     decimal.NewFromFloat(100),
		PremiumPaid:     decimal.NewFromFloat(5),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  pastDate, // also past → should be EXCLUDED by guarded ListExpiring
		BuyerAccountID:  30,
		SellerAccountID: 40,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-remote-1",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(remoteContract).Error; err != nil {
		t.Fatalf("seed remoteContract: %v", err)
	}
	remoteContractID = remoteContract.ID

	return
}

// ---------------------------------------------------------------------------
// OTCOfferRepository guards
// ---------------------------------------------------------------------------

// TestGuard_ListOpenForCache_ExcludesRemote verifies that ListOpenForCache
// only returns offers whose routing_number == OwnRouting() (111).
func TestGuard_ListOpenForCache_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCOfferRepository(db)
	seedGuardFixtures(t, db)

	rows, err := r.ListOpenForCache(1000)
	if err != nil {
		t.Fatalf("ListOpenForCache: %v", err)
	}
	for _, o := range rows {
		if !o.Local {
			t.Errorf("ListOpenForCache returned remote row id=%d (local=%v routing=%d)", o.ID, o.Local, o.RoutingNumber)
		}
	}
	// Must still return the local open offer (sanity check it's not empty).
	if len(rows) == 0 {
		t.Errorf("ListOpenForCache returned nothing — local row missing from result")
	}
}

// TestGuard_LockByIDTx_RemoteOffer_NotFound verifies that LockByIDTx returns
// gorm.ErrRecordNotFound when the locked row is remote (routing != own).
func TestGuard_LockByIDTx_RemoteOffer_NotFound(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCOfferRepository(db)
	_, remoteOfferID, _, _, _, _ := seedGuardFixtures(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := r.LockByIDTx(tx, remoteOfferID)
		return err
	})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("LockByIDTx(remote): want ErrRecordNotFound, got %v", err)
	}
}

// TestGuard_LockByIDTx_LocalOffer_Succeeds verifies that LockByIDTx still
// works for local rows (routing == own).
func TestGuard_LockByIDTx_LocalOffer_Succeeds(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCOfferRepository(db)
	localOfferID, _, _, _, _, _ := seedGuardFixtures(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		o, err := r.LockByIDTx(tx, localOfferID)
		if err != nil {
			return err
		}
		if !o.Local {
			t.Errorf("LockByIDTx(local): Local=%v want true (routing=%d)", o.Local, o.RoutingNumber)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("LockByIDTx(local) unexpectedly failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiationRepository guards
// ---------------------------------------------------------------------------

// TestGuard_ListOpenByParentOfferForUpdate_ExcludesRemote verifies that the
// cascade-cancel query only locks LOCAL chains.
func TestGuard_ListOpenByParentOfferForUpdate_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	localOfferID, _, _, _, _, _ := seedGuardFixtures(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		rows, err := r.ListOpenByParentOfferForUpdate(tx, localOfferID)
		if err != nil {
			return err
		}
		for _, n := range rows {
			if !n.Local {
				t.Errorf("ListOpenByParentOfferForUpdate returned remote row id=%d (local=%v routing=%d)", n.ID, n.Local, n.RoutingNumber)
			}
		}
		if len(rows) == 0 {
			t.Errorf("ListOpenByParentOfferForUpdate returned nothing — local row missing")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("tx: %v", err)
	}
}

// TestGuard_LockByID_RemoteNeg_NotFound verifies that LockByID (negotiation)
// returns gorm.ErrRecordNotFound for a remote row.
func TestGuard_LockByID_RemoteNeg_NotFound(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_, _, _, remoteNegID, _, _ := seedGuardFixtures(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := r.LockByID(tx, remoteNegID)
		return err
	})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("LockByID(remote neg): want ErrRecordNotFound, got %v", err)
	}
}

// TestGuard_LockByID_LocalNeg_Succeeds verifies local negotiation lock still works.
func TestGuard_LockByID_LocalNeg_Succeeds(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_, _, localNegID, _, _, _ := seedGuardFixtures(t, db)

	err := db.Transaction(func(tx *gorm.DB) error {
		n, err := r.LockByID(tx, localNegID)
		if err != nil {
			return err
		}
		if !n.Local {
			t.Errorf("LockByID(local neg): Local=%v want true (routing=%d)", n.Local, n.RoutingNumber)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("LockByID(local neg) unexpectedly failed: %v", err)
	}
}

// TestGuard_ListByBidder_ExcludesRemote verifies that ListByBidder only
// returns negotiations with routing_number == OwnRouting().
func TestGuard_ListByBidder_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	bidder1 := uint64(1)
	seedGuardFixtures(t, db)

	// bidder1 has a local negotiation; the remote has bidder2 so results are
	// scoped by routing even when using bidder1 only.
	rows, _, err := r.ListByBidder(model.OwnerClient, &bidder1, nil, 1, 1000)
	if err != nil {
		t.Fatalf("ListByBidder: %v", err)
	}
	for _, n := range rows {
		if !n.Local {
			t.Errorf("ListByBidder returned remote row id=%d (local=%v routing=%d)", n.ID, n.Local, n.RoutingNumber)
		}
	}
}

// TestGuard_ListByParentOffer_ExcludesRemote verifies that ListByParentOffer
// (used by cascade-cancel) only returns LOCAL chains.
func TestGuard_ListByParentOffer_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	localOfferID, _, _, _, _, _ := seedGuardFixtures(t, db)

	rows, err := r.ListByParentOffer(localOfferID)
	if err != nil {
		t.Fatalf("ListByParentOffer: %v", err)
	}
	for _, n := range rows {
		if !n.Local {
			t.Errorf("ListByParentOffer returned remote row id=%d (local=%v routing=%d)", n.ID, n.Local, n.RoutingNumber)
		}
	}
	if len(rows) == 0 {
		t.Errorf("ListByParentOffer returned nothing — local row missing")
	}
}

// TestGuard_FindChainByBidder_RemoteOnly_NotFound verifies that when only a
// remote chain matches (parent, bidder), findChainByBidder returns
// ErrRecordNotFound (the remote row must not trigger false chain-exists).
func TestGuard_FindChainByBidder_RemoteOnly_NotFound(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	localOfferID, _, _, _, _, _ := seedGuardFixtures(t, db)

	// bidder2 has only a REMOTE negotiation under localOfferID (routing 222).
	// FindChainByBidder must return ErrRecordNotFound for it.
	bidder2 := uint64(2)
	_, err := r.FindChainByBidder(localOfferID, model.OwnerClient, &bidder2)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("FindChainByBidder(remote-only bidder): want ErrRecordNotFound, got %v", err)
	}
}

// TestGuard_FindChainByBidder_LocalExists_Found verifies that when a LOCAL
// chain matches the local bidder, it is still returned correctly.
func TestGuard_FindChainByBidder_LocalExists_Found(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	localOfferID, _, localNegID, _, _, _ := seedGuardFixtures(t, db)

	bidder1 := uint64(1)
	got, err := r.FindChainByBidder(localOfferID, model.OwnerClient, &bidder1)
	if err != nil {
		t.Fatalf("FindChainByBidder(local): %v", err)
	}
	if got.ID != localNegID {
		t.Errorf("FindChainByBidder(local): got id=%d want %d", got.ID, localNegID)
	}
}

// ---------------------------------------------------------------------------
// OptionContractRepository guards
// ---------------------------------------------------------------------------

// TestGuard_ListExpiring_ExcludesRemote verifies that ListExpiring (used by
// the expiry cron on LOCAL contracts) only returns rows with routing == own.
func TestGuard_ListExpiring_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOptionContractRepository(db)
	seedGuardFixtures(t, db)

	today := time.Now().UTC().Format("2006-01-02")
	rows, err := r.ListExpiring(today, 1000)
	if err != nil {
		t.Fatalf("ListExpiring: %v", err)
	}
	for _, c := range rows {
		if !c.Local {
			t.Errorf("ListExpiring returned remote row id=%d (local=%v routing=%d)", c.ID, c.Local, c.RoutingNumber)
		}
	}
	if len(rows) == 0 {
		t.Errorf("ListExpiring returned nothing — local expired contract missing")
	}
}

// TestGuard_ListExpiringOn_ExcludesRemote verifies that ListExpiringOn (used
// by the SP5 expiring-soon warning pass) only returns contracts whose
// routing_number == OwnRouting() (111). It seeds one LOCAL ACTIVE contract
// expiring on the target day (routing 111 via BeforeCreate) and one REMOTE
// ACTIVE contract also expiring on the same day (routing 222) and asserts
// that only the local one is returned.
func TestGuard_ListExpiringOn_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	model.SetOwnRouting("111")
	r := NewOptionContractRepository(db)

	// Target day: two days from now (ensures it is not the same day as
	// "past" contracts already seeded by seedGuardFixtures, so the window
	// [day, day+1) contains ONLY these two purpose-built rows).
	targetDay := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 2)

	bidder1 := uint64(11)
	bidder2 := uint64(12)

	// LOCAL contract: BeforeCreate stamps routing_number = 111.
	localContract := &model.OptionContract{
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bidder1,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &bidder2,
		StockID:         42,
		Ticker:          "EXP",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromFloat(200),
		PremiumPaid:     decimal.NewFromFloat(10),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  targetDay,
		BuyerAccountID:  100,
		SellerAccountID: 200,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-expiring-on-local",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(localContract).Error; err != nil {
		t.Fatalf("seed local expiring-on contract: %v", err)
	}

	// REMOTE contract: explicit routing 222, same settlement day.
	remoteNativeID := "remote-expiring-on-1"
	remoteContract := &model.OptionContract{
		RoutingNumber:   222,
		NativeID:        &remoteNativeID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &bidder1,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &bidder2,
		StockID:         42,
		Ticker:          "EXP",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromFloat(200),
		PremiumPaid:     decimal.NewFromFloat(10),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  targetDay,
		BuyerAccountID:  300,
		SellerAccountID: 400,
		Status:          model.OptionContractStatusActive,
		SagaID:          "saga-expiring-on-remote",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(remoteContract).Error; err != nil {
		t.Fatalf("seed remote expiring-on contract: %v", err)
	}

	rows, err := r.ListExpiringOn(targetDay, 1000)
	if err != nil {
		t.Fatalf("ListExpiringOn: %v", err)
	}
	// Must return exactly the local contract.
	if len(rows) != 1 {
		t.Fatalf("ListExpiringOn: got %d rows want 1", len(rows))
	}
	if !rows[0].Local {
		t.Errorf("ListExpiringOn returned remote row id=%d (local=%v routing=%d)", rows[0].ID, rows[0].Local, rows[0].RoutingNumber)
	}
	if rows[0].ID != localContract.ID {
		t.Errorf("ListExpiringOn: got contract id=%d want %d (local)", rows[0].ID, localContract.ID)
	}
}

// TestGuard_ContractGetByID_RemoteRow_NotFound verifies that GetByID returns
// ErrRecordNotFound for a remote contract (routing != own).
func TestGuard_ContractGetByID_RemoteRow_NotFound(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOptionContractRepository(db)
	_, _, _, _, _, remoteContractID := seedGuardFixtures(t, db)

	_, err := r.GetByID(remoteContractID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetByID(remote contract): want ErrRecordNotFound, got %v", err)
	}
}

// TestGuard_ContractGetByID_LocalRow_Succeeds verifies that GetByID still
// works for local contracts.
func TestGuard_ContractGetByID_LocalRow_Succeeds(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOptionContractRepository(db)
	_, _, _, _, localContractID, _ := seedGuardFixtures(t, db)

	c, err := r.GetByID(localContractID)
	if err != nil {
		t.Fatalf("GetByID(local contract): %v", err)
	}
	if !c.Local {
		t.Errorf("GetByID(local contract): Local=%v want true (routing=%d)", c.Local, c.RoutingNumber)
	}
}

// TestGuard_ContractGetByOfferID_RemoteRow_NotFound verifies that
// GetByOfferID returns ErrRecordNotFound when the matched contract is remote.
func TestGuard_ContractGetByOfferID_RemoteRow_NotFound(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOptionContractRepository(db)

	// Seed a remote contract with an explicit offer_id so GetByOfferID can
	// match it. Use a fresh DB to control the offer_id cleanly.
	model.SetOwnRouting("111")
	remoteNativeID := "roc-offer-guard"
	remoteOfferID := uint64(9999)
	buyerID := uint64(77)
	sellerID := uint64(88)
	remoteContract := &model.OptionContract{
		RoutingNumber:   222,
		NativeID:        &remoteNativeID,
		OfferID:         &remoteOfferID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &buyerID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &sellerID,
		StockID:         1,
		Ticker:          "X",
		Quantity:        decimal.NewFromInt(1),
		StrikePrice:     decimal.NewFromFloat(10),
		PremiumPaid:     decimal.NewFromFloat(1),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().UTC().AddDate(1, 0, 0),
		BuyerAccountID:  1,
		SellerAccountID: 2,
		Status:          model.OptionContractStatusActive,
		SagaID:          "sg-roc",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(remoteContract).Error; err != nil {
		t.Fatalf("seed remote contract for offer: %v", err)
	}

	_, err := r.GetByOfferID(remoteOfferID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetByOfferID(remote contract): want ErrRecordNotFound, got %v", err)
	}
}

// TestGuard_ContractListByOwner_ExcludesRemote verifies that ListByOwner
// filters to routing_number == OwnRouting() and therefore excludes a REMOTE
// contract even when its buyer_owner_id deliberately collides with the local
// user's ID (the scenario the routing guard must catch that the old bank_code
// NULL check could not).
func TestGuard_ContractListByOwner_ExcludesRemote(t *testing.T) {
	db := newGuardTestDB(t)
	model.SetOwnRouting("111")
	r := NewOptionContractRepository(db)

	ownerID := uint64(7)

	// LOCAL contract: routing 111 (via BeforeCreate), buyer = client/7.
	localContract := &model.OptionContract{
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &ownerID,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &ownerID,
		StockID:         1,
		Ticker:          "LOC",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromFloat(50),
		PremiumPaid:     decimal.NewFromFloat(2),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().UTC().AddDate(1, 0, 0),
		BuyerAccountID:  1,
		SellerAccountID: 2,
		Status:          model.OptionContractStatusActive,
		SagaID:          "sg-local-7",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(localContract).Error; err != nil {
		t.Fatalf("seed local contract: %v", err)
	}

	// REMOTE contract: routing 222, buyer = client/7 (same owner — deliberate
	// collision to prove the routing guard, not the bank_code NULL check, is
	// what excludes it). BuyerBankCode is populated as a real remote row would be.
	remoteNativeID := "remote-owner-guard"
	buyerBankCode := "222"
	remoteContract := &model.OptionContract{
		RoutingNumber:   222,
		NativeID:        &remoteNativeID,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &ownerID, // same owner ID — the critical collision
		BuyerBankCode:   &buyerBankCode,
		SellerOwnerType: model.OwnerClient,
		SellerOwnerID:   &ownerID,
		StockID:         1,
		Ticker:          "REM",
		Quantity:        decimal.NewFromInt(5),
		StrikePrice:     decimal.NewFromFloat(50),
		PremiumPaid:     decimal.NewFromFloat(2),
		PremiumCurrency: "RSD",
		StrikeCurrency:  "RSD",
		SettlementDate:  time.Now().UTC().AddDate(1, 0, 0),
		BuyerAccountID:  3,
		SellerAccountID: 4,
		Status:          model.OptionContractStatusActive,
		SagaID:          "sg-remote-7",
		PremiumPaidAt:   time.Now().UTC(),
	}
	if err := db.Create(remoteContract).Error; err != nil {
		t.Fatalf("seed remote contract: %v", err)
	}

	rows, total, err := r.ListByOwner(model.OwnerClient, &ownerID, "buyer", nil, 1, 100)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if total != 1 {
		t.Errorf("ListByOwner total: got %d want 1", total)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByOwner rows: got %d want 1", len(rows))
	}
	if !rows[0].Local {
		t.Errorf("ListByOwner returned remote row id=%d (local=%v routing=%d)", rows[0].ID, rows[0].Local, rows[0].RoutingNumber)
	}
	if rows[0].ID != localContract.ID {
		t.Errorf("ListByOwner: got contract id=%d want %d (local)", rows[0].ID, localContract.ID)
	}
}

// ---------------------------------------------------------------------------
// Invariant Guard tests — `local` can NEVER diverge from routing == own.
//
// These assert the load-bearing invariant directly: across the full mixed set
// of local + remote rows seeded above, row.Local == (row.RoutingNumber ==
// OwnRouting()) for EVERY row. If a write path ever stamped the two
// inconsistently, the local/remote isolation would invert — these tests catch
// that.
// ---------------------------------------------------------------------------

// TestInvariant_OfferLocalMatchesRouting asserts the offer table's `local`
// column never disagrees with routing == own, AND that the local-discriminator
// query (ListOpenForCache) sees only Local=true rows while the remote row is
// invisible to it (and vice-versa via GetRemoteByID).
func TestInvariant_OfferLocalMatchesRouting(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCOfferRepository(db)
	localOfferID, remoteOfferID, _, _, _, _ := seedGuardFixtures(t, db)

	var all []model.OTCOffer
	if err := db.Find(&all).Error; err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no offers seeded")
	}
	sawLocal, sawRemote := false, false
	for _, o := range all {
		if o.Local != (o.RoutingNumber == model.OwnRouting()) {
			t.Errorf("offer id=%d: Local=%v but routing==own is %v (routing=%d)",
				o.ID, o.Local, o.RoutingNumber == model.OwnRouting(), o.RoutingNumber)
		}
		if o.Local {
			sawLocal = true
		} else {
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Fatalf("fixture must contain both local and remote rows (sawLocal=%v sawRemote=%v)", sawLocal, sawRemote)
	}

	// A remote row is invisible to the local-discriminator path.
	if _, err := r.LockByIDTx(db, remoteOfferID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("LockByIDTx(remote offer): want ErrRecordNotFound, got %v", err)
	}
	// A local row is invisible to the remote-discriminator path.
	if _, err := r.GetRemoteByID(localOfferID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetRemoteByID(local offer): want ErrRecordNotFound, got %v", err)
	}
}

// TestInvariant_NegotiationLocalMatchesRouting asserts the negotiation table's
// `local` column never disagrees with routing == own, and that a remote chain
// is invisible to the local lock path while a local chain is invisible to the
// remote read path.
func TestInvariant_NegotiationLocalMatchesRouting(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOTCNegotiationRepository(db)
	_, _, localNegID, remoteNegID, _, _ := seedGuardFixtures(t, db)

	var all []model.OTCNegotiation
	if err := db.Find(&all).Error; err != nil {
		t.Fatalf("list negotiations: %v", err)
	}
	sawLocal, sawRemote := false, false
	for _, n := range all {
		if n.Local != (n.RoutingNumber == model.OwnRouting()) {
			t.Errorf("neg id=%d: Local=%v but routing==own is %v (routing=%d)",
				n.ID, n.Local, n.RoutingNumber == model.OwnRouting(), n.RoutingNumber)
		}
		if n.Local {
			sawLocal = true
		} else {
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Fatalf("fixture must contain both local and remote negotiations (sawLocal=%v sawRemote=%v)", sawLocal, sawRemote)
	}

	// Remote chain invisible to the local lock path.
	err := db.Transaction(func(tx *gorm.DB) error {
		_, e := r.LockByID(tx, remoteNegID)
		return e
	})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("LockByID(remote neg): want ErrRecordNotFound, got %v", err)
	}
	// Local chain invisible to the remote read path.
	if _, err := r.GetRemoteNegByID(localNegID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetRemoteNegByID(local neg): want ErrRecordNotFound, got %v", err)
	}
}

// TestInvariant_ContractLocalMatchesRouting asserts the contract table's
// `local` column never disagrees with routing == own, and that a remote
// contract is invisible to the local GetByID path while a local contract is
// invisible to the remote read path.
func TestInvariant_ContractLocalMatchesRouting(t *testing.T) {
	db := newGuardTestDB(t)
	r := NewOptionContractRepository(db)
	_, _, _, _, localContractID, remoteContractID := seedGuardFixtures(t, db)

	var all []model.OptionContract
	if err := db.Find(&all).Error; err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	sawLocal, sawRemote := false, false
	for _, c := range all {
		if c.Local != (c.RoutingNumber == model.OwnRouting()) {
			t.Errorf("contract id=%d: Local=%v but routing==own is %v (routing=%d)",
				c.ID, c.Local, c.RoutingNumber == model.OwnRouting(), c.RoutingNumber)
		}
		if c.Local {
			sawLocal = true
		} else {
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Fatalf("fixture must contain both local and remote contracts (sawLocal=%v sawRemote=%v)", sawLocal, sawRemote)
	}

	// Remote contract invisible to the local read path.
	if _, err := r.GetByID(remoteContractID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetByID(remote contract): want ErrRecordNotFound, got %v", err)
	}
	// Local contract invisible to the remote read path.
	if _, err := r.GetRemoteContractByID(localContractID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("GetRemoteContractByID(local contract): want ErrRecordNotFound, got %v", err)
	}
}
