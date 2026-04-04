# API Versioning & Router Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add /api/v1/ routes that mirror all current /api/ routes plus new endpoints, /api/latest alias, per-version router files, and per-version REST API docs.

**Architecture:** Frozen /api/ routes in router.go (untouched). New router_v1.go mirrors routes under /api/v1/ with same handlers. router_latest.go aliases /api/latest to /api/v1/. V1 handlers can diverge over time.

**Tech Stack:** Go, Gin, gRPC

---

## Design Decisions

1. **router.go stays frozen.** The existing `/api/` routes remain untouched to preserve backward compatibility. All new code goes in new files.
2. **Handlers are recreated, not shared.** `SetupV1Routes` receives the same gRPC client parameters as `Setup` and creates its own handler instances. Since handlers are stateless wrappers around gRPC clients, duplication is zero-cost and avoids coupling the two routers.
3. **`/api/latest` is a path rewrite, not a route copy.** A single `r.Any("/api/latest/*path", ...)` rewrites the URL to `/api/v1/...` and re-dispatches via `r.HandleContext(c)`. This means `/api/latest` always resolves to whatever the highest numbered version is without maintaining a third set of routes.
4. **Placeholder endpoints return 501.** New v1-only endpoints (sessions, changelog, candles) return `501 Not Implemented` with a structured error body until Plans 2, 3, 5 implement the backing gRPC calls.
5. **V1 REST API docs are a separate file.** `docs/api/REST_API_v1.md` documents the v1 surface. The original `docs/api/REST_API.md` stays unchanged (it documents `/api/`).

## Route inventory

The following is the complete list of routes in `router.go`'s `Setup()` that must be mirrored under `/api/v1/`:

**Public auth (no middleware):**
- POST /auth/login
- POST /auth/refresh
- POST /auth/logout
- POST /auth/password/reset-request
- POST /auth/password/reset
- POST /auth/activate

**Public exchange (no middleware):**
- GET /exchange/rates
- GET /exchange/rates/:from/:to
- POST /exchange/calculate

**Me routes (AnyAuthMiddleware):**
- GET /me
- GET /me/accounts, GET /me/accounts/:id
- GET /me/cards, GET /me/cards/:id, POST /me/cards/:id/pin, POST /me/cards/:id/verify-pin, POST /me/cards/:id/temporary-block, POST /me/cards/virtual, POST /me/cards/requests, GET /me/cards/requests
- POST /me/payments, GET /me/payments, GET /me/payments/:id, POST /me/payments/:id/execute
- POST /me/transfers, GET /me/transfers, GET /me/transfers/:id, POST /me/transfers/:id/execute
- POST /me/payment-recipients, GET /me/payment-recipients, PUT /me/payment-recipients/:id, DELETE /me/payment-recipients/:id
- POST /me/loan-requests, GET /me/loan-requests, GET /me/loans, GET /me/loans/:id, GET /me/loans/:id/installments
- POST /me/orders, GET /me/orders, GET /me/orders/:id, POST /me/orders/:id/cancel
- GET /me/portfolio, GET /me/portfolio/summary, POST /me/portfolio/:id/make-public, POST /me/portfolio/:id/exercise
- GET /me/tax

**Stock exchanges (AnyAuthMiddleware):**
- GET /stock-exchanges, GET /stock-exchanges/:id

**Securities (AnyAuthMiddleware):**
- GET /securities/stocks, GET /securities/stocks/:id, GET /securities/stocks/:id/history
- GET /securities/futures, GET /securities/futures/:id, GET /securities/futures/:id/history
- GET /securities/forex, GET /securities/forex/:id, GET /securities/forex/:id/history
- GET /securities/options, GET /securities/options/:id

**OTC (AnyAuthMiddleware):**
- GET /otc/offers, POST /otc/offers/:id/buy

**Mobile auth (public):**
- POST /mobile/auth/request-activation, POST /mobile/auth/activate, POST /mobile/auth/refresh

**Mobile device (MobileAuthMiddleware):**
- GET /mobile/device, POST /mobile/device/deactivate, POST /mobile/device/transfer

**Mobile verifications (MobileAuthMiddleware + RequireDeviceSignature):**
- GET /mobile/verifications/pending, POST /mobile/verifications/:challenge_id/submit

**QR verification (MobileAuthMiddleware + RequireDeviceSignature):**
- POST /verify/:challenge_id

**WebSocket:**
- GET /ws/mobile

**Browser verifications (AnyAuthMiddleware):**
- POST /verifications, GET /verifications/:id/status, POST /verifications/:id/code

**Employee/admin (AuthMiddleware + RequirePermission):**
- GET /employees, GET /employees/:id (employees.read)
- POST /employees (employees.create)
- PUT /employees/:id (employees.update)
- GET /roles, GET /roles/:id, POST /roles, PUT /roles/:id/permissions, GET /permissions, PUT /employees/:id/roles, PUT /employees/:id/permissions (employees.permissions)
- GET /employees/:id/limits, PUT /employees/:id/limits, POST /employees/:id/limits/template, GET /limits/templates, POST /limits/templates, GET /clients/:id/limits, PUT /clients/:id/limits (limits.manage)
- GET /clients, GET /clients/:id (clients.read)
- POST /clients (clients.create)
- PUT /clients/:id (clients.update)
- GET /currencies (any authenticated employee)
- GET /accounts, GET /accounts/:id, GET /accounts/by-number/:account_number (accounts.read)
- POST /accounts (accounts.create)
- PUT /accounts/:id/name, PUT /accounts/:id/limits, PUT /accounts/:id/status (accounts.update)
- POST /companies (accounts.create)
- GET /bank-accounts, POST /bank-accounts, DELETE /bank-accounts/:id (bank-accounts.manage)
- GET /cards, GET /cards/:id (cards.read)
- POST /cards, POST /cards/authorized-person (cards.create)
- POST /cards/:id/block, POST /cards/:id/unblock, POST /cards/:id/deactivate (cards.update)
- GET /cards/requests, GET /cards/requests/:id, POST /cards/requests/:id/approve, POST /cards/requests/:id/reject (cards.approve)
- GET /payments, GET /payments/:id (payments.read)
- GET /transfers, GET /transfers/:id (payments.read)
- GET /fees, POST /fees, PUT /fees/:id, DELETE /fees/:id (fees.manage)
- GET /loans, GET /loans/:id, GET /loans/:id/installments (credits.read)
- GET /loan-requests, GET /loan-requests/:id (credits.read)
- POST /loan-requests/:id/approve, POST /loan-requests/:id/reject (credits.approve)
- GET /interest-rate-tiers, POST /interest-rate-tiers, PUT /interest-rate-tiers/:id, DELETE /interest-rate-tiers/:id, POST /interest-rate-tiers/:id/apply (interest-rates.manage)
- GET /bank-margins, PUT /bank-margins/:id (interest-rates.manage)
- POST /stock-exchanges/testing-mode, GET /stock-exchanges/testing-mode (exchanges.manage)
- GET /orders, POST /orders/:id/approve, POST /orders/:id/decline (orders.approve)
- GET /actuaries, PUT /actuaries/:id/limit, POST /actuaries/:id/reset-limit, PUT /actuaries/:id/approval (agents.manage)
- GET /tax, POST /tax/collect (tax.manage)

