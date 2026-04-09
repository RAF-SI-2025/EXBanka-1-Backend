# Comprehensive Testing Design

**Date:** 2026-04-04
**Scope:** Celine 1, 2, 3 — User management, core banking, securities trading
**Goal:** Full test coverage (unit + integration) that validates spec behavior and catches regressions

## 1. Overview

This design covers checking, fixing, and adding tests across the entire EXBanka backend. The work is organized in 5 phases:

1. **Audit & Fix** — run all tests, fix failures, verify correctness of passing tests
2. **Shared Test Infrastructure** — build reusable helpers to eliminate duplication
3. **Unit Tests** — fill gaps per service (service + handler layers), ~185 new tests
4. **Integration Tests** — fix existing test-app tests + 14 new cross-service workflows
5. **Verify** — full suite green, spec compliance confirmed

### Principles

- Tests validate **spec behavior** adapted to current implementation (APIs, models, response shapes)
- Shared helpers for auth, verification, client/employee setup — no duplicate code
- Unit tests mock repos and external gRPC clients; integration tests hit the real stack
- Every test checks response bodies and side effects, not just HTTP status codes

---

## 2. Shared Test Infrastructure

### 2.1 Unit Test Helpers — `contract/testutil/` (new package)

Importable by all services for consistent unit test setup:

- `SetupTestDB(models ...interface{}) *gorm.DB` — in-memory SQLite with auto-migrate
- `MockKafkaProducer` — struct that captures published events for assertion, implements each service's producer interface
- `RequireGRPCCode(t, err, codes.Code)` — assert gRPC error status
- `RequireNoGRPCError(t, err)` — assert no error

### 2.2 Integration Test Helpers — `test-app/workflows/helpers_test.go`

Existing helpers to keep as-is:
- `setupActivatedClient(t, adminC) → (clientID, accountNumber, clientC, email)`
- `setupAgentEmployee(t, adminC) → (empID, agentC, email)`
- `setupSupervisorEmployee(t, adminC) → (empID, supervisorC, email)`
- `getAccountBalance(t, client, accountNumber) → float64`
- `getBankRSDAccount(t, client) → (accountNumber, balance)`
- `scanKafkaForActivationToken(t, email) → token`
- `scanKafkaForVerificationCode(t, email) → code`
- `parseJSONBalance(t, body, field) → float64`
- `getFirstStockListingID(t, client) → (stockID, listingID)`
- `getFirstFuturesID / getFirstForexPairID / getFirstOptionID`

New helpers to add:

**Verification helpers:**
- `createAndVerifyChallenge(t, client, sourceService, sourceID) → challengeID` — creates challenge via POST /api/verifications, extracts verification code from Kafka email topic, submits code, returns challengeID. Replaces the current `createVerificationAndGetChallengeID` that uses hardcoded bypass "111111".
- `createChallengeOnly(t, client, sourceService, sourceID) → (challengeID, code)` — creates challenge, extracts code from Kafka, returns both without submitting. For tests that need to test the verification step itself.
- `submitVerificationCode(t, client, challengeID, code)` — POSTs the code and asserts 200.

**Setup helpers:**
- `setupActivatedClientWithForeignAccount(t, adminC, currency) → (clientID, rsdAccountNum, foreignAccountNum, clientC, email)` — client + RSD account + foreign currency account, both funded.
- `setupClientWithCard(t, adminC, brand) → (clientID, accountNum, cardID, clientC, email)` — client + account + approved card.

**Transaction helpers:**
- `createAndExecutePayment(t, fromClient, toAccountNum, amount float64, email string) → paymentID` — create payment + verify via challenge + execute, returns paymentID.
- `createAndExecuteTransfer(t, client, fromAccountNum, toAccountNum, amount float64, email string) → transferID` — create transfer + verify via challenge + execute.

**Stock helpers:**
- `buyStock(t, client, listingID uint64, quantity int, email string) → orderID` — place market buy + wait for fill.
- `waitForOrderFill(t, client, orderID, timeout time.Duration)` — poll GET /api/orders/{id} until is_done=true or timeout.
- `createLoanAndApprove(t, adminC, clientC, clientID int, loanType string, amount float64, accountNum string) → loanID` — submit request + admin approves.

**Assertion helpers:**
- `assertBalanceChanged(t, client, accountNum string, before float64, expectedDelta float64)` — fetches current balance, compares to before+expectedDelta with tolerance for floating point.

