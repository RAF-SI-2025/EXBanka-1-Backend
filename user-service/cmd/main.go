package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/userpb"
	shared "github.com/exbanka/contract/shared"
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
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

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

	repo := repository.NewEmployeeRepository(db)
	permRepo := repository.NewPermissionRepository(db)
	roleRepo := repository.NewRoleRepository(db)

	roleSvc := service.NewRoleService(roleRepo, permRepo)

	// Seed roles and permissions on startup
	if err := roleSvc.SeedRolesAndPermissions(); err != nil {
		log.Printf("warn: failed to seed roles and permissions: %v", err)
	}

	empService := service.NewEmployeeService(repo, producer, redisCache, roleSvc)

	if err := seedAdminUser(repo, roleSvc); err != nil {
		log.Printf("warn: seed admin user: %v", err)
	}

	grpcHandler := handler.NewUserGRPCHandler(empService, roleSvc)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, grpcHandler)
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

func seedAdminUser(repo *repository.EmployeeRepository, roleSvc *service.RoleService) error {
	existing, _ := repo.GetByEmail("admin@exbanka.com")
	if existing != nil {
		log.Println("Admin user already exists, skipping seed")
		return nil
	}

	hash, err := service.HashPassword("AdminAdmin2026.!")
	if err != nil {
		return fmt.Errorf("failed to hash admin password: %w", err)
	}

	admin := &model.Employee{
		FirstName:    "System",
		LastName:     "Admin",
		DateOfBirth:  time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:       "other",
		Email:        "admin@exbanka.com",
		Phone:        "+381000000000",
		Address:      "System Account",
		JMBG:         "0101990000000",
		Username:     "admin",
		PasswordHash: hash,
		Salt:         "system-seed",
		Position:     "System Administrator",
		Department:   "IT",
		Active:       true,
		Role:         "EmployeeAdmin",
		Activated:    true,
	}
	if err := repo.Create(admin); err != nil {
		return err
	}

	// Associate admin role using the many2many relationship
	if roleSvc != nil {
		adminRole, err := roleSvc.GetRole(0) // won't work with 0 ID
		_ = adminRole
		_ = err
		// Look up by name
		role, err2 := roleSvc.ListRoles()
		if err2 == nil {
			for _, r := range role {
				if r.Name == "EmployeeAdmin" {
					_ = repo.SetEmployeeRoles(admin.ID, []model.Role{r})
					break
				}
			}
		}
	}

	return nil
}