**New v1-only endpoints (placeholders returning 501):**
- GET /me/sessions (Plan 3 - auth sessions)
- DELETE /me/sessions/:id (Plan 3)
- DELETE /me/sessions (Plan 3 - revoke all sessions)
- GET /me/login-history (Plan 3)
- GET /accounts/:id/changelog (Plan 2 - audit trails)
- GET /employees/:id/changelog (Plan 2)
- GET /clients/:id/changelog (Plan 2)
- GET /cards/:id/changelog (Plan 2)
- GET /loans/:id/changelog (Plan 2)
- GET /securities/stocks/:id/candles (Plan 5 - InfluxDB candles)
- GET /securities/futures/:id/candles (Plan 5)
- GET /securities/forex/:id/candles (Plan 5)

---

## Task 1: Create `router_v1.go` with all v1 routes

**Files:**
- Create: `api-gateway/internal/router/router_v1.go`

This file mirrors every route from `router.go` under `/api/v1/` and adds the new v1-only placeholder endpoints. It creates its own handler and middleware instances from the same gRPC client parameters.

### Steps

- [ ] **1.1** Create `api-gateway/internal/router/router_v1.go` with the full `SetupV1Routes` function.

```go
// api-gateway/internal/router/router_v1.go
package router

import (
	"net/http"

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

// notImplemented returns a 501 handler for endpoints planned but not yet backed by gRPC.
func notImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": gin.H{
			"code":    "not_implemented",
			"message": "this endpoint is coming in a future release",
		},
	})
}

// SetupV1Routes registers all /api/v1/ routes on the given engine.
// It mirrors every route from Setup() (the /api/ router) and adds new v1-only
// placeholder endpoints. The handler instances are freshly created from the
// same gRPC clients — they are stateless, so duplication is zero-cost.
//
// IMPORTANT: This function must be called AFTER Setup() has already configured
// the engine (CORS, swagger, etc.) and registered /api/ routes.
func SetupV1Routes(
	r *gin.Engine,
	authClient authpb.AuthServiceClient,
	userClient userpb.UserServiceClient,
	clientClient clientpb.ClientServiceClient,
	accountClient accountpb.AccountServiceClient,
	cardClient cardpb.CardServiceClient,
	txClient transactionpb.TransactionServiceClient,
	creditClient creditpb.CreditServiceClient,
	empLimitClient userpb.EmployeeLimitServiceClient,
	clientLimitClient clientpb.ClientLimitServiceClient,
	virtualCardClient cardpb.VirtualCardServiceClient,
	bankAccountClient accountpb.BankAccountServiceClient,
	feeClient transactionpb.FeeServiceClient,
	cardRequestClient cardpb.CardRequestServiceClient,
	exchangeClient exchangepb.ExchangeServiceClient,
	stockExchangeClient stockpb.StockExchangeGRPCServiceClient,
	securityClient stockpb.SecurityGRPCServiceClient,
	orderClient stockpb.OrderGRPCServiceClient,
	portfolioClient stockpb.PortfolioGRPCServiceClient,
	otcClient stockpb.OTCGRPCServiceClient,
	taxClient stockpb.TaxGRPCServiceClient,
	actuaryClient userpb.ActuaryServiceClient,
	verificationClient verificationpb.VerificationGRPCServiceClient,
	notificationClient notificationpb.NotificationServiceClient,
	wsHandler *handler.WebSocketHandler,
) {
	// ── Create handlers (same as Setup, stateless wrappers) ──────────────
	authHandler := handler.NewAuthHandler(authClient)
	empHandler := handler.NewEmployeeHandler(userClient, authClient)
	roleHandler := handler.NewRoleHandler(userClient)
	limitHandler := handler.NewLimitHandler(empLimitClient, clientLimitClient)
	clientHandler := handler.NewClientHandler(clientClient, authClient)
	accountHandler := handler.NewAccountHandler(accountClient, bankAccountClient, cardClient, txClient)
	cardHandler := handler.NewCardHandler(cardClient, virtualCardClient, cardRequestClient)
	txHandler := handler.NewTransactionHandler(txClient, feeClient, accountClient)
	exchangeHandler := handler.NewExchangeHandler(exchangeClient)
	creditHandler := handler.NewCreditHandler(creditClient)
	meHandler := handler.NewMeHandler(clientClient, userClient, authClient)
	stockExchangeHandler := handler.NewStockExchangeHandler(stockExchangeClient)
	securitiesHandler := handler.NewSecuritiesHandler(securityClient)
	stockOrderHandler := handler.NewStockOrderHandler(orderClient)
	portfolioHandler := handler.NewPortfolioHandler(portfolioClient, otcClient)
	actuaryHandler := handler.NewActuaryHandler(actuaryClient)
	taxHandler := handler.NewTaxHandler(taxClient)

	// ── /api/v1 root group ──────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// ── Public auth routes (no middleware) ───────────────────────
		auth := v1.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
			auth.POST("/password/reset", authHandler.ResetPassword)
			auth.POST("/activate", authHandler.ActivateAccount)
		}

		// ── Public exchange rate routes (no middleware) ──────────────
		v1.GET("/exchange/rates", exchangeHandler.ListExchangeRates)
		v1.GET("/exchange/rates/:from/:to", exchangeHandler.GetExchangeRate)
		v1.POST("/exchange/calculate", exchangeHandler.CalculateExchange)

		// ── /api/v1/me/* (AnyAuthMiddleware) ────────────────────────
		me := v1.Group("/me")
		me.Use(middleware.AnyAuthMiddleware(authClient))
		{
			me.GET("", middleware.RequireClientToken(), meHandler.GetMe)

			// Accounts
			me.GET("/accounts", accountHandler.ListMyAccounts)
			me.GET("/accounts/:id", accountHandler.GetMyAccount)

			// Cards
			me.GET("/cards", cardHandler.ListMyCards)
			me.GET("/cards/:id", cardHandler.GetMyCard)
			me.POST("/cards/:id/pin", middleware.RequireClientToken(), cardHandler.SetCardPin)
			me.POST("/cards/:id/verify-pin", middleware.RequireClientToken(), cardHandler.VerifyCardPin)
			me.POST("/cards/:id/temporary-block", middleware.RequireClientToken(), cardHandler.TemporaryBlockCard)
			me.POST("/cards/virtual", cardHandler.CreateVirtualCard)
			me.POST("/cards/requests", middleware.RequireClientToken(), cardHandler.CreateCardRequest)
			me.GET("/cards/requests", middleware.RequireClientToken(), cardHandler.ListMyCardRequests)

			// Payments
			me.POST("/payments", txHandler.CreatePayment)
			me.GET("/payments", txHandler.ListMyPayments)
			me.GET("/payments/:id", txHandler.GetMyPayment)
			me.POST("/payments/:id/execute", txHandler.ExecutePayment)

			// Transfers
			me.POST("/transfers", txHandler.CreateTransfer)
			me.GET("/transfers", txHandler.ListMyTransfers)
			me.GET("/transfers/:id", txHandler.GetMyTransfer)
			me.POST("/transfers/:id/execute", txHandler.ExecuteTransfer)

			// Payment recipients
			me.POST("/payment-recipients", txHandler.CreateMyPaymentRecipient)
			me.GET("/payment-recipients", txHandler.ListMyPaymentRecipients)
			me.PUT("/payment-recipients/:id", txHandler.UpdatePaymentRecipient)
			me.DELETE("/payment-recipients/:id", txHandler.DeletePaymentRecipient)

			// Loans
			me.POST("/loan-requests", creditHandler.CreateLoanRequest)
			me.GET("/loan-requests", creditHandler.ListMyLoanRequests)
			me.GET("/loans", creditHandler.ListMyLoans)
			me.GET("/loans/:id", creditHandler.GetMyLoan)
			me.GET("/loans/:id/installments", creditHandler.GetMyInstallments)

			// Stock orders
			me.POST("/orders", stockOrderHandler.CreateOrder)
			me.GET("/orders", stockOrderHandler.ListMyOrders)
			me.GET("/orders/:id", stockOrderHandler.GetMyOrder)
			me.POST("/orders/:id/cancel", stockOrderHandler.CancelOrder)

			// Portfolio
			me.GET("/portfolio", portfolioHandler.ListHoldings)
			me.GET("/portfolio/summary", portfolioHandler.GetPortfolioSummary)
			me.POST("/portfolio/:id/make-public", portfolioHandler.MakePublic)
			me.POST("/portfolio/:id/exercise", portfolioHandler.ExerciseOption)

			// Tax
			me.GET("/tax", taxHandler.ListMyTaxRecords)

			// ── v1-only: Sessions (Plan 3) ──────────────────────────
			me.GET("/sessions", notImplemented)
			me.DELETE("/sessions/:id", notImplemented)
			me.DELETE("/sessions", notImplemented)

			// ── v1-only: Login history (Plan 3) ─────────────────────
			me.GET("/login-history", notImplemented)
		}

		// ── Stock exchanges (AnyAuthMiddleware) ─────────────────────
		stockExchanges := v1.Group("/stock-exchanges")
		stockExchanges.Use(middleware.AnyAuthMiddleware(authClient))
		{
			stockExchanges.GET("", stockExchangeHandler.ListExchanges)
			stockExchanges.GET("/:id", stockExchangeHandler.GetExchange)
		}

		// ── Securities (AnyAuthMiddleware) ──────────────────────────
		securities := v1.Group("/securities")
		securities.Use(middleware.AnyAuthMiddleware(authClient))
		{
			securities.GET("/stocks", securitiesHandler.ListStocks)
			securities.GET("/stocks/:id", securitiesHandler.GetStock)
			securities.GET("/stocks/:id/history", securitiesHandler.GetStockHistory)
			securities.GET("/futures", securitiesHandler.ListFutures)
			securities.GET("/futures/:id", securitiesHandler.GetFutures)
			securities.GET("/futures/:id/history", securitiesHandler.GetFuturesHistory)
			securities.GET("/forex", securitiesHandler.ListForexPairs)
			securities.GET("/forex/:id", securitiesHandler.GetForexPair)
			securities.GET("/forex/:id/history", securitiesHandler.GetForexPairHistory)
			securities.GET("/options", securitiesHandler.ListOptions)
			securities.GET("/options/:id", securitiesHandler.GetOption)

			// ── v1-only: Candle chart data (Plan 5) ─────────────────
			securities.GET("/stocks/:id/candles", notImplemented)
			securities.GET("/futures/:id/candles", notImplemented)
			securities.GET("/forex/:id/candles", notImplemented)
		}

		// ── OTC (AnyAuthMiddleware) ─────────────────────────────────
		otc := v1.Group("/otc/offers")
		otc.Use(middleware.AnyAuthMiddleware(authClient))
		{
			otc.GET("", portfolioHandler.ListOTCOffers)
			otc.POST("/:id/buy", portfolioHandler.BuyOTCOffer)
		}

		// ── Mobile auth (public) ────────────────────────────────────
		mobileAuth := v1.Group("/mobile/auth")
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileAuth.POST("/request-activation", mobileAuthHandler.RequestActivation)
			mobileAuth.POST("/activate", mobileAuthHandler.ActivateDevice)
			mobileAuth.POST("/refresh", mobileAuthHandler.RefreshMobileToken)
		}

		// ── Mobile device management (MobileAuthMiddleware) ─────────
		mobileDevice := v1.Group("/mobile/device")
		mobileDevice.Use(middleware.MobileAuthMiddleware(authClient))
		{
			mobileAuthHandler := handler.NewMobileAuthHandler(authClient)
			mobileDevice.GET("", mobileAuthHandler.GetDeviceInfo)
			mobileDevice.POST("/deactivate", mobileAuthHandler.DeactivateDevice)
			mobileDevice.POST("/transfer", mobileAuthHandler.TransferDevice)
		}

		// ── Mobile verifications (MobileAuth + DeviceSignature) ─────
		mobileVerify := v1.Group("/mobile/verifications")
		mobileVerify.Use(middleware.MobileAuthMiddleware(authClient))
		mobileVerify.Use(middleware.RequireDeviceSignature(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			mobileVerify.GET("/pending", verifyHandler.GetPendingVerifications)
			mobileVerify.POST("/:challenge_id/submit", verifyHandler.SubmitMobileVerification)
		}

		// ── QR verification (MobileAuth + DeviceSignature) ──────────
		qrVerify := v1.Group("/verify")
		qrVerify.Use(middleware.MobileAuthMiddleware(authClient))
		qrVerify.Use(middleware.RequireDeviceSignature(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			qrVerify.POST("/:challenge_id", verifyHandler.VerifyQR)
		}

		// ── WebSocket ───────────────────────────────────────────────
		v1.GET("/ws/mobile", wsHandler.HandleConnect)

		// ── Browser-facing verifications (AnyAuthMiddleware) ────────
		verifications := v1.Group("/verifications")
		verifications.Use(middleware.AnyAuthMiddleware(authClient))
		{
			verifyHandler := handler.NewVerificationHandler(verificationClient, notificationClient)
			verifications.POST("", verifyHandler.CreateVerification)
			verifications.GET("/:id/status", verifyHandler.GetVerificationStatus)
			verifications.POST("/:id/code", verifyHandler.SubmitVerificationCode)
		}

		// ── Employee/admin routes (AuthMiddleware + RequirePermission) ─
		protected := v1.Group("/")
		protected.Use(middleware.AuthMiddleware(authClient))
		{
			// Employees
			employees := protected.Group("/employees")
			employees.Use(middleware.RequirePermission("employees.read"))
			{
				employees.GET("", empHandler.ListEmployees)
				employees.GET("/:id", empHandler.GetEmployee)
			}
			adminEmployees := protected.Group("/employees")
			adminEmployees.Use(middleware.RequirePermission("employees.create"))
			{
				adminEmployees.POST("", empHandler.CreateEmployee)
			}
			updateEmployees := protected.Group("/employees")
			updateEmployees.Use(middleware.RequirePermission("employees.update"))
			{
				updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
			}

			// Role and permission management
			permManagement := protected.Group("/")
			permManagement.Use(middleware.RequirePermission("employees.permissions"))
			{
				permManagement.GET("/roles", roleHandler.ListRoles)
				permManagement.GET("/roles/:id", roleHandler.GetRole)
				permManagement.POST("/roles", roleHandler.CreateRole)
				permManagement.PUT("/roles/:id/permissions", roleHandler.UpdateRolePermissions)
				permManagement.GET("/permissions", roleHandler.ListPermissions)
				permManagement.PUT("/employees/:id/roles", roleHandler.SetEmployeeRoles)
				permManagement.PUT("/employees/:id/permissions", roleHandler.SetEmployeeAdditionalPermissions)
			}

			// Employee limit management
			limitsEmployee := protected.Group("/")
			limitsEmployee.Use(middleware.RequirePermission("limits.manage"))
			{
				limitsEmployee.GET("/employees/:id/limits", limitHandler.GetEmployeeLimits)
				limitsEmployee.PUT("/employees/:id/limits", limitHandler.SetEmployeeLimits)
				limitsEmployee.POST("/employees/:id/limits/template", limitHandler.ApplyLimitTemplate)
				limitsEmployee.GET("/limits/templates", limitHandler.ListLimitTemplates)
				limitsEmployee.POST("/limits/templates", limitHandler.CreateLimitTemplate)
				limitsEmployee.GET("/clients/:id/limits", limitHandler.GetClientLimits)
				limitsEmployee.PUT("/clients/:id/limits", limitHandler.SetClientLimits)
			}

			// Client management
			clientsRead := protected.Group("/clients")
			clientsRead.Use(middleware.RequirePermission("clients.read"))
			{
				clientsRead.GET("", clientHandler.ListClients)
				clientsRead.GET("/:id", clientHandler.GetClient)
			}
			clientsCreate := protected.Group("/clients")
			clientsCreate.Use(middleware.RequirePermission("clients.create"))
			{
				clientsCreate.POST("", clientHandler.CreateClient)
			}
			clientsUpdate := protected.Group("/clients")
			clientsUpdate.Use(middleware.RequirePermission("clients.update"))
			{
				clientsUpdate.PUT("/:id", clientHandler.UpdateClient)
			}

			// Currencies (any authenticated employee)
			protected.GET("/currencies", accountHandler.ListCurrencies)

			// Account management
			accountsRead := protected.Group("/accounts")
			accountsRead.Use(middleware.RequirePermission("accounts.read"))
			{
				accountsRead.GET("", accountHandler.ListAllAccounts)
				accountsRead.GET("/:id", accountHandler.GetAccount)
				accountsRead.GET("/by-number/:account_number", accountHandler.GetAccountByNumber)
			}
			accountsCreate := protected.Group("/accounts")
			accountsCreate.Use(middleware.RequirePermission("accounts.create"))
			{
				accountsCreate.POST("", accountHandler.CreateAccount)
			}
			accountsUpdate := protected.Group("/accounts")
			accountsUpdate.Use(middleware.RequirePermission("accounts.update"))
			{
				accountsUpdate.PUT("/:id/name", accountHandler.UpdateAccountName)
				accountsUpdate.PUT("/:id/limits", accountHandler.UpdateAccountLimits)
				accountsUpdate.PUT("/:id/status", accountHandler.UpdateAccountStatus)
			}

			// Companies
			companiesEmployee := protected.Group("/companies")
			companiesEmployee.Use(middleware.RequirePermission("accounts.create"))
			{
				companiesEmployee.POST("", accountHandler.CreateCompany)
			}

			// Bank account management (admin only)
			bankAccountsAdmin := protected.Group("/bank-accounts")
			bankAccountsAdmin.Use(middleware.RequirePermission("bank-accounts.manage"))
			{
				bankAccountsAdmin.GET("", accountHandler.ListBankAccounts)
				bankAccountsAdmin.POST("", accountHandler.CreateBankAccount)
				bankAccountsAdmin.DELETE("/:id", accountHandler.DeleteBankAccount)
			}

			// Cards management
			cardsRead := protected.Group("/cards")
			cardsRead.Use(middleware.RequirePermission("cards.read"))
			{
				cardsRead.GET("", cardHandler.ListCards)
				cardsRead.GET("/:id", cardHandler.GetCard)
			}
			cardsCreate := protected.Group("/cards")
			cardsCreate.Use(middleware.RequirePermission("cards.create"))
			{
				cardsCreate.POST("", cardHandler.CreateCard)
				cardsCreate.POST("/authorized-person", cardHandler.CreateAuthorizedPerson)
			}
			cardsUpdate := protected.Group("/cards")
			cardsUpdate.Use(middleware.RequirePermission("cards.update"))
			{
				cardsUpdate.POST("/:id/block", cardHandler.BlockCard)
				cardsUpdate.POST("/:id/unblock", cardHandler.UnblockCard)
				cardsUpdate.POST("/:id/deactivate", cardHandler.DeactivateCard)
			}

			// Card requests (cards.approve)
			cardsApprove := protected.Group("/cards/requests")
			cardsApprove.Use(middleware.RequirePermission("cards.approve"))
			{
				cardsApprove.GET("", cardHandler.ListCardRequests)
				cardsApprove.GET("/:id", cardHandler.GetCardRequest)
				cardsApprove.POST("/:id/approve", cardHandler.ApproveCardRequest)
				cardsApprove.POST("/:id/reject", cardHandler.RejectCardRequest)
			}

			// Payments (employee read)
			paymentsRead := protected.Group("/payments")
			paymentsRead.Use(middleware.RequirePermission("payments.read"))
			{
				paymentsRead.GET("", txHandler.ListPayments)
				paymentsRead.GET("/:id", txHandler.GetPayment)
			}

			// Transfers (employee read)
			transfersRead := protected.Group("/transfers")
			transfersRead.Use(middleware.RequirePermission("payments.read"))
			{
				transfersRead.GET("", txHandler.ListTransfers)
				transfersRead.GET("/:id", txHandler.GetTransfer)
			}

			// Transfer fee management (admin only)
			feesAdmin := protected.Group("/fees")
			feesAdmin.Use(middleware.RequirePermission("fees.manage"))
			{
				feesAdmin.GET("", txHandler.ListFees)
				feesAdmin.POST("", txHandler.CreateFee)
				feesAdmin.PUT("/:id", txHandler.UpdateFee)
				feesAdmin.DELETE("/:id", txHandler.DeleteFee)
			}

			// Loans (employee read)
			loansRead := protected.Group("/loans")
			loansRead.Use(middleware.RequirePermission("credits.read"))
			{
				loansRead.GET("", creditHandler.ListAllLoans)
				loansRead.GET("/:id", creditHandler.GetLoan)
				loansRead.GET("/:id/installments", creditHandler.GetInstallmentsByLoan)
			}

			// Loan requests
			loanRequestsRead := protected.Group("/loan-requests")
			loanRequestsRead.Use(middleware.RequirePermission("credits.read"))
			{
				loanRequestsRead.GET("", creditHandler.ListLoanRequests)
				loanRequestsRead.GET("/:id", creditHandler.GetLoanRequest)
			}
			loanRequestsApprove := protected.Group("/loan-requests")
			loanRequestsApprove.Use(middleware.RequirePermission("credits.approve"))
			{
				loanRequestsApprove.POST("/:id/approve", creditHandler.ApproveLoanRequest)
				loanRequestsApprove.POST("/:id/reject", creditHandler.RejectLoanRequest)
			}

			// Interest rate tier management (admin only)
			rateTiersAdmin := protected.Group("/interest-rate-tiers")
			rateTiersAdmin.Use(middleware.RequirePermission("interest-rates.manage"))
			{
				rateTiersAdmin.GET("", creditHandler.ListInterestRateTiers)
				rateTiersAdmin.POST("", creditHandler.CreateInterestRateTier)
				rateTiersAdmin.PUT("/:id", creditHandler.UpdateInterestRateTier)
				rateTiersAdmin.DELETE("/:id", creditHandler.DeleteInterestRateTier)
				rateTiersAdmin.POST("/:id/apply", creditHandler.ApplyVariableRateUpdate)
			}

			// Bank margin management (admin only)
			bankMarginsAdmin := protected.Group("/bank-margins")
			bankMarginsAdmin.Use(middleware.RequirePermission("interest-rates.manage"))
			{
				bankMarginsAdmin.GET("", creditHandler.ListBankMargins)
				bankMarginsAdmin.PUT("/:id", creditHandler.UpdateBankMargin)
			}

			// Stock exchange management (supervisor)
			stockExchangeAdmin := protected.Group("/stock-exchanges")
			stockExchangeAdmin.Use(middleware.RequirePermission("exchanges.manage"))
			{
				stockExchangeAdmin.POST("/testing-mode", stockExchangeHandler.SetTestingMode)
				stockExchangeAdmin.GET("/testing-mode", stockExchangeHandler.GetTestingMode)
			}

			// Order management (supervisor)
			ordersAdmin := protected.Group("/orders")
			ordersAdmin.Use(middleware.RequirePermission("orders.approve"))
			{
				ordersAdmin.GET("", stockOrderHandler.ListOrders)
				ordersAdmin.POST("/:id/approve", stockOrderHandler.ApproveOrder)
				ordersAdmin.POST("/:id/decline", stockOrderHandler.DeclineOrder)
			}

			// Actuary management (supervisor)
			actuariesAdmin := protected.Group("/actuaries")
			actuariesAdmin.Use(middleware.RequirePermission("agents.manage"))
			{
				actuariesAdmin.GET("", actuaryHandler.ListActuaries)
				actuariesAdmin.PUT("/:id/limit", actuaryHandler.SetActuaryLimit)
				actuariesAdmin.POST("/:id/reset-limit", actuaryHandler.ResetActuaryLimit)
				actuariesAdmin.PUT("/:id/approval", actuaryHandler.SetNeedApproval)
			}

			// Tax management (supervisor)
			taxAdmin := protected.Group("/tax")
			taxAdmin.Use(middleware.RequirePermission("tax.manage"))
			{
				taxAdmin.GET("", taxHandler.ListTaxRecords)
				taxAdmin.POST("/collect", taxHandler.CollectTax)
			}

			// ── v1-only: Changelog endpoints (Plan 2) ───────────────
			// These require the same permission as the parent resource's read.
			changelogAccounts := protected.Group("/accounts")
			changelogAccounts.Use(middleware.RequirePermission("accounts.read"))
			{
				changelogAccounts.GET("/:id/changelog", notImplemented)
			}
			changelogEmployees := protected.Group("/employees")
			changelogEmployees.Use(middleware.RequirePermission("employees.read"))
			{
				changelogEmployees.GET("/:id/changelog", notImplemented)
			}
			changelogClients := protected.Group("/clients")
			changelogClients.Use(middleware.RequirePermission("clients.read"))
			{
				changelogClients.GET("/:id/changelog", notImplemented)
			}
			changelogCards := protected.Group("/cards")
			changelogCards.Use(middleware.RequirePermission("cards.read"))
			{
				changelogCards.GET("/:id/changelog", notImplemented)
			}
			changelogLoans := protected.Group("/loans")
			changelogLoans.Use(middleware.RequirePermission("credits.read"))
			{
				changelogLoans.GET("/:id/changelog", notImplemented)
			}
		}
	}
}
```

