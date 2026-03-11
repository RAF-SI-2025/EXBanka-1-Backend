package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

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
	if err := db.AutoMigrate(&model.Employee{}); err != nil {
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
	empService := service.NewEmployeeService(repo, producer, redisCache)

	if err := seedAdminUser(repo); err != nil {
		log.Printf("warn: seed admin user: %v", err)
	}

	grpcHandler := handler.NewUserGRPCHandler(empService)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterUserServiceServer(s, grpcHandler)

	fmt.Printf("user service listening on %s\n", cfg.GRPCAddr)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func seedAdminUser(repo *repository.EmployeeRepository) error {
	if _, err := repo.GetByEmail("admin@admin.admin"); err == nil {
		return nil // already exists
	}

	hash, err := service.HashPassword("AdminAdmin2026.!")
	if err != nil {
		return err
	}

	admin := &model.Employee{
		FirstName:    "Admin",
		LastName:     "Admin",
		DateOfBirth:  time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC),
		Gender:       "M",
		Email:        "admin@admin.admin",
		Phone:        "+38600000000",
		Address:      "Admin Street 1",
		Username:     "admin",
		PasswordHash: hash,
		Salt:         "",
		Position:     "Administrator",
		Department:   "IT",
		Active:       true,
		Role:         "EmployeeAdmin",
		Activated:    true,
	}

	return repo.Create(admin)
}
