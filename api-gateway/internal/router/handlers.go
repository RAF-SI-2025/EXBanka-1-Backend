// Package router wires HTTP routes to gRPC-backed handlers. This file defines
// the cross-version Handlers bundle so router_v3.go (and any future
// router_v4.go) can re-use a single set of handler instances rather than
// re-instantiating per version.
package router

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	"github.com/exbanka/api-gateway/internal/handler"
	gatewaykafka "github.com/exbanka/api-gateway/internal/kafka"
	"github.com/exbanka/api-gateway/internal/middleware"
	accountpb "github.com/exbanka/contract/accountpb"
	authpb "github.com/exbanka/contract/authpb"
	cardpb "github.com/exbanka/contract/cardpb"
	clientpb "github.com/exbanka/contract/clientpb"
	creditpb "github.com/exbanka/contract/creditpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	notificationpb "github.com/exbanka/contract/notificationpb"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	userpb "github.com/exbanka/contract/userpb"
	verificationpb "github.com/exbanka/contract/verificationpb"
)

// RateLimitConfig holds the gateway rate-limit wiring (Phase A). A nil Redis
// or a zero limit disables that bucket, so an empty config (as built by tests)
// applies no rate limiting at all.
type RateLimitConfig struct {
	Redis        *redis.Client
	GlobalPerMin int // generous per-IP ceiling across all routes
	LoginPer5Min int // strict per-IP bucket on POST /auth/login
	ResetPer5Min int // strict per-IP bucket on POST /auth/password/reset-request
}

// Deps groups every gRPC client the gateway depends on. It tidies the
// signature of NewHandlers and keeps cmd/main.go a flat assignment block.
type Deps struct {
	AuthClient           authpb.AuthServiceClient
	UserClient           userpb.UserServiceClient
	ClientClient         clientpb.ClientServiceClient
	AccountClient        accountpb.AccountServiceClient
	CardClient           cardpb.CardServiceClient
	TxClient             transactionpb.TransactionServiceClient
	CreditClient         creditpb.CreditServiceClient
	EmpLimitClient       userpb.EmployeeLimitServiceClient
	ClientLimitClient    clientpb.ClientLimitServiceClient
	VirtualCardClient    cardpb.VirtualCardServiceClient
	BankAccountClient    accountpb.BankAccountServiceClient
	FeeClient            transactionpb.FeeServiceClient
	CardRequestClient    cardpb.CardRequestServiceClient
	ExchangeClient       exchangepb.ExchangeServiceClient
	StockExchangeClient  stockpb.StockExchangeGRPCServiceClient
	SecurityClient       stockpb.SecurityGRPCServiceClient
	OrderClient          stockpb.OrderGRPCServiceClient
	PortfolioClient      stockpb.PortfolioGRPCServiceClient
	OTCClient            stockpb.OTCGRPCServiceClient
	TaxClient            stockpb.TaxGRPCServiceClient
	ActuaryClient        userpb.ActuaryServiceClient
	BlueprintClient      userpb.BlueprintServiceClient
	VerificationClient   verificationpb.VerificationGRPCServiceClient
	NotificationClient   notificationpb.NotificationServiceClient
	SourceAdminClient    stockpb.SourceAdminServiceClient
	FundClient           stockpb.InvestmentFundServiceClient
	OTCOptionsClient     stockpb.OTCOptionsServiceClient
	WatchlistClient      stockpb.WatchlistServiceClient
	PriceAlertClient     stockpb.PriceAlertServiceClient
	RecurringOrderClient stockpb.RecurringOrderServiceClient
	RecurringFundClient  stockpb.RecurringFundServiceClient

	// SI-TX peer-bank wiring (Phase 2 Task 14). PeerTxClient dispatches
	// decoded SI-TX envelopes from POST /api/v3/interbank to
	// transaction-service. PeerBankAdminClient backs the
	// /api/v3/peer-banks admin routes. PeerNonces is the Redis-backed
	// nonce dedup window for the HMAC auth path; PeerBanks resolves a
	// peer-bank record from a bank code or API token.
	PeerTxClient        transactionpb.PeerTxServiceClient
	PeerBankAdminClient transactionpb.PeerBankAdminServiceClient
	// PeerUserClient backs /cross-bank-protocol/user/{rid}/{id}, served by
	// interbank-service (2026-06-07 cutover).
	PeerUserClient transactionpb.PeerUserServiceClient
	PeerNonces     middleware.PeerNonceClaimer
	PeerBanks      middleware.PeerBankResolver

	// PeerOTCClient backs the /api/v3/public-stock and
	// /api/v3/negotiations/* peer OTC routes (Phase 4 Task 8 of the
	// Celina 5 SI-TX refactor). It is hosted by stock-service.
	PeerOTCClient stockpb.PeerOTCServiceClient

	// OwnBankCode is the 3-digit bank prefix used by PeerTxDispatcherHandler
	// to distinguish intra-bank receivers from foreign-bank ones. Foreign-
	// prefix receivers dispatch to PeerTxService.InitiateOutboundTx
	// (Phase 3 Task 11 of the SI-TX refactor; see docs/superpowers/specs/
	// 2026-04-29-celina5-sitx-refactor-design.md).
	OwnBankCode string

	// OwnBankName is the human-readable display name surfaced by the
	// peer-facing GET /user/{rid}/{id} endpoint as bankDisplayName
	// (SI-TX §3.7). Falls back to OwnBankCode when unset.
	OwnBankName string

	// AdminCronClients is the per-service pool of AdminCron gRPC clients
	// (C9 — 2026-05-28). A nil entry means the service was unreachable at
	// startup and will show as status "unreachable" in List responses.
	AdminCronClients []*grpcclients.AdminCronClient

	// AuditProducer is the Kafka producer used to publish admin cron audit
	// events to the admin.cron-action topic (C10 — 2026-05-28).
	AuditProducer *gatewaykafka.AuditProducer

	// RateLimit configures the gateway's rate-limit buckets (Phase A).
	// Zero-value (nil Redis) disables limiting.
	RateLimit RateLimitConfig

	// TokenVerifier verifies access tokens for AuthMiddleware/AnyAuthMiddleware
	// (local ES256 with gRPC fallback). Built in cmd/main.go.
	TokenVerifier *middleware.TokenVerifier
}