- [ ] **1.2** Verify the file compiles.

```bash
cd api-gateway && go build ./internal/router/
```

Expected: no errors. If there are import issues (e.g., handler constructors changed), fix them to match the current constructor signatures in `router.go`.

---

## Task 2: Create `router_latest.go` with `/api/latest` alias

**Files:**
- Create: `api-gateway/internal/router/router_latest.go`

This file registers a single catch-all route that rewrites `/api/latest/...` to `/api/v1/...` and re-dispatches.

### Steps

- [ ] **2.1** Create `api-gateway/internal/router/router_latest.go`.

```go
// api-gateway/internal/router/router_latest.go
package router

import (
	"github.com/gin-gonic/gin"
)

// SetupLatestRoutes registers /api/latest/* as an alias for the highest
// versioned API (/api/v1). When a v2 is added, change the rewrite target.
//
// IMPORTANT: Must be called AFTER SetupV1Routes so that /api/v1/ routes exist.
func SetupLatestRoutes(r *gin.Engine) {
	r.Any("/api/latest/*path", func(c *gin.Context) {
		path := c.Param("path")
		c.Request.URL.Path = "/api/v1" + path
		r.HandleContext(c)
	})
}
```

- [ ] **2.2** Verify the file compiles.

```bash
cd api-gateway && go build ./internal/router/
```

