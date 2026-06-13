package sitx

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSpecPostingToInternal_NegativeIsDebitOutgoing(t *testing.T) {
	// Spec: negative amount = credit = asset LEAVES = internal DEBIT.
	p := Posting{
		Account: TxAccount{Type: "ACCOUNT", Num: "444000100182503611"},
		Amount:  DecimalNumber{decimal.RequireFromString("-260")},
		Asset:   Asset{Type: "MONAS", Asset: map[string]interface{}{"currency": "RSD"}},
	}
	ip, err := SpecPostingToInternal(p)
	if err != nil {
		t.Fatal(err)
	}
	if ip.Direction != DirectionDebit {
		t.Fatalf("negative amount must map to internal DEBIT, got %s", ip.Direction)
	}
	if ip.Amount != "260" {
		t.Fatalf("magnitude must be abs, got %s", ip.Amount)
	}
	if ip.AccountType != "ACCOUNT" || ip.AccountID != "444000100182503611" {
		t.Fatalf("account mapping wrong: %+v", ip)
	}
	if ip.AssetType != "MONAS" || ip.AssetID != "RSD" {
		t.Fatalf("asset mapping wrong: %+v", ip)
	}
}

func TestSpecPostingToInternal_PositiveIsCreditIncoming(t *testing.T) {
	p := Posting{
		Account: TxAccount{Type: "ACCOUNT", Num: "111000141215476411"},
		Amount:  DecimalNumber{decimal.RequireFromString("260")},
		Asset:   Asset{Type: "MONAS", Asset: map[string]interface{}{"currency": "RSD"}},
	}
	ip, _ := SpecPostingToInternal(p)
	if ip.Direction != DirectionCredit {
		t.Fatalf("positive amount must map to internal CREDIT, got %s", ip.Direction)
	}
}

func TestPostingRoundTrip(t *testing.T) {
	orig := Posting{
		Account: TxAccount{Type: "PERSON", ID: &ForeignBankId{RoutingNumber: 222, ID: "client-7"}},
		Amount:  DecimalNumber{decimal.RequireFromString("-12.5")},
		Asset:   Asset{Type: "MONAS", Asset: map[string]interface{}{"currency": "EUR"}},
	}
	ip, err := SpecPostingToInternal(orig)
	if err != nil {
		t.Fatal(err)
	}
	back, err := InternalPostingToSpec(ip)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Amount.Equal(orig.Amount.Decimal) {
		t.Fatalf("amount round-trip: got %s want %s", back.Amount.Decimal, orig.Amount.Decimal)
	}
	if back.Account.Type != "PERSON" || back.Account.ID.ID != "client-7" {
		t.Fatalf("account round-trip wrong: %+v", back.Account)
	}
	if back.Asset.Type != "MONAS" {
		t.Fatalf("asset round-trip wrong: %+v", back.Asset)
	}
}

// TestOptionAssetRoundTrip verifies that an OPTION-typed posting preserves all
// option description fields (ticker, strike, settlement date, negotiation id)
// across the SpecPostingToInternal → InternalPostingToSpec round-trip.
func TestOptionAssetRoundTrip(t *testing.T) {
	od := OptionDescription{
		NegotiationID:  ForeignBankId{RoutingNumber: 222, ID: "neg-1"},
		Stock:          StockDescription{Ticker: "AAPL"},
		PricePerUnit:   MonetaryValue{Amount: DecimalNumber{Decimal: decimal.RequireFromString("5")}, Currency: "USD"},
		SettlementDate: "2026-06-15T00:00:00Z",
		Amount:         10,
	}
	orig := Posting{
		Account: TxAccount{Type: "PERSON", ID: &ForeignBankId{RoutingNumber: 222, ID: "client-42"}},
		Amount:  DecimalNumber{decimal.RequireFromString("100")}, // positive → internal CREDIT
		Asset:   Asset{Type: "OPTION", Asset: od},
	}

	ip, err := SpecPostingToInternal(orig)
	if err != nil {
		t.Fatal(err)
	}
	if ip.AssetType != "OPTION" {
		t.Fatalf("AssetType must be OPTION, got %s", ip.AssetType)
	}
	if ip.Direction != DirectionCredit {
		t.Fatalf("positive amount must map to internal CREDIT, got %s", ip.Direction)
	}

	back, err := InternalPostingToSpec(ip)
	if err != nil {
		t.Fatal(err)
	}
	if back.Asset.Type != "OPTION" {
		t.Fatalf("asset round-trip must be OPTION, got %s", back.Asset.Type)
	}

	// The Asset.Asset will be an OptionDescription (typed) coming back from idToAsset.
	backOD, ok := back.Asset.Asset.(OptionDescription)
	if !ok {
		t.Fatalf("expected OptionDescription, got %T", back.Asset.Asset)
	}
	if backOD.Stock.Ticker != "AAPL" {
		t.Fatalf("ticker round-trip: got %q want %q", backOD.Stock.Ticker, "AAPL")
	}
	if !backOD.PricePerUnit.Amount.Decimal.Equal(decimal.RequireFromString("5")) {
		t.Fatalf("strike round-trip: got %s want %s", backOD.PricePerUnit.Amount.String(), "5")
	}
	if backOD.NegotiationID.ID != "neg-1" {
		t.Fatalf("negotiation id round-trip: got %q want %q", backOD.NegotiationID.ID, "neg-1")
	}
	if backOD.SettlementDate != "2026-06-15T00:00:00Z" {
		t.Fatalf("settlement date round-trip: got %q", backOD.SettlementDate)
	}
}

// TestInternalCreditMapsToPositiveSpecAmount confirms that a positive internal
// CREDIT posting reconstructs as a positive spec amount (the happy-path
// sign direction from executor back to wire).
func TestInternalCreditMapsToPositiveSpecAmount(t *testing.T) {
	ip := InternalPosting{
		RoutingNumber: 111,
		AccountType:   "ACCOUNT",
		AccountID:     "111000100182503611",
		AssetType:     "MONAS",
		AssetID:       "RSD",
		Direction:     DirectionCredit,
		Amount:        "500",
	}
	p, err := InternalPostingToSpec(ip)
	if err != nil {
		t.Fatal(err)
	}
	if p.Amount.IsNegative() || p.Amount.IsZero() {
		t.Fatalf("internal CREDIT must produce positive spec amount, got %s", p.Amount.Decimal)
	}
	if p.Amount.String() != "500" {
		t.Fatalf("magnitude must be preserved, got %s", p.Amount.Decimal)
	}
}
