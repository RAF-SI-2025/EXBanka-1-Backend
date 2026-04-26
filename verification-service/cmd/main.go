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
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
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
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	// 7. Create TCP listener
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// 8. Create gRPC server and register services
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	pb.RegisterVerificationGRPCServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "verification-service")
	metrics.InitializeGRPCMetrics(s)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// 9. Ensure Kafka topics exist (produces to)
	shared.EnsureTopics(cfg.KafkaBrokers,
		kafkamsg.TopicVerificationChallengeCreated,
		kafkamsg.TopicVerificationChallengeVerified,
		kafkamsg.TopicVerificationChallengeFailed,
		kafkamsg.TopicSendEmail,
	)

	// 10. Create cancellable context for background goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 11. Start background expiry goroutine
	go runExpiryLoop(ctx, svc)

	// 12. Start gRPC server
	markReady()
	go func() {
		log.Printf("verification-service listening on %s", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// 13. Wait for interrupt signal and shut down gracefully
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down verification-service gracefully...")
	cancel()
	s.GracefulStop()
	log.Println("Server stopped")
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
