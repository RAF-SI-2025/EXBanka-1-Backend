package model

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// DividendPayment.BeforeCreate
// ---------------------------------------------------------------------------

func TestDividendPayment_BeforeCreate_SetsDefaultStatus(t *testing.T) {
	d := &DividendPayment{}
	if err := d.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != "declared" {
		t.Errorf("got status %q, want declared", d.Status)
	}
}

func TestDividendPayment_BeforeCreate_PreserveExistingStatus(t *testing.T) {
	d := &DividendPayment{Status: "paid_out"}
	if err := d.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != "paid_out" {
		t.Errorf("got status %q, want paid_out", d.Status)
	}
}

// ---------------------------------------------------------------------------
// TableName helpers
// ---------------------------------------------------------------------------

func TestFundPositionSettlement_TableName(t *testing.T) {
	if got := (FundPositionSettlement{}).TableName(); got != "fund_position_settlements" {
		t.Errorf("got %q", got)
	}
}

func TestFundValueSnapshot_TableName(t *testing.T) {
	if got := (FundValueSnapshot{}).TableName(); got != "fund_value_snapshots" {
		t.Errorf("got %q", got)
	}
}

func TestHoldingCreditMarker_TableName(t *testing.T) {
	if got := (HoldingCreditMarker{}).TableName(); got != "holding_credit_markers" {
		t.Errorf("got %q", got)
	}
}

func TestOTCNegotiation_TableName(t *testing.T) {
	if got := (OTCNegotiation{}).TableName(); got != "otc_negotiations" {
		t.Errorf("got %q", got)
	}
}

func TestOTCNegotiationRevision_TableName(t *testing.T) {
	if got := (OTCNegotiationRevision{}).TableName(); got != "otc_negotiation_revisions" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiation.BeforeUpdate
// ---------------------------------------------------------------------------

func TestOTCNegotiation_BeforeUpdate_NilTx(t *testing.T) {
	n := &OTCNegotiation{Version: 3}
	if err := n.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if n.Version != 4 {
		t.Errorf("got version %d, want 4", n.Version)
	}
}

func TestOTCNegotiation_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	n := &OTCNegotiation{Version: 1}
	if err := n.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if n.Version != 2 {
		t.Errorf("got version %d, want 2", n.Version)
	}
}

// ---------------------------------------------------------------------------
// OTCNegotiation.IsTerminal
// ---------------------------------------------------------------------------

func TestOTCNegotiation_IsTerminal_TerminalStatuses(t *testing.T) {
	for _, s := range []string{
		OTCNegotiationStatusAccepted,
		OTCNegotiationStatusRejected,
		OTCNegotiationStatusCancelled,
		OTCNegotiationStatusExpired,
	} {
		n := &OTCNegotiation{Status: s}
		if !n.IsTerminal() {
			t.Errorf("status %q should be terminal", s)
		}
	}
}

func TestOTCNegotiation_IsTerminal_NonTerminalStatuses(t *testing.T) {
	for _, s := range []string{
		OTCNegotiationStatusOpen,
		OTCNegotiationStatusCountered,
	} {
		n := &OTCNegotiation{Status: s}
		if n.IsTerminal() {
			t.Errorf("status %q should not be terminal", s)
		}
	}
}

// ---------------------------------------------------------------------------
// OTCOffer.IsOpenListing
// ---------------------------------------------------------------------------

func TestOTCOffer_IsOpenListing_OpenStatuses(t *testing.T) {
	for _, s := range []string{
		OTCOfferStatusOpen,
		OTCOfferStatusPending,
		OTCOfferStatusCountered,
	} {
		o := &OTCOffer{Status: s}
		if !o.IsOpenListing() {
			t.Errorf("status %q should be open listing", s)
		}
	}
}

func TestOTCOffer_IsOpenListing_TerminalStatuses(t *testing.T) {
	for _, s := range []string{
		OTCOfferStatusAccepted,
		OTCOfferStatusRejected,
		OTCOfferStatusExpired,
		OTCOfferStatusFailed,
		OTCOfferStatusConsumed,
		OTCOfferStatusCancelled,
	} {
		o := &OTCOffer{Status: s}
		if o.IsOpenListing() {
			t.Errorf("status %q should not be open listing", s)
		}
	}
}

