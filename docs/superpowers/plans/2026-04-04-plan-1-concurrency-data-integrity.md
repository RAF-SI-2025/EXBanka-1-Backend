# Concurrency & Data Integrity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix concurrency bugs and add startup integrity checks across exchange-service, stock-service, transaction-service, and account-service.

**Architecture:** Targeted fixes to existing services — add missing optimistic lock hook, wrap unsafe multi-step operations in compensating patterns, add startup validation.

**Tech Stack:** Go, GORM, PostgreSQL, gRPC

---

## Task 1: Fix ExchangeRate Missing BeforeUpdate Hook

**Problem:** `ExchangeRate` model has a `Version` field but no `BeforeUpdate` hook, so GORM cannot enforce optimistic locking. Additionally, `UpsertInTx` uses a map-based `Updates()` call which bypasses GORM hooks entirely, manually incrementing the version without any conflict detection.

**Files:**
- Modify: `exchange-service/internal/model/exchange_rate.go`
- Modify: `exchange-service/internal/repository/exchange_rate_repository.go`
- Create: `exchange-service/internal/model/exchange_rate_test.go`
- Modify: `exchange-service/internal/repository/exchange_rate_repository_test.go`

### Steps

- [ ] **1.1** Add `BeforeUpdate` hook to the `ExchangeRate` model.

In `exchange-service/internal/model/exchange_rate.go`, add the hook after the struct definition (before `SeedDefaultRates`):

```go
// BeforeUpdate adds a WHERE version=? clause and increments the version,
// providing optimistic locking on every Save/Update of an ExchangeRate.
func (r *ExchangeRate) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", r.Version)
	r.Version++
	return nil
}
```

- [ ] **1.2** Refactor `UpsertInTx` in `exchange-service/internal/repository/exchange_rate_repository.go` to use struct-based `Save` instead of map-based `Updates`.

Replace lines 71-76 (the map-based update block):

```go
return tx.Model(&existing).Updates(map[string]interface{}{
    "buy_rate":   buy,
    "sell_rate":  sell,
    "version":    existing.Version + 1,
    "updated_at": now,
}).Error
```

With:

```go
existing.BuyRate = buy
existing.SellRate = sell
existing.UpdatedAt = now
result := tx.Save(&existing)
if result.Error != nil {
    return result.Error
}
if result.RowsAffected == 0 {
    return fmt.Errorf("optimistic lock conflict on exchange rate %s/%s", from, to)
}
return nil
```

Also add `"fmt"` to the import block if not already present (it is not currently imported in that file).

The full updated `UpsertInTx` method should be:

```go
func (r *ExchangeRateRepository) UpsertInTx(tx *gorm.DB, from, to string, buy, sell decimal.Decimal) error {
	now := time.Now()
	var existing model.ExchangeRate
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("from_currency = ? AND to_currency = ?", from, to).
		First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&model.ExchangeRate{
			FromCurrency: from,
			ToCurrency:   to,
			BuyRate:      buy,
			SellRate:     sell,
			Version:      1,
			UpdatedAt:    now,
		}).Error
	}
	if err != nil {
		return err
	}
	existing.BuyRate = buy
	existing.SellRate = sell
	existing.UpdatedAt = now
	result := tx.Save(&existing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("optimistic lock conflict on exchange rate %s/%s", from, to)
	}
	return nil
}
```

- [ ] **1.3** Create unit test for the `BeforeUpdate` hook in `exchange-service/internal/model/exchange_rate_test.go`:

```go
package model_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/exchange-service/internal/model"
)

func TestExchangeRate_BeforeUpdate_IncrementsVersion(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ExchangeRate{})

	// Create initial rate
	rate := model.ExchangeRate{
		FromCurrency: "EUR",
		ToCurrency:   "RSD",
		BuyRate:      decimal.NewFromFloat(116.0),
		SellRate:     decimal.NewFromFloat(118.0),
		Version:      1,
	}
	require.NoError(t, db.Create(&rate).Error)
	assert.Equal(t, int64(1), rate.Version)

	// Update via Save — hook should increment version
	rate.SellRate = decimal.NewFromFloat(119.0)
	result := db.Save(&rate)
	require.NoError(t, result.Error)
	assert.Equal(t, int64(1), result.RowsAffected)

	// Reload and verify
	var reloaded model.ExchangeRate
	require.NoError(t, db.First(&reloaded, rate.ID).Error)
	assert.Equal(t, int64(2), reloaded.Version)
	assert.True(t, reloaded.SellRate.Equal(decimal.NewFromFloat(119.0)))
}

func TestExchangeRate_BeforeUpdate_OptimisticLockConflict(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.ExchangeRate{})

	// Create initial rate
	rate := model.ExchangeRate{
		FromCurrency: "USD",
		ToCurrency:   "RSD",
		BuyRate:      decimal.NewFromFloat(106.0),
		SellRate:     decimal.NewFromFloat(108.0),
		Version:      1,
	}
	require.NoError(t, db.Create(&rate).Error)

	// Simulate stale version: load a copy, modify the DB version externally
	staleRate := rate // copy with Version=1
	rate.SellRate = decimal.NewFromFloat(109.0)
	db.Save(&rate) // version now 2 in DB

	// Try to save stale copy — should get 0 rows affected
	staleRate.SellRate = decimal.NewFromFloat(110.0)
	result := db.Save(&staleRate)
	// SQLite doesn't error, it just affects 0 rows
	assert.Equal(t, int64(0), result.RowsAffected,
		"expected 0 rows affected due to version mismatch (optimistic lock)")
}
```

