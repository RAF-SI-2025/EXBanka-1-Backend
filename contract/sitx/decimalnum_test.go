package sitx

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestDecimalNumber_MarshalsAsBareNumber(t *testing.T) {
	d := DecimalNumber{decimal.RequireFromString("260")}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "260" {
		t.Fatalf("want bare number 260, got %s", b)
	}
}

func TestDecimalNumber_MarshalsFraction(t *testing.T) {
	d := DecimalNumber{decimal.RequireFromString("1.5")}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "1.5" {
		t.Fatalf("want 1.5, got %s", b)
	}
}

func TestDecimalNumber_UnmarshalNumberAndQuoted(t *testing.T) {
	var a DecimalNumber
	if err := json.Unmarshal([]byte("260"), &a); err != nil {
		t.Fatalf("unmarshal number: %v", err)
	}
	if !a.Decimal.Equal(decimal.RequireFromString("260")) {
		t.Fatalf("want 260, got %s", a.Decimal)
	}
	var b DecimalNumber
	if err := json.Unmarshal([]byte(`"1.25"`), &b); err != nil {
		t.Fatalf("unmarshal quoted: %v", err)
	}
	if !b.Decimal.Equal(decimal.RequireFromString("1.25")) {
		t.Fatalf("want 1.25, got %s", b.Decimal)
	}
}

func TestDecimalNumber_UnmarshalNull_NoOp(t *testing.T) {
	d := DecimalNumber{decimal.RequireFromString("5")}
	if err := json.Unmarshal([]byte("null"), &d); err != nil {
		t.Fatalf("null should be a no-op, got err: %v", err)
	}
	if !d.Decimal.Equal(decimal.RequireFromString("5")) {
		t.Fatalf("null must leave value unchanged, got %s", d.Decimal)
	}
}

func TestDecimalNumber_ZeroValueMarshalsZero(t *testing.T) {
	var d DecimalNumber
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal zero value: %v", err)
	}
	if string(b) != "0" {
		t.Fatalf("zero value must marshal as 0, got %s", b)
	}
}

func TestDecimalNumber_NegativeRoundTrip(t *testing.T) {
	d := DecimalNumber{decimal.RequireFromString("-1.5")}
	b, _ := json.Marshal(d)
	if string(b) != "-1.5" {
		t.Fatalf("want -1.5, got %s", b)
	}
	var back DecimalNumber
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Decimal.Equal(d.Decimal) {
		t.Fatalf("round-trip mismatch: %s", back.Decimal)
	}
}
