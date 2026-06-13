package model

import "testing"

func TestBeforeCreate_StampsOwnRouting(t *testing.T) {
	SetOwnRouting("111")
	for _, tc := range []struct {
		name string
		run  func() int64
	}{
		{"offer", func() int64 { o := &OTCOffer{}; _ = o.BeforeCreate(nil); return o.RoutingNumber }},
		{"neg", func() int64 { n := &OTCNegotiation{}; _ = n.BeforeCreate(nil); return n.RoutingNumber }},
		{"contract", func() int64 { c := &OptionContract{}; _ = c.BeforeCreate(nil); return c.RoutingNumber }},
	} {
		if got := tc.run(); got != 111 {
			t.Errorf("%s: routing = %d, want 111", tc.name, got)
		}
	}
	// a pre-set (remote) routing is preserved
	o := &OTCOffer{RoutingNumber: 222}
	_ = o.BeforeCreate(nil)
	if o.RoutingNumber != 222 {
		t.Errorf("remote routing overwritten: %d", o.RoutingNumber)
	}
}
