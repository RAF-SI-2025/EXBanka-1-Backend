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

// mapGroupToProto must surface the underlying holding id on security positions
// so clients can feed make-public/exercise (both require a holding_id) straight
// from the unified portfolio. Fund positions carry no holding and stay at 0.
func TestMapGroupToProto_HoldingID(t *testing.T) {
	g := service.PortfolioGroup{
		Positions: []service.PortfolioPosition{
			{AssetType: "stock", Symbol: "AAPL", HoldingID: 153, Quantity: 50},
			{AssetType: "investment_fund", FundID: 7, Quantity: 1}, // no holding
		},
	}
	out := mapGroupToProto(g)
	if len(out.Positions) != 2 {
		t.Fatalf("want 2 positions, got %d", len(out.Positions))
	}
	if out.Positions[0].HoldingId != 153 {
		t.Errorf("stock position HoldingId: want 153, got %d", out.Positions[0].HoldingId)
	}
	if out.Positions[1].HoldingId != 0 {
		t.Errorf("fund position HoldingId: want 0, got %d", out.Positions[1].HoldingId)
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
