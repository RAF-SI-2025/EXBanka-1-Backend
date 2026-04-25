package handler

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/stock-service/internal/service"
)

func TestToExerciseResultPB(t *testing.T) {
	in := &service.ExerciseResult{
		ID:                42,
		OptionTicker:      "AAPL260116C00200000",
		ExercisedQuantity: 5,
		SharesAffected:    500,
		Profit:            decimal.NewFromFloat(123.45),
	}
	out := toExerciseResultPB(in)
	if out.Id != 42 {
		t.Errorf("Id: %d", out.Id)
	}
	if out.OptionTicker != "AAPL260116C00200000" {
		t.Errorf("OptionTicker: %q", out.OptionTicker)
	}
	if out.ExercisedQuantity != 5 {
		t.Errorf("ExercisedQuantity: %d", out.ExercisedQuantity)
	}
	if out.SharesAffected != 500 {
		t.Errorf("SharesAffected: %d", out.SharesAffected)
	}
	if out.Profit != "123.45" {
		t.Errorf("Profit fixed-2: got %q", out.Profit)
	}
}

func TestMapPortfolioError_NotFound(t *testing.T) {
	for _, msg := range []string{
		"holding not found", "option not found",
		"stock listing not found for option's underlying",
		"option holding not found",
	} {
		err := mapPortfolioError(errors.New(msg))
		if status.Code(err) != codes.NotFound {
			t.Errorf("%q: expected NotFound, got %v", msg, status.Code(err))
		}
	}
}

func TestMapPortfolioError_PermissionDenied(t *testing.T) {
	err := mapPortfolioError(errors.New("holding does not belong to user"))
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", status.Code(err))
	}
}

func TestMapPortfolioError_FailedPrecondition(t *testing.T) {
	for _, msg := range []string{
		"only stocks can be made public for OTC trading",
		"invalid public quantity",
		"holding is not an option",
		"option has expired (settlement date passed)",
		"call option is not in the money",
		"put option is not in the money",
		"insufficient stock holdings to exercise put option",
	} {
		err := mapPortfolioError(errors.New(msg))
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%q: expected FailedPrecondition, got %v", msg, status.Code(err))
		}
	}
}

func TestMapPortfolioError_DefaultInternal(t *testing.T) {
	err := mapPortfolioError(errors.New("unexpected database failure"))
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}
