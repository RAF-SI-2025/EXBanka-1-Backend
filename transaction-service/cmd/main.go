package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
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
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	if err := db.AutoMigrate(
		&model.Payment{},
		&model.Transfer{},
		&model.PaymentRecipient{},
		&model.TransferFee{},
		&model.SagaLog{},
		&model.Bank{},
		&model.InterBankTransaction{},
		&model.IdempotencyRecord{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

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
		"transfer.interbank-prepared",
		"transfer.interbank-committed",
		"transfer.interbank-received",
		"transfer.interbank-rolled-back",
		"notification.send-email",
		"notification.general",
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
	banksRepo := repository.NewBanksRepository(db)
	ibTxRepo := repository.NewInterBankTxRepository(db)
	idemRepo := repository.NewIdempotencyRepository(db)
	seedPeerBanks(banksRepo, cfg)

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
	transferSvc := service.NewTransferService(transferRepo, exchangeClient, accountClient, bankClient, feeSvc, producer, sagaLogRepo)
	transferSvc.StartCompensationRecovery(ctx)
	recipientSvc := service.NewPaymentRecipientService(recipientRepo)

	grpcHandler := handler.NewTransactionGRPCHandler(
		paymentSvc,
		transferSvc,
		recipientSvc,
		verificationClient,
		producer,
	)

	feeHandler := handler.NewFeeGRPCHandler(feeSvc)

	// Build inter-bank service stack.
	ibPeerClients := buildPeerBankClients(cfg)
	ibPeerRouter := service.NewStaticPeerBankRouter(ibPeerClients)
	ibAccountClient := service.NewGRPCAccountInterBankClient(accountClient)
	ibFeeRules := service.NewFeeServiceInterBankAdapter(feeSvc)
	interBankSvc := service.NewInterBankService(
		db, ibTxRepo, banksRepo, ibPeerRouter, ibAccountClient, exchangeClient,
		ibFeeRules, producer,
		service.InterBankServiceConfig{
			OwnBankCode:  cfg.OwnBankCode,
			ReceiverWait: cfg.InterbankReceiverWait,
		},
	)
	interBankHandler := handler.NewInterBankGRPCHandler(interBankSvc, db, idemRepo)

	// Crash-recovery sweep — run synchronously before serving so any
	// commit_received receiver rows finish their CommitIncoming and any
	// stuck sender rows are nudged to reconciling.
	ibRecovery := service.NewInterBankRecovery(interBankSvc, cfg.InterbankPrepareTimeout, cfg.InterbankCommitTimeout)
	if err := ibRecovery.RunOnce(ctx); err != nil {
		log.Printf("warn: inter-bank recovery sweep: %v", err)
	}
	// Reconciler + receiver-timeout crons.
	service.NewInterBankReconciler(interBankSvc,
		cfg.InterbankReconcileInterval,
		cfg.InterbankPrepareTimeout,
		cfg.InterbankReconcileStaleAfter,
		cfg.InterbankReconcileMaxRetries,
	).Start(ctx)
	service.NewInterBankTimeoutCron(interBankSvc,
		cfg.InterbankReconcileInterval,
		cfg.InterbankReceiverWait,
	).Start(ctx)

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
			pb.RegisterInterBankServiceServer(s, interBankHandler)
			shared.RegisterHealthCheck(s, "transaction-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("transaction service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}

// buildPeerBankClients constructs one PeerBankClient per peer whose env
// vars are populated. Keys are the peer's 3-digit bank code. The
// InterBankService routes outbound traffic through these clients.
func buildPeerBankClients(cfg *config.Config) map[string]*service.PeerBankClient {
	out := map[string]*service.PeerBankClient{}
	type peer struct {
		code, baseURL, outboundKey string
	}
	peers := []peer{
		{"222", cfg.Peer222BaseURL, cfg.Peer222OutboundKey},
		{"333", cfg.Peer333BaseURL, cfg.Peer333OutboundKey},
		{"444", cfg.Peer444BaseURL, cfg.Peer444OutboundKey},
	}
	for _, p := range peers {
		if p.baseURL == "" {
			continue
		}
		out[p.code] = service.NewPeerBankClient(cfg.OwnBankCode, p.code, p.outboundKey, p.baseURL, cfg.InterbankPrepareTimeout)
	}
	return out
}

// seedPeerBanks idempotently inserts/updates rows in `banks` for each peer
// whose env vars are set. Plaintext keys live in env at runtime; the bcrypt
// hashes stored here are for audit and rotation only.
func seedPeerBanks(repo *repository.BanksRepository, cfg *config.Config) {
	type peer struct {
		code, baseURL, outboundKey, inboundKey string
	}
	peers := []peer{
		{"222", cfg.Peer222BaseURL, cfg.Peer222OutboundKey, cfg.Peer222InboundKey},
		{"333", cfg.Peer333BaseURL, cfg.Peer333OutboundKey, cfg.Peer333InboundKey},
		{"444", cfg.Peer444BaseURL, cfg.Peer444OutboundKey, cfg.Peer444InboundKey},
	}
	for _, p := range peers {
		if p.baseURL == "" || p.inboundKey == "" {
			continue
		}
		outHash, err := bcrypt.GenerateFromPassword([]byte(p.outboundKey), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("warn: bcrypt outbound key for peer %s: %v", p.code, err)
			continue
		}
		inHash, err := bcrypt.GenerateFromPassword([]byte(p.inboundKey), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("warn: bcrypt inbound key for peer %s: %v", p.code, err)
			continue
		}
		if err := repo.Upsert(&model.Bank{
			Code:                  p.code,
			Name:                  "Bank " + p.code,
			BaseURL:               p.baseURL,
			APIKeyBcrypted:        string(outHash),
			InboundAPIKeyBcrypted: string(inHash),
			Active:                true,
		}); err != nil {
			log.Printf("warn: seed peer bank %s: %v", p.code, err)
		} else {
			log.Printf("Seeded peer bank %s -> %s", p.code, p.baseURL)
		}
	}
}
