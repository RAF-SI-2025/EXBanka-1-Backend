package main

import (
	"context"
	"log"
	"log/slog"
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
	"github.com/exbanka/contract/logger"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	userpb "github.com/exbanka/contract/userpb"
)

func main() {
	logger.Init("auth-service")
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
	// Access tokens are signed with ES256 (asymmetric): the private key lives
	// here; the gateway fetches the public half via GetSigningKeys and verifies
	// locally. A configured PEM key persists across restarts; otherwise a fresh
	// ephemeral key is generated (dev only — restart invalidates live tokens).
	var signingKey *service.SigningKey
	if cfg.JWTECPrivateKey != "" {
		k, kerr := service.LoadSigningKeyFromPEM(cfg.JWTECKid, cfg.JWTECPrivateKey)
		if kerr != nil {
			log.Fatalf("failed to load JWT ES256 signing key: %v", kerr)
		}
		signingKey = k
	} else {
		k, kerr := service.GenerateSigningKey()
		if kerr != nil {
			log.Fatalf("failed to generate JWT ES256 signing key: %v", kerr)
		}
		log.Printf("warn: JWT_EC_PRIVATE_KEY unset — generated ephemeral ES256 key kid=%s (tokens will not survive a restart)", k.Kid)
		signingKey = k
	}
	keyManager := service.NewKeyManager(signingKey)
	jwtService := service.NewJWTService(keyManager, cfg.AccessExpiry)
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
			slog.Info("Auth service listening", "addr", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
	cancel()
	log.Println("Server stopped")
}
