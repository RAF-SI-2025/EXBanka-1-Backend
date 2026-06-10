package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	adminpb "github.com/exbanka/contract/adminpb"
	"github.com/exbanka/contract/cronreg"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/logger"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	pb "github.com/exbanka/contract/transactionpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/transaction-service/internal/config"
	"github.com/exbanka/transaction-service/internal/handler"
	kafkaprod "github.com/exbanka/transaction-service/internal/kafka"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
	"github.com/exbanka/transaction-service/internal/service"
)

func main() {
	logger.Init("transaction-service")
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Phase 1 SI-TX cleanup: drop legacy tables that previously held
	// InterBankTransaction / Bank rows. The corresponding GORM models
	// have been deleted; AutoMigrate no longer recreates them. The DROPs
	// run on every startup but are idempotent. Replaced in Phase 2 with
	// SI-TX-shape peer_banks / peer_idempotence_records / outbound_peer_txs.
	if err := db.Exec("DROP TABLE IF EXISTS inter_bank_transactions").Error; err != nil {
		log.Printf("warn: drop inter_bank_transactions failed: %v", err)
	}
	if err := db.Exec("DROP TABLE IF EXISTS banks").Error; err != nil {
		log.Printf("warn: drop banks failed: %v", err)
	}

	if err := db.AutoMigrate(
		&model.Payment{},
		&model.Transfer{},
		&model.PaymentRecipient{},
		&model.TransferFee{},
		&model.SagaLog{},
		// SI-TX peer tables (peer_banks / peer_idempotence_records /
		// outbound_peer_txs) moved to interbank-service (2026-06-07 cutover).
		// They are no longer migrated here; existing rows in transaction_db are
		// orphaned and may be dropped manually once peer_banks is migrated to
		// interbank_db (see docs/superpowers/plans/2026-06-06-interbank-cutover.md).
		&cronreg.CronPauseState{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	cronRegistry := cronreg.NewRegistry("transaction-service", cronreg.NewGormPauseStore(db))

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"transaction.payment-created",
		"transaction.payment-completed",
		"transaction.payment-failed",
		"transaction.transfer-created",
		"transaction.transfer-completed",
		"transaction.transfer-failed",
		"transaction.saga-dead-letter",
		"notification.send-email",
		"notification.general",
		"admin.cron-action",
	)

	// Connect to account-service
	accountConn, err := grpc.NewClient(cfg.AccountGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to account service: %v", err)
	}
	defer accountConn.Close()
	accountClient := accountpb.NewAccountServiceClient(accountConn)

	// Connect to exchange-service
	exchangeConn, err := grpc.NewClient(cfg.ExchangeGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to exchange service: %v", err)
	}
	defer exchangeConn.Close()
	exchangeGRPCClient := exchangepb.NewExchangeServiceClient(exchangeConn)
	exchangeClient := service.NewGRPCExchangeClient(exchangeGRPCClient)

	// Connect to verification-service
	verificationConn, err := grpc.NewClient(cfg.VerificationGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to verification service: %v", err)
	}
	defer verificationConn.Close()
	verificationClient := verificationpb.NewVerificationGRPCServiceClient(verificationConn)

	paymentRepo := repository.NewPaymentRepository(db)
	transferRepo := repository.NewTransferRepository(db)
	recipientRepo := repository.NewPaymentRecipientRepository(db)
	sagaLogRepo := repository.NewSagaLogRepository(db)

	feeRepo := repository.NewTransferFeeRepository(db)
	feeSvc := service.NewFeeService(feeRepo)

	// Seed default fee rules if none exist
	existingFees, _ := feeSvc.ListFees()
	if len(existingFees) == 0 {
		_ = feeSvc.CreateFee(&model.TransferFee{
			Name:            "Standard Payment Fee",
			FeeType:         "percentage",
			FeeValue:        decimal.NewFromFloat(0.1),
			MinAmount:       decimal.NewFromInt(1000),
			TransactionType: "all",
			Active:          true,
		})
		log.Println("Seeded default payment fee (0.1%)")
		_ = feeSvc.CreateFee(&model.TransferFee{
			Name:            "Default Commission",
			FeeType:         "percentage",
			FeeValue:        decimal.NewFromFloat(5.0),
			MinAmount:       decimal.NewFromInt(5000),
			TransactionType: "all",
			Active:          true,
		})
		log.Println("Seeded default commission (5% for transactions >= 5000 RSD)")
	}

	// Reuse existing account connection for BankAccountServiceClient
	bankRSDAccountNumber := ""
	bankClient := accountpb.NewBankAccountServiceClient(accountConn)
	bankResp, bankRSDErr := bankClient.GetBankRSDAccount(context.Background(), &accountpb.GetBankRSDAccountRequest{})
	if bankRSDErr == nil && bankResp != nil {
		bankRSDAccountNumber = bankResp.GetAccountNumber()
		log.Printf("Bank RSD account: %s", bankRSDAccountNumber)
	} else {
		log.Printf("warn: could not fetch bank RSD account, fees will not be credited to bank: %v", bankRSDErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paymentSvc := service.NewPaymentService(paymentRepo, accountClient, feeSvc, producer, bankRSDAccountNumber, sagaLogRepo)
	transferSvc := service.NewTransferService(transferRepo, exchangeClient, accountClient, bankClient, feeSvc, producer, sagaLogRepo).
		WithCronRegistry(cronRegistry)
	transferSvc.StartCompensationRecovery(ctx)
	recipientSvc := service.NewPaymentRecipientService(recipientRepo)

	grpcHandler := handler.NewTransactionGRPCHandler(
		paymentSvc,
		transferSvc,
		recipientSvc,
		verificationClient,
		producer,
	).WithSagaLogReader(sagaLogRepo)

	feeHandler := handler.NewFeeGRPCHandler(feeSvc)

	// Cross-bank SI-TX engine REMOVED (2026-06-07 cutover). The whole
	// cross-bank surface — the 2PC engine (PeerTxService), the peer_banks
	// registry (PeerBankAdminService), the outbound-replay + reconciler
	// crons, and the outbound HTTP egress — now lives in interbank-service.
	// transaction-service is pure local payments / transfers / fees.

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
				grpcmw.UnaryLoggingInterceptor("transaction-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterTransactionServiceServer(s, grpcHandler)
			pb.RegisterFeeServiceServer(s, feeHandler)
			// PeerBankAdminService + PeerTxService moved to interbank-service
			// (2026-06-07 cutover) — no longer registered here.
			adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
			shared.RegisterHealthCheck(s, "transaction-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			slog.Info("transaction service listening", "addr", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}