---

## Task 3: Wire v1 and latest routes in `cmd/main.go`

**Files:**
- Modify: `api-gateway/cmd/main.go`

Add two function calls after the existing `router.Setup(...)` call. The existing line 182 is:

```go
r := router.Setup(authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient, empLimitClient, clientLimitClient, virtualCardClient, bankAccountClient, feeClient, cardRequestClient, exchangeClient, stockExchangeClient, securityClient, orderClient, portfolioClient, otcClient, taxClient, actuaryClient, verificationClient, notificationClient, wsHandler)
```

### Steps

- [ ] **3.1** Add v1 and latest route registration after the `router.Setup(...)` call.

Insert immediately after line 182 (the `r := router.Setup(...)` line):

```go
	// Register versioned API routes
	router.SetupV1Routes(r, authClient, userClient, clientClient, accountClient, cardClient, txClient, creditClient, empLimitClient, clientLimitClient, virtualCardClient, bankAccountClient, feeClient, cardRequestClient, exchangeClient, stockExchangeClient, securityClient, orderClient, portfolioClient, otcClient, taxClient, actuaryClient, verificationClient, notificationClient, wsHandler)
	router.SetupLatestRoutes(r)
```

- [ ] **3.2** Verify the full api-gateway builds.

```bash
cd api-gateway && go build -o bin/api-gateway ./cmd
```

