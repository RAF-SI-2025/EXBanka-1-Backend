package main

import (
	"time"
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

	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/cache"
	"github.com/exbanka/user-service/internal/config"
	grpc_client "github.com/exbanka/user-service/internal/grpc_client"
	"github.com/exbanka/user-service/internal/handler"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
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
		&model.Permission{},
		&model.Role{},
		&model.Employee{},
		&model.EmployeeLimit{},
		&model.LimitTemplate{},
		&model.ActuaryLimit{},
		&model.LimitBlueprint{},
		&model.Changelog{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before any publishing to avoid
	// partition assignment race condition for downstream consumers.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"user.employee-created",
		"user.employee-updated",
		"user.employee-limits-updated",
		"user.limit-template-created",
		"user.limit-template-updated",
		"user.limit-template-deleted",
		"user.actuary-limit-updated",
		"user.blueprint-created",
		"user.blueprint-updated",
		"user.blueprint-deleted",
		"user.blueprint-applied",
		"user.changelog",
		"notification.send-email",
	)

	var redisCache *cache.RedisCache
	redisCache, err = cache.NewRedisCache(cfg.RedisAddr)
	if err != nil {
		log.Printf("warn: redis unavailable, running without cache: %v", err)
	}
	if redisCache != nil {
		defer redisCache.Close()
	}

	repo := repository.NewEmployeeRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	roleRepo := repository.NewRoleRepository(db)
	employeeLimitRepo := repository.NewEmployeeLimitRepository(db)
	limitTemplateRepo := repository.NewLimitTemplateRepository(db)

	roleSvc := service.NewRoleService(roleRepo, permRepo)

	// Seed roles and permissions on startup
	if err := roleSvc.SeedRolesAndPermissions(); err != nil {
		log.Fatalf("failed to seed roles and permissions: %v", err)
	}

	changelogRepo := repository.NewChangelogRepository(db)
	empService := service.NewEmployeeService(repo, producer, redisCache, roleSvc, changelogRepo)
	limitSvc := service.NewLimitService(employeeLimitRepo, limitTemplateRepo, repo, producer, changelogRepo)

	if err := limitSvc.SeedDefaultTemplates(); err != nil {
		log.Fatalf("failed to seed default limit templates: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limitCron := service.NewLimitCronService(employeeLimitRepo)
	limitCron.Start(ctx)

	actuaryRepo := repository.NewActuaryRepository(db)
	actuarySvc := service.NewActuaryService(actuaryRepo, repo, producer)
	actuaryHandler := handler.NewActuaryGRPCHandler(actuarySvc)

	actuaryCron := service.NewActuaryCronService(actuaryRepo)
	actuaryCron.Start(ctx)

	// Blueprint service
	blueprintRepo := repository.NewLimitBlueprintRepository(db)

	// Connect to client-service for client blueprint apply
	clientConn, err := grpc.NewClient(cfg.ClientGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("warn: failed to connect to client service: %v (client blueprints will not work)", err)
	}
	if clientConn != nil {
		defer clientConn.Close()
	}
	var clientLimitClient service.ClientLimitClient
	if clientConn != nil {
		clientLimitClient = grpc_client.NewClientLimitAdapter(
			clientpb.NewClientLimitServiceClient(clientConn),
		)
	}

	blueprintSvc := service.NewBlueprintService(blueprintRepo, employeeLimitRepo, actuaryRepo, clientLimitClient, producer, changelogRepo)
	blueprintHandler := handler.NewBlueprintGRPCHandler(blueprintSvc)

	// Seed blueprints from existing templates
	templates, err := limitTemplateRepo.List()
	if err != nil {
		log.Printf("warn: failed to list templates for blueprint seeding: %v", err)
	} else {
		if err := blueprintSvc.SeedFromTemplates(templates); err != nil {
			log.Printf("warn: failed to seed blueprints from templates: %v", err)
		}
	}

	grpcHandler := handler.NewUserGRPCHandler(empService, roleSvc)
	limitHandler := handler.NewLimitGRPCHandler(limitSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	pb.RegisterUserServiceServer(s, grpcHandler)
	pb.RegisterEmployeeLimitServiceServer(s, limitHandler)
	pb.RegisterActuaryServiceServer(s, actuaryHandler)
	pb.RegisterBlueprintServiceServer(s, blueprintHandler)
	shared.RegisterHealthCheck(s, "user-service")
	metrics.InitializeGRPCMetrics(s)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// Start gRPC server in goroutine
	markReady()
	go func() {
		fmt.Printf("user service listening on %s\n", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	s.GracefulStop()
	log.Println("Server stopped")
}
