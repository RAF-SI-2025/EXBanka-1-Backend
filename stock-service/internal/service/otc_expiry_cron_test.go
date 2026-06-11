package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

func newOTCExpiryDB(t *testing.T) *gorm.DB {
	t.Helper()
	// SP-2a: remote (cross-bank) contracts live in the unified option_contracts
	// table as routing_number != OwnRouting() rows. Set own routing so the
	// remote-scoped repo methods recognise the seeded peer rows (routing 222).
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
		&model.OTCOffer{},
		&model.OTCOfferRevision{},
		&model.OptionContract{},
		&model.CapitalGain{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOTCExpiryCron_NewDefaultsBatchAndCron(t *testing.T) {
	cr := NewOTCExpiryCron(nil, nil, nil, 0, "", nilRegistry())
	if cr.batchSize != 500 {
		t.Errorf("batchSize=%d", cr.batchSize)
	}
	if cr.cronUTC != "02:00" {
		t.Errorf("cron=%q", cr.cronUTC)
	}
}

// expireContract marks the contract expired without a holdingRes wired.
func TestOTCExpiryCron_ExpireContract_NoHoldingRes(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 10, "02:00", nilRegistry())

	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ := contractRepo.GetByID(c.ID)
	if got.Status != model.OptionContractStatusExpired {
		t.Errorf("status=%s", got.Status)
	}
	if got.ExpiredAt == nil {
		t.Error("expected expired_at to be set")
	}
}

// TestOTCExpiryCron_RunOnce_DoesNotExpireOffers proves offers are termless
// "inventory": an old-style offer with a PAST settlement_date is NOT expired by
// the cron. Offers now end only via cancel/accept — the offer-expiry pass was
// removed. (settlement_date still exists on the model at this point.)
func TestOTCExpiryCron_RunOnce_DoesNotExpireOffers(t *testing.T) {
	db := newOTCExpiryDB(t)
	offerRepo := repository.NewOTCOfferRepository(db)
	cr := NewOTCExpiryCron(repository.NewOptionContractRepository(db), nil, nil, 10, "02:00", nilRegistry())

	uid := uint64(7)
	o := &model.OTCOffer{
		InitiatorOwnerType: model.OwnerClient, InitiatorOwnerID: &uid,
		Direction: model.OTCDirectionSellInitiated, StockID: 42,
		Quantity: decimal.NewFromInt(10), Status: model.OTCOfferStatusPending,
		LastModifiedByPrincipalType: "client", LastModifiedByPrincipalID: 7,
	}
	if err := offerRepo.Create(o); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := offerRepo.GetByID(o.ID)
	if got.Status != model.OTCOfferStatusPending {
		t.Fatalf("offer must NOT auto-expire (status=%s, want %s)", got.Status, model.OTCOfferStatusPending)
	}
}

// TestOTCExpiryCron_RunOnce_ExpiresContractReleasesReservation proves contract
// expiry is fully intact through RunOnce: an active contract past its settlement
// date is flipped to expired AND the seller's underlying-share reservation is
// released. This is the behavior that MUST survive the offer-expiry removal.
func TestOTCExpiryCron_RunOnce_ExpiresContractReleasesReservation(t *testing.T) {
	db := newOTCExpiryDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	// HoldingReservationService uses FOR UPDATE transactions; pin to a single
	// connection so the sqlite :memory: DB is shared across them.
	sqlDB.SetMaxOpenConns(1)

	contractRepo := repository.NewOptionContractRepository(db)
	holdingRepo := repository.NewHoldingRepository(db)
	resRepo := repository.NewHoldingReservationRepository(db)
	holdingRes := NewHoldingReservationService(db, holdingRepo, resRepo)

	sellerUID := uint64(1)
	h := &model.Holding{
		OwnerType: model.OwnerClient, OwnerID: &sellerUID,
		UserFirstName: "Sell", UserLastName: "Er",
		SecurityType: "stock", SecurityID: 42, ListingID: 100,
		Ticker: "AAPL", Name: "Apple", Quantity: 100,
		AveragePrice: decimal.NewFromInt(50), ReservedQuantity: 0,
		AccountID: 1, Version: 1,
	}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("seed holding: %v", err)
	}

	buyerUID := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	// Seller reserves the underlying shares for the contract.
	if _, err := holdingRes.ReserveForOTCContract(context.Background(),
		model.OwnerClient, &sellerUID, "stock", 42, c.ID, 10); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	cr := NewOTCExpiryCron(contractRepo, holdingRes, nil, 10, "02:00", nilRegistry())
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, _ := contractRepo.GetByID(c.ID)
	if got.Status != model.OptionContractStatusExpired {
		t.Fatalf("contract must expire (status=%s)", got.Status)
	}
	if got.ExpiredAt == nil {
		t.Error("expected expired_at to be set")
	}
	gotH, _ := holdingRepo.GetByID(h.ID)
	if gotH.ReservedQuantity != 0 {
		t.Fatalf("seller reservation must be released (reserved=%d)", gotH.ReservedQuantity)
	}
}

