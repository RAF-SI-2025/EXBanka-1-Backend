package handler

import "testing"

// These tests cover the production NewXxxHandler constructors (the test
// suites elsewhere use newXxxHandlerForTest with interface-typed mocks,
// which leaves the real constructors at 0%). Passing nil services exercises
// the constructor body without spinning up a real service stack.

func TestNewExchangeGRPCHandler(t *testing.T) {
	h := NewExchangeGRPCHandler(nil)
	if h == nil {
		t.Fatal("NewExchangeGRPCHandler returned nil")
	}
}

func TestNewOrderHandler(t *testing.T) {
	h := NewOrderHandler(nil, nil)
	if h == nil {
		t.Fatal("NewOrderHandler returned nil")
	}
}

func TestNewOTCHandler(t *testing.T) {
	h := NewOTCHandler(nil)
	if h == nil {
		t.Fatal("NewOTCHandler returned nil")
	}
	if h.cache != nil {
		t.Error("expected cache to be nil for plain constructor")
	}
}

func TestNewPortfolioHandler(t *testing.T) {
	h := NewPortfolioHandler(nil, nil)
	if h == nil {
		t.Fatal("NewPortfolioHandler returned nil")
	}
}

func TestNewTaxHandler(t *testing.T) {
	h := NewTaxHandler(nil)
	if h == nil {
		t.Fatal("NewTaxHandler returned nil")
	}
}

func TestNewInvestmentFundHandler(t *testing.T) {
	h := NewInvestmentFundHandler(nil, nil, nil)
	if h == nil {
		t.Fatal("NewInvestmentFundHandler returned nil")
	}
}

func TestNewInvestmentFundHandler_WithActuaryDeps(t *testing.T) {
	h := NewInvestmentFundHandler(nil, nil, nil)
	cp := h.WithActuaryDeps(nil, nil, nil)
	if cp == nil {
		t.Fatal("WithActuaryDeps returned nil")
	}
	// returns a copy — original should still be valid
	if h == cp {
		t.Error("WithActuaryDeps should return a copy, not the same pointer")
	}
}

func TestNewInvestmentFundHandler_WithFundDetailDeps(t *testing.T) {
	h := NewInvestmentFundHandler(nil, nil, nil)
	cp := h.WithFundDetailDeps(nil, nil, nil)
	if cp == nil {
		t.Fatal("WithFundDetailDeps returned nil")
	}
	if h == cp {
		t.Error("WithFundDetailDeps should return a copy")
	}
}
