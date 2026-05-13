// api-gateway/internal/handler/changelog_handler_test.go
package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	accountpb "github.com/exbanka/contract/accountpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	userpb "github.com/exbanka/contract/userpb"
)

// changelogAccountStub overrides ListChangelog on accountFullStub to allow
// tests to assert on the request and inject responses.
type changelogAccountStub struct {
	accountFullStub
	listFn func(*accountpb.ListChangelogRequest) (*accountpb.ListChangelogResponse, error)
}

func (s *changelogAccountStub) ListChangelog(_ context.Context, in *accountpb.ListChangelogRequest, _ ...grpc.CallOption) (*accountpb.ListChangelogResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &accountpb.ListChangelogResponse{}, nil
}

type changelogCardStub struct {
	stubCardClient
	listFn func(*cardpb.ListChangelogRequest) (*cardpb.ListChangelogResponse, error)
}

func (s *changelogCardStub) ListChangelog(_ context.Context, in *cardpb.ListChangelogRequest, _ ...grpc.CallOption) (*cardpb.ListChangelogResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &cardpb.ListChangelogResponse{}, nil
}

type changelogClientStub struct {
	stubClientClient
	listFn func(*clientpb.ListChangelogRequest) (*clientpb.ListChangelogResponse, error)
}

func (s *changelogClientStub) ListChangelog(_ context.Context, in *clientpb.ListChangelogRequest, _ ...grpc.CallOption) (*clientpb.ListChangelogResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &clientpb.ListChangelogResponse{}, nil
}

type changelogCreditStub struct {
	stubCreditClient
	listFn func(*creditpb.ListChangelogRequest) (*creditpb.ListChangelogResponse, error)
}

func (s *changelogCreditStub) ListChangelog(_ context.Context, in *creditpb.ListChangelogRequest, _ ...grpc.CallOption) (*creditpb.ListChangelogResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &creditpb.ListChangelogResponse{}, nil
}

type changelogUserStub struct {
	stubUserClient
	listFn func(*userpb.ListChangelogRequest) (*userpb.ListChangelogResponse, error)
}

func (s *changelogUserStub) ListChangelog(_ context.Context, in *userpb.ListChangelogRequest, _ ...grpc.CallOption) (*userpb.ListChangelogResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &userpb.ListChangelogResponse{}, nil
}

func changelogRouter(h *handler.ChangelogHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/accounts/:id/changelog", h.GetAccountChangelog)
	r.GET("/cards/:id/changelog", h.GetCardChangelog)
	r.GET("/clients/:id/changelog", h.GetClientChangelog)
	r.GET("/loans/:id/changelog", h.GetLoanChangelog)
	r.GET("/employees/:id/changelog", h.GetEmployeeChangelog)
	return r
}

