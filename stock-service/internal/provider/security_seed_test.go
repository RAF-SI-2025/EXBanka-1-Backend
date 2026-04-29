package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFuturesFromJSON_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "futures.json")
	jsonStr := `[
		{
			"ticker": "CL",
			"name": "Crude Oil",
			"contract_size": 1000,
			"contract_unit": "barrel",
			"settlement_date": "2026-12-15",
			"exchange_acronym": "NYMEX",
			"price": 75.5,
			"high": 76.0,
			"low": 74.0,
			"volume": 1500
		},
		{
			"ticker": "GC",
			"name": "Gold",
			"contract_size": 100,
			"contract_unit": "ounce",
			"settlement_date": "2026-06-30",
			"exchange_acronym": "COMEX",
			"price": 2050.0,
			"high": 2060.0,
			"low": 2040.0,
			"volume": 500
		}
	]`
	if err := os.WriteFile(path, []byte(jsonStr), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	rows, err := LoadFuturesFromJSON(path)
	if err != nil {
		t.Fatalf("LoadFuturesFromJSON error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Contract.Ticker != "CL" {
		t.Errorf("expected ticker CL, got %q", rows[0].Contract.Ticker)
	}
	if rows[0].ExchangeAcronym != "NYMEX" {
		t.Errorf("expected exchange NYMEX, got %q", rows[0].ExchangeAcronym)
	}
	if rows[0].Contract.ContractSize != 1000 {
		t.Errorf("expected contract size 1000, got %d", rows[0].Contract.ContractSize)
	}
	if rows[0].Contract.SettlementDate.Year() != 2026 {
		t.Errorf("expected settlement year 2026, got %d", rows[0].Contract.SettlementDate.Year())
	}
	// price - high - low matches change calculation
	if rows[0].Contract.Price.String() != "75.5" {
		t.Errorf("expected price 75.5, got %s", rows[0].Contract.Price.String())
	}
}

func TestLoadFuturesFromJSON_FileNotFound(t *testing.T) {
	_, err := LoadFuturesFromJSON("/this/path/does/not/exist.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "read futures seed file") {
		t.Errorf("expected wrapped error, got %q", err.Error())
	}
}

func TestLoadFuturesFromJSON_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadFuturesFromJSON(path)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
	if !strings.Contains(err.Error(), "parse futures seed json") {
		t.Errorf("expected wrapped parse error, got %q", err.Error())
	}
}

func TestLoadFuturesFromJSON_BadDate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baddate.json")
	jsonStr := `[
		{
			"ticker": "CL",
			"name": "Crude Oil",
			"contract_size": 1000,
			"contract_unit": "barrel",
			"settlement_date": "not-a-date",
			"exchange_acronym": "NYMEX",
			"price": 75.5,
			"high": 76.0,
			"low": 74.0,
			"volume": 1500
		}
	]`
	if err := os.WriteFile(path, []byte(jsonStr), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadFuturesFromJSON(path)
	if err == nil {
		t.Fatal("expected error for bad date")
	}
	if !strings.Contains(err.Error(), "parse settlement date") {
		t.Errorf("expected 'parse settlement date' error, got %q", err.Error())
	}
}

func TestLoadFuturesFromJSON_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rows, err := LoadFuturesFromJSON(path)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
