package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/exbanka/account-service/internal/cache"
	"github.com/exbanka/account-service/internal/config"
	"github.com/exbanka/account-service/internal/handler"
	kafkaprod "github.com/exbanka/account-service/internal/kafka"
	"github.com/exbanka/account-service/internal/model"
	"github.com/exbanka/account-service/internal/repository"
	"github.com/exbanka/account-service/internal/service"
	pb "github.com/exbanka/contract/accountpb"
	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Currency{}, &model.Company{}, &model.Account{}, &model.LedgerEntry{}, &model.Changelog{}, &model.BankOperation{}, &model.AccountReservation{}, &model.AccountReservationSettlement{}, &model.IncomingReservation{}, &model.IdempotencyRecord{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	if err := model.SeedCurrencies(db); err != nil {
		log.Printf("warn: failed to seed currencies: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"account.created",
		"account.status-changed",
		"account.name-updated",
		"account.limits-updated",
		"account.maintenance-charged",
		"account.spending-reset",
		"account.changelog",
		"notification.send-email",
		"notification.general",
	)

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	// Connect to client-service for email lookup on account creation.
	clientConn, clientConnErr := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if clientConnErr != nil {
		log.Printf("warn: failed to connect to client service: %v", clientConnErr)
	}
	var clientClient clientpb.ClientServiceClient
	if clientConn != nil {
		defer clientConn.Close()
		clientClient = clientpb.NewClientServiceClient(clientConn)
	}

	accountRepo := repository.NewAccountRepository(db)
	companyRepo := repository.NewCompanyRepository(db)
	currencyRepo := repository.NewCurrencyRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)
	bankRepo := repository.NewBankAccountRepository(db)
	reservationRepo := repository.NewAccountReservationRepository(db)
	incomingReservationRepo := repository.NewIncomingReservationRepository(db)
	idempRepo := repository.NewIdempotencyRepository(db)

	accountService := service.NewAccountService(accountRepo, db, redisCache, changelogRepo)
	accountService.SetBankRepo(bankRepo)
	companyService := service.NewCompanyService(companyRepo)
	currencyService := service.NewCurrencyService(currencyRepo)
	ledgerService := service.NewLedgerService(ledgerRepo, db)
	reservationService := service.NewReservationService(db, accountRepo, reservationRepo, ledgerRepo)
	incomingReservationService := service.NewIncomingReservationService(db, accountRepo, incomingReservationRepo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spendingCron := service.NewSpendingCronService(accountRepo)
	spendingCron.Start(ctx)

	maintenanceCron := service.NewMaintenanceCronService(accountRepo, ledgerService)
	maintenanceCron.Start(ctx)

	// Seed bank accounts for all supported currencies (idempotent)
	bankAccounts, _ := accountService.ListBankAccounts()
	existingCurrencies := make(map[string]bool)
	for _, a := range bankAccounts {
		existingCurrencies[a.CurrencyCode] = true
	}
	seedCurrencies := []struct {
		Code string
		Kind string
		Name string
	}{
		{"RSD", "current", "EX Banka RSD Account"},
		{"EUR", "foreign", "EX Banka EUR Account"},
		{"CHF", "foreign", "EX Banka CHF Account"},
		{"USD", "foreign", "EX Banka USD Account"},
		{"GBP", "foreign", "EX Banka GBP Account"},
		{"JPY", "foreign", "EX Banka JPY Account"},
		{"CAD", "foreign", "EX Banka CAD Account"},
		{"AUD", "foreign", "EX Banka AUD Account"},
	}
	for _, c := range seedCurrencies {
		if existingCurrencies[c.Code] {
			continue
		}
		if _, err := accountService.CreateBankAccount(c.Code, c.Kind, c.Name, decimal.NewFromInt(10_000_000)); err != nil {
			log.Printf("warn: failed to seed bank %s account: %v", c.Code, err)
		} else {
			log.Printf("Seeded bank %s account", c.Code)
		}
	}

	// Seed State (Government) entity - one RSD account for tax collection (idempotent)
	if _, err := companyService.GetByOwnerID(service.StateOwnerID); err != nil {
		stateCompany := &model.Company{
			CompanyName:        "Republika Srbija",
			RegistrationNumber: "00000001",
			TaxNumber:          "000000001",
			ActivityCode:       "84.11",
			Address:            "Beograd, Srbija",
			OwnerID:            service.StateOwnerID,
		}
		if err := companyService.Create(stateCompany); err != nil {
			log.Printf("warn: failed to seed state company: %v", err)
		} else {
			log.Println("Seeded state company: Republika Srbija")

			stateAccount := &model.Account{
				AccountName:    "Državni račun za poreze",
				OwnerID:        service.StateOwnerID,
				OwnerName:      "Republika Srbija",
				CurrencyCode:   "RSD",
				Status:         "active",
				AccountKind:    "current",
				AccountType:    "standard",
				IsBankAccount:  false,
				AccountNumber:  "0000000000000099", // well-known state account number for tax deposits
				ExpiresAt:      time.Now().AddDate(50, 0, 0),
				MaintenanceFee: decimal.Zero,
				CompanyID:      &stateCompany.ID,
			}
			if err := accountRepo.Create(stateAccount); err != nil {
				log.Printf("warn: failed to seed state RSD account: %v", err)
			} else {
				log.Println("Seeded state RSD account")
			}
		}
	}

	reconcileSvc := service.NewReconciliationService(db, ledgerService)
	reconcileSvc.CheckAllBalances(ctx)

	reservationHandler := handler.NewReservationHandler(reservationService)
	grpcHandler := handler.NewAccountGRPCHandler(accountService, companyService, currencyService, ledgerService, reservationHandler, incomingReservationService, producer, clientClient, db, idempRepo)
	bankAccountHandler := handler.NewBankAccountGRPCHandler(accountService, producer)

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	if err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				metrics.GRPCUnaryServerInterceptor(),
				grpcmw.UnaryLoggingInterceptor("account-service"),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterAccountServiceServer(s, grpcHandler)
			pb.RegisterBankAccountServiceServer(s, bankAccountHandler)
			shared.RegisterHealthCheck(s, "account-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("account service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}
