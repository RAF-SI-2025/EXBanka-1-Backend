package identity

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// roundTrip simulates the gRPC transport moving outgoing metadata to the
// incoming side of the callee.
func roundTrip(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestInjectFromIncoming_Client(t *testing.T) {
	ctx := Inject(context.Background(), Caller{PrincipalType: PrincipalClient, PrincipalID: 42})
	got := FromIncoming(roundTrip(ctx))
	if !got.IsClient() || got.PrincipalID != 42 {
		t.Fatalf("client round-trip wrong: %+v", got)
	}
	if got.IsService() || got.IsEmployee() {
		t.Fatalf("client must not be service/employee: %+v", got)
	}
}

func TestInjectFromIncoming_EmployeeOnBehalf(t *testing.T) {
	ctx := Inject(context.Background(), Caller{PrincipalType: PrincipalEmployee, PrincipalID: 7, OnBehalfClientID: 99})
	got := FromIncoming(roundTrip(ctx))
	if !got.IsEmployee() || got.PrincipalID != 7 || got.OnBehalfClientID != 99 {
		t.Fatalf("employee on-behalf round-trip wrong: %+v", got)
	}
}

func TestOwnsResource(t *testing.T) {
	cases := []struct {
		name    string
		caller  Caller
		ownerID int64
		want    bool
	}{
		{"client owns own", Caller{PrincipalType: PrincipalClient, PrincipalID: 5}, 5, true},
		{"client not others", Caller{PrincipalType: PrincipalClient, PrincipalID: 5}, 6, false},
		{"employee admin any", Caller{PrincipalType: PrincipalEmployee, PrincipalID: 9}, 6, true},
		{"employee on-behalf match", Caller{PrincipalType: PrincipalEmployee, PrincipalID: 9, OnBehalfClientID: 6}, 6, true},
		{"employee on-behalf mismatch", Caller{PrincipalType: PrincipalEmployee, PrincipalID: 9, OnBehalfClientID: 6}, 7, false},
		{"service any", Caller{}, 6, true},
	}
	for _, tc := range cases {
		if got := tc.caller.OwnsResource(tc.ownerID); got != tc.want {
			t.Errorf("%s: OwnsResource(%d)=%v want %v", tc.name, tc.ownerID, got, tc.want)
		}
	}
}

func TestAbsentIdentity_IsService(t *testing.T) {
	// No metadata at all → trusted service call (backward-compat for money RPCs).
	got := FromIncoming(context.Background())
	if !got.IsService() {
		t.Fatalf("absent identity must be a service call: %+v", got)
	}
	// Explicit service principal injects nothing and still reads as service.
	ctx := Inject(context.Background(), Caller{PrincipalType: PrincipalService})
	if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md) > 0 {
		t.Fatalf("service caller should inject no metadata, got %v", md)
	}
}
