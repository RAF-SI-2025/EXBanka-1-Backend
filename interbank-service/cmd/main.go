// Command interbank-service is the standalone cross-bank SI-TX settlement engine:
// the 2PC transaction transport (NEW_TX/COMMIT_TX/ROLLBACK_TX), the peer-bank
// registry, the receiver/sender idempotency state + recovery crons, and the
// centralized outbound HTTP egress to permitted peer banks (PeerEgressService).
//
// It exposes gRPC ONLY for business (PeerTxService + PeerBankAdminService +
// PeerEgressService); the api-gateway is the sole HTTP↔gRPC translator for
// inbound peer traffic. The only HTTP this process serves is ops:
// /metrics, /livez, /readyz (via contract/metrics).
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	adminpb "github.com/exbanka/contract/adminpb"
	clientpb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/contract/cronreg"
	exchangepb "github.com/exbanka/contract/exchangepb"
	"github.com/exbanka/contract/logger"
	"github.com/exbanka/contract/metrics"
	shared "github.com/exbanka/contract/shared"
	"github.com/exbanka/contract/shared/grpcmw"
	stockpb "github.com/exbanka/contract/stockpb"
	pb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/interbank-service/internal/config"
	"github.com/exbanka/interbank-service/internal/handler"
	"github.com/exbanka/interbank-service/internal/model"
	"github.com/exbanka/interbank-service/internal/repository"
	"github.com/exbanka/interbank-service/internal/service"
	"github.com/exbanka/interbank-service/internal/sitx"
)

