package service

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCalculateDerivedData_Stock(t *testing.T) {
	d := CalculateDerivedData("stock", decimal.NewFromInt(100), decimal.NewFromInt(2), 1000, 0, 1_000_000, decimal.Zero)
	if d.ContractSize != 1 {
		t.Errorf("expected ContractSize=1, got %d", d.ContractSize)
	}
	if !d.MaintenanceMargin.Equal(decimal.NewFromInt(50)) {
		t.Errorf("expected MaintenanceMargin=50, got %s", d.MaintenanceMargin)
	}
	if !d.InitialMarginCost.Equal(decimal.NewFromInt(55)) {
		t.Errorf("expected InitialMarginCost=55, got %s", d.InitialMarginCost)
	}
	expectedChangePct := decimal.NewFromInt(2).Mul(decimal.NewFromInt(100)).Div(decimal.NewFromInt(98)).Round(4)
	if !d.ChangePercent.Equal(expectedChangePct) {
		t.Errorf("expected ChangePercent=%s, got %s", expectedChangePct, d.ChangePercent)
	}
	if !d.DollarVolume.Equal(decimal.NewFromInt(100_000)) {
		t.Errorf("expected DollarVolume=100000, got %s", d.DollarVolume)
	}
	if !d.NominalValue.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected NominalValue=100, got %s", d.NominalValue)
	}
	if !d.MarketCap.Equal(decimal.NewFromInt(100_000_000)) {
		t.Errorf("expected MarketCap=100000000, got %s", d.MarketCap)
	}
}

func TestCalculateDerivedData_Futures(t *testing.T) {
	d := CalculateDerivedData("futures", decimal.NewFromInt(50), decimal.NewFromInt(0), 100, 1000, 0, decimal.Zero)
	if d.ContractSize != 1000 {
		t.Errorf("expected ContractSize=1000, got %d", d.ContractSize)
	}
	// 1000 × 50 × 0.10 = 5000
	if !d.MaintenanceMargin.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("expected MaintenanceMargin=5000, got %s", d.MaintenanceMargin)
	}
}

func TestCalculateDerivedData_FuturesNoOverride(t *testing.T) {
	d := CalculateDerivedData("futures", decimal.NewFromInt(50), decimal.NewFromInt(0), 100, 0, 0, decimal.Zero)
	if d.ContractSize != 1 {
		t.Errorf("expected ContractSize=1 (default), got %d", d.ContractSize)
	}
}

func TestCalculateDerivedData_Forex(t *testing.T) {
	d := CalculateDerivedData("forex", decimal.NewFromFloat(1.05), decimal.NewFromFloat(0), 100, 0, 0, decimal.Zero)
	if d.ContractSize != 1000 {
		t.Errorf("expected ContractSize=1000, got %d", d.ContractSize)
	}
	// 1000 × 1.05 × 0.10 = 105
	expected := decimal.NewFromFloat(105)
	if !d.MaintenanceMargin.Equal(expected) {
		t.Errorf("expected MaintenanceMargin=105, got %s", d.MaintenanceMargin)
	}
}

func TestCalculateDerivedData_DefaultType(t *testing.T) {
	d := CalculateDerivedData("unknown", decimal.NewFromInt(100), decimal.NewFromInt(0), 0, 0, 0, decimal.Zero)
	if d.ContractSize != 1 {
		t.Errorf("expected ContractSize=1 (default), got %d", d.ContractSize)
	}
	if !d.MaintenanceMargin.IsZero() {
		t.Errorf("expected MaintenanceMargin=0, got %s", d.MaintenanceMargin)
	}
}

func TestCalculateDerivedData_ZeroDenominator(t *testing.T) {
	// price - change = 0 -> ChangePercent stays at zero (doesn't divide by zero)
	d := CalculateDerivedData("stock", decimal.NewFromInt(100), decimal.NewFromInt(100), 0, 0, 0, decimal.Zero)
	if !d.ChangePercent.IsZero() {
		t.Errorf("expected ChangePercent=0, got %s", d.ChangePercent)
	}
}

func TestCalculateDerivedData_NoMarketCapForFutures(t *testing.T) {
	d := CalculateDerivedData("futures", decimal.NewFromInt(100), decimal.NewFromInt(0), 0, 100, 1_000_000, decimal.Zero)
	if !d.MarketCap.IsZero() {
		t.Errorf("expected MarketCap=0 for non-stock, got %s", d.MarketCap)
	}
}

func TestCalculateOptionDerivedData(t *testing.T) {
	d := CalculateOptionDerivedData(decimal.NewFromInt(5), decimal.NewFromInt(150))
	if d.ContractSize != 100 {
		t.Errorf("expected ContractSize=100, got %d", d.ContractSize)
	}
	// 100 × 150 × 0.50 = 7500
	if !d.MaintenanceMargin.Equal(decimal.NewFromInt(7500)) {
		t.Errorf("expected MaintenanceMargin=7500, got %s", d.MaintenanceMargin)
	}
	// 7500 × 1.1 = 8250
	if !d.InitialMarginCost.Equal(decimal.NewFromInt(8250)) {
		t.Errorf("expected InitialMarginCost=8250, got %s", d.InitialMarginCost)
	}
	// 100 × 5 = 500
	if !d.NominalValue.Equal(decimal.NewFromInt(500)) {
		t.Errorf("expected NominalValue=500, got %s", d.NominalValue)
	}
}