// ---------------------------------------------------------------------------
// OTCTraderRating.BeforeSave
// ---------------------------------------------------------------------------

func TestOTCTraderRating_BeforeSave_Valid(t *testing.T) {
	raterID := uint64(1)
	ratedID := uint64(2)
	r := &OTCTraderRating{
		Score:          3,
		RaterOwnerType: OwnerClient,
		RaterOwnerID:   &raterID,
		RatedOwnerType: OwnerClient,
		RatedOwnerID:   &ratedID,
	}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestOTCTraderRating_BeforeSave_ScoreTooLow(t *testing.T) {
	r := &OTCTraderRating{Score: 0}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for score < 1")
	}
}

func TestOTCTraderRating_BeforeSave_ScoreTooHigh(t *testing.T) {
	r := &OTCTraderRating{Score: 6}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for score > 5")
	}
}

func TestOTCTraderRating_BeforeSave_InvalidRater(t *testing.T) {
	// client rater with nil id → ValidateOwner returns error
	r := &OTCTraderRating{Score: 3, RaterOwnerType: OwnerClient, RaterOwnerID: nil}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid rater")
	}
}

func TestOTCTraderRating_BeforeSave_InvalidRated(t *testing.T) {
	raterID := uint64(1)
	r := &OTCTraderRating{
		Score:          3,
		RaterOwnerType: OwnerClient,
		RaterOwnerID:   &raterID,
		RatedOwnerType: OwnerClient,
		RatedOwnerID:   nil, // invalid
	}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid rated")
	}
}

func TestOTCTraderRating_BeforeSave_CreatedAtPreserved(t *testing.T) {
	raterID := uint64(1)
	ratedID := uint64(2)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := &OTCTraderRating{
		Score:          5,
		RaterOwnerType: OwnerClient,
		RaterOwnerID:   &raterID,
		RatedOwnerType: OwnerClient,
		RatedOwnerID:   &ratedID,
		CreatedAt:      ts,
	}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !r.CreatedAt.Equal(ts) {
		t.Errorf("CreatedAt was overwritten: got %v", r.CreatedAt)
	}
}

// ---------------------------------------------------------------------------
// PriceAlert.BeforeUpdate  and  Valid
// ---------------------------------------------------------------------------

func TestPriceAlertCondition_Valid_AllValidValues(t *testing.T) {
	for _, c := range []PriceAlertCondition{
		PriceAlertConditionGTE,
		PriceAlertConditionLTE,
		PriceAlertConditionDailyChangePctGTE,
		PriceAlertConditionDailyChangePctLTE,
	} {
		if !c.Valid() {
			t.Errorf("condition %q should be valid", c)
		}
	}
}

func TestPriceAlertCondition_Valid_Invalid(t *testing.T) {
	if PriceAlertCondition("bogus").Valid() {
		t.Error("expected false for unknown condition")
	}
}

func TestPriceAlert_BeforeSave_InvalidCondition(t *testing.T) {
	id := uint64(1)
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   &id,
		Condition: PriceAlertCondition("not_a_real_condition"),
		Cooldown:  3600,
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid condition")
	}
}

func TestPriceAlert_BeforeSave_CooldownTooLow(t *testing.T) {
	id := uint64(1)
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   &id,
		Condition: PriceAlertConditionGTE,
		Threshold: decimal.NewFromInt(100),
		Cooldown:  59, // below 60
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for cooldown < 60")
	}
}

func TestPriceAlert_BeforeSave_CooldownTooHigh(t *testing.T) {
	id := uint64(1)
	a := &PriceAlert{
		OwnerType: OwnerClient,
		OwnerID:   &id,
		Condition: PriceAlertConditionGTE,
		Threshold: decimal.NewFromInt(100),
		Cooldown:  86401, // above 86400
	}
	if err := a.BeforeSave(nil); err == nil {
		t.Fatal("expected error for cooldown > 86400")
	}
}

