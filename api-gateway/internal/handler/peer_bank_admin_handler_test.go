package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubPeerBankAdminClient struct {
	transactionpb.PeerBankAdminServiceClient
	store map[uint64]*transactionpb.PeerBank
	next  uint64
}

func (s *stubPeerBankAdminClient) ListPeerBanks(_ context.Context, _ *transactionpb.ListPeerBanksRequest, _ ...grpc.CallOption) (*transactionpb.ListPeerBanksResponse, error) {
	out := &transactionpb.ListPeerBanksResponse{}
	for _, v := range s.store {
		out.PeerBanks = append(out.PeerBanks, v)
	}
	return out, nil
}
func (s *stubPeerBankAdminClient) GetPeerBank(_ context.Context, in *transactionpb.GetPeerBankRequest, _ ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	v, ok := s.store[in.GetId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	return v, nil
}
func (s *stubPeerBankAdminClient) CreatePeerBank(_ context.Context, in *transactionpb.CreatePeerBankRequest, _ ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	if s.store == nil {
		s.store = map[uint64]*transactionpb.PeerBank{}
	}
	s.next++
	v := &transactionpb.PeerBank{
		Id: s.next, BankCode: in.GetBankCode(), RoutingNumber: in.GetRoutingNumber(),
		BaseUrl: in.GetBaseUrl(), Active: in.GetActive(),
	}
	s.store[s.next] = v
	return v, nil
}
func (s *stubPeerBankAdminClient) UpdatePeerBank(_ context.Context, in *transactionpb.UpdatePeerBankRequest, _ ...grpc.CallOption) (*transactionpb.PeerBank, error) {
	v, ok := s.store[in.GetId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	if in.GetBaseUrlSet() {
		v.BaseUrl = in.GetBaseUrl()
	}
	if in.GetActiveSet() {
		v.Active = in.GetActive()
	}
	return v, nil
}
func (s *stubPeerBankAdminClient) DeletePeerBank(_ context.Context, in *transactionpb.DeletePeerBankRequest, _ ...grpc.CallOption) (*transactionpb.DeletePeerBankResponse, error) {
	delete(s.store, in.GetId())
	return &transactionpb.DeletePeerBankResponse{}, nil
}

func setupAdminRouter(client transactionpb.PeerBankAdminServiceClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewPeerBankAdminHandler(client)
	g := r.Group("/peer-banks")
	g.GET("", h.List)
	g.GET("/:id", h.Get)
	g.POST("", h.Create)
	g.PUT("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	return r
}

func TestPeerBankAdmin_CreateAndList(t *testing.T) {
	r := setupAdminRouter(&stubPeerBankAdminClient{})

	body, _ := json.Marshal(map[string]any{
		"bank_code": "222", "routing_number": 222, "base_url": "http://peer-222",
		"api_token": "tok-222", "active": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/peer-banks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created["bank_code"] != "222" {
		t.Errorf("created: %+v", created)
	}

	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/peer-banks", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("list: %d", w2.Code)
	}
	var listResp map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &listResp)
	if listResp["peer_banks"] == nil {
		t.Errorf("missing peer_banks: %+v", listResp)
	}
}

func TestPeerBankAdmin_GetUpdateDelete(t *testing.T) {
	client := &stubPeerBankAdminClient{}
	r := setupAdminRouter(client)
	create, _ := client.CreatePeerBank(context.Background(), &transactionpb.CreatePeerBankRequest{BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "t", Active: true})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/peer-banks/"+strconv.FormatUint(create.Id, 10), nil))
	if w.Code != 200 {
		t.Fatalf("get: %d", w.Code)
	}

	body, _ := json.Marshal(map[string]any{"base_url": "http://y", "active": false})
	wU := httptest.NewRecorder()
	rU := httptest.NewRequest(http.MethodPut, "/peer-banks/"+strconv.FormatUint(create.Id, 10), bytes.NewReader(body))
	rU.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wU, rU)
	if wU.Code != 200 {
		t.Fatalf("update: %d %s", wU.Code, wU.Body.String())
	}

	wD := httptest.NewRecorder()
	r.ServeHTTP(wD, httptest.NewRequest(http.MethodDelete, "/peer-banks/"+strconv.FormatUint(create.Id, 10), nil))
	if wD.Code != http.StatusNoContent {
		t.Errorf("delete: %d", wD.Code)
	}
}
