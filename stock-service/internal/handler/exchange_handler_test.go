package handler

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/exbanka/stock-service/internal/model"
)

func TestMapServiceError_NotFound(t *testing.T) {
	if got := mapServiceError(errors.New("exchange not found")); got != codes.NotFound {
		t.Errorf("expected NotFound, got %v", got)
	}
}

func TestMapServiceError_InvalidArgument(t *testing.T) {
	for _, msg := range []string{
		"value must be positive",
		"invalid input",
		"name must not be empty",
		"field is required",
	} {
		if got := mapServiceError(errors.New(msg)); got != codes.InvalidArgument {
			t.Errorf("%q: expected InvalidArgument, got %v", msg, got)
		}
	}
}

func TestMapServiceError_AlreadyExists(t *testing.T) {
	for _, msg := range []string{"record already exists", "duplicate entry"} {
		if got := mapServiceError(errors.New(msg)); got != codes.AlreadyExists {
			t.Errorf("%q: expected AlreadyExists, got %v", msg, got)
		}
	}
}

func TestMapServiceError_FailedPrecondition(t *testing.T) {
	for _, msg := range []string{
		"insufficient funds",
		"limit exceeded",
		"not enough shares",
	} {
		if got := mapServiceError(errors.New(msg)); got != codes.FailedPrecondition {
			t.Errorf("%q: expected FailedPrecondition, got %v", msg, got)
		}
	}
}

func TestMapServiceError_PermissionDenied(t *testing.T) {
	for _, msg := range []string{"permission denied", "forbidden access"} {
		if got := mapServiceError(errors.New(msg)); got != codes.PermissionDenied {
			t.Errorf("%q: expected PermissionDenied, got %v", msg, got)
		}
	}
}

func TestMapServiceError_DefaultInternal(t *testing.T) {
	if got := mapServiceError(errors.New("random db blowup")); got != codes.Internal {
		t.Errorf("expected Internal, got %v", got)
	}
}

func TestToExchangeProto_PopulatesAllFields(t *testing.T) {
	ex := &model.StockExchange{
		ID:              7,
		Name:            "New York Stock Exchange",
		Acronym:         "NYSE",
		MICCode:         "XNYS",
		Polity:          "USA",
		Currency:        "USD",
		TimeZone:        "America/New_York",
		OpenTime:        "09:30",
		CloseTime:       "16:00",
		PreMarketOpen:   "07:00",
		PostMarketClose: "20:00",
	}
	p := toExchangeProto(ex)
	if p.Id != 7 {
		t.Errorf("Id: %d", p.Id)
	}
	if p.Acronym != "NYSE" {
		t.Errorf("Acronym: %q", p.Acronym)
	}
	if p.MicCode != "XNYS" {
		t.Errorf("MicCode: %q", p.MicCode)
	}
	if p.PreMarketOpen != "07:00" {
		t.Errorf("PreMarketOpen: %q", p.PreMarketOpen)
	}
	if p.Currency != "USD" {
		t.Errorf("Currency: %q", p.Currency)
	}
}