func TestPriceAlert_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	a := &PriceAlert{Version: 5}
	if err := a.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.Version != 6 {
		t.Errorf("got version %d, want 6", a.Version)
	}
}

func TestPriceAlert_BeforeUpdate_NilTx(t *testing.T) {
	a := &PriceAlert{Version: 0}
	if err := a.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if a.Version != 1 {
		t.Errorf("got version %d, want 1", a.Version)
	}
}

// ---------------------------------------------------------------------------
// RecurringFundInvestment — BeforeSave, BeforeUpdate, AdvanceNextRun
// ---------------------------------------------------------------------------

func TestRecurringFundInvestment_BeforeSave_Valid(t *testing.T) {
	r := &RecurringFundInvestment{
		AmountRSD:  decimal.NewFromInt(1000),
		DayOfMonth: 15,
	}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecurringFundInvestment_BeforeSave_NonPositiveAmount(t *testing.T) {
	r := &RecurringFundInvestment{AmountRSD: decimal.Zero, DayOfMonth: 5}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestRecurringFundInvestment_BeforeSave_NegativeAmount(t *testing.T) {
	r := &RecurringFundInvestment{AmountRSD: decimal.NewFromInt(-1), DayOfMonth: 5}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestRecurringFundInvestment_BeforeSave_DayOfMonthTooLow(t *testing.T) {
	r := &RecurringFundInvestment{AmountRSD: decimal.NewFromInt(100), DayOfMonth: 0}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_month < 1")
	}
}

func TestRecurringFundInvestment_BeforeSave_DayOfMonthTooHigh(t *testing.T) {
	r := &RecurringFundInvestment{AmountRSD: decimal.NewFromInt(100), DayOfMonth: 29}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_month > 28")
	}
}

func TestRecurringFundInvestment_BeforeUpdate_NilTx(t *testing.T) {
	r := &RecurringFundInvestment{Version: 2}
	if err := r.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Version != 3 {
		t.Errorf("got version %d, want 3", r.Version)
	}
}

func TestRecurringFundInvestment_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	r := &RecurringFundInvestment{Version: 0}
	if err := r.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Version != 1 {
		t.Errorf("got version %d, want 1", r.Version)
	}
}

func TestRecurringFundInvestment_AdvanceNextRun(t *testing.T) {
	r := &RecurringFundInvestment{DayOfMonth: 15}
	from := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	got := r.AdvanceNextRun(from)
	want := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// RecurringOrder — BeforeSave, BeforeUpdate, AdvanceNextRun
// ---------------------------------------------------------------------------

func TestRecurringOrder_BeforeSave_ValidWeekly(t *testing.T) {
	day := 1
	id := uint64(1)
	r := &RecurringOrder{
		OwnerType: OwnerClient,
		OwnerID:   &id,
		Side:      "buy",
		Quantity:  10,
		Interval:  RecurrenceWeekly,
		DayOfWeek: &day,
	}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecurringOrder_BeforeSave_ValidMonthly(t *testing.T) {
	day := 15
	id := uint64(1)
	r := &RecurringOrder{
		OwnerType:  OwnerClient,
		OwnerID:    &id,
		Side:       "sell",
		Quantity:   5,
		Interval:   RecurrenceMonthly,
		DayOfMonth: &day,
	}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecurringOrder_BeforeSave_InvalidSide(t *testing.T) {
	r := &RecurringOrder{Side: "hold", Quantity: 1}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid side")
	}
}

func TestRecurringOrder_BeforeSave_ZeroQuantity(t *testing.T) {
	r := &RecurringOrder{Side: "buy", Quantity: 0}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for quantity <= 0")
	}
}

func TestRecurringOrder_BeforeSave_WeeklyNilDayOfWeek(t *testing.T) {
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceWeekly, DayOfWeek: nil}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for weekly without day_of_week")
	}
}