**Deduplication rules:**
- Every test needing an authenticated admin → `loginAsAdmin()`
- Every test needing a funded client → `setupActivatedClient()`
- Every test needing payment/transfer execution → `createAndExecutePayment()` / `createAndExecuteTransfer()`
- Every test needing verification → `createAndVerifyChallenge()` (never inline Kafka scanning)
- Every test needing a stock purchase → `buyStock()`
- No test file contains its own Kafka scanning logic — always use shared helpers

---

## 3. Unit Tests Per Service

### Layer convention
- **Service layer tests**: mock repository interfaces + external gRPC clients, test business logic
- **Handler layer tests**: mock service interface, test gRPC request→response mapping and error code translation

### 3.1 Tier 1 — Critical Gaps

#### verification-service (0 → ~25 tests)

**Service layer (~18 tests):**
- CreateChallenge with `code_pull` method → challenge created, code generated, Kafka event published
- CreateChallenge with `email` method → challenge created, email sent via Kafka
- CreateChallenge with `qr_scan` method → returns error (method disabled, not in validMethods)
- CreateChallenge with `number_match` method → returns error (method disabled)
- CreateChallenge with invalid source_service → validation error
- GetChallengeStatus → found, returns correct fields
- GetChallengeStatus → not found, returns NotFound
- GetPendingChallenge → returns most recent pending for user+device
- GetPendingChallenge → none pending, returns NotFound
- SubmitCode → correct code, status becomes "verified"
- SubmitCode → wrong code, attempts incremented
- SubmitCode → wrong code 3 times (max attempts), challenge status becomes "failed"
- SubmitCode → expired challenge, rejected
- SubmitCode → already verified challenge, returns AlreadyExists
- SubmitVerification → valid device response, verified
- SubmitVerification → invalid device, rejected
- ExpireOldChallenges → marks expired challenges

**Handler layer (~7 tests):**
- All 5 RPCs: request mapping to service call, response mapping back
- Error code mapping: NotFound → codes.NotFound, validation → codes.InvalidArgument

#### stock-service (4 → ~44 tests)

**OrderService (~12 tests):**
- CreateOrder market buy → order created, auto-approved for client
- CreateOrder limit buy → order created with limit_value set
- CreateOrder stop sell → order created with stop_value
- CreateOrder stop-limit → both values set
- CreateOrder for expired futures → validation error
- CreateOrder for agent with needApproval=true → status pending
- ApproveOrder → status changes to approved
- DeclineOrder → status changes to declined
- CancelOrder pending → cancelled
- CancelOrder already filled → error
- CreateOrder exceeding agent limit → rejected
- CreateOrder sell without holding → rejected

**PortfolioService (~10 tests):**
- Buy fill → holding created with correct weighted average cost
- Buy fill existing holding → quantity increased, avg cost recalculated
- Sell fill → holding quantity decreased, capital gain recorded
- Sell fill entire position → holding removed
- Capital gain = (sell_price - avg_cost) × quantity
- ExerciseOption call ITM (strike < current) → holding created
- ExerciseOption call OTM (strike >= current) → rejected
- ExerciseOption put ITM (strike > current) → holding sold
- ExerciseOption put OTM → rejected
- ListHoldings → returns P&L per holding

**OTCService (~6 tests):**
- MakePublic → holding public_quantity set
- MakePublic more than owned → rejected
- BuyOffer → buyer gets holding, seller's quantity decremented
- BuyOffer → commission calculated as min(14% of total, $7)
- BuyOffer → capital gain recorded for seller
- ListOffers → returns only public holdings

**TaxService (~6 tests):**
- Capital gain positive → 15% tax liability recorded
- Capital gain negative → no tax (loss)
- CollectTax → sums all unpaid gains, debits user account, credits state account
- Multi-currency gain → converted to RSD via exchange-service
- ListTaxRecords → filters by year/month
- ListUserTaxRecords → returns only that user's records

**SecurityService + ExchangeService (~4 tests):**
- ListStocks with filters (name search, price range)
- ListExchanges
- SetTestingMode toggle
- GetStock with related options

**Handler layer (~6 tests):**
- All 6 gRPC services: verify request mapping and error codes

### 3.2 Tier 2 — Weak Coverage

#### notification-service (1 → ~10 tests)

**Service/Consumer (~6 tests):**
- GetPendingMobileItems → returns items for user+device
- GetPendingMobileItems → empty for unknown device
- AckMobileItem → marks as delivered
- AckMobileItem → already delivered → error or no-op
- Inbox cleanup cron → removes old delivered items
- Template: verification code email type
- Template: mobile activation email type

**Handler (~4 tests):**
- SendEmail, GetPendingMobileItems, AckMobileItem RPC mapping

