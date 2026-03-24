package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/cache"
	"github.com/exbanka/user-service/internal/config"
	"github.com/exbanka/user-service/internal/handler"
	kafkaprod "github.com/exbanka/user-service/internal/kafka"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
	"github.com/exbanka/user-service/internal/service"
)

func main() {
	cfg := config.Load()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Permission{},
		&model.Role{},
		&model.Employee{},
		&model.EmployeeLimit{},
		&model.LimitTemplate{},
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

	empService := service.NewEmployeeService(repo, producer, redisCache, roleSvc)
	limitSvc := service.NewLimitService(employeeLimitRepo, limitTemplateRepo, producer)

	if err := limitSvc.SeedDefaultTemplates(); err != nil {
		log.Fatalf("failed to seed default limit templates: %v", err)
	}

	limitCron := service.NewLimitCronService(employeeLimitRepo)
	limitCron.Start()

	grpcHandler := handler.NewUserGRPCHandler(empService, roleSvc)
	limitHandler := handler.NewLimitGRPCHandler(limitSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, grpcHandler)
	pb.RegisterEmployeeLimitServiceServer(s, limitHandler)
	shared.RegisterHealthCheck(s, "user-service")

	// Start gRPC server in goroutine
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