func TestRecurringOrder_BeforeSave_WeeklyDayOfWeekOutOfRange(t *testing.T) {
	day := 7
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceWeekly, DayOfWeek: &day}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_week > 6")
	}
}

func TestRecurringOrder_BeforeSave_WeeklyDayOfWeekNegative(t *testing.T) {
	day := -1
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceWeekly, DayOfWeek: &day}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_week < 0")
	}
}

func TestRecurringOrder_BeforeSave_MonthlyNilDayOfMonth(t *testing.T) {
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceMonthly, DayOfMonth: nil}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for monthly without day_of_month")
	}
}

func TestRecurringOrder_BeforeSave_MonthlyDayOfMonthOutOfRange(t *testing.T) {
	day := 29
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceMonthly, DayOfMonth: &day}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_month > 28")
	}
}

func TestRecurringOrder_BeforeSave_MonthlyDayOfMonthTooLow(t *testing.T) {
	day := 0
	r := &RecurringOrder{Side: "buy", Quantity: 1, Interval: RecurrenceMonthly, DayOfMonth: &day}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for day_of_month < 1")
	}
}

func TestRecurringOrder_BeforeSave_InvalidInterval(t *testing.T) {
	id := uint64(1)
	r := &RecurringOrder{
		OwnerType: OwnerClient,
		OwnerID:   &id,
		Side:      "buy",
		Quantity:  1,
		Interval:  RecurrenceInterval("daily"),
	}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

func TestRecurringOrder_BeforeSave_InvalidOwner(t *testing.T) {
	// client with nil OwnerID → ValidateOwner error
	day := 1
	r := &RecurringOrder{
		OwnerType: OwnerClient,
		OwnerID:   nil,
		Side:      "buy",
		Quantity:  1,
		Interval:  RecurrenceWeekly,
		DayOfWeek: &day,
	}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid owner")
	}
}

func TestRecurringOrder_BeforeUpdate_NilTx(t *testing.T) {
	r := &RecurringOrder{Version: 4}
	if err := r.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Version != 5 {
		t.Errorf("got version %d, want 5", r.Version)
	}
}

func TestRecurringOrder_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	r := &RecurringOrder{Version: 1}
	if err := r.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Version != 2 {
		t.Errorf("got version %d, want 2", r.Version)
	}
}

