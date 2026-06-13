package peeregress

import (
	"context"
	"errors"
	"net/http"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// TestPublicStock_OK verifies that PublicStock routes a GET /public-stock request
// through ProxyToPeer and returns (body, 200, nil) on success.
func TestPublicStock_OK(t *testing.T) {
	body := []byte(`[{"stock":{"ticker":"AAPL"},"sellers":[]}]`)
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{StatusCode: http.StatusOK, Body: body}}
	d := NewDispatcher(eg)
	got, code, err := d.PublicStock(context.Background(), "222")
	if err != nil {
		t.Fatalf("PublicStock: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200", code)
	}
	if string(got) != string(body) {
		t.Errorf("body = %s, want %s", got, body)
	}
	if eg.gotReq.GetPath() != "/public-stock" || eg.gotReq.GetMethod() != http.MethodGet {
		t.Errorf("req = %+v", eg.gotReq)
	}
	if eg.gotReq.GetPeerBankCode() != "222" {
		t.Errorf("peerBankCode = %q, want 222", eg.gotReq.GetPeerBankCode())
	}
}

// TestPublicStock_EgressError verifies that a transport/gRPC failure returns
// (nil, 502, err).
func TestPublicStock_EgressError(t *testing.T) {
	d := NewDispatcher(&fakeEgress{err: errors.New("interbank down")})
	body, code, err := d.PublicStock(context.Background(), "222")
	if err == nil {
		t.Fatal("expected error on egress failure")
	}
	if code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", code)
	}
	if body != nil {
		t.Errorf("body = %v, want nil", body)
	}
}

// TestPublicStock_NonOKStatus verifies that a non-200 peer response is passed
// through (body, status, nil) — same passthrough semantics as Proxy.
func TestPublicStock_NonOKStatus(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{StatusCode: http.StatusServiceUnavailable, Body: []byte("down")}}
	d := NewDispatcher(eg)
	got, code, err := d.PublicStock(context.Background(), "333")
	if err != nil {
		t.Fatalf("PublicStock: %v", err)
	}
	if code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", code)
	}
	if string(got) != "down" {
		t.Errorf("body = %q", got)
	}
}

// TestCreateNegotiation_BadPeerJSON covers the json.Unmarshal error path when
// the peer returns 200/201 but with non-JSON body.
func TestCreateNegotiation_BadPeerJSON(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{
		StatusCode: http.StatusCreated,
		Body:       []byte("not-json"),
	}}
	d := NewDispatcher(eg)
	_, _, err := d.CreateNegotiation(context.Background(), "222", map[string]any{})
	if err == nil {
		t.Fatal("expected error when peer returns non-JSON body")
	}
}