func main() {
	logger.Init("interbank-service")
	cfg := config.Load()
	ctx := context.Background()

	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		log.Fatalf("interbank-service: connect database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.PeerBank{},
		&model.PeerIdempotenceRecord{},
		&model.OutboundPeerTx{},
		&cronreg.CronPauseState{},
	); err != nil {
		log.Fatalf("interbank-service: migrate: %v", err)
	}
	cronRegistry := cronreg.NewRegistry("interbank-service", cronreg.NewGormPauseStore(db))

	ownRouting, _ := strconv.ParseInt(cfg.OwnBankCode, 10, 64)

	// account-service (money legs) — required.
	accountConn, err := grpc.NewClient(cfg.AccountGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if err != nil {
		log.Fatalf("interbank-service: account-service connection: %v", err)
	}
	defer func() { _ = accountConn.Close() }()
	accountClient := accountpb.NewAccountServiceClient(accountConn)

	// stock-service (option legs) — optional; degrades to COMMIT-time best-effort.
	var optionRecorder handler.PeerOptionRecorder
	stockConn, stockErr := grpc.NewClient(cfg.StockGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if stockErr != nil {
		log.Printf("interbank-service: warn: stock-service connection failed; option-leg materialisation disabled: %v", stockErr)
	} else {
		defer func() { _ = stockConn.Close() }()
		optionRecorder = stockpb.NewPeerOTCServiceClient(stockConn)
	}

	// client-service + user-service — used by the /user friendly-name resolver
	// (inbound /cross-bank-protocol/user). Lazy dials; missing connection just
	// means /user resolution errors at call time.
	clientConn, clientErr := grpc.NewClient(cfg.ClientGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if clientErr != nil {
		log.Printf("interbank-service: warn: client-service connection failed: %v", clientErr)
	} else {
		defer func() { _ = clientConn.Close() }()
	}
	userConn, userErr := grpc.NewClient(cfg.UserGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if userErr != nil {
		log.Printf("interbank-service: warn: user-service connection failed: %v", userErr)
	} else {
		defer func() { _ = userConn.Close() }()
	}

	// exchange-service — seller-side FX on cross-currency OTC credits so a premium
	// /strike lands in the recipient's own account currency rather than voting
	// NO_SUCH_ACCOUNT. Lazy dial; a missing connection just disables FX (the
	// executor then fails closed exactly as before for cross-currency credits).
	var exchangeClient exchangepb.ExchangeServiceClient
	exchangeConn, exchangeErr := grpc.NewClient(cfg.ExchangeGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.UnaryClientSagaContextInterceptor()),
	)
	if exchangeErr != nil {
		log.Printf("interbank-service: warn: exchange-service connection failed; seller-side FX on cross-currency OTC credits disabled: %v", exchangeErr)
	} else {
		defer func() { _ = exchangeConn.Close() }()
		exchangeClient = exchangepb.NewExchangeServiceClient(exchangeConn)
	}

	// Repositories.
	peerBankRepo := repository.NewPeerBankRepository(db)
	peerIdemRepo := repository.NewPeerIdempotenceRepository(db)
	outRepo := repository.NewOutboundPeerTxRepository(db)

	// Posting executor (receiver-side account holds + option legs).
	peerExecutor := sitx.NewPostingExecutor(accountClient, ownRouting)
	if stockConn != nil {
		peerExecutor.SetHoldingChecker(stockpb.NewPeerOTCServiceClient(stockConn))
	}
	if exchangeClient != nil {
		peerExecutor.SetConverter(exchangeClient)
	}

	peerHTTPClient := sitx.NewPeerHTTPClient(&http.Client{Timeout: 30 * time.Second})

	// peerLookup resolves a peer-bank-code to a signed HTTP target from peer_banks.
	peerLookup := func(ctx context.Context, code string) (*sitx.PeerHTTPTarget, error) {
		row, err := peerBankRepo.GetByBankCode(code)
		if err != nil {
			return nil, err
		}
		if !row.Active {
			return nil, fmt.Errorf("peer bank %s inactive", code)
		}
		return &sitx.PeerHTTPTarget{
			BankCode:        row.BankCode,
			RoutingNumber:   row.RoutingNumber,
			OwnBankCode:     cfg.OwnBankCode,
			OwnRouting:      ownRouting,
			BaseURL:         row.BaseURL,
			APIToken:        row.APITokenPlaintext,
			HMACOutboundKey: row.HMACOutboundKey,
		}, nil
	}

	peerTxHandler := handler.NewPeerTxGRPCHandler(
		peerIdemRepo, peerExecutor, accountClient,
		outRepo, peerHTTPClient, handler.PeerLookupFunc(peerLookup), ownRouting,
		cfg.ReceiveSyncDeadline,
	)
	if optionRecorder != nil {
		peerTxHandler.SetOptionRecorder(optionRecorder)
	}

	peerBankAdminHandler := handler.NewPeerBankAdminGRPCHandler(peerBankRepo, cfg.OwnBankCode)
	peerEgressHandler := handler.NewPeerEgressGRPCHandler(peerBankRepo, &http.Client{Timeout: 30 * time.Second}, cfg.OwnBankCode)

	// Inbound /cross-bank-protocol forwarders: interbank-service is the single
	// cross-bank backend, so it fronts the OTC surface (→ stock-service) and the
	// /user friendly-name surface (→ client/user-service). The OTC + user DOMAINS
	// stay in their owning services; these only forward.
	var peerOTCForwarder *handler.PeerOTCForwarder
	if stockConn != nil {
		peerOTCForwarder = handler.NewPeerOTCForwarder(stockpb.NewPeerOTCServiceClient(stockConn))
	}
	peerUserHandler := handler.NewPeerUserGRPCHandler(
		clientpb.NewClientServiceClient(clientConn),
		userpb.NewUserServiceClient(userConn),
		ownRouting, cfg.OwnBankDisplayName,
	)

	// Recovery crons (forward-resume + peer status reconcile).
	service.NewOutboundReplayCron(outRepo, peerHTTPClient, service.PeerLookupFunc(peerLookup), cronRegistry).
		WithLocalReversal(peerTxHandler.ReverseOutboundLocal).
		WithLocalCommit(peerTxHandler.CommitOutboundLocal).
		Start(ctx)
	service.NewPeerTxReconciler(outRepo, peerHTTPClient, service.PeerLookupFunc(peerLookup), cronRegistry).
		WithLocalReversal(peerTxHandler.ReverseOutboundLocal).
		WithLocalCommit(peerTxHandler.CommitOutboundLocal).
		Start(ctx)

	// Ops HTTP: /metrics, /livez, /readyz ONLY.
	markReady, addReadinessCheck, metricsShutdown := metrics.StartMetricsServer(cfg.MetricsPort)
	defer func() { _ = metricsShutdown(context.Background()) }()
	if sqlDB, derr := db.DB(); derr == nil {
		addReadinessCheck(func(ctx context.Context) error { return sqlDB.PingContext(ctx) })
	}

	if err := shared.RunGRPCServer(ctx, shared.GRPCServerConfig{
		Address: cfg.GRPCAddr,
		Options: []grpc.ServerOption{
			grpc.ChainUnaryInterceptor(
				metrics.GRPCUnaryServerInterceptor(),
				grpcmw.UnaryLoggingInterceptor("interbank-service"),
				grpcmw.UnarySagaContextInterceptor(),
			),
			grpc.ChainStreamInterceptor(metrics.GRPCStreamServerInterceptor()),
		},
		Register: func(s *grpc.Server) {
			pb.RegisterPeerTxServiceServer(s, peerTxHandler)
			pb.RegisterPeerBankAdminServiceServer(s, peerBankAdminHandler)
			pb.RegisterPeerEgressServiceServer(s, peerEgressHandler)
			pb.RegisterPeerUserServiceServer(s, peerUserHandler)
			if peerOTCForwarder != nil {
				stockpb.RegisterPeerOTCServiceServer(s, peerOTCForwarder)
			}
			adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
			shared.RegisterHealthCheck(s, "interbank-service")
			metrics.InitializeGRPCMetrics(s)
		},
		Signals: shared.DefaultShutdownSignals,
		OnReady: func() {
			markReady()
			slog.Info("interbank-service listening", "addr", cfg.GRPCAddr, "metrics_port", cfg.MetricsPort)
		},
	}); err != nil {
		log.Fatalf("interbank-service: grpc: %v", err)
	}
}