// TestOTCExpiryCron_BooksBuyerPremiumLoss verifies the resolution-month model:
// when an OTC contract expires unexercised, the buyer's lost premium is booked
// as a capital loss (-premium) in the expiry month, idempotently (a re-run does
// not double-book). The seller keeps the premium (already taxed at accept).
// Spec §3.1, §4 C3.
func TestOTCExpiryCron_BooksBuyerPremiumLoss(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cgRepo := repository.NewCapitalGainRepository(db)
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 10, "02:00", nilRegistry()).
		WithCapitalGains(cgRepo)

	uid := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(1150),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &uid,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		BuyerAccountID: 11, SellerAccountID: 12, PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	// Re-running must not duplicate the loss row (idempotent by contract key).
	c.Status = model.OptionContractStatusActive
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire (re-run): %v", err)
	}

	var rows []model.CapitalGain
	db.Where("owner_type = ? AND security_type = ?", model.OwnerClient, "option").Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 buyer loss row (idempotent), got %d", len(rows))
	}
	if !rows[0].TotalGain.Equal(decimal.NewFromInt(-1150)) {
		t.Fatalf("buyer loss = %s, want -1150", rows[0].TotalGain)
	}
	if rows[0].SecurityType != "option" || !rows[0].OTC {
		t.Fatalf("loss row must be option/OTC, got type=%s otc=%v", rows[0].SecurityType, rows[0].OTC)
	}
}

// TestOTCExpiryCron_WarnsExpiringSoon verifies SP5 E: a contract settling
// exactly warnDays out triggers an OTC_CONTRACT_EXPIRING_SOON notification to
// both client parties, without expiring the (still-future) contract.
func TestOTCExpiryCron_WarnsExpiringSoon(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 100, "02:00", nilRegistry()).
		WithExpiryWarning(3)
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	buyer := uint64(7)
	seller := uint64(8)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(50),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, 3).Truncate(24 * time.Hour),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyer,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &seller,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Contract must NOT be expired (it settles in the future).
	got, _ := contractRepo.GetByID(c.ID)
	if got.Status != model.OptionContractStatusActive {
		t.Fatalf("contract should remain active, got %s", got.Status)
	}
	var warns int
	for _, n := range notifier.notifs {
		if n.Type == "OTC_CONTRACT_EXPIRING_SOON" {
			warns++
		}
	}
	if warns != 2 {
		t.Fatalf("expected 2 expiring-soon warnings (buyer+seller), got %d", warns)
	}
}

// expireContract emits OTC_CONTRACT_EXPIRED to both client parties; a bank
// party is skipped.
func TestOTCExpiryCron_ExpireContract_EmitsNotifications(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	buyerUID := uint64(7)
	sellerUID := uint64(8)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerClient, SellerOwnerID: &sellerUID,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_CONTRACT_EXPIRED"); got != 2 {
		t.Fatalf("OTC_CONTRACT_EXPIRED count = %d, want 2 (buyer + seller)", got)
	}
	sawBuyer, sawSeller := false, false
	for _, m := range notifier.notifs {
		if m.RefType != "otc_contract" || m.RefID != c.ID {
			t.Errorf("notif ref = %s/%d, want otc_contract/%d", m.RefType, m.RefID, c.ID)
		}
		if m.Data["ticker"] != "AAPL" {
			t.Errorf("notif ticker = %q, want AAPL", m.Data["ticker"])
		}
		if m.UserID == buyerUID {
			sawBuyer = true
		}
		if m.UserID == sellerUID {
			sawSeller = true
		}
	}
	if !sawBuyer || !sawSeller {
		t.Errorf("expected notifications to both buyer (%d) and seller (%d); sawBuyer=%v sawSeller=%v", buyerUID, sellerUID, sawBuyer, sawSeller)
	}
}

// expireContract against a bank seller notifies only the client buyer.
func TestOTCExpiryCron_ExpireContract_BankParty_Skipped(t *testing.T) {
	db := newOTCExpiryDB(t)
	contractRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(contractRepo, nil, nil, 10, "02:00", nilRegistry())
	notifier := &recordingOTCNotifier{}
	cr.notifier = notifier

	buyerUID := uint64(7)
	c := &model.OptionContract{
		StockID: 42, Ticker: "AAPL", Quantity: decimal.NewFromInt(10),
		StrikePrice: decimal.NewFromInt(150), PremiumPaid: decimal.NewFromInt(20),
		PremiumCurrency: "USD", StrikeCurrency: "USD",
		SettlementDate: time.Now().UTC().AddDate(0, 0, -2),
		Status:         model.OptionContractStatusActive,
		BuyerOwnerType: model.OwnerClient, BuyerOwnerID: &buyerUID,
		SellerOwnerType: model.OwnerBank, SellerOwnerID: nil,
		PremiumPaidAt: time.Now(),
	}
	if err := contractRepo.Create(c); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if err := cr.expireContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if got := countNotifs(notifier.notifs, "OTC_CONTRACT_EXPIRED"); got != 1 {
		t.Fatalf("OTC_CONTRACT_EXPIRED count = %d, want 1 (buyer only; bank seller skipped)", got)
	}
	for _, m := range notifier.notifs {
		if m.UserID != buyerUID {
			t.Errorf("unexpected notification to UserID=%d (want only buyer %d)", m.UserID, buyerUID)
		}
	}
}

