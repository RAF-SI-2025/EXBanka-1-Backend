// Package handler — PeerUserGRPCHandler resolves one of THIS bank's users
// (client-<N> / employee-<N>) to display names for a peer's friendly-name lookup
// (SI-TX §9, the /user/{rid}/{id} surface). interbank-service serves it by
// forwarding to client-service / user-service so the gateway routes the whole
// /cross-bank-protocol surface to one backend. Mirrors the gateway's prior
// PeerUserHandler logic verbatim (prefix dispatch, NotFound-tolerant, own-routing gate).
package handler

import (
	"context"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientpb "github.com/exbanka/contract/clientpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
)

// PeerUserGRPCHandler implements transactionpb.PeerUserServiceServer.
type PeerUserGRPCHandler struct {
	transactionpb.UnimplementedPeerUserServiceServer
	clientClient       clientpb.ClientServiceClient
	userClient         userpb.UserServiceClient
	ownRouting         int64
	ownBankDisplayName string
}

// NewPeerUserGRPCHandler builds the resolver over the client + user services.
func NewPeerUserGRPCHandler(c clientpb.ClientServiceClient, u userpb.UserServiceClient, ownRouting int64, ownBankDisplayName string) *PeerUserGRPCHandler {
	return &PeerUserGRPCHandler{clientClient: c, userClient: u, ownRouting: ownRouting, ownBankDisplayName: ownBankDisplayName}
}

// ResolvePeerUser maps a (routing, id) to display names. A foreign routing or an
// unknown/ill-formed id yields found=false (the gateway renders that as 404). A
// real downstream error (other than NotFound) is surfaced as a gRPC error.
func (h *PeerUserGRPCHandler) ResolvePeerUser(ctx context.Context, req *transactionpb.ResolvePeerUserRequest) (*transactionpb.ResolvePeerUserResponse, error) {
	notFound := &transactionpb.ResolvePeerUserResponse{Found: false}
	if req.GetRoutingNumber() != h.ownRouting {
		return notFound, nil
	}
	id := req.GetId()

	if strings.HasPrefix(id, "client-") {
		if clientID, perr := strconv.ParseUint(strings.TrimPrefix(id, "client-"), 10, 64); perr == nil {
			resp, lerr := h.clientClient.GetClient(ctx, &clientpb.GetClientRequest{Id: clientID})
			if lerr == nil && resp != nil {
				return &transactionpb.ResolvePeerUserResponse{
					Found:           true,
					BankDisplayName: h.ownBankDisplayName,
					DisplayName:     resp.GetFirstName() + " " + resp.GetLastName(),
				}, nil
			}
			if st, ok := status.FromError(lerr); ok && st.Code() != codes.NotFound {
				return nil, lerr
			}
		}
	}

	if strings.HasPrefix(id, "employee-") {
		if empID, perr := strconv.ParseInt(strings.TrimPrefix(id, "employee-"), 10, 64); perr == nil {
			resp, lerr := h.userClient.GetEmployee(ctx, &userpb.GetEmployeeRequest{Id: empID})
			if lerr == nil && resp != nil {
				return &transactionpb.ResolvePeerUserResponse{
					Found:           true,
					BankDisplayName: h.ownBankDisplayName,
					DisplayName:     resp.GetFirstName() + " " + resp.GetLastName(),
				}, nil
			}
			if st, ok := status.FromError(lerr); ok && st.Code() != codes.NotFound {
				return nil, lerr
			}
		}
	}

	return notFound, nil
}
