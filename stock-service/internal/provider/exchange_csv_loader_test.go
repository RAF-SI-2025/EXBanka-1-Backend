package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExchangesFromCSVFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exchanges.csv")
	csv := "Name,Acronym,MIC,Polity,Currency,TZ,Open,Close,PreOpen,PostClose\n" +
		"New York Stock Exchange,NYSE,XNYS,USA,USD,America/New_York,09:30,16:00,07:00,20:00\n" +
		"London Stock Exchange,LSE,XLON,UK,GBP,Europe/London,08:00,16:30,07:00,18:00\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatalf("write temp csv: %v", err)
	}

	exchanges, err := LoadExchangesFromCSVFile(path)
	if err != nil {
		t.Fatalf("LoadExchangesFromCSVFile error: %v", err)
	}
	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}
	if exchanges[0].Acronym != "NYSE" {
		t.Errorf("expected first acronym NYSE, got %q", exchanges[0].Acronym)
	}
	if exchanges[0].MICCode != "XNYS" {
		t.Errorf("expected MIC XNYS, got %q", exchanges[0].MICCode)
	}
	if exchanges[1].Currency != "GBP" {
		t.Errorf("expected GBP, got %q", exchanges[1].Currency)
	}
	if exchanges[1].OpenTime != "08:00" {
		t.Errorf("expected open 08:00, got %q", exchanges[1].OpenTime)
	}
}

func TestLoadExchangesFromCSVFile_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exchanges.csv")
	csv := "Name,Acronym,MIC,Polity,Currency,TZ,Open,Close,PreOpen,PostClose\n" +
		"  Tokyo SE  ,  TSE  ,XTKS,Japan,JPY,Asia/Tokyo,09:00,15:00,08:00,17:00\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatalf("write temp csv: %v", err)
	}

	exchanges, err := LoadExchangesFromCSVFile(path)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
	if exchanges[0].Name != "Tokyo SE" {
		t.Errorf("expected trimmed name 'Tokyo SE', got %q", exchanges[0].Name)
	}
	if exchanges[0].Acronym != "TSE" {
		t.Errorf("expected trimmed acronym 'TSE', got %q", exchanges[0].Acronym)
	}
}

func TestLoadExchangesFromCSVFile_SkipsShortRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exchanges.csv")
	// Header has 10 columns, data row has 5 -> too short -> skipped.
	csv := "Name,Acronym,MIC,Polity,Currency,TZ,Open,Close,PreOpen,PostClose\n" +
		"Short,Row,Only,5,Cols\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	exchanges, err := LoadExchangesFromCSVFile(path)
	if err != nil {
		// csv reader requires consistent field count; if it errors, that is OK
		// — the test verifies behaviour either way: no panic, and result is empty.
		if !strings.Contains(err.Error(), "wrong number of fields") &&
			!strings.Contains(err.Error(), "read csv row") {
			t.Fatalf("unexpected error type: %v", err)
		}
		return
	}
	if len(exchanges) != 0 {
		t.Errorf("expected 0 exchanges from short row, got %d", len(exchanges))
	}
}

func TestLoadExchangesFromCSVFile_FileNotFound(t *testing.T) {
	_, err := LoadExchangesFromCSVFile("/this/path/does/not/exist.csv")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "open csv file") {
		t.Errorf("expected wrapped 'open csv file' error, got %q", err.Error())
	}
}

func TestLoadExchangesFromCSVFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	_, err := LoadExchangesFromCSVFile(path)
	if err == nil {
		t.Fatal("expected error for empty file (no header)")
	}
}
