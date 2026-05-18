package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

// ----------------------------------------------------------------------------
// Holding
// ----------------------------------------------------------------------------

func TestHolding_BeforeSave_BankWithNilID_OK(t *testing.T) {
	h := &Holding{OwnerType: OwnerBank, OwnerID: nil}
	if err := h.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestHolding_BeforeSave_ClientNilID_Errors(t *testing.T) {
	h := &Holding{OwnerType: OwnerClient, OwnerID: nil}
	if err := h.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestHolding_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	h := &Holding{Version: 3}
	if err := h.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if h.Version != 4 {
		t.Errorf("got %d", h.Version)
	}
}

// ----------------------------------------------------------------------------
// HoldingReservation
// ----------------------------------------------------------------------------

func TestHoldingReservation_TableName(t *testing.T) {
	if got := (HoldingReservation{}).TableName(); got != "holding_reservations" {
		t.Errorf("got %q", got)
	}
}

func TestHoldingReservation_BeforeCreate_NoFKs(t *testing.T) {
	r := &HoldingReservation{}
	if err := r.BeforeCreate(nil); err == nil {
		t.Fatal("expected error: zero FKs")
	}
}

func TestHoldingReservation_BeforeCreate_TwoFKs(t *testing.T) {
	a, b := uint64(1), uint64(2)
	r := &HoldingReservation{OrderID: &a, OTCContractID: &b}
	if err := r.BeforeCreate(nil); err == nil {
		t.Fatal("expected error: two FKs")
	}
}

func TestHoldingReservation_BeforeCreate_OnlyOrderID_OK(t *testing.T) {
	id := uint64(1)
	r := &HoldingReservation{OrderID: &id}
	if err := r.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestHoldingReservation_BeforeCreate_OnlyOTC_OK(t *testing.T) {
	id := uint64(1)
	r := &HoldingReservation{OTCContractID: &id}
	if err := r.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestHoldingReservation_BeforeCreate_OnlyPeerOption_OK(t *testing.T) {
	id := uint64(1)
	r := &HoldingReservation{PeerOptionContractID: &id}
	if err := r.BeforeCreate(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestHoldingReservation_BeforeCreate_AllThreeFKs_Errors(t *testing.T) {
	a, b, c := uint64(1), uint64(2), uint64(3)
	r := &HoldingReservation{OrderID: &a, OTCContractID: &b, PeerOptionContractID: &c}
	if err := r.BeforeCreate(nil); err == nil {
		t.Fatal("expected error: three FKs")
	}
}

func TestHoldingReservation_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	r := &HoldingReservation{Version: 0}
	if err := r.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if r.Version != 1 {
		t.Errorf("got %d", r.Version)
	}
}

// ----------------------------------------------------------------------------
// HoldingReservationSettlement
// ----------------------------------------------------------------------------

func TestHoldingReservationSettlement_TableName(t *testing.T) {
	if got := (HoldingReservationSettlement{}).TableName(); got != "holding_reservation_settlements" {
		t.Errorf("got %q", got)
	}
}

// ----------------------------------------------------------------------------
// IdempotencyRecord
// ----------------------------------------------------------------------------

func TestIdempotencyRecord_TableName(t *testing.T) {
	if got := (IdempotencyRecord{}).TableName(); got != "idempotency_records" {
		t.Errorf("got %q", got)
	}
}

// ----------------------------------------------------------------------------
// SagaLog
// ----------------------------------------------------------------------------

func TestSagaLog_TableName(t *testing.T) {
	if got := (SagaLog{}).TableName(); got != "saga_logs" {
		t.Errorf("got %q", got)
	}
}

func TestSagaLog_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	l := &SagaLog{Version: 5}
	if err := l.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if l.Version != 6 {
		t.Errorf("got %d", l.Version)
	}
}

// ----------------------------------------------------------------------------
// PeerOptionContract
// ----------------------------------------------------------------------------

func TestPeerOptionContract_TableName(t *testing.T) {
	if got := (PeerOptionContract{}).TableName(); got != "peer_option_contracts" {
		t.Errorf("got %q", got)
	}
}

// ----------------------------------------------------------------------------
// Order BeforeSave + BeforeUpdate + IsAutoApproved
// ----------------------------------------------------------------------------

func TestOrder_BeforeSave_BankOK(t *testing.T) {
	o := &Order{OwnerType: OwnerBank}
	if err := o.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestOrder_BeforeSave_ClientWithoutID_Errors(t *testing.T) {
	o := &Order{OwnerType: OwnerClient, OwnerID: nil}
	if err := o.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOrder_BeforeUpdate(t *testing.T) {
	db := newCoverageTestDB(t)
	o := &Order{Version: 11}
	if err := o.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if o.Version != 12 {
		t.Errorf("got %d", o.Version)
	}
}

func TestOrder_IsAutoApproved_Bank(t *testing.T) {
	o := &Order{OwnerType: OwnerBank}
	if !o.IsAutoApproved() {
		t.Error("expected auto-approved for bank")
	}
}

func TestOrder_IsAutoApproved_ClientWithoutEmployee(t *testing.T) {
	id := uint64(1)
	o := &Order{OwnerType: OwnerClient, OwnerID: &id}
	if !o.IsAutoApproved() {
		t.Error("expected auto-approved for client w/o acting employee")
	}
}

func TestOrder_IsAutoApproved_ClientWithEmployee(t *testing.T) {
	id := uint64(1)
	emp := uint64(99)
	o := &Order{OwnerType: OwnerClient, OwnerID: &id, ActingEmployeeID: &emp}
	if o.IsAutoApproved() {
		t.Error("expected NOT auto-approved when employee acts on behalf of client")
	}
}

// ----------------------------------------------------------------------------
// CapitalGain
// ----------------------------------------------------------------------------

func TestCapitalGain_BeforeSave_BankOK(t *testing.T) {
	g := &CapitalGain{OwnerType: OwnerBank, TotalGain: decimal.NewFromInt(1)}
	if err := g.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestCapitalGain_BeforeSave_BadType(t *testing.T) {
	g := &CapitalGain{OwnerType: OwnerType("xyz")}
	if err := g.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

// ----------------------------------------------------------------------------
// TaxCollection
// ----------------------------------------------------------------------------

func TestTaxCollection_BeforeSave_OK(t *testing.T) {
	id := uint64(7)
	tc := &TaxCollection{OwnerType: OwnerClient, OwnerID: &id}
	if err := tc.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestTaxCollection_BeforeSave_BadOwner(t *testing.T) {
	tc := &TaxCollection{OwnerType: OwnerClient, OwnerID: nil}
	if err := tc.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

// ----------------------------------------------------------------------------
// FundContribution
// ----------------------------------------------------------------------------

func TestFundContribution_BeforeSave_OK(t *testing.T) {
	id := uint64(7)
	c := &FundContribution{OwnerType: OwnerClient, OwnerID: &id}
	if err := c.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestFundContribution_BeforeSave_BankWithID_Errors(t *testing.T) {
	id := uint64(1)
	c := &FundContribution{OwnerType: OwnerBank, OwnerID: &id}
	if err := c.BeforeSave(nil); err == nil {
		t.Fatal("expected error: bank with id")
	}
}

// ----------------------------------------------------------------------------
// ClientFundPosition
// ----------------------------------------------------------------------------

func TestClientFundPosition_BeforeSave_BankOK(t *testing.T) {
	p := &ClientFundPosition{OwnerType: OwnerBank}
	if err := p.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestClientFundPosition_BeforeSave_BadType(t *testing.T) {
	p := &ClientFundPosition{OwnerType: OwnerType("nope")}
	if err := p.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}
