package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/config"
	"github.com/exbanka/notification-service/internal/consumer"
	"github.com/exbanka/notification-service/internal/handler"
	kafkaprod "github.com/exbanka/notification-service/internal/kafka"
	"github.com/exbanka/notification-service/internal/sender"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()

	// Email sender
	emailSender := sender.NewEmailSender(
		cfg.SMTPHost, cfg.SMTPPort,
		cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPFrom,
	)

	// Kafka producer (delivery confirmations)
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// Kafka consumer (email events)
	emailConsumer := consumer.NewEmailConsumer(cfg.KafkaBrokers, emailSender, producer)
	defer emailConsumer.Close()

	// Start consumer in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go emailConsumer.Start(ctx)

	// gRPC server
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	notifpb.RegisterNotificationServiceServer(grpcServer, handler.NewGRPCHandler(emailSender))
	reflection.Register(grpcServer)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down notification-service...")
		cancel()
		grpcServer.GracefulStop()
	}()

	fmt.Printf("Notification service listening on %s\n", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
