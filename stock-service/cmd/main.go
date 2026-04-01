package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/config"
	"github.com/exbanka/stock-service/internal/handler"
	kafkaprod "github.com/exbanka/stock-service/internal/kafka"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/provider"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

func main() {
	cfg := config.Load()

	// --- Database ---
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
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
	); err != nil {
		log.Fatalf("auto-migrate failed: %v", err)
	}

	// Composite unique indexes
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_listings_security_unique ON listings(security_id, security_type)")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_price_listing_date ON listing_daily_price_infos(listing_id, date)")

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
	)

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

	// --- Services ---
	exchangeSvc := service.NewExchangeService(exchangeRepo, settingRepo)

	// Seed exchanges from CSV on startup
	if err := exchangeSvc.SeedExchanges(cfg.ExchangeCSVPath); err != nil {
		log.Printf("WARN: failed to seed exchanges from CSV: %v", err)
	}

	secSvc := service.NewSecurityService(stockRepo, futuresRepo, forexRepo, optionRepo, exchangeRepo)
	listingSvc := service.NewListingService(listingRepo, dailyPriceRepo, stockRepo, futuresRepo, forexRepo)

	// AlphaVantage client (nil if no API key)
	var avClient *provider.AlphaVantageClient
	if cfg.AlphaVantageAPIKey != "" {
		avClient = provider.NewAlphaVantageClient(cfg.AlphaVantageAPIKey)
	}

	syncSvc := service.NewSecuritySyncService(
		stockRepo, futuresRepo, forexRepo, optionRepo,
		exchangeRepo, settingRepo, avClient, listingSvc,
	)

	// Order services
	securityLookup := service.NewSecurityLookupAdapter(futuresRepo)
	orderSvc := service.NewOrderService(orderRepo, orderTxRepo, listingRepo, settingRepo, securityLookup, producer)
	execEngine := service.NewOrderExecutionEngine(orderRepo, orderTxRepo, listingRepo, settingRepo, producer)
	otcSvc := service.NewOTCService(orderRepo, orderTxRepo)

	// --- Seed securities ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		syncSvc.SeedAll(ctx, "data/futures_seed.json")
	}()

	// Start periodic price refresh
	syncSvc.StartPeriodicRefresh(ctx, cfg.SecuritySyncIntervalMins)

	// Start daily price snapshot cron
	listingCron := service.NewListingCronService(listingRepo, dailyPriceRepo)
	listingCron.StartDailyCron(ctx)

	// Seed initial price history after listings are created
	go func() {
		// Wait for seed to complete (the seed goroutine runs async)
		time.Sleep(5 * time.Second)
		listingCron.SeedInitialSnapshot()
	}()

	// Start execution engine for active orders
	execEngine.Start(ctx)

	// --- gRPC Server ---
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	// Register handlers
	exchangeHandler := handler.NewExchangeGRPCHandler(exchangeSvc)
	pb.RegisterStockExchangeGRPCServiceServer(grpcServer, exchangeHandler)

	securityHandler := handler.NewSecurityHandler(secSvc, listingSvc)
	pb.RegisterSecurityGRPCServiceServer(grpcServer, securityHandler)

	orderHandler := handler.NewOrderHandler(orderSvc, execEngine)
	pb.RegisterOrderGRPCServiceServer(grpcServer, orderHandler)

	otcHandler := handler.NewOTCHandler(otcSvc)
	pb.RegisterOTCGRPCServiceServer(grpcServer, otcHandler)

	shared.RegisterHealthCheck(grpcServer, "stock-service")

	// --- Graceful shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down stock-service...")
		cancel()
		grpcServer.GracefulStop()
	}()

	log.Printf("stock-service listening on %s", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("gRPC server failed: %v", err)
	}
}
