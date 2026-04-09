package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/exbanka/contract/metrics"
	notifpb "github.com/exbanka/contract/notificationpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/notification-service/internal/config"
	"github.com/exbanka/notification-service/internal/consumer"
	"github.com/exbanka/notification-service/internal/handler"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	"github.com/exbanka/notification-service/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg := config.Load()

	// Database
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.MobileInboxItem{}, &model.GeneralNotification{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	// Repositories
	inboxRepo := repository.NewMobileInboxRepository(db)
	notifRepo := repository.NewGeneralNotificationRepository(db)

	// Email sender
	emailSender := sender.NewEmailSender(
		cfg.SMTPHost, cfg.SMTPPort,
		cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPFrom,
	)

	// Kafka producer (delivery confirmations)
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Pre-create Kafka topics before starting consumers to avoid
	// partition assignment race condition on fresh startup.
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		"notification.send-email",
		"notification.email-sent",
		"verification.challenge-created",
		"notification.mobile-push",
		"notification.general",
	)

	// Kafka consumer (email events)
	emailConsumer := consumer.NewEmailConsumer(cfg.KafkaBrokers, emailSender, producer)
	defer emailConsumer.Close()

	// Start consumers in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go emailConsumer.Start(ctx)

	// Verification consumer (challenge events → email or mobile inbox)
	verificationConsumer := consumer.NewVerificationConsumer(cfg.KafkaBrokers, emailSender, producer, inboxRepo)
	verificationConsumer.Start(ctx)
	defer verificationConsumer.Close()

	// General notification consumer (persistent user notifications)
	generalConsumer := consumer.NewGeneralNotificationConsumer(cfg.KafkaBrokers, notifRepo)
	generalConsumer.Start(ctx)
	defer generalConsumer.Close()

	// Background inbox cleanup
	cleanupSvc := service.NewInboxCleanupService(inboxRepo)
	cleanupSvc.StartCleanupCron(ctx)

	// gRPC server
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.GRPCUnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
	)
	notifpb.RegisterNotificationServiceServer(grpcServer, handler.NewGRPCHandler(emailSender, inboxRepo, notifRepo))
	shared.RegisterHealthCheck(grpcServer, "notification-service")
	reflection.Register(grpcServer)
	metrics.InitializeGRPCMetrics(grpcServer)
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	sqlDB, _ := db.DB()
	addReadinessCheck(func(ctx context.Context) error {
		return sqlDB.PingContext(ctx)
	})

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down notification-service...")
		cancel()
		grpcServer.GracefulStop()
	}()

	markReady()
	fmt.Printf("Notification service listening on %s\n", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