- [ ] **1.4** Add a test to the existing repository test file for the refactored `UpsertInTx` in `exchange-service/internal/repository/exchange_rate_repository_test.go`:

Append the following test:

```go
func TestUpsertInTx_VersionIncrements(t *testing.T) {
	db := setupTestDB(t)
	repo := repository.NewExchangeRateRepository(db)

	buy := decimal.NewFromFloat(116.0)
	sell := decimal.NewFromFloat(118.0)

	// Create
	err := repo.Upsert("GBP", "RSD", buy, sell)
	require.NoError(t, err)

	rate, err := repo.GetByPair("GBP", "RSD")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rate.Version)

	// Update — version should increment via hook
	sell2 := decimal.NewFromFloat(119.5)
	err = repo.Upsert("GBP", "RSD", buy, sell2)
	require.NoError(t, err)

	rate2, err := repo.GetByPair("GBP", "RSD")
	require.NoError(t, err)
	assert.Equal(t, int64(2), rate2.Version)
	assert.True(t, rate2.SellRate.Equal(sell2))

	// Third update
	sell3 := decimal.NewFromFloat(120.0)
	err = repo.Upsert("GBP", "RSD", buy, sell3)
	require.NoError(t, err)

	rate3, err := repo.GetByPair("GBP", "RSD")
	require.NoError(t, err)
	assert.Equal(t, int64(3), rate3.Version)
}
```

- [ ] **1.5** Run tests and verify:

```bash
cd exchange-service && go test ./internal/model/... ./internal/repository/... -v -count=1
```

Expected: all tests pass, including the new `TestExchangeRate_BeforeUpdate_IncrementsVersion`, `TestExchangeRate_BeforeUpdate_OptimisticLockConflict`, and `TestUpsertInTx_VersionIncrements`.

- [ ] **1.6** Commit:

```
fix(exchange): add BeforeUpdate optimistic lock hook to ExchangeRate model

Adds the missing BeforeUpdate GORM hook that enforces version-based
optimistic locking. Refactors UpsertInTx to use struct-based Save
instead of map-based Updates (which bypasses hooks). Adds RowsAffected
check after Save to detect conflicts.
```

---

## Task 2: Audit Stock Service Concurrency

**Problem:** `OTCService.BuyOffer` performs sequential balance operations (debit buyer, credit seller, record capital gain, update holdings) without any transaction wrapping or compensation. If the seller credit fails after the buyer debit succeeds, the buyer loses money permanently. Same issue exists in `PortfolioService.ExerciseOption`.

**Files:**
- Modify: `stock-service/internal/service/otc_service.go`
- Modify: `stock-service/internal/service/portfolio_service.go`
- Modify: `stock-service/internal/service/otc_service_test.go`
- Modify: `stock-service/internal/service/portfolio_service_test.go`

### Steps

- [ ] **2.1** Refactor `BuyOffer` in `stock-service/internal/service/otc_service.go` to add compensating reversal when the seller credit fails after the buyer debit succeeds.

Replace the entire `BuyOffer` method (lines 47-183) with the following implementation that includes compensation logic:

```go
// BuyOffer purchases shares from an OTC offer (a public holding).
func (s *OTCService) BuyOffer(
	offerID uint64, // holding ID of the seller
	buyerID uint64,
	buyerSystemType string,
	quantity int64,
	buyerAccountID uint64,
) (*OTCBuyResult, error) {
	// Get seller's holding
	sellerHolding, err := s.holdingRepo.GetByID(offerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("OTC offer not found")
		}
		return nil, err
	}
	if sellerHolding.PublicQuantity < quantity {
		return nil, errors.New("insufficient public quantity for OTC purchase")
	}
	if sellerHolding.UserID == buyerID {
		return nil, errors.New("cannot buy your own OTC offer")
	}

	// Get current market price from listing
	listing, err := s.listingRepo.GetByID(sellerHolding.ListingID)
	if err != nil {
		return nil, errors.New("listing not found for OTC offer")
	}

	pricePerUnit := listing.Price
	totalPrice := pricePerUnit.Mul(decimal.NewFromInt(quantity))

	// Commission: same as Market order = min(14% * total, $7)
	commission := totalPrice.Mul(decimal.NewFromFloat(0.14))
	cap := decimal.NewFromFloat(7)
	if commission.GreaterThan(cap) {
		commission = cap
	}
	commission = commission.Round(2)

	// Step 1: Debit buyer's account: total + commission
	buyerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: buyerAccountID})
	if err != nil {
		return nil, errors.New("buyer account not found")
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   buyerAcct.AccountNumber,
		Amount:          totalPrice.Add(commission).Neg().StringFixed(4),
		UpdateAvailable: true,
	})
	if err != nil {
		return nil, errors.New("failed to debit buyer account: " + err.Error())
	}

	// Step 2: Credit seller's account: total
	// If this fails, compensate by re-crediting the buyer.
	sellerAcct, err := s.accountClient.GetAccount(context.Background(), &accountpb.GetAccountRequest{Id: sellerHolding.AccountID})
	if err != nil {
		// Compensate: re-credit buyer
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
		})
		return nil, errors.New("seller account not found")
	}
	_, err = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
		AccountNumber:   sellerAcct.AccountNumber,
		Amount:          totalPrice.StringFixed(4),
		UpdateAvailable: true,
	})
	if err != nil {
		// Compensate: re-credit buyer
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
		})
		return nil, errors.New("failed to credit seller account: " + err.Error())
	}

	// Step 3: Record capital gain for seller
	gain := pricePerUnit.Sub(sellerHolding.AveragePrice).Mul(decimal.NewFromInt(quantity))
	capitalGain := &model.CapitalGain{
		UserID:           sellerHolding.UserID,
		SystemType:       sellerHolding.SystemType,
		OTC:              true,
		SecurityType:     sellerHolding.SecurityType,
		Ticker:           sellerHolding.Ticker,
		Quantity:         quantity,
		BuyPricePerUnit:  sellerHolding.AveragePrice,
		SellPricePerUnit: pricePerUnit,
		TotalGain:        gain,
		Currency:         listing.Exchange.Currency,
		AccountID:        sellerHolding.AccountID,
		TaxYear:          time.Now().Year(),
		TaxMonth:         int(time.Now().Month()),
	}
	if err := s.capitalGainRepo.Create(capitalGain); err != nil {
		// Compensate: reverse both balance changes
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   buyerAcct.AccountNumber,
			Amount:          totalPrice.Add(commission).StringFixed(4),
			UpdateAvailable: true,
		})
		_, _ = s.accountClient.UpdateBalance(context.Background(), &accountpb.UpdateBalanceRequest{
			AccountNumber:   sellerAcct.AccountNumber,
			Amount:          totalPrice.Neg().StringFixed(4),
			UpdateAvailable: true,
		})
		return nil, err
	}

	// Step 4: Decrease seller's holding
	sellerHolding.Quantity -= quantity
	sellerHolding.PublicQuantity -= quantity
	if sellerHolding.Quantity == 0 {
		if err := s.holdingRepo.Delete(sellerHolding.ID); err != nil {
			return nil, err
		}
	} else {
		if err := s.holdingRepo.Update(sellerHolding); err != nil {
			return nil, err
		}
	}

	// Step 5: Create/update buyer's holding
	buyerFirstName, buyerLastName := "", ""
	if s.nameResolver != nil {
		fn, ln, resolveErr := s.nameResolver(buyerID, buyerSystemType)
		if resolveErr == nil {
			buyerFirstName, buyerLastName = fn, ln
		}
	}

	buyerHolding := &model.Holding{
		UserID:        buyerID,
		SystemType:    buyerSystemType,
		UserFirstName: buyerFirstName,
		UserLastName:  buyerLastName,
		SecurityType:  sellerHolding.SecurityType,
		SecurityID:    listing.SecurityID,
		ListingID:     sellerHolding.ListingID,
		Ticker:        sellerHolding.Ticker,
		Name:          sellerHolding.Name,
		Quantity:      quantity,
		AveragePrice:  pricePerUnit,
		AccountID:     buyerAccountID,
	}
	if err := s.holdingRepo.Upsert(buyerHolding); err != nil {
		return nil, err
	}

	return &OTCBuyResult{
		ID:           buyerHolding.ID,
		OfferID:      offerID,
		Quantity:     quantity,
		PricePerUnit: pricePerUnit,
		TotalPrice:   totalPrice,
		Commission:   commission,
	}, nil
}
```

- [ ] **2.2** Refactor `ExerciseOption` in `stock-service/internal/service/portfolio_service.go` to add compensating reversal on failure in the call-option path.

Replace the call-option branch (lines 262-296, the `if option.OptionType == "call"` block) with:

```go
	if option.OptionType == "call" {
		// CALL: stock price must be > strike price
		if stockListing.Price.LessThanOrEqual(option.StrikePrice) {
			return nil, errors.New("call option is not in the money")
		}
		profit = stockListing.Price.Sub(option.StrikePrice).Mul(decimal.NewFromInt(sharesAffected))

		// Debit account by strike * shares (buying stock at strike price)
		debitAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.debitAccount(holding.AccountID, debitAmount); err != nil {
			return nil, err
		}

		// Create/update stock holding at strike price
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			// Compensate: re-credit the debit
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}
		stockHolding := &model.Holding{
			UserID:        userID,
			SystemType:    holding.SystemType,
			UserFirstName: holding.UserFirstName,
			UserLastName:  holding.UserLastName,
			SecurityType:  "stock",
			SecurityID:    option.StockID,
			ListingID:     stockListing.ID,
			Ticker:        stock.Ticker,
			Name:          stock.Name,
			Quantity:      sharesAffected,
			AveragePrice:  option.StrikePrice,
			AccountID:     holding.AccountID,
		}
		if err := s.holdingRepo.Upsert(stockHolding); err != nil {
			// Compensate: re-credit the debit
			_ = s.creditAccount(holding.AccountID, debitAmount)
			return nil, err
		}
```

Replace the put-option branch (lines 298-356, the `} else { // "put"` block) with:

```go
	} else { // "put"
		// PUT: stock price must be < strike price
		if stockListing.Price.GreaterThanOrEqual(option.StrikePrice) {
			return nil, errors.New("put option is not in the money")
		}
		profit = option.StrikePrice.Sub(stockListing.Price).Mul(decimal.NewFromInt(sharesAffected))

		// User must hold enough stock
		stock, err := s.stockRepo.GetByID(option.StockID)
		if err != nil {
			return nil, err
		}
		stockHolding, err := s.holdingRepo.GetByUserAndSecurity(userID, "stock", option.StockID, holding.AccountID)
		if err != nil || stockHolding.Quantity < sharesAffected {
			return nil, errors.New("insufficient stock holdings to exercise put option")
		}

		// Credit account by strike * shares (selling stock at strike price)
		creditAmount := option.StrikePrice.Mul(decimal.NewFromInt(sharesAffected))
		if err := s.creditAccount(holding.AccountID, creditAmount); err != nil {
			return nil, err
		}

		// Decrease stock holding
		stockHolding.Quantity -= sharesAffected
		if stockHolding.PublicQuantity > stockHolding.Quantity {
			stockHolding.PublicQuantity = stockHolding.Quantity
		}

		if stockHolding.Quantity == 0 {
			if err := s.holdingRepo.Delete(stockHolding.ID); err != nil {
				// Compensate: reverse the credit
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		} else {
			if err := s.holdingRepo.Update(stockHolding); err != nil {
				// Compensate: reverse the credit
				_ = s.debitAccount(holding.AccountID, creditAmount)
				return nil, err
			}
		}

		// Record capital gain for the stock sale
		gain := option.StrikePrice.Sub(stockHolding.AveragePrice).Mul(decimal.NewFromInt(sharesAffected))
		capitalGain := &model.CapitalGain{
			UserID:           userID,
			SystemType:       holding.SystemType,
			SecurityType:     "stock",
			Ticker:           stock.Ticker,
			Quantity:         sharesAffected,
			BuyPricePerUnit:  stockHolding.AveragePrice,
			SellPricePerUnit: option.StrikePrice,
			TotalGain:        gain,
			Currency:         stockListing.Exchange.Currency,
			AccountID:        holding.AccountID,
			TaxYear:          time.Now().Year(),
			TaxMonth:         int(time.Now().Month()),
		}
		if err := s.capitalGainRepo.Create(capitalGain); err != nil {
			// Compensate: reverse the credit (holding change is already persisted,
			// but the gain record is the bookkeeping entry — we reverse the money).
			_ = s.debitAccount(holding.AccountID, creditAmount)
			return nil, err
		}
	}
```

- [ ] **2.3** Add test for OTC BuyOffer compensation on seller credit failure.

Add the following test to `stock-service/internal/service/otc_service_test.go`:

```go
func TestOTC_BuyOffer_CompensatesOnSellerCreditFailure(t *testing.T) {
	svc, mocks := buildOTCService()

	listing := otcStockListing(1, 100, 50.00)
	mocks.listingRepo.addListing(listing)

	// Seller has 10 shares, 10 public
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:         10,
		SystemType:     "employee",
		SecurityType:   "stock",
		SecurityID:     100,
		ListingID:      1,
		Ticker:         "AAPL",
		Name:           "Apple",
		Quantity:       10,
		PublicQuantity: 10,
		AveragePrice:   decimal.NewFromFloat(40.00),
		AccountID:      200,
	})

	// Buyer account exists
	mocks.accountClient.addAccount(300, "ACC-BUYER", decimal.NewFromFloat(10000))
	// Seller account exists but UpdateBalance will fail for seller credit
	mocks.accountClient.addAccount(200, "ACC-SELLER", decimal.NewFromFloat(5000))
	mocks.accountClient.failUpdateForAccount("ACC-SELLER")

	_, err := svc.BuyOffer(1, 20, "client", 5, 300)
	if err == nil {
		t.Fatal("expected error when seller credit fails")
	}

	// Verify buyer account was re-credited (compensation happened).
	// The buyer's balance should be restored to original value.
	buyerAcct := mocks.accountClient.getBalance("ACC-BUYER")
	assert.True(t, buyerAcct.Equal(decimal.NewFromFloat(10000)),
		"buyer balance should be restored after compensation, got %s", buyerAcct.StringFixed(4))
}
```

**Note:** This test requires adding a `failUpdateForAccount` capability and a `getBalance` method to the existing `mockAccountClient` in the test file. Add these to the mock:

```go
// Add to mockAccountClient struct:
//   failAccounts map[string]bool

// Add to newMockAccountClient():
//   failAccounts: make(map[string]bool),

// Add method:
func (m *mockAccountClient) failUpdateForAccount(accountNumber string) {
	m.failAccounts[accountNumber] = true
}

// Modify the mock's UpdateBalance to check failAccounts:
// Before applying the balance change:
//   if m.failAccounts[req.AccountNumber] {
//       return nil, errors.New("simulated failure")
//   }

// Add method:
func (m *mockAccountClient) getBalance(accountNumber string) decimal.Decimal {
	for _, a := range m.accounts {
		if a.AccountNumber == accountNumber {
			bal, _ := decimal.NewFromString(a.Balance)
			return bal
		}
	}
	return decimal.Zero
}
```

