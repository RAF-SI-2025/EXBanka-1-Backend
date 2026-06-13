package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	adminpb "github.com/exbanka/contract/adminpb"
	"github.com/exbanka/contract/cronreg"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/contract/logger"
	"github.com/exbanka/contract/metrics"
	notifpb "github.com/exbanka/contract/notificationpb"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
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
	logger.Init("notification-service")
	cfg := config.Load()

	// Database
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.MobileInboxItem{}, &model.GeneralNotification{}, &model.NotificationTemplate{}, &cronreg.CronPauseState{}, &model.AdminAuditLog{}, &model.BusinessAuditLog{}, &model.ProcessedMessage{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	// Partial unique index for watchlist-alert (and any future) idempotency keys.
	// PostgreSQL partial unique indexes are not expressible via GORM struct tags,
	// so we create them explicitly. Safe: CREATE UNIQUE INDEX IF NOT EXISTS is a
	// no-op when the index already exists.
	if err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_general_notif_idem_key
		 ON general_notifications (idempotency_key)
		 WHERE idempotency_key <> ''`,
	).Error; err != nil {
		log.Printf("WARN: create idempotency_key partial index: %v", err)
	}
	cronRegistry := cronreg.NewRegistry("notification-service", cronreg.NewGormPauseStore(db))

	// Repositories
	inboxRepo := repository.NewMobileInboxRepository(db)
	notifRepo := repository.NewGeneralNotificationRepository(db)
	templateRepo := repository.NewTemplateRepository(db)
	adminAuditRepo := repository.NewAdminAuditLogRepository(db)
	businessAuditRepo := repository.NewBusinessAuditLogRepository(db)
	// Consumer-side idempotency: dedups Kafka at-least-once redeliveries by the
	// per-message idempotency-key header. Passed to every consumer.
	dedup := repository.NewProcessedMessageRepository(db)

	// Template service (registry-backed render + admin CRUD)
	templateSvc := service.NewTemplateService(templateRepo)

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
	shared.EnsureTopics(cfg.KafkaBrokers,
		"notification.send-email",
		"notification.email-sent",
		"verification.challenge-created",
		"notification.general",
		"notification.watchlist-alert",
		"admin.cron-action",
		"admin.business-action",
		kafkamsg.TopicNotificationDeadLetter,
	)

	// All consumers use manual-commit + bounded retry + dead-letter (the shared
	// producer is the DeadLetterWriter). This replaces the prior auto-commit
	// loop that silently dropped events on a transient DB/SMTP failure.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Kafka consumer (email events)
	emailConsumer := consumer.NewEmailConsumer(cfg.KafkaBrokers, emailSender, producer, templateSvc, producer, dedup)
	defer emailConsumer.Close()
	emailConsumer.Start(ctx)

	// Verification consumer (challenge events → email or mobile inbox)
	verificationConsumer := consumer.NewVerificationConsumer(cfg.KafkaBrokers, emailSender, producer, inboxRepo, templateSvc, producer, dedup)
	verificationConsumer.Start(ctx)
	defer verificationConsumer.Close()

	// General notification consumer (persistent user notifications)
	generalConsumer := consumer.NewGeneralNotificationConsumer(cfg.KafkaBrokers, notifRepo, templateSvc, producer, dedup)
	generalConsumer.Start(ctx)
	defer generalConsumer.Close()

	// Watchlist alert consumer (persists daily price-move alerts to general_notifications)
	watchlistAlertConsumer := consumer.NewWatchlistAlertConsumer(cfg.KafkaBrokers, notifRepo, templateSvc, producer, dedup)
	watchlistAlertConsumer.Start(ctx)
	defer func() { _ = watchlistAlertConsumer.Close() }()

	// Admin cron audit consumer (persists admin.cron-action events to admin_audit_logs)
	adminAuditConsumer := consumer.NewAdminAuditConsumer(cfg.KafkaBrokers, db, producer, dedup)
	adminAuditConsumer.Start(ctx)
	defer adminAuditConsumer.Close()

	// Business audit consumer (persists admin.business-action events to business_audit_logs)
	businessAuditConsumer := consumer.NewBusinessAuditConsumer(cfg.KafkaBrokers, db, producer, dedup)
	businessAuditConsumer.Start(ctx)
	defer businessAuditConsumer.Close()

	// Background inbox cleanup
	cleanupSvc := service.NewInboxCleanupService(inboxRepo, cronRegistry)
	cleanupSvc.StartCleanupCron(ctx)

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
				grpcmw.UnaryLoggingInterceptor("notification-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			notifpb.RegisterNotificationServiceServer(s, handler.NewGRPCHandler(emailSender, inboxRepo, notifRepo, templateSvc, adminAuditRepo, businessAuditRepo))
			adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
			shared.RegisterHealthCheck(s, "notification-service")
			reflection.Register(s)
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			slog.Info("Notification service listening", "addr", cfg.GRPCAddr)
		},
	}); err != nil {
		log.Fatalf("grpc: %v", err)
	}
	cancel()
}
