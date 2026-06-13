package model

import "testing"

// TestBeforeCreate_StampsLocal verifies the explicit `local` discriminator is
// stamped by BeforeCreate AFTER routing is finalized: a row created with
// RoutingNumber==0 (local create) becomes Local==true; a row created with an
// explicit peer RoutingNumber becomes Local==false. The invariant
// `Local == (RoutingNumber == OwnRouting())` must hold for all three models.
func TestBeforeCreate_StampsLocal(t *testing.T) {
	SetOwnRouting("111")

	for _, tc := range []struct {
		name      string
		localRun  func() bool // local create (routing 0) → expect Local==true
		remoteRun func() bool // remote create (routing 222) → expect Local==false
	}{
		{
			"offer",
			func() bool { o := &OTCOffer{}; _ = o.BeforeCreate(nil); return o.Local },
			func() bool { o := &OTCOffer{RoutingNumber: 222}; _ = o.BeforeCreate(nil); return o.Local },
		},
		{
			"neg",
			func() bool { n := &OTCNegotiation{}; _ = n.BeforeCreate(nil); return n.Local },
			func() bool { n := &OTCNegotiation{RoutingNumber: 222}; _ = n.BeforeCreate(nil); return n.Local },
		},
		{
			"contract",
			func() bool { c := &OptionContract{}; _ = c.BeforeCreate(nil); return c.Local },
			func() bool { c := &OptionContract{RoutingNumber: 222}; _ = c.BeforeCreate(nil); return c.Local },
		},
	} {
		if got := tc.localRun(); !got {
			t.Errorf("%s local create: Local = %v, want true", tc.name, got)
		}
		if got := tc.remoteRun(); got {
			t.Errorf("%s remote create: Local = %v, want false", tc.name, got)
		}
	}
}

// TestBeforeCreate_LocalMatchesRoutingInvariant asserts that across a matrix of
// routing inputs the stamped Local field never diverges from
// `RoutingNumber == OwnRouting()` — the two can never disagree.
func TestBeforeCreate_LocalMatchesRoutingInvariant(t *testing.T) {
	SetOwnRouting("111")

	for _, routing := range []int64{0, 111, 222, 333} {
		o := &OTCOffer{RoutingNumber: routing}
		_ = o.BeforeCreate(nil)
		if o.Local != (o.RoutingNumber == OwnRouting()) {
			t.Errorf("offer routing=%d: Local=%v but routing==own is %v", routing, o.Local, o.RoutingNumber == OwnRouting())
		}
		n := &OTCNegotiation{RoutingNumber: routing}
		_ = n.BeforeCreate(nil)
		if n.Local != (n.RoutingNumber == OwnRouting()) {
			t.Errorf("neg routing=%d: Local=%v but routing==own is %v", routing, n.Local, n.RoutingNumber == OwnRouting())
		}
		c := &OptionContract{RoutingNumber: routing}
		_ = c.BeforeCreate(nil)
		if c.Local != (c.RoutingNumber == OwnRouting()) {
			t.Errorf("contract routing=%d: Local=%v but routing==own is %v", routing, c.Local, c.RoutingNumber == OwnRouting())
		}
	}
}
