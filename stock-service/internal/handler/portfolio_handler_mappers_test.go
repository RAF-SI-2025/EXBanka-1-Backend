package handler

import (
	"errors"
	"fmt"
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

// mapPortfolioError is now a passthrough; the wire status is determined by
// the sentinel embedded in the wrapped error.
func TestMapPortfolioError_SentinelPassthrough(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
		want     codes.Code
	}{
		{"HoldingNotFound", service.ErrHoldingNotFound, codes.NotFound},
		{"OptionNotFound", service.ErrOptionNotFound, codes.NotFound},
		{"ListingNotFound", service.ErrListingNotFound, codes.NotFound},
		{"OptionHoldingNotFound", service.ErrOptionHoldingNotFound, codes.NotFound},
		{"HoldingOwnership", service.ErrHoldingOwnership, codes.PermissionDenied},
		{"PublicOnlyStocks", service.ErrPublicOnlyStocks, codes.FailedPrecondition},
		{"InvalidPublicQuantity", service.ErrInvalidPublicQuantity, codes.FailedPrecondition},
		{"HoldingNotOption", service.ErrHoldingNotOption, codes.FailedPrecondition},
		{"OptionExpired", service.ErrOptionExpired, codes.FailedPrecondition},
		{"CallNotInTheMoney", service.ErrCallNotInTheMoney, codes.FailedPrecondition},
		{"PutNotInTheMoney", service.ErrPutNotInTheMoney, codes.FailedPrecondition},
		{"InsufficientStockForPut", service.ErrInsufficientStockForPut, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Op: %w", tc.sentinel)
			err := mapPortfolioError(wrapped)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("errors.Is should match")
			}
			if got := status.Code(err); got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}
