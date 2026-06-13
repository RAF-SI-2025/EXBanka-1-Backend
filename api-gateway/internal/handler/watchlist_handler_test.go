package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	stockpb "github.com/exbanka/contract/stockpb"
)

// stubWatchlistClient implements stockpb.WatchlistServiceClient with per-method
// function fields so each test can shape the canned response / error.
type stubWatchlistClient struct {
	addFn      func(*stockpb.AddWatchlistItemRequest) (*stockpb.WatchlistItemResponse, error)
	removeFn   func(*stockpb.RemoveWatchlistItemRequest) (*stockpb.RemoveWatchlistItemResponse, error)
	listMyFn   func(*stockpb.ListMyWatchlistRequest) (*stockpb.ListMyWatchlistResponse, error)
	createWLFn func(*stockpb.CreateWatchlistRequest) (*stockpb.WatchlistResponse, error)
	listWLFn   func(*stockpb.ListWatchlistsRequest) (*stockpb.ListWatchlistsResponse, error)
	deleteWLFn func(*stockpb.DeleteWatchlistRequest) (*stockpb.DeleteWatchlistResponse, error)
}

func (s *stubWatchlistClient) AddItem(_ context.Context, in *stockpb.AddWatchlistItemRequest, _ ...grpc.CallOption) (*stockpb.WatchlistItemResponse, error) {
	if s.addFn != nil {
		return s.addFn(in)
	}
	return &stockpb.WatchlistItemResponse{}, nil
}
func (s *stubWatchlistClient) RemoveItem(_ context.Context, in *stockpb.RemoveWatchlistItemRequest, _ ...grpc.CallOption) (*stockpb.RemoveWatchlistItemResponse, error) {
	if s.removeFn != nil {
		return s.removeFn(in)
	}
	return &stockpb.RemoveWatchlistItemResponse{}, nil
}
func (s *stubWatchlistClient) ListMy(_ context.Context, in *stockpb.ListMyWatchlistRequest, _ ...grpc.CallOption) (*stockpb.ListMyWatchlistResponse, error) {
	if s.listMyFn != nil {
		return s.listMyFn(in)
	}
	return &stockpb.ListMyWatchlistResponse{}, nil
}
func (s *stubWatchlistClient) CreateWatchlist(_ context.Context, in *stockpb.CreateWatchlistRequest, _ ...grpc.CallOption) (*stockpb.WatchlistResponse, error) {
	if s.createWLFn != nil {
		return s.createWLFn(in)
	}
	return &stockpb.WatchlistResponse{}, nil
}
func (s *stubWatchlistClient) ListWatchlists(_ context.Context, in *stockpb.ListWatchlistsRequest, _ ...grpc.CallOption) (*stockpb.ListWatchlistsResponse, error) {
	if s.listWLFn != nil {
		return s.listWLFn(in)
	}
	return &stockpb.ListWatchlistsResponse{}, nil
}
func (s *stubWatchlistClient) DeleteWatchlist(_ context.Context, in *stockpb.DeleteWatchlistRequest, _ ...grpc.CallOption) (*stockpb.DeleteWatchlistResponse, error) {
	if s.deleteWLFn != nil {
		return s.deleteWLFn(in)
	}
	return &stockpb.DeleteWatchlistResponse{}, nil
}

var _ stockpb.WatchlistServiceClient = (*stubWatchlistClient)(nil)

func watchlistRouter(h *handler.WatchlistHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCli := setClientIdentity(42)
	r.POST("/api/v3/me/watchlist", withCli, h.AddItem)
	r.DELETE("/api/v3/me/watchlist/:listing_id", withCli, h.RemoveItem)
	r.GET("/api/v3/me/watchlist", withCli, h.ListMy)
	r.GET("/api/v3/watchlist/:portfolio_id", withCli, h.GetByPortfolioID)
	r.GET("/api/v3/me/watchlists", withCli, h.ListWatchlists)
	r.POST("/api/v3/me/watchlists", withCli, h.CreateWatchlist)
	r.DELETE("/api/v3/me/watchlists/:watchlist_id", withCli, h.DeleteWatchlist)
	r.GET("/api/v3/me/watchlists/:watchlist_id/items", withCli, h.ListItemsInList)
	r.POST("/api/v3/me/watchlists/:watchlist_id/items", withCli, h.AddItemToList)
	r.DELETE("/api/v3/me/watchlists/:watchlist_id/items/:listing_id", withCli, h.RemoveItemFromList)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	return rec
}

