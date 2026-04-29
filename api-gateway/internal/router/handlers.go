// Package router wires HTTP routes to gRPC-backed handlers. This file defines
// the cross-version Handlers bundle so router_v3.go (and any future
// router_v4.go) can re-use a single set of handler instances rather than
// re-instantiating per version.
package router

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/handler"
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

// Deps groups every gRPC client the gateway depends on. It tidies the
// signature of NewHandlers and keeps cmd/main.go a flat assignment block.
type Deps struct {
	AuthClient          authpb.AuthServiceClient
	UserClient          userpb.UserServiceClient
	ClientClient        clientpb.ClientServiceClient
	AccountClient       accountpb.AccountServiceClient
	CardClient          cardpb.CardServiceClient
	TxClient            transactionpb.TransactionServiceClient
	CreditClient        creditpb.CreditServiceClient
	EmpLimitClient      userpb.EmployeeLimitServiceClient
	ClientLimitClient   clientpb.ClientLimitServiceClient
	VirtualCardClient   cardpb.VirtualCardServiceClient
	BankAccountClient   accountpb.BankAccountServiceClient
	FeeClient           transactionpb.FeeServiceClient
	CardRequestClient   cardpb.CardRequestServiceClient
	ExchangeClient      exchangepb.ExchangeServiceClient
	StockExchangeClient stockpb.StockExchangeGRPCServiceClient
	SecurityClient      stockpb.SecurityGRPCServiceClient
	OrderClient         stockpb.OrderGRPCServiceClient
	PortfolioClient     stockpb.PortfolioGRPCServiceClient
	OTCClient           stockpb.OTCGRPCServiceClient
	TaxClient           stockpb.TaxGRPCServiceClient
	ActuaryClient       userpb.ActuaryServiceClient
	BlueprintClient     userpb.BlueprintServiceClient
	VerificationClient  verificationpb.VerificationGRPCServiceClient
	NotificationClient  notificationpb.NotificationServiceClient
	SourceAdminClient   stockpb.SourceAdminServiceClient
	FundClient          stockpb.InvestmentFundServiceClient
	OTCOptionsClient    stockpb.OTCOptionsServiceClient

	// SI-TX peer-bank wiring (Phase 2 Task 14). PeerTxClient dispatches
	// decoded SI-TX envelopes from POST /api/v3/interbank to
	// transaction-service. PeerBankAdminClient backs the
	// /api/v3/peer-banks admin routes. PeerNonces is the Redis-backed
	// nonce dedup window for the HMAC auth path; PeerBanks resolves a
	// peer-bank record from a bank code or API token.
	PeerTxClient        transactionpb.PeerTxServiceClient
	PeerBankAdminClient transactionpb.PeerBankAdminServiceClient
	PeerNonces          middleware.PeerNonceClaimer
	PeerBanks           middleware.PeerBankResolver

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
}

// Handlers bundles every HTTP handler the gateway exposes. The constructor
// (NewHandlers) takes a single Deps argument so adding a new dependency
// doesn't ripple into every router version.
type Handlers struct {
	Auth             *handler.AuthHandler
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

	// SI-TX peer-facing wiring (Phase 2 Task 14). PeerTx serves
	// POST /api/v3/interbank, PeerBankAdmin serves /api/v3/peer-banks
	// admin routes, and PeerAuthMW is the hybrid auth middleware
	// (X-Api-Key OR HMAC bundle) that protects /interbank.
	PeerTx        *handler.PeerTxHandler
	PeerBankAdmin *handler.PeerBankAdminHandler
	PeerAuthMW    gin.HandlerFunc

	// Phase 4 SI-TX OTC peer-facing handlers (Celina 5).
	// PeerOTC serves /api/v3/public-stock and /api/v3/negotiations/*;
	// PeerUser serves /api/v3/user/{rid}/{id}.
	PeerOTC  *handler.PeerOTCHandler
	PeerUser *handler.PeerUserHandler
}

// NewHandlers wires every handler from the supplied gRPC client deps.
// Call once at startup; pass the returned bundle to SetupV3 (and any
// future SetupV4) so all versions share the same handler instances.
func NewHandlers(d Deps) *Handlers {
	tx := handler.NewTransactionHandler(d.TxClient, d.FeeClient, d.AccountClient, d.ExchangeClient)
	// OwnBankCode is a 3-digit string ("111"); parse it once for the
	// PeerUserHandler which needs an int64 routing-number comparator.
	ownRouting, _ := strconv.ParseInt(d.OwnBankCode, 10, 64)
	return &Handlers{
		Auth:             handler.NewAuthHandler(d.AuthClient),
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
		Actuary:          handler.NewActuaryHandler(d.ActuaryClient),
		Blueprint:        handler.NewBlueprintHandler(d.BlueprintClient),
		Tax:              handler.NewTaxHandler(d.TaxClient),
		StockSource:      handler.NewStockSourceHandler(d.SourceAdminClient),
		Notification:     handler.NewNotificationHandler(d.NotificationClient),
		MobileAuth:       handler.NewMobileAuthHandler(d.AuthClient),
		Verification:     handler.NewVerificationHandler(d.VerificationClient, d.NotificationClient),
		OptionsV2:        handler.NewOptionsV2Handler(d.SecurityClient, d.OrderClient, d.PortfolioClient),
		Fund:             handler.NewInvestmentFundHandler(d.FundClient),
		OTCOptions:       handler.NewOTCOptionsHandler(d.OTCOptionsClient),
		PeerTxDispatcher: handler.NewPeerTxDispatcherHandler(tx, d.PeerTxClient, d.OwnBankCode),
		Changelog:        handler.NewChangelogHandler(d.AccountClient, d.CardClient, d.ClientClient, d.CreditClient, d.UserClient),
		PeerTx:           handler.NewPeerTxHandler(d.PeerTxClient),
		PeerBankAdmin:    handler.NewPeerBankAdminHandler(d.PeerBankAdminClient),
		PeerAuthMW:       middleware.PeerAuth(d.PeerBanks, d.PeerNonces, 5*time.Minute),
		PeerOTC:          handler.NewPeerOTCHandler(d.PeerOTCClient),
		PeerUser:         handler.NewPeerUserHandler(d.ClientClient, d.UserClient, ownRouting),
	}
}