---

## Task 4: Add unit tests for router_v1.go and router_latest.go

**Files:**
- Create: `api-gateway/internal/router/router_v1_test.go`
- Create: `api-gateway/internal/router/router_latest_test.go`

These tests verify route registration (not full handler logic -- that is tested in handler tests). They confirm that the v1 router registers routes, the placeholder endpoints return 501, and the `/api/latest` alias correctly rewrites to `/api/v1`.

### Steps

- [ ] **4.1** Create `api-gateway/internal/router/router_v1_test.go`.

```go
// api-gateway/internal/router/router_v1_test.go
package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotImplementedHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", notImplemented)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)

	var body map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	errObj, ok := body["error"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "not_implemented", errObj["code"])
	assert.Contains(t, errObj["message"], "coming in a future release")
}
```

- [ ] **4.2** Create `api-gateway/internal/router/router_latest_test.go`.

This test registers a simple v1 route and verifies that `/api/latest/...` rewrites to it.

```go
// api-gateway/internal/router/router_latest_test.go
package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestLatestRoutesRewriteToV1(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Register a known v1 route
	r.GET("/api/v1/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": "v1"})
	})

	// Register the latest alias
	SetupLatestRoutes(r)

	// Hit /api/latest/ping — should be served by /api/v1/ping
	req := httptest.NewRequest(http.MethodGet, "/api/latest/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "v1")
}

func TestLatestRoutes404ForUnknownPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// No v1 routes registered
	SetupLatestRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/latest/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
```