func TestWatchlist_AddItem_Success(t *testing.T) {
	var captured *stockpb.AddWatchlistItemRequest
	cl := &stubWatchlistClient{addFn: func(in *stockpb.AddWatchlistItemRequest) (*stockpb.WatchlistItemResponse, error) {
		captured = in
		return &stockpb.WatchlistItemResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "POST", "/api/v3/me/watchlist", `{"listing_id":7}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, captured)
	require.Equal(t, uint64(7), captured.ListingId)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestWatchlist_AddItem_BadBody(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "POST", "/api/v3/me/watchlist", `not-json`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid body")
}

func TestWatchlist_AddItem_MissingListingID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "POST", "/api/v3/me/watchlist", `{"listing_id":0}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_id is required")
}

func TestWatchlist_AddItem_GRPCError(t *testing.T) {
	cl := &stubWatchlistClient{addFn: func(*stockpb.AddWatchlistItemRequest) (*stockpb.WatchlistItemResponse, error) {
		return nil, status.Error(codes.NotFound, "no such listing")
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "POST", "/api/v3/me/watchlist", `{"listing_id":7}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWatchlist_RemoveItem_Success(t *testing.T) {
	var captured *stockpb.RemoveWatchlistItemRequest
	cl := &stubWatchlistClient{removeFn: func(in *stockpb.RemoveWatchlistItemRequest) (*stockpb.RemoveWatchlistItemResponse, error) {
		captured = in
		return &stockpb.RemoveWatchlistItemResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "DELETE", "/api/v3/me/watchlist/12", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(12), captured.ListingId)
}

func TestWatchlist_RemoveItem_BadID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "DELETE", "/api/v3/me/watchlist/0", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid listing_id")
}

func TestWatchlist_ListMy_Success(t *testing.T) {
	var captured *stockpb.ListMyWatchlistRequest
	cl := &stubWatchlistClient{listMyFn: func(in *stockpb.ListMyWatchlistRequest) (*stockpb.ListMyWatchlistResponse, error) {
		captured = in
		return &stockpb.ListMyWatchlistResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "GET", "/api/v3/me/watchlist?listing_type=stock", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "stock", captured.ListingType)
	require.Contains(t, rec.Body.String(), `"items"`)
}

func TestWatchlist_ListMy_BadListingType(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "GET", "/api/v3/me/watchlist?listing_type=banana", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_type must be one of")
}

func TestWatchlist_GetByPortfolioID_OwnClientAllowed(t *testing.T) {
	var captured *stockpb.ListMyWatchlistRequest
	cl := &stubWatchlistClient{listMyFn: func(in *stockpb.ListMyWatchlistRequest) (*stockpb.ListMyWatchlistResponse, error) {
		captured = in
		return &stockpb.ListMyWatchlistResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "GET", "/api/v3/watchlist/client-42", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "client", captured.OwnerType)
	require.Equal(t, uint64(42), captured.OwnerId)
}

func TestWatchlist_GetByPortfolioID_OtherClientForbidden(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "GET", "/api/v3/watchlist/client-99", "")
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestWatchlist_GetByPortfolioID_BadID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "GET", "/api/v3/watchlist/not-a-portfolio", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWatchlist_ListWatchlists_Success(t *testing.T) {
	cl := &stubWatchlistClient{listWLFn: func(*stockpb.ListWatchlistsRequest) (*stockpb.ListWatchlistsResponse, error) {
		return &stockpb.ListWatchlistsResponse{Watchlists: []*stockpb.WatchlistResponse{
			{Id: 3, Name: "Tech", ItemCount: 0},
		}}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "GET", "/api/v3/me/watchlists", "")
	require.Equal(t, http.StatusOK, rec.Code)
	// item_count must always render even when zero.
	require.Contains(t, rec.Body.String(), `"item_count":0`)
	require.Contains(t, rec.Body.String(), `"name":"Tech"`)
}

func TestWatchlist_CreateWatchlist_Success(t *testing.T) {
	var captured *stockpb.CreateWatchlistRequest
	cl := &stubWatchlistClient{createWLFn: func(in *stockpb.CreateWatchlistRequest) (*stockpb.WatchlistResponse, error) {
		captured = in
		return &stockpb.WatchlistResponse{Id: 9, Name: in.Name}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "POST", "/api/v3/me/watchlists", `{"name":"Energy"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "Energy", captured.Name)
}

func TestWatchlist_CreateWatchlist_MissingName(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "POST", "/api/v3/me/watchlists", `{"name":""}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "name is required")
}

func TestWatchlist_DeleteWatchlist_Success(t *testing.T) {
	var captured *stockpb.DeleteWatchlistRequest
	cl := &stubWatchlistClient{deleteWLFn: func(in *stockpb.DeleteWatchlistRequest) (*stockpb.DeleteWatchlistResponse, error) {
		captured = in
		return &stockpb.DeleteWatchlistResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "DELETE", "/api/v3/me/watchlists/5", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(5), captured.WatchlistId)
}

func TestWatchlist_DeleteWatchlist_BadID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "DELETE", "/api/v3/me/watchlists/0", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid watchlist_id")
}

func TestWatchlist_ListItemsInList_Success(t *testing.T) {
	var captured *stockpb.ListMyWatchlistRequest
	cl := &stubWatchlistClient{listMyFn: func(in *stockpb.ListMyWatchlistRequest) (*stockpb.ListMyWatchlistResponse, error) {
		captured = in
		return &stockpb.ListMyWatchlistResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "GET", "/api/v3/me/watchlists/8/items?listing_type=option", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, uint64(8), captured.WatchlistId)
	require.Equal(t, "option", captured.ListingType)
}

func TestWatchlist_ListItemsInList_BadListingType(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "GET", "/api/v3/me/watchlists/8/items?listing_type=banana", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWatchlist_ListItemsInList_BadWatchlistID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "GET", "/api/v3/me/watchlists/0/items", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWatchlist_AddItemToList_Success(t *testing.T) {
	var captured *stockpb.AddWatchlistItemRequest
	cl := &stubWatchlistClient{addFn: func(in *stockpb.AddWatchlistItemRequest) (*stockpb.WatchlistItemResponse, error) {
		captured = in
		return &stockpb.WatchlistItemResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "POST", "/api/v3/me/watchlists/8/items", `{"listing_id":21}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, uint64(8), captured.WatchlistId)
	require.Equal(t, uint64(21), captured.ListingId)
}

func TestWatchlist_AddItemToList_MissingListingID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "POST", "/api/v3/me/watchlists/8/items", `{"listing_id":0}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "listing_id is required")
}

func TestWatchlist_AddItemToList_BadWatchlistID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "POST", "/api/v3/me/watchlists/0/items", `{"listing_id":21}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWatchlist_RemoveItemFromList_Success(t *testing.T) {
	var captured *stockpb.RemoveWatchlistItemRequest
	cl := &stubWatchlistClient{removeFn: func(in *stockpb.RemoveWatchlistItemRequest) (*stockpb.RemoveWatchlistItemResponse, error) {
		captured = in
		return &stockpb.RemoveWatchlistItemResponse{}, nil
	}}
	r := watchlistRouter(handler.NewWatchlistHandler(cl))
	rec := do(r, "DELETE", "/api/v3/me/watchlists/8/items/21", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, uint64(8), captured.WatchlistId)
	require.Equal(t, uint64(21), captured.ListingId)
}

func TestWatchlist_RemoveItemFromList_BadListingID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "DELETE", "/api/v3/me/watchlists/8/items/0", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid listing_id")
}

func TestWatchlist_RemoveItemFromList_BadWatchlistID(t *testing.T) {
	r := watchlistRouter(handler.NewWatchlistHandler(&stubWatchlistClient{}))
	rec := do(r, "DELETE", "/api/v3/me/watchlists/0/items/21", "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