#### client-service (1 → ~15 tests)

**ClientService (~10 tests):**
- CreateClient → valid, all fields persisted
- CreateClient → duplicate email → AlreadyExists
- CreateClient → duplicate JMBG → AlreadyExists
- CreateClient → invalid JMBG (not 13 digits) → InvalidArgument
- CreateClient → invalid email format → InvalidArgument
- GetClient → found
- GetClient → not found → NotFound
- GetClientByEmail → found
- ListClients → pagination, filter by name, filter by email
- UpdateClient → valid (change phone, address), email uniqueness enforced

**ClientLimitService (~3 tests):**
- GetLimits → returns defaults if not set
- SetLimits → persisted correctly
- SetLimits → constrained by employee max limits

**Handler (~2 tests):**
- RPC mapping for CreateClient, ListClients

#### account-service (2 → ~20 tests)

**AccountService (~12 tests):**
- CreateAccount current/RSD → 18-digit account number, correct format
- CreateAccount foreign/EUR → correct format
- CreateAccount with initial_balance → balance set
- CreateAccount business + company → company record created
- GetAccount, GetAccountByNumber → found/not found
- ListAccountsByClient → pagination
- ListAllAccounts → admin filters
- UpdateAccountName → success, duplicate name same client → rejected
- UpdateAccountLimits → daily/monthly set
- UpdateAccountStatus → active/inactive
- UpdateAccountStatus → cannot deactivate last bank RSD or foreign account
- UpdateBalance → balance adjusted

**Other services (~5 tests):**
- LedgerService: GetLedgerEntries with pagination
- CompanyService: Create, Get, Update
- CurrencyService: List, GetByCode

**Handler (~3 tests):**
- Key RPCs mapping

#### card-service (2 → ~18 tests)

**CardService (~12 tests):**
- CreateCard visa/mastercard/dinacard/amex → correct number format, Luhn valid
- CreateCard amex → 15 digits
- CreateCard → max 2 per personal account enforced
- CreateCard → max 1 per person for business account
- GetCard → found, CVV not exposed after creation
- ListCardsByAccount, ListCardsByClient
- BlockCard → status blocked
- UnblockCard → status active again
- DeactivateCard → permanent, cannot re-activate
- PIN management: set (4 digits, bcrypt hashed), verify, lock after 3 failed

**CardRequestService (~4 tests):**
- Client submits request → pending
- Employee approves → card created
- Employee rejects → status rejected

**Handler (~2 tests):**
- Key RPCs mapping

### 3.3 Tier 3 — Strengthen Existing

#### credit-service (4 → ~15 tests)
- CreateLoanRequest: all 5 types (cash, housing, auto, refinancing, student)
- CreateLoanRequest: invalid account currency mismatch → error
- CreateLoanRequest: repayment period validation per type (housing: 60-360, others: 12-84)
- ApproveLoanRequest: disbursement to account, installment schedule created
- ApproveLoanRequest: installment amount matches spec formula `A = P × r × (1+r)^n / ((1+r)^n - 1)`
- RejectLoanRequest: status changes, no disbursement
- Interest rate tier: correct tier selected based on loan amount
- Bank margin: correct margin applied based on loan type
- Variable rate update: propagates to active loans
- Cron: installment due today → collected, insufficient funds → retry

#### transaction-service (6 → ~12 tests)
- CreatePayment cross-currency → exchange-service called, correct conversion
- CreateTransfer same currency (prenos) → no commission
- CreateTransfer cross-currency → two-leg RSD conversion, commission per leg
- Payment recipient CRUD: create, list, update, delete
- ListPaymentsByClient: date/status/amount filters
- Saga compensation: debit succeeds but credit fails → debit reversed
- Verify existing: fee stacking, idempotency correctness

#### auth-service (4 → ~12 tests)
- Login: valid → access + refresh tokens with correct claims
- Login: wrong password → Unauthenticated
- Login: inactive account → PermissionDenied
- Login: rate limiting after N failed attempts
- Mobile: RequestMobileActivation → code sent
- Mobile: ActivateMobileDevice → device registered
- Mobile: RefreshMobileToken → new token pair
- Mobile: DeactivateDevice → device removed
- SetAccountStatus / GetAccountStatus
- Token claims: employee → system_type="employee", client → system_type="client"
- Verify existing: JWT expiry, pepper hash stability

#### user-service (3 → ~8 tests)
- ActuaryService: set agent limit, reset used limit
- ActuaryCron: daily limit reset at end of day
- UpdateEmployee: email uniqueness, JMBG immutable (cannot change)
- Verify existing: role seed, permission resolution