- [ ] **4.3** Run the tests.

```bash
cd api-gateway && go test ./internal/router/ -v -count=1
```

Expected: all tests pass.

---

## Task 5: Create `docs/api/REST_API_v1.md`

**Files:**
- Create: `docs/api/REST_API_v1.md`

This is a copy of `docs/api/REST_API.md` with all `/api/` paths changed to `/api/v1/` plus new sections for v1-only endpoints. The file is large (~4800 lines), so the steps describe the transformation rather than inlining the full document.

### Steps

- [ ] **5.1** Copy the existing REST API docs.

```bash
cp docs/api/REST_API.md docs/api/REST_API_v1.md
```

- [ ] **5.2** Replace all route paths from `/api/` to `/api/v1/` in the new file.

Use sed or editor find-replace to change all occurrences of `/api/` to `/api/v1/` in `docs/api/REST_API_v1.md`. This includes:
- Route paths in headers (e.g., `### POST /api/auth/login` becomes `### POST /api/v1/auth/login`)
- Route paths in example URLs
- Route paths in description text

Also update the title and header:
```
# EXBanka REST API v1 Documentation
```

Add a note at the top after the base URL:
```markdown
> **Version note:** This documents the `/api/v1/` surface. The unversioned `/api/` routes are
> documented in [REST_API.md](REST_API.md) and remain available for backward compatibility.
> `/api/latest/` is an alias for `/api/v1/`.
```

- [ ] **5.3** Add new sections for v1-only endpoints at the end of the Table of Contents and document body.

Add these sections:

```markdown
## 31. Sessions (new in v1)

### GET /api/v1/me/sessions

List all active sessions for the authenticated user.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Status:** 501 Not Implemented (planned for a future release)

---

### DELETE /api/v1/me/sessions/:id

Revoke a specific session by ID.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | string | Session ID to revoke |

**Status:** 501 Not Implemented (planned for a future release)

---

### DELETE /api/v1/me/sessions

Revoke all sessions except the current one.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Status:** 501 Not Implemented (planned for a future release)

---

## 32. Login History (new in v1)

### GET /api/v1/me/login-history

Get the login history for the authenticated user.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default 1) |
| `page_size` | int | No | Items per page (default 20) |

**Status:** 501 Not Implemented (planned for a future release)

---

## 33. Changelog (new in v1)

### GET /api/v1/accounts/:id/changelog

Get the field-level change history for an account.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `accounts.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Account ID |

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `page` | int | No | Page number (default 1) |
| `page_size` | int | No | Items per page (default 20) |

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/employees/:id/changelog

Get the field-level change history for an employee.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `employees.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Employee ID |

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/clients/:id/changelog

Get the field-level change history for a client.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `clients.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Client ID |

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/cards/:id/changelog

Get the field-level change history for a card.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `cards.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Card ID |

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/loans/:id/changelog

Get the field-level change history for a loan.

**Authentication:** Bearer token (AuthMiddleware)
**Permission:** `credits.read`

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Loan ID |

**Status:** 501 Not Implemented (planned for a future release)

---

## 34. Candle Chart Data (new in v1)

### GET /api/v1/securities/stocks/:id/candles

Get OHLCV candle data for a stock.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Stock security ID |

**Query Parameters:**

| Parameter | Type | Required | Description |
|---|---|---|---|
| `interval` | string | No | Candle interval: `1m`, `5m`, `15m`, `1h`, `1d` (default `1h`) |
| `from` | string | No | Start time (RFC3339) |
| `to` | string | No | End time (RFC3339) |

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/securities/futures/:id/candles

Get OHLCV candle data for a futures contract.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Futures security ID |

**Query Parameters:** Same as stocks candles.

**Status:** 501 Not Implemented (planned for a future release)

---

### GET /api/v1/securities/forex/:id/candles

Get OHLCV candle data for a forex pair.

**Authentication:** Bearer token (AnyAuthMiddleware)

**Path Parameters:**

| Parameter | Type | Description |
|---|---|---|
| `id` | int | Forex pair security ID |

**Query Parameters:** Same as stocks candles.

**Status:** 501 Not Implemented (planned for a future release)
```