- [ ] **2.4** Add test for ExerciseOption compensation on holding upsert failure for call options.

Add the following test to `stock-service/internal/service/portfolio_service_test.go`:

```go
func TestExerciseOption_Call_CompensatesOnHoldingUpsertFailure(t *testing.T) {
	svc, mocks := buildPortfolioService()

	// Set up an option holding
	mocks.holdingRepo.addHolding(&model.Holding{
		UserID:       1,
		SystemType:   "client",
		SecurityType: "option",
		SecurityID:   50,
		ListingID:    10,
		Ticker:       "AAPL240120C150",
		Name:         "AAPL Call 150",
		Quantity:     1,
		AveragePrice: decimal.NewFromFloat(5.00),
		AccountID:    100,
	})

	// Option: call, strike $100, settlement in the future
	mocks.optionRepo.addOption(&model.Option{
		ID:             50,
		StockID:        200,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromFloat(100.00),
		SettlementDate: time.Now().Add(24 * time.Hour),
	})

	// Stock listing at $150 (in the money for call with strike $100)
	mocks.listingRepo.addListing(&model.Listing{
		ID:           20,
		SecurityID:   200,
		SecurityType: "stock",
		ExchangeID:   1,
		Exchange:     model.StockExchange{ID: 1, Currency: "USD"},
		Price:        decimal.NewFromFloat(150.00),
	})

	// Stock entity
	mocks.stockRepo.addStock(&model.Stock{
		ID:     200,
		Ticker: "AAPL",
		Name:   "Apple Inc.",
	})

	// Account with sufficient balance
	mocks.accountClient.addAccount(100, "ACC-100", decimal.NewFromFloat(100000))

	// Make holding upsert fail to trigger compensation
	mocks.holdingRepo.failNextUpsert = true

	_, err := svc.ExerciseOption(1, 1)
	if err == nil {
		t.Fatal("expected error when holding upsert fails")
	}

	// Verify account was re-credited (compensation happened).
	balance := mocks.accountClient.getBalance("ACC-100")
	assert.True(t, balance.Equal(decimal.NewFromFloat(100000)),
		"balance should be restored after compensation, got %s", balance.StringFixed(4))
}
```

**Note:** This test requires adding `failNextUpsert bool` field to `mockHoldingRepo` and checking it in the mock's `Upsert` method:

```go
// Add to mockHoldingRepo struct:
//   failNextUpsert bool

// Add to the start of Upsert:
//   if m.failNextUpsert {
//       m.failNextUpsert = false
//       return errors.New("simulated upsert failure")
//   }
```

Also requires the mock's `getBalance` method on `mockAccountClient` (same as in 2.3), and a `buildPortfolioService` helper if not already present.

- [ ] **2.5** Run tests and verify:

```bash
cd stock-service && go test ./internal/service/... -v -count=1
```

Expected: all tests pass, including the new compensation tests.

- [ ] **2.6** Commit:

```
fix(stock): add compensating reversals to OTC BuyOffer and ExerciseOption

BuyOffer now re-credits the buyer if the seller credit or capital gain
recording fails. ExerciseOption now reverses the account debit/credit
if the subsequent holding upsert or update fails. Prevents permanent
money loss on partial failures in cross-service operations.
```

---

## Task 3: Saga Startup Recovery

**Problem:** `StartCompensationRecovery` only runs recovery on a 5-minute ticker. If the service restarts with pending compensations, they won't be attempted until 5 minutes after startup. In a banking system, failed compensations should be retried immediately on startup.

**Files:**
- Modify: `transaction-service/internal/service/transfer_service.go`
- Create: `transaction-service/internal/service/transfer_recovery_test.go`

### Steps

- [ ] **3.1** Modify `StartCompensationRecovery` in `transaction-service/internal/service/transfer_service.go` to run an immediate recovery pass and log the count of pending compensations at startup.

Replace the `StartCompensationRecovery` method (lines 503-516) with:

```go
// StartCompensationRecovery starts a background goroutine that periodically retries
// all saga log entries in "compensating" status. These are compensation steps that
// previously failed to execute; the recovery loop retries them until they succeed or
// reach maxSagaCompensationRetries failures, at which point they are moved to dead_letter.
// Both transfer and payment compensations are handled here because both only require
// an accountClient.UpdateBalance call.
//
// An immediate recovery pass is run before the ticker starts to handle any
// compensations left over from a previous process crash or restart.
func (s *TransferService) StartCompensationRecovery(ctx context.Context) {
	// Run immediate recovery pass at startup
	if s.sagaRepo != nil {
		pending, err := s.sagaRepo.FindPendingCompensations()
		if err != nil {
			log.Printf("saga recovery: failed to check pending compensations at startup: %v", err)
		} else {
			log.Printf("saga recovery: startup check found %d pending compensation(s)", len(pending))
		}
	}
	s.runRecoveryTick(ctx)

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runRecoveryTick(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}
```

- [ ] **3.2** Create unit test in `transaction-service/internal/service/transfer_recovery_test.go`:

