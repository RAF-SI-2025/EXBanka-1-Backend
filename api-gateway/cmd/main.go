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
	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/router"
	"github.com/exbanka/contract/metrics"
)

// @title           EXBanka API
// @version         3.0
// @description     EXBanka Banking Microservices API Gateway. All endpoints are served under /api/v3 — v1 and v2 have been retired (plan E, 2026-04-27). Future versions (v4+) will be added as separate explicit router files with no transparent fallback. See api-gateway/internal/router/router_versioning.md.
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

	// Inter-bank wiring removed — Phase 1 of the SI-TX refactor unwires
	// the wrong-assumption InterBankService routes. PeerDisabledHandler
	// (in router.NewHandlers) serves /api/v3/me/transfers and returns
	// 501 for foreign-prefix receivers until the SI-TX-conformant
	// replacement ships in Phase 2. See docs/superpowers/specs/
	// 2026-04-29-celina5-sitx-refactor-design.md.

	r := router.NewRouter()

	// Wire every gRPC client into the router-level Deps bundle, then
	// build the cross-version Handlers bundle. v3 is the only live
	// version — see api-gateway/internal/router/router_versioning.md
	// for the v4-add-later pattern.
	deps := router.Deps{
		AuthClient:          authClient,
		UserClient:          userClient,
		ClientClient:        clientClient,
		AccountClient:       accountClient,
		CardClient:          cardClient,
		TxClient:            txClient,
		CreditClient:        creditClient,
		EmpLimitClient:      empLimitClient,
		ClientLimitClient:   clientLimitClient,
		VirtualCardClient:   virtualCardClient,
		BankAccountClient:   bankAccountClient,
		FeeClient:           feeClient,
		CardRequestClient:   cardRequestClient,
		ExchangeClient:      exchangeClient,
		StockExchangeClient: stockExchangeClient,
		SecurityClient:      securityClient,
		OrderClient:         orderClient,
		PortfolioClient:     portfolioClient,
		OTCClient:           otcClient,
		TaxClient:           taxClient,
		ActuaryClient:       actuaryClient,
		BlueprintClient:     blueprintClient,
		VerificationClient:  verificationClient,
		NotificationClient:  notificationClient,
		SourceAdminClient:   sourceAdminClient,
		FundClient:          fundClient,
		OTCOptionsClient:    otcOptionsClient,
		OwnBankCode:         cfg.OwnBankCode,
	}
	h := router.NewHandlers(deps)
	router.SetupV3(r, h)
	// When v4 ships:
	//   router.SetupV4(r, h)

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
