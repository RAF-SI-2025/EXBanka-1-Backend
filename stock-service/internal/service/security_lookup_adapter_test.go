package service

import (
	"testing"
	"time"

	"github.com/exbanka/stock-service/internal/model"
)

func TestSecurityLookupAdapter_New(t *testing.T) {
	a := NewSecurityLookupAdapter(nil, nil, nil, nil)
	if a == nil {
		t.Fatal("NewSecurityLookupAdapter returned nil")
	}
}

func TestSecurityLookupAdapter_GetFuturesSettlementDate(t *testing.T) {
	settle := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	repo := &secSvcFuturesRepo{futures: []model.FuturesContract{{ID: 7, SettlementDate: settle}}}
	a := NewSecurityLookupAdapter(nil, repo, nil, nil)
	got, err := a.GetFuturesSettlementDate(7)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got.Equal(settle) {
		t.Errorf("got %v, want %v", got, settle)
	}
}

func TestSecurityLookupAdapter_GetFuturesSettlementDate_NotFound(t *testing.T) {
	a := NewSecurityLookupAdapter(nil, &secSvcFuturesRepo{}, nil, nil)
	_, err := a.GetFuturesSettlementDate(99)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Stock(t *testing.T) {
	stocks := &secSvcStockRepo{stocks: []model.Stock{{ID: 1, Ticker: "AAPL"}}}
	a := NewSecurityLookupAdapter(stocks, nil, nil, nil)
	got, _ := a.GetSecurityTicker("stock", 1)
	if got != "AAPL" {
		t.Errorf("got %q", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Stock_NilRepo(t *testing.T) {
	a := NewSecurityLookupAdapter(nil, nil, nil, nil)
	got, _ := a.GetSecurityTicker("stock", 1)
	if got != "" {
		t.Errorf("got %q want empty", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Stock_NotFound(t *testing.T) {
	a := NewSecurityLookupAdapter(&secSvcStockRepo{}, nil, nil, nil)
	got, _ := a.GetSecurityTicker("stock", 99)
	if got != "" {
		t.Errorf("got %q want empty (not-found returns empty)", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Futures(t *testing.T) {
	futures := &secSvcFuturesRepo{futures: []model.FuturesContract{{ID: 7, Ticker: "CLJ26"}}}
	a := NewSecurityLookupAdapter(nil, futures, nil, nil)
	got, _ := a.GetSecurityTicker("futures", 7)
	if got != "CLJ26" {
		t.Errorf("got %q", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Forex(t *testing.T) {
	forex := &secSvcForexRepo{pairs: []model.ForexPair{{ID: 1, Ticker: "EUR/USD"}}}
	a := NewSecurityLookupAdapter(nil, nil, forex, nil)
	got, _ := a.GetSecurityTicker("forex", 1)
	if got != "EUR/USD" {
		t.Errorf("got %q", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_Option(t *testing.T) {
	opts := &secSvcOptionRepo{options: []model.Option{{ID: 1, Ticker: "AAPL260116C200"}}}
	a := NewSecurityLookupAdapter(nil, nil, nil, opts)
	got, _ := a.GetSecurityTicker("option", 1)
	if got != "AAPL260116C200" {
		t.Errorf("got %q", got)
	}
}

func TestSecurityLookupAdapter_GetSecurityTicker_UnknownType(t *testing.T) {
	a := NewSecurityLookupAdapter(nil, nil, nil, nil)
	got, _ := a.GetSecurityTicker("xyz", 1)
	if got != "" {
		t.Errorf("got %q want empty", got)
	}
}
