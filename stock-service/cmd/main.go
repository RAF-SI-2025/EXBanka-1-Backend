package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	clientpb "github.com/exbanka/contract/clientpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/influx"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/stockpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/stock-service/internal/cache"
	"github.com/exbanka/stock-service/internal/config"
	stockgrpc "github.com/exbanka/stock-service/internal/grpc"
	"github.com/exbanka/stock-service/internal/handler"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
	"github.com/exbanka/stock-service/internal/source"
)

func main() {
	cfg := config.Load()

	// --- Database ---
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// AutoMigrate all models
	if err := db.AutoMigrate(
		&model.StockExchange{},
		&model.SystemSetting{},
		&model.Stock{},
		&model.FuturesContract{},
		&model.ForexPair{},
		&model.Option{},
		&model.Listing{},
		&model.ListingDailyPriceInfo{},
		&model.Order{},
		&model.OrderTransaction{},
		&model.Holding{},
		&model.HoldingReservation{},
		&model.HoldingReservationSettlement{},
		&model.CapitalGain{},
		&model.TaxCollection{},
		&model.SagaLog{},
	); err != nil {
		log.Fatalf("auto-migrate failed: %v", err)
	}

	// One-shot backfill for the new capital_gains.tax_collection_id column.
	// Stamps every existing capital_gain row that already has a corresponding
	// tax_collections row (matched on user, system_type, year, month,
	// account_id, currency) so the next incremental CollectTax run does not
	// re-tax already-collected gains. Without this, the deploy-day click of
	// "Collect Tax" for the current month would re-charge users whose taxes
	// were already collected before the column existed. Idempotent: the
	// `WHERE cg.tax_collection_id IS NULL` clause makes this a no-op on
	// subsequent restarts.
	if res := db.Exec(`
		UPDATE capital_gains AS cg
		SET tax_collection_id = tc.id
		FROM tax_collections AS tc
		WHERE cg.tax_collection_id IS NULL
		  AND cg.user_id = tc.user_id
		  AND cg.system_type = tc.system_type
		  AND cg.tax_year = tc.year
		  AND cg.tax_month = tc.month
		  AND cg.account_id = tc.account_id
		  AND cg.currency = tc.currency
	`); res.Error != nil {
		log.Printf("WARN: capital_gains tax_collection_id backfill failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("backfilled tax_collection_id on %d capital_gains rows", res.RowsAffected)
	}

	// Composite unique indexes
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_listings_security_unique ON listings(security_id, security_type)")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_price_listing_date ON listing_daily_price_infos(listing_id, date)")
	// Drop the pre-rollup unique index (if present) before recreating the new
	// aggregation-key index. The old index included account_id, which caused
	// buys from two different accounts for the same user+security to create
	// two rows and required a holding_id on every sell. The new index keys on
	// (user_id, system_type, security_type, security_id) only.
	db.Exec("DROP INDEX IF EXISTS idx_holding_unique")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_holding_per_security ON holdings(user_id, system_type, security_type, security_id)")

	// Durable data-normalization: exchange-service only accepts 8 ISO currency
	// codes (RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD). Any other code on a
	// stock_exchanges row causes "currency unsupported" errors on cross-currency
	// orders (the listing's Exchange.Currency flows into exchange.Convert).
	// stock_exchanges is seeded from a CSV containing 40+ unsupported codes
	// (PLN, KRW, HKD, CNY, …). Rewrite every non-supported code to USD at
	// startup so the table is always in a state exchange-service can accept.
	// The source layer's NormalizeExchangeCurrency + the StockExchange.BeforeSave
	// hook handle new rows; this handles pre-existing rows + anything that slips
	// through a raw SQL insert or CSV seed path.
	if res := db.Exec(
		"UPDATE stock_exchanges SET currency = 'USD' WHERE currency NOT IN ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')",
	); res.Error != nil {
		log.Printf("WARN: exchange-currency normalization failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("normalized %d stock_exchanges rows to USD (was unsupported)", res.RowsAffected)
	}

	// Defense-in-depth: flag any forex_pairs row whose base or quote currency
	// is outside the supported-8 set. The source layer + ForexPair.BeforeSave
	// hook should already prevent this, so any positive count indicates either
	// historical bad data or a write path bypassing hooks. We log (not delete)
	// because forex pairs have listings referencing them by FK.
	var badForexCount int64
	if res := db.Raw(
		"SELECT COUNT(*) FROM forex_pairs WHERE base_currency NOT IN ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD') OR quote_currency NOT IN ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD') OR base_currency = quote_currency",
	).Scan(&badForexCount); res.Error != nil {
		log.Printf("WARN: forex-currency audit failed: %v", res.Error)
	} else if badForexCount > 0 {
		log.Printf("WARN: %d forex_pairs rows have unsupported or same base/quote currencies — investigate", badForexCount)
	}

	// --- Kafka ---
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()
	shared.EnsureTopics(cfg.KafkaBrokers,
		"stock.exchange-synced",
		"stock.security-synced",
		"stock.listing-updated",
		"stock.order-created",
		"stock.order-approved",
		"stock.order-declined",
		"stock.order-filled",
		"stock.order-cancelled",
		"stock.holding-updated",
		"stock.otc-trade-executed",
		"stock.tax-collected",
		"stock.option-exercised",
	)

	// --- InfluxDB ---
	influxClient := influx.NewClient(cfg.InfluxURL, cfg.InfluxToken, cfg.InfluxOrg, cfg.InfluxBucket)
	if influxClient != nil {
		defer influxClient.Close()
		log.Println("InfluxDB client connected")
	}

	// --- gRPC Client Connections ---

	// Account service client (for debit/credit)
	accountConn, err := grpc.NewClient(cfg.AccountGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to account-service: %v", err)
	}
	defer accountConn.Close()
	accountClient := accountpb.NewAccountServiceClient(accountConn)

	// Exchange service client (for currency conversion)
	exchangeConn, err := grpc.NewClient(cfg.ExchangeGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to exchange-service: %v", err)
	}
	defer exchangeConn.Close()
	exchangeClient := exchangepb.NewExchangeServiceClient(exchangeConn)

	// User service client (for name resolution + actuary limit enforcement)
	userConn, err := grpc.NewClient(cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to user-service: %v", err)
	}
	defer userConn.Close()
	userClient := userpb.NewUserServiceClient(userConn)
	actuaryStub := userpb.NewActuaryServiceClient(userConn)
	stockActuaryClient := stockgrpc.NewActuaryClient(actuaryStub)

	// Client service client (for name resolution)
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to client-service: %v", err)
	}
	defer clientConn.Close()
	clientClient := clientpb.NewClientServiceClient(clientConn)

	// --- Redis ---
	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	// --- Repositories ---
	exchangeRepo := repository.NewExchangeRepository(db)
	settingRepo := repository.NewSystemSettingRepository(db)
	stockRepo := repository.NewStockRepository(db)
	futuresRepo := repository.NewFuturesRepository(db)
	forexRepo := repository.NewForexPairRepository(db)
	optionRepo := repository.NewOptionRepository(db)

	listingRepo := repository.NewListingRepository(db)
	dailyPriceRepo := repository.NewListingDailyPriceRepository(db)
	orderRepo := repository.NewOrderRepository(db)
	orderTxRepo := repository.NewOrderTransactionRepository(db)
	wipeRepo := repository.NewWipeRepository(db)

	holdingRepo := repository.NewHoldingRepository(db)
	capitalGainRepo := repository.NewCapitalGainRepository(db)
	taxCollectionRepo := repository.NewTaxCollectionRepository(db)

	// --- Name Resolver ---
	nameResolver := service.UserNameResolver(func(userID uint64, systemType string) (string, string, error) {
		if systemType == "client" {
			resp, err := clientClient.GetClient(context.Background(), &clientpb.GetClientRequest{Id: userID})
			if err != nil {
				return "", "", err
			}
			return resp.FirstName, resp.LastName, nil
		}
		resp, err := userClient.GetEmployee(context.Background(), &userpb.GetEmployeeRequest{Id: int64(userID)})
		if err != nil {
			return "", "", err
		}
		return resp.FirstName, resp.LastName, nil
	})

	// --- Services ---
	exchangeSvc := service.NewExchangeService(exchangeRepo, settingRepo)

	secSvc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo, redisCache)
	listingSvc := service.NewListingService(listingRepo, dailyPriceRepo, stockRepo, futuresRepo, forexRepo)
	candleSvc := service.NewCandleService(influxClient)

	// --- External API Clients ---
	// Each is nil when its API key is not set (triggers fallback to static data).

	var avClient *provider.AlphaVantageClient
	if cfg.AlphaVantageAPIKey != "" {
		avClient = provider.NewAlphaVantageClient(cfg.AlphaVantageAPIKey)
	}

	var eodhClient *provider.EODHDClient
	if cfg.EODHDAPIKey != "" {
		eodhClient = provider.NewEODHDClient(cfg.EODHDAPIKey)
	}

	var alpacaClient *provider.AlpacaClient
	if cfg.AlpacaAPIKey != "" && cfg.AlpacaAPISecret != "" {
		alpacaClient = provider.NewAlpacaClient(cfg.AlpacaAPIKey, cfg.AlpacaAPISecret)
	}

	var finnhubClient *provider.FinnhubClient
	if cfg.FinnhubAPIKey != "" {
		finnhubClient = provider.NewFinnhubClient(cfg.FinnhubAPIKey)
	}

	// Build the external source and wire in the exchange resolver so it can map
	// acronyms (e.g. "NYSE", "FOREX") to DB IDs during seeding.
	extSource := source.NewExternalSource(
		alpacaClient, finnhubClient, eodhClient, avClient,
		cfg.ExchangeCSVPath, "data/futures_seed.json",
	).WithExchangeResolver(func(acronym string) (uint64, error) {
		ex, err := exchangeRepo.GetByAcronym(acronym)
		if err != nil {
			return 0, err
		}
		return ex.ID, nil
	})

	// Helper to construct a generated source on demand (used as both the
	// default and the restored choice).
	newGeneratedSource := func() source.Source {
		return source.NewGeneratedSource().WithExchangeResolver(func(acronym string) (uint64, error) {
			ex, err := exchangeRepo.GetByAcronym(acronym)
			if err != nil {
				return 0, err
			}
			return ex.ID, nil
		})
	}

	// Default to the generated source. External is opt-in via the admin
	// switch endpoint (or by seeding active_stock_source=external before boot).
	// Rationale: in the project environment, external providers (AlphaVantage,
	// Finnhub) routinely exhaust their free-tier quota, which the legacy
	// fallback would then interpret as zero-prices and wipe the market data.
	// Generated prices are deterministic, offline, and good enough for all
	// demo / test workflows.
	var initialSource source.Source = newGeneratedSource()
	if settingRepo != nil {
		if active, err := settingRepo.Get("active_stock_source"); err == nil && active != "" {
			switch active {
			case "external":
				initialSource = extSource
				log.Println("restored active stock source: external")
			case "generated":
				initialSource = newGeneratedSource()
				log.Println("restored active stock source: generated")
			case "simulator":
				client := source.NewSimulatorClient(cfg.MarketSimulatorURL, cfg.BankName, settingRepo)
				if err := client.EnsureRegistered(); err != nil {
					log.Printf("WARN: simulator registration failed on boot, falling back to generated: %v", err)
				} else {
					initialSource = source.NewSimulatorSource(client)
					log.Println("restored active stock source: simulator")
				}
			}
		} else {
			// No setting yet — persist the default so future restarts are
			// unambiguous and the admin endpoint reports the real value.
			if err := settingRepo.Set("active_stock_source", "generated"); err != nil {
				log.Printf("WARN: could not persist default active_stock_source: %v", err)
			}
			log.Println("initial stock source: generated (default)")
		}
	}

	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo,
		listingSvc, redisCache, influxClient,
		avClient, finnhubClient,
		initialSource,
		wipeRepo,
	)
	syncSvc.StartSimulatorRefreshLoopIfActive()

	// Portfolio, OTC, and tax services.
	// Placement-saga deps (sagaLogRepo, stockAccountClient) are wired below
	// so we defer the WithFillSaga upgrade until after they exist. The base
	// constructor is called first so OTC/tax services can share the same
	// instance without pulling in saga dependencies they don't need.
	portfolioSvc := service.NewPortfolioService(
		holdingRepo, capitalGainRepo, listingRepo,
		stockRepo, optionRepo,
		accountClient, nameResolver, cfg.StateAccountNo,
	)

	otcSvc := service.NewOTCService(
		holdingRepo, capitalGainRepo, listingRepo,
		accountClient, nameResolver,
	)

	taxSvc := service.NewTaxService(
		capitalGainRepo, taxCollectionRepo, holdingRepo,
		accountClient, exchangeClient, cfg.StateAccountNo,
	).WithDB(db)

	taxCronSvc := service.NewTaxCronService(taxSvc)

	// Long-lived ctx for background goroutines (seed, sync, crons, order execution).
	// Must be created BEFORE NewOrderExecutionEngine so the engine's baseCtx is
	// decoupled from any gRPC request ctx. See bug #3 in docs/Bugs.txt.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Order services
	securityLookup := service.NewSecurityLookupAdapter(stockRepo, futuresRepo, forexRepo, optionRepo)

	// Placement-saga dependencies (Task 12).
	sagaLogRepo := repository.NewSagaLogRepository(db)
	holdingReservationRepo := repository.NewHoldingReservationRepository(db)
	holdingReservationSvc := service.NewHoldingReservationService(db, holdingRepo, holdingReservationRepo)
	stockAccountClient := stockgrpc.NewAccountClient(accountClient)

	orderSvc := service.NewOrderService(
		orderRepo, orderTxRepo, listingRepo, settingRepo, securityLookup, producer,
		sagaLogRepo, stockAccountClient, exchangeClient, holdingReservationSvc,
		forexRepo, nil, // nil settings → uses defaults (5% slippage, 0.25% commission)
	).WithActuaryClient(stockActuaryClient)

	// Upgrade portfolioSvc with Phase-2 fill-saga deps so ProcessBuyFill and
	// ProcessSellFill run the saga paths. Buy saga:
	//   record_transaction → convert_amount → settle_reservation →
	//   update_holding → credit_commission.
	// Sell saga (Task 14):
	//   record_transaction → convert_amount → credit_proceeds →
	//   decrement_holding → credit_commission.
	// The legacy constructor path is retained in-struct and falls back if
	// any dep is nil. Settings = nil uses the default OrderSettings
	// (0.25% commission).
	portfolioSvc = portfolioSvc.WithFillSaga(
		sagaLogRepo, orderTxRepo, exchangeClient, stockAccountClient, holdingReservationSvc, nil,
	)

	// Wire the forex-specific fill path (Task 15). Forex fills don't go
	// through the stock saga: they debit the user's quote account and
	// credit their base account with no holding row. The bank-commission
	// recipient adapter exposes the pre-seeded state account to the forex
	// saga without pulling the full PortfolioService lookup logic in.
	// Commissions go to the bank's RSD account (discovered dynamically via
	// account-service.GetBankRSDAccount and cached for 5 minutes).
	// cfg.StateAccountNo is reserved for capital-gains tax in tax_service.
	bankCommissionRecipient := newBankCommissionAccountAdapter(accountConn)
	forexFillSvc := service.NewForexFillService(
		sagaLogRepo, stockAccountClient, orderTxRepo, nil, bankCommissionRecipient,
	)
	portfolioSvc = portfolioSvc.WithForexFillService(forexFillSvc)
	// Route stock/futures/options commission credits through the same bank-RSD
	// recipient as forex. stateAccountNo is NOT used for commissions anymore —
	// it's reserved for capital-gains tax in tax_service.
	portfolioSvc = portfolioSvc.WithBankCommissionRecipient(bankCommissionRecipient)
	// Part B: wire the per-holding transaction history repo so
	// ListHoldingTransactions returns real data.
	portfolioSvc = portfolioSvc.WithHoldingTxRepo(orderTxRepo)
	execEngine := service.NewOrderExecutionEngine(ctx, orderRepo, orderTxRepo, listingRepo, settingRepo, producer, portfolioSvc)

	// --- Seed securities ---

	go func() {
		syncSvc.SeedAll(ctx, "data/futures_seed.json")
	}()

	// Start periodic price refresh
	syncSvc.StartPeriodicRefresh(ctx, cfg.SecuritySyncIntervalMins)

	// Start daily price snapshot cron
	listingCron := service.NewListingCronService(listingRepo, dailyPriceRepo, influxClient)
	listingCron.StartDailyCron(ctx)

	// Seed initial price history after listings are created
	go func() {
		// Wait for seed to complete (the seed goroutine runs async)
		time.Sleep(5 * time.Second)
		listingCron.SeedInitialSnapshot()
	}()

	// Start execution engine for active orders
	execEngine.Start(ctx)

	// Start the saga recovery reconciler. Runs once at boot (to pick up rows
	// stuck from a prior crash) and then every 60 seconds until ctx is
	// cancelled. Must use the long-lived main ctx so the ticker lives for the
	// process lifetime and honors graceful shutdown via cancel().
	sagaRecovery := service.NewSagaRecovery(sagaLogRepo, stockAccountClient, orderRepo, cfg.StateAccountNo)
	sagaRecovery.Run(ctx, 60*time.Second)

	// Start tax collection cron
	taxCronSvc.StartMonthlyCron(ctx)

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	if err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterStockExchangeGRPCServiceServer(s, handler.NewExchangeGRPCHandler(exchangeSvc))
			pb.RegisterSecurityGRPCServiceServer(s, handler.NewSecurityHandler(secSvc, listingSvc, candleSvc, listingRepo))
			pb.RegisterOrderGRPCServiceServer(s, handler.NewOrderHandler(orderSvc, execEngine))
			pb.RegisterPortfolioGRPCServiceServer(s, handler.NewPortfolioHandler(portfolioSvc, taxSvc))
			pb.RegisterOTCGRPCServiceServer(s, handler.NewOTCHandler(otcSvc))
			pb.RegisterTaxGRPCServiceServer(s, handler.NewTaxHandler(taxSvc))
			sourceAdminHandler := handler.NewSourceAdminHandler(syncSvc, func(name string) (source.Source, error) {
				switch name {
				case "external":
					return extSource, nil
				case "generated":
					return source.NewGeneratedSource().WithExchangeResolver(func(acronym string) (uint64, error) {
						ex, err := exchangeRepo.GetByAcronym(acronym)
						if err != nil {
							return 0, err
						}
						return ex.ID, nil
					}), nil
				case "simulator":
					simClient := source.NewSimulatorClient(cfg.MarketSimulatorURL, cfg.BankName, settingRepo)
					if err := simClient.EnsureRegistered(); err != nil {
						return nil, fmt.Errorf("simulator registration: %w", err)
					}
					return source.NewSimulatorSource(simClient), nil
				default:
					return nil, fmt.Errorf("unknown source %q", name)
				}
			})
			pb.RegisterSourceAdminServiceServer(s, sourceAdminHandler)
			shared.RegisterHealthCheck(s, "stock-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			log.Printf("stock-service listening on %s", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
	cancel()
}

