package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/exbanka/auth-service/internal/cache"
	"github.com/exbanka/auth-service/internal/config"
	"github.com/exbanka/auth-service/internal/consumer"
	"github.com/exbanka/auth-service/internal/handler"
	kafkaprod "github.com/exbanka/auth-service/internal/kafka"
	"github.com/exbanka/auth-service/internal/model"
	"github.com/exbanka/auth-service/internal/repository"
	"github.com/exbanka/auth-service/internal/service"
	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	userpb "github.com/exbanka/contract/userpb"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Account{},
		&model.RefreshToken{},
		&model.ActivationToken{},
		&model.PasswordResetToken{},
		&model.LoginAttempt{},
		&model.AccountLock{},
		&model.TOTPSecret{},
		&model.ActiveSession{},
		&model.MobileDevice{},
		&model.MobileActivationCode{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	userConn, err := grpc.NewClient(cfg.UserGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := userpb.NewUserServiceClient(userConn)

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	tokenRepo := repository.NewTokenRepository(db)
	sessionRepo := repository.NewSessionRepository(db)
	loginAttemptRepo := repository.NewLoginAttemptRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	totpRepo := repository.NewTOTPRepository(db)
	jwtService := service.NewJWTService(cfg.JWTSecret, cfg.AccessExpiry)
	totpSvc := service.NewTOTPService()
	authService := service.NewAuthService(tokenRepo, sessionRepo, loginAttemptRepo, totpRepo, totpSvc, jwtService, accountRepo, userClient, producer, redisCache, cfg.RefreshExpiry, cfg.MobileRefreshExpiry, cfg.FrontendBaseURL, cfg.PasswordPepper)

	mobileDeviceRepo := repository.NewMobileDeviceRepository(db)
	mobileActivationRepo := repository.NewMobileActivationRepository(db)
	mobileSvc := service.NewMobileDeviceService(
		mobileDeviceRepo, mobileActivationRepo, accountRepo, tokenRepo,
		jwtService, producer, cfg.MobileRefreshExpiry, cfg.MobileActivationExpiry, cfg.FrontendBaseURL,
	)

	grpcHandler := handler.NewAuthGRPCHandler(authService, mobileSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	authpb.RegisterAuthServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "auth-service")
	metrics.InitializeGRPCMetrics(s)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// Pre-create Kafka topics before starting consumers to avoid
	// partition assignment race condition on fresh startup.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"user.employee-created",
		"client.created",
		"notification.send-email",
		kafkamsg.TopicAuthAccountStatusChanged,
		kafkamsg.TopicAuthDeadLetter,
		kafkamsg.TopicAuthMobileDeviceActivated,
		kafkamsg.TopicAuthSessionCreated,
		kafkamsg.TopicAuthSessionRevoked,
	)

	// Start Kafka consumer for employee-created events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	employeeConsumer := consumer.NewEmployeeConsumer(cfg.KafkaBrokers, authService)
	employeeConsumer.Start(ctx)
	defer employeeConsumer.Close()

	clientConsumer := consumer.NewClientConsumer(cfg.KafkaBrokers, authService)
	clientConsumer.Start(ctx)
	defer clientConsumer.Close()

	// Start gRPC server in goroutine
	markReady()
	go func() {
		fmt.Printf("Auth service listening on %s\n", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	cancel()
	s.GracefulStop()
	log.Println("Server stopped")
}
