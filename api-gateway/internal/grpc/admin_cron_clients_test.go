package grpc

import "testing"

// NewAdminCronClient has a distinct (service, addr) signature and labels the
// returned client with the service name, so it gets its own test.
func TestNewAdminCronClient(t *testing.T) {
	cl, err := NewAdminCronClient("stock-service", dummyAddr)
	if err != nil {
		t.Fatalf("NewAdminCronClient: %v", err)
	}
	if cl == nil || cl.Client == nil || cl.Conn == nil {
		t.Fatal("nil client/conn")
	}
	if cl.Service != "stock-service" {
		t.Fatalf("Service = %q, want stock-service", cl.Service)
	}
	_ = cl.Conn.Close()
}
