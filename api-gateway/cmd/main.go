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
	"github.com/exbanka/api-gateway/internal/cache"
	"github.com/exbanka/api-gateway/internal/config"
	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
	"github.com/exbanka/api-gateway/internal/router"
	"github.com/exbanka/contract/metrics"
	"github.com/redis/go-redis/v9"
)

// @title           EXBanka API
// @version         2.0
// @description     EXBanka Banking Microservices API Gateway. All endpoints below are served under /api/v2. A v1 surface remains available at /api/v1 with identical semantics.
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

	// Stock-service gRPC clients (all share the same address)
	stockExchangeClient, stockExchangeConn, err := grpcclients.NewStockExchangeClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to stock exchange service: %v", err)
	}
	defer stockExchangeConn.Close()

	securityClient, securityConn, err := grpcclients.NewSecurityClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to security service: %v", err)
	}
	defer securityConn.Close()

	orderClient, orderConn, err := grpcclients.NewOrderClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to order service: %v", err)
	}
	defer orderConn.Close()

	portfolioClient, portfolioConn, err := grpcclients.NewPortfolioClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to portfolio service: %v", err)
	}
	defer portfolioConn.Close()

	otcClient, otcConn, err := grpcclients.NewOTCClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to OTC service: %v", err)
	}
	defer otcConn.Close()

	taxClient, taxConn, err := grpcclients.NewTaxClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to tax service: %v", err)
	}
	defer taxConn.Close()

	sourceAdminClient, sourceAdminConn, err := grpcclients.NewSourceAdminClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to source admin service: %v", err)
	}
	defer sourceAdminConn.Close()

	fundClient, fundConn, err := grpcclients.NewInvestmentFundClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to investment fund service: %v", err)
	}
	defer fundConn.Close()

	otcOptionsClient, otcOptionsConn, err := grpcclients.NewOTCOptionsClient(cfg.StockGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to OTC options service: %v", err)
	}
	defer otcOptionsConn.Close()

	// Blueprint service reuses the user-service connection
	blueprintClient, blueprintConn, err := grpcclients.NewBlueprintClient(cfg.UserGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to blueprint service: %v", err)
	}
	defer blueprintConn.Close()

	// Actuary service reuses user-service connection
	actuaryClient, actuaryConn, err := grpcclients.NewActuaryClient(cfg.UserGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to actuary service: %v", err)
	}
	defer actuaryConn.Close()

	verificationClient, verificationConn, err := grpcclients.NewVerificationClient(cfg.VerificationGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to verification service: %v", err)
	}
	defer verificationConn.Close()

	notificationClient, notificationConn, err := grpcclients.NewNotificationClient(cfg.NotificationGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to notification service: %v", err)
	}
	defer notificationConn.Close()

	wsHandler := handler.NewWebSocketHandler(authClient)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wsHandler.StartKafkaConsumer(ctx, cfg.KafkaBrokers)

	markReady, _, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()

	// Inter-bank wiring (Spec 3): InterBankService gRPC client, Redis nonce
	// store, peer-key resolver, internal HMAC-authenticated routes.
	interBankClient, interBankConn, err := grpcclients.NewInterBankServiceClient(cfg.TransactionGRPCAddr)
	if err != nil {
		log.Fatalf("failed to connect to inter-bank service: %v", err)
	}
	defer interBankConn.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Printf("warn: redis ping failed (%s) — inter-bank nonce store unavailable: %v", cfg.RedisAddr, err)
	}
	nonceStore := cache.NewNonceStore(redisClient, 10*time.Minute)
	peerKeys := map[string]string{
		"222": cfg.Peer222InboundKey,
		"333": cfg.Peer333InboundKey,
		"444": cfg.Peer444InboundKey,
	}
	peerKeyResolver := func(code string) (string, bool) {
		k, ok := peerKeys[code]
		if !ok || k == "" {
			return "", false
		}
		return k, true
	}
	interBankInternalHandler := handler.NewInterBankInternalHandler(interBankClient)

	r := router.NewRouter()
	router.SetupV1Routes(r, authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient, empLimitClient, clientLimitClient, virtualCardClient, bankAccountClient, feeClient, cardRequestClient, exchangeClient, stockExchangeClient, securityClient, orderClient, portfolioClient, otcClient, taxClient, actuaryClient, blueprintClient, verificationClient, notificationClient, sourceAdminClient)
	router.SetupV2Routes(r, authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient, empLimitClient, clientLimitClient, virtualCardClient, bankAccountClient, feeClient, cardRequestClient, exchangeClient, stockExchangeClient, securityClient, orderClient, portfolioClient, otcClient, taxClient, actuaryClient, blueprintClient, verificationClient, notificationClient, sourceAdminClient)
	router.SetupV3Routes(r, authClient, fundClient, interBankClient, accountClient, txClient, otcOptionsClient)

	// Internal HMAC-authenticated inter-bank routes (Spec 3 §7.2). NOT under
	// any /api/v* prefix — peer banks talk straight to /internal/inter-bank/*.
	internal := r.Group("/internal/inter-bank")
	internal.Use(middleware.HMACMiddleware(peerKeyResolver, nonceStore, 5*time.Minute))
	{
		internal.POST("/transfer/prepare", interBankInternalHandler.Prepare)
		internal.POST("/transfer/commit", interBankInternalHandler.Commit)
		internal.POST("/check-status", interBankInternalHandler.CheckStatus)
	}

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: r,
	}

	// Start HTTP server in goroutine
	markReady()
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
	cancel() // stop WebSocket Kafka consumer
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}
	log.Println("Server stopped")
}
