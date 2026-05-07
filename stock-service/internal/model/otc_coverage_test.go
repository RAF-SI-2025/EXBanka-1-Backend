package model

import "testing"

// ----------------------------------------------------------------------------
// OTCOffer
// ----------------------------------------------------------------------------

func TestOTCOffer_BeforeSave_OK(t *testing.T) {
	id := uint64(1)
	o := &OTCOffer{InitiatorOwnerType: OwnerClient, InitiatorOwnerID: &id}
	if err := o.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestOTCOffer_BeforeSave_BadInitiator(t *testing.T) {
	// client with nil id
	o := &OTCOffer{InitiatorOwnerType: OwnerClient, InitiatorOwnerID: nil}
	if err := o.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOffer_BeforeSave_BadCounterparty(t *testing.T) {
	id := uint64(1)
	cpt := OwnerClient
	o := &OTCOffer{
		InitiatorOwnerType:    OwnerClient,
		InitiatorOwnerID:      &id,
		CounterpartyOwnerType: &cpt,
		CounterpartyOwnerID:   nil, // client must have id
	}
	if err := o.BeforeSave(nil); err == nil {
		t.Fatal("expected error: counterparty client without id")
	}
}

func TestOTCOffer_BeforeSave_BankCounterpartyOK(t *testing.T) {
	id := uint64(1)
	cpt := OwnerBank
	o := &OTCOffer{
		InitiatorOwnerType:    OwnerClient,
		InitiatorOwnerID:      &id,
		CounterpartyOwnerType: &cpt,
	}
	if err := o.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestOTCOffer_BeforeUpdate_NilTx(t *testing.T) {
	// OTCOffer.BeforeUpdate is nil-safe
	o := &OTCOffer{Version: 2}
	if err := o.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if o.Version != 3 {
		t.Errorf("got %d", o.Version)
	}
}

func TestOTCOffer_BeforeUpdate_WithTx(t *testing.T) {
	db := newCoverageTestDB(t)
	o := &OTCOffer{Version: 0}
	if err := o.BeforeUpdate(db); err != nil {
		t.Fatalf("err: %v", err)
	}
	if o.Version != 1 {
		t.Errorf("got %d", o.Version)
	}
}

func TestIsCrossBankOffer_BothEmpty(t *testing.T) {
	if IsCrossBankOffer(&OTCOffer{}, "111") {
		t.Error("expected false for both-empty")
	}
}

func TestIsCrossBankOffer_InitiatorRemote(t *testing.T) {
	bank := "222"
	if !IsCrossBankOffer(&OTCOffer{InitiatorBankCode: &bank}, "111") {
		t.Error("expected true: initiator on different bank")
	}
}

func TestIsCrossBankOffer_CounterpartyRemote(t *testing.T) {
	bank := "333"
	if !IsCrossBankOffer(&OTCOffer{CounterpartyBankCode: &bank}, "111") {
		t.Error("expected true: counterparty on different bank")
	}
}

func TestIsCrossBankOffer_AllSelf(t *testing.T) {
	self := "111"
	o := &OTCOffer{InitiatorBankCode: &self, CounterpartyBankCode: &self}
	if IsCrossBankOffer(o, "111") {
		t.Error("expected false: both on self bank")
	}
}

func TestOTCOffer_IsTerminal(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		{OTCOfferStatusAccepted, true},
		{OTCOfferStatusRejected, true},
		{OTCOfferStatusExpired, true},
		{OTCOfferStatusFailed, true},
		{OTCOfferStatusPending, false},
		{OTCOfferStatusCountered, false},
	}
	for _, c := range cases {
		o := &OTCOffer{Status: c.status}
		if got := o.IsTerminal(); got != c.want {
			t.Errorf("status %q: got %v want %v", c.status, got, c.want)
		}
	}
}

// ----------------------------------------------------------------------------
// OptionContract
// ----------------------------------------------------------------------------

func TestOptionContract_BeforeSave_OK(t *testing.T) {
	a, b := uint64(1), uint64(2)
	c := &OptionContract{
		BuyerOwnerType:  OwnerClient,
		BuyerOwnerID:    &a,
		SellerOwnerType: OwnerClient,
		SellerOwnerID:   &b,
	}
	if err := c.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestOptionContract_BeforeSave_BadBuyer(t *testing.T) {
	c := &OptionContract{
		BuyerOwnerType:  OwnerClient,
		BuyerOwnerID:    nil,
		SellerOwnerType: OwnerBank,
	}
	if err := c.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOptionContract_BeforeSave_BadSeller(t *testing.T) {
	id := uint64(1)
	c := &OptionContract{
		BuyerOwnerType:  OwnerClient,
		BuyerOwnerID:    &id,
		SellerOwnerType: OwnerClient,
		SellerOwnerID:   nil,
	}
	if err := c.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOptionContract_BeforeUpdate_NilTx(t *testing.T) {
	c := &OptionContract{Version: 1}
	if err := c.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Version != 2 {
		t.Errorf("got %d", c.Version)
	}
}

func TestOptionContract_IsCrossBank_BothNil(t *testing.T) {
	c := &OptionContract{}
	if c.IsCrossBank() {
		t.Error("expected false")
	}
}

func TestOptionContract_IsCrossBank_OneEmpty(t *testing.T) {
	bank := "111"
	c := &OptionContract{BuyerBankCode: &bank}
	if c.IsCrossBank() {
		t.Error("expected false: only one bank code present")
	}
}

func TestOptionContract_IsCrossBank_DifferentBanks(t *testing.T) {
	a := "111"
	b := "222"
	c := &OptionContract{BuyerBankCode: &a, SellerBankCode: &b}
	if !c.IsCrossBank() {
		t.Error("expected true")
	}
}

func TestOptionContract_IsCrossBank_SameBank(t *testing.T) {
	a := "111"
	c := &OptionContract{BuyerBankCode: &a, SellerBankCode: &a}
	if c.IsCrossBank() {
		t.Error("expected false: same bank")
	}
}

func TestOptionContract_IsTerminal(t *testing.T) {
	for _, s := range []string{OptionContractStatusExercised, OptionContractStatusExpired, OptionContractStatusFailed} {
		c := &OptionContract{Status: s}
		if !c.IsTerminal() {
			t.Errorf("status %q expected terminal", s)
		}
	}
	c := &OptionContract{Status: OptionContractStatusActive}
	if c.IsTerminal() {
		t.Error("active should not be terminal")
	}
}

// ----------------------------------------------------------------------------
// OTCOfferReadReceipt.BeforeSave
// ----------------------------------------------------------------------------

func TestOTCOfferReadReceipt_BeforeSave_BankOK(t *testing.T) {
	r := &OTCOfferReadReceipt{OwnerType: OwnerBank, OwnerID: 0}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestOTCOfferReadReceipt_BeforeSave_BadType(t *testing.T) {
	r := &OTCOfferReadReceipt{OwnerType: OwnerType("nope")}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOfferReadReceipt_BeforeSave_ClientZeroID(t *testing.T) {
	// client with owner_id == 0 is rejected
	r := &OTCOfferReadReceipt{OwnerType: OwnerClient, OwnerID: 0}
	if err := r.BeforeSave(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestOTCOfferReadReceipt_BeforeSave_ClientWithID(t *testing.T) {
	r := &OTCOfferReadReceipt{OwnerType: OwnerClient, OwnerID: 42}
	if err := r.BeforeSave(nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
