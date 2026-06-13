package peeregress

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"google.golang.org/grpc"

	transactionpb "github.com/exbanka/contract/transactionpb"
)

// fakeEgress records the ProxyToPeer request it receives and returns a canned
// response/error. CheckPeerReachability/GetPeersState are unused.
type fakeEgress struct {
	resp   *transactionpb.ProxyToPeerResponse
	err    error
	gotReq *transactionpb.ProxyToPeerRequest
}

func (f *fakeEgress) ProxyToPeer(ctx context.Context, in *transactionpb.ProxyToPeerRequest, opts ...grpc.CallOption) (*transactionpb.ProxyToPeerResponse, error) {
	f.gotReq = in
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}
func (f *fakeEgress) CheckPeerReachability(ctx context.Context, in *transactionpb.CheckPeerReachabilityRequest, opts ...grpc.CallOption) (*transactionpb.PeerReachability, error) {
	return nil, errors.New("not used")
}
func (f *fakeEgress) GetPeersState(ctx context.Context, in *transactionpb.GetPeersStateRequest, opts ...grpc.CallOption) (*transactionpb.GetPeersStateResponse, error) {
	return nil, errors.New("not used")
}

// CreateNegotiation POSTs to the peer's bare /negotiations leaf and parses the
// peer-assigned {routingNumber, id}.
func TestCreateNegotiation_OK(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{
		StatusCode: http.StatusCreated,
		Body:       []byte(`{"routingNumber":222,"id":"abc-1"}`),
	}}
	d := NewDispatcher(eg)
	rid, fid, err := d.CreateNegotiation(context.Background(), "222", map[string]any{"foo": "bar"})
	if err != nil {
		t.Fatalf("CreateNegotiation: %v", err)
	}
	if rid != 222 || fid != "abc-1" {
		t.Errorf("got (%d, %q), want (222, abc-1)", rid, fid)
	}
	if eg.gotReq.GetPeerBankCode() != "222" || eg.gotReq.GetMethod() != http.MethodPost || eg.gotReq.GetPath() != "/negotiations" {
		t.Errorf("proxy req = %+v", eg.gotReq)
	}
	// Body must be the marshalled offer.
	var sent map[string]any
	if err := json.Unmarshal(eg.gotReq.GetBody(), &sent); err != nil || sent["foo"] != "bar" {
		t.Errorf("body = %s (err %v)", eg.gotReq.GetBody(), err)
	}
}

// A non-2xx peer status on create is an error (not a silent success).
func TestCreateNegotiation_PeerRejects(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{
		StatusCode: http.StatusBadRequest, Body: []byte("bad offer"),
	}}
	d := NewDispatcher(eg)
	if _, _, err := d.CreateNegotiation(context.Background(), "222", map[string]any{}); err == nil {
		t.Fatal("expected error on 400")
	}
}

// A transport/gRPC error reaching interbank is surfaced.
func TestCreateNegotiation_EgressError(t *testing.T) {
	d := NewDispatcher(&fakeEgress{err: errors.New("interbank down")})
	if _, _, err := d.CreateNegotiation(context.Background(), "222", map[string]any{}); err == nil {
		t.Fatal("expected error")
	}
}

// Proxy composes /negotiations/{rid}/{fid}{subpath} and passes through the
// peer's status + body verbatim (non-2xx is NOT an error).
func TestProxy_PassThrough(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{
		StatusCode: http.StatusConflict, Body: []byte(`{"vote":"NO"}`),
	}}
	d := NewDispatcher(eg)
	body, code, err := d.Proxy(context.Background(), "222", "222", "abc-1", http.MethodGet, "/accept", nil)
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if code != http.StatusConflict || string(body) != `{"vote":"NO"}` {
		t.Errorf("got (%d, %s)", code, body)
	}
	if eg.gotReq.GetPath() != "/negotiations/222/abc-1/accept" || eg.gotReq.GetMethod() != http.MethodGet {
		t.Errorf("proxy req = %+v", eg.gotReq)
	}
}

// Empty subpath ⇒ leaf at /negotiations/{rid}/{fid} (e.g. PUT counter, DELETE).
func TestProxy_EmptySubpath(t *testing.T) {
	eg := &fakeEgress{resp: &transactionpb.ProxyToPeerResponse{StatusCode: http.StatusOK, Body: []byte(`{}`)}}
	d := NewDispatcher(eg)
	if _, _, err := d.Proxy(context.Background(), "222", "222", "abc-1", http.MethodPut, "", []byte(`{"price":5}`)); err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if eg.gotReq.GetPath() != "/negotiations/222/abc-1" {
		t.Errorf("path = %q", eg.gotReq.GetPath())
	}
}

// A transport/gRPC error reaching interbank ⇒ (nil, 502, err).
func TestProxy_EgressError(t *testing.T) {
	d := NewDispatcher(&fakeEgress{err: errors.New("interbank down")})
	body, code, err := d.Proxy(context.Background(), "222", "222", "abc-1", http.MethodGet, "/accept", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if code != http.StatusBadGateway || body != nil {
		t.Errorf("got (%d, %v), want (502, nil)", code, body)
	}
}