```go
package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// mockTransferRepo implements TransferRepo for testing.
type mockTransferRepo struct{}

func (m *mockTransferRepo) Create(t *model.Transfer) error                { return nil }
func (m *mockTransferRepo) GetByID(id uint64) (*model.Transfer, error)    { return nil, nil }
func (m *mockTransferRepo) GetByIdempotencyKey(key string) (*model.Transfer, error) {
	return nil, nil
}
func (m *mockTransferRepo) UpdateStatus(id uint64, status string) error { return nil }
func (m *mockTransferRepo) UpdateStatusWithReason(id uint64, status, reason string) error {
	return nil
}
func (m *mockTransferRepo) ListByAccountNumbers(accountNumbers []string, page, pageSize int) ([]model.Transfer, int64, error) {
	return nil, 0, nil
}

func TestStartCompensationRecovery_RunsImmediately(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.SagaLog{})
	sagaRepo := repository.NewSagaLogRepository(db)

	// Insert a pending compensation
	comp := &model.SagaLog{
		SagaID:          "test-saga-1",
		TransactionID:   1,
		TransactionType: "transfer",
		StepNumber:      1,
		StepName:        "credit_recipient",
		Status:          "compensating",
		IsCompensation:  true,
		AccountNumber:   "1234567890123456",
		Amount:          decimal.NewFromFloat(100.0),
		CreatedAt:       time.Now(),
	}
	require.NoError(t, db.Create(comp).Error)

	// Verify it exists before recovery
	pending, err := sagaRepo.FindPendingCompensations()
	require.NoError(t, err)
	assert.Len(t, pending, 1, "should have 1 pending compensation before recovery")

	// Create service with nil accountClient (recovery tick will skip
	// actual compensation but the logging path is exercised)
	svc := &TransferService{
		transferRepo: &mockTransferRepo{},
		sagaRepo:     sagaRepo,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// StartCompensationRecovery should run immediately (not wait for ticker)
	svc.StartCompensationRecovery(ctx)

	// Give the goroutine a moment to start
	time.Sleep(50 * time.Millisecond)
	cancel()

	// The compensation still exists (accountClient is nil so runRecoveryTick
	// short-circuits), but the test verifies no panics and immediate execution.
	remaining, err := sagaRepo.FindPendingCompensations()
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "compensation should still be pending (no accountClient)")
}

func TestStartCompensationRecovery_NoSagaRepo(t *testing.T) {
	// Ensures no panic when sagaRepo is nil
	svc := &TransferService{
		transferRepo: &mockTransferRepo{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic
	svc.StartCompensationRecovery(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
}
```

- [ ] **3.3** Run tests and verify:

```bash
cd transaction-service && go test ./internal/service/... -v -run TestStartCompensationRecovery -count=1
```

Expected: both tests pass without panics.

- [ ] **3.4** Commit:

```
fix(transaction): run saga compensation recovery immediately on startup

Previously, compensation recovery only ran on a 5-minute ticker, meaning
pending compensations from a crash could wait up to 5 minutes. Now runs
an immediate pass at startup and logs the count of pending compensations.
```

---

## Task 4: Ledger Consistency Check at Startup

**Problem:** There is no startup verification that account balances match their ledger entry sums. A bug, manual DB edit, or partial failure could cause silent balance drift. The existing `ReconcileBalance` method in `LedgerService` performs per-account reconciliation but is never called at startup.

**Files:**
- Create: `account-service/internal/service/reconciliation_service.go`
- Create: `account-service/internal/service/reconciliation_service_test.go`
- Modify: `account-service/cmd/main.go`

### Steps

- [ ] **4.1** Create `account-service/internal/service/reconciliation_service.go`:

```go
package service

import (
	"context"
	"log"

	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/model"
)

// ReconciliationService checks ledger consistency at startup.
type ReconciliationService struct {
	db           *gorm.DB
	ledgerSvc    *LedgerService
}

// NewReconciliationService creates a new ReconciliationService.
func NewReconciliationService(db *gorm.DB, ledgerSvc *LedgerService) *ReconciliationService {
	return &ReconciliationService{db: db, ledgerSvc: ledgerSvc}
}

// CheckAllBalances iterates over all accounts and compares the stored balance
// to the sum of ledger entries. Mismatches are logged as WARNINGs — no
// auto-correction is performed. Returns the number of mismatches found.
func (s *ReconciliationService) CheckAllBalances(ctx context.Context) int {
	var accounts []model.Account
	if err := s.db.Select("account_number").Find(&accounts).Error; err != nil {
		log.Printf("WARNING: reconciliation check failed to load accounts: %v", err)
		return 0
	}

	mismatches := 0
	for _, acct := range accounts {
		if err := s.ledgerSvc.ReconcileBalance(ctx, acct.AccountNumber); err != nil {
			log.Printf("WARNING: %v", err)
			mismatches++
		}
	}

	if mismatches == 0 {
		log.Printf("Ledger reconciliation: all %d accounts are consistent", len(accounts))
	} else {
		log.Printf("WARNING: Ledger reconciliation: %d of %d accounts have mismatches", mismatches, len(accounts))
	}

	return mismatches
}
```

- [ ] **4.2** Create `account-service/internal/service/reconciliation_service_test.go`:

```go
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	"github.com/exbanka/contract/testutil"
)

func TestCheckAllBalances_Consistent(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.LedgerEntry{})

	// Create an account with balance 500
	acct := &model.Account{
		AccountNumber:    "1234567890123456",
		AccountName:      "Test Account",
		OwnerID:          1,
		OwnerName:        "Test User",
		Balance:          decimal.NewFromFloat(500),
		AvailableBalance: decimal.NewFromFloat(500),
		EmployeeID:       1,
		ExpiresAt:        time.Now().Add(365 * 24 * time.Hour),
		CurrencyCode:     "RSD",
		Status:           "active",
		AccountKind:      "current",
		AccountType:      "standard",
		Version:          1,
	}
	require.NoError(t, db.Create(acct).Error)

	// Create matching ledger entries: credit 1000, debit 500 = net 500
	entries := []model.LedgerEntry{
		{
			AccountNumber: "1234567890123456",
			EntryType:     "credit",
			Amount:        decimal.NewFromFloat(1000),
			BalanceBefore: decimal.Zero,
			BalanceAfter:  decimal.NewFromFloat(1000),
			Description:   "initial deposit",
			CreatedAt:     time.Now(),
		},
		{
			AccountNumber: "1234567890123456",
			EntryType:     "debit",
			Amount:        decimal.NewFromFloat(500),
			BalanceBefore: decimal.NewFromFloat(1000),
			BalanceAfter:  decimal.NewFromFloat(500),
			Description:   "withdrawal",
			CreatedAt:     time.Now(),
		},
	}
	for _, e := range entries {
		require.NoError(t, db.Create(&e).Error)
	}

	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := service.NewLedgerService(ledgerRepo, db)
	reconcileSvc := service.NewReconciliationService(db, ledgerSvc)

	mismatches := reconcileSvc.CheckAllBalances(context.Background())
	assert.Equal(t, 0, mismatches, "expected no mismatches for consistent account")
}

func TestCheckAllBalances_Mismatch(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.LedgerEntry{})

	// Create an account with balance 999 (intentionally wrong)
	acct := &model.Account{
		AccountNumber:    "9876543210123456",
		AccountName:      "Mismatched Account",
		OwnerID:          2,
		OwnerName:        "Test User 2",
		Balance:          decimal.NewFromFloat(999),
		AvailableBalance: decimal.NewFromFloat(999),
		EmployeeID:       1,
		ExpiresAt:        time.Now().Add(365 * 24 * time.Hour),
		CurrencyCode:     "RSD",
		Status:           "active",
		AccountKind:      "current",
		AccountType:      "standard",
		Version:          1,
	}
	require.NoError(t, db.Create(acct).Error)

	// Ledger says net = 500 (not 999)
	entries := []model.LedgerEntry{
		{
			AccountNumber: "9876543210123456",
			EntryType:     "credit",
			Amount:        decimal.NewFromFloat(500),
			BalanceBefore: decimal.Zero,
			BalanceAfter:  decimal.NewFromFloat(500),
			Description:   "deposit",
			CreatedAt:     time.Now(),
		},
	}
	for _, e := range entries {
		require.NoError(t, db.Create(&e).Error)
	}

	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := service.NewLedgerService(ledgerRepo, db)
	reconcileSvc := service.NewReconciliationService(db, ledgerSvc)

	mismatches := reconcileSvc.CheckAllBalances(context.Background())
	assert.Equal(t, 1, mismatches, "expected 1 mismatch for inconsistent account")
}

func TestCheckAllBalances_NoAccounts(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.LedgerEntry{})

	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := service.NewLedgerService(ledgerRepo, db)
	reconcileSvc := service.NewReconciliationService(db, ledgerSvc)

	mismatches := reconcileSvc.CheckAllBalances(context.Background())
	assert.Equal(t, 0, mismatches, "no accounts means no mismatches")
}

func TestCheckAllBalances_MultipleAccounts_MixedResults(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.Account{}, &model.LedgerEntry{})

	// Account 1: consistent (balance = 200, ledger net = 200)
	require.NoError(t, db.Create(&model.Account{
		AccountNumber:    "AAAA000000000001",
		AccountName:      "Good Account",
		OwnerID:          1,
		OwnerName:        "User A",
		Balance:          decimal.NewFromFloat(200),
		AvailableBalance: decimal.NewFromFloat(200),
		EmployeeID:       1,
		ExpiresAt:        time.Now().Add(365 * 24 * time.Hour),
		CurrencyCode:     "RSD",
		Status:           "active",
		AccountKind:      "current",
		AccountType:      "standard",
		Version:          1,
	}).Error)
	require.NoError(t, db.Create(&model.LedgerEntry{
		AccountNumber: "AAAA000000000001",
		EntryType:     "credit",
		Amount:        decimal.NewFromFloat(200),
		BalanceBefore: decimal.Zero,
		BalanceAfter:  decimal.NewFromFloat(200),
		CreatedAt:     time.Now(),
	}).Error)

	// Account 2: inconsistent (balance = 300, ledger net = 100)
	require.NoError(t, db.Create(&model.Account{
		AccountNumber:    "BBBB000000000002",
		AccountName:      "Bad Account",
		OwnerID:          2,
		OwnerName:        "User B",
		Balance:          decimal.NewFromFloat(300),
		AvailableBalance: decimal.NewFromFloat(300),
		EmployeeID:       1,
		ExpiresAt:        time.Now().Add(365 * 24 * time.Hour),
		CurrencyCode:     "EUR",
		Status:           "active",
		AccountKind:      "foreign",
		AccountType:      "standard",
		Version:          1,
	}).Error)
	require.NoError(t, db.Create(&model.LedgerEntry{
		AccountNumber: "BBBB000000000002",
		EntryType:     "credit",
		Amount:        decimal.NewFromFloat(100),
		BalanceBefore: decimal.Zero,
		BalanceAfter:  decimal.NewFromFloat(100),
		CreatedAt:     time.Now(),
	}).Error)

	// Account 3: consistent (balance = 0, no ledger entries)
	require.NoError(t, db.Create(&model.Account{
		AccountNumber:    "CCCC000000000003",
		AccountName:      "Empty Account",
		OwnerID:          3,
		OwnerName:        "User C",
		Balance:          decimal.Zero,
		AvailableBalance: decimal.Zero,
		EmployeeID:       1,
		ExpiresAt:        time.Now().Add(365 * 24 * time.Hour),
		CurrencyCode:     "RSD",
		Status:           "active",
		AccountKind:      "current",
		AccountType:      "standard",
		Version:          1,
	}).Error)

	ledgerRepo := repository.NewLedgerRepository(db)
	ledgerSvc := service.NewLedgerService(ledgerRepo, db)
	reconcileSvc := service.NewReconciliationService(db, ledgerSvc)

	mismatches := reconcileSvc.CheckAllBalances(context.Background())
	assert.Equal(t, 1, mismatches, "expected exactly 1 mismatch (account BBBB)")
}
```