#### api-gateway (3 → ~10 tests)
- Validation: new endpoint input validation (stock endpoints, verification)
- AnyAuthMiddleware: accepts both client and employee tokens
- AuthMiddleware: rejects client tokens
- handleGRPCError: all error code mappings match convention

#### exchange-service (5 → verify only)
- Verify: cross-currency two-leg, commission per leg, sync atomicity

#### contract/shared (4 → verify only)
- Verify: idempotency key format, money parsing edge cases, optimistic lock detection, retry exhaustion

---

## 4. Cross-Service Integration Workflows (test-app)

### 4.1 Audit Existing Tests (29 files)

For every existing test file, run the 5-point audit:
1. Does it actually run? (not skipped, not empty)
2. Does it test what it claims? (not just status 200)
3. Does it validate response bodies? (field values, not just structure)
4. Does it check side effects? (balances, Kafka events, related records)
5. Does it match spec behavior? (amounts, rules, constraints)

Fix any test that fails audit. Strengthen weak assertions.

### 4.2 New Cross-Service Workflows (14 workflows)

#### Banking Core (5 workflows)

**WF-NEW-1: Full Client Onboarding to First Transaction**
1. Admin creates client
2. Admin creates RSD account with initial balance
3. Client activates via Kafka token
4. Client logs in
5. Client adds payment recipient
6. Client creates payment to another client
7. Verify challenge → execute payment
8. Assert: sender balance decreased by amount+fee, receiver increased by amount, bank account increased by fee

**WF-NEW-2: Multi-Currency Client Lifecycle**
1. Client created with RSD + EUR accounts (both funded)
2. Client creates transfer RSD→EUR
3. Verify + execute
4. Assert: RSD decreased, EUR increased by converted amount minus commission
5. Assert: bank RSD account received commission
6. Assert: ledger entries exist on both accounts

**WF-NEW-3: Card Full Lifecycle with Spending Limits**
1. Client + account created
2. Client requests card via /api/me/cards/requests
3. Employee approves card request
4. Card is active, client sets PIN
5. Client verifies PIN (correct)
6. Client blocks own card
7. Employee unblocks card
8. Employee deactivates card → permanent
9. Assert: card status is deactivated, cannot unblock
10. Client requests new card → approved

**WF-NEW-4: Loan Full Lifecycle with Installments**
1. Client + RSD account
2. Client submits housing loan request (variable rate, 60 months, 2M RSD)
3. Employee approves
4. Assert: loan disbursed, client balance increased by loan amount
5. Assert: 60 installments created
6. Assert: installment amounts match formula with correct interest tier (5.75% for 1M-2M range) + bank margin (1.50% for housing)
7. Assert: loan details show correct dates and amounts

**WF-NEW-5: Payment Verification Failure and Retry**
1. Client A + Client B setup
2. A creates payment to B
3. Create challenge → submit wrong code 3 times → challenge failed
4. Create NEW payment (same details)
5. New challenge → correct code → execute
6. Assert: balances correct, only second payment executed

#### Securities Trading (5 workflows)

**WF-NEW-6: Stock Market Buy and Sell Cycle**
1. Agent employee created + funded account
2. List exchanges, list stocks
3. Place market buy order for a stock
4. Wait for fill
5. Assert: holding exists in portfolio, account debited by price+commission
6. Place market sell order for same stock
7. Wait for fill
8. Assert: holding gone, account credited, capital gain recorded

**WF-NEW-7: Multi-Asset Order Types**
1. Supervisor employee created
2. Place limit buy order (stock) with limit below current price → stays pending
3. Cancel the limit order → status cancelled
4. Place market buy for futures contract → filled
5. Assert: holding with correct contract size
6. Place market buy for forex pair → accounts updated (base/quote currencies)

**WF-NEW-8: Order Approval Workflow**
1. Admin creates agent (needApproval=true via limit settings)
2. Agent places buy order → status = pending approval
3. Supervisor approves → status = approved, fills
4. Assert: holding created
5. Agent places another order → supervisor declines
6. Assert: status = declined, no holding

**WF-NEW-9: OTC Trading Between Users**
1. Agent A buys stock (market order, wait for fill)
2. Agent A makes holding partially public
3. Agent B lists OTC offers → sees A's shares
4. Agent B buys from A
5. Assert: A's quantity decreased, B has holding, commission charged, capital gain for A

