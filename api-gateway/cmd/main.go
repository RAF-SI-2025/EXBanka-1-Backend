package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/exbanka/api-gateway/docs"
	"github.com/exbanka/api-gateway/internal/config"
	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/router"
)

// @title           EXBanka API
// @version         1.0
// @description     EXBanka Banking Microservices API Gateway
// @host            localhost:8080
// @BasePath        /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Enter "Bearer <token>"
func main() {
	cfg := config.Load()

	authClient, authConn, err := grpcclients.NewAuthClient(cfg.AuthGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to auth service: %v", err)
	}
	defer authConn.Close()

	userClient, userConn, err := grpcclients.NewUserClient(cfg.UserGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to user service: %v", err)
	}
	defer userConn.Close()

	clientClient, clientConn, err := grpcclients.NewClientClient(cfg.ClientGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to client service: %v", err)
	}
	defer clientConn.Close()

	accountClient, accountConn, err := grpcclients.NewAccountClient(cfg.AccountGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to account service: %v", err)
	}
	defer accountConn.Close()

	cardClient, cardConn, err := grpcclients.NewCardClient(cfg.CardGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to card service: %v", err)
	}
	defer cardConn.Close()

	txClient, txConn, err := grpcclients.NewTransactionClient(cfg.TransactionGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to transaction service: %v", err)
	}
	defer txConn.Close()

	feeClient, feeConn, err := grpcclients.NewFeeServiceClient(cfg.TransactionGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to fee service: %v", err)
	}
	defer feeConn.Close()

	creditClient, creditConn, err := grpcclients.NewCreditClient(cfg.CreditGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to credit service: %v", err)
	}
	defer creditConn.Close()

	// Employee limit service reuses the user-service connection
	empLimitClient, empLimitConn, err := grpcclients.NewEmployeeLimitClient(cfg.UserGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to employee limit service: %v", err)
	}
	defer empLimitConn.Close()

	// Client limit service reuses the client-service connection
	clientLimitClient, clientLimitConn, err := grpcclients.NewClientLimitClient(cfg.ClientGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to client limit service: %v", err)
	}
	defer clientLimitConn.Close()

	// Virtual card service reuses the card-service connection
	virtualCardClient, virtualCardConn, err := grpcclients.NewVirtualCardClient(cfg.CardGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to virtual card service: %v", err)
	}
	defer virtualCardConn.Close()

	// Bank account service reuses the account-service connection
	bankAccountClient, bankAccountConn, err := grpcclients.NewBankAccountClient(cfg.AccountGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to bank account service: %v", err)
	}
	defer bankAccountConn.Close()

	// Card request service reuses the card-service connection
	cardRequestClient, cardRequestConn, err := grpcclients.NewCardRequestClient(cfg.CardGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to card request service: %v", err)
	}
	defer cardRequestConn.Close()

	exchangeClient, exchangeConn, err := grpcclients.NewExchangeClient(cfg.ExchangeGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to exchange service: %v", err)
	}
	defer exchangeConn.Close()

	r := router.Setup(authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient, empLimitClient, clientLimitClient, virtualCardClient, bankAccountClient, feeClient, cardRequestClient, exchangeClient, cfg.BootstrapSecret)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: r,
	}

	// Start HTTP server in goroutine
	go func() {
		fmt.Printf("API Gateway listening on %s\n", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down API Gateway gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}
	log.Println("Server stopped")
}
