package service

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Celina 3 approval rule: an agent's BUY order needs supervisor approval if ANY
// of: (1) the agent's needApproval flag is set; (2) the agent has used up their
// daily limit; (3) the order would push used+amount over the daily limit.
// I.e. a DISJUNCTION, not a conjunction.
func TestDecideNeedsApproval(t *testing.T) {
	d := decimal.RequireFromString
	cases := []struct {
		name        string
		needApprove bool
		limit       string
		usedLimit   string
		amount      string
		want        bool
	}{
		// flag set, under limit -> approval required (flag alone). Old AND-logic got this WRONG.
		{"flag set, under limit", true, "100", "0", "10", true},
		// flag NOT set, over limit -> approval required (money-control). Old AND-logic let this auto-approve.
		{"no flag, over limit", false, "100", "95", "10", true},
		// flag NOT set, under limit -> auto-approve
		{"no flag, under limit", false, "100", "0", "10", false},
		// flag set, over limit -> approval required
		{"flag set, over limit", true, "100", "95", "10", true},
		// no limit configured, no flag -> auto-approve regardless of amount
		{"no limit, no flag", false, "0", "0", "999999", false},
		// no limit configured, flag set -> approval required (flag alone)
		{"no limit, flag set", true, "0", "0", "10", true},
		// boundary: used+amount exactly equals limit -> still within limit -> auto-approve
		{"boundary at limit", false, "100", "90", "10", false},
		// boundary: used+amount one over limit -> approval required
		{"boundary one over limit", false, "100", "90", "11", true},
		// fully used limit, any new amount -> approval required (condition 2)
		{"limit fully used", false, "100", "100", "1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideNeedsApproval(tc.needApprove, d(tc.limit), d(tc.usedLimit), d(tc.amount))
			if got != tc.want {
				t.Fatalf("decideNeedsApproval(flag=%v limit=%s used=%s amt=%s) = %v, want %v",
					tc.needApprove, tc.limit, tc.usedLimit, tc.amount, got, tc.want)
			}
		})
	}
}
