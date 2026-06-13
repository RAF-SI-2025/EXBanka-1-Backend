package handler_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientpb "github.com/exbanka/contract/clientpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
)

// TestResolvePeerUser_ClientRealError_Surfaces verifies a downstream error
// OTHER than NotFound (e.g. the client-service is down) is surfaced as a gRPC
// error rather than masked as found=false.
func TestResolvePeerUser_ClientRealError_Surfaces(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{err: status.Error(codes.Unavailable, "client-service down")},
		&fakeUserSvc{},
	)
	_, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "client-7"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable surfaced, got %v", err)
	}
}

// TestResolvePeerUser_EmployeeRealError_Surfaces is the employee-branch mirror:
// a non-NotFound error from user-service is surfaced.
func TestResolvePeerUser_EmployeeRealError_Surfaces(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{},
		&fakeUserSvc{err: status.Error(codes.Internal, "user-service boom")},
	)
	_, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "employee-3"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal surfaced, got %v", err)
	}
}

// TestResolvePeerUser_UnknownEmployee_NotFound verifies a NotFound from
// user-service is tolerated (found=false, no error) — the employee-branch
// counterpart of the existing unknown-client test.
func TestResolvePeerUser_UnknownEmployee_NotFound(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{},
		&fakeUserSvc{err: status.Error(codes.NotFound, "no such employee")},
	)
	resp, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "employee-99"})
	if err != nil {
		t.Fatalf("NotFound must not error: %v", err)
	}
	if resp.GetFound() {
		t.Errorf("unknown employee must yield found=false")
	}
}

// TestResolvePeerUser_IllFormedId_NotFound verifies a non-numeric participant id
// yields found=false without any downstream lookup.
func TestResolvePeerUser_IllFormedId_NotFound(t *testing.T) {
	h := newUserResolver(&fakeClientSvc{resp: &clientpb.ClientResponse{}}, &fakeUserSvc{resp: &userpb.EmployeeResponse{}})
	resp, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "client-abc"})
	if err != nil {
		t.Fatalf("ill-formed id must not error: %v", err)
	}
	if resp.GetFound() {
		t.Errorf("ill-formed id must yield found=false")
	}
}
