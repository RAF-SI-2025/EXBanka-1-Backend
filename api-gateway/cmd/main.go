package main

import (
	"fmt"
	"log"

	"github.com/exbanka/api-gateway/internal/config"
	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/router"
)

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

	r := router.Setup(authClient, userClient)

	fmt.Printf("API Gateway listening on %s\n", cfg.HTTPAddr)
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
