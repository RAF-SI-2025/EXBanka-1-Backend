package templates

import "testing"

func TestGet_UnknownReturnsFalse(t *testing.T) {
	if _, ok := Get("NO_SUCH_TYPE", "email"); ok {
		t.Error("Get on unknown type should return ok=false")
	}
	// Known type but wrong channel.
	if _, ok := Get("CONFIRMATION", "push"); ok {
		t.Error("Get on known type with wrong channel should return ok=false")
	}
}

func TestKnownVars_UnknownReturnsFalse(t *testing.T) {
	if set, ok := KnownVars("NO_SUCH_TYPE", "email"); ok || set != nil {
		t.Errorf("KnownVars on unknown type: want (nil,false), got (%v,%v)", set, ok)
	}
}

func TestKnownVars_KnownReturnsDeclaredSet(t *testing.T) {
	set, ok := KnownVars("CONFIRMATION", "email")
	if !ok {
		t.Fatal("CONFIRMATION/email should be known")
	}
	if len(set) == 0 {
		t.Fatal("expected a non-empty variable set")
	}
	// Every declared variable name must be present in the set.
	def, _ := Get("CONFIRMATION", "email")
	for _, v := range def.Variables {
		if !set[v.Name] {
			t.Errorf("declared variable %q missing from KnownVars set", v.Name)
		}
	}
}