// Handlers bundles every HTTP handler the gateway exposes. The constructor
// (NewHandlers) takes a single Deps argument so adding a new dependency
// doesn't ripple into every router version.
type Handlers struct {
	Auth             *handler.AuthHandler
	Version          *handler.VersionHandler
	Employee         *handler.EmployeeHandler
	Role             *handler.RoleHandler
	Limit            *handler.LimitHandler
	Client           *handler.ClientHandler
	Account          *handler.AccountHandler
	Card             *handler.CardHandler
	Tx               *handler.TransactionHandler
	Exchange         *handler.ExchangeHandler
	Credit           *handler.CreditHandler
	Me               *handler.MeHandler
	Session          *handler.SessionHandler
	StockExchange    *handler.StockExchangeHandler
	Securities       *handler.SecuritiesHandler
	StockOrder       *handler.StockOrderHandler
	Portfolio        *handler.PortfolioHandler
	UnifiedPortfolio *handler.UnifiedPortfolioHandler
	Actuary          *handler.ActuaryHandler
	Blueprint        *handler.BlueprintHandler
	Tax              *handler.TaxHandler
	StockSource      *handler.StockSourceHandler
	Notification     *handler.NotificationHandler
	MobileAuth       *handler.MobileAuthHandler
	Verification     *handler.VerificationHandler
	OptionsV2        *handler.OptionsV2Handler
	Fund             *handler.InvestmentFundHandler
	OTCOptions       *handler.OTCOptionsHandler
	PeerTxDispatcher *handler.PeerTxDispatcherHandler
	Changelog        *handler.ChangelogHandler
	Watchlist        *handler.WatchlistHandler
	PriceAlert       *handler.PriceAlertHandler
	RecurringOrder   *handler.RecurringOrderHandler
	RecurringFund    *handler.RecurringFundHandler

	// SI-TX peer-facing wiring (Phase 2 Task 14). PeerTx serves
	// POST /api/v3/interbank, PeerBankAdmin serves /api/v3/peer-banks
	// admin routes, and PeerAuthMW is the hybrid auth middleware
	// (X-Api-Key OR HMAC bundle) that protects /interbank.
	PeerTx        *handler.PeerTxHandler
	PeerTxStatus  *handler.PeerTxStatusHandler
	PeerBankAdmin *handler.PeerBankAdminHandler
	PeerAuthMW    gin.HandlerFunc

	// Phase 4 SI-TX OTC peer-facing handlers (Celina 5).
	// PeerOTC serves /api/v3/cross-bank-protocol/public-stock and
	// .../negotiations/*; PeerUser serves .../user/{rid}/{id}.
	// (SP-2b clean-cut, 2026-06-05) The OUTBOUND client-facing
	// PeerOTCInitiateHandler was retired — cross-bank negotiation
	// initiation/counter/accept/cancel now dispatch inside stock-service
	// behind the unified /api/v3/otc/... routes.
	PeerOTC  *handler.PeerOTCHandler
	PeerUser *handler.PeerUserHandler

	// AdminCron serves the /api/v3/admin/crons/* routes (C10 — 2026-05-28).
	AdminCron *handler.AdminCronHandler

	// AdminAudit serves the /api/v3/admin/audit/* routes (D4 — 2026-05-28).
	AdminAudit *handler.AdminAuditHandler

	// Dividend serves the /api/v3/admin/dividends/* and /api/v3/me/dividends
	// routes (E4 — 2026-05-28).
	Dividend *handler.DividendHandler

	// RateLimit carries the rate-limit wiring from Deps so SetupV3 can apply
	// the global + strict-auth buckets (Phase A).
	RateLimit RateLimitConfig

	// TokenVerifier backs AuthMiddleware/AnyAuthMiddleware (local ES256 verify).
	TokenVerifier *middleware.TokenVerifier
}

