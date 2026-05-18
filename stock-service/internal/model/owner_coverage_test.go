package model

import "testing"

func TestOwnerType_Valid(t *testing.T) {
	cases := []struct {
		t    OwnerType
		want bool
	}{
		{OwnerClient, true},
		{OwnerBank, true},
		{OwnerType(""), false},
		{OwnerType("employee"), false},
		{OwnerType("BANK"), false},
	}
	for _, c := range cases {
		if got := c.t.Valid(); got != c.want {
			t.Errorf("OwnerType(%q).Valid() = %v, want %v", c.t, got, c.want)
		}
	}
}

func TestOwnerType_String(t *testing.T) {
	if got := OwnerClient.String(); got != "client" {
		t.Errorf("got %q want client", got)
	}
	if got := OwnerBank.String(); got != "bank" {
		t.Errorf("got %q want bank", got)
	}
}

func TestValidateOwner_InvalidType(t *testing.T) {
	id := uint64(1)
	if err := ValidateOwner(OwnerType("foo"), &id); err == nil {
		t.Fatal("expected error for invalid owner type")
	}
}

func TestValidateOwner_BankWithID(t *testing.T) {
	id := uint64(1)
	if err := ValidateOwner(OwnerBank, &id); err == nil {
		t.Fatal("expected error: bank owner with non-nil id")
	}
}

func TestValidateOwner_BankNilID(t *testing.T) {
	if err := ValidateOwner(OwnerBank, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOwner_ClientWithID(t *testing.T) {
	id := uint64(99)
	if err := ValidateOwner(OwnerClient, &id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOwner_ClientNilID(t *testing.T) {
	if err := ValidateOwner(OwnerClient, nil); err == nil {
		t.Fatal("expected error: client with nil id")
	}
}

func TestOwnerIDOrZero_Nil(t *testing.T) {
	if got := OwnerIDOrZero(nil); got != 0 {
		t.Errorf("got %d want 0", got)
	}
}

func TestOwnerIDOrZero_Set(t *testing.T) {
	id := uint64(42)
	if got := OwnerIDOrZero(&id); got != 42 {
		t.Errorf("got %d want 42", got)
	}
}

func TestOwnerFromLegacy_Bank(t *testing.T) {
	tp, id := OwnerFromLegacy(0, "bank")
	if tp != OwnerBank {
		t.Errorf("got %q want bank", tp)
	}
	if id != nil {
		t.Errorf("expected nil id for bank, got %v", *id)
	}
}

func TestOwnerFromLegacy_Client(t *testing.T) {
	tp, id := OwnerFromLegacy(7, "client")
	if tp != OwnerClient {
		t.Errorf("got %q want client", tp)
	}
	if id == nil || *id != 7 {
		t.Fatalf("expected id 7, got %v", id)
	}
}

func TestOwnerFromLegacy_Employee(t *testing.T) {
	// any non-bank system_type maps to client
	tp, id := OwnerFromLegacy(13, "employee")
	if tp != OwnerClient {
		t.Errorf("got %q want client", tp)
	}
	if id == nil || *id != 13 {
		t.Fatalf("expected id 13, got %v", id)
	}
}
