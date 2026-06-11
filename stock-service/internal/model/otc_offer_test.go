package model

import (
	"reflect"
	"testing"
)

// TestOTCOffer_HasNoPresetTermFields guards the R12 refactor: option terms
// (strike/premium/settlement/currencies) live only on the negotiation chain,
// never on the termless OTCOffer listing. If any of these fields reappears on
// the model, this test fails so the readers/writers get re-audited.
func TestOTCOffer_HasNoPresetTermFields(t *testing.T) {
	typ := reflect.TypeOf(OTCOffer{})
	for _, f := range []string{"StrikePrice", "Premium", "SettlementDate", "StrikeCurrency", "PremiumCurrency", "HasPresetTerms"} {
		if _, ok := typ.FieldByName(f); ok {
			t.Fatalf("OTCOffer must not have field %q", f)
		}
	}
}