// otcNextRunAt: same day vs next day handling.
func TestOTCNextRunAt_FutureToday(t *testing.T) {
	now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "02:00")
	if got.Hour() != 2 || got.Minute() != 0 {
		t.Errorf("got %v", got)
	}
	if got.Day() != 1 {
		t.Errorf("expected same day, got %v", got)
	}
}

func TestOTCNextRunAt_PastTodayWrapsTomorrow(t *testing.T) {
	now := time.Date(2026, 5, 1, 5, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "02:00")
	if got.Day() != 2 {
		t.Errorf("expected next day, got %v", got)
	}
}

func TestOTCNextRunAt_BadFormatFallsBackTo0200(t *testing.T) {
	now := time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)
	got := otcNextRunAt(now, "garbage")
	if got.Hour() != 2 || got.Minute() != 0 {
		t.Errorf("got %v want 02:00", got)
	}
}

// Start triggers RunOnce immediately + spawns a goroutine; cancel ctx
// to make it return promptly.
func TestOTCExpiryCron_StartAndCancel(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cr.Start(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

// RunOnce should be a no-op when nothing is expiring.
func TestOTCExpiryCron_RunOnce_NothingToExpire(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	if err := cr.RunOnce(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// TestOTCExpiryCron_WithOutbox covers the WithOutbox wiring helper.
func TestOTCExpiryCron_WithOutbox(t *testing.T) {
	cr := NewOTCExpiryCron(nil, nil, nil, 10, "02:00", nilRegistry())
	out := cr.WithOutbox(nil, nil)
	if out == nil {
		t.Error("WithOutbox returned nil")
	}
}

// WithPeerContracts wires the optional peer-contracts repo + verifies
// RunOnce still completes without error when peer rows are absent.
func TestOTCExpiryCron_WithPeerContracts(t *testing.T) {
	db := newOTCExpiryDB(t)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	)
	cr2 := cr.WithPeerContracts(repository.NewOptionContractRepository(db))
	if cr2.peerContracts == nil {
		t.Fatal("expected peer repo wired")
	}
	if err := cr2.RunOnce(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// expirePeerContract on a CREDIT-direction row: just sets status, does NOT
// touch holdings (because the buyer's bank holds no lock).
func TestOTCExpiryCron_ExpirePeerContract_CreditDirection(t *testing.T) {
	db := newOTCExpiryDB(t)
	peerRepo := repository.NewOptionContractRepository(db)
	cr := NewOTCExpiryCron(
		repository.NewOptionContractRepository(db),
		nil, nil, 10, "02:00", nilRegistry(),
	).WithPeerContracts(peerRepo)

	// REMOTE CREDIT contract: we host the buyer (111); the seller's bank (222)
	// is the counterparty, so routing_number=222 (!= own 111).
	c := seedExpiryRemoteContract(t, db, "CREDIT", 222)
	if err := cr.expirePeerContract(context.Background(), c); err != nil {
		t.Fatalf("expire: %v", err)
	}
	got, _ := peerRepo.GetRemoteContractByID(c.ID)
	if got.Status != "expired" {
		t.Errorf("status=%s", got.Status)
	}
}

// seedExpiryRemoteContract inserts a REMOTE option contract (SP-2a fold) into
// the unified table for the expiry-cron tests. counterparty is the routing of
// the bank we do NOT host (so the row is recognised as remote).
func seedExpiryRemoteContract(t *testing.T, db *gorm.DB, direction string, counterparty int64) *model.OptionContract {
	t.Helper()
	native := "tx-1:0"
	dir := direction
	bbc, sbc := "111", "222"
	if direction == "DEBIT" {
		// We host the seller (111); buyer's bank (counterparty) hosts the buyer.
		bbc = "222"
		sbc = "111"
	}
	pIdx := int32(0)
	cbTx := "tx-1"
	negRouting := int64(111)
	negNative := "neg-1"
	bID, sID := "client-1", "client-9"
	now := time.Now().UTC()
	row := &model.OptionContract{
		RoutingNumber:             counterparty,
		NativeID:                  &native,
		BuyerOwnerType:            model.OwnerBank,
		BuyerBankCode:             &bbc,
		SellerOwnerType:           model.OwnerBank,
		SellerBankCode:            &sbc,
		Ticker:                    "AAPL",
		Quantity:                  decimal.NewFromInt(10),
		StrikePrice:               decimal.NewFromInt(100),
		PremiumPaid:               decimal.Zero,
		PremiumCurrency:           "USD",
		StrikeCurrency:            "USD",
		SettlementDate:            time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:                    "active",
		SagaID:                    cbTx,
		PremiumPaidAt:             now,
		CrossbankTxID:             &cbTx,
		RemotePostingIndex:        &pIdx,
		RemoteNegotiationRouting:  &negRouting,
		RemoteNegotiationNativeID: &negNative,
		RemoteDirection:           &dir,
		RemoteBuyerID:             &bID,
		RemoteSellerID:            &sID,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed remote contract: %v", err)
	}
	return row
}