func TestChangelog_GetAccountChangelog_Success(t *testing.T) {
	acct := &changelogAccountStub{
		listFn: func(in *accountpb.ListChangelogRequest) (*accountpb.ListChangelogResponse, error) {
			require.Equal(t, "account", in.EntityType)
			require.Equal(t, int64(7), in.EntityId)
			require.Equal(t, int32(2), in.Page)
			require.Equal(t, int32(50), in.PageSize)
			return &accountpb.ListChangelogResponse{
				Total: 1,
				Entries: []*accountpb.ChangelogEntry{
					{Id: 1, EntityType: "account", EntityId: 7, Action: "update", FieldName: "balance", OldValue: "100", NewValue: "200", ChangedBy: 5, ChangedAt: 1714000000, Reason: "deposit"},
				},
			}, nil
		},
	}
	h := handler.NewChangelogHandler(acct, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)

	req := httptest.NewRequest("GET", "/accounts/7/changelog?page=2&page_size=50", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.EqualValues(t, 1, got["total"])
	require.EqualValues(t, 2, got["page"])
	require.EqualValues(t, 50, got["page_size"])
	entries, ok := got["entries"].([]any)
	require.True(t, ok)
	require.Len(t, entries, 1)
}

func TestChangelog_GetAccountChangelog_BadID(t *testing.T) {
	h := handler.NewChangelogHandler(&changelogAccountStub{}, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)

	for _, raw := range []string{"abc", "0", "-3"} {
		req := httptest.NewRequest("GET", "/accounts/"+raw+"/changelog", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "raw=%s", raw)
	}
}

func TestChangelog_GetAccountChangelog_DefaultsAndClamps(t *testing.T) {
	var captured *accountpb.ListChangelogRequest
	acct := &changelogAccountStub{
		listFn: func(in *accountpb.ListChangelogRequest) (*accountpb.ListChangelogResponse, error) {
			captured = in
			return &accountpb.ListChangelogResponse{}, nil
		},
	}
	h := handler.NewChangelogHandler(acct, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)

	// page=0 clamped to 1, page_size=999 clamped to 200
	req := httptest.NewRequest("GET", "/accounts/1/changelog?page=0&page_size=999", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, int32(1), captured.Page)
	require.Equal(t, int32(200), captured.PageSize)
}

func TestChangelog_GetAccountChangelog_GRPCError(t *testing.T) {
	acct := &changelogAccountStub{
		listFn: func(*accountpb.ListChangelogRequest) (*accountpb.ListChangelogResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "nope")
		},
	}
	h := handler.NewChangelogHandler(acct, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)

	req := httptest.NewRequest("GET", "/accounts/1/changelog", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestChangelog_GetCardChangelog_Success(t *testing.T) {
	card := &changelogCardStub{
		listFn: func(in *cardpb.ListChangelogRequest) (*cardpb.ListChangelogResponse, error) {
			require.Equal(t, "card", in.EntityType)
			return &cardpb.ListChangelogResponse{Total: 0}, nil
		},
	}
	h := handler.NewChangelogHandler(&changelogAccountStub{}, card, &changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)
	req := httptest.NewRequest("GET", "/cards/3/changelog", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestChangelog_GetClientChangelog_Success(t *testing.T) {
	cli := &changelogClientStub{
		listFn: func(in *clientpb.ListChangelogRequest) (*clientpb.ListChangelogResponse, error) {
			require.Equal(t, "client", in.EntityType)
			return &clientpb.ListChangelogResponse{}, nil
		},
	}
	h := handler.NewChangelogHandler(&changelogAccountStub{}, &changelogCardStub{}, cli, &changelogCreditStub{}, &changelogUserStub{})
	r := changelogRouter(h)
	req := httptest.NewRequest("GET", "/clients/9/changelog", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestChangelog_GetLoanChangelog_Success(t *testing.T) {
	cr := &changelogCreditStub{
		listFn: func(in *creditpb.ListChangelogRequest) (*creditpb.ListChangelogResponse, error) {
			require.Equal(t, "loan", in.EntityType)
			return &creditpb.ListChangelogResponse{}, nil
		},
	}
	h := handler.NewChangelogHandler(&changelogAccountStub{}, &changelogCardStub{}, &changelogClientStub{}, cr, &changelogUserStub{})
	r := changelogRouter(h)
	req := httptest.NewRequest("GET", "/loans/12/changelog", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestChangelog_GetEmployeeChangelog_Success(t *testing.T) {
	user := &changelogUserStub{
		listFn: func(in *userpb.ListChangelogRequest) (*userpb.ListChangelogResponse, error) {
			require.Equal(t, "employee", in.EntityType)
			require.Equal(t, int64(33), in.EntityId)
			return &userpb.ListChangelogResponse{}, nil
		},
	}
	h := handler.NewChangelogHandler(&changelogAccountStub{}, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{}, user)
	r := changelogRouter(h)
	req := httptest.NewRequest("GET", "/employees/33/changelog", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestChangelog_GRPCErrors_FromAllEndpoints(t *testing.T) {
	tests := []struct {
		name string
		path string
		set  func() *handler.ChangelogHandler
	}{
		{
			name: "card",
			path: "/cards/1/changelog",
			set: func() *handler.ChangelogHandler {
				return handler.NewChangelogHandler(
					&changelogAccountStub{},
					&changelogCardStub{listFn: func(*cardpb.ListChangelogRequest) (*cardpb.ListChangelogResponse, error) {
						return nil, status.Error(codes.NotFound, "missing")
					}},
					&changelogClientStub{}, &changelogCreditStub{}, &changelogUserStub{},
				)
			},
		},
		{
			name: "client",
			path: "/clients/1/changelog",
			set: func() *handler.ChangelogHandler {
				return handler.NewChangelogHandler(
					&changelogAccountStub{}, &changelogCardStub{},
					&changelogClientStub{listFn: func(*clientpb.ListChangelogRequest) (*clientpb.ListChangelogResponse, error) {
						return nil, status.Error(codes.Internal, "boom")
					}},
					&changelogCreditStub{}, &changelogUserStub{},
				)
			},
		},
		{
			name: "loan",
			path: "/loans/1/changelog",
			set: func() *handler.ChangelogHandler {
				return handler.NewChangelogHandler(
					&changelogAccountStub{}, &changelogCardStub{}, &changelogClientStub{},
					&changelogCreditStub{listFn: func(*creditpb.ListChangelogRequest) (*creditpb.ListChangelogResponse, error) {
						return nil, status.Error(codes.Unavailable, "down")
					}},
					&changelogUserStub{},
				)
			},
		},
		{
			name: "employee",
			path: "/employees/1/changelog",
			set: func() *handler.ChangelogHandler {
				return handler.NewChangelogHandler(
					&changelogAccountStub{}, &changelogCardStub{}, &changelogClientStub{}, &changelogCreditStub{},
					&changelogUserStub{listFn: func(*userpb.ListChangelogRequest) (*userpb.ListChangelogResponse, error) {
						return nil, status.Error(codes.InvalidArgument, "bad")
					}},
				)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := changelogRouter(tc.set())
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
			require.Less(t, rec.Code, 600)
		})
	}
}
