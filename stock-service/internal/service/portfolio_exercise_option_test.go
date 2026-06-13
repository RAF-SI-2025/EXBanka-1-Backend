package service

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

func TestExerciseOption_ErrorBranches(t *testing.T) {
	svc, mocks := buildPortfolioService()
	owner := uint64(7)

	// Missing holding → not found.
	if _, err := svc.ExerciseOption(999, model.OwnerClient, &owner); !errors.Is(err, ErrHoldingNotFound) {
		t.Fatalf("missing holding → not found, got %v", err)
	}

	// Non-owner → ownership error.
	mocks.holdingRepo.holdings[1] = &model.Holding{
		ID: 1, OwnerType: model.OwnerClient, OwnerID: &owner,
		SecurityType: "option", SecurityID: 100, Quantity: 1, AccountID: 1,
	}
	other := uint64(99)
	if _, err := svc.ExerciseOption(1, model.OwnerClient, &other); !errors.Is(err, ErrHoldingOwnership) {
		t.Fatalf("non-owner → ownership error, got %v", err)
	}

	// Not an option (stock holding) → ErrHoldingNotOption.
	mocks.holdingRepo.holdings[2] = &model.Holding{
		ID: 2, OwnerType: model.OwnerClient, OwnerID: &owner,
		SecurityType: "stock", SecurityID: 50, Quantity: 1, AccountID: 1,
	}
	if _, err := svc.ExerciseOption(2, model.OwnerClient, &owner); !errors.Is(err, ErrHoldingNotOption) {
		t.Fatalf("stock holding → not-option, got %v", err)
	}

	// Option holding but option row missing → ErrOptionNotFound.
	if _, err := svc.ExerciseOption(1, model.OwnerClient, &owner); !errors.Is(err, ErrOptionNotFound) {
		t.Fatalf("missing option row → option-not-found, got %v", err)
	}

	// Option present but expired → ErrOptionExpired.
	mocks.optionRepo.addOption(&model.Option{
		ID: 100, StockID: 500, OptionType: "call", StrikePrice: decimal.NewFromInt(100),
		SettlementDate: time.Now().Add(-24 * time.Hour),
	})
	if _, err := svc.ExerciseOption(1, model.OwnerClient, &owner); !errors.Is(err, ErrOptionExpired) {
		t.Fatalf("expired option → option-expired, got %v", err)
	}

	// Fresh option, but no stock listing → ErrListingNotFound.
	mocks.holdingRepo.holdings[3] = &model.Holding{
		ID: 3, OwnerType: model.OwnerClient, OwnerID: &owner,
		SecurityType: "option", SecurityID: 101, Quantity: 1, AccountID: 1,
	}
	mocks.optionRepo.addOption(&model.Option{
		ID: 101, StockID: 600, OptionType: "call", StrikePrice: decimal.NewFromInt(100),
		SettlementDate: time.Now().Add(48 * time.Hour),
	})
	if _, err := svc.ExerciseOption(3, model.OwnerClient, &owner); !errors.Is(err, ErrListingNotFound) {
		t.Fatalf("no stock listing → listing-not-found, got %v", err)
	}

	// In-range option + listing below strike → call not in the money.
	mocks.holdingRepo.holdings[4] = &model.Holding{
		ID: 4, OwnerType: model.OwnerClient, OwnerID: &owner,
		SecurityType: "option", SecurityID: 102, Quantity: 1, AccountID: 1,
	}
	mocks.optionRepo.addOption(&model.Option{
		ID: 102, StockID: 700, OptionType: "call", StrikePrice: decimal.NewFromInt(100),
		SettlementDate: time.Now().Add(48 * time.Hour),
	})
	mocks.listingRepo.addListing(&model.Listing{ID: 9, SecurityID: 700, SecurityType: "stock", Price: decimal.NewFromInt(50)})
	if _, err := svc.ExerciseOption(4, model.OwnerClient, &owner); !errors.Is(err, ErrCallNotInTheMoney) {
		t.Fatalf("below-strike call → not-in-the-money, got %v", err)
	}
}
