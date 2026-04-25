package model

import "testing"

func TestOrder_FundIDDefaultsToNil(t *testing.T) {
	o := Order{}
	if o.FundID != nil {
		t.Errorf("expected FundID to default to nil, got %v", *o.FundID)
	}
}

func TestOrder_FundIDIsAssignable(t *testing.T) {
	id := uint64(101)
	o := Order{FundID: &id}
	if o.FundID == nil || *o.FundID != 101 {
		t.Fatalf("expected FundID=101, got %v", o.FundID)
	}
}
