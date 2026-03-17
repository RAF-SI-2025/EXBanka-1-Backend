package main

import (
	"fmt"
	"log"

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

	creditClient, creditConn, err := grpcclients.NewCreditClient(cfg.CreditGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to credit service: %v", err)
	}
	defer creditConn.Close()

	r := router.Setup(authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient)

	fmt.Printf("API Gateway listening on %s\n", cfg.HTTPAddr)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
