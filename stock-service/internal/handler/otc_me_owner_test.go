package handler

import (
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOTCMeOwner(t *testing.T) {
	tests := []struct {
		name      string
		ownerType string
		ownerID   uint64
		kind      string
		sellerID  string
		want      bool
	}{
		{"remote is never owned", "bank", 0, "remote", "bank", false},
		{"remote client never owned", "client", 7, "remote", "client-7", false},
		{"bank caller owns bank listing", "bank", 0, "local", "bank", true},
		{"bank caller not own client listing", "bank", 0, "local", "client-7", false},
		{"client caller owns own listing", "client", 7, "local", "client-7", true},
		{"client caller not own other client", "client", 7, "local", "client-8", false},
		{"client caller not own bank listing", "client", 7, "local", "bank", false},
		{"client with zero id owns nothing", "client", 0, "local", "client-0", false},
		{"unknown owner type owns nothing", "", 0, "local", "bank", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := otcMeOwner(tc.ownerType, tc.ownerID, tc.kind, tc.sellerID)
			if got != tc.want {
				t.Fatalf("otcMeOwner(%q,%d,%q,%q) = %v, want %v",
					tc.ownerType, tc.ownerID, tc.kind, tc.sellerID, got, tc.want)
			}
		})
	}
}

func TestSellerIDForOwner(t *testing.T) {
	id := uint64(42)
	tests := []struct {
		name      string
		ownerType model.OwnerType
		ownerID   *uint64
		want      string
	}{
		{"bank owner", model.OwnerBank, nil, "bank"},
		{"client owner", model.OwnerClient, &id, "client-42"},
		{"client owner missing id", model.OwnerClient, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sellerIDForOwner(tc.ownerType, tc.ownerID)
			if got != tc.want {
				t.Fatalf("sellerIDForOwner(%q,%v) = %q, want %q", tc.ownerType, tc.ownerID, got, tc.want)
			}
		})
	}
}
