package handler_test

import (
	"context"
	"testing"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPeerBankAdmin_Create_MissingFields_400 verifies validation rejects
// requests missing any of the required fields.
func TestPeerBankAdmin_Create_MissingFields_400(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()

	cases := []struct {
		name string
		req  *transactionpb.CreatePeerBankRequest
	}{
		{"missing bank_code", &transactionpb.CreatePeerBankRequest{
			RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "t",
		}},
		{"missing routing_number", &transactionpb.CreatePeerBankRequest{
			BankCode: "222", BaseUrl: "http://x", ApiToken: "t",
		}},
		{"missing base_url", &transactionpb.CreatePeerBankRequest{
			BankCode: "222", RoutingNumber: 222, ApiToken: "t",
		}},
		{"missing api_token", &transactionpb.CreatePeerBankRequest{
			BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x",
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.CreatePeerBank(ctx, tc.req)
			if err == nil || status.Code(err) != codes.InvalidArgument {
				t.Errorf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

// TestPeerBankAdmin_Get_NotFound verifies the GetPeerBank-not-found path.
func TestPeerBankAdmin_Get_NotFound(t *testing.T) {
	h := newAdminTestHandler(t)
	_, err := h.GetPeerBank(context.Background(), &transactionpb.GetPeerBankRequest{Id: 99999})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestPeerBankAdmin_Update_NotFound verifies the UpdatePeerBank-not-found path.
func TestPeerBankAdmin_Update_NotFound(t *testing.T) {
	h := newAdminTestHandler(t)
	_, err := h.UpdatePeerBank(context.Background(), &transactionpb.UpdatePeerBankRequest{
		Id: 99999, BaseUrl: "http://x", BaseUrlSet: true,
	})
	if err == nil || status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

// TestPeerBankAdmin_Update_FullFieldSet verifies all setter branches in
// UpdatePeerBank fire when the matching *Set fields are true.
func TestPeerBankAdmin_Update_FullFieldSet(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	created, _ := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://old", ApiToken: "old-token", Active: true,
	})

	updated, err := h.UpdatePeerBank(ctx, &transactionpb.UpdatePeerBankRequest{
		Id:                 created.Id,
		BaseUrl:            "http://new",
		BaseUrlSet:         true,
		ApiToken:           "new-token",
		ApiTokenSet:        true,
		HmacInboundKey:     "in-K",
		HmacInboundKeySet:  true,
		HmacOutboundKey:    "out-K",
		HmacOutboundKeySet: true,
		// Keep peer active so post-update API-token resolution still works.
		Active:    true,
		ActiveSet: true,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.BaseUrl != "http://new" {
		t.Errorf("base_url not updated: %q", updated.BaseUrl)
	}
	if !updated.Active {
		t.Errorf("active not preserved")
	}

	// Re-resolve via API token to confirm the new token is what was bcrypted.
	resp, _ := h.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "new-token"})
	if !resp.GetFound() {
		t.Errorf("re-resolve with new token must succeed")
	}
	if resp.GetPeerBank().GetHmacInboundKey() != "in-K" {
		t.Errorf("hmac inbound: %+v", resp.GetPeerBank())
	}
}

// TestPeerBankAdmin_Update_EmptyApiTokenIgnored verifies that ApiTokenSet=true
// with an empty token string does NOT clear/rotate the bcrypt hash. (See the
// `if req.GetApiTokenSet() && req.GetApiToken() != ""` guard.)
func TestPeerBankAdmin_Update_EmptyApiTokenIgnored(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	created, _ := h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "stable-token", Active: true,
	})

	if _, err := h.UpdatePeerBank(ctx, &transactionpb.UpdatePeerBankRequest{
		Id:          created.Id,
		ApiToken:    "",
		ApiTokenSet: true,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Original token still resolves.
	resp, _ := h.ResolvePeerByAPIToken(ctx, &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: "stable-token"})
	if !resp.GetFound() {
		t.Errorf("original token must still match after empty-rotate")
	}
}

// TestPeerBankAdmin_ResolveByAPIToken_EmptyToken verifies the early-out for
// empty tokens.
func TestPeerBankAdmin_ResolveByAPIToken_EmptyToken(t *testing.T) {
	h := newAdminTestHandler(t)
	resp, err := h.ResolvePeerByAPIToken(context.Background(), &transactionpb.ResolvePeerByAPITokenRequest{ApiToken: ""})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resp.GetFound() {
		t.Errorf("empty token must yield Found=false")
	}
}

// TestPeerBankAdmin_ResolveByBankCode_InactiveExcluded verifies that an
// inactive peer is treated as not-found.
func TestPeerBankAdmin_ResolveByBankCode_InactiveExcluded(t *testing.T) {
	h := newAdminTestHandler(t)
	ctx := context.Background()
	_, _ = h.CreatePeerBank(ctx, &transactionpb.CreatePeerBankRequest{
		BankCode: "222", RoutingNumber: 222, BaseUrl: "http://x", ApiToken: "t", Active: false,
	})
	resp, _ := h.ResolvePeerByBankCode(ctx, &transactionpb.ResolvePeerByBankCodeRequest{BankCode: "222"})
	if resp.GetFound() {
		t.Errorf("inactive peer must not match by bank code")
	}
}
