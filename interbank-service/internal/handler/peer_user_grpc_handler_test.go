package handler_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientpb "github.com/exbanka/contract/clientpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/interbank-service/internal/handler"
)

type fakeClientSvc struct {
	clientpb.ClientServiceClient
	resp *clientpb.ClientResponse
	err  error
}

func (f *fakeClientSvc) GetClient(_ context.Context, _ *clientpb.GetClientRequest, _ ...grpc.CallOption) (*clientpb.ClientResponse, error) {
	return f.resp, f.err
}

type fakeUserSvc struct {
	userpb.UserServiceClient
	resp *userpb.EmployeeResponse
	err  error
}

func (f *fakeUserSvc) GetEmployee(_ context.Context, _ *userpb.GetEmployeeRequest, _ ...grpc.CallOption) (*userpb.EmployeeResponse, error) {
	return f.resp, f.err
}

func newUserResolver(c clientpb.ClientServiceClient, u userpb.UserServiceClient) *handler.PeerUserGRPCHandler {
	return handler.NewPeerUserGRPCHandler(c, u, 111, "EXBanka")
}

func TestResolvePeerUser_Client(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{resp: &clientpb.ClientResponse{FirstName: "Ana", LastName: "Anic"}},
		&fakeUserSvc{},
	)
	resp, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "client-7"})
	if err != nil {
		t.Fatalf("ResolvePeerUser: %v", err)
	}
	if !resp.GetFound() || resp.GetDisplayName() != "Ana Anic" || resp.GetBankDisplayName() != "EXBanka" {
		t.Errorf("got %+v, want found/Ana Anic/EXBanka", resp)
	}
}

func TestResolvePeerUser_Employee(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{},
		&fakeUserSvc{resp: &userpb.EmployeeResponse{FirstName: "Boban", LastName: "Bobic"}},
	)
	resp, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "employee-3"})
	if err != nil {
		t.Fatalf("ResolvePeerUser: %v", err)
	}
	if !resp.GetFound() || resp.GetDisplayName() != "Boban Bobic" {
		t.Errorf("got %+v, want found/Boban Bobic", resp)
	}
}

func TestResolvePeerUser_ForeignRouting_NotFound(t *testing.T) {
	h := newUserResolver(&fakeClientSvc{}, &fakeUserSvc{})
	resp, _ := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 222, Id: "client-7"})
	if resp.GetFound() {
		t.Errorf("foreign routing must yield found=false")
	}
}

func TestResolvePeerUser_UnknownClient_NotFound(t *testing.T) {
	h := newUserResolver(
		&fakeClientSvc{err: status.Error(codes.NotFound, "no such client")},
		&fakeUserSvc{},
	)
	resp, err := h.ResolvePeerUser(context.Background(), &transactionpb.ResolvePeerUserRequest{RoutingNumber: 111, Id: "client-99"})
	if err != nil {
		t.Fatalf("NotFound from downstream must not error: %v", err)
	}
	if resp.GetFound() {
		t.Errorf("unknown client must yield found=false")
	}
}
