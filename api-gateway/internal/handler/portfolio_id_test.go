package handler

import (
	"testing"

	"github.com/exbanka/api-gateway/internal/middleware"
)

func TestEncodePortfolioID(t *testing.T) {
	cases := []struct {
		ownerType string
		ownerID   *uint64
		want      string
	}{
		{"client", ptrU64(42), "client-42"},
		{"bank", nil, "bank"},
		{"investment_fund", ptrU64(7), "fund-7"},
	}
	for _, c := range cases {
		got, err := EncodePortfolioID(c.ownerType, c.ownerID)
		if err != nil {
			t.Fatalf("EncodePortfolioID(%q,%v) err: %v", c.ownerType, c.ownerID, err)
		}
		if got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestDecodePortfolioID(t *testing.T) {
	cases := []struct {
		in       string
		wantType string
		wantID   *uint64
		wantErr  bool
	}{
		{"client-42", "client", ptrU64(42), false},
		{"bank", "bank", nil, false},
		{"fund-7", "investment_fund", ptrU64(7), false},
		{"", "", nil, true},
		{"client-", "", nil, true},
		{"client-abc", "", nil, true},
		{"unknown-1", "", nil, true},
		{"fund-0", "", nil, true},
	}
	for _, c := range cases {
		ot, oid, err := DecodePortfolioID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("Decode(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if ot != c.wantType {
			t.Errorf("Decode(%q) type=%q want %q", c.in, ot, c.wantType)
		}
		if (oid == nil) != (c.wantID == nil) || (oid != nil && *oid != *c.wantID) {
			t.Errorf("Decode(%q) id=%v want %v", c.in, oid, c.wantID)
		}
	}
}

func ptrU64(v uint64) *uint64 { return &v }

func TestEnforcePortfolioAccess_ClientOwnPortfolio(t *testing.T) {
	c, _ := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 42}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), nil); err != nil {
		t.Fatal("client viewing own portfolio should succeed")
	}
}

func TestEnforcePortfolioAccess_ClientOtherClient(t *testing.T) {
	c, _ := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "client", PrincipalID: 42}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(43), nil); err == nil {
		t.Fatal("client viewing other client's portfolio should be forbidden")
	}
}

func TestEnforcePortfolioAccess_EmployeeBank(t *testing.T) {
	c, _ := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "bank", nil, []string{}); err != nil {
		t.Fatal("employee viewing bank portfolio should succeed")
	}
}

func TestEnforcePortfolioAccess_EmployeeClientWithoutPerm(t *testing.T) {
	c, _ := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), []string{}); err == nil {
		t.Fatal("employee without portfolio.view_client should be denied")
	}
}

func TestEnforcePortfolioAccess_EmployeeClientWithPerm(t *testing.T) {
	c, _ := newTestGinCtx()
	id := &middleware.ResolvedIdentity{PrincipalType: "employee", PrincipalID: 1}
	if err := enforcePortfolioAccess(c, id, "client", ptrU64(42), []string{"portfolio.view_client"}); err != nil {
		t.Fatal("employee WITH portfolio.view_client should be allowed")
	}
}
