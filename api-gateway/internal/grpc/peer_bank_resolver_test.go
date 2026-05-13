package grpc

import (
	"context"
	"errors"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	grpc1 "google.golang.org/grpc"
)

type stubAdminClient struct {
	transactionpb.PeerBankAdminServiceClient
	codeResp  *transactionpb.ResolvePeerByBankCodeResponse
	codeErr   error
	tokenResp *transactionpb.ResolvePeerByAPITokenResponse
	tokenErr  error
}

func (s *stubAdminClient) ResolvePeerByBankCode(_ context.Context, _ *transactionpb.ResolvePeerByBankCodeRequest, _ ...grpc1.CallOption) (*transactionpb.ResolvePeerByBankCodeResponse, error) {
	return s.codeResp, s.codeErr
}

func (s *stubAdminClient) ResolvePeerByAPIToken(_ context.Context, _ *transactionpb.ResolvePeerByAPITokenRequest, _ ...grpc1.CallOption) (*transactionpb.ResolvePeerByAPITokenResponse, error) {
	return s.tokenResp, s.tokenErr
}

func TestResolveByBankCode_Found(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{
		codeResp: &transactionpb.ResolvePeerByBankCodeResponse{
			Found: true,
			PeerBank: &transactionpb.PeerBankFull{
				BankCode:          "222",
				RoutingNumber:     222,
				ApiTokenPlaintext: "tok",
				HmacInboundKey:    "key",
				Active:            true,
			},
		},
	}}
	rec, ok, err := a.ResolveByBankCode(context.Background(), "222")
	if err != nil || !ok || rec == nil || rec.BankCode != "222" || rec.RoutingNumber != 222 || rec.APITokenPlaintext != "tok" || rec.HMACInboundKey != "key" || !rec.Active {
		t.Fatalf("unexpected: %v %v %+v", err, ok, rec)
	}
}

func TestResolveByBankCode_NotFound(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{
		codeResp: &transactionpb.ResolvePeerByBankCodeResponse{Found: false},
	}}
	_, ok, err := a.ResolveByBankCode(context.Background(), "x")
	if err != nil || ok {
		t.Fatalf("want not-found, got ok=%v err=%v", ok, err)
	}
}

func TestResolveByBankCode_Error(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{codeErr: errors.New("boom")}}
	_, ok, err := a.ResolveByBankCode(context.Background(), "x")
	if err == nil || ok {
		t.Fatalf("want err, got ok=%v err=%v", ok, err)
	}
}

func TestResolveByAPIToken_Found(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{
		tokenResp: &transactionpb.ResolvePeerByAPITokenResponse{
			Found: true,
			PeerBank: &transactionpb.PeerBankFull{
				BankCode: "333", RoutingNumber: 333, Active: true,
			},
		},
	}}
	rec, ok, err := a.ResolveByAPIToken(context.Background(), "t")
	if err != nil || !ok || rec.BankCode != "333" {
		t.Fatalf("unexpected: %v %v %+v", err, ok, rec)
	}
}

func TestResolveByAPIToken_NotFound(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{
		tokenResp: &transactionpb.ResolvePeerByAPITokenResponse{Found: false},
	}}
	_, ok, err := a.ResolveByAPIToken(context.Background(), "t")
	if err != nil || ok {
		t.Fatalf("want not-found")
	}
}

func TestResolveByAPIToken_Error(t *testing.T) {
	a := &PeerBankResolverAdapter{Client: &stubAdminClient{tokenErr: errors.New("x")}}
	_, _, err := a.ResolveByAPIToken(context.Background(), "t")
	if err == nil {
		t.Fatal("want err")
	}
}
