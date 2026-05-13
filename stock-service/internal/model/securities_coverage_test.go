package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

// ----------------------------------------------------------------------------
// Stock
// ----------------------------------------------------------------------------

func TestStock_BeforeUpdate_BumpsVersion(t *testing.T) {
	db := newCoverageTestDB(t)
	s := &Stock{Version: 4}
	if err := s.BeforeUpdate(db); err != nil {
		t.Fatalf("BeforeUpdate: %v", err)
	}
	if s.Version != 5 {
		t.Errorf("got version %d, want 5", s.Version)
	}
}

func TestStock_ContractSize(t *testing.T) {
	s := &Stock{}
	if got := s.ContractSize(); got != 1 {
		t.Errorf("got %d want 1", got)
	}
}

func TestStock_MaintenanceMargin(t *testing.T) {
	s := &Stock{Price: decimal.NewFromInt(100)}
	if got := s.MaintenanceMargin(); !got.Equal(decimal.NewFromInt(50)) {
		t.Errorf("got %s want 50", got)
	}
}

func TestStock_InitialMarginCost(t *testing.T) {
	s := &Stock{Price: decimal.NewFromInt(100)}
	// 100 * 0.5 * 1.1 = 55
	if got := s.InitialMarginCost(); !got.Equal(decimal.NewFromInt(55)) {
		t.Errorf("got %s want 55", got)
	}
}

func TestStock_MarketCap(t *testing.T) {
	s := &Stock{Price: decimal.NewFromInt(20), OutstandingShares: 1000}
	if got := s.MarketCap(); !got.Equal(decimal.NewFromInt(20000)) {
		t.Errorf("got %s want 20000", got)
	}
}

// ----------------------------------------------------------------------------
// Option
// ----------------------------------------------------------------------------

func TestOption_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	o := &Option{Version: 2}
	if err := o.BeforeUpdate(db); err != nil {
		t.Fatalf("BeforeUpdate: %v", err)
	}
	if o.Version != 3 {
		t.Errorf("got %d want 3", o.Version)
	}
}

func TestOption_ContractSizeValue(t *testing.T) {
	o := &Option{}
	if got := o.ContractSizeValue(); got != 100 {
		t.Errorf("got %d want 100", got)
	}
}

func TestOption_MaintenanceMargin(t *testing.T) {
	o := &Option{}
	// 100 * 200 * 0.5 = 10000
	got := o.MaintenanceMargin(decimal.NewFromInt(200))
	if !got.Equal(decimal.NewFromInt(10000)) {
		t.Errorf("got %s want 10000", got)
	}
}

func TestOption_InitialMarginCost(t *testing.T) {
	o := &Option{}
	// 10000 * 1.1 = 11000
	got := o.InitialMarginCost(decimal.NewFromInt(200))
	if !got.Equal(decimal.NewFromInt(11000)) {
		t.Errorf("got %s want 11000", got)
	}
}

// ----------------------------------------------------------------------------
// Listing
// ----------------------------------------------------------------------------

func TestListing_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	l := &Listing{Version: 7}
	if err := l.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if l.Version != 8 {
		t.Errorf("got %d want 8", l.Version)
	}
}

// ----------------------------------------------------------------------------
// FuturesContract
// ----------------------------------------------------------------------------

func TestFuturesContract_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	f := &FuturesContract{Version: 1}
	if err := f.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if f.Version != 2 {
		t.Errorf("got %d want 2", f.Version)
	}
}

func TestFuturesContract_MaintenanceMargin(t *testing.T) {
	f := &FuturesContract{ContractSize: 1000, Price: decimal.NewFromInt(50)}
	// 1000 * 50 * 0.1 = 5000
	if got := f.MaintenanceMargin(); !got.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("got %s want 5000", got)
	}
}

func TestFuturesContract_InitialMarginCost(t *testing.T) {
	f := &FuturesContract{ContractSize: 1000, Price: decimal.NewFromInt(50)}
	// 5000 * 1.1 = 5500
	if got := f.InitialMarginCost(); !got.Equal(decimal.NewFromInt(5500)) {
		t.Errorf("got %s want 5500", got)
	}
}

// ----------------------------------------------------------------------------
// ForexPair (extra, BeforeUpdate / Margin not in currency_test)
// ----------------------------------------------------------------------------

func TestForexPair_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	fp := &ForexPair{Version: 9}
	if err := fp.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if fp.Version != 10 {
		t.Errorf("got %d want 10", fp.Version)
	}
}

func TestForexPair_ContractSizeValue(t *testing.T) {
	fp := &ForexPair{}
	if got := fp.ContractSizeValue(); got != 1000 {
		t.Errorf("got %d want 1000", got)
	}
}

func TestForexPair_MaintenanceMargin(t *testing.T) {
	fp := &ForexPair{ExchangeRate: decimal.NewFromFloat(1.5)}
	// 1000 * 1.5 * 0.1 = 150
	if got := fp.MaintenanceMargin(); !got.Equal(decimal.NewFromInt(150)) {
		t.Errorf("got %s want 150", got)
	}
}

func TestForexPair_InitialMarginCost(t *testing.T) {
	fp := &ForexPair{ExchangeRate: decimal.NewFromFloat(1.5)}
	// 150 * 1.1 = 165
	if got := fp.InitialMarginCost(); !got.Equal(decimal.NewFromInt(165)) {
		t.Errorf("got %s want 165", got)
	}
}
