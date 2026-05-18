package model

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestInvestmentFund_BeforeUpdate_BumpsVersion(t *testing.T) {
	f := &InvestmentFund{Version: 5}
	if err := f.BeforeUpdate(nil); err != nil {
		t.Fatalf("BeforeUpdate returned error: %v", err)
	}
	if f.Version != 6 {
		t.Errorf("expected version=6, got %d", f.Version)
	}
}

func TestInvestmentFund_DefaultMinimumIsZero(t *testing.T) {
	f := &InvestmentFund{}
	if !f.MinimumContributionRSD.Equal(decimal.Zero) {
		t.Errorf("expected zero default minimum, got %s", f.MinimumContributionRSD)
	}
}

func TestClientFundPosition_BeforeUpdate_BumpsVersion(t *testing.T) {
	p := &ClientFundPosition{Version: 2}
	if err := p.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.Version != 3 {
		t.Errorf("got %d want 3", p.Version)
	}
}

func TestFundContribution_StatusEnum(t *testing.T) {
	c := &FundContribution{Status: FundContributionStatusPending}
	if c.Status != "pending" {
		t.Errorf("status const wrong: %q", c.Status)
	}
}

func TestFundContribution_DirectionEnum(t *testing.T) {
	for _, d := range []string{FundDirectionInvest, FundDirectionRedeem} {
		if d == "" {
			t.Errorf("empty direction const")
		}
	}
}

func TestFundHolding_BeforeUpdate_BumpsVersion(t *testing.T) {
	h := &FundHolding{Version: 0}
	if err := h.BeforeUpdate(nil); err != nil {
		t.Fatalf("err: %v", err)
	}
	if h.Version != 1 {
		t.Errorf("got %d want 1", h.Version)
	}
}