- [ ] **5.4** Update the Table of Contents to include sections 31-34.

---

## Task 6: Update Swagger annotations for new v1-only endpoints

**Files:**
- Modify: `api-gateway/internal/router/router_v1.go` (add swagger annotations to `notImplemented` handler)

Since the `notImplemented` handler is shared by all placeholder routes, Swagger needs per-route annotations. The cleanest approach is to create thin wrapper functions for each placeholder so `swag` can parse distinct annotations.

### Steps

- [ ] **6.1** Add named placeholder handlers with Swagger annotations to `router_v1.go`.

Add these functions below the `notImplemented` function in `router_v1.go`:

```go
// @Summary      List active sessions (not yet implemented)
// @Tags         Sessions
// @Security     BearerAuth
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/me/sessions [get]
func v1GetSessions(c *gin.Context) { notImplemented(c) }

// @Summary      Revoke a session (not yet implemented)
// @Tags         Sessions
// @Security     BearerAuth
// @Param        id   path  string  true  "Session ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/me/sessions/{id} [delete]
func v1DeleteSession(c *gin.Context) { notImplemented(c) }

// @Summary      Revoke all sessions (not yet implemented)
// @Tags         Sessions
// @Security     BearerAuth
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/me/sessions [delete]
func v1DeleteAllSessions(c *gin.Context) { notImplemented(c) }

// @Summary      Get login history (not yet implemented)
// @Tags         Sessions
// @Security     BearerAuth
// @Param        page       query  int  false  "Page number"
// @Param        page_size  query  int  false  "Items per page"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/me/login-history [get]
func v1GetLoginHistory(c *gin.Context) { notImplemented(c) }

// @Summary      Get account changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Account ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/accounts/{id}/changelog [get]
func v1GetAccountChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get employee changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Employee ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/employees/{id}/changelog [get]
func v1GetEmployeeChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get client changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Client ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/clients/{id}/changelog [get]
func v1GetClientChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get card changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Card ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/cards/{id}/changelog [get]
func v1GetCardChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get loan changelog (not yet implemented)
// @Tags         Changelog
// @Security     BearerAuth
// @Param        id  path  int  true  "Loan ID"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/loans/{id}/changelog [get]
func v1GetLoanChangelog(c *gin.Context) { notImplemented(c) }

// @Summary      Get stock candle data (not yet implemented)
// @Tags         Securities
// @Security     BearerAuth
// @Param        id        path   int     true   "Stock security ID"
// @Param        interval  query  string  false  "Candle interval (1m, 5m, 15m, 1h, 1d)"
// @Param        from      query  string  false  "Start time (RFC3339)"
// @Param        to        query  string  false  "End time (RFC3339)"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/securities/stocks/{id}/candles [get]
func v1GetStockCandles(c *gin.Context) { notImplemented(c) }

// @Summary      Get futures candle data (not yet implemented)
// @Tags         Securities
// @Security     BearerAuth
// @Param        id        path   int     true   "Futures security ID"
// @Param        interval  query  string  false  "Candle interval (1m, 5m, 15m, 1h, 1d)"
// @Param        from      query  string  false  "Start time (RFC3339)"
// @Param        to        query  string  false  "End time (RFC3339)"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/securities/futures/{id}/candles [get]
func v1GetFuturesCandles(c *gin.Context) { notImplemented(c) }

// @Summary      Get forex candle data (not yet implemented)
// @Tags         Securities
// @Security     BearerAuth
// @Param        id        path   int     true   "Forex pair security ID"
// @Param        interval  query  string  false  "Candle interval (1m, 5m, 15m, 1h, 1d)"
// @Param        from      query  string  false  "Start time (RFC3339)"
// @Param        to        query  string  false  "End time (RFC3339)"
// @Produce      json
// @Success      501  {object}  map[string]interface{}
// @Router       /api/v1/securities/forex/{id}/candles [get]
func v1GetForexCandles(c *gin.Context) { notImplemented(c) }
```

- [ ] **6.2** Update the route registrations in `SetupV1Routes` to use the named functions instead of `notImplemented`.

Replace the placeholder route registrations in the `SetupV1Routes` function body:

```go
			// ── v1-only: Sessions (Plan 3) ──────────────────────────
			me.GET("/sessions", v1GetSessions)
			me.DELETE("/sessions/:id", v1DeleteSession)
			me.DELETE("/sessions", v1DeleteAllSessions)

			// ── v1-only: Login history (Plan 3) ─────────────────────
			me.GET("/login-history", v1GetLoginHistory)
```

```go
			// ── v1-only: Candle chart data (Plan 5) ─────────────────
			securities.GET("/stocks/:id/candles", v1GetStockCandles)
			securities.GET("/futures/:id/candles", v1GetFuturesCandles)
			securities.GET("/forex/:id/candles", v1GetForexCandles)
```

```go
			// ── v1-only: Changelog endpoints (Plan 2) ───────────────
			changelogAccounts := protected.Group("/accounts")
			changelogAccounts.Use(middleware.RequirePermission("accounts.read"))
			{
				changelogAccounts.GET("/:id/changelog", v1GetAccountChangelog)
			}
			changelogEmployees := protected.Group("/employees")
			changelogEmployees.Use(middleware.RequirePermission("employees.read"))
			{
				changelogEmployees.GET("/:id/changelog", v1GetEmployeeChangelog)
			}
			changelogClients := protected.Group("/clients")
			changelogClients.Use(middleware.RequirePermission("clients.read"))
			{
				changelogClients.GET("/:id/changelog", v1GetClientChangelog)
			}
			changelogCards := protected.Group("/cards")
			changelogCards.Use(middleware.RequirePermission("cards.read"))
			{
				changelogCards.GET("/:id/changelog", v1GetCardChangelog)
			}
			changelogLoans := protected.Group("/loans")
			changelogLoans.Use(middleware.RequirePermission("credits.read"))
			{
				changelogLoans.GET("/:id/changelog", v1GetLoanChangelog)
			}
```

- [ ] **6.3** Regenerate swagger docs.

```bash
cd api-gateway && swag init -g cmd/main.go --output docs
```

---

## Task 7: Integration test helpers for versioned base paths

**Files:**
- Modify: `test-app/internal/config/config.go`
- Create: `test-app/workflows/v1_smoke_test.go`

### Steps

- [ ] **7.1** Add `APIVersion` field to test config.

In `test-app/internal/config/config.go`, add:

```go
// Add to Config struct:
APIVersion string // e.g., "v1" or "" for unversioned

// Add to Load():
APIVersion: getEnv("TEST_API_VERSION", ""),
```

