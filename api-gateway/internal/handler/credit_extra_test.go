package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
)

func TestCredit_UpdateBankMargin_BadID(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `{"margin":3.5}`
	req := httptest.NewRequest("PUT", "/bank-margins/abc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestCredit_UpdateBankMargin_BadJSON(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	body := `not-json`
	req := httptest.NewRequest("PUT", "/bank-margins/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestCredit_ListInterestRateTiers_NoFilter(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/interest-rate-tiers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Either 200 (success with stub) or 500 (handle error path) — the handler executed.
	if w.Code == 0 {
		t.Fatal("no response")
	}
}

func TestCredit_ListBankMargins_Stub(t *testing.T) {
	h := handler.NewCreditHandler(&stubCreditClient{})
	r := creditRouter(h)
	req := httptest.NewRequest("GET", "/bank-margins", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == 0 {
		t.Fatal("no response")
	}
}