// bankCommissionAccountAdapter satisfies service.BankCommissionRecipient by
// resolving the bank's RSD account number dynamically via account-service's
// BankAccountService.GetBankRSDAccount RPC. "Dynamically" because the seed
// assigns a different account_number on every reseed — hardcoding it would
// break after a docker compose down -v. Caches the result for 5 minutes to
// avoid hitting account-service on every fill.
//
// Used for every fee/commission credit in stock-service (securities trade
// commission, forex trade commission, OTC commission). Separate from
// cfg.StateAccountNo which is reserved for capital-gains tax collection.
type bankCommissionAccountAdapter struct {
	bankClient  accountpb.BankAccountServiceClient
	mu          sync.Mutex
	cached      string
	cachedAt    time.Time
	cacheTTL    time.Duration
}

func newBankCommissionAccountAdapter(conn *grpc.ClientConn) *bankCommissionAccountAdapter {
	return &bankCommissionAccountAdapter{
		bankClient: accountpb.NewBankAccountServiceClient(conn),
		cacheTTL:   5 * time.Minute,
	}
}

func (a *bankCommissionAccountAdapter) BankCommissionAccountNumber(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.cached != "" && time.Since(a.cachedAt) < a.cacheTTL {
		defer a.mu.Unlock()
		return a.cached, nil
	}
	a.mu.Unlock()

	resp, err := a.bankClient.GetBankRSDAccount(ctx, &accountpb.GetBankRSDAccountRequest{})
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.cached = resp.AccountNumber
	a.cachedAt = time.Now()
	a.mu.Unlock()
	return resp.AccountNumber, nil
}
