package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
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
	// The source layer's NormalizeExchangeCurrency handles new rows; this
	// handles pre-existing rows + any that slip through via the CSV seed.
	if res := db.Exec(
		"UPDATE stock_exchanges SET currency = 'USD' WHERE currency NOT IN ('RSD','EUR','CHF','USD','GBP','JPY','CAD','AUD')",
	); res.Error != nil {
		log.Printf("WARN: exchange-currency normalization failed: %v", res.Error)
	} else if res.RowsAffected > 0 {
		log.Printf("normalized %d stock_exchanges rows to USD (was unsupported)", res.RowsAffected)
	}

	// --- Kafka ---
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
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

	// Restore the last-active data source so a restart preserves the admin's choice.
	var initialSource source.Source = extSource // default fallback
	if settingRepo != nil {
		if active, err := settingRepo.Get("active_stock_source"); err == nil && active != "" {
			switch active {
			case "external":
				initialSource = extSource
			case "generated":
				initialSource = source.NewGeneratedSource().WithExchangeResolver(func(acronym string) (uint64, error) {
					ex, err := exchangeRepo.GetByAcronym(acronym)
					if err != nil {
						return 0, err
					}
					return ex.ID, nil
				})
				log.Println("restored active stock source: generated")
			case "simulator":
				client := source.NewSimulatorClient(cfg.MarketSimulatorURL, cfg.BankName, settingRepo)
				if err := client.EnsureRegistered(); err != nil {
					log.Printf("WARN: simulator registration failed on boot, falling back to external: %v", err)
				} else {
					initialSource = source.NewSimulatorSource(client)
					log.Println("restored active stock source: simulator")
				}
			}
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
	securityLookup := service.NewSecurityLookupAdapter(futuresRepo)

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
	bankCommissionRecipient := bankCommissionAccountAdapter{accountNo: cfg.StateAccountNo}
	forexFillSvc := service.NewForexFillService(
		sagaLogRepo, stockAccountClient, orderTxRepo, nil, bankCommissionRecipient,
	)
	portfolioSvc = portfolioSvc.WithForexFillService(forexFillSvc)
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

	// --- gRPC Server ---
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)

	// Register handlers
	exchangeHandler := handler.NewExchangeGRPCHandler(exchangeSvc)
	pb.RegisterStockExchangeGRPCServiceServer(grpcServer, exchangeHandler)

	securityHandler := handler.NewSecurityHandler(secSvc, listingSvc, candleSvc, listingRepo)
	pb.RegisterSecurityGRPCServiceServer(grpcServer, securityHandler)

	orderHandler := handler.NewOrderHandler(orderSvc, execEngine)
	pb.RegisterOrderGRPCServiceServer(grpcServer, orderHandler)

	// Portfolio handler
	portfolioHandler := handler.NewPortfolioHandler(portfolioSvc, taxSvc)
	pb.RegisterPortfolioGRPCServiceServer(grpcServer, portfolioHandler)

	// OTC handler
	otcHandler := handler.NewOTCHandler(otcSvc)
	pb.RegisterOTCGRPCServiceServer(grpcServer, otcHandler)

	// Tax handler
	taxHandler := handler.NewTaxHandler(taxSvc)
	pb.RegisterTaxGRPCServiceServer(grpcServer, taxHandler)

	// Source admin handler
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
	pb.RegisterSourceAdminServiceServer(grpcServer, sourceAdminHandler)

	shared.RegisterHealthCheck(grpcServer, "stock-service")
	metrics.InitializeGRPCMetrics(grpcServer)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// --- Graceful shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down stock-service...")
		cancel()
		grpcServer.GracefulStop()
	}()

	markReady()
	log.Printf("stock-service listening on %s", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}
}

// bankCommissionAccountAdapter satisfies service.BankCommissionRecipient by
// returning the pre-seeded state-account number from config. Kept here (not
// in internal/service) so the package stays free of config dependencies.
type bankCommissionAccountAdapter struct {
	accountNo string
}

func (a bankCommissionAccountAdapter) BankCommissionAccountNumber(context.Context) (string, error) {
	return a.accountNo, nil
}
