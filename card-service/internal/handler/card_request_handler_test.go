package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/card-service/internal/model"
	"github.com/exbanka/card-service/internal/service"
	pb "github.com/exbanka/contract/cardpb"
)

func sampleRequest() *model.CardRequest {
	return &model.CardRequest{
		ID:            10,
		ClientID:      1,
		AccountNumber: "265000000000000001",
		CardBrand:     "visa",
		CardType:      "debit",
		Status:        "pending",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
}

// ---------------------------------------------------------------------------
// CreateCardRequest
// ---------------------------------------------------------------------------

func TestCreateCardRequest_Success(t *testing.T) {
	svc := &stubCardRequestService{
		createRequestFn: func(_ context.Context, req *model.CardRequest) error {
			req.ID = 10
			req.Status = "pending"
			return nil
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.CreateCardRequest(context.Background(), &pb.CreateCardRequestRequest{
		ClientId:      1,
		AccountNumber: "265000000000000001",
		CardBrand:     "visa",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(10), resp.Id)
	assert.Equal(t, "pending", resp.Status)
}

func TestCreateCardRequest_InvalidBrand(t *testing.T) {
	svc := &stubCardRequestService{
		createRequestFn: func(_ context.Context, _ *model.CardRequest) error {
			return service.ErrInvalidCard
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.CreateCardRequest(context.Background(), &pb.CreateCardRequestRequest{CardBrand: "discover"})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// ---------------------------------------------------------------------------
// GetCardRequest
// ---------------------------------------------------------------------------

func TestGetCardRequest_Found(t *testing.T) {
	svc := &stubCardRequestService{
		getRequestFn: func(id uint64) (*model.CardRequest, error) {
			r := sampleRequest()
			r.ID = id
			return r, nil
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.GetCardRequest(context.Background(), &pb.GetCardRequestRequest{Id: 10})
	require.NoError(t, err)
	assert.Equal(t, uint64(10), resp.Id)
}

func TestGetCardRequest_NotFound(t *testing.T) {
	svc := &stubCardRequestService{
		getRequestFn: func(_ uint64) (*model.CardRequest, error) { return nil, gorm.ErrRecordNotFound },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.GetCardRequest(context.Background(), &pb.GetCardRequestRequest{Id: 999})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Equal(t, "card request not found", st.Message())
}

func TestGetCardRequest_GenericError(t *testing.T) {
	svc := &stubCardRequestService{
		getRequestFn: func(_ uint64) (*model.CardRequest, error) { return nil, errors.New("db died") },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.GetCardRequest(context.Background(), &pb.GetCardRequestRequest{Id: 1})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Unknown, st.Code())
}

// ---------------------------------------------------------------------------
// ListCardRequests
// ---------------------------------------------------------------------------

func TestListCardRequests_DefaultPagination(t *testing.T) {
	var capturedPage, capturedPageSize int
	svc := &stubCardRequestService{
		listRequestsFn: func(_ string, page, pageSize int) ([]model.CardRequest, int64, error) {
			capturedPage = page
			capturedPageSize = pageSize
			return []model.CardRequest{*sampleRequest()}, 1, nil
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.ListCardRequests(context.Background(), &pb.ListCardRequestsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Requests, 1)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, 1, capturedPage, "page must default to 1 when zero")
	assert.Equal(t, 20, capturedPageSize, "pageSize must default to 20 when zero")
}

func TestListCardRequests_PassesThroughPagination(t *testing.T) {
	var capturedPage, capturedPageSize int
	svc := &stubCardRequestService{
		listRequestsFn: func(_ string, page, pageSize int) ([]model.CardRequest, int64, error) {
			capturedPage = page
			capturedPageSize = pageSize
			return nil, 0, nil
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ListCardRequests(context.Background(), &pb.ListCardRequestsRequest{Page: 3, PageSize: 50})
	require.NoError(t, err)
	assert.Equal(t, 3, capturedPage)
	assert.Equal(t, 50, capturedPageSize)
}

func TestListCardRequests_Error(t *testing.T) {
	svc := &stubCardRequestService{
		listRequestsFn: func(_ string, _, _ int) ([]model.CardRequest, int64, error) {
			return nil, 0, errors.New("boom")
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ListCardRequests(context.Background(), &pb.ListCardRequestsRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// ListCardRequestsByClient
// ---------------------------------------------------------------------------

func TestListCardRequestsByClient_DefaultPagination(t *testing.T) {
	var capturedPage, capturedPageSize int
	svc := &stubCardRequestService{
		listByClientFn: func(_ uint64, page, pageSize int) ([]model.CardRequest, int64, error) {
			capturedPage = page
			capturedPageSize = pageSize
			return []model.CardRequest{*sampleRequest()}, 1, nil
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.ListCardRequestsByClient(context.Background(), &pb.ListCardRequestsByClientRequest{ClientId: 1})
	require.NoError(t, err)
	assert.Len(t, resp.Requests, 1)
	assert.Equal(t, 1, capturedPage)
	assert.Equal(t, 20, capturedPageSize)
}

func TestListCardRequestsByClient_Error(t *testing.T) {
	svc := &stubCardRequestService{
		listByClientFn: func(_ uint64, _, _ int) ([]model.CardRequest, int64, error) {
			return nil, 0, errors.New("boom")
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ListCardRequestsByClient(context.Background(), &pb.ListCardRequestsByClientRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

// ---------------------------------------------------------------------------
// ApproveCardRequest
// ---------------------------------------------------------------------------

func TestApproveCardRequest_Success(t *testing.T) {
	approved := sampleRequest()
	approved.Status = "approved"

	svc := &stubCardRequestService{
		approveRequestFn: func(_ context.Context, _, _ uint64) (*model.Card, error) {
			return sampleCard(), nil
		},
		getRequestFn: func(_ uint64) (*model.CardRequest, error) { return approved, nil },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.ApproveCardRequest(context.Background(), &pb.ApproveCardRequestRequest{Id: 10, EmployeeId: 100})
	require.NoError(t, err)
	require.NotNil(t, resp.Card)
	require.NotNil(t, resp.Request)
	assert.Equal(t, "approved", resp.Request.Status)
	assert.Equal(t, uint64(7), resp.Card.Id)
}

func TestApproveCardRequest_AlreadyApproved(t *testing.T) {
	svc := &stubCardRequestService{
		approveRequestFn: func(_ context.Context, _, _ uint64) (*model.Card, error) {
			return nil, service.ErrCardRequestAlreadyDecided
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ApproveCardRequest(context.Background(), &pb.ApproveCardRequestRequest{Id: 10, EmployeeId: 100})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestApproveCardRequest_NotFound(t *testing.T) {
	svc := &stubCardRequestService{
		approveRequestFn: func(_ context.Context, _, _ uint64) (*model.Card, error) {
			return nil, service.ErrCardRequestNotFound
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ApproveCardRequest(context.Background(), &pb.ApproveCardRequestRequest{Id: 999})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestApproveCardRequest_FetchUpdatedFails(t *testing.T) {
	svc := &stubCardRequestService{
		approveRequestFn: func(_ context.Context, _, _ uint64) (*model.Card, error) { return sampleCard(), nil },
		getRequestFn:     func(_ uint64) (*model.CardRequest, error) { return nil, errors.New("fetch died") },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.ApproveCardRequest(context.Background(), &pb.ApproveCardRequestRequest{Id: 10})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to fetch updated card request")
}

// ---------------------------------------------------------------------------
// RejectCardRequest
// ---------------------------------------------------------------------------

func TestRejectCardRequest_Success(t *testing.T) {
	rejected := sampleRequest()
	rejected.Status = "rejected"
	rejected.Reason = "missing docs"

	svc := &stubCardRequestService{
		rejectRequestFn: func(_ context.Context, _, _ uint64, _ string) error { return nil },
		getRequestFn:    func(_ uint64) (*model.CardRequest, error) { return rejected, nil },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	resp, err := h.RejectCardRequest(context.Background(), &pb.RejectCardRequestRequest{
		Id: 10, EmployeeId: 100, Reason: "missing docs",
	})
	require.NoError(t, err)
	assert.Equal(t, "rejected", resp.Status)
	assert.Equal(t, "missing docs", resp.Reason)
}

func TestRejectCardRequest_AlreadyApproved(t *testing.T) {
	svc := &stubCardRequestService{
		rejectRequestFn: func(_ context.Context, _, _ uint64, _ string) error {
			return service.ErrCardRequestAlreadyDecided
		},
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.RejectCardRequest(context.Background(), &pb.RejectCardRequestRequest{Id: 10, EmployeeId: 100})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestRejectCardRequest_FetchUpdatedFails(t *testing.T) {
	svc := &stubCardRequestService{
		rejectRequestFn: func(_ context.Context, _, _ uint64, _ string) error { return nil },
		getRequestFn:    func(_ uint64) (*model.CardRequest, error) { return nil, errors.New("fetch died") },
	}
	h := &CardRequestGRPCHandler{cardRequestSvc: svc}
	_, err := h.RejectCardRequest(context.Background(), &pb.RejectCardRequestRequest{Id: 10})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}
