package main

import (
	"context"
	"fmt"
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

	// Connect to auth-service for direct account provisioning during seeding.
	// grpc.NewClient is non-blocking; actual connection is made lazily.
	authConn, err := grpc.NewClient(cfg.AuthGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("warn: failed to create auth gRPC client: %v", err)
	}
	var authClient authpb.AuthServiceClient
	if authConn != nil {
		defer authConn.Close()
		authClient = authpb.NewAuthServiceClient(authConn)
	}

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

	// Seed admin in background so user-service is not blocked waiting for
	// auth-service to become ready (auth-service starts after user-service).
	go func() {
		if err := seedAdminUser(empService, roleSvc, authClient); err != nil {
			log.Printf("warn: seed admin user: %v", err)
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

// getEnv returns the value of an environment variable or a fallback default.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func seedAdminUser(empSvc *service.EmployeeService, roleSvc *service.RoleService, authClient authpb.AuthServiceClient) error {
	adminEmail := getEnv("ADMIN_EMAIL", "admin@exbanka.com")

	existing, _ := empSvc.GetEmployeeByEmail(adminEmail)
	if existing != nil {
		log.Println("Admin user already exists, skipping seed")
		// Ensure EmployeeAdmin role is assigned even if a previous run missed it.
		if err := empSvc.SetEmployeeRoles(context.Background(), existing.ID, []string{"EmployeeAdmin"}); err != nil {
			log.Printf("warn: assign EmployeeAdmin role to existing admin: %v", err)
		}
		if authClient != nil {
			ensureAuthAccount(authClient, existing.ID, existing.Email, existing.FirstName, "employee")
		}
		return nil
	}

	admin := &model.Employee{
		FirstName:   "System",
		LastName:    "Admin",
		DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:      "other",
		Email:       adminEmail,
		Phone:       "+381000000000",
		Address:     "System Account",
		JMBG:        "0101990000000",
		Username:    "admin",
		Position:    "System Administrator",
		Department:  "IT",
	}
	if err := empSvc.CreateEmployee(context.Background(), admin); err != nil {
		return err
	}

	if err := empSvc.SetEmployeeRoles(context.Background(), admin.ID, []string{"EmployeeAdmin"}); err != nil {
		log.Printf("warn: assign EmployeeAdmin role to admin: %v", err)
	}

	if authClient != nil {
		ensureAuthAccount(authClient, admin.ID, admin.Email, admin.FirstName, "employee")
	}
	return nil
}

// ensureAuthAccount calls auth-service's CreateAccount RPC with retries,
// waiting up to ~5 minutes for auth-service to become available.
func ensureAuthAccount(authClient authpb.AuthServiceClient, principalID int64, email, firstName, principalType string) {
	const maxAttempts = 60
	const retryDelay = 5 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := authClient.CreateAccount(ctx, &authpb.CreateAccountRequest{
			PrincipalId:   principalID,
			Email:         email,
			FirstName:     firstName,
			PrincipalType: principalType,
		})
		cancel()
		if err == nil {
			log.Printf("auth account provisioned for %s", email)
			return
		}
		log.Printf("ensureAuthAccount attempt %d/%d for %s: %v", attempt, maxAttempts, email, err)
		if attempt < maxAttempts {
			time.Sleep(retryDelay)
		}
	}
	log.Printf("warn: could not provision auth account for %s after %d attempts", email, maxAttempts)
}