// NewHandlers wires every handler from the supplied gRPC client deps.
// Call once at startup; pass the returned bundle to SetupV3 (and any
// future SetupV4) so all versions share the same handler instances.
func NewHandlers(d Deps) *Handlers {
	tx := handler.NewTransactionHandler(d.TxClient, d.FeeClient, d.AccountClient, d.ExchangeClient)
	h := &Handlers{
		Auth:             handler.NewAuthHandler(d.AuthClient),
		Version:          handler.NewVersionHandler(),
		Employee:         handler.NewEmployeeHandler(d.UserClient, d.AuthClient),
		Role:             handler.NewRoleHandler(d.UserClient),
		Limit:            handler.NewLimitHandler(d.EmpLimitClient, d.ClientLimitClient),
		Client:           handler.NewClientHandler(d.ClientClient, d.AuthClient),
		Account:          handler.NewAccountHandler(d.AccountClient, d.BankAccountClient, d.CardClient, d.TxClient),
		Card:             handler.NewCardHandler(d.CardClient, d.VirtualCardClient, d.CardRequestClient, d.AccountClient),
		Tx:               tx,
		Exchange:         handler.NewExchangeHandler(d.ExchangeClient),
		Credit:           handler.NewCreditHandler(d.CreditClient),
		Me:               handler.NewMeHandler(d.ClientClient, d.UserClient, d.AuthClient),
		Session:          handler.NewSessionHandler(d.AuthClient),
		StockExchange:    handler.NewStockExchangeHandler(d.StockExchangeClient),
		Securities:       handler.NewSecuritiesHandler(d.SecurityClient),
		StockOrder:       handler.NewStockOrderHandler(d.OrderClient, d.AccountClient),
		Portfolio:        handler.NewPortfolioHandler(d.PortfolioClient, d.OTCClient, d.AccountClient),
		UnifiedPortfolio: handler.NewUnifiedPortfolioHandler(d.PortfolioClient),
		Actuary:          handler.NewActuaryHandler(d.ActuaryClient),
		Blueprint:        handler.NewBlueprintHandler(d.BlueprintClient, d.ClientLimitClient),
		Tax:              handler.NewTaxHandler(d.TaxClient),
		StockSource:      handler.NewStockSourceHandler(d.SourceAdminClient),
		Notification:     handler.NewNotificationHandler(d.NotificationClient),
		MobileAuth:       handler.NewMobileAuthHandler(d.AuthClient),
		Verification:     handler.NewVerificationHandler(d.VerificationClient, d.NotificationClient),
		OptionsV2:        handler.NewOptionsV2Handler(d.SecurityClient, d.OrderClient, d.PortfolioClient),
		Fund:             handler.NewInvestmentFundHandler(d.FundClient),
		OTCOptions:       handler.NewOTCOptionsHandler(d.OTCOptionsClient, d.SecurityClient, d.AccountClient),
		PeerTxDispatcher: handler.NewPeerTxDispatcherHandler(tx, d.PeerTxClient, d.OwnBankCode),
		Changelog:        handler.NewChangelogHandler(d.AccountClient, d.CardClient, d.ClientClient, d.CreditClient, d.UserClient),
		Watchlist:        handler.NewWatchlistHandler(d.WatchlistClient),
		PriceAlert:       handler.NewPriceAlertHandler(d.PriceAlertClient),
		RecurringOrder:   handler.NewRecurringOrderHandler(d.RecurringOrderClient),
		RecurringFund:    handler.NewRecurringFundHandler(d.RecurringFundClient),
		PeerTx:           handler.NewPeerTxHandler(d.PeerTxClient),
		PeerTxStatus:     handler.NewPeerTxStatusHandler(d.PeerTxClient),
		PeerBankAdmin:    handler.NewPeerBankAdminHandler(d.PeerBankAdminClient),
		PeerAuthMW:       middleware.PeerAuth(d.PeerBanks, d.PeerNonces, 5*time.Minute),
		PeerOTC:          handler.NewPeerOTCHandler(d.PeerOTCClient),
		PeerUser:         handler.NewPeerUserHandler(d.PeerUserClient),
		AdminCron:        handler.NewAdminCronHandler(d.AdminCronClients, d.AuditProducer),
		AdminAudit:       handler.NewAdminAuditHandler(d.AccountClient, d.CardClient, d.ClientClient, d.CreditClient, d.UserClient, d.NotificationClient, d.TxClient),
		Dividend:         handler.NewDividendHandler(d.FundClient),
	}
	// Wire the optional business-audit publisher into the handlers that perform
	// audited business actions (limit changes, usedLimit resets, order
	// approve/reject, permission changes, manual tax collection). Best-effort:
	// nil-safe, never blocks the request. SP2 audit log.
	h.Limit.Audit = d.AuditProducer
	h.Actuary.Audit = d.AuditProducer
	h.StockOrder.Audit = d.AuditProducer
	h.Role.Audit = d.AuditProducer
	h.Tax.Audit = d.AuditProducer
	h.RateLimit = d.RateLimit
	h.TokenVerifier = d.TokenVerifier
	return h
}