**WF-NEW-10: Tax Collection Cycle**
1. Agent buys stock, sells at profit
2. Assert: capital gain recorded, 15% tax liability visible
3. Admin triggers tax collection
4. Assert: agent account debited, state RSD account credited

#### Cross-Domain (4 workflows)

**WF-NEW-11: Client Trades Stock After Banking Setup**
1. Full client onboarding (account + card)
2. Client places stock market buy → filled
3. Client also makes a regular payment from same account
4. Assert: portfolio has holding, payment executed, account balance reflects both

**WF-NEW-12: Employee Limit Enforcement Across Domains**
1. Admin creates agent with low MaxSingleTransaction limit
2. Agent tries stock order exceeding limit → rejected
3. Admin increases limit
4. Agent retries → succeeds
5. Agent tries payment exceeding daily limit → rejected

**WF-NEW-13: Cross-Currency Trading and Transfer**
1. Client has RSD + EUR accounts
2. Client buys stock listed in USD → RSD converted via exchange
3. Client sells stock → profit in RSD
4. Client transfers profit RSD→EUR
5. Assert: exchange rates applied correctly at each step, commissions deducted, EUR balance correct

**WF-NEW-14: Full Banking Day Simulation**
1. Admin onboards 3 clients (A, B, C) + 1 agent
2. A pays B (with fee) → assert balances + fee to bank
3. B transfers own RSD→EUR → assert exchange + commission
4. C requests housing loan → employee approves → C balance increases
5. Agent buys stock → sells at profit → capital gain recorded
6. Admin collects tax → agent debited, state credited
7. Final assertions: all account balances are internally consistent, all fees accounted for in bank account, all Kafka events published

---

## 5. Test Correctness & Spec Compliance

### Spec Rules to Validate in Tests

**Accounts:**
- Account number: 18 digits, correct bank code prefix
- Bank must always keep >= 1 RSD + 1 foreign account (delete protection)
- Supported currencies: RSD, EUR, CHF, USD, GBP, JPY, CAD, AUD

**Payments:**
- Between different clients (same or different currency)
- Verification code required (5 min expiry, 3 max attempts, cancels on failure)
- Fee credited to bank's RSD account
- Payment recipients: CRUD, reusable

**Transfers:**
- Same client only
- Same currency = no commission
- Different currency = two-leg via RSD, commission per leg

**Cards:**
- Luhn-valid, 16 digits (15 for Amex)
- Visa starts 4, Mastercard 51-55 or 2221-2720, DinaCard 9891, Amex 34/37
- Max 2 per personal account, 1 per person per business account
- PIN: 4 digits, bcrypt, locked after 3 failures
- Block = temporary, deactivate = permanent

**Credits:**
- Types: cash, housing, auto, refinancing, student
- Periods: housing 60-360, others 12-84
- Interest rate tiers by amount (0-500k=6.25%, 500k-1M=6.00%, etc.)
- Bank margin by type (cash=1.75%, housing=1.50%, auto=1.25%, refinancing=1.00%, student=0.75%)
- Formula: A = P × r × (1+r)^n / ((1+r)^n - 1) where r = annual/12
- Variable rate: adjusted monthly with ±1.50% random offset

**Securities:**
- Order types: market, limit, stop, stop-limit
- Client orders auto-approved, agent orders may need supervisor
- Commission: min(14%, $7) for OTC
- Tax: 15% on positive capital gains, monthly collection

**Auth:**
- Password: 8-32 chars, 2+ digits, 1 uppercase, 1 lowercase
- Access: 15 min, claims include user_id, roles, permissions, system_type
- Refresh: 168h, stored and revocable
- system_type: "employee" or "client"

**Verification:**
- Active methods: code_pull, email
- Disabled methods (test they fail): qr_scan, number_match
- Challenge: 6-digit code, 5 min expiry, 3 max attempts
- Failed after max attempts, expired after timeout

---

## 6. Summary

| Phase | Scope | Estimated Tests |
|-------|-------|----------------|
| Phase 1: Audit & Fix | 72 existing test files | Fix count TBD |
| Phase 2: Shared Infra | contract/testutil + test-app helpers | ~15 helper functions |
| Phase 3: Unit Tests | 12 services, service + handler layers | ~185 new tests |
| Phase 4: Integration | 29 existing (audit) + 14 new workflows | 14 new workflow files |
| Phase 5: Verify | Full suite run | 0 new, all green |

**Priority order within Phase 3:** verification-service → stock-service → client-service → account-service → card-service → notification-service → credit-service → transaction-service → auth-service → user-service → api-gateway → exchange-service/contract (verify only)
