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
	"github.com/exbanka/contract/shared/grpcmw"
	userpb "github.com/exbanka/contract/userpb"
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

	userConn, err := grpc.NewClient(cfg.UserGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
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

	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// Pre-create Kafka topics before starting consumers to avoid
	// partition assignment race condition on fresh startup.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"user.employee-created",
		"client.created",
		"notification.send-email",
		"notification.general",
		kafkamsg.TopicAuthAccountStatusChanged,
		kafkamsg.TopicAuthDeadLetter,
		kafkamsg.TopicAuthMobileDeviceActivated,
		kafkamsg.TopicAuthSessionCreated,
		kafkamsg.TopicAuthSessionRevoked,
		kafkamsg.TopicUserRolePermissionsChanged,
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

	if redisCache != nil {
		rolePermHandler := consumer.NewRolePermChangeHandler(redisCache, tokenRepo, cfg.AccessExpiry)
		rolePermConsumer := consumer.NewRolePermChangeConsumer(cfg.KafkaBrokers, rolePermHandler)
		rolePermConsumer.Start(ctx)
		defer rolePermConsumer.Close()
	} else {
		log.Println("WARN: redis unavailable — role-perm-change consumer NOT started; session revocation will be eventually consistent (next refresh / token expiry only)")
	}

	if err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				metrics.GRPCUnaryServerInterceptor(),
				grpcmw.UnaryLoggingInterceptor("auth-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			authpb.RegisterAuthServiceServer(s, grpcHandler)
			shared.RegisterHealthCheck(s, "auth-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			fmt.Printf("Auth service listening on %s\n", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
	cancel()
	log.Println("Server stopped")
}
