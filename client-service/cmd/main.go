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

	"github.com/exbanka/client-service/internal/cache"
	"github.com/exbanka/client-service/internal/config"
	"github.com/exbanka/client-service/internal/consumer"
	"github.com/exbanka/client-service/internal/handler"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/repository"
	"github.com/exbanka/client-service/internal/service"
	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/logger"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	userpb "github.com/exbanka/contract/userpb"
)

func main() {
	logger.Init("client-service")
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
		// Translate driver-specific errors (e.g. Postgres unique-violation) into
		// portable gorm sentinels (gorm.ErrDuplicatedKey) so the service layer can
		// map a duplicate email/JMBG to AlreadyExists (409) instead of leaking the
		// raw constraint string — which contains the colliding email/JMBG (PII).
		TranslateError: true,
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}, &model.ClientLimit{}, &model.Changelog{}, &model.EmployeeLimitReplica{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	shared.EnsureTopics(cfg.KafkaBrokers,
		"client.created",
		"client.updated",
		"client.limits-updated",
		"client.changelog",
		"notification.send-email",
		"notification.general",
		"user.employee-limits-updated",
	)

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	// Connect to user-service for employee limit enforcement
	var userLimitClient userpb.EmployeeLimitServiceClient
	userConn, userErr := grpc.NewClient(cfg.UserGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if userErr != nil {
		log.Printf("warn: failed to connect to user service for limit enforcement: %v", userErr)
	} else {
		defer userConn.Close()
		userLimitClient = userpb.NewEmployeeLimitServiceClient(userConn)
	}

	repo := repository.NewClientRepository(db)
	clientLimitRepo := repository.NewClientLimitRepository(db)
	changelogRepo := repository.NewChangelogRepository(db)
	employeeLimitReplicaRepo := repository.NewEmployeeLimitReplicaRepository(db)

	clientService := service.NewClientService(repo, producer, redisCache, changelogRepo)
	clientLimitSvc := service.NewClientLimitService(clientLimitRepo, userLimitClient, producer, employeeLimitReplicaRepo, changelogRepo).
		WithEmailLookup(clientEmailLookup{repo: repo}) // SP5 D1: limit-change email
	changelogSvc := service.NewChangelogService(changelogRepo)

	// Start employee-limit replica consumer (SP-2b): maintains a local snapshot
	// of employee limits from user.employee-limits-updated events to avoid a
	// synchronous gRPC call on every SetClientLimits.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limitReplicaConsumer := consumer.NewEmployeeLimitReplicaConsumer(cfg.KafkaBrokers, employeeLimitReplicaRepo)
	limitReplicaConsumer.Start(ctx)
	defer limitReplicaConsumer.Close()

	grpcHandler := handler.NewClientGRPCHandler(clientService, changelogSvc)
	limitHandler := handler.NewClientLimitGRPCHandler(clientLimitSvc)

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
				grpcmw.UnaryLoggingInterceptor("client-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			clientpb.RegisterClientServiceServer(s, grpcHandler)
			clientpb.RegisterClientLimitServiceServer(s, limitHandler)
			shared.RegisterHealthCheck(s, "client-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			slog.Info("client service listening", "addr", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}

// clientEmailLookup adapts the client repository to the limit service's
// ClientEmailLookup interface (SP5 D1).
type clientEmailLookup struct {
	repo *repository.ClientRepository
}

func (l clientEmailLookup) GetEmailByID(clientID int64) (string, error) {
	c, err := l.repo.GetByID(uint64(clientID))
	if err != nil {
		return "", err
	}
	return c.Email, nil
}