func TestRecurringOrder_AdvanceNextRun_Weekly(t *testing.T) {
	day := 1 // Monday
	r := &RecurringOrder{Interval: RecurrenceWeekly, DayOfWeek: &day}
	// Wednesday = weekday 3; next Monday is in 5 days
	from := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC) // Wednesday
	got := r.AdvanceNextRun(from)
	// Next Monday = 2026-06-15
	want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRecurringOrder_AdvanceNextRun_WeeklySameDay(t *testing.T) {
	day := 3 // Wednesday
	r := &RecurringOrder{Interval: RecurrenceWeekly, DayOfWeek: &day}
	// If today is Wednesday, next run is next Wednesday (delta=7)
	from := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC) // Wednesday
	got := r.AdvanceNextRun(from)
	want := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRecurringOrder_AdvanceNextRun_Monthly(t *testing.T) {
	day := 5
	r := &RecurringOrder{Interval: RecurrenceMonthly, DayOfMonth: &day}
	from := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	got := r.AdvanceNextRun(from)
	want := time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Watchlist.BeforeSave
// ---------------------------------------------------------------------------

func TestWatchlist_BeforeSave_ValidClient(t *testing.T) {
	id := uint64(1)
	w := &Watchlist{OwnerType: OwnerClient, OwnerID: &id}
	if err := w.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWatchlist_BeforeSave_ValidBank(t *testing.T) {
	w := &Watchlist{OwnerType: OwnerBank}
	if err := w.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWatchlist_BeforeSave_ClientWithoutID(t *testing.T) {
	w := &Watchlist{OwnerType: OwnerClient, OwnerID: nil}
	if err := w.BeforeSave(nil); err == nil {
		t.Fatal("expected error for client without owner_id")
	}
}

func TestWatchlist_BeforeSave_InvalidOwnerType(t *testing.T) {
	w := &Watchlist{OwnerType: OwnerType("unknown")}
	if err := w.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid owner_type")
	}
}

// ---------------------------------------------------------------------------
// WatchlistItem.BeforeSave
// ---------------------------------------------------------------------------

func TestWatchlistItem_BeforeSave_SetsAddedAt(t *testing.T) {
	id := uint64(1)
	wi := &WatchlistItem{OwnerType: OwnerClient, OwnerID: &id}
	if err := wi.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wi.AddedAt.IsZero() {
		t.Error("AddedAt should be set")
	}
}

func TestWatchlistItem_BeforeSave_PreservesAddedAt(t *testing.T) {
	id := uint64(1)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	wi := &WatchlistItem{OwnerType: OwnerClient, OwnerID: &id, AddedAt: ts}
	if err := wi.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wi.AddedAt.Equal(ts) {
		t.Errorf("AddedAt overwritten: got %v", wi.AddedAt)
	}
}

func TestWatchlistItem_BeforeSave_InvalidOwner(t *testing.T) {
	wi := &WatchlistItem{OwnerType: OwnerClient, OwnerID: nil}
	if err := wi.BeforeSave(nil); err == nil {
		t.Fatal("expected error for invalid owner")
	}
}

// ---------------------------------------------------------------------------
// InvestmentFund.BeforeSave — all branches
// ---------------------------------------------------------------------------

func TestInvestmentFund_BeforeSave_OpenFundValid(t *testing.T) {
	f := &InvestmentFund{FundType: FundTypeOpen}
	if err := f.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvestmentFund_BeforeSave_EmptyFundTypeDefaults(t *testing.T) {
	f := &InvestmentFund{}
	if err := f.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.FundType != FundTypeOpen {
		t.Errorf("got fund_type %q, want open", f.FundType)
	}
	if f.FundStatus != FundStatusOpen {
		t.Errorf("got fund_status %q, want open", f.FundStatus)
	}
}

func TestInvestmentFund_BeforeSave_OpenFundWithDateField_Error(t *testing.T) {
	ts := time.Now()
	f := &InvestmentFund{FundType: FundTypeOpen, FundraisingStart: &ts}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for open fund with fundraising_start")
	}
}

func TestInvestmentFund_BeforeSave_ClosedFundMissingDates_Error(t *testing.T) {
	f := &InvestmentFund{FundType: FundTypeClosed, TargetAmountRSD: decimal.NewFromInt(1000)}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for closed fund missing dates")
	}
}

func TestInvestmentFund_BeforeSave_ClosedFundStartAfterEnd_Error(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // before start
	maturity := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f := &InvestmentFund{
		FundType:         FundTypeClosed,
		FundraisingStart: &start,
		FundraisingEnd:   &end,
		MaturityDate:     &maturity,
		TargetAmountRSD:  decimal.NewFromInt(1000),
	}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for start >= end")
	}
}

func TestInvestmentFund_BeforeSave_ClosedFundEndAfterMaturity_Error(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	maturity := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // before end
	f := &InvestmentFund{
		FundType:         FundTypeClosed,
		FundraisingStart: &start,
		FundraisingEnd:   &end,
		MaturityDate:     &maturity,
		TargetAmountRSD:  decimal.NewFromInt(1000),
	}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for end >= maturity")
	}
}

func TestInvestmentFund_BeforeSave_ClosedFundZeroTarget_Error(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	maturity := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f := &InvestmentFund{
		FundType:         FundTypeClosed,
		FundraisingStart: &start,
		FundraisingEnd:   &end,
		MaturityDate:     &maturity,
		TargetAmountRSD:  decimal.Zero,
	}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for zero target_amount_rsd")
	}
}

