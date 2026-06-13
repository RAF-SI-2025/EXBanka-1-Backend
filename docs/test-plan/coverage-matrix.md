# EXBanka — Master Coverage Matrix

Every feature / sub-feature / option from the seven test-plan files, mapped to its
TC IDs, the existing Go test that exercises it (where one exists), and a status:

- **covered** — the requirement has at least one positive and one negative TC and the behaviour is implemented.
- **partial** — the behaviour is implemented but only partly (e.g. one channel of two), or only unit-level / analogue test coverage exists, or it diverges from the spec in a documented way.
- **NO-ENDPOINT** — the requirement has no implementing endpoint/behaviour (a real coverage gap to surface, not skip).

Rows are reproduced verbatim from each source file's "Coverage rows" block. The
**[Gap list](#gap-list-every-partial--no-endpoint-row)** at the bottom collects every `partial` and
`NO-ENDPOINT` row with a one-line note — the "no silent caps" checklist.

---

## Summary

| Source file | Covered | Partial | NO-ENDPOINT | Total |
|---|---:|---:|---:|---:|
| Celina 1 — User Management | 38 | 15 | 2 | 55 |
| Celina 2 — Core Banking | 47 | 31 | 5 | 83 |
| Celina 3 — Securities | 69 | 6 | 7 | 82 |
| Celina 4 — OTC & Funds | 62 | 4 | 3 | 69 |
| Celina 5 — Cross-Bank | 50 | 15 | 6 | 71 |
| Cross-cutting — Verification | 22 | 7 | 3 | 32 |
| TODO_final — Notifications & Mobile | 20 | 17 | 6 | 43 |
| **Grand total** | **308** | **95** | **32** | **435** |

---

## Celina 1 — User Management

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| Login: employee success | TC-C1-LOGIN-001 | auth_test.go::TestAuth_LoginWithValidCredentials | covered |
| Login: client success + system_type routing | TC-C1-LOGIN-002, TC-C1-RBAC-030 | wf_client_stock_banking_test.go (loginAsClient helper) | covered |
| Login: wrong password | TC-C1-LOGIN-010 | auth_test.go::TestAuth_LoginWithInvalidPassword; auth_login_failure_modes_test.go::TestLogin_Wrong_Password_Returns_401_Unauthorized | covered |
| Login: non-existent email (anti-enum) | TC-C1-LOGIN-011 | auth_test.go::TestAuth_LoginWithNonexistentEmail | covered |
| Login: inactive/disabled account | TC-C1-LOGIN-012 | role_permission_revocation_test.go | covered |
| Login: pending (not activated) | TC-C1-LOGIN-013 | auth_login_failure_modes_test.go::TestLogin_Pending_Account_Returns_409_BusinessRule | covered |
| Login: missing/empty/malformed fields | TC-C1-LOGIN-014 | auth_test.go::TestAuth_LoginWithEmptyFields; auth_login_failure_modes_test.go::TestLogin_Missing_Email_Returns_401_Unauthorized | covered |
| Brute-force: 4th attempt allowed (boundary low) | TC-C1-LOCK-040 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | partial |
| Brute-force: 5th attempt locks (boundary high) | TC-C1-LOCK-041 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | covered |
| Brute-force: locked login blocked (correct pass) | TC-C1-LOCK-042 | auth_login_failure_modes_test.go::TestLogin_Locked_Account_Returns_403_Forbidden | covered |
| Brute-force: lock auto-expiry (30 min impl) | TC-C1-LOCK-043 | — | partial |
| Brute-force: lock-notification email | TC-C1-LOCK-050 | — | NO-ENDPOINT |
| Brute-force: reset unlocks + resets counter | TC-C1-LOCK-051 | auth_service_flows_test.go::TestResetPassword_UnlocksLockedAccount | covered |
| Password reset: request (existing email) | TC-C1-PWD-060 | auth_test.go::TestAuth_PasswordResetRequest | covered |
| Password reset: request (unknown email anti-enum) | TC-C1-PWD-061 | auth_test.go::TestAuth_PasswordResetRequest | covered |
| Password reset: reset with valid token | TC-C1-PWD-062 | — | partial |
| Password reset: expired/invalid token | TC-C1-PWD-063 | auth_test.go::TestAuth_ActivateAccountInvalidToken (sibling) | partial |
| Password reset: confirm mismatch | TC-C1-PWD-064 | — | partial |
| Password constraints (each violation) | TC-C1-PWD-070..074 | auth_service_test.go (unit, complexity) | covered |
| Activation: valid token → active | TC-C1-ACT-080 | mobile_auth_test.go::TestMobileAuth_ActivateFullFlow (mobile variant) | partial |
| Activation: invalid/expired token (24h) | TC-C1-ACT-081 | auth_test.go::TestAuth_ActivateAccountInvalidToken | covered |
| Activation: resend email | TC-C1-ACT-082 | — | partial |
| Employee create: all fields (no password) | TC-C1-EMP-100 | employee_test.go::TestEmployee_CreateWith{Basic,Agent,Supervisor,Admin}Role | covered |
| Employee create: inactive at create | TC-C1-EMP-100b | — | NO-ENDPOINT |
| Employee create: duplicate email/username/JMBG | TC-C1-EMP-101 | employee_test.go::TestEmployee_CreateWithDuplicateEmail | covered |
| Employee create: invalid JMBG (13 digits) | TC-C1-EMP-102 | employee_test.go::TestEmployee_CreateWithInvalidJMBG | covered |
| Employee create: non-admin forbidden | TC-C1-EMP-103 | roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles; employee_onbehalf_test.go::TestEmployeeOnBehalf_AsBasic_Forbidden | covered |
| Employee list + filters | TC-C1-EMP-110 | employee_test.go::TestEmployee_ListAndGet | covered |
| Employee get / not-found | TC-C1-EMP-111 | employee_test.go::TestEmployee_ListAndGet; TestEmployee_GetNonExistent | covered |
| Employee update: editable fields | TC-C1-EMP-112 | employee_test.go::TestEmployee_Update | covered |
| Employee update: cannot edit admin | TC-C1-EMP-113 | — | partial |
| Employee deactivate → sessions killed + token_expired | TC-C1-EMP-120, TC-C1-E2E-201 | role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth | covered |
| Roles/permissions: list | TC-C1-RBAC-130 | roles_permissions_test.go::TestRoles_ListRoles/GetRole/ListPermissions | covered |
| Roles: assign roles to employee | TC-C1-RBAC-131 | roles_permissions_test.go::TestRoles_SetEmployeeRoles | covered |
| Permissions: per-employee additional + grant admin | TC-C1-RBAC-132 | roles_permissions_test.go::TestRoles_SetEmployeeAdditionalPermissions | covered |
| Role perms: assign/revoke single (catalog/idempotent/404/auth) | TC-C1-RBAC-133 | wf_role_admin_test.go::TestAdmin_Assign/RevokePermissionFromRole_* | covered |
| Role perms: replace all | TC-C1-RBAC-134 | roles_permissions_test.go::TestRoles_UpdateRolePermissions | covered |
| Role perm change → holder re-auth | TC-C1-RBAC-135 | role_permission_revocation_test.go::TestRoleRevocation_AdminUpdatesRolePerms_AgentMustReauth | covered |
| RBAC gating: unpermitted→403, unauth→401, clients unaware | TC-C1-RBAC-030 | roles_permissions_test.go::TestRoles_NonAdminCannotManageRoles; employee_onbehalf_test.go::TestEmployeeOnBehalf_AccountNotOwnedByClient_Returns403/_AsBasic_Forbidden | covered |
| Client create | TC-C1-CLI-140 | client_test.go::TestClient_CreateMultipleClients | covered |
| Client create: invalid JMBG | TC-C1-CLI-141 | client_test.go::TestClient_CreateWithInvalidJMBG | covered |
| Client create: missing required fields | TC-C1-CLI-142 | client_test.go::TestClient_CreateWithMissingRequiredFields | covered |
| Client validation: DOB not future | TC-C1-CLI-143 | — | partial |
| Client validation: email format/unique + phone digits/+ | TC-C1-CLI-144 | client_test.go::TestClient_CreateMultipleClients (email-format via binding) | partial |
| Client list/get/update (+deactivate) | TC-C1-CLI-145 | client_test.go::TestClient_ListAndGet/Update/GetNonExistent | covered |
| Token: refresh | TC-C1-TOK-150 | auth_test.go::TestAuth_RefreshToken | covered |
| Token: refresh invalid/revoked | TC-C1-TOK-151 | auth_test.go::TestAuth_RefreshTokenInvalid | covered |
| Token: logout (revoke) | TC-C1-TOK-152 | auth_test.go::TestAuth_Logout | covered |
| Token: protected route without/invalid token | TC-C1-TOK-153 | auth_test.go::TestAuth_AccessProtectedRouteWithoutToken/WithInvalidToken | covered |
| Sessions: list my sessions | TC-C1-TOK-154 | session_handler_test.go (unit) | partial |
| Sessions: revoke specific (+ownership) | TC-C1-TOK-155 | session_handler_test.go (unit) | partial |
| Sessions: revoke all others | TC-C1-TOK-156 | session_handler_test.go (unit) | partial |
| Login history | TC-C1-TOK-157 | session_handler_test.go (unit) | partial |
| Defense Provera 1: create→activate→login | TC-C1-E2E-200 | employee_test.go + auth_test.go (chain) | partial |
| Defense Provera 2: deactivate employee | TC-C1-E2E-201 | role_permission_revocation_test.go | covered |

---

## Celina 2 — Core Banking

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| Account: create current (RSD-only) | TC-C2-ACC-001, TC-C2-ACC-002 | account_test.go::TestAccount_CreateCurrentAccount, TestAccount_CreateWithInvalidKind | covered |
| Account: create foreign (all 7 FX currencies) | TC-C2-ACC-003 | account_test.go::TestAccount_CreateForeignAccount, TestAccount_CreateForeignPersonalUSD | covered |
| Account: initial balance | TC-C2-ACC-004 | account_test.go::TestAccount_CreateWithInitialBalance | covered |
| Account: all personal subtypes + maintenance fee | TC-C2-ACC-005a..f | — | partial |
| Account: all business subtypes (DOO/AD/Fondacija) | TC-C2-ACC-006a..c | — | partial |
| Account: auto-card checkbox (on/off) | TC-C2-ACC-007, TC-C2-ACC-008 | — | partial |
| Account: owner existing vs new-client-inline | TC-C2-ACC-009 | client_test.go::TestClient_CreateMultipleClients | covered |
| Account: list/filter (name/number) + pagination | TC-C2-ACC-010, TC-C2-ACC-011 | account_test.go::TestAccount_ListAllAccounts, TestAccount_GetByAccountNumber, TestAccount_ListWithPagination | covered |
| Account: client /me list (active, sorted) | TC-C2-ACC-012 | — | partial |
| Account: detail personal vs business | TC-C2-ACC-013, TC-C2-ACC-014 | account_test.go::TestAccount_GetAccountByID, TestAccount_GetNonExistent | partial |
| Account: update name (uniqueness) | TC-C2-ACC-015 | account_test.go::TestAccount_UpdateName | covered |
| Account: update limits + verification | TC-C2-ACC-016 | account_test.go::TestAccount_UpdateLimits, TestAccount_UpdateLimitsNegativeRejected | covered |
| Account: status active/inactive | TC-C2-ACC-017 | account_test.go::TestAccount_UpdateStatus, TestAccount_DeactivateNonExistent | covered |
| Account: list currencies | TC-C2-ACC-018 | account_test.go::TestAccount_ListCurrencies | covered |
| Account-number format (bank prefix + check digit) | TC-C2-ACC-001 | — | partial |
| Company: create (firma) | TC-C2-CMP-001 | account_test.go::TestAccount_CreateCompany | covered |
| Bank accounts: list/create/delete | TC-C2-BANK-001 | account_test.go::TestAccount_BankAccountCRUD, TestAccount_DeleteBankAccount, TestAccount_BankAccountCreateForeignEUR | covered |
| Bank accounts: delete guard (>=1 RSD + >=1 FX) | TC-C2-BANK-002 | — | partial |
| Bank accounts: ledger activity (commission credit) | TC-C2-BANK-003 | bank_account_activity_test.go::TestBankAccountActivity_EmployeeCanView, TestBankAccountActivity_RejectsClientAccount | covered |
| Payment: same-currency to different client | TC-C2-PAY-001 | payment_test.go::TestPayment_EndToEnd | covered |
| Payment: fee stacking 0.1%+5% to bank RSD | TC-C2-PAY-002, TC-C2-PAY-003, TC-C2-PAY-004 | payment_test.go::TestPayment_WithFee | covered |
| Payment: insufficient funds | TC-C2-PAY-005 | payment_test.go::TestPayment_InsufficientBalance | covered |
| Payment: wrong-owner source (403) | TC-C2-PAY-006 | — | partial |
| Payment: wrong verification code | TC-C2-PAY-007 | payment_test.go::TestPayment_WrongOTPCodeRejected | covered |
| Payment: inactive/nonexistent recipient | TC-C2-PAY-008 | — | partial |
| Payment: over client daily/monthly limit | TC-C2-PAY-009 | — | partial |
| Payment: preview (fee/limit info) | TC-C2-PAY-010 | payment_test.go::TestPayment_PreviewAndStatus | covered |
| Payment: history + status filters | TC-C2-PAY-011 | payment_test.go::TestPayment_EmployeeCanReadPayments, TestPayment_PreviewAndStatus | covered |
| Payment: Kafka notifications | TC-C2-PAY-012 | payment_test.go::TestPayment_KafkaEventsOnPayment | covered |
| Payment: unauthenticated rejected | TC-C2-PAY-013 | payment_test.go::TestPayment_UnauthenticatedCannotCreatePayment | covered |
| Payment: cross-currency FX (different clients) | TC-C2-PAY-020 | — | NO-ENDPOINT |
| Transfer: same-currency own accounts | TC-C2-TRF-001 | transfer_test.go::TestTransfer_SameCurrency_EndToEnd, TestTransfer_InsufficientBalance | covered |
| Transfer: cross-currency FX + commission to bank | TC-C2-TRF-002 | transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR | covered |
| Transfer: preview (rate + commission) | TC-C2-TRF-003 | — | partial |
| Transfer: intra-client guard (reject other client) | TC-C2-TRF-004 | — | partial |
| Transfer: history + listings | TC-C2-TRF-005 | transfer_test.go::TestTransfer_EmployeeCanReadTransfers, TestTransfer_ListByClient, TestTransfer_UnauthenticatedCannotCreateTransfer | covered |
| Transfer: reserved-funds semantics (internal=0) | TC-C2-TRF-006 | — | partial |
| Payment recipients: CRUD + ownership | TC-C2-RCP-001 | transfer_test.go::TestTransfer_PaymentRecipientCRUD | covered |
| Menjačnica: kursna lista | TC-C2-FX-001 | exchange_rate_test.go::TestExchangeRates_ListAll | covered |
| Menjačnica: specific pair | TC-C2-FX-002 | exchange_rate_test.go::TestExchangeRates_GetSpecific | covered |
| Menjačnica: equivalence calculator | TC-C2-FX-003 | exchange_rate_test.go::TestExchangeRates_Calculate, _MissingFields, _InvalidAmount, _UnsupportedCurrency | covered |
| Menjačnica: 2-leg via RSD + per-leg commission | TC-C2-FX-004 | transfer_test.go::TestTransfer_CrossCurrencyRSDtoEUR | partial |
| Card: issue per brand (visa/mc/dina/amex) | TC-C2-CARD-001a..d | card_test.go::TestCard_CreateAllBrands, TestCard_AllBrandsDebitAndCredit, TestCard_CreateWithInvalidBrand | covered |
| Card: max 2 physical per personal account | TC-C2-CARD-002 | — | partial |
| Card: max 1 per authorized person per business acct | TC-C2-CARD-003 | — | partial |
| Card: block/unblock/deactivate (employee) | TC-C2-CARD-004 | card_test.go::TestCard_BlockUnblockDeactivate | covered |
| Card: client blocks own, employee unblocks | TC-C2-CARD-005 | — | partial |
| Card: deactivated cannot be reactivated | TC-C2-CARD-006 | — | partial |
| Card: virtual single_use | TC-C2-CARD-007 | card_test.go::TestCard_VirtualCardSingleUse, TestCard_VirtualSingleUseWithClientAuth, TestCard_VirtualInvalidUsageType | covered |
| Card: virtual multi_use + max_uses boundary | TC-C2-CARD-008 | card_test.go::TestCard_VirtualMultiUseWithClientAuth | covered |
| Card: virtual unlimited | TC-C2-CARD-009 | card_test.go::TestCard_VirtualUnlimitedWithClientAuth | NO-ENDPOINT |
| Card: PIN set + verify | TC-C2-CARD-010 | card_test.go::TestCard_PINManagement, TestCard_PINSetAndVerify, TestCard_ChangePin | covered |
| Card: PIN lock after 3 fails | TC-C2-CARD-011 | card_test.go::TestCard_PINWrongThreeTimes_LocksCard | covered |
| Card: temporary block + auto-expiry | TC-C2-CARD-012 | card_test.go::TestCard_TemporaryBlockWithExpiry | covered |
| Card: list (masked, by account/client) | TC-C2-CARD-013 | card_test.go::TestCard_GetCard, TestCard_ListByAccount | covered |
| Card: authorized person (business) | TC-C2-CARD-014 | — | partial |
| Card: multi-currency spend fee (2%+0.5%) | TC-C2-CARD-015 | — | NO-ENDPOINT |
| Card request: client request -> employee approve | TC-C2-CREQ-001 | card_request_test.go::TestCardRequest_FullLifecycle, TestCardRequest_EmployeeApproveAndRejectFlow | covered |
| Card request: reject with reason | TC-C2-CREQ-002 | card_request_test.go::TestCardRequest_RejectRequiresReason, TestCardRequest_RejectNonExistentRequest | covered |
| Card request: auth/role boundaries + filters | TC-C2-CREQ-003 | card_request_test.go::TestCardRequest_UnauthenticatedCannotCreateRequest, TestCardRequest_EmployeeCannotCreateRequest, TestCardRequest_EmployeeCanListRequests, TestCardRequest_EmployeeCanFilterByStatus, TestCardRequest_InvalidStatusFilterRejected | covered |
| Card request: client tracks own (/me) | TC-C2-CREQ-004 | card_request_test.go::TestCardRequest_GetNonExistentRequest | covered |
| Loan: submit all types x fixed | TC-C2-LOAN-001a..e | loan_test.go::TestLoan_AllLoanTypes, TestLoan_FullLifecycle, TestLoan_UnauthenticatedCannotCreateLoanRequest | covered |
| Loan: submit variable interest | TC-C2-LOAN-002 | loan_test.go::TestLoan_FullLifecycle | covered |
| Loan: approve -> create + disburse (bank debit/client credit) | TC-C2-LOAN-003 | loan_test.go::TestLoan_FullLifecycle, loan_disbursement_test.go::TestLoanDisbursement_Saga_HappyPath, TestLoan_ApproveNonExistentRequest | covered |
| Loan: approval limit gate (MaxLoanApprovalAmount) | TC-C2-LOAN-004 | — | partial |
| Loan: insufficient bank liquidity -> 409 | TC-C2-LOAN-005 | loan_disbursement_test.go::TestLoanDisbursement_BankInsufficientLiquidity_Returns409 | covered |
| Loan: reject request | TC-C2-LOAN-006 | loan_test.go::TestLoan_RejectLoanRequest, TestLoan_RejectNonExistentRequest | covered |
| Loan: currency must match account | TC-C2-LOAN-007 | — | partial |
| Loan: registry & detail views | TC-C2-LOAN-008 | loan_test.go::TestLoan_ListAllLoans, TestLoan_ListLoansByClient, TestLoan_ListLoanRequests, TestLoan_ListLoanRequestsByClient, TestLoan_GetMyLoanRequest_SelfRoute, TestLoan_GetNonExistentLoan | covered |
| Loan: variable-rate recalculation (tier apply) | TC-C2-LOAN-009 | — | partial |
| Loan: interest tiers + bank margins config | TC-C2-LOAN-010 | — | partial |
| Installment: schedule + formula on creation | TC-C2-INST-001 | loan_test.go::TestLoan_FullLifecycle | partial |
| Installment: rate-tier boundary by amount | TC-C2-INST-002 | — | partial |
| Installment: auto monthly deduction success | TC-C2-INST-005 | — | NO-ENDPOINT |
| Installment: auto deduction failure (overdue + notice) | TC-C2-INST-006 | — | NO-ENDPOINT |
| Transfer fees: rule CRUD + stacking | TC-C2-FEE-001 | payment_test.go::TestPayment_WithFee | partial |
| Defense Provera 1 (business acct, no card, new client+company, x2 FX) | TC-C2-E2E-P1 | — | partial |
| Defense Provera 2 (personal acct + card, new client, x2 FX) | TC-C2-E2E-P2 | — | partial |
| Defense Provera 3 (transfer same+diff currency, bank commission) | TC-C2-E2E-P3 | transfer_test.go::TestTransfer_SameCurrency_EndToEnd, TestTransfer_CrossCurrencyRSDtoEUR | partial |
| Defense Provera 4 (payment same+diff currency, bank commission) | TC-C2-E2E-P4 | payment_test.go::TestPayment_WithFee | partial |
| Defense Provera 5 (loan request -> approve -> disburse) | TC-C2-E2E-P5 | loan_disbursement_test.go::TestLoanDisbursement_Saga_HappyPath | covered |
| Defense Provera 6 (card request -> auto -> limit -> block -> deactivate) | TC-C2-E2E-P6 | card_test.go::TestCard_BlockUnblockDeactivate | partial |
| Defense Provera 7 (menjacnica equivalent value) | TC-C2-E2E-P7 | exchange_rate_test.go::TestExchangeRates_Calculate | covered |

---

## Celina 3 — Securities

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| exchanges: list/search/detail | TC-C3-EXC-001..011 | stock_exchange_test.go::TestStockExchange_ListExchanges/_SearchFilter/_GetExchange/_GetExchange_NotFound/_ListExchanges_Unauthenticated | covered |
| exchanges: testing-mode toggle (open/close for testing) | TC-C3-EXC-020,021 | stock_exchange_test.go::TestStockExchange_TestingMode_SetAndGet/_RequiresSupervisor | covered |
| order rejected when exchange closed ("Berza je zatvorena") | TC-C3-EXC-030 | — | NO-ENDPOINT |
| after-hours (<4h to close) slow fill + after_hours flag | TC-C3-EXC-031 | — | partial |
| listings: stocks list/search/sort/filter | TC-C3-LST-001..005 | securities_test.go::TestSecurities_ListStocks/_SearchByTicker/_SortByPrice/_InvalidSortBy | covered |
| listings: stock detail + price history periods | TC-C3-LST-010,011 | securities_test.go::TestSecurities_GetStock/_GetStockHistory/_GetStockHistory_InvalidPeriod | covered |
| listings: futures (month codes/settlement) + filter/detail/history | TC-C3-LST-020,021 | securities_test.go::TestSecurities_ListFutures/_SettlementDateFilter/_GetFutures/_GetFutures_NotFound/_GetFuturesHistory | covered |
| listings: forex pairs list/filter/detail/history | TC-C3-LST-030,031 | securities_test.go::TestSecurities_ListForexPairs/_LiquidityFilter/_InvalidLiquidity/_GetForexPair/_GetForexPair_NotFound/_GetForexPairHistory | covered |
| listings: options chain + detail | TC-C3-LST-040,041 | securities_test.go::TestSecurities_ListOptions_RequiresStockID/_WithStockID/_FilterByType/_GetOption/_GetOption_NotFound | covered |
| market-data: candles | TC-C3-LST-050 | — | covered |
| client visibility: stocks+futures allowed | TC-C3-VIS-001 | securities_test.go::TestSecurities_ClientCanViewStocksAndFutures | covered |
| client visibility: forex/options hidden from clients | TC-C3-VIS-002,003 | middleware/auth_test.go::TestDenyClientToken_* | covered |
| order: market buy/sell pricing (ask/bid) + commission min(14%,$7) | TC-C3-ORD-001,002 | stock_order_test.go::TestOrder_CreateMarketBuyOrder; wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle; wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding | covered |
| order: limit buy/sell favorable-price + commission min(24%,$12) | TC-C3-ORD-003,004 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| order: stop → market on trigger | TC-C3-ORD-010 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| order: stop-limit two-stage activation | TC-C3-ORD-011 | wf_stop_limit_refund_test.go::TestWF_StopLimit_ExpiryReleasesReservation | covered |
| order: input validation (qty/account/limit/stop/direction/type/auth) | TC-C3-ORD-005..009 | stock_order_test.go::TestOrder_CreateOrder_ZeroQuantity/_InvalidDirection/_InvalidOrderType/_CreateLimitOrder_RequiresLimitValue/_CreateBuyOrder_RequiresAccountID/_CreateOrder_Unauthenticated | covered |
| order: forex buy convert+base-credit + forex constraints | TC-C3-ORD-012..015 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | covered |
| order: account ownership enforcement | TC-C3-ORD-016 | — | covered |
| order: client auto-approved | TC-C3-ORD-017 | stock_order_test.go::TestOrder_ClientOrderAutoApproved | covered |
| order: on-behalf-of-client | TC-C3-ORD-020 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow | covered |
| order: on-behalf-of-fund (fund_holdings) | TC-C3-ORD-021 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| order: concurrency / reservation release | TC-C3-ORD-030 | wf_stock_concurrent_orders_test.go::TestWF_StockConcurrentOrders_RespectsAvailableBalance; wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation | covered |
| order: partial multi-trader fill aggregation | TC-C3-ORD-031 | wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle | covered |
| order: commission-failure resilience | TC-C3-ORD-040 | wf_stock_commission_failure_test.go::TestWF_StockFill_CommissionFailure_TradeStillCompletes | covered |
| order: fill saga no-divergence | TC-C3-ORD-041 | wf_stock_fill_failure_test.go::TestWF_StockFill_AccountServiceFailure_NoDivergence | covered |
| AON: blocks partial fill / full fill | TC-C3-AON-001,002 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | partial |
| margin: flag persisted | TC-C3-MGN-001 | wf_order_types_test.go::TestWF_MultiAssetOrderTypes | covered |
| margin: permission prerequisite | TC-C3-MGN-002 | — | NO-ENDPOINT |
| margin: credit/cash ≥ IMC prerequisite | TC-C3-MGN-003 | — | NO-ENDPOINT |
| margin: IMC = MM×1.1 display | TC-C3-MGN-004 | securities_test.go::TestSecurities_GetStock | covered |
| agent approval: over-limit+needApproval → pending | TC-C3-APV-001 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow | covered |
| agent approval: under-limit auto-approve (boundary) | TC-C3-APV-002 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType | covered |
| agent approval: over-limit requires approval even when needApproval=false | TC-C3-APV-003 | order_service_test.go::TestCreateOrder_Employee_OverLimit_RequiresApproval_EvenWhenNeedApprovalFalse | covered |
| agent approval: multi-currency limit via no-commission conversion | TC-C3-APV-004 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | partial |
| supervisor approve/decline | TC-C3-APV-005,006 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; stock_order_test.go::TestOrder_ApproveOrder_RequiresSupervisor/_RejectOrder_RequiresSupervisor | covered |
| approve/decline once-only + illegal transition | TC-C3-APV-007 | — | covered |
| settlement-passed → decline-only | TC-C3-APV-008 | — | covered |
| approve/reject id validation | TC-C3-APV-009 | stock_order_test.go::TestOrder_GetMyOrder_NotFound | covered |
| order-review portal (supervisor list+filters) | TC-C3-MGMT-001 | stock_order_test.go::TestOrder_ListOrders_Supervisor/_ListOrders_RequiresSupervisor | covered |
| my-orders (agent/client list+filters) | TC-C3-MGMT-010 | stock_order_test.go::TestOrder_ListMyOrders | covered |
| order detail (audit/reservation fields) | TC-C3-MGMT-011 | stock_order_test.go::TestOrder_GetMyOrder/_GetMyOrder_NotFound | covered |
| cancel unfilled portion + release reservation + ownership | TC-C3-MGMT-030,031 | stock_order_test.go::TestOrder_CancelOrder/_CancelOrder_NotFound; wf_stock_reservation_test.go::TestWF_StockBuy_CancelReleasesReservation | covered |
| audit-log entries (approve/decline/limit/reset/tax) | TC-C3-MGMT-040 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; business_audit_handler_test.go | covered |
| portfolio: holdings + realized/unrealized P/L | TC-C3-PRT-001,002 | portfolio_test.go::TestPortfolio_ListHoldings/_FilterByType/_GetSummary/_ListHoldings_Unauthenticated/_ListHoldings_InvalidSecurityType; wf_client_stock_banking_test.go::TestWF_ClientTradesStockAfterBanking | covered |
| portfolio: holding transaction breakdown | TC-C3-PRT-003 | — | covered |
| portfolio: sell qty ≤ held | TC-C3-PRT-010 | wf_stock_sell_all_aggregated_test.go::TestWF_SellAllAcrossAggregatedHolding | covered |
| portfolio: make-public → OTC | TC-C3-PRT-020 | portfolio_test.go::TestPortfolio_MakePublic_InvalidQuantity | covered |
| portfolio: option exercise | TC-C3-PRT-030 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle; portfolio_test.go::TestPortfolio_ExerciseOption_NotFound/_ExerciseOption_Unauthenticated | covered |
| portfolio: dividend history | TC-C3-PRT-040 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: declare | TC-C3-DIV-001 | — | covered |
| dividends: payout 15% client / 0% bank / fund snapshot | TC-C3-DIV-002 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: account routing fallback → RSD | TC-C3-DIV-010 | — | partial |
| dividends: fund dividends listing | TC-C3-DIV-020 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| dividends: quarterly auto cron + qty×price×yield/4 | TC-C3-DIV-030 | — | NO-ENDPOINT |
| tax: self balance (paid-year/unpaid-month) | TC-C3-TAX-001 | tax_test.go::TestTax_ListMyTaxRecords/_EmployeeToken/_Unauthenticated | covered |
| tax: supervisor portal list + filters | TC-C3-TAX-010 | tax_test.go::TestTax_ListTaxRecords/_FilterByUserType/_InvalidUserType | covered |
| tax: manual collect (15%, RSD no-commission, state credited) | TC-C3-TAX-020 | tax_test.go::TestTax_CollectTax/_AgentCannot; wf_tax_collection_test.go::TestWF_TaxCollectionCycle | covered |
| tax: none on loss/unrealized | TC-C3-TAX-030 | wf_tax_collection_test.go::TestWF_TaxCollectionCycle; wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| tax: multiple asset types | TC-C3-TAX-031 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle; wf_tax_collection_test.go::TestWF_TaxCollectionCycle | covered |
| tax: PDF export | TC-C3-TAX-040 | — | NO-ENDPOINT |
| tax: report by year filter | TC-C3-TAX-041 | — | NO-ENDPOINT |
| tax: profit-discrepancy flagging | TC-C3-TAX-042 | — | NO-ENDPOINT |
| DCA: create monthly/weekly | TC-C3-DCA-001 | — | covered |
| DCA: validation (side/interval/day) | TC-C3-DCA-002 | — | covered |
| DCA: pause/resume/cancel + ownership | TC-C3-DCA-003 | — | covered |
| DCA: list/get own | TC-C3-DCA-004 | — | covered |
| DCA: cron fire + insufficient-funds skip+notify | TC-C3-DCA-010 | — | partial |
| watchlist: default add/list/remove + filter | TC-C3-WL-001 | wf_watchlist_named_test.go::TestWF_WatchlistNamedLists | covered |
| watchlist: multiple named lists | TC-C3-WL-010 | wf_watchlist_named_test.go::TestWF_WatchlistNamedLists | covered |
| price alerts: CRUD | TC-C3-ALERT-001 | — | partial |
| price alerts: validation + ownership | TC-C3-ALERT-002 | — | covered |
| actuaries: list + filters + RBAC | TC-C3-ACT-001 | actuary_test.go::TestActuary_ListActuaries/_ListActuaries_AgentCannot/_Unauthenticated | covered |
| actuaries: set limit | TC-C3-ACT-002 | actuary_test.go::TestActuary_SetLimit/_SetLimit_EmptyValue; employee_limits_test.go | covered |
| actuaries: reset used-limit | TC-C3-ACT-003 | actuary_test.go::TestActuary_ResetLimit | covered |
| actuaries: require/skip approval toggle | TC-C3-ACT-004 | actuary_test.go::TestActuary_RequireApproval | covered |
| actuaries: performance feed | TC-C3-ACT-010 | wf_actuary_limit_owner_type_test.go::TestActuaryLimit_EmployeeMeOrder_OwnerType | covered |
| defense Provera 1 — portal layout | TC-C3-E2E-001 | securities_test.go::TestSecurities_ListStocks/_ListFutures/_ListForexPairs/_GetStock | covered |
| defense Provera 2 — buy ForexPair | TC-C3-E2E-002 | wf_stock_cross_currency_test.go::TestWF_StockBuy_CrossCurrency_ConvertedDebit | covered |
| defense Provera 3 — buy stock/futures + approval + portfolio | TC-C3-E2E-003 | wf_order_approval_test.go::TestWF_OrderApprovalWorkflow; wf_stock_buy_sell_test.go::TestWF_StockBuySellCycle | covered |
| defense Provera 4 — buy & exercise option | TC-C3-E2E-004 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| defense Provera 5 — tax end-to-end | TC-C3-E2E-005 | wf_tax_collection_test.go::TestWF_TaxCollectionCycle; tax_test.go::TestTax_CollectTax | covered |

---

## Celina 4 — OTC & Funds

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| OTC stocks — publish sell offer (make public) | TC-C4-OTCSTK-001 | otc_test.go::TestOTC_ListOffers | covered |
| OTC stocks — standing buy offer + cash reservation | TC-C4-OTCSTK-002 | otc_test.go::TestOTC_BuyOffer_MissingAccountID, TestOTC_BuyOffer_InvalidQuantity | covered |
| OTC stocks — browse marketplace + filter | TC-C4-OTCSTK-003 | otc_test.go::TestOTC_ListOffers, TestOTC_ListOffers_FilterBySecurityType, TestOTC_ListOffers_Unauthenticated, TestOTC_ListOffers_InvalidSecurityType | covered |
| OTC stocks — buy from sell offer | TC-C4-OTCSTK-004 | otc_test.go::TestOTC_BuyOffer_InvalidQuantity | covered |
| OTC stocks — sell into buy offer (shares-available guard) | TC-C4-OTCSTK-005 | — | covered |
| OTC stocks — cancel offer (sell/buy) | TC-C4-OTCSTK-006 | — | covered |
| OTC stocks — buy on behalf of client | TC-C4-OTCSTK-007 | — | covered |
| OTC stocks — list my offers | TC-C4-OTCSTK-008 | — | covered |
| OTC options — post listing (sell/buy initiated) | TC-C4-OTCNEG-001 | otc_options_test.go::TestOTCOptions_ClientLifecycle, TestOTCOptions_UnknownTickerRejected, TestOTCOptions_ClientCannotUseForeignAccount | covered |
| OTC options — bid (open negotiation chain) | TC-C4-OTCNEG-002 | otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — counter (each field mutable + old→new history) | TC-C4-OTCNEG-003 | otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — accept (first-accept-wins, cascade-cancel) | TC-C4-OTCNEG-004 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers, otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — withdraw own chain | TC-C4-OTCNEG-005 | — | covered |
| OTC options — reject (parent stays open) | TC-C4-OTCNEG-006 | — | covered |
| OTC options — cancel listing cascade | TC-C4-OTCNEG-007 | — | covered |
| OTC options — active-offers page | TC-C4-OTCNEG-008 | otc_unified_read_test.go::TestSP1_MyNegotiations_HasProvenanceFields, TestSP2b_OfferList_StampsMyNegotiationForBidder | covered |
| OTC options — poster sees all bids / bidder forbidden | TC-C4-OTCNEG-009 | otc_timeline_test.go::TestOTCTimeline_PosterAllowed_BidderForbidden | covered |
| OTC options — concluded-contracts page | TC-C4-OTCNEG-010 | otc_options_test.go::TestOTCOptions_ListMyContractsEmpty, otc_unified_read_test.go::TestSP1_MyContracts_BuyerIsOwner | covered |
| OTC options — deviation color bands ±5/±20% | TC-C4-OTCNEG-011 | — | NO-ENDPOINT |
| OTC options — unread-negotiations indicator | TC-C4-OTCNEG-012 | — | partial |
| OTC contract — accept moves premium + locks seller shares | TC-C4-OTCCON-001 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers | covered |
| OTC contract — cross-currency premium conversion | TC-C4-OTCCON-002 | — | covered |
| OTC contract — multiple per seller, Σ committed ≤ owned | TC-C4-OTCCON-003 | — | covered |
| OTC contract — expired unused frees shares | TC-C4-OTCCON-004 | — | covered |
| OTC contract — get by id + ownership | TC-C4-OTCCON-005 | otc_unified_read_test.go::TestSP1_GetOffer_LocalKindAndMeOwner | covered |
| SAGA exercise — happy path | TC-C4-SAGA-001 | saga_sg_test.go::TestSG01_HappyPath, wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| SAGA exercise — cannot exercise expired/used | TC-C4-SAGA-002 | — | covered |
| SAGA exercise — non-buyer/unknown rejected | TC-C4-SAGA-003 | saga_sg_test.go::TestSG02a_NonBuyerRejected, TestSG02b_UnknownContract | covered |
| SAGA exercise — phase 3/4 failure full compensation ("Poništena") | TC-C4-SAGA-004 | saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds, TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds | covered |
| SAGA exercise — refund retry ≤3 then admin alert | TC-C4-SAGA-005 | — | partial |
| SAGA exercise — double-reserve prevention (concurrent) | TC-C4-SAGA-006 | saga_sg_test.go (invariants), wf_stock_concurrent_orders_test.go | covered |
| SAGA exercise — funds consumed before exec → cancel+refund | TC-C4-SAGA-007 | — | covered |
| SAGA exercise — CHECK_STATUS resume | TC-C4-SAGA-008 | saga_sg_test.go (retry-after-fault) | partial |
| Option tax — seller premium at accept (15% client) | TC-C4-OTCTAX-001 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| Option tax — buyer at exercise (market−strike)×qty−premium | TC-C4-OTCTAX-002 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| Option tax — expired: buyer premium loss, seller none | TC-C4-OTCTAX-003 | — | covered |
| Option tax — bank/aktuar exemption → Profit Banke | TC-C4-OTCTAX-004 | — | covered |
| Fund — create (unique name, min, manager, auto RSD acct) | TC-C4-FUND-001 | investment_funds_test.go::TestInvestmentFunds_CreateAndList, TestInvestmentFunds_DuplicateNameRejected, TestInvestmentFunds_ClientCannotCreate | covered |
| Fund — discovery list/filter/sort | TC-C4-FUND-002 | investment_funds_test.go::TestInvestmentFunds_CreateAndList, wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — detailed view (NAV/liquidity/holdings/profit) | TC-C4-FUND-003 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — update (supervisor) | TC-C4-FUND-004 | — | covered |
| Fund — client invest ≥ min from own account | TC-C4-FUND-005 | investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient | covered |
| Fund — cross-currency invest conversion | TC-C4-FUND-006 | — | covered |
| Fund — client redeem full/partial + fee + target acct | TC-C4-FUND-007 | — | covered |
| Fund — supervisor invest on behalf of bank (no fee) | TC-C4-FUND-008 | — | covered |
| Fund — Moji fondovi (client vs supervisor) | TC-C4-FUND-009 | investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient | covered |
| Fund — partial liquidation when illiquid + notify | TC-C4-FUND-010 | — | NO-ENDPOINT |
| Fund — block deposit while withdrawal pending | TC-C4-FUND-011 | — | NO-ENDPOINT |
| Fund — position/NAV/profit recompute on value change | TC-C4-FUND-012 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — supervisor buys security on behalf of fund | TC-C4-FUND-013 | — | covered |
| Fund — ownership transfer on supervisor-permission removal | TC-C4-FUND-014 | — | covered |
| Fund — recurring DCA into fund | TC-C4-FUND-015 | — | covered |
| Fund dividends — declare | TC-C4-FUNDIV-001 | — | covered |
| Fund dividends — payout fan-out (client 15%, fund/bank exempt) | TC-C4-FUNDIV-002 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — reinvest (DRIP) | TC-C4-FUNDIV-003 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — distribute proportional to share | TC-C4-FUNDIV-004 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — history (fund + me) | TC-C4-FUNDIV-005 | — | covered |
| Fund stats — metrics surface + sort | TC-C4-FUNSTAT-001 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund stats — min-snapshots gate (metrics_available) | TC-C4-FUNSTAT-002 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund stats — detail charts (history + average) | TC-C4-FUNSTAT-003 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Profit Banke — actuary performances | TC-C4-PROFIT-001 | — | covered |
| Profit Banke — bank positions in funds | TC-C4-PROFIT-002 | — | covered |
| Profit Banke — deposit/withdraw as bank | TC-C4-PROFIT-003 | — | covered |
| OTC notif — counter-offer received | TC-C4-OTCNOTE-001 | — | covered |
| OTC notif — offer accepted/withdrawn | TC-C4-OTCNOTE-002 | — | covered |
| OTC notif — contract expiring in N days | TC-C4-OTCNOTE-003 | — | partial |
| OTC negotiation history + filters (status/date/counterparty) | TC-C4-OTCNOTE-004 | otc_unified_read_test.go (read shapes) | covered |
| Defense — Provera 1 OTC trade internal (E2E) | TC-C4-E2E-001 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers | covered |
| Defense — Provera 2 OTC trade external | (see celina-5-cross-bank.md) | otc_sp2b_test.go::TestSP2b_UnifiedRemote* | covered (Celina 5) |

---

## Celina 5 — Cross-Bank

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| two-stack bring-up (gen-bank-stacks, peer registration, distinct OWN_BANK_CODE) | (precondition) TC-C5-SETUP-001 | test-app/workflows/cohort_dry_run_test.go::TestCohortDryRun | covered |
| peer-bank registry: create | TC-C5-SETUP-001 | api-gateway/internal/handler/peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList | covered |
| peer-bank registry: list/read | TC-C5-SETUP-002 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_CreateAndList/_GetUpdateDelete | covered |
| peer-bank registry: update/rotate | TC-C5-SETUP-003 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete | covered |
| peer-bank registry: delete | TC-C5-SETUP-004 | peer_bank_admin_handler_test.go::TestPeerBankAdmin_GetUpdateDelete | covered |
| peer-collision guard (own code/routing) | TC-C5-SETUP-005 | — | covered |
| peer registry RBAC (admin-only) | TC-C5-SETUP-006 | peer_bank_admin_handler_test.go | covered |
| inter-bank payment success (same currency) | TC-C5-PAY-001 | cohort_dry_run_test.go::TestCohortDryRun; sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| bank identified by first 3 digits | TC-C5-PAY-002 | sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| Not-Ready (inactive/nonexistent recipient) → release+notify | TC-C5-PAY-010 | test-app/peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason; peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting | covered |
| sender insufficient funds → reject "Nedovoljno sredstava" | TC-C5-PAY-020 | — | covered |
| Bank B no-response → cancel+refund | TC-C5-PAY-021 | peerbank/server_test.go::TestMock_ConfigureFiveXX_PrepareReturns503 | covered |
| any-step failure → full rollback + reservation release | TC-C5-PAY-022 | interbank-service/internal/handler/reverse_outbound_local_test.go; inline_rollback_test.go | covered |
| audit-trail fields (sender/receiver bank, send/receive time, status) | TC-C5-PAY-030 | api-gateway/internal/handler/peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus | partial |
| client-facing cross-bank status polling | TC-C5-PAY-031 | peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_UUIDResolvesViaGetTxStatus/_OTCCrossBankStatus | covered |
| prose Ready{endValue,FX,commission} message | TC-C5-PAY-040 | — | NO-ENDPOINT |
| receiver-side currency conversion (cross-currency credit) | TC-C5-PAY-041 | — | NO-ENDPOINT |
| commission credited to receiving bank (Bank B) | TC-C5-PAY-042 | — | NO-ENDPOINT |
| literal 10-second no-response SLA | TC-C5-PAY-043 | — | NO-ENDPOINT |
| inbound NEW_TX transfer → YES vote (byte-pinned) | TC-C5-PROTO-001 | peer_tx_handler_test.go::TestPostInterbank_NewTx_SpecShape/TestPeerTxHandler_NewTx_YesPassthrough | covered |
| COMMIT_TX/ROLLBACK_TX correlation + 204 | TC-C5-PROTO-002 | sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| NO UNBALANCED_TX (no posting) | TC-C5-PROTO-003 | peer_tx_handler_test.go::TestPostInterbank_NewTx_NoVote_ReattachesPosting | covered |
| NO NO_SUCH_ACCOUNT | TC-C5-PROTO-004 | — | covered |
| NO UNACCEPTABLE_ASSET (inactive/own-routing debit) | TC-C5-PROTO-005 | — | covered |
| NO NO_SUCH_ASSET (currency mismatch) | TC-C5-PROTO-006 | — | covered |
| NO INSUFFICIENT_ASSET (singular, byte-pinned) | TC-C5-PROTO-007 | — | covered |
| NO option codes (AMOUNT_INCORRECT/USED_OR_EXPIRED/NEGOTIATION_NOT_FOUND) | TC-C5-PROTO-008 | — | covered |
| receiver-side 202 async + retransmit | TC-C5-PROTO-009 | peer_tx_handler_test.go::TestPostInterbank_NewTx_Pending_Returns202 | covered |
| idempotence-key replay returns cached vote | TC-C5-PROTO-010 | interbank-service/internal/handler/peer_tx_grpc_handler_test.go | covered |
| PeerAuth failures → 401 empty body | TC-C5-PROTO-011 | peerbank/server_test.go::TestMock_BadSignature_Returns401 | covered |
| unknown messageType → 400 (501 only if backend Unimplemented) | TC-C5-PROTO-012 | peer_tx_handler_test.go::TestPeerTxHandler_UnknownMessageType_Returns400/TestPeerTxHandler_NewTx_Unimplemented_Returns501 | covered |
| CHECK_STATUS read endpoint (frozen GET) | TC-C5-PROTO-013 | peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath/_Unknown | covered |
| accept-shape NEW_TX (OPTION-as-asset, byte-pinned) | TC-C5-PROTO-014 | peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches | covered |
| exercise-shape NEW_TX (OPTION-pseudo-acct + STOCK, byte-pinned) | TC-C5-PROTO-015 | — (two-stack: otc_sp3_test.go skips) | partial |
| OTC discovery: /public-stock bare array + opaque seller id | TC-C5-DISC-001 | sitx_public_stock_seller_id_test.go::TestSITX_PublicStockSellerIdAndOpaqueBuyerId; peer_otc_handler_test.go::TestPeerOTC_GetPublicStocks | covered |
| OTC discovery: /public-option-offers (sell_initiated only) | TC-C5-DISC-002 | — | covered |
| friendly-name resolution: /user display name | TC-C5-DISC-003 | peer_user_handler_test.go::TestPeerUser_Found/_NotFound_404/_BadRid_400 | covered |
| negotiation: POST /negotiations (create) | TC-C5-NEG-001 | peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation/_NumericAmount/_SellerBankForm/_ForwardsParentOfferId | covered |
| negotiation: participant-id §2.3 validation matrix | TC-C5-NEG-002 | peer_otc_handler_test.go::TestPeerOTC_CreateNegotiation_OpaqueBuyerId/_RejectsOverlongOrEmptyId; sitx_public_stock_seller_id_test.go | covered |
| negotiation: PUT counter — turn/closed 409 guards | TC-C5-NEG-003 | — | covered |
| negotiation: GET state (OtcNegotiation) | TC-C5-NEG-004 | peer_otc_handler_test.go::TestPeerOTC_GetNegotiation/_GetNegotiation_BadRid_400 | covered |
| negotiation: DELETE soft-cancel (isOngoing=false) | TC-C5-NEG-005 | peer_otc_handler_test.go::TestPeerOTC_DeleteNegotiation_Returns204 | covered |
| negotiation: GET …/accept (frozen GET-mutates) → contract | TC-C5-NEG-006 | peer_otc_handler_test.go::TestPeerOTC_AcceptNegotiation_Dispatches | covered |
| client cross-bank bid (client + bank principal) | TC-C5-OTC-001 | otc_sp3_test.go::TestSP3_BankBidsCrossBank_RequiresTwoStacks | partial |
| client cross-bank counter/accept/cancel (client↔client & supervisor↔supervisor) | TC-C5-OTC-002 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks/TestSP3_PeerBidsOnOurBankOffer_RequiresTwoStacks | partial |
| own chains merged (local+remote) | TC-C5-OTC-003 | test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteNegotiation_MergesIntoMyNegotiations | covered |
| contracts merged (local+remote) | TC-C5-OTC-010 | test-app/workflows/otc_unified_read_test.go::TestSP1_RemoteContract_AppearsWithKindRemote | covered |
| cross-bank buy_initiated bid rejected | TC-C5-OTC-040 | — | covered |
| cross-bank exercise happy path (buyer paid+receives, seller delivers, reservations cleaned) | TC-C5-SAGA-001 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks; saga_sg_test.go::TestSG01_HappyPath (local analogue) | partial |
| decline OTM option (lose only premium) | TC-C5-SAGA-002 | — | covered |
| RESERVE_SHARES_FAIL → release buyer funds | TC-C5-SAGA-010 | saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds (local analogue) | partial |
| ownership-transfer failure → refund+return shares+ACTIVE | TC-C5-SAGA-011 | saga_sg_test.go::TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds (local analogue) | partial |
| compensation retry → dead-letter escalation | TC-C5-SAGA-012 | — | partial |
| CHECK_STATUS resume after comms break | TC-C5-SAGA-013 | peer_tx_status_handler_test.go::TestPeerTxStatusHandler_HappyPath; peerbank/server_test.go::TestMock_StatusKnown_ReturnsState/_StatusUnknown_Returns404 | covered |
| concurrent double-exercise/double-accept prevented (CAS) | TC-C5-SAGA-014 | — | partial |
| expired unused option unlocks seller shares | TC-C5-SAGA-020 | — | covered |
| cross-bank seller premium tax (15% at accept) | TC-C5-TAX-001 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle (local analogue) | partial |
| cross-bank seller strike-gain tax at exercise | TC-C5-TAX-002 | — | partial |
| aktuar/bank exemption → bank profit | TC-C5-TAX-010 | — | partial |
| cross-bank buyer exercise tax | TC-C5-TAX-030 | — | NO-ENDPOINT |
| expired option premium loss / no extra seller tax | TC-C5-TAX-031 | — | partial |
| adversarial: cannot debit unowned account (cross-bank payment) | TC-C5-ADV-001 | peer_tx_dispatcher_handler_test.go::TestPeerTxDispatcher_CreatePayment_ForeignFromNotOwned | covered |
| adversarial: exercise strike-account gate (all principals) | TC-C5-ADV-002 | — | covered |
| adversarial: non-buyer cannot exercise (existence privacy) | TC-C5-ADV-010 | saga_sg_test.go::TestSG02a_NonBuyerRejected/TestSG02b_UnknownContract (local analogue) | partial |
| adversarial: forged-money exercise legs rejected | TC-C5-ADV-011 | — | covered |
| adversarial: buyer-routing spoof on inbound bid rejected | TC-C5-ADV-012 | peer_otc_handler_test.go (participant-id validation) | covered |
| defense Provera 2 — OTC trade external (chain) | TC-C5-E2E-001 | otc_sp3_test.go::TestSP3_BankCounterAcceptExerciseCrossBank_RequiresTwoStacks | partial |
| E2E — inter-bank transfer success + audit (chain) | TC-C5-E2E-002 | cohort_dry_run_test.go::TestCohortDryRun; sitx_conformance_test.go::TestSITXConformance_OutboundNewTxIsSpecShaped | covered |
| E2E — cancel on bad/non-responsive receiver (chain) | TC-C5-E2E-010 | peerbank/server_test.go::TestMock_ConfigureNotReady_PassesThroughReason/_ConfigureFiveXX_PrepareReturns503 | covered |
| E2E — reject insufficient funds (chain) | TC-C5-E2E-011 | — | covered |
| defense Provera 3 — cross-bank payment DIFFERENT currency + Bank B fee | TC-C5-E2E-030 | — | NO-ENDPOINT |

---

## Cross-cutting — Verification

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| Create challenge (code_pull, default) + persisted pending row | TC-VERIF-001 | verification_test.go::TestVerification_CreateChallenge; verification_service_more_test.go::TestCreateChallenge_HappyPath_PublishesEvent | covered |
| challenge-created → mobile inbox item + mobile-push | TC-VERIF-002 | wf_mobile_verification_test.go::TestMobileVerification_BrowserChallengeVisibleOnMobile | covered |
| Create challenge: unauthenticated → 401 | TC-VERIF-010 | verification_test.go::TestVerification_UnauthenticatedRejected | covered |
| Create challenge: missing/zero source_id → 400 | TC-VERIF-011 | verification_service_more_test.go::TestCreateChallenge_ZeroSourceID | covered |
| Create challenge: invalid source_service → 400 | TC-VERIF-012 | verification_service_more_test.go::TestCreateChallenge_InvalidSourceService | covered |
| Method qr_scan rejected → 400 | TC-VERIF-020 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_QRScanDisabled | covered |
| Method number_match rejected → 400 | TC-VERIF-021 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_NumberMatchDisabled | covered |
| Method email rejected/removed → 400 | TC-VERIF-022 | verification_test.go::TestVerification_InvalidMethodRejected; verification_service_test.go::TestValidMethods_EmailDisabled | covered |
| Poll status pending→verified | TC-VERIF-030 | verification_test.go::TestVerification_GetChallengeStatus; wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile | covered |
| Browser submit correct/bypass → verified + challenge-verified event | TC-VERIF-031 | verification_test.go::TestVerification_SubmitBypassCode | covered |
| Mobile submit response → verified + device bound (E2E) | TC-VERIF-032 | wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile, ::TestMobileVerification_MobileCreatedChallengeVisible | covered |
| ACK inbox item removes from pending | TC-VERIF-033 | wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending | covered |
| Multiple concurrent challenges all verifiable | TC-VERIF-034 | wf_mobile_verification_test.go::TestMobileVerification_MultipleChallengesVisible | covered |
| Wrong code → success:false, attempt counter++ | TC-VERIF-040 | verification_test.go::TestVerification_SubmitWrongCode; verification_service_test.go::TestCheckResponse_CodePull_WrongCode | covered |
| 3 wrong → challenge failed + challenge-failed event; txn not executable | TC-VERIF-041 | wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry | covered |
| Boundary: 2 wrong then correct → verified | TC-VERIF-042 | verification_service_test.go::TestValidateChallengeState_MaxAttemptsReached (unit) | partial |
| Submit after 5-min expiry → 409; no expiry event | TC-VERIF-053 | verification_service_test.go::TestValidateChallengeState_Expired; grpc_handler_test.go::TestHandlerSubmitVerification_Expired_FailedPrecondition | covered |
| Execute payment/transfer with non-verified challenge → 409 | TC-VERIF-050 | wf_verification_retry_test.go::TestWF_PaymentVerificationFailureAndRetry | covered |
| Submit/status on nonexistent challenge → 404 | TC-VERIF-051 | grpc_handler_test.go::TestHandlerSubmitCode_NotFound, ::TestHandlerGetChallengeStatus_NotFound | covered |
| Submit on already-consumed (verified/failed) → 409 | TC-VERIF-052 | verification_service_test.go::TestValidateChallengeState_AlreadyVerified, ::TestValidateChallengeState_StatusFailed | covered |
| Fast-path: execute without challenge_id → no verification | TC-VERIF-060 | — | NO-ENDPOINT |
| verification.skip held by supervisor/admin (capability) | TC-VERIF-061 | — | partial |
| verification.skip absent from basic/agent | TC-VERIF-062 | — | partial |
| Code submit has no ownership check (adversarial GAP) | TC-VERIF-070 | — | partial |
| Mobile endpoints reject non-mobile token → 403 | TC-VERIF-071 | verification_test.go::TestVerification_AckEndpointRequiresMobileAuth | covered |
| Mobile submit from second device → device mismatch 409 | TC-VERIF-072 | grpc_handler_test.go::TestHandlerSubmitVerification_DeviceMismatch_FailedPrecondition | covered |
| Biometric verify: success + ownership + biometrics-enabled | TC-VERIF-073 | grpc_handler_test.go::TestHandlerVerifyByBiometric_Success, ::_OwnershipDenied, ::_BiometricsDisabled | covered |
| QR verify endpoint unusable (no qr_scan challenge) | TC-VERIF-074 | — | NO-ENDPOINT |
| Quick Approve: approve from push w/o code (biometric) | TC-VERIF-080 | grpc_handler_test.go::TestHandlerVerifyByBiometric_Success | partial |
| Quick Approve: 5-min no-response → expires | TC-VERIF-081 | verification_service_test.go::TestValidateChallengeState_Expired, ::TestExpireOldChallenges_MarksOnlyExpired | partial |
| Quick Approve: dedicated push-approve endpoint | TC-VERIF-082 | — | NO-ENDPOINT |
| Gated-action matrix (payment/transfer gated; OTC/limit not gated) | (matrix) | wf_verification_retry_test.go; helpers_test.go::createAndExecutePayment/createAndExecuteTransfer | partial |

---

## TODO_final — Notifications & Mobile

| Feature / sub-feature / option | TC IDs | Existing Go test | Status |
|---|---|---|---|
| C1 account-locked email | TC-NOTIF-001 | — | NO-ENDPOINT |
| C2 payment-executed notify (email+inapp+push) | TC-NOTIF-002 | — | partial (in-app only) |
| C2 transfer-executed notify | TC-NOTIF-003 | — | partial (in-app only) |
| C2 limit-changed notify | TC-NOTIF-004 | wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange | covered (email+in-app) |
| C2 card-blocked notify | TC-NOTIF-005 | — | covered (email+in-app) |
| C2 card-unblocked notify | TC-NOTIF-006 | — | covered (email+in-app) |
| C2 credit-created notify | TC-NOTIF-007 | — | partial (in-app only) |
| C2 credit-approved notify | TC-NOTIF-008 | — | partial (in-app only; LoanApproved email defined-but-unused) |
| C3 order-created→Pending notify | TC-NOTIF-009 | — | partial (in-app only) |
| C3 order-approved notify | TC-NOTIF-010 | — | partial (in-app only) |
| C3 order-rejected notify | TC-NOTIF-011 | — | partial (in-app only) |
| C3 order-fully-executed notify | TC-NOTIF-012 | — | partial (in-app only) |
| C3 order-partially-filled notify | TC-NOTIF-013 | — | partial (in-app only) |
| C3 order-auto-cancelled notify | TC-NOTIF-014 | — | partial (in-app only; no settlement-expiry auto-cancel cron) |
| C3 price-alert-fired notify | TC-NOTIF-015 | — | partial (in-app only) |
| C3 dividend-received notify | TC-NOTIF-016 | — | NO-ENDPOINT |
| C3 tax-deducted notify | TC-NOTIF-017 | — | NO-ENDPOINT |
| C3 recurring-order-skipped notify | TC-NOTIF-018 | — | partial (in-app only) |
| C4 OTC counter-offer notify | TC-NOTIF-019 | — | partial (in-app only) |
| C4 OTC offer-accepted notify | TC-NOTIF-020 | — | partial (in-app only) |
| C4 OTC offer-withdrawn notify | TC-NOTIF-021 | — | partial (in-app only) |
| C4 option-contract-expiring-N-days notify | TC-NOTIF-022 | — | partial (in-app only) |
| Mobile push for business events (systemic) | TC-NOTIF-023 | — | NO-ENDPOINT |
| Watchlist ±5% daily-move notify (bonus) | TC-NOTIF-024 | — | covered (in-app) |
| In-app notifications: list | TC-NOTIF-030 | wf_notification_coverage_test.go::TestWF_NotificationCoverage_LimitChange | covered |
| In-app notifications: unread-count | TC-NOTIF-031 | — | covered |
| In-app notifications: mark-read | TC-NOTIF-032 | — | covered |
| In-app notifications: mark-all-read | TC-NOTIF-033 | — | covered |
| In-app notifications: ownership | TC-NOTIF-034 | — | covered |
| Mobile: view own cards | TC-MOBILE-001 | — | covered |
| Mobile: block own card | TC-MOBILE-002 | — | covered (temporary-block) |
| Mobile: unblock not allowed (banker-only) | TC-MOBILE-003 | — | covered (by-design) |
| Mobile: view accounts + balances | TC-MOBILE-004 | — | covered |
| Mobile: transaction history (per account) | TC-MOBILE-005 | — | covered |
| Mobile: per-card transaction history | TC-MOBILE-006 | — | NO-ENDPOINT |
| Mobile: menjačnica calculate + currencies | TC-MOBILE-007 | — | covered |
| Mobile: kursna lista current | TC-MOBILE-008 | — | covered |
| Mobile: kursna lista 30-day history | TC-MOBILE-009 | — | NO-ENDPOINT |
| Mobile: upcoming loan-installment view | TC-MOBILE-010 | — | covered |
| Quick Approve: approve without code (biometric) | TC-MOBILE-020 | wf_mobile_verification_test.go::TestMobileVerification_VerifyFromMobile | covered |
| Quick Approve: 5-minute expiry | TC-MOBILE-021 | — | covered |
| Quick Approve: approve-after-expiry rejected | TC-MOBILE-022 | — | covered |
| Quick Approve: dedicated push-button approve | TC-MOBILE-023 | wf_mobile_verification_test.go::TestMobileVerification_AckRemovesChallengeFromPending | partial (biometric only; ack≠approve) |

---

## Gap list (every `partial` / `NO-ENDPOINT` row)

The "no silent caps" checklist: every non-`covered` row, grouped by source file, with a one-line note on
what is missing. **NE** = NO-ENDPOINT (no implementing endpoint/behaviour); **P** = partial (implemented
but only partly, or only unit/analogue-level test coverage, or a documented spec divergence).

### Celina 1 — User Management (16 partial, 2 NO-ENDPOINT)

- **TC-C1-LOCK-040** (P) — 4th-attempt-allowed boundary only exercised indirectly via the lock test; no dedicated boundary assertion.
- **TC-C1-LOCK-043** (P) — 30-min lock auto-expiry recovery has no test (asserts impl 30 min vs Celina-1's 10 min).
- **TC-C1-LOCK-050** (NE) — account-locked email is required by Celina 1 but no `EmailTypeAccountLocked`/template exists; user never notified on lock.
- **TC-C1-LOCK-051** — ✅ FIXED 2026-06-07: `ResetPassword` now calls `UnlockAccount` (test `TestResetPassword_UnlocksLockedAccount`). Now **covered**.
- **TC-C1-PWD-062** (P) — reset-with-valid-token positive path has no integration test (only invalid-token covered).
- **TC-C1-PWD-063** (P) — expired/invalid reset-token only covered by an activation-token sibling; reset-link TTL is 1h (E2E says 15 min).
- **TC-C1-PWD-064** (P) — reset confirm-mismatch path has no test.
- **TC-C1-ACT-080** (P) — browser activation flow only exercised via the mobile variant; no browser-activation workflow test.
- **TC-C1-ACT-082** (P) — resend-activation email has no test.
- **TC-C1-EMP-100b** (NE) — no create-time `active` flag; inactive-at-create only reachable via post-create `PUT {active:false}`.
- **TC-C1-EMP-113** (P) — "cannot edit admin employee" guard has no test.
- **TC-C1-CLI-143** (P) — DOB-not-future rule spec'd but enforcement unverified; no test.
- **TC-C1-CLI-144** (P) — phone digits/`+` rule unverified; only email-format covered via binding.
- **TC-C1-TOK-154/155/156/157** (P×4) — sessions list / revoke-one / revoke-others / login-history only have gateway unit tests; no end-to-end workflow test.
- **TC-C1-E2E-200** (P) — defense Provera 1 (create→activate→login) only as a loose chain, not a single named E2E test.

### Celina 2 — Core Banking (31 partial, 5 NO-ENDPOINT)

- **TC-C2-ACC-005a..f** (P) — personal subtypes + maintenance-fee mapping have no test.
- **TC-C2-ACC-006a..c** (P) — business subtypes (DOO/AD/Fondacija) have no test.
- **TC-C2-ACC-007/008** (P) — auto-card checkbox on/off has no test.
- **TC-C2-ACC-012** (P) — client `/me` accounts list (active-only, sorted) has no test.
- **TC-C2-ACC-013/014** (P) — business-account detail (company info) path has no test.
- **TC-C2-ACC-001 (number format)** (P) — bank-prefix + check-digit format invariant not asserted.
- **TC-C2-BANK-002** (P) — last-RSD / last-FX delete-guard has no test.
- **TC-C2-PAY-006** (P) — wrong-owner source-account (403) has no test.
- **TC-C2-PAY-008** (P) — inactive/nonexistent recipient path has no test.
- **TC-C2-PAY-009** (P) — over client daily/monthly limit boundary has no test.
- **TC-C2-PAY-020** (NE) — cross-currency payment between different clients with FX: payments are single-currency; FX lives only in transfers.
- **TC-C2-TRF-003** (P) — transfer preview (rate + commission) has no test.
- **TC-C2-TRF-004** (P) — intra-client transfer guard (reject other client) has no test.
- **TC-C2-TRF-006** (P) — reserved-funds-stays-0 (internal instant) semantics has no test.
- **TC-C2-FX-004** (P) — 2-leg via-RSD per-leg-commission only exercised via a transfer, not the calculator directly.
- **TC-C2-CARD-002/003** (P) — max-2-physical / max-1-per-authorized-person boundaries have no test.
- **TC-C2-CARD-005** (P) — client-blocks-own / employee-unblocks split has no test.
- **TC-C2-CARD-006** (P) — deactivated-cannot-reactivate has no test (no activate route by design).
- **TC-C2-CARD-009** (NE) — virtual `unlimited` usage_type not creatable via the API (gateway `oneOf` only single_use/multi_use).
- **TC-C2-CARD-014** (P) — authorized-person (business) card flow has no test.
- **TC-C2-CARD-015** (NE) — multi-currency card-spend fee (2%+0.5%): no card-spend/POS endpoint exists.
- **TC-C2-LOAN-004** (P) — approval-limit gate (`MaxLoanApprovalAmount`) has no test.
- **TC-C2-LOAN-007** (P) — loan currency-must-match-account rule has no test.
- **TC-C2-LOAN-009/010** (P) — variable-rate tier recalculation + interest-tier/bank-margin config have no test.
- **TC-C2-INST-001/002** (P) — installment schedule/formula + rate-tier-by-amount boundary only partly covered.
- **TC-C2-INST-005/006** (NE×2) — automatic monthly installment deduction (success / overdue+notice) is a cron with no API trigger; untested.
- **TC-C2-FEE-001** (P) — transfer-fee rule CRUD + stacking only exercised via a fee'd payment.
- **TC-C2-E2E-P1/P2/P3/P4/P6** (P×5) — defense Provere 1,2,3,4,6 only as loose chains, not single named E2E tests.

### Celina 3 — Securities (7 partial, 8 NO-ENDPOINT)

- **TC-C3-EXC-030** (NE) — "Berza je zatvorena" exchange-closed rejection not server-enforced (order accepted, fill deferred).
- **TC-C3-EXC-031** (P) — after-hours slow-fill + `after_hours` flag has no test.
- **TC-C3-VIS-002/003** — ✅ FIXED 2026-06-07: gateway `DenyClientToken()` 403s clients on `/securities/forex*` + `/securities/options*` (live-verified). Now **covered**.
- **TC-C3-AON-001/002** (P) — All-or-None partial-block / full-fill only via the multi-asset order test.
- **TC-C3-MGN-002** (NE) — margin permission prerequisite not gated server-side.
- **TC-C3-MGN-003** (NE) — margin credit/cash ≥ IMC prerequisite not enforced (IMC display-only).
- **TC-C3-APV-003** — ✅ FIXED 2026-06-07: approval gate is now the spec's disjunction (`decideNeedsApproval`, 9-case test; live-verified). Now **covered**.
- **TC-C3-APV-004** (P) — multi-currency limit via no-commission conversion only via the cross-currency order test.
- **TC-C3-DIV-010** (P) — dividend account-routing fallback → RSD has no test.
- **TC-C3-DIV-030** (NE) — quarterly auto-cron + qty×price×yield/4 formula: dividends are admin-declared + manual payout only.
- **TC-C3-TAX-040/041/042** (NE×3) — tax PDF export, report-by-year filter, profit-discrepancy flagging have no endpoints.
- **TC-C3-DCA-010** (P) — DCA cron fire + insufficient-funds skip+notify has no test.
- **TC-C3-ALERT-001** (P) — price-alert CRUD has no test.

### Celina 4 — OTC & Funds (4 partial, 3 NO-ENDPOINT)

- **TC-C4-OTCNEG-011** (NE) — deviation color bands ±5/±20% are frontend-only; raw revision data is exposed, no dedicated API.
- **TC-C4-OTCNEG-012** (P) — unread-negotiations indicator is an implicit read-receipt; no explicit mark-read mutation.
- **TC-C4-SAGA-005** (P) — refund-retry≤3-then-admin-alert realized only on the cross-bank SI-TX path; local admin-alert email not wired.
- **TC-C4-SAGA-008** (P) — CHECK_STATUS resume proven only via force-fail-then-retry; no dedicated local saga-step query endpoint.
- **TC-C4-FUND-010** (NE) — partial liquidation when illiquid + deferred-payout notify is a §24 follow-up; redeem 409s on short cash today.
- **TC-C4-FUND-011** (NE) — block-deposit-while-withdrawal-pending has no endpoint (tied to the liquidation follow-up).
- **TC-C4-OTCNOTE-003** (P) — "contract expiring in N days" reminder is in-app only; email gap.

### Celina 5 — Cross-Bank (15 partial, 6 NO-ENDPOINT)

- **TC-C5-PAY-030** (P) — audit-trail data exists but is split across two status endpoints, not one combined log object.
- **TC-C5-PAY-040** (NE) — prose `Ready{endValue,FX,commission}` message: the wire reply is a `TransactionVote`, no such payload.
- **TC-C5-PAY-041** (NE) — receiver-side currency conversion: SI-TX postings balance per asset; no execution-time FX on the wire.
- **TC-C5-PAY-042** (NE) — commission credited to receiving Bank B: the fee model is sender-side only.
- **TC-C5-PAY-043** (NE) — literal 10-second no-response SLA: refund is via replay-cron cap + 10-min reservation TTL.
- **TC-C5-PROTO-015** (P) — exercise-shape NEW_TX byte-pinning needs two stacks; the relevant Go test skips.
- **TC-C5-OTC-001/002** (P×2) — client cross-bank bid / counter-accept-cancel only via `*_RequiresTwoStacks` skips.
- **TC-C5-SAGA-001** (P) — cross-bank exercise happy path covered only by the local saga analogue (two-stack test skips).
- **TC-C5-SAGA-010/011** (P×2) — RESERVE_SHARES_FAIL / ownership-transfer-failure compensation covered only by local analogues.
- **TC-C5-SAGA-012** (P) — compensation-retry → dead-letter escalation has no direct test.
- **TC-C5-SAGA-014** (P) — concurrent double-exercise/double-accept CAS prevention has no cross-bank test.
- **TC-C5-TAX-001/002/010/031** (P×4) — cross-bank seller premium/strike-gain tax, aktuar/bank exemption, and expired-premium-loss covered only by local analogues.
- **TC-C5-TAX-030** (NE) — cross-bank buyer exercise tax: the frozen exercise wire carries neither premium nor market price.
- **TC-C5-ADV-010** (P) — non-buyer-cannot-exercise (existence privacy) covered only by the local analogue.
- **TC-C5-E2E-001** (P) — defense Provera 2 (OTC trade external chain) covered only by a two-stack skip.
- **TC-C5-E2E-030** (NE) — defense Provera 3 (cross-bank payment DIFFERENT currency + Bank B fee): no conformant wire representation.

### Cross-cutting — Verification (7 partial, 3 NO-ENDPOINT)

- **TC-VERIF-042** (P) — 2-wrong-then-correct boundary only covered by a unit test.
- **TC-VERIF-060** (NE) — fast-path execute without `challenge_id` (no verification required) has no test; the gate is structural, not permission-checked.
- **TC-VERIF-061/062** (P×2) — `verification.skip` presence/absence on roles is decorative (no execute path checks it); seed-only.
- **TC-VERIF-070** (P) — code submit has no caller-ownership check (adversarial gap: any caller can verify any challenge).
- **TC-VERIF-074** (NE) — QR verify endpoint unusable (no qr_scan challenge can be created).
- **TC-VERIF-080** (P) — Quick Approve via push approximated by biometric only.
- **TC-VERIF-081** (P) — Quick Approve 5-min expiry only covered by unit tests.
- **TC-VERIF-082** (NE) — dedicated push-approve endpoint does not exist.
- **Gated-action matrix** (P) — only payment/transfer are verification-gated; OTC exercise / limit changes are not gated.

### TODO_final — Notifications & Mobile (17 partial, 6 NO-ENDPOINT)

- **TC-NOTIF-001** (NE) — C1 account-locked email never published.
- **TC-NOTIF-002/003** (P×2) — payment/transfer executed notify is in-app only; email gap.
- **TC-NOTIF-007/008** (P×2) — credit created/approved notify is in-app only (LoanApproved email defined-but-unused).
- **TC-NOTIF-009..015, 018** (P×8) — every C3 order-lifecycle / price-alert / recurring-skip notify is in-app only; email + push gaps.
- **TC-NOTIF-014 note** — also no settlement-expiry auto-cancel cron (PDF auto-cancel maps onto generic cancel).
- **TC-NOTIF-016** (NE) — dividend-received: no in-app/email/push at all.
- **TC-NOTIF-017** (NE) — tax-deducted: no in-app/email/push at all.
- **TC-NOTIF-019..022** (P×4) — every C4 OTC negotiation/contract notify is in-app only; email gap.
- **TC-NOTIF-023** (NE) — systemic: business events are never delivered by mobile push (only verification challenges are).
- **TC-MOBILE-006** (NE) — per-card transaction history has no endpoint (history is per-account only).
- **TC-MOBILE-009** (NE) — 30-day kursna-lista history has no endpoint (exchange-service stores current rate only).
- **TC-MOBILE-023** (P) — dedicated push-button Quick Approve is biometric-only; `ack` is delivery-only, not approval.
