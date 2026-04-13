package repository_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// setupWipeTestDB opens a fresh in-memory SQLite DB and auto-migrates all
// stock-service models in FK-safe order.
func setupWipeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	// Migrate parents before children.
	require.NoError(t, db.AutoMigrate(
		&model.StockExchange{},
		&model.Stock{},
		&model.FuturesContract{},
		&model.ForexPair{},
		&model.Listing{},
		&model.ListingDailyPriceInfo{},
		&model.Option{},
		&model.Order{},
		&model.Holding{},
		&model.OrderTransaction{},
		&model.CapitalGain{},
		&model.TaxCollection{},
	))
	return db
}

func TestWipeAll_DeletesAllStockAndTradingState(t *testing.T) {
	db := setupWipeTestDB(t)

	// ---- seed one row in every wiped table ---------------------------------

	// 1. StockExchange (root)
	exch := model.StockExchange{
		Name:      "Test Exchange",
		Acronym:   "TST",
		MICCode:   "TTST",
		Polity:    "Testland",
		Currency:  "USD",
		TimeZone:  "UTC",
		OpenTime:  "09:30",
		CloseTime: "16:00",
	}
	require.NoError(t, db.Create(&exch).Error)

	// 2. Stock (child of StockExchange)
	stock := model.Stock{
		Ticker:     "TSTK",
		Name:       "Test Stock",
		ExchangeID: exch.ID,
	}
	require.NoError(t, db.Create(&stock).Error)

	// 3. FuturesContract (child of StockExchange)
	future := model.FuturesContract{
		Ticker:         "TFC1",
		Name:           "Test Futures",
		ContractSize:   100,
		ContractUnit:   "barrels",
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
		ExchangeID:     exch.ID,
	}
	require.NoError(t, db.Create(&future).Error)

	// 4. ForexPair (child of StockExchange)
	forex := model.ForexPair{
		Ticker:        "EUR/USD",
		Name:          "Euro / US Dollar",
		BaseCurrency:  "EUR",
		QuoteCurrency: "USD",
		ExchangeRate:  decimal.NewFromFloat(1.08),
		ExchangeID:    exch.ID,
	}
	require.NoError(t, db.Create(&forex).Error)

	// 5. Listing (child of StockExchange, references security by ID/type)
	listing := model.Listing{
		SecurityID:   stock.ID,
		SecurityType: "stock",
		ExchangeID:   exch.ID,
		Price:        decimal.NewFromFloat(100),
	}
	require.NoError(t, db.Create(&listing).Error)

	// 5b. ListingDailyPriceInfo (child of Listing) — regression: this table
	// was missing from the wipe list and its FK to listings caused the
	// switch-source flow to fail with SQLSTATE 23503.
	priceInfo := model.ListingDailyPriceInfo{
		ListingID: listing.ID,
		Date:      time.Now(),
		Price:     decimal.NewFromFloat(100),
		High:      decimal.NewFromFloat(101),
		Low:       decimal.NewFromFloat(99),
		Change:    decimal.NewFromFloat(1),
		Volume:    1000,
	}
	require.NoError(t, db.Create(&priceInfo).Error)

	// 6. Option (child of Stock)
	opt := model.Option{
		Ticker:         "TSTK240101C00100000",
		Name:           "Test Option",
		StockID:        stock.ID,
		OptionType:     "call",
		StrikePrice:    decimal.NewFromFloat(100),
		Premium:        decimal.NewFromFloat(5),
		SettlementDate: time.Now().Add(30 * 24 * time.Hour),
	}
	require.NoError(t, db.Create(&opt).Error)

	// 7. Order (child of Listing)
	order := model.Order{
		UserID:            1,
		SystemType:        "employee",
		ListingID:         listing.ID,
		SecurityType:      "stock",
		Ticker:            "TSTK",
		Direction:         "buy",
		OrderType:         "market",
		Quantity:          10,
		ContractSize:      1,
		PricePerUnit:      decimal.NewFromFloat(100),
		ApproximatePrice:  decimal.NewFromFloat(1000),
		RemainingPortions: 10,
		AccountID:         1,
		LastModification:  time.Now(),
	}
	require.NoError(t, db.Create(&order).Error)

	// 8. Holding (independent)
	holding := model.Holding{
		UserID:        1,
		SystemType:    "employee",
		UserFirstName: "Test",
		UserLastName:  "User",
		SecurityType:  "stock",
		SecurityID:    stock.ID,
		ListingID:     listing.ID,
		Ticker:        "TSTK",
		Name:          "Test Stock",
		Quantity:      10,
		AveragePrice:  decimal.NewFromFloat(100),
		AccountID:     1,
	}
	require.NoError(t, db.Create(&holding).Error)

	// 9. OrderTransaction (child of Order)
	orderTx := model.OrderTransaction{
		OrderID:      order.ID,
		Quantity:     10,
		PricePerUnit: decimal.NewFromFloat(100),
		TotalPrice:   decimal.NewFromFloat(1000),
		ExecutedAt:   time.Now(),
	}
	require.NoError(t, db.Create(&orderTx).Error)

	// 10. CapitalGain (independent)
	cg := model.CapitalGain{
		UserID:             1,
		SystemType:         "employee",
		OrderTransactionID: orderTx.ID,
		SecurityType:       "stock",
		Ticker:             "TSTK",
		Quantity:           10,
		BuyPricePerUnit:    decimal.NewFromFloat(90),
		SellPricePerUnit:   decimal.NewFromFloat(100),
		TotalGain:          decimal.NewFromFloat(100),
		Currency:           "USD",
		AccountID:          1,
		TaxYear:            2026,
		TaxMonth:           4,
	}
	require.NoError(t, db.Create(&cg).Error)

	// 11. TaxCollection (independent)
	tc := model.TaxCollection{
		UserID:       1,
		SystemType:   "employee",
		Year:         2026,
		Month:        4,
		AccountID:    1,
		Currency:     "USD",
		TotalGain:    decimal.NewFromFloat(100),
		TaxAmount:    decimal.NewFromFloat(15),
		TaxAmountRSD: decimal.NewFromFloat(1800),
		CollectedAt:  time.Now(),
	}
	require.NoError(t, db.Create(&tc).Error)

	// ---- run WipeAll -------------------------------------------------------
	wipe := repository.NewWipeRepository(db)
	require.NoError(t, wipe.WipeAll())

	// ---- verify every table is empty --------------------------------------
	tables := []string{
		"stock_exchanges",
		"stocks",
		"listings",
		"listing_daily_price_infos",
		"options",
		"futures_contracts",
		"forex_pairs",
		"orders",
		"holdings",
		"order_transactions",
		"capital_gains",
		"tax_collections",
	}
	for _, tbl := range tables {
		var n int64
		require.NoError(t, db.Table(tbl).Count(&n).Error)
		require.Equal(t, int64(0), n, "table %s should be empty after wipe", tbl)
	}
}