func TestInvestmentFund_BeforeSave_ClosedFundValid(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	maturity := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f := &InvestmentFund{
		FundType:         FundTypeClosed,
		FundraisingStart: &start,
		FundraisingEnd:   &end,
		MaturityDate:     &maturity,
		TargetAmountRSD:  decimal.NewFromInt(100000),
	}
	if err := f.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvestmentFund_BeforeSave_UnknownFundType_Error(t *testing.T) {
	f := &InvestmentFund{FundType: FundType("unknown")}
	if err := f.BeforeSave(nil); err == nil {
		t.Fatal("expected error for unknown fund_type")
	}
}

func TestInvestmentFund_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	f := &InvestmentFund{Version: 3}
	if err := f.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.Version != 4 {
		t.Errorf("got version %d, want 4", f.Version)
	}
}

// ---------------------------------------------------------------------------
// ClientFundPosition.BeforeUpdate with tx
// ---------------------------------------------------------------------------

func TestClientFundPosition_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	p := &ClientFundPosition{Version: 1}
	if err := p.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Version != 2 {
		t.Errorf("got version %d, want 2", p.Version)
	}
}

func TestClientFundPosition_BeforeSave_ClientWithID(t *testing.T) {
	id := uint64(1)
	p := &ClientFundPosition{OwnerType: OwnerClient, OwnerID: &id}
	if err := p.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientFundPosition_BeforeSave_ClientWithoutID(t *testing.T) {
	p := &ClientFundPosition{OwnerType: OwnerClient, OwnerID: nil}
	if err := p.BeforeSave(nil); err == nil {
		t.Fatal("expected error for client without owner_id")
	}
}

// ---------------------------------------------------------------------------
// FundHolding.BeforeUpdate with tx
// ---------------------------------------------------------------------------

func TestFundHolding_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	h := &FundHolding{Version: 2}
	if err := h.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if h.Version != 3 {
		t.Errorf("got version %d, want 3", h.Version)
	}
}

// ---------------------------------------------------------------------------
// HoldingReservation.BeforeCreate — count != 1 paths
// ---------------------------------------------------------------------------

func TestHoldingReservation_BeforeCreate_NoKeys_Error(t *testing.T) {
	h := &HoldingReservation{} // all FK columns nil → count=0
	if err := h.BeforeCreate(nil); err == nil {
		t.Fatal("expected error for count=0")
	}
}

func TestHoldingReservation_BeforeCreate_MultipleKeys_Error(t *testing.T) {
	oid := uint64(1)
	cid := uint64(2)
	h := &HoldingReservation{OrderID: &oid, OTCContractID: &cid} // count=2
	if err := h.BeforeCreate(nil); err == nil {
		t.Fatal("expected error for count=2")
	}
}

func TestHoldingReservation_BeforeCreate_OrderIDOnly_OK(t *testing.T) {
	oid := uint64(1)
	h := &HoldingReservation{OrderID: &oid}
	if err := h.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHoldingReservation_BeforeCreate_CrossbankTxIDOnly_OK(t *testing.T) {
	txID := "444:some-uuid"
	h := &HoldingReservation{CrossbankTxID: &txID}
	if err := h.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// OptionContract.BeforeUpdate with tx
// ---------------------------------------------------------------------------

func TestOptionContract_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	c := &OptionContract{Version: 7}
	if err := c.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Version != 8 {
		t.Errorf("got version %d, want 8", c.Version)
	}
}

// ---------------------------------------------------------------------------
// SetOwnRouting — non-numeric bank code path
// ---------------------------------------------------------------------------

func TestSetOwnRouting_NonNumericBankCode(t *testing.T) {
	// Save and restore original routing to avoid test pollution.
	orig := OwnRouting()
	defer ownRouting.Store(orig)

	SetOwnRouting("abc") // non-numeric → logs warning, leaves ownRouting unchanged
	if OwnRouting() != orig {
		t.Errorf("ownRouting changed to %d after non-numeric input", OwnRouting())
	}
}

func TestSetOwnRouting_NumericBankCode(t *testing.T) {
	orig := OwnRouting()
	defer ownRouting.Store(orig)

	SetOwnRouting("999")
	if OwnRouting() != 999 {
		t.Errorf("got %d, want 999", OwnRouting())
	}
}