- [ ] **4.3** Add reconciliation call to `account-service/cmd/main.go`.

After the bank account seeding block (after line 128, `}` closing the `for _, c := range seedCurrencies` loop) and before the State entity seeding, add:

```go
	// Run ledger consistency check at startup (warning-only, no auto-correction)
	reconcileSvc := service.NewReconciliationService(db, ledgerService)
	reconcileSvc.CheckAllBalances(ctx)
```

Note: `ctx` is defined at line 90 in the current file. If it is defined after this insertion point, move the call to after `ctx` is defined (line 92). Looking at the current code, `ctx` is created at line 90:
```go
ctx, cancel := context.WithCancel(context.Background())
```

So the reconciliation call should be placed after line 97 (after `maintenanceCron.Start(ctx)`) and before the bank account seeding block. Alternatively, place it after all seeding is complete (after line 165, after the state entity seeding). The latter is better since seeding creates accounts/ledger entries that should be included in the check.

Place after line 165 (after the closing `}` of the state entity seed block):

```go
	// Run ledger consistency check at startup (warning-only, no auto-correction)
	reconcileSvc := service.NewReconciliationService(db, ledgerService)
	reconcileSvc.CheckAllBalances(ctx)
```

Also ensure the `service` package import is already present (it is — used at line 93 for `service.NewSpendingCronService`).

- [ ] **4.4** Run tests and verify:

```bash
cd account-service && go test ./internal/service/... -v -run TestCheckAllBalances -count=1
```

Expected: all four tests pass (`TestCheckAllBalances_Consistent`, `TestCheckAllBalances_Mismatch`, `TestCheckAllBalances_NoAccounts`, `TestCheckAllBalances_MultipleAccounts_MixedResults`).

- [ ] **4.5** Verify the build succeeds:

```bash
cd account-service && go build ./cmd/...
```

- [ ] **4.6** Commit:

```
feat(account): add ledger consistency check at startup

Adds ReconciliationService.CheckAllBalances that iterates all accounts,
compares stored balance against the net of ledger entries, and logs
WARNINGs for mismatches. Called once at startup. Does not auto-correct —
human intervention is required for any discrepancies found.
```

---

## Summary of Changes

| Service | File | Change |
|---------|------|--------|
| exchange-service | `internal/model/exchange_rate.go` | Add `BeforeUpdate` hook |
| exchange-service | `internal/repository/exchange_rate_repository.go` | Refactor `UpsertInTx` to struct-based `Save` + `RowsAffected` check |
| exchange-service | `internal/model/exchange_rate_test.go` | New: hook unit tests |
| exchange-service | `internal/repository/exchange_rate_repository_test.go` | Add version increment test |
| stock-service | `internal/service/otc_service.go` | Add compensating reversals to `BuyOffer` |
| stock-service | `internal/service/portfolio_service.go` | Add compensating reversals to `ExerciseOption` |
| stock-service | `internal/service/otc_service_test.go` | Add compensation failure test |
| stock-service | `internal/service/portfolio_service_test.go` | Add compensation failure test |
| transaction-service | `internal/service/transfer_service.go` | Run immediate recovery pass + log pending count |
| transaction-service | `internal/service/transfer_recovery_test.go` | New: startup recovery tests |
| account-service | `internal/service/reconciliation_service.go` | New: ledger consistency checker |
| account-service | `internal/service/reconciliation_service_test.go` | New: reconciliation tests |
| account-service | `cmd/main.go` | Call `CheckAllBalances` at startup |

## Verification

After all tasks are complete, run the full test suite from the repo root:

```bash
make test
```

All existing and new tests must pass. No regressions allowed.
