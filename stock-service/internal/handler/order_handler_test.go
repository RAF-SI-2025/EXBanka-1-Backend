package handler

import "testing"

func TestResolveOrderOwner_ClientSelfPlaced(t *testing.T) {
	uid, st, err := resolveOrderOwner(42, "client", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 42 || st != "client" {
		t.Errorf("client self-placed: got (%d,%q), want (42,\"client\")", uid, st)
	}
}

func TestResolveOrderOwner_EmployeeSelfPlaced(t *testing.T) {
	uid, st, err := resolveOrderOwner(21, "employee", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 21 || st != "employee" {
		t.Errorf("employee self-placed: got (%d,%q), want (21,\"employee\")", uid, st)
	}
}

func TestResolveOrderOwner_EmployeeOnBehalfOfClient_FlipsSystemType(t *testing.T) {
	// Gateway passes UserId=<clientID> and SystemType="employee" along with
	// ActingEmployeeId + OnBehalfOfClientId. The order + holding must land
	// under system_type="client" so the client sees it in /me/portfolio.
	uid, st, err := resolveOrderOwner(999 /* junk */, "employee", 17 /* acting employee */, 42 /* client */)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 42 {
		t.Errorf("target user id: got %d, want 42 (client)", uid)
	}
	if st != "client" {
		t.Errorf("target system_type: got %q, want %q", st, "client")
	}
}

func TestResolveOrderOwner_ActingEmployeeWithoutClient_Fails(t *testing.T) {
	_, _, err := resolveOrderOwner(0, "client", 17, 0)
	if err == nil {
		t.Fatal("expected error when ActingEmployeeId is set without OnBehalfOfClientId on a non-bank request")
	}
}

// Plan C: ResolveIdentity middleware on /me/* trading routes resolves
// employee callers to bank ownership (UserId=0, SystemType="bank") with
// ActingEmployeeId set. resolveOrderOwner must accept this pattern as
// "employee places for the bank" rather than rejecting it.
func TestResolveOrderOwner_EmployeeAsBank_Accepted(t *testing.T) {
	uid, st, err := resolveOrderOwner(0, "bank", 17 /* acting employee */, 0)
	if err != nil {
		t.Fatalf("expected success for employee-as-bank, got: %v", err)
	}
	if uid != 0 || st != "bank" {
		t.Errorf("employee-as-bank: got (%d,%q), want (0,\"bank\")", uid, st)
	}
}
