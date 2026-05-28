# Route Inventory — api-gateway /api/v3 — 2026-05-28

Source: `api-gateway/internal/router/router_v3.go`  
Permission strings: `contract/permissions/perms.gen.go`

## 🔒 Frozen routes — do NOT modify

Routes that implement the inter-bank protocol (Celina-5, SI-TX-Proto generation 2024/25 at https://arsen.srht.site/si-tx-proto/) must match the spec exactly so the four cohort banks can interop. The path, verb, auth mechanism, and response shape on these routes are **frozen** — even when they violate REST conventions (e.g. `GET /negotiations/:rid/:id/accept` is intentional and must stay GET).

Frozen wire-facing routes (peer-authenticated):
- `POST /api/v3/interbank` (NEW_TX / COMMIT_TX / ROLLBACK_TX envelopes)
- `GET /api/v3/interbank/:transaction_id/status` (CHECK_STATUS)
- `GET /api/v3/public-stock`
- `GET /api/v3/public-option-offers`
- `POST /api/v3/negotiations`
- `PUT /api/v3/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id`
- `DELETE /api/v3/negotiations/:rid/:id`
- `GET /api/v3/negotiations/:rid/:id/accept`
- `GET /api/v3/user/:rid/:id`

Touchy (local routes that drive the wire protocol — outbound calls match the spec):
- All `/api/v3/me/peer-otc/*` initiate routes
- `/api/v3/peer-banks` admin registration routes

In the inventory tables below these rows are marked **🔒 FROZEN** in the Notes column. The "Inconsistencies and questions" section also strikes out items that touch frozen routes — any "fix" to those would break inter-bank interop.

---

## 1. Bottom-line counts

| Metric | Count |
|---|---|
| **Total routes** | **162** |
| GET | 80 |
| POST | 59 |
| PUT | 18 |
| DELETE | 14 |
| PATCH | 0 |

| Domain | Routes |
|---|---|
| Auth (login/refresh/logout/reset/activate) | 7 |
| Exchange rates (public) | 3 |
| /me — Accounts | 3 |
| /me — Cards | 8 |
| /me — Payments | 4 |
| /me — Transfers | 6 |
| /me — Payment recipients | 4 |
| /me — Loans | 5 |
| /me — Stock orders | 4 |
| /me — Portfolio / Holdings | 4 |
| /me — Tax | 1 |
| /me — Watchlist | 3 |
| /me — Price alerts | 5 |
| /me — Recurring orders | 6 |
| /me — Recurring fund investments | 6 |
| /me — Sessions / Login history | 4 |
| /me — Notifications | 4 |
| /me — Investment funds | 1 |
| /me — OTC options (offers + negotiations) | 11 |
| /me — OTC stocks | 3 |
| /me — Peer OTC negotiations | 5 |
| /me — General | 1 |
| Interbank / Peer-authenticated (SI-TX) | 8 |
| Stock exchanges | 4 |
| Securities | 11 |
| OTC stocks (public browse + trade) | 3 |
| OTC options (read + trade) | 5 |
| Mobile auth (public) | 3 |
| Mobile device management | 3 |
| Mobile device settings | 2 |
| Mobile verifications | 4 |
| QR verification | 1 |
| Browser verifications | 3 |
| Employees | 4 |
| Roles / Permissions | 7 |
| Employee limits | 3 |
| Limit templates | 2 |
| Blueprints | 5 |
| Client limits | 2 |
| Clients | 4 |
| Client sub-collections (accounts/payments/transfers/loans/cards) | 5 |
| Currencies | 1 |
| Accounts | 8 |
| Companies | 1 |
| Bank accounts | 3 |
| Notification templates | 4 |
| Peer banks admin | 5 |
| Cards (employee) | 8 |
| Card requests | 5 |
| Payments (employee) | 1 |
| Transfers (employee) | 1 |
| Fees | 4 |
| Loans (employee) | 3 |
| Loan requests | 4 |
| Interest rate tiers | 5 |
| Bank margins | 2 |
| Stock exchange management | 2 |
| Stock sources | 2 |
| Orders (employee) | 4 |
| Actuaries | 5 |
| Tax (employee) | 2 |
| Changelogs | 5 |
| Unified portfolio (employee) | 4 |
| Watchlist by portfolio_id (employee) | 1 |
| Investment funds (employee manage + bank positions) | 3 |
| Investment funds (AnyAuth browse + invest/redeem) | 4 |
| Options v2 (by option_id) | 2 |
| Admin crons | 5 |

| Auth class | Routes |
|---|---|
| None (public) | 13 |
| AnyAuth (client or employee token) | ~75 |
| Auth (employee token only) | ~66 |
| Peer (X-Api-Key / HMAC) | 8 |

| Route shape | Count |
|---|---|
| /me/... (caller-owns) | ~87 |
| Non-/me | ~75 |

---

## 2. Route inventory by domain

### 2.1 Auth (public — no middleware)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/auth/login | none | | h.Auth.Login | |
| POST | /api/v3/auth/refresh | none | | h.Auth.RefreshToken | |
| POST | /api/v3/auth/logout | none | | h.Auth.Logout | |
| POST | /api/v3/auth/password/reset-request | none | | h.Auth.RequestPasswordReset | |
| POST | /api/v3/auth/password/reset | none | | h.Auth.ResetPassword | |
| POST | /api/v3/auth/activate | none | | h.Auth.ActivateAccount | |
| POST | /api/v3/auth/resend-activation | none | | h.Auth.ResendActivationEmail | |

### 2.2 Exchange rates (public — no middleware)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/exchange/rates | none | | h.Exchange.ListExchangeRates | |
| GET | /api/v3/exchange/rates/:from/:to | none | | h.Exchange.GetExchangeRate | |
| POST | /api/v3/exchange/calculate | none | | h.Exchange.CalculateExchange | |

### 2.3 /me — General

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me | AnyAuth + RequireClientToken | | h.Me.GetMe | Extra RequireClientToken middleware — only clients, not employees |

### 2.4 /me — Accounts

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/accounts | AnyAuth | | h.Account.ListMyAccounts | |
| GET | /api/v3/me/accounts/:id | AnyAuth | | h.Account.GetMyAccount | :id = account_id |
| GET | /api/v3/me/accounts/:id/activity | AnyAuth | | h.Account.GetMyAccountActivity | :id = account_id |

### 2.5 /me — Cards

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/cards | AnyAuth | | h.Card.ListMyCards | |
| GET | /api/v3/me/cards/:id | AnyAuth | | h.Card.GetMyCard | |
| POST | /api/v3/me/cards/:id/pin | AnyAuth + RequireClientToken | | h.Card.SetCardPin | Client-only |
| POST | /api/v3/me/cards/:id/verify-pin | AnyAuth + RequireClientToken | | h.Card.VerifyCardPin | Client-only |
| POST | /api/v3/me/cards/:id/temporary-block | AnyAuth + RequireClientToken | | h.Card.TemporaryBlockCard | Client-only |
| POST | /api/v3/me/cards/virtual | AnyAuth | | h.Card.CreateVirtualCard | No RequireClientToken — employees can call |
| POST | /api/v3/me/cards/requests | AnyAuth + RequireClientToken | | h.Card.CreateCardRequest | |
| GET | /api/v3/me/cards/requests | AnyAuth + RequireClientToken | | h.Card.ListMyCardRequests | |

### 2.6 /me — Payments

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/payments | AnyAuth | | h.Tx.CreatePayment | |
| GET | /api/v3/me/payments | AnyAuth | | h.Tx.ListMyPayments | |
| GET | /api/v3/me/payments/:id | AnyAuth | | h.Tx.GetMyPayment | |
| POST | /api/v3/me/payments/:id/execute | AnyAuth | | h.Tx.ExecutePayment | |

### 2.7 /me — Transfers

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/transfers | AnyAuth | | h.PeerTxDispatcher.CreateTransfer | Cross-bank dispatcher, not h.Tx |
| POST | /api/v3/me/transfers/preview | AnyAuth | | h.Tx.PreviewTransfer | |
| GET | /api/v3/me/transfers | AnyAuth | | h.Tx.ListMyTransfers | |
| GET | /api/v3/me/transfers/:id | AnyAuth | | h.PeerTxDispatcher.GetTransferByID | Also via dispatcher |
| GET | /api/v3/me/transfers/:id/status | AnyAuth | | h.Tx.GetMyTransferStatus | |
| POST | /api/v3/me/transfers/:id/execute | AnyAuth | | h.Tx.ExecuteTransfer | |

### 2.8 /me — Payment recipients

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/payment-recipients | AnyAuth | | h.Tx.CreateMyPaymentRecipient | |
| GET | /api/v3/me/payment-recipients | AnyAuth | | h.Tx.ListMyPaymentRecipients | |
| PUT | /api/v3/me/payment-recipients/:id | AnyAuth | | h.Tx.UpdatePaymentRecipient | |
| DELETE | /api/v3/me/payment-recipients/:id | AnyAuth | | h.Tx.DeletePaymentRecipient | |

### 2.9 /me — Loans

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/loan-requests | AnyAuth | | h.Credit.CreateLoanRequest | |
| GET | /api/v3/me/loan-requests | AnyAuth | | h.Credit.ListMyLoanRequests | |
| GET | /api/v3/me/loans | AnyAuth | | h.Credit.ListMyLoans | |
| GET | /api/v3/me/loans/:id | AnyAuth | | h.Credit.GetMyLoan | |
| GET | /api/v3/me/loans/:id/installments | AnyAuth | | h.Credit.GetMyInstallments | |

### 2.10 /me — Stock orders

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/orders | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.StockOrder.CreateOrder | |
| GET | /api/v3/me/orders | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.StockOrder.ListMyOrders | |
| GET | /api/v3/me/orders/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.StockOrder.GetMyOrder | |
| POST | /api/v3/me/orders/:id/cancel | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.StockOrder.CancelOrder | |

### 2.11 /me — Portfolio and Holdings

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/portfolio | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.UnifiedPortfolio.GetMy | Unified portfolio |
| GET | /api/v3/me/portfolio/summary | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Portfolio.GetPortfolioSummary | Legacy summary view |
| POST | /api/v3/me/portfolio/:id/exercise | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Portfolio.ExerciseOption | :id = holding_id |
| GET | /api/v3/me/holdings/:id/transactions | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Portfolio.ListHoldingTransactions | Different resource prefix from portfolio |

### 2.12 /me — Tax

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/tax | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Tax.ListMyTaxRecords | |

### 2.13 /me — Watchlist

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/watchlist | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Watchlist.ListMy | |
| POST | /api/v3/me/watchlist | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Watchlist.AddItem | |
| DELETE | /api/v3/me/watchlist/:listing_id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Watchlist.RemoveItem | :listing_id |

### 2.14 /me — Price alerts

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/price-alerts | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.PriceAlert.ListMy | |
| POST | /api/v3/me/price-alerts | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.PriceAlert.Create | |
| GET | /api/v3/me/price-alerts/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.PriceAlert.Get | |
| PUT | /api/v3/me/price-alerts/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.PriceAlert.Update | |
| DELETE | /api/v3/me/price-alerts/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.PriceAlert.Delete | |

### 2.15 /me — Recurring securities orders

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/recurring-orders | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.ListMy | |
| POST | /api/v3/me/recurring-orders | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.Create | |
| GET | /api/v3/me/recurring-orders/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.Get | |
| POST | /api/v3/me/recurring-orders/:id/pause | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.Pause | |
| POST | /api/v3/me/recurring-orders/:id/resume | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.Resume | |
| POST | /api/v3/me/recurring-orders/:id/cancel | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.RecurringOrder.Cancel | |

### 2.16 /me — Recurring fund investments

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/recurring-funds | AnyAuth | | h.RecurringFund.ListMy | No ResolveIdentity unlike recurring-orders |
| POST | /api/v3/me/recurring-funds | AnyAuth | | h.RecurringFund.Create | No ResolveIdentity |
| GET | /api/v3/me/recurring-funds/:id | AnyAuth | | h.RecurringFund.Get | No ResolveIdentity |
| POST | /api/v3/me/recurring-funds/:id/pause | AnyAuth | | h.RecurringFund.Pause | No ResolveIdentity |
| POST | /api/v3/me/recurring-funds/:id/resume | AnyAuth | | h.RecurringFund.Resume | No ResolveIdentity |
| DELETE | /api/v3/me/recurring-funds/:id | AnyAuth | | h.RecurringFund.Cancel | DELETE verb for cancel; recurring-orders uses POST /:id/cancel |

### 2.17 /me — Sessions and login history

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/sessions | AnyAuth | | h.Session.ListMySessions | |
| DELETE | /api/v3/me/sessions/:id | AnyAuth | | h.Session.RevokeSession | |
| POST | /api/v3/me/sessions/revoke-others | AnyAuth | | h.Session.RevokeAllSessions | Name says "others" but handler says "all" |
| GET | /api/v3/me/login-history | AnyAuth | | h.Session.GetMyLoginHistory | |

### 2.18 /me — Notifications

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/notifications | AnyAuth | | h.Notification.ListNotifications | |
| GET | /api/v3/me/notifications/unread-count | AnyAuth | | h.Notification.GetUnreadCount | |
| POST | /api/v3/me/notifications/read-all | AnyAuth | | h.Notification.MarkAllRead | |
| POST | /api/v3/me/notifications/:id/read | AnyAuth | | h.Notification.MarkRead | |

### 2.19 /me — Investment fund positions

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/investment-funds | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Fund.ListMyPositions | Lists caller's fund positions, not the fund catalog |

### 2.20 /me — OTC options (offers + negotiations)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/otc/options | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Portfolio.ListMyOTCOptions | Uses Portfolio handler, not OTCOptions |
| GET | /api/v3/me/otc/options/posted | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListMyPostedOffers | |
| POST | /api/v3/me/otc/options | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.CreateOffer | |
| GET | /api/v3/me/otc/history | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListNegotiationHistory | |
| POST | /api/v3/me/otc/ratings | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.SubmitRating | |
| GET | /api/v3/me/otc/ratings/received | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListMyReceivedRatings | |
| GET | /api/v3/me/otc/contracts | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListMyContracts | |
| GET | /api/v3/me/otc/options/negotiations | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListMyNegotiations | |
| POST | /api/v3/me/otc/options/:id/negotiations/:nid/counter | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.CounterMyNegotiation | |
| POST | /api/v3/me/otc/options/:id/negotiations/:nid/accept | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.AcceptMyNegotiation | |
| POST | /api/v3/me/otc/options/:id/negotiations/:nid/reject | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.RejectMyNegotiation | |
| DELETE | /api/v3/me/otc/options/:id/negotiations/:nid | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.CancelMyNegotiation | DELETE for cancel |
| DELETE | /api/v3/me/otc/options/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.CancelMyListing | DELETE for cancel listing |

### 2.21 /me — OTC stocks

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/me/otc/stocks | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCStock.ListMyOTCStocks | |
| POST | /api/v3/me/otc/stocks | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCStock.CreateOTCStockOffer | |
| DELETE | /api/v3/me/otc/stocks/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCStock.CancelOTCStockOffer | DELETE for cancel |

### 2.22 /me — Peer OTC negotiations

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/peer-otc/negotiations | AnyAuth | | h.PeerOTCInitiate.CreatePeerNegotiation | 🔒 TOUCHY — drives wire protocol; no ResolveIdentity unlike most OTC /me routes |
| GET | /api/v3/me/peer-otc/negotiations | AnyAuth | | h.PeerOTCInitiate.ListMyPeerNegotiations | 🔒 TOUCHY — drives wire protocol |
| PUT | /api/v3/me/peer-otc/negotiations/:rid/:id | AnyAuth | | h.PeerOTCInitiate.CounterPeerNegotiation | 🔒 TOUCHY — drives wire protocol; PUT for counter is intentional |
| POST | /api/v3/me/peer-otc/negotiations/:rid/:id/accept | AnyAuth | | h.PeerOTCInitiate.AcceptPeerNegotiation | 🔒 TOUCHY — drives wire protocol |
| DELETE | /api/v3/me/peer-otc/negotiations/:rid/:id | AnyAuth | | h.PeerOTCInitiate.CancelPeerNegotiation | 🔒 TOUCHY — drives wire protocol |

### 2.23 /me — OTC contract peer exercise

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/me/otc/contracts/peer/:id/exercise | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ExercisePeerContract | |

### 2.24 Interbank / Peer-authenticated routes (SI-TX)

All routes in this group use `h.PeerAuthMW` (X-Api-Key / HMAC). Group prefix is bare `/api/v3` (empty string group).

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/interbank | Peer | | h.PeerTx.PostInterbank | 🔒 FROZEN — SI-TX-Proto |
| GET | /api/v3/interbank/:transaction_id/status | Peer | | h.PeerTxStatus.GetTxStatus | 🔒 FROZEN — CHECK_STATUS per spec |
| GET | /api/v3/public-stock | Peer | | h.PeerOTC.GetPublicStocks | 🔒 FROZEN — SI-TX-Proto |
| GET | /api/v3/public-option-offers | Peer | | h.PeerOTC.GetPublicOptionOffers | 🔒 FROZEN — SI-TX-Proto |
| POST | /api/v3/negotiations | Peer | | h.PeerOTC.CreateNegotiation | 🔒 FROZEN — top-level /negotiations per spec, not under /otc |
| PUT | /api/v3/negotiations/:rid/:id | Peer | | h.PeerOTC.UpdateNegotiation | 🔒 FROZEN — SI-TX-Proto |
| GET | /api/v3/negotiations/:rid/:id | Peer | | h.PeerOTC.GetNegotiation | 🔒 FROZEN — SI-TX-Proto |
| DELETE | /api/v3/negotiations/:rid/:id | Peer | | h.PeerOTC.DeleteNegotiation | 🔒 FROZEN — SI-TX-Proto |
| GET | /api/v3/negotiations/:rid/:id/accept | Peer | | h.PeerOTC.AcceptNegotiation | 🔒 FROZEN — GET for accept is per spec; do not change |
| GET | /api/v3/user/:rid/:id | Peer | | h.PeerUser.GetUser | 🔒 FROZEN — SI-TX-Proto peer user lookup |

### 2.25 Stock exchanges

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/stock-exchanges | AnyAuth | | h.StockExchange.ListExchanges | |
| GET | /api/v3/stock-exchanges/:id | AnyAuth | | h.StockExchange.GetExchange | |
| GET | /api/v3/stock-exchanges/testing-mode | Auth | securities.manage.catalog | h.StockExchange.GetTestingMode | Static segment collides with /:id above in different groups |
| POST | /api/v3/stock-exchanges/testing-mode | Auth | securities.manage.catalog | h.StockExchange.SetTestingMode | |

### 2.26 Securities (market data)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/securities/stocks | AnyAuth | | h.Securities.ListStocks | |
| GET | /api/v3/securities/stocks/:id | AnyAuth | | h.Securities.GetStock | |
| GET | /api/v3/securities/stocks/:id/history | AnyAuth | | h.Securities.GetStockHistory | |
| GET | /api/v3/securities/futures | AnyAuth | | h.Securities.ListFutures | |
| GET | /api/v3/securities/futures/:id | AnyAuth | | h.Securities.GetFutures | |
| GET | /api/v3/securities/futures/:id/history | AnyAuth | | h.Securities.GetFuturesHistory | |
| GET | /api/v3/securities/forex | AnyAuth | | h.Securities.ListForexPairs | |
| GET | /api/v3/securities/forex/:id | AnyAuth | | h.Securities.GetForexPair | |
| GET | /api/v3/securities/forex/:id/history | AnyAuth | | h.Securities.GetForexPairHistory | |
| GET | /api/v3/securities/options | AnyAuth | | h.Securities.ListOptions | |
| GET | /api/v3/securities/options/:id | AnyAuth | | h.Securities.GetOption | |
| GET | /api/v3/securities/candles | AnyAuth | | h.Securities.GetCandles | Query-param based, no :id |

### 2.27 OTC stocks (public browse + authenticated trade)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/otc/stocks | AnyAuth | | h.Portfolio.ListOTCOffers | Uses Portfolio handler |
| POST | /api/v3/otc/stocks/:id/buy | AnyAuth + RequirePermissionOrClient(PermAny, otc.trade.accept, securities.trade.any) + ResolveIdentity | | h.Portfolio.BuyOTCOffer | |
| POST | /api/v3/otc/stocks/:id/sell | AnyAuth + RequirePermissionOrClient(PermAny, otc.trade.accept, securities.trade.any) + ResolveIdentity | | h.OTCStock.SellOTCStockOffer | |
| POST | /api/v3/otc/stocks/:id/buy-on-behalf | Auth + RequireAnyPermission(otc.trade.accept, otc.trade.on_behalf) + ResolveIdentity | | h.Portfolio.BuyOTCOfferOnBehalf | Employee-only; in protected group |

### 2.28 OTC options (public read + authenticated trade)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/otc/options | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.Portfolio.ListOTCOptions | Uses Portfolio handler |
| GET | /api/v3/otc/options/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.GetOffer | |
| GET | /api/v3/otc/contracts/:id | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.GetContract | |
| GET | /api/v3/otc/traders/:owner_type/:owner_id/rating | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.GetTraderProfile | |
| GET | /api/v3/otc/options/:id/negotiations | AnyAuth + ResolveIdentity(OwnerIsBankIfEmployee) | | h.OTCOptions.ListNegotiationsOnListing | |
| POST | /api/v3/otc/contracts/:id/exercise | AnyAuth + RequirePermissionOrClient(PermAll, securities.trade.any, otc.trade.accept) + ResolveIdentity | | h.OTCOptions.ExerciseContract | |
| POST | /api/v3/otc/options/:id/bid | AnyAuth + RequirePermissionOrClient(PermAll, securities.trade.any, otc.trade.accept) + ResolveIdentity | | h.OTCOptions.OpenNegotiationChain | |

### 2.29 Mobile auth (public)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/mobile/auth/request-activation | none | | h.MobileAuth.RequestActivation | |
| POST | /api/v3/mobile/auth/activate | none | | h.MobileAuth.ActivateDevice | |
| POST | /api/v3/mobile/auth/refresh | none | | h.MobileAuth.RefreshMobileToken | |

### 2.30 Mobile device management

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/mobile/device | MobileAuth | | h.MobileAuth.GetDeviceInfo | |
| POST | /api/v3/mobile/device/deactivate | MobileAuth | | h.MobileAuth.DeactivateDevice | |
| POST | /api/v3/mobile/device/transfer | MobileAuth | | h.MobileAuth.TransferDevice | |

### 2.31 Mobile device settings

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/mobile/device/biometrics | MobileAuth + RequireDeviceSignature | | h.MobileAuth.SetBiometrics | |
| GET | /api/v3/mobile/device/biometrics | MobileAuth + RequireDeviceSignature | | h.MobileAuth.GetBiometrics | |

### 2.32 Mobile verifications

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/mobile/verifications/pending | MobileAuth + RequireDeviceSignature | | h.Verification.GetPendingVerifications | |
| POST | /api/v3/mobile/verifications/:id/submit | MobileAuth + RequireDeviceSignature | | h.Verification.SubmitMobileVerification | |
| POST | /api/v3/mobile/verifications/:id/ack | MobileAuth + RequireDeviceSignature | | h.Verification.AckVerification | |
| POST | /api/v3/mobile/verifications/:id/biometric | MobileAuth + RequireDeviceSignature | | h.Verification.BiometricVerify | |

### 2.33 QR verification

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/verify/:challenge_id | MobileAuth + RequireDeviceSignature | | h.Verification.VerifyQR | Route prefix /verify, not /verifications |

### 2.34 Browser-facing verifications

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/verifications | AnyAuth | | h.Verification.CreateVerification | |
| GET | /api/v3/verifications/:id/status | AnyAuth | | h.Verification.GetVerificationStatus | |
| POST | /api/v3/verifications/:id/code | AnyAuth | | h.Verification.SubmitVerificationCode | |

### 2.35 Employees

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/employees | Auth | employees.read.all | h.Employee.ListEmployees | |
| GET | /api/v3/employees/:id | Auth | employees.read.all | h.Employee.GetEmployee | |
| POST | /api/v3/employees | Auth | employees.create.any | h.Employee.CreateEmployee | |
| PUT | /api/v3/employees/:id | Auth | employees.update.any | h.Employee.UpdateEmployee | |

Note: `employees.deactivate.any` exists in the catalog but there is no DELETE or POST deactivate route for employees in the router.

### 2.36 Roles and permissions

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/roles | Auth | roles.read.all | h.Role.ListRoles | |
| GET | /api/v3/roles/:id | Auth | roles.read.all | h.Role.GetRole | |
| GET | /api/v3/permissions | Auth | roles.read.all | h.Role.ListPermissions | |
| POST | /api/v3/roles | Auth | roles.update.any | h.Role.CreateRole | POST for create gated by update.any — no create.any perm exists |
| PUT | /api/v3/roles/:id/permissions | Auth | roles.permissions.assign OR roles.permissions.revoke OR roles.update.any | h.Role.UpdateRolePermissions | Bulk replace; multiple perms accepted |
| POST | /api/v3/roles/:id/permissions | Auth | roles.permissions.assign | h.Role.AssignPermissionToRole | Granular add |
| DELETE | /api/v3/roles/:id/permissions/:permission | Auth | roles.permissions.revoke | h.Role.RevokePermissionFromRole | |

### 2.37 Per-employee role and permission assignment

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| PUT | /api/v3/employees/:id/roles | Auth | employees.roles.assign OR employees.permissions.assign | h.Role.SetEmployeeRoles | |
| PUT | /api/v3/employees/:id/permissions | Auth | employees.roles.assign OR employees.permissions.assign | h.Role.SetEmployeeAdditionalPermissions | Same RequireAnyPermission gate for both |

### 2.38 Employee limits

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/employees/:id/limits | Auth | limits.employee.read | h.Limit.GetEmployeeLimits | |
| PUT | /api/v3/employees/:id/limits | Auth | limits.employee.update | h.Limit.SetEmployeeLimits | |
| POST | /api/v3/employees/:id/limits/template | Auth | limits.employee.update | h.Limit.ApplyLimitTemplate | |

### 2.39 Limit templates

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/limits/templates | Auth | limit_templates.create.any OR limit_templates.update.any | h.Limit.ListLimitTemplates | Read gated by create-or-update, not a read perm |
| POST | /api/v3/limits/templates | Auth | limit_templates.create.any | h.Limit.CreateLimitTemplate | |

### 2.40 Client limits

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/clients/:id/limits | Auth | clients.update.limits | h.Limit.GetClientLimits | Read gated by update perm; :id = client_id |
| PUT | /api/v3/clients/:id/limits | Auth | clients.update.limits | h.Limit.SetClientLimits | |

### 2.41 Blueprints

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/blueprints | Auth | limit_templates.create.any OR limit_templates.update.any | h.Blueprint.ListBlueprints | Uses limit_templates perms for a "blueprints" resource |
| GET | /api/v3/blueprints/:id | Auth | limit_templates.create.any OR limit_templates.update.any | h.Blueprint.GetBlueprint | |
| POST | /api/v3/blueprints | Auth | limit_templates.create.any | h.Blueprint.CreateBlueprint | |
| PUT | /api/v3/blueprints/:id | Auth | limit_templates.update.any | h.Blueprint.UpdateBlueprint | |
| POST | /api/v3/blueprints/:id/apply | Auth | limit_templates.update.any | h.Blueprint.ApplyBlueprint | |
| DELETE | /api/v3/blueprints/:id | Auth | limit_templates.update.any | h.Blueprint.DeleteBlueprint | DELETE gated by update perm, not a delete perm |

### 2.42 Clients

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/clients | Auth | clients.read.all OR clients.read.assigned OR clients.read.own | h.Client.ListClients | |
| GET | /api/v3/clients/:id | Auth | clients.read.all OR clients.read.assigned OR clients.read.own | h.Client.GetClient | |
| POST | /api/v3/clients | Auth | clients.create.any | h.Client.CreateClient | |
| PUT | /api/v3/clients/:id | Auth | clients.update.profile OR clients.update.contact | h.Client.UpdateClient | |

### 2.43 Client sub-collections

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/clients/:id/accounts | Auth | accounts.read.all OR accounts.read.own | h.Account.ListAccountsByClientPath | :id = client_id |
| GET | /api/v3/clients/:id/payments | Auth | accounts.read.all OR accounts.read.own | h.Tx.ListPaymentsByClientPath | :id = client_id |
| GET | /api/v3/clients/:id/transfers | Auth | accounts.read.all OR accounts.read.own | h.Tx.ListTransfersByClientPath | :id = client_id |
| GET | /api/v3/clients/:id/loans | Auth | credits.read.all OR credits.read.own | h.Credit.ListLoansByClientPath | :id = client_id |
| GET | /api/v3/clients/:id/cards | Auth | cards.read.all OR cards.read.own | h.Card.ListCardsByClientPath | :id = client_id |

### 2.44 Currencies

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/currencies | Auth | (none — bare protected group) | h.Account.ListCurrencies | No RequirePermission — any authenticated employee |

### 2.45 Accounts (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/accounts | Auth | accounts.read.all OR accounts.read.own | h.Account.ListAllAccounts | |
| GET | /api/v3/accounts/:id | Auth | accounts.read.all OR accounts.read.own | h.Account.GetAccount | |
| GET | /api/v3/accounts/:id/payments | Auth | accounts.read.all OR accounts.read.own | h.Tx.ListPaymentsByAccountPath | :id = account_id |
| GET | /api/v3/accounts/:id/cards | Auth | accounts.read.all OR accounts.read.own | h.Card.ListCardsByAccountPath | :id = account_id |
| POST | /api/v3/accounts | Auth | accounts.create.current OR accounts.create.foreign | h.Account.CreateAccount | |
| PUT | /api/v3/accounts/:id/name | Auth | accounts.update.name | h.Account.UpdateAccountName | |
| PUT | /api/v3/accounts/:id/limits | Auth | accounts.update.limits | h.Account.UpdateAccountLimits | |
| POST | /api/v3/accounts/:id/activate | Auth | accounts.deactivate.any | h.Account.ActivateAccount | Activate gated by deactivate.any perm |
| POST | /api/v3/accounts/:id/deactivate | Auth | accounts.deactivate.any | h.Account.DeactivateAccount | |
| GET | /api/v3/accounts/:id/changelog | Auth | accounts.read.all | h.Changelog.GetAccountChangelog | |

### 2.46 Companies

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/companies | Auth | accounts.create.current OR accounts.create.foreign | h.Account.CreateCompany | Gated by accounts create perms, not a company perm |

### 2.47 Bank accounts

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/bank-accounts | Auth | bank_accounts.manage.any | h.Account.ListBankAccounts | |
| GET | /api/v3/bank-accounts/:id/activity | Auth | bank_accounts.manage.any | h.Account.GetBankAccountActivity | |
| POST | /api/v3/bank-accounts | Auth | bank_accounts.manage.any | h.Account.CreateBankAccount | |
| DELETE | /api/v3/bank-accounts/:id | Auth | bank_accounts.manage.any | h.Account.DeleteBankAccount | |

### 2.48 Notification templates

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/notification-templates | Auth | notifications.templates.manage | h.Notification.ListNotificationTemplates | |
| GET | /api/v3/notification-templates/:channel/:type | Auth | notifications.templates.manage | h.Notification.GetNotificationTemplate | Two-segment param |
| PUT | /api/v3/notification-templates/:channel/:type | Auth | notifications.templates.manage | h.Notification.SetNotificationTemplate | |
| DELETE | /api/v3/notification-templates/:channel/:type | Auth | notifications.templates.manage | h.Notification.ResetNotificationTemplate | DELETE = reset to default |

### 2.49 Peer banks admin

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/peer-banks | Auth | peer_banks.manage.any | h.PeerBankAdmin.List | 🔒 TOUCHY — peer registration |
| GET | /api/v3/peer-banks/:id | Auth | peer_banks.manage.any | h.PeerBankAdmin.Get | 🔒 TOUCHY — peer registration |
| POST | /api/v3/peer-banks | Auth | peer_banks.manage.any | h.PeerBankAdmin.Create | 🔒 TOUCHY — peer registration |
| PUT | /api/v3/peer-banks/:id | Auth | peer_banks.manage.any | h.PeerBankAdmin.Update | 🔒 TOUCHY — peer registration |
| DELETE | /api/v3/peer-banks/:id | Auth | peer_banks.manage.any | h.PeerBankAdmin.Delete | 🔒 TOUCHY — peer registration |

### 2.50 Cards (employee management)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/cards/:id | Auth | cards.read.all OR cards.read.own | h.Card.GetCard | No bare list — collection via /clients/:id/cards or /accounts/:id/cards |
| POST | /api/v3/cards | Auth | cards.create.physical OR cards.create.virtual | h.Card.CreateCard | |
| POST | /api/v3/cards/authorized-persons | Auth | cards.create.physical OR cards.create.virtual | h.Card.CreateAuthorizedPerson | Shares create perm with card creation |
| POST | /api/v3/cards/:id/block | Auth | cards.block.any | h.Card.BlockCard | |
| POST | /api/v3/cards/:id/unblock | Auth | cards.unblock.any | h.Card.UnblockCard | |
| POST | /api/v3/cards/:id/deactivate | Auth | cards.block.any | h.Card.DeactivateCard | Deactivate gated by block.any, not a dedicated deactivate perm |
| GET | /api/v3/cards/:id/changelog | Auth | cards.read.all | h.Changelog.GetCardChangelog | |

### 2.51 Card requests (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/cards/requests | Auth | cards.approve.physical OR cards.approve.virtual OR cards.read.all | h.Card.ListCardRequests | |
| GET | /api/v3/cards/requests/:id | Auth | cards.approve.physical OR cards.approve.virtual OR cards.read.all | h.Card.GetCardRequest | |
| POST | /api/v3/cards/requests/:id/approve | Auth | cards.approve.physical OR cards.approve.virtual | h.Card.ApproveCardRequest | |
| POST | /api/v3/cards/requests/:id/reject | Auth | cards.approve.physical OR cards.approve.virtual | h.Card.RejectCardRequest | |

### 2.52 Payments (employee read)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/payments/:id | Auth | accounts.read.all | h.Tx.GetPayment | Gated by accounts perm, not a payments perm |

### 2.53 Transfers (employee read)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/transfers/:id | Auth | accounts.read.all | h.Tx.GetTransfer | Gated by accounts perm, not a transfers perm |

### 2.54 Transfer fees

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/fees | Auth | fees.create.any OR fees.update.any | h.Tx.ListFees | Read gated by create-or-update perms; no read perm exists |
| POST | /api/v3/fees | Auth | fees.create.any | h.Tx.CreateFee | |
| PUT | /api/v3/fees/:id | Auth | fees.update.any | h.Tx.UpdateFee | |
| DELETE | /api/v3/fees/:id | Auth | fees.update.any | h.Tx.DeleteFee | DELETE gated by update perm |

### 2.55 Loans (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/loans | Auth | credits.read.all OR credits.read.own | h.Credit.ListAllLoans | |
| GET | /api/v3/loans/:id | Auth | credits.read.all OR credits.read.own | h.Credit.GetLoan | |
| GET | /api/v3/loans/:id/installments | Auth | credits.read.all OR credits.read.own | h.Credit.GetInstallmentsByLoan | |
| GET | /api/v3/loans/:id/changelog | Auth | credits.read.all | h.Changelog.GetLoanChangelog | |

### 2.56 Loan requests (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/loan-requests | Auth | credits.read.all OR credits.read.own | h.Credit.ListLoanRequests | |
| GET | /api/v3/loan-requests/:id | Auth | credits.read.all OR credits.read.own | h.Credit.GetLoanRequest | |
| POST | /api/v3/loan-requests/:id/approve | Auth | credits.approve.cash OR credits.approve.housing | h.Credit.ApproveLoanRequest | |
| POST | /api/v3/loan-requests/:id/reject | Auth | credits.approve.cash OR credits.approve.housing | h.Credit.RejectLoanRequest | |

### 2.57 Interest rate tiers

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/interest-rate-tiers | Auth | credits.disburse.any | h.Credit.ListInterestRateTiers | |
| POST | /api/v3/interest-rate-tiers | Auth | credits.disburse.any | h.Credit.CreateInterestRateTier | |
| PUT | /api/v3/interest-rate-tiers/:id | Auth | credits.disburse.any | h.Credit.UpdateInterestRateTier | |
| DELETE | /api/v3/interest-rate-tiers/:id | Auth | credits.disburse.any | h.Credit.DeleteInterestRateTier | |
| POST | /api/v3/interest-rate-tiers/:id/apply | Auth | credits.disburse.any | h.Credit.ApplyVariableRateUpdate | |

### 2.58 Bank margins

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/bank-margins | Auth | credits.disburse.any | h.Credit.ListBankMargins | |
| PUT | /api/v3/bank-margins/:id | Auth | credits.disburse.any | h.Credit.UpdateBankMargin | |

### 2.59 Stock sources

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/stock-sources | Auth | securities.manage.catalog | h.StockSource.SwitchSource | POST to switch (no :id — replaces active source) |
| GET | /api/v3/stock-sources/active | Auth | securities.manage.catalog | h.StockSource.GetSourceStatus | |

### 2.60 Orders (employee / on-behalf)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/orders | Auth | orders.place.on_behalf_client OR orders.place.on_behalf_bank + ResolveIdentity | | h.StockOrder.CreateOrderOnBehalf | Employee only; distinct from /me/orders |
| GET | /api/v3/orders | Auth | orders.read.all | h.StockOrder.ListOrders | |
| POST | /api/v3/orders/:id/approve | Auth | orders.cancel.all + ResolveIdentity | | h.StockOrder.ApproveOrder | Approve gated by cancel.all perm |
| POST | /api/v3/orders/:id/reject | Auth | orders.cancel.all + ResolveIdentity | | h.StockOrder.RejectOrder | Reject gated by cancel.all perm |

### 2.61 Actuaries

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/actuaries | Auth | actuaries.read.all | h.Actuary.ListActuaries | |
| GET | /api/v3/actuaries/performance | Auth | actuaries.read.all | h.Fund.ActuaryPerformance | Uses Fund handler, not Actuary |
| PUT | /api/v3/actuaries/:id/limit | Auth | actuaries.manage.any | h.Actuary.SetActuaryLimit | |
| POST | /api/v3/actuaries/:id/require-approval | Auth | actuaries.manage.any | h.Actuary.RequireApproval | |
| POST | /api/v3/actuaries/:id/skip-approval | Auth | actuaries.manage.any | h.Actuary.SkipApproval | |
| POST | /api/v3/actuaries/:id/reset-limit | Auth | actuaries.manage.any | h.Actuary.ResetActuaryLimit | |

### 2.62 Tax (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/tax | Auth | securities.read.holdings_all | h.Tax.ListTaxRecords | Gated by holdings read perm, not a tax perm |
| POST | /api/v3/tax/collect | Auth | securities.manage.catalog | h.Tax.CollectTax | Gated by catalog manage perm, not a tax perm |

### 2.63 Changelogs (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/accounts/:id/changelog | Auth | accounts.read.all | h.Changelog.GetAccountChangelog | Listed also under Accounts (2.45) |
| GET | /api/v3/employees/:id/changelog | Auth | employees.read.all | h.Changelog.GetEmployeeChangelog | |
| GET | /api/v3/clients/:id/changelog | Auth | clients.read.all | h.Changelog.GetClientChangelog | |
| GET | /api/v3/cards/:id/changelog | Auth | cards.read.all | h.Changelog.GetCardChangelog | Listed also under Cards (2.50) |
| GET | /api/v3/loans/:id/changelog | Auth | credits.read.all | h.Changelog.GetLoanChangelog | Listed also under Loans (2.55) |

### 2.64 Unified portfolio (employee — explicit owner)

All routes in this section also carry `ResolveIdentity(OwnerIsBankIfEmployee)` via the `portfolioBank` group.

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/portfolio/bank | Auth + ResolveIdentity | (none — bare protected, no RequirePermission) | h.UnifiedPortfolio.GetBank | No explicit permission check at router layer |
| GET | /api/v3/portfolio/client/:client_id | Auth + ResolveIdentity | (none — bare protected) | h.UnifiedPortfolio.GetByClientID | No RequirePermission; portfolio.view.client exists but is not wired |
| GET | /api/v3/portfolio/investment-fund/:fund_id | Auth + ResolveIdentity | (none — bare protected) | h.UnifiedPortfolio.GetByFundID | No RequirePermission; portfolio.view.fund exists but is not wired |
| GET | /api/v3/portfolio/:portfolio_id | Auth + ResolveIdentity | (none — bare protected) | h.UnifiedPortfolio.GetByPortfolioID | Wildcard; no RequirePermission |

### 2.65 Watchlist by portfolio_id (employee)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/watchlist/:portfolio_id | Auth + ResolveIdentity | (none — bare protected) | h.Watchlist.GetByPortfolioID | No RequirePermission |

### 2.66 Investment funds (employee manage + positions)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/investment-funds | Auth | funds.manage.catalog | h.Fund.CreateFund | |
| PUT | /api/v3/investment-funds/:id | Auth | funds.manage.catalog | h.Fund.UpdateFund | |
| GET | /api/v3/investment-funds/positions | Auth | funds.read.all | h.Fund.ListBankPositions | |

### 2.67 Investment funds (AnyAuth — browse + invest/redeem)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/investment-funds | AnyAuth | | h.Fund.ListFunds | |
| GET | /api/v3/investment-funds/:id | AnyAuth | | h.Fund.GetFund | |
| POST | /api/v3/investment-funds/:id/invest | AnyAuth + ResolveIdentity | | h.Fund.Invest | No permission check — any authenticated caller |
| POST | /api/v3/investment-funds/:id/redeem | AnyAuth + ResolveIdentity | | h.Fund.Redeem | No permission check — any authenticated caller |

### 2.68 Options v2 (by option_id)

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| POST | /api/v3/options/:option_id/orders | Auth + ResolveIdentity | otc.trade.accept OR otc.trade.exercise OR securities.trade.any | h.OptionsV2.CreateOrder | /options prefix distinct from /securities/options |
| POST | /api/v3/options/:option_id/exercise | Auth + ResolveIdentity | otc.trade.accept OR otc.trade.exercise OR securities.trade.any | h.OptionsV2.Exercise | |

### 2.69 Admin crons

| Method | Path | Auth | Permission | Handler | Notes |
|---|---|---|---|---|---|
| GET | /api/v3/admin/crons | Auth | admin.crons.view | h.AdminCron.List | |
| GET | /api/v3/admin/crons/:service/:name | Auth | admin.crons.view | h.AdminCron.Get | Two-segment param |
| POST | /api/v3/admin/crons/:service/:name/trigger | Auth | admin.crons.trigger | h.AdminCron.Trigger | |
| POST | /api/v3/admin/crons/:service/:name/pause | Auth | admin.crons.manage | h.AdminCron.Pause | |
| POST | /api/v3/admin/crons/:service/:name/resume | Auth | admin.crons.manage | h.AdminCron.Resume | |

---

## 3. Inconsistencies and questions

### 3.1 Cancel verb inconsistency — DELETE vs POST /:id/cancel

Three resources use `DELETE /:id` for cancel:
- `DELETE /api/v3/me/recurring-funds/:id` → h.RecurringFund.Cancel
- `DELETE /api/v3/me/otc/stocks/:id` → h.OTCStock.CancelOTCStockOffer
- `DELETE /api/v3/me/otc/options/:id` → h.OTCOptions.CancelMyListing
- `DELETE /api/v3/me/otc/options/:id/negotiations/:nid` → h.OTCOptions.CancelMyNegotiation
- `DELETE /api/v3/me/peer-otc/negotiations/:rid/:id` → h.PeerOTCInitiate.CancelPeerNegotiation

While parallel resources use `POST /:id/cancel`:
- `POST /api/v3/me/orders/:id/cancel` → h.StockOrder.CancelOrder
- `POST /api/v3/me/recurring-orders/:id/cancel` → h.RecurringOrder.Cancel

The two cancel patterns coexist for closely related resources (recurring-orders vs recurring-funds; stock orders vs OTC stock offers) with no clear rule for which pattern applies when.

### 3.2 ~~Counter-negotiation verb inconsistency — PUT vs POST /:id/counter~~ — 🔒 FROZEN

~~Peer OTC uses `PUT /api/v3/me/peer-otc/negotiations/:rid/:id` → CounterPeerNegotiation. Local OTC options use `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter` → CounterMyNegotiation.~~

Both peer-OTC routes follow the SI-TX-Proto spec (the PUT shape is what cohort banks expect). The local /me/otc/options counter route is the one that could in principle be reshaped to match, but doing so would mean the local and peer flows diverge from a UI perspective — discuss before changing.

### 3.3 ~~Accept-negotiation verb inconsistency — GET vs POST~~ — 🔒 FROZEN

~~`GET /api/v3/negotiations/:rid/:id/accept` → h.PeerOTC.AcceptNegotiation uses GET for a state change.~~

GET-on-accept is per the SI-TX-Proto spec and required for peer-bank interop. Do not change.

### 3.4 Missing RequirePermission on the /portfolio/* employee routes

All four routes under `/api/v3/portfolio/...` (GetBank, GetByClientID, GetByFundID, GetByPortfolioID) sit inside `protected` (AuthMiddleware) but have no `RequirePermission` call. The permissions `portfolio.view.client` and `portfolio.view.fund` exist in the catalog and are seeded, but are not checked at the router layer. Any authenticated employee can call these endpoints.

Similarly, `GET /api/v3/watchlist/:portfolio_id` has no RequirePermission despite sitting in `protected`.

### 3.5 Read operations gated by write/manage permissions

Several read endpoints use write permissions as their gate, meaning someone who can only read cannot use them without also holding the write perm:
- `GET /api/v3/limits/templates` — gated by `limit_templates.create.any OR limit_templates.update.any` (no read perm exists for limit templates)
- `GET /api/v3/blueprints` and `GET /api/v3/blueprints/:id` — same pattern, gated by `limit_templates.create.any OR limit_templates.update.any`
- `GET /api/v3/fees` — gated by `fees.create.any OR fees.update.any` (no `fees.read.any` perm exists)
- `GET /api/v3/clients/:id/limits` — gated by `clients.update.limits` (no separate read perm)

### 3.6 DELETE gated by update permission

- `DELETE /api/v3/fees/:id` → gated by `fees.update.any` (no delete perm)
- `DELETE /api/v3/blueprints/:id` → gated by `limit_templates.update.any`
- `DELETE /api/v3/interest-rate-tiers/:id` → gated by `credits.disburse.any`

### 3.7 Missing employee deactivate route

The permission `employees.deactivate.any` is in the catalog and seeded for EmployeeAdmin and EmployeeSupervisor (via perms.gen.go), but there is no `POST /employees/:id/deactivate` or `DELETE /employees/:id` route in the router.

### 3.8 /me/holdings/:id vs /me/portfolio/:id shape inconsistency

Holdings transactions live at `/me/holdings/:id/transactions` while option exercise lives at `/me/portfolio/:id/exercise`. Both operate on individual holdings (positions) but use different resource prefixes (`holdings` vs `portfolio`) for what appear to be parallel sub-resource actions on the same domain object.

### 3.9 Recurring orders vs recurring funds middleware inconsistency

`/me/recurring-orders` routes all carry `bankIfEmp` (ResolveIdentity), but `/me/recurring-funds` routes carry no ResolveIdentity. These are parallel recurring-investment features across the same user base.

### 3.10 /me/otc/options ListMyOTCOptions uses Portfolio handler, not OTCOptions handler

`GET /api/v3/me/otc/options` → `h.Portfolio.ListMyOTCOptions`  
All other `/me/otc/options` routes use `h.OTCOptions.*`. The read list for the caller's own options is split across two handler structs.

Similarly, `GET /api/v3/otc/options` (public listing browse) and `GET /api/v3/otc/stocks` (public stock browse) both route through `h.Portfolio.*` while the write paths for the same resources route through `h.OTCOptions.*` / `h.OTCStock.*`.

### 3.11 `/options/:option_id/orders` prefix ambiguity with `/securities/options`

The route `POST /api/v3/options/:option_id/orders` lives under the top-level `/options` prefix. The securities catalog also exposes `GET /api/v3/securities/options` and `GET /api/v3/securities/options/:id`. These are different concepts (trading an option contract vs browsing the option security catalog) but share the word "options" at different path levels with no consistent nesting.

### 3.12 `GET /api/v3/currencies` has no RequirePermission

`GET /api/v3/currencies` sits in `protected` (AuthMiddleware) but has no `RequirePermission` call — it is reachable by any authenticated employee regardless of role. This may be intentional (currencies is reference data) but it is inconsistent with other reference-data routes like exchange rates (public, no auth) or stock exchanges (AnyAuth).

### 3.13 Tax routes use borrowed permissions from unrelated domains

`GET /api/v3/tax` → gated by `securities.read.holdings_all` (not a tax-specific perm).  
`POST /api/v3/tax/collect` → gated by `securities.manage.catalog`.  
No `tax.*` permission entries exist in the catalog. Tax management is entirely governed by securities perms.

### 3.14 Approve/reject orders gated by `orders.cancel.all`

`POST /api/v3/orders/:id/approve` and `POST /api/v3/orders/:id/reject` are both gated by `orders.cancel.all`. Semantically approve ≠ cancel, but there is no `orders.approve.*` or `orders.manage.*` perm in the catalog.

### 3.15 `POST /api/v3/roles` uses `roles.update.any` (no `roles.create.any` perm)

Creating a role is gated by the update permission. The catalog has no `roles.create.any` entry.

### 3.16 Client changelog gated by `clients.read.all`, not `clients.read.assigned`

`GET /api/v3/clients/:id/changelog` requires `clients.read.all` but `GET /api/v3/clients/:id` accepts `clients.read.all OR clients.read.assigned OR clients.read.own`. An employee with only `clients.read.assigned` can read a client but cannot read their changelog.

### 3.17 `POST /api/v3/me/cards/virtual` lacks RequireClientToken

All other mutating `/me/cards/:id/...` routes that are client-specific (pin, verify-pin, temporary-block) carry `RequireClientToken`, but `POST /me/cards/virtual` does not. An employee calling `/me/cards/virtual` would act on their own non-existent client identity.

### 3.18 /stock-exchanges/testing-mode routing ambiguity

`GET /api/v3/stock-exchanges/testing-mode` (Auth, `securities.manage.catalog`) is registered in the `protected` group while `GET /api/v3/stock-exchanges` and `GET /api/v3/stock-exchanges/:id` are registered in a separate `stockExchanges` AnyAuth group. Gin routes are matched by registration order and the protected route uses a static segment (`testing-mode`) rather than a wildcard, so the match will work correctly, but it requires knowledge of both groups to understand the full surface of `/stock-exchanges`.

---

## 4. Suggested follow-ups

1. **Cancel verb**: Should the system standardize on `POST /:id/cancel` (explicit action, keeps the resource alive for history) or `DELETE /:id` (RESTful destructive) for all "cancel" operations? Recurring-orders uses POST; recurring-funds uses DELETE. OTC options listings use DELETE; stock orders use POST.

2. ~~**Counter-negotiation verb**~~ — 🔒 FROZEN (peer-OTC PUT is per SI-TX-Proto spec). The local `/me/otc/options/:id/negotiations/:nid/counter` route could theoretically be reshaped to PUT to match, but that would unify the local UI flow with the wire protocol — discuss before touching.

3. ~~**Peer `GET negotiations/:rid/:id/accept`**~~ — 🔒 FROZEN per SI-TX-Proto.

4. **Portfolio permission enforcement**: Should `portfolio.view.client` be added to `GET /portfolio/client/:client_id` and `portfolio.view.fund` to `GET /portfolio/investment-fund/:fund_id` at the router layer? Currently any authenticated employee can call them.

5. **Missing read permissions for fees, limit templates, and blueprints**: Should `fees.read.any`, `limit_templates.read.any` (or `limits.read.any`) be added to the catalog so that a reader role can be granted without also granting write access?

6. **Employee deactivate route**: `employees.deactivate.any` exists in the catalog but has no route. Should `POST /employees/:id/deactivate` (or `DELETE /employees/:id`) be added?

7. **Holdings vs portfolio prefix**: Should `/me/holdings/:id/transactions` be moved to `/me/portfolio/:id/transactions` so both holding-level actions share the same prefix? Or is `holdings` intentionally a separate resource?

8. **Recurring funds missing ResolveIdentity**: Should `/me/recurring-funds` carry `bankIfEmp` (ResolveIdentity) to match the pattern used by `/me/recurring-orders`, `/me/price-alerts`, `/me/watchlist`, etc.?

9. **Tax permissions**: Should dedicated `tax.read.*` and `tax.manage.*` permissions be added to the catalog rather than borrowing from `securities.*`?

10. **`POST /me/cards/virtual` client restriction**: Should `RequireClientToken` be added to `POST /me/cards/virtual` to match the pattern of the other mutating card routes under `/me/cards`?

11. **`GET /currencies` permission**: Should this be moved to a public route (no middleware), an AnyAuth route (matching exchange rates), or stay as Auth-with-no-permission? The current state makes it inconsistent with comparable reference-data endpoints.

12. **`orders.cancel.all` as approve/reject gate**: Should an `orders.approve.all` or `orders.manage.any` permission be added so that approve and reject are not conflated with cancel?

13. **`/stock-exchanges/testing-mode` split across groups**: Should the testing-mode routes live in the same `stockExchanges` group (with an additional permission guard inline) to co-locate all `/stock-exchanges` routes in one place?

14. **`/otc/stocks` and `/otc/options` read handlers from Portfolio vs dedicated handlers**: Should the list-browse routes (`GET /otc/stocks`, `GET /otc/options`, `GET /me/otc/options`) be migrated to `h.OTCStock` / `h.OTCOptions` for consistency, or is the Portfolio handler split intentional?

15. **There are no pagination query parameters visible at the route level for collection endpoints** — should pagination (page/size or cursor) be standardized across all list routes?
