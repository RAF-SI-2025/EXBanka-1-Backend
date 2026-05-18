package main

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/config"
	"github.com/exbanka/verification-service/internal/handler"
	kafkaprod "github.com/exbanka/verification-service/internal/kafka"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
	"github.com/exbanka/verification-service/internal/service"
)

func main() {
	cfg := config.Load()

	// 1. Connect to PostgreSQL
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// 2. Auto-migrate models
	// VerificationChallenge: skip if table already exists (gorm.io/datatypes v1.2.7 has a
	// known issue generating bad SQL when migrating existing jsonb columns).
	if !db.Migrator().HasTable(&model.VerificationChallenge{}) {
		if err := db.AutoMigrate(&model.VerificationChallenge{}); err != nil {
			log.Fatalf("failed to migrate VerificationChallenge: %v", err)
		}
	}

	// 3. Create Kafka producer
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Connect to auth-service for biometrics checks
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("failed to connect to auth service: %v", err)
	}
	defer authConn.Close()
	authClient := authpb.NewAuthServiceClient(authConn)

	// 4. Create repository
	repo := repository.NewVerificationChallengeRepository(db)

	// 5. Create service
	svc := service.NewVerificationService(repo, producer, db, authClient, cfg.ChallengeExpiry, cfg.MaxAttempts)

	// 6. Create gRPC handler
	grpcHandler := handler.NewVerificationGRPCHandler(svc)

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	shared.EnsureTopics(cfg.KafkaBrokers,
		kafkamsg.TopicVerificationChallengeCreated,
		kafkamsg.TopicVerificationChallengeVerified,
		kafkamsg.TopicVerificationChallengeFailed,
		kafkamsg.TopicSendEmail,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runExpiryLoop(ctx, svc)

	if err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				metrics.GRPCUnaryServerInterceptor(),
				grpcmw.UnaryLoggingInterceptor("verification-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterVerificationGRPCServiceServer(s, grpcHandler)
			shared.RegisterHealthCheck(s, "verification-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			log.Printf("verification-service listening on %s", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}

// runExpiryLoop runs every 60 seconds to expire old pending challenges.
// Delegates to shared.RunScheduled which honors ctx cancellation.
func runExpiryLoop(ctx context.Context, svc *service.VerificationService) {
	shared.RunScheduled(ctx, shared.ScheduledJob{
		Name:     "verification-challenge-expiry",
		Interval: 1 * time.Minute,
		OnTick: func(ctx context.Context) error {
			svc.ExpireOldChallenges(ctx)
			return nil
		},
	})
}
