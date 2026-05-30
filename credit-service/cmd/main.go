package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	adminpb "github.com/exbanka/contract/adminpb"
	clientpb "github.com/exbanka/contract/clientpb"
	pb "github.com/exbanka/contract/creditpb"
	"github.com/exbanka/contract/cronreg"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/credit-service/internal/config"
	"github.com/exbanka/credit-service/internal/handler"
	kafkaprod "github.com/exbanka/credit-service/internal/kafka"
	"github.com/exbanka/credit-service/internal/model"
	"github.com/exbanka/credit-service/internal/repository"
	"github.com/exbanka/credit-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.LoanRequest{}, &model.Loan{}, &model.Installment{}, &model.InterestRateTier{}, &model.BankMargin{}, &model.Changelog{}, &model.IdempotencyRecord{}, &model.SagaLog{}, &cronreg.CronPauseState{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	cronRegistry := cronreg.NewRegistry("credit-service", cronreg.NewGormPauseStore(db))

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"credit.loan-requested",
		"credit.loan-approved",
		"credit.loan-rejected",
		"credit.loan-disbursed",
		"credit.installment-collected",
		"credit.installment-failed",
		"credit.variable-rate-adjusted",
		"credit.late-penalty-applied",
		"credit.changelog",
		"notification.send-email",
		"notification.general",
		"credit.saga-dead-letter",
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
	bankAccountClient := accountpb.NewBankAccountServiceClient(accountConn)

	// Connect to client-service
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to client service: %v", err)
	}
	defer clientConn.Close()
	clientClient := clientpb.NewClientServiceClient(clientConn)

	// Connect to user-service for employee limit checks
	userConn, err := grpc.NewClient(cfg.UserGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	limitClient := userpb.NewEmployeeLimitServiceClient(userConn)

	// Fetch the bank's RSD account number at startup (non-fatal if unavailable)
	var bankRSDAccount string
	bankRSDResp, bankRSDErr := bankAccountClient.GetBankRSDAccount(context.Background(), &accountpb.GetBankRSDAccountRequest{})
	if bankRSDErr != nil {
		log.Printf("warn: could not fetch bank RSD account at startup: %v", bankRSDErr)
	} else {
		bankRSDAccount = bankRSDResp.AccountNumber
		log.Printf("credit-service: bank RSD account: %s", bankRSDAccount)
	}

	loanRequestRepo := repository.NewLoanRequestRepository(db)
	loanRepo := repository.NewLoanRepository(db)
	installmentRepo := repository.NewInstallmentRepository(db)
	tierRepo := repository.NewInterestRateTierRepository(db)
	marginRepo := repository.NewBankMarginRepository(db)

	rateConfigSvc := service.NewRateConfigService(tierRepo, marginRepo, db)
	if err := rateConfigSvc.SeedDefaults(); err != nil {
		log.Fatalf("failed to seed interest rate config: %v", err)
	}

	changelogRepo := repository.NewChangelogRepository(db)
	sagaRepo := repository.NewSagaLogRepository(db)
	disbursementSaga := service.NewLoanDisbursementSaga(bankAccountClient, accountClient, loanRepo, sagaRepo)
	loanRequestSvc := service.NewLoanRequestService(loanRequestRepo, loanRepo, installmentRepo, limitClient, accountClient, rateConfigSvc, db, changelogRepo)
	loanRequestSvc.SetBankAccountClient(bankAccountClient)
	loanRequestSvc.SetDisbursementSaga(disbursementSaga)
	loanSvc := service.NewLoanService(loanRepo)
	installmentSvc := service.NewInstallmentService(installmentRepo)
	changelogSvc := service.NewChangelogService(changelogRepo)
	cronSvc := service.NewCronService(installmentSvc, loanSvc, accountClient, bankAccountClient, clientClient, producer, bankRSDAccount, db, cronRegistry)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cronSvc.Start(ctx)

	// Start the loan disbursement saga compensation recovery worker.
	// It polls credit_saga_logs for stuck "compensating" rows and retries them.
	compensationRecovery := service.NewCompensationRecovery(sagaRepo, disbursementSaga, producer, cronRegistry)
	compensationRecovery.Start(ctx)

	grpcHandler := handler.NewCreditGRPCHandler(loanRequestSvc, loanSvc, installmentSvc, rateConfigSvc, loanRepo, installmentRepo, producer, changelogSvc)

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
				grpcmw.UnaryLoggingInterceptor("credit-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterCreditServiceServer(s, grpcHandler)
			adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
			shared.RegisterHealthCheck(s, "credit-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("credit service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
	cancel()
	log.Println("Server stopped")
}