- [ ] **7.2** Add `BasePath()` helper method to Config.

```go
// BasePath returns the API base path prefix for the configured version.
// Returns "/api/v1" when APIVersion="v1", "/api" otherwise.
func (c *Config) BasePath() string {
	if c.APIVersion != "" {
		return "/api/" + c.APIVersion
	}
	return "/api"
}
```

- [ ] **7.3** Create `test-app/workflows/v1_smoke_test.go`.

This test verifies that the v1 routes are reachable and placeholder endpoints return 501.

```go
// test-app/workflows/v1_smoke_test.go
package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestV1_PublicRoutes_Reachable verifies that public v1 routes respond
// (not 404), confirming the v1 router is wired correctly.
func TestV1_PublicRoutes_Reachable(t *testing.T) {
	c := newClient(t)

	// Exchange rates should return 200 on v1
	resp, err := c.GET("/api/v1/exchange/rates")
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode, "GET /api/v1/exchange/rates should be reachable")
}

// TestV1_PlaceholderEndpoints_Return501 verifies that new v1-only endpoints
// return 501 Not Implemented until their backing plans are implemented.
func TestV1_PlaceholderEndpoints_Return501(t *testing.T) {
	c := loginAsAdmin(t)

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/accounts/1/changelog"},
		{"GET", "/api/v1/employees/1/changelog"},
		{"GET", "/api/v1/clients/1/changelog"},
		{"GET", "/api/v1/cards/1/changelog"},
		{"GET", "/api/v1/loans/1/changelog"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var resp *Response
			var err error
			switch tt.method {
			case "GET":
				resp, err = c.GET(tt.path)
			case "DELETE":
				resp, err = c.DELETE(tt.path)
			}
			require.NoError(t, err)
			assert.Equal(t, 501, resp.StatusCode, "%s %s should return 501", tt.method, tt.path)
		})
	}
}

// TestV1_MePlaceholders_Return501 verifies that /me session/login-history
// placeholders return 501.
func TestV1_MePlaceholders_Return501(t *testing.T) {
	c := loginAsClient(t)

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/me/sessions"},
		{"GET", "/api/v1/me/login-history"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp, err := c.GET(tt.path)
			require.NoError(t, err)
			assert.Equal(t, 501, resp.StatusCode, "%s %s should return 501", tt.method, tt.path)
		})
	}
}

// TestV1_LatestAlias_Rewrites verifies that /api/latest/* aliases to /api/v1/*.
func TestV1_LatestAlias_Rewrites(t *testing.T) {
	c := newClient(t)

	// Both should return the same result
	v1Resp, err := c.GET("/api/v1/exchange/rates")
	require.NoError(t, err)

	latestResp, err := c.GET("/api/latest/exchange/rates")
	require.NoError(t, err)

	assert.Equal(t, v1Resp.StatusCode, latestResp.StatusCode,
		"/api/latest should return same status as /api/v1")
}

// TestV1_SecuritiesCandlePlaceholders_Return501 verifies candle endpoints.
func TestV1_SecuritiesCandlePlaceholders_Return501(t *testing.T) {
	c := loginAsAdmin(t)

	tests := []string{
		"/api/v1/securities/stocks/1/candles",
		"/api/v1/securities/futures/1/candles",
		"/api/v1/securities/forex/1/candles",
	}

	for _, path := range tests {
		t.Run("GET "+path, func(t *testing.T) {
			resp, err := c.GET(path)
			require.NoError(t, err)
			assert.Equal(t, 501, resp.StatusCode, "GET %s should return 501", path)
		})
	}
}
```

**Note:** The helper functions `newClient`, `loginAsAdmin`, `loginAsClient`, and the `Response` type are expected to exist in `test-app/workflows/helpers_test.go`. Adjust the test to match actual helper signatures.

- [ ] **7.4** Run the integration test (requires running services).

```bash
cd test-app && go test ./workflows/ -v -run TestV1_ -count=1
```

---

## Task 8: Update `Specification.md`

**Files:**
- Modify: `Specification.md` (repo root)

### Steps

- [ ] **8.1** Add a new section to the Specification documenting the API versioning scheme.

Add to the appropriate location (e.g., a new Section 22 or append to the API routes section):

```markdown
## API Versioning

The API gateway supports versioned routes alongside the original unversioned routes:

| Prefix | Description |
|---|---|
| `/api/` | Original unversioned routes (frozen, backward-compatible) |
| `/api/v1/` | Version 1 routes (mirrors `/api/` plus new endpoints) |
| `/api/latest/` | Alias that rewrites to the highest version (`/api/v1/`) |

### v1-only endpoints

These endpoints exist only under `/api/v1/` and are not available on the unversioned `/api/`:

| Method | Path | Status | Plan |
|---|---|---|---|
| GET | /api/v1/me/sessions | 501 placeholder | Plan 3 |
| DELETE | /api/v1/me/sessions/:id | 501 placeholder | Plan 3 |
| DELETE | /api/v1/me/sessions | 501 placeholder | Plan 3 |
| GET | /api/v1/me/login-history | 501 placeholder | Plan 3 |
| GET | /api/v1/accounts/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/employees/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/clients/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/cards/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/loans/:id/changelog | 501 placeholder | Plan 2 |
| GET | /api/v1/securities/stocks/:id/candles | 501 placeholder | Plan 5 |
| GET | /api/v1/securities/futures/:id/candles | 501 placeholder | Plan 5 |
| GET | /api/v1/securities/forex/:id/candles | 501 placeholder | Plan 5 |
```

- [ ] **8.2** Add the new REST API v1 docs file to the docs index if one exists.

---

## Summary of new and modified files

| Action | File |
|---|---|
| Create | `api-gateway/internal/router/router_v1.go` |
| Create | `api-gateway/internal/router/router_latest.go` |
| Create | `api-gateway/internal/router/router_v1_test.go` |
| Create | `api-gateway/internal/router/router_latest_test.go` |
| Modify | `api-gateway/cmd/main.go` (2 lines added) |
| Create | `docs/api/REST_API_v1.md` |
| Create | `test-app/workflows/v1_smoke_test.go` |
| Modify | `test-app/internal/config/config.go` |
| Modify | `Specification.md` |
| Regenerate | `api-gateway/docs/` (swagger) |
| **Frozen** | `api-gateway/internal/router/router.go` (NOT modified) |

## Verification checklist

After all tasks are complete, run these commands to verify correctness:

```bash
# 1. Build compiles cleanly
cd api-gateway && go build -o bin/api-gateway ./cmd

# 2. Unit tests pass
cd api-gateway && go test ./internal/router/ -v -count=1

# 3. Full test suite passes
make test

# 4. Integration tests (requires running services)
cd test-app && go test ./workflows/ -v -run TestV1_ -count=1

# 5. Swagger regenerated
cd api-gateway && swag init -g cmd/main.go --output docs
```
