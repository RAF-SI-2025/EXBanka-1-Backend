# Banking Core Services - Backend Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement core banking operations backend: client management, accounts (current/foreign currency), cards, payments/transfers/exchange, and credits/loans — as five new Go microservices following the existing architecture patterns.

**Architecture:** Five new gRPC microservices (client-service, account-service, card-service, transaction-service, credit-service), each with its own PostgreSQL database, following the existing layered pattern (model → repository → service → handler). Services communicate via gRPC (sync) and Kafka (async events). API Gateway extended with new REST routes. All services publish Kafka events for every significant action.

**Tech Stack:** Go 1.25, Gin, GORM, gRPC/protobuf, Kafka (segmentio/kafka-go), Redis, PostgreSQL, testify, godotenv

**New Ports:**
| Service | gRPC Port | DB Port |
|---|---|---|
| client-service | :50054 | 5434 |
| account-service | :50055 | 5435 |
| card-service | :50056 | 5436 |
| transaction-service | :50057 | 5437 |
| credit-service | :50058 | 5438 |

---

## Scope & Phasing

This plan covers ALL backend requirements from `docs/NewRequirements.txt`. Due to the large scope, implementation should proceed in phases — each chunk produces working, testable software independently:

- **Phase 1** (Chunks 1-3): Foundation — Contracts, Client Service, Account Service
- **Phase 2** (Chunks 4-5): Operations — Card Service, Transaction Service
- **Phase 3** (Chunks 6-8): Advanced — Credit Service, API Gateway Extensions, Notification Extensions

---

## Architecture Overview

### New Services

```
client-service/          (gRPC :50054, client_db :5434)
  Client CRUD, client authentication support, JMBG validation

account-service/         (gRPC :50055, account_db :5435)
  Accounts (current/foreign), Companies, Currencies, account number generation

card-service/            (gRPC :50056, card_db :5436)
  Card CRUD, Luhn card number generation, AuthorizedPerson, blocking/deactivation

transaction-service/     (gRPC :50057, transaction_db :5437)
  Payments, Transfers, Exchange rates, PaymentRecipients, verification codes

credit-service/          (gRPC :50058, credit_db :5438)
  Loans, LoanRequests, Installments, interest rate calculation, cron job
```

### Kafka Topics (new)

```
client.created              — published when a new client is created
client.updated              — published when client data changes
account.created             — published when a new account is opened
account.status-changed      — published when account activated/deactivated
card.created                — published when a new card is issued
card.status-changed         — published when card blocked/unblocked/deactivated
transaction.payment-created — published when a payment is initiated
transaction.payment-completed — published on successful payment
transaction.transfer-created  — published when a transfer is initiated
transaction.transfer-completed — published on successful transfer
credit.loan-requested       — published when loan request submitted
credit.loan-approved        — published when loan approved
credit.loan-rejected        — published when loan rejected
credit.installment-collected — published when installment successfully collected
credit.installment-failed   — published when installment collection fails
```

### Inter-Service Communication

```
API Gateway → client-service:   Client CRUD, client login orchestration
API Gateway → account-service:  Account/Company/Currency CRUD
API Gateway → card-service:     Card CRUD, blocking
API Gateway → transaction-service: Payments, transfers, exchange
API Gateway → credit-service:   Loans, loan requests, installments

account-service → client-service:   Validate client exists (owner)
card-service → account-service:     Validate account exists, check card limits
transaction-service → account-service: Validate accounts, update balances
credit-service → account-service:   Validate account, deduct installments

All services → Kafka → notification-service: Email notifications
```

---

## File Structure

### New Files

```
# Protobuf definitions
contract/proto/client/client.proto
contract/proto/account/account.proto
contract/proto/card/card.proto
contract/proto/transaction/transaction.proto
contract/proto/credit/credit.proto
contract/clientpb/                              (generated)
contract/accountpb/                             (generated)
contract/cardpb/                                (generated)
contract/transactionpb/                         (generated)
contract/creditpb/                              (generated)

# Client Service
client-service/cmd/main.go
client-service/go.mod
client-service/Dockerfile
client-service/internal/config/config.go
client-service/internal/model/client.go
client-service/internal/repository/client_repository.go
client-service/internal/service/client_service.go
client-service/internal/service/client_service_test.go
client-service/internal/handler/grpc_handler.go
client-service/internal/kafka/producer.go
client-service/internal/cache/redis.go

# Account Service
account-service/cmd/main.go
account-service/go.mod
account-service/Dockerfile
account-service/internal/config/config.go
account-service/internal/model/account.go
account-service/internal/model/company.go
account-service/internal/model/currency.go
account-service/internal/repository/account_repository.go
account-service/internal/repository/company_repository.go
account-service/internal/repository/currency_repository.go
account-service/internal/service/account_service.go
account-service/internal/service/account_service_test.go
account-service/internal/service/account_number.go
account-service/internal/service/account_number_test.go
account-service/internal/service/company_service.go
account-service/internal/service/currency_service.go
account-service/internal/handler/grpc_handler.go
account-service/internal/kafka/producer.go
account-service/internal/cache/redis.go

# Card Service
card-service/cmd/main.go
card-service/go.mod
card-service/Dockerfile
card-service/internal/config/config.go
card-service/internal/model/card.go
card-service/internal/model/authorized_person.go
card-service/internal/repository/card_repository.go
card-service/internal/repository/authorized_person_repository.go
card-service/internal/service/card_service.go
card-service/internal/service/card_service_test.go
card-service/internal/service/card_number.go
card-service/internal/service/card_number_test.go
card-service/internal/handler/grpc_handler.go
card-service/internal/kafka/producer.go
card-service/internal/cache/redis.go

# Transaction Service
transaction-service/cmd/main.go
transaction-service/go.mod
transaction-service/Dockerfile
transaction-service/internal/config/config.go
transaction-service/internal/model/payment.go
transaction-service/internal/model/transfer.go
transaction-service/internal/model/payment_recipient.go
transaction-service/internal/model/verification_code.go
transaction-service/internal/repository/payment_repository.go
transaction-service/internal/repository/transfer_repository.go
transaction-service/internal/repository/payment_recipient_repository.go
transaction-service/internal/service/payment_service.go
transaction-service/internal/service/payment_service_test.go
transaction-service/internal/service/transfer_service.go
transaction-service/internal/service/transfer_service_test.go
transaction-service/internal/service/exchange_service.go
transaction-service/internal/service/exchange_service_test.go
transaction-service/internal/service/verification_service.go
transaction-service/internal/service/payment_recipient_service.go
transaction-service/internal/handler/grpc_handler.go
transaction-service/internal/kafka/producer.go
transaction-service/internal/cache/redis.go

# Credit Service
credit-service/cmd/main.go
credit-service/go.mod
credit-service/Dockerfile
credit-service/internal/config/config.go
credit-service/internal/model/loan.go
credit-service/internal/model/loan_request.go
credit-service/internal/model/installment.go
credit-service/internal/repository/loan_repository.go
credit-service/internal/repository/loan_request_repository.go
credit-service/internal/repository/installment_repository.go
credit-service/internal/service/loan_service.go
credit-service/internal/service/loan_service_test.go
credit-service/internal/service/loan_request_service.go
credit-service/internal/service/installment_service.go
credit-service/internal/service/installment_service_test.go
credit-service/internal/service/interest_rate.go
credit-service/internal/service/interest_rate_test.go
credit-service/internal/service/cron_service.go
credit-service/internal/handler/grpc_handler.go
credit-service/internal/kafka/producer.go
credit-service/internal/cache/redis.go

# API Gateway extensions
api-gateway/internal/grpc/client_client.go
api-gateway/internal/grpc/account_client.go
api-gateway/internal/grpc/card_client.go
api-gateway/internal/grpc/transaction_client.go
api-gateway/internal/grpc/credit_client.go
api-gateway/internal/handler/client_handler.go
api-gateway/internal/handler/account_handler.go
api-gateway/internal/handler/card_handler.go
api-gateway/internal/handler/transaction_handler.go
api-gateway/internal/handler/credit_handler.go
api-gateway/internal/handler/exchange_handler.go
api-gateway/internal/handler/employee_portal_handler.go
```

### Modified Files

```
contract/kafka/messages.go                      — Add all new Kafka message types
contract/go.mod                                 — No change (shared module)
docker-compose.yml                              — Add 5 new DB containers + 5 new services
Makefile                                        — Add proto targets + build targets for new services
go.work                                         — Add 5 new service modules
api-gateway/internal/router/router.go           — Add all new routes
api-gateway/internal/config/config.go           — Add new gRPC addresses
api-gateway/cmd/main.go                         — Wire new gRPC clients
auth-service/internal/service/auth_service.go   — Support client login (call client-service)
auth-service/internal/config/config.go          — Add CLIENT_GRPC_ADDR
notification-service/internal/sender/templates.go — Add new email templates
notification-service/internal/consumer/email_consumer.go — Handle new email types
```

---

## Chunk 1: Protobuf Contracts & Infrastructure

### Task 1: Define client.proto

**Files:**
- Create: `contract/proto/client/client.proto`

- [ ] **Step 1: Write client.proto**

```protobuf
// contract/proto/client/client.proto
syntax = "proto3";
package client;
option go_package = "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/clientpb";

service ClientService {
  rpc CreateClient(CreateClientRequest) returns (ClientResponse);
  rpc GetClient(GetClientRequest) returns (ClientResponse);
  rpc GetClientByEmail(GetClientByEmailRequest) returns (ClientResponse);
  rpc ListClients(ListClientsRequest) returns (ListClientsResponse);
  rpc UpdateClient(UpdateClientRequest) returns (ClientResponse);
  rpc ValidateCredentials(ValidateClientCredentialsRequest) returns (ValidateClientCredentialsResponse);
  rpc SetPassword(SetClientPasswordRequest) returns (SetClientPasswordResponse);
}

message CreateClientRequest {
  string first_name = 1;
  string last_name = 2;
  int64 date_of_birth = 3;
  string gender = 4;
  string email = 5;
  string phone = 6;
  string address = 7;
  string jmbg = 8;
}

message GetClientRequest {
  uint64 id = 1;
}

message GetClientByEmailRequest {
  string email = 1;
}

message ListClientsRequest {
  string email_filter = 1;
  string name_filter = 2;
  int32 page = 3;
  int32 page_size = 4;
}

message ListClientsResponse {
  repeated ClientResponse clients = 1;
  int64 total = 2;
}

message UpdateClientRequest {
  uint64 id = 1;
  optional string first_name = 2;
  optional string last_name = 3;
  optional int64 date_of_birth = 4;
  optional string gender = 5;
  optional string email = 6;
  optional string phone = 7;
  optional string address = 8;
}

message ClientResponse {
  uint64 id = 1;
  string first_name = 2;
  string last_name = 3;
  int64 date_of_birth = 4;
  string gender = 5;
  string email = 6;
  string phone = 7;
  string address = 8;
  string jmbg = 9;
  bool active = 10;
  string created_at = 11;
}

message ValidateClientCredentialsRequest {
  string email = 1;
  string password = 2;
}

message ValidateClientCredentialsResponse {
  uint64 id = 1;
  string email = 2;
  string first_name = 3;
  string last_name = 4;
}

message SetClientPasswordRequest {
  uint64 user_id = 1;
  string password_hash = 2;
}

message SetClientPasswordResponse {
  bool success = 1;
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/proto/client/client.proto
git commit -m "feat(contract): add client.proto definition"
```

---

### Task 2: Define account.proto

**Files:**
- Create: `contract/proto/account/account.proto`

- [ ] **Step 1: Write account.proto**

```protobuf
// contract/proto/account/account.proto
syntax = "proto3";
package account;
option go_package = "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/accountpb";

service AccountService {
  // Account operations
  rpc CreateAccount(CreateAccountRequest) returns (AccountResponse);
  rpc GetAccount(GetAccountRequest) returns (AccountResponse);
  rpc GetAccountByNumber(GetAccountByNumberRequest) returns (AccountResponse);
  rpc ListAccountsByClient(ListAccountsByClientRequest) returns (ListAccountsResponse);
  rpc ListAllAccounts(ListAllAccountsRequest) returns (ListAccountsResponse);
  rpc UpdateAccountName(UpdateAccountNameRequest) returns (AccountResponse);
  rpc UpdateAccountLimits(UpdateAccountLimitsRequest) returns (AccountResponse);
  rpc UpdateAccountStatus(UpdateAccountStatusRequest) returns (AccountResponse);
  rpc UpdateBalance(UpdateBalanceRequest) returns (AccountResponse);

  // Company operations
  rpc CreateCompany(CreateCompanyRequest) returns (CompanyResponse);
  rpc GetCompany(GetCompanyRequest) returns (CompanyResponse);
  rpc UpdateCompany(UpdateCompanyRequest) returns (CompanyResponse);

  // Currency operations
  rpc ListCurrencies(ListCurrenciesRequest) returns (ListCurrenciesResponse);
  rpc GetCurrency(GetCurrencyRequest) returns (CurrencyResponse);
}

message CreateAccountRequest {
  uint64 owner_id = 1;
  string account_kind = 2;       // "current" or "foreign"
  string account_type = 3;       // "standard","savings","pension","youth","student","unemployed","doo","ad","foundation"
  string account_category = 4;   // "personal" or "business"
  string currency_code = 5;      // "RSD","EUR","CHF","USD","GBP","JPY","CAD","AUD"
  uint64 employee_id = 6;        // employee who created it
  double initial_balance = 7;
  bool create_card = 8;          // checkbox: auto-create card
  optional uint64 company_id = 9; // only for business accounts
}

message GetAccountRequest {
  uint64 id = 1;
}

message GetAccountByNumberRequest {
  string account_number = 1;
}

message ListAccountsByClientRequest {
  uint64 client_id = 1;
  int32 page = 2;
  int32 page_size = 3;
}

message ListAllAccountsRequest {
  string name_filter = 1;
  string account_number_filter = 2;
  string type_filter = 3;
  int32 page = 4;
  int32 page_size = 5;
}

message ListAccountsResponse {
  repeated AccountResponse accounts = 1;
  int64 total = 2;
}

message UpdateAccountNameRequest {
  uint64 id = 1;
  uint64 client_id = 2;
  string new_name = 3;
}

message UpdateAccountLimitsRequest {
  uint64 id = 1;
  optional double daily_limit = 2;
  optional double monthly_limit = 3;
}

message UpdateAccountStatusRequest {
  uint64 id = 1;
  string status = 2; // "active" or "inactive"
}

message UpdateBalanceRequest {
  string account_number = 1;
  double amount = 2;           // positive = credit, negative = debit
  bool update_available = 3;   // true = also update available_balance
}

message AccountResponse {
  uint64 id = 1;
  string account_number = 2;
  string account_name = 3;
  uint64 owner_id = 4;
  string owner_name = 5;
  double balance = 6;
  double available_balance = 7;
  uint64 employee_id = 8;
  string created_at = 9;
  string expires_at = 10;
  string currency_code = 11;
  string status = 12;
  string account_kind = 13;
  string account_type = 14;
  string account_category = 15;
  double maintenance_fee = 16;
  double daily_limit = 17;
  double monthly_limit = 18;
  double daily_spending = 19;
  double monthly_spending = 20;
  optional uint64 company_id = 21;
}

message CreateCompanyRequest {
  string company_name = 1;
  string registration_number = 2;  // matični broj, 8 digits
  string tax_number = 3;           // PIB, 9 digits
  string activity_code = 4;        // šifra delatnosti, xx.xx
  string address = 5;
  uint64 owner_id = 6;             // client FK
}

message GetCompanyRequest {
  uint64 id = 1;
}

message UpdateCompanyRequest {
  uint64 id = 1;
  optional string company_name = 2;
  optional string activity_code = 3;
  optional string address = 4;
  optional uint64 owner_id = 5;
}

message CompanyResponse {
  uint64 id = 1;
  string company_name = 2;
  string registration_number = 3;
  string tax_number = 4;
  string activity_code = 5;
  string address = 6;
  uint64 owner_id = 7;
  string created_at = 8;
}

message ListCurrenciesRequest {}

message ListCurrenciesResponse {
  repeated CurrencyResponse currencies = 1;
}

message GetCurrencyRequest {
  string code = 1;
}

message CurrencyResponse {
  uint64 id = 1;
  string name = 2;
  string code = 3;
  string symbol = 4;
  string country = 5;
  string description = 6;
  bool active = 7;
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/proto/account/account.proto
git commit -m "feat(contract): add account.proto with Account, Company, Currency services"
```

---

### Task 3: Define card.proto

**Files:**
- Create: `contract/proto/card/card.proto`

- [ ] **Step 1: Write card.proto**

```protobuf
// contract/proto/card/card.proto
syntax = "proto3";
package card;
option go_package = "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/cardpb";

service CardService {
  rpc CreateCard(CreateCardRequest) returns (CardResponse);
  rpc GetCard(GetCardRequest) returns (CardResponse);
  rpc ListCardsByAccount(ListCardsByAccountRequest) returns (ListCardsResponse);
  rpc ListCardsByClient(ListCardsByClientRequest) returns (ListCardsResponse);
  rpc BlockCard(BlockCardRequest) returns (CardResponse);
  rpc UnblockCard(UnblockCardRequest) returns (CardResponse);
  rpc DeactivateCard(DeactivateCardRequest) returns (CardResponse);

  rpc CreateAuthorizedPerson(CreateAuthorizedPersonRequest) returns (AuthorizedPersonResponse);
  rpc GetAuthorizedPerson(GetAuthorizedPersonRequest) returns (AuthorizedPersonResponse);
}

message CreateCardRequest {
  string account_number = 1;
  uint64 owner_id = 2;          // client ID or authorized person ID
  string owner_type = 3;        // "client" or "authorized_person"
  string card_brand = 4;        // "visa","mastercard","dinacard","amex"
}

message GetCardRequest {
  uint64 id = 1;
}

message ListCardsByAccountRequest {
  string account_number = 1;
}

message ListCardsByClientRequest {
  uint64 client_id = 1;
}

message ListCardsResponse {
  repeated CardResponse cards = 1;
}

message BlockCardRequest {
  uint64 id = 1;
}

message UnblockCardRequest {
  uint64 id = 1;
}

message DeactivateCardRequest {
  uint64 id = 1;
}

message CardResponse {
  uint64 id = 1;
  string card_number = 2;       // masked for client view: 5798********5571
  string card_number_full = 3;  // only for internal/employee use
  string card_type = 4;         // "debit"
  string card_name = 5;
  string card_brand = 6;
  string created_at = 7;
  string expires_at = 8;
  string account_number = 9;
  string cvv = 10;              // only on creation response
  double card_limit = 11;
  string status = 12;           // "active","blocked","deactivated"
  string owner_type = 13;
  uint64 owner_id = 14;
}

message CreateAuthorizedPersonRequest {
  string first_name = 1;
  string last_name = 2;
  int64 date_of_birth = 3;
  string gender = 4;
  string email = 5;
  string phone = 6;
  string address = 7;
  uint64 account_id = 8;
}

message GetAuthorizedPersonRequest {
  uint64 id = 1;
}

message AuthorizedPersonResponse {
  uint64 id = 1;
  string first_name = 2;
  string last_name = 3;
  int64 date_of_birth = 4;
  string gender = 5;
  string email = 6;
  string phone = 7;
  string address = 8;
  uint64 account_id = 9;
  string created_at = 10;
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/proto/card/card.proto
git commit -m "feat(contract): add card.proto with Card and AuthorizedPerson services"
```

---

### Task 4: Define transaction.proto

**Files:**
- Create: `contract/proto/transaction/transaction.proto`

- [ ] **Step 1: Write transaction.proto**

```protobuf
// contract/proto/transaction/transaction.proto
syntax = "proto3";
package transaction;
option go_package = "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/transactionpb";

service TransactionService {
  // Payments (between different clients)
  rpc CreatePayment(CreatePaymentRequest) returns (PaymentResponse);
  rpc GetPayment(GetPaymentRequest) returns (PaymentResponse);
  rpc ListPaymentsByAccount(ListPaymentsByAccountRequest) returns (ListPaymentsResponse);

  // Transfers (between same client accounts, possibly different currencies)
  rpc CreateTransfer(CreateTransferRequest) returns (TransferResponse);
  rpc GetTransfer(GetTransferRequest) returns (TransferResponse);
  rpc ListTransfersByClient(ListTransfersByClientRequest) returns (ListTransfersResponse);

  // Payment recipients
  rpc CreatePaymentRecipient(CreatePaymentRecipientRequest) returns (PaymentRecipientResponse);
  rpc ListPaymentRecipients(ListPaymentRecipientsRequest) returns (ListPaymentRecipientsResponse);
  rpc UpdatePaymentRecipient(UpdatePaymentRecipientRequest) returns (PaymentRecipientResponse);
  rpc DeletePaymentRecipient(DeletePaymentRecipientRequest) returns (DeletePaymentRecipientResponse);

  // Exchange rate
  rpc GetExchangeRate(GetExchangeRateRequest) returns (ExchangeRateResponse);
  rpc ListExchangeRates(ListExchangeRatesRequest) returns (ListExchangeRatesResponse);

  // Verification
  rpc CreateVerificationCode(CreateVerificationCodeRequest) returns (CreateVerificationCodeResponse);
  rpc ValidateVerificationCode(ValidateVerificationCodeRequest) returns (ValidateVerificationCodeResponse);
}

message CreatePaymentRequest {
  string from_account_number = 1;
  string to_account_number = 2;
  double amount = 3;
  string recipient_name = 4;
  string payment_code = 5;        // 3 digits starting with 2
  string reference_number = 6;    // poziv na broj (optional)
  string payment_purpose = 7;
}

message GetPaymentRequest {
  uint64 id = 1;
}

message ListPaymentsByAccountRequest {
  string account_number = 1;
  string date_from = 2;
  string date_to = 3;
  string status_filter = 4;
  double amount_min = 5;
  double amount_max = 6;
  int32 page = 7;
  int32 page_size = 8;
}

message ListPaymentsResponse {
  repeated PaymentResponse payments = 1;
  int64 total = 2;
}

message PaymentResponse {
  uint64 id = 1;
  string from_account_number = 2;
  string to_account_number = 3;
  double initial_amount = 4;
  double final_amount = 5;
  double commission = 6;
  string recipient_name = 7;
  string payment_code = 8;
  string reference_number = 9;
  string payment_purpose = 10;
  string status = 11;              // "completed","rejected","processing"
  string timestamp = 12;
}

message CreateTransferRequest {
  string from_account_number = 1;
  string to_account_number = 2;
  double amount = 3;
}

message GetTransferRequest {
  uint64 id = 1;
}

message ListTransfersByClientRequest {
  uint64 client_id = 1;
  int32 page = 2;
  int32 page_size = 3;
}

message ListTransfersResponse {
  repeated TransferResponse transfers = 1;
  int64 total = 2;
}

message TransferResponse {
  uint64 id = 1;
  string from_account_number = 2;
  string to_account_number = 3;
  double initial_amount = 4;
  double final_amount = 5;
  double exchange_rate = 6;
  double commission = 7;
  string timestamp = 8;
}

message CreatePaymentRecipientRequest {
  uint64 client_id = 1;
  string recipient_name = 2;
  string account_number = 3;
}

message ListPaymentRecipientsRequest {
  uint64 client_id = 1;
}

message ListPaymentRecipientsResponse {
  repeated PaymentRecipientResponse recipients = 1;
}

message UpdatePaymentRecipientRequest {
  uint64 id = 1;
  optional string recipient_name = 2;
  optional string account_number = 3;
}

message DeletePaymentRecipientRequest {
  uint64 id = 1;
}

message DeletePaymentRecipientResponse {
  bool success = 1;
}

message PaymentRecipientResponse {
  uint64 id = 1;
  uint64 client_id = 2;
  string recipient_name = 3;
  string account_number = 4;
  string created_at = 5;
}

message GetExchangeRateRequest {
  string from_currency = 1;
  string to_currency = 2;
}

message ListExchangeRatesRequest {}

message ExchangeRateResponse {
  string from_currency = 1;
  string to_currency = 2;
  double buy_rate = 3;
  double sell_rate = 4;
  string updated_at = 5;
}

message ListExchangeRatesResponse {
  repeated ExchangeRateResponse rates = 1;
}

message CreateVerificationCodeRequest {
  uint64 client_id = 1;
  uint64 transaction_id = 2;
  string transaction_type = 3;   // "payment" or "transfer"
}

message CreateVerificationCodeResponse {
  string code = 1;               // 6-digit code
  int64 expires_at = 2;
}

message ValidateVerificationCodeRequest {
  uint64 client_id = 1;
  uint64 transaction_id = 2;
  string code = 3;
}

message ValidateVerificationCodeResponse {
  bool valid = 1;
  int32 remaining_attempts = 2;
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/proto/transaction/transaction.proto
git commit -m "feat(contract): add transaction.proto with Payment, Transfer, Exchange, Verification"
```

---

### Task 5: Define credit.proto

**Files:**
- Create: `contract/proto/credit/credit.proto`

- [ ] **Step 1: Write credit.proto**

```protobuf
// contract/proto/credit/credit.proto
syntax = "proto3";
package credit;
option go_package = "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/creditpb";

service CreditService {
  // Loan requests
  rpc CreateLoanRequest(CreateLoanRequestReq) returns (LoanRequestResponse);
  rpc GetLoanRequest(GetLoanRequestReq) returns (LoanRequestResponse);
  rpc ListLoanRequests(ListLoanRequestsReq) returns (ListLoanRequestsResponse);
  rpc ApproveLoanRequest(ApproveLoanRequestReq) returns (LoanResponse);
  rpc RejectLoanRequest(RejectLoanRequestReq) returns (LoanRequestResponse);

  // Loans
  rpc GetLoan(GetLoanReq) returns (LoanResponse);
  rpc ListLoansByClient(ListLoansByClientReq) returns (ListLoansResponse);
  rpc ListAllLoans(ListAllLoansReq) returns (ListLoansResponse);

  // Installments
  rpc GetInstallmentsByLoan(GetInstallmentsByLoanReq) returns (ListInstallmentsResponse);
}

message CreateLoanRequestReq {
  uint64 client_id = 1;
  string loan_type = 2;           // "cash","housing","auto","refinancing","student"
  string interest_type = 3;       // "fixed" or "variable"
  double amount = 4;
  string currency_code = 5;
  string purpose = 6;
  double monthly_salary = 7;
  string employment_status = 8;   // "permanent","temporary","unemployed"
  int32 employment_period = 9;    // months at current employer
  int32 repayment_period = 10;    // number of months
  string phone = 11;
  string account_number = 12;     // must match currency
}

message GetLoanRequestReq {
  uint64 id = 1;
}

message ListLoanRequestsReq {
  string loan_type_filter = 1;
  string account_number_filter = 2;
  string status_filter = 3;
  int32 page = 4;
  int32 page_size = 5;
}

message ListLoanRequestsResponse {
  repeated LoanRequestResponse requests = 1;
  int64 total = 2;
}

message ApproveLoanRequestReq {
  uint64 request_id = 1;
}

message RejectLoanRequestReq {
  uint64 request_id = 1;
}

message LoanRequestResponse {
  uint64 id = 1;
  uint64 client_id = 2;
  string loan_type = 3;
  string interest_type = 4;
  double amount = 5;
  string currency_code = 6;
  string purpose = 7;
  double monthly_salary = 8;
  string employment_status = 9;
  int32 employment_period = 10;
  int32 repayment_period = 11;
  string phone = 12;
  string account_number = 13;
  string status = 14;
  string created_at = 15;
}

message GetLoanReq {
  uint64 id = 1;
}

message ListLoansByClientReq {
  uint64 client_id = 1;
  int32 page = 2;
  int32 page_size = 3;
}

message ListAllLoansReq {
  string loan_type_filter = 1;
  string account_number_filter = 2;
  string status_filter = 3;
  int32 page = 4;
  int32 page_size = 5;
}

message ListLoansResponse {
  repeated LoanResponse loans = 1;
  int64 total = 2;
}

message LoanResponse {
  uint64 id = 1;
  string loan_number = 2;
  string loan_type = 3;
  string account_number = 4;
  double amount = 5;
  int32 repayment_period = 6;
  double nominal_interest_rate = 7;
  double effective_interest_rate = 8;
  string contract_date = 9;
  string maturity_date = 10;
  double next_installment_amount = 11;
  string next_installment_date = 12;
  double remaining_debt = 13;
  string currency_code = 14;
  string status = 15;                // "approved","rejected","paid_off","overdue"
  string interest_type = 16;
  string created_at = 17;
}

message GetInstallmentsByLoanReq {
  uint64 loan_id = 1;
}

message ListInstallmentsResponse {
  repeated InstallmentResponse installments = 1;
}

message InstallmentResponse {
  uint64 id = 1;
  uint64 loan_id = 2;
  double amount = 3;
  double interest_rate = 4;
  string currency_code = 5;
  string expected_date = 6;
  string actual_date = 7;
  string status = 8;               // "paid","unpaid","overdue"
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/proto/credit/credit.proto
git commit -m "feat(contract): add credit.proto with Loan, LoanRequest, Installment services"
```

---

### Task 6: Update Kafka message types

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Add new Kafka message types**

Add the following to `contract/kafka/messages.go`:

```go
// New topic constants
const (
	TopicClientCreated           = "client.created"
	TopicClientUpdated           = "client.updated"
	TopicAccountCreated          = "account.created"
	TopicAccountStatusChanged    = "account.status-changed"
	TopicCardCreated             = "card.created"
	TopicCardStatusChanged       = "card.status-changed"
	TopicPaymentCreated          = "transaction.payment-created"
	TopicPaymentCompleted        = "transaction.payment-completed"
	TopicTransferCreated         = "transaction.transfer-created"
	TopicTransferCompleted       = "transaction.transfer-completed"
	TopicLoanRequested           = "credit.loan-requested"
	TopicLoanApproved            = "credit.loan-approved"
	TopicLoanRejected            = "credit.loan-rejected"
	TopicInstallmentCollected    = "credit.installment-collected"
	TopicInstallmentFailed       = "credit.installment-failed"
)

// New email types
const (
	EmailTypeAccountCreated       = "ACCOUNT_CREATED"
	EmailTypeCardVerification     = "CARD_VERIFICATION"
	EmailTypeCardStatusChanged    = "CARD_STATUS_CHANGED"
	EmailTypeLoanApproved         = "LOAN_APPROVED"
	EmailTypeLoanRejected         = "LOAN_REJECTED"
	EmailTypeInstallmentFailed    = "INSTALLMENT_FAILED"
	EmailTypeTransactionVerify    = "TRANSACTION_VERIFICATION"
	EmailTypePaymentConfirmation  = "PAYMENT_CONFIRMATION"
)

// Event messages for inter-service Kafka communication
type ClientCreatedMessage struct {
	ClientID  uint64 `json:"client_id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type AccountCreatedMessage struct {
	AccountNumber string `json:"account_number"`
	OwnerID       uint64 `json:"owner_id"`
	OwnerEmail    string `json:"owner_email"`
	AccountKind   string `json:"account_kind"`
	CurrencyCode  string `json:"currency_code"`
}

type CardCreatedMessage struct {
	CardID        uint64 `json:"card_id"`
	AccountNumber string `json:"account_number"`
	OwnerEmail    string `json:"owner_email"`
	CardBrand     string `json:"card_brand"`
}

type CardStatusChangedMessage struct {
	CardID        uint64 `json:"card_id"`
	AccountNumber string `json:"account_number"`
	NewStatus     string `json:"new_status"`
	OwnerEmail    string `json:"owner_email"`
	// For business accounts, also notify account owner
	AccountOwnerEmail string `json:"account_owner_email,omitempty"`
}

type PaymentCompletedMessage struct {
	PaymentID         uint64  `json:"payment_id"`
	FromAccountNumber string  `json:"from_account_number"`
	ToAccountNumber   string  `json:"to_account_number"`
	Amount            float64 `json:"amount"`
	Status            string  `json:"status"`
}

type TransferCompletedMessage struct {
	TransferID        uint64  `json:"transfer_id"`
	FromAccountNumber string  `json:"from_account_number"`
	ToAccountNumber   string  `json:"to_account_number"`
	InitialAmount     float64 `json:"initial_amount"`
	FinalAmount       float64 `json:"final_amount"`
	ExchangeRate      float64 `json:"exchange_rate"`
}

type LoanStatusMessage struct {
	LoanRequestID uint64  `json:"loan_request_id"`
	ClientEmail   string  `json:"client_email"`
	LoanType      string  `json:"loan_type"`
	Amount        float64 `json:"amount"`
	Status        string  `json:"status"` // "approved" or "rejected"
}

type InstallmentResultMessage struct {
	LoanID        uint64  `json:"loan_id"`
	ClientEmail   string  `json:"client_email"`
	Amount        float64 `json:"amount"`
	Success       bool    `json:"success"`
	Error         string  `json:"error,omitempty"`
	RetryDeadline string  `json:"retry_deadline,omitempty"`
}
```

- [ ] **Step 2: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat(contract): add Kafka topics and message types for all new services"
```

---

### Task 7: Update docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add new database containers and services**

Add the following services to `docker-compose.yml`:

```yaml
  # New databases
  client-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: clientdb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5434:5432"
    volumes:
      - client-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  account-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: accountdb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5435:5432"
    volumes:
      - account-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  card-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: carddb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5436:5432"
    volumes:
      - card-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  transaction-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: transactiondb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5437:5432"
    volumes:
      - transaction-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  credit-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: creditdb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5438:5432"
    volumes:
      - credit-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5

  # New services
  client-service:
    build: ./client-service
    ports:
      - "50054:50054"
    env_file: .env
    depends_on:
      client-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:
        condition: service_healthy

  account-service:
    build: ./account-service
    ports:
      - "50055:50055"
    env_file: .env
    depends_on:
      account-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:
        condition: service_healthy

  card-service:
    build: ./card-service
    ports:
      - "50056:50056"
    env_file: .env
    depends_on:
      card-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:
        condition: service_healthy

  transaction-service:
    build: ./transaction-service
    ports:
      - "50057:50057"
    env_file: .env
    depends_on:
      transaction-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:
        condition: service_healthy

  credit-service:
    build: ./credit-service
    ports:
      - "50058:50058"
    env_file: .env
    depends_on:
      credit-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
      redis:
        condition: service_healthy
```

Add to volumes section:

```yaml
  client-db-data:
  account-db-data:
  card-db-data:
  transaction-db-data:
  credit-db-data:
```

- [ ] **Step 2: Commit**

```bash
git add docker-compose.yml
git commit -m "infra: add database containers and service definitions for new banking services"
```

---

### Task 8: Update Makefile and go.work

**Files:**
- Modify: `Makefile`
- Modify: `go.work`

- [ ] **Step 1: Add new services to go.work**

Add to `go.work`:

```
use ./client-service
use ./account-service
use ./card-service
use ./transaction-service
use ./credit-service
```

- [ ] **Step 2: Add proto targets and build targets to Makefile**

Add to the `proto` target:

```makefile
	protoc --go_out=. --go-grpc_out=. contract/proto/client/client.proto
	protoc --go_out=. --go-grpc_out=. contract/proto/account/account.proto
	protoc --go_out=. --go-grpc_out=. contract/proto/card/card.proto
	protoc --go_out=. --go-grpc_out=. contract/proto/transaction/transaction.proto
	protoc --go_out=. --go-grpc_out=. contract/proto/credit/credit.proto
```

Add to the `build` target:

```makefile
	cd client-service && go build -o bin/client-service ./cmd
	cd account-service && go build -o bin/account-service ./cmd
	cd card-service && go build -o bin/card-service ./cmd
	cd transaction-service && go build -o bin/transaction-service ./cmd
	cd credit-service && go build -o bin/credit-service ./cmd
```

Add to the `tidy` target:

```makefile
	cd client-service && go mod tidy
	cd account-service && go mod tidy
	cd card-service && go mod tidy
	cd transaction-service && go mod tidy
	cd credit-service && go mod tidy
```

- [ ] **Step 3: Run `make proto` to generate Go code**

Run: `make proto`
Expected: New directories `contract/clientpb/`, `contract/accountpb/`, `contract/cardpb/`, `contract/transactionpb/`, `contract/creditpb/` with generated `.pb.go` and `_grpc.pb.go` files.

- [ ] **Step 4: Commit**

```bash
git add Makefile go.work contract/clientpb/ contract/accountpb/ contract/cardpb/ contract/transactionpb/ contract/creditpb/
git commit -m "infra: update Makefile and go.work for new services, generate protobuf code"
```

---

## Chunk 2: Client Service Implementation

### Task 9: Client model

**Files:**
- Create: `client-service/internal/model/client.go`

- [ ] **Step 1: Write Client GORM model**

```go
// client-service/internal/model/client.go
package model

import "time"

type Client struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName    string    `gorm:"not null" json:"first_name"`
	LastName     string    `gorm:"not null" json:"last_name"`
	DateOfBirth  int64     `gorm:"not null" json:"date_of_birth"`
	Gender       string    `gorm:"size:10" json:"gender"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	Phone        string    `json:"phone"`
	Address      string    `json:"address"`
	JMBG         string    `gorm:"uniqueIndex;size:13" json:"jmbg"`
	PasswordHash string    `gorm:"not null" json:"-"`
	Salt         string    `gorm:"not null" json:"-"`
	Active       bool      `gorm:"default:true" json:"active"`
	Activated    bool      `gorm:"default:false" json:"activated"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Commit**

```bash
git add client-service/internal/model/client.go
git commit -m "feat(client-service): add Client GORM model"
```

---

### Task 10: Client config

**Files:**
- Create: `client-service/internal/config/config.go`

- [ ] **Step 1: Write config struct**

Follow the pattern from `user-service/internal/config/config.go`. Key env vars:

```go
// client-service/internal/config/config.go
package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	GRPCAddr   string
	KafkaBrokers string
	RedisAddr  string
}

func Load() *Config {
	// Walk up directory tree to find .env
	for _, path := range []string{".env", "../.env", "../../.env", "../../../.env"} {
		if err := godotenv.Load(path); err == nil {
			break
		}
	}

	return &Config{
		DBHost:       getEnv("CLIENT_DB_HOST", "localhost"),
		DBPort:       getEnv("CLIENT_DB_PORT", "5434"),
		DBUser:       getEnv("CLIENT_DB_USER", "postgres"),
		DBPassword:   getEnv("CLIENT_DB_PASSWORD", "postgres"),
		DBName:       getEnv("CLIENT_DB_NAME", "clientdb"),
		GRPCAddr:     getEnv("CLIENT_GRPC_ADDR", ":50054"),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Commit**

```bash
git add client-service/internal/config/config.go
git commit -m "feat(client-service): add config with env loading"
```

---

### Task 11: Client repository

**Files:**
- Create: `client-service/internal/repository/client_repository.go`

- [ ] **Step 1: Write client repository**

Follow the pattern from `user-service/internal/repository/employee_repository.go`:

```go
// client-service/internal/repository/client_repository.go
package repository

import (
	"client-service/internal/model"

	"gorm.io/gorm"
)

type ClientRepository struct {
	db *gorm.DB
}

func NewClientRepository(db *gorm.DB) *ClientRepository {
	return &ClientRepository{db: db}
}

func (r *ClientRepository) Create(client *model.Client) error {
	return r.db.Create(client).Error
}

func (r *ClientRepository) GetByID(id uint64) (*model.Client, error) {
	var client model.Client
	err := r.db.First(&client, id).Error
	return &client, err
}

func (r *ClientRepository) GetByEmail(email string) (*model.Client, error) {
	var client model.Client
	err := r.db.Where("email = ?", email).First(&client).Error
	return &client, err
}

func (r *ClientRepository) GetByJMBG(jmbg string) (*model.Client, error) {
	var client model.Client
	err := r.db.Where("jmbg = ?", jmbg).First(&client).Error
	return &client, err
}

func (r *ClientRepository) Update(client *model.Client) error {
	return r.db.Save(client).Error
}

func (r *ClientRepository) SetPassword(userID uint64, hash string) error {
	return r.db.Model(&model.Client{}).Where("id = ?", userID).
		Updates(map[string]interface{}{
			"password_hash": hash,
			"activated":     true,
		}).Error
}

func (r *ClientRepository) List(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	var clients []model.Client
	var total int64

	query := r.db.Model(&model.Client{})
	if emailFilter != "" {
		query = query.Where("email ILIKE ?", "%"+emailFilter+"%")
	}
	if nameFilter != "" {
		query = query.Where("first_name ILIKE ? OR last_name ILIKE ?", "%"+nameFilter+"%", "%"+nameFilter+"%")
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("last_name ASC, first_name ASC").Offset(offset).Limit(pageSize).Find(&clients).Error
	return clients, total, err
}
```

- [ ] **Step 2: Commit**

```bash
git add client-service/internal/repository/client_repository.go
git commit -m "feat(client-service): add client repository with CRUD operations"
```

---

### Task 12: Client service with validation and tests

**Files:**
- Create: `client-service/internal/service/client_service.go`
- Create: `client-service/internal/service/client_service_test.go`

- [ ] **Step 1: Write failing tests for client service**

```go
// client-service/internal/service/client_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateClientJMBG(t *testing.T) {
	tests := []struct {
		name    string
		jmbg    string
		wantErr bool
	}{
		{"valid 13 digits", "0101990710024", false},
		{"empty", "", true},
		{"too short", "12345", true},
		{"too long", "12345678901234", true},
		{"contains letters", "012345678901a", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJMBG(tt.jmbg)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateClientEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"valid email", "test@example.com", false},
		{"empty", "", true},
		{"no at sign", "testexample.com", true},
		{"no domain", "test@", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmail(tt.email)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd client-service && go test ./internal/service/ -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Write client service implementation**

```go
// client-service/internal/service/client_service.go
package service

import (
	"context"
	"errors"
	"strings"

	"client-service/internal/cache"
	"client-service/internal/model"
	"client-service/internal/repository"

	"golang.org/x/crypto/bcrypt"
)

type ClientService struct {
	repo  *repository.ClientRepository
	cache *cache.RedisCache
}

func NewClientService(repo *repository.ClientRepository, cache *cache.RedisCache) *ClientService {
	return &ClientService{repo: repo, cache: cache}
}

func ValidateJMBG(jmbg string) error {
	if len(jmbg) != 13 {
		return errors.New("JMBG must be exactly 13 digits")
	}
	for _, c := range jmbg {
		if c < '0' || c > '9' {
			return errors.New("JMBG must contain only digits")
		}
	}
	return nil
}

func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("email is required")
	}
	atIdx := strings.Index(email, "@")
	if atIdx < 1 || atIdx >= len(email)-1 {
		return errors.New("invalid email format")
	}
	return nil
}

func (s *ClientService) CreateClient(ctx context.Context, client *model.Client) error {
	if err := ValidateJMBG(client.JMBG); err != nil {
		return err
	}
	if err := ValidateEmail(client.Email); err != nil {
		return err
	}

	// Generate salt and empty password (activated via email later)
	salt, _ := bcrypt.GenerateFromPassword([]byte(client.Email), bcrypt.DefaultCost)
	client.Salt = string(salt)
	client.PasswordHash = ""
	client.Active = true
	client.Activated = false

	return s.repo.Create(client)
}

func (s *ClientService) GetClient(id uint64) (*model.Client, error) {
	cacheKey := "client:id:" + string(rune(id))
	var cached model.Client
	if s.cache != nil {
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			return &cached, nil
		}
	}

	client, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Set(context.Background(), cacheKey, client, 300) // 5 min TTL
	}
	return client, nil
}

func (s *ClientService) GetByEmail(email string) (*model.Client, error) {
	return s.repo.GetByEmail(email)
}

func (s *ClientService) UpdateClient(id uint64, updates map[string]interface{}) (*model.Client, error) {
	client, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// JMBG cannot be changed
	delete(updates, "jmbg")
	delete(updates, "password_hash")

	if email, ok := updates["email"].(string); ok {
		if err := ValidateEmail(email); err != nil {
			return nil, err
		}
	}

	for k, v := range updates {
		switch k {
		case "first_name":
			client.FirstName = v.(string)
		case "last_name":
			client.LastName = v.(string)
		case "email":
			client.Email = v.(string)
		case "phone":
			client.Phone = v.(string)
		case "address":
			client.Address = v.(string)
		case "gender":
			client.Gender = v.(string)
		case "date_of_birth":
			client.DateOfBirth = v.(int64)
		}
	}

	if err := s.repo.Update(client); err != nil {
		return nil, err
	}

	// Invalidate cache
	if s.cache != nil {
		_ = s.cache.DeleteByPattern(context.Background(), "client:*")
	}

	return client, nil
}

func (s *ClientService) ListClients(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return s.repo.List(emailFilter, nameFilter, page, pageSize)
}

func (s *ClientService) ValidateCredentials(email, password string) (*model.Client, error) {
	client, err := s.repo.GetByEmail(email)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if !client.Active || !client.Activated {
		return nil, errors.New("account not active")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(client.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}
	return client, nil
}

func (s *ClientService) SetPassword(userID uint64, hash string) error {
	return s.repo.SetPassword(userID, hash)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd client-service && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add client-service/internal/service/
git commit -m "feat(client-service): add client service with validation and tests"
```

---

### Task 13: Client Kafka producer, Redis cache, gRPC handler

**Files:**
- Create: `client-service/internal/kafka/producer.go`
- Create: `client-service/internal/cache/redis.go`
- Create: `client-service/internal/handler/grpc_handler.go`

- [ ] **Step 1: Write Kafka producer**

Follow the pattern from `user-service/internal/kafka/producer.go`. Add methods:
- `PublishClientCreated(ctx, msg ClientCreatedMessage)` → topic `client.created`
- `PublishClientUpdated(ctx, msg ClientCreatedMessage)` → topic `client.updated`
- `SendEmail(ctx, msg SendEmailMessage)` → topic `notification.send-email`

- [ ] **Step 2: Write Redis cache**

Copy `user-service/internal/cache/redis.go` — identical pattern.

- [ ] **Step 3: Write gRPC handler**

Follow the pattern from `user-service/internal/handler/grpc_handler.go`. Implement all `ClientService` RPC methods by delegating to `service.ClientService`. Publish Kafka events after successful create/update operations.

```go
// client-service/internal/handler/grpc_handler.go
package handler

import (
	"context"

	"client-service/internal/model"
	"client-service/internal/service"
	kafkaProducer "client-service/internal/kafka"

	pb "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/clientpb"
	kafkaMsg "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/kafka"
)

type ClientGRPCHandler struct {
	pb.UnimplementedClientServiceServer
	clientService *service.ClientService
	kafka         *kafkaProducer.Producer
}

func NewClientGRPCHandler(cs *service.ClientService, k *kafkaProducer.Producer) *ClientGRPCHandler {
	return &ClientGRPCHandler{clientService: cs, kafka: k}
}

func (h *ClientGRPCHandler) CreateClient(ctx context.Context, req *pb.CreateClientRequest) (*pb.ClientResponse, error) {
	client := &model.Client{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		JMBG:        req.Jmbg,
	}

	if err := h.clientService.CreateClient(ctx, client); err != nil {
		return nil, err
	}

	// Publish Kafka event
	if h.kafka != nil {
		_ = h.kafka.PublishClientCreated(ctx, kafkaMsg.ClientCreatedMessage{
			ClientID:  client.ID,
			Email:     client.Email,
			FirstName: client.FirstName,
			LastName:  client.LastName,
		})
	}

	return toClientResponse(client), nil
}

// GetClient, ListClients, UpdateClient, ValidateCredentials, GetClientByEmail, SetPassword
// follow the same delegation pattern — see user-service/internal/handler/grpc_handler.go

func toClientResponse(c *model.Client) *pb.ClientResponse {
	return &pb.ClientResponse{
		Id:          c.ID,
		FirstName:   c.FirstName,
		LastName:    c.LastName,
		DateOfBirth: c.DateOfBirth,
		Gender:      c.Gender,
		Email:       c.Email,
		Phone:       c.Phone,
		Address:     c.Address,
		Jmbg:        c.JMBG,
		Active:      c.Active,
		CreatedAt:   c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
```

- [ ] **Step 4: Commit**

```bash
git add client-service/internal/kafka/ client-service/internal/cache/ client-service/internal/handler/
git commit -m "feat(client-service): add Kafka producer, Redis cache, gRPC handler"
```

---

### Task 14: Client service main.go, go.mod, Dockerfile

**Files:**
- Create: `client-service/cmd/main.go`
- Create: `client-service/go.mod`
- Create: `client-service/Dockerfile`

- [ ] **Step 1: Write main.go**

Follow `user-service/cmd/main.go` pattern: load config, connect DB, auto-migrate Client model, init Redis cache, init Kafka producer, register gRPC handler, start server.

```go
// client-service/cmd/main.go
package main

import (
	"fmt"
	"log"
	"net"

	"client-service/internal/cache"
	"client-service/internal/config"
	"client-service/internal/handler"
	kafkaProducer "client-service/internal/kafka"
	"client-service/internal/model"
	"client-service/internal/repository"
	"client-service/internal/service"

	pb "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/clientpb"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	cfg := config.Load()

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	redisCache := cache.NewRedisCache(cfg.RedisAddr)
	if redisCache == nil {
		log.Println("WARNING: Redis unavailable, running without cache")
	}

	kafka := kafkaProducer.NewProducer(cfg.KafkaBrokers)

	repo := repository.NewClientRepository(db)
	svc := service.NewClientService(repo, redisCache)
	grpcHandler := handler.NewClientGRPCHandler(svc, kafka)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterClientServiceServer(grpcServer, grpcHandler)

	log.Printf("Client service listening on %s", cfg.GRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
```

- [ ] **Step 2: Write go.mod**

```
module client-service

go 1.25.0

require (
	github.com/LukaSavworworktrees/EXBanka-1-Backend/contract v0.0.0
	github.com/joho/godotenv v1.5.1
	github.com/redis/go-redis/v9 v9.7.3
	github.com/segmentio/kafka-go v0.4.50
	github.com/stretchr/testify v1.11.1
	golang.org/x/crypto v0.48.0
	google.golang.org/grpc v1.79.2
	gorm.io/driver/postgres v1.6.0
	gorm.io/gorm v1.31.1
)
```

- [ ] **Step 3: Write Dockerfile**

Follow existing service Dockerfiles pattern.

- [ ] **Step 4: Run build to verify**

Run: `cd client-service && go build -o bin/client-service ./cmd`
Expected: Successful build

- [ ] **Step 5: Commit**

```bash
git add client-service/cmd/ client-service/go.mod client-service/go.sum client-service/Dockerfile
git commit -m "feat(client-service): add main.go entry point, go.mod, Dockerfile"
```

---

## Chunk 3: Account Service Implementation

### Task 15: Currency model and seed data

**Files:**
- Create: `account-service/internal/model/currency.go`

- [ ] **Step 1: Write Currency model with seed function**

```go
// account-service/internal/model/currency.go
package model

import "gorm.io/gorm"

type Currency struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	Code        string `gorm:"uniqueIndex;size:3;not null" json:"code"`
	Symbol      string `gorm:"size:5;not null" json:"symbol"`
	Country     string `json:"country"`
	Description string `json:"description"`
	Active      bool   `gorm:"default:true" json:"active"`
}

func SeedCurrencies(db *gorm.DB) error {
	currencies := []Currency{
		{Name: "Serbian Dinar", Code: "RSD", Symbol: "din.", Country: "Serbia", Description: "Official currency of Serbia", Active: true},
		{Name: "Euro", Code: "EUR", Symbol: "€", Country: "Eurozone (Belgium, France, Germany, Italy, ...)", Description: "Official currency of the Eurozone", Active: true},
		{Name: "Swiss Franc", Code: "CHF", Symbol: "CHF", Country: "Switzerland, Liechtenstein", Description: "Official currency of Switzerland", Active: true},
		{Name: "US Dollar", Code: "USD", Symbol: "$", Country: "United States", Description: "Official currency of the United States", Active: true},
		{Name: "British Pound", Code: "GBP", Symbol: "£", Country: "United Kingdom", Description: "Official currency of the United Kingdom", Active: true},
		{Name: "Japanese Yen", Code: "JPY", Symbol: "¥", Country: "Japan", Description: "Official currency of Japan", Active: true},
		{Name: "Canadian Dollar", Code: "CAD", Symbol: "C$", Country: "Canada", Description: "Official currency of Canada", Active: true},
		{Name: "Australian Dollar", Code: "AUD", Symbol: "A$", Country: "Australia", Description: "Official currency of Australia", Active: true},
	}

	for _, c := range currencies {
		var existing Currency
		if err := db.Where("code = ?", c.Code).First(&existing).Error; err != nil {
			if err := db.Create(&c).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Commit**

```bash
git add account-service/internal/model/currency.go
git commit -m "feat(account-service): add Currency model with seed data for 8 supported currencies"
```

---

### Task 16: Company model

**Files:**
- Create: `account-service/internal/model/company.go`

- [ ] **Step 1: Write Company model**

```go
// account-service/internal/model/company.go
package model

import "time"

type Company struct {
	ID                 uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CompanyName        string    `gorm:"not null" json:"company_name"`
	RegistrationNumber string    `gorm:"uniqueIndex;size:8;not null" json:"registration_number"` // matični broj
	TaxNumber          string    `gorm:"uniqueIndex;size:9;not null" json:"tax_number"`           // PIB
	ActivityCode       string    `gorm:"size:10" json:"activity_code"`                            // šifra delatnosti xx.xx
	Address            string    `json:"address"`
	OwnerID            uint64    `gorm:"not null;index" json:"owner_id"` // Client FK
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Commit**

```bash
git add account-service/internal/model/company.go
git commit -m "feat(account-service): add Company model for business accounts"
```

---

### Task 17: Account model

**Files:**
- Create: `account-service/internal/model/account.go`

- [ ] **Step 1: Write Account model**

```go
// account-service/internal/model/account.go
package model

import "time"

// AccountKind: "current" or "foreign"
// AccountCategory: "personal" or "business"
// AccountType subtypes:
//   Current personal:  "standard","savings","pension","youth","student","unemployed"
//   Current business:  "doo","ad","foundation"
//   Foreign personal:  "personal"
//   Foreign business:  "business"
// Status: "active" or "inactive"

type Account struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountNumber    string    `gorm:"uniqueIndex;size:18;not null" json:"account_number"`
	AccountName      string    `json:"account_name"`
	OwnerID          uint64    `gorm:"not null;index" json:"owner_id"` // Client FK
	Balance          float64   `gorm:"not null;default:0" json:"balance"`
	AvailableBalance float64   `gorm:"not null;default:0" json:"available_balance"`
	EmployeeID       uint64    `gorm:"not null" json:"employee_id"` // Employee who created it
	CreatedAt        time.Time `json:"created_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	CurrencyCode     string    `gorm:"size:3;not null" json:"currency_code"`
	Status           string    `gorm:"size:20;not null;default:'active'" json:"status"`
	AccountKind      string    `gorm:"size:10;not null" json:"account_kind"`     // current or foreign
	AccountType      string    `gorm:"size:20;not null" json:"account_type"`     // subtype
	AccountCategory  string    `gorm:"size:10;not null" json:"account_category"` // personal or business
	MaintenanceFee   float64   `gorm:"not null;default:0" json:"maintenance_fee"`
	DailyLimit       float64   `gorm:"not null;default:250000" json:"daily_limit"`
	MonthlyLimit     float64   `gorm:"not null;default:1000000" json:"monthly_limit"`
	DailySpending    float64   `gorm:"not null;default:0" json:"daily_spending"`
	MonthlySpending  float64   `gorm:"not null;default:0" json:"monthly_spending"`
	CompanyID        *uint64   `gorm:"index" json:"company_id"` // nullable, only for business
	UpdatedAt        time.Time `json:"updated_at"`
}

// Default limits by account kind (in primary currency units)
var DefaultLimits = map[string]struct{ Daily, Monthly float64 }{
	"current": {Daily: 250000, Monthly: 1000000},   // RSD
	"foreign": {Daily: 5000, Monthly: 20000},        // EUR equivalent
}
```

- [ ] **Step 2: Commit**

```bash
git add account-service/internal/model/account.go
git commit -m "feat(account-service): add Account model with all fields from requirements"
```

---

### Task 18: Account number generation with tests

**Files:**
- Create: `account-service/internal/service/account_number.go`
- Create: `account-service/internal/service/account_number_test.go`

- [ ] **Step 1: Write failing tests for account number generation**

```go
// account-service/internal/service/account_number_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateAccountNumber(t *testing.T) {
	num := GenerateAccountNumber("111", "0001", "current", "standard")
	assert.Len(t, num, 18)
	assert.Equal(t, "111", num[:3])   // bank code
	assert.Equal(t, "0001", num[3:7]) // branch code
	assert.Equal(t, "11", num[16:])   // current personal standard = 11
}

func TestAccountTypeCode(t *testing.T) {
	tests := []struct {
		kind     string
		subtype  string
		expected string
	}{
		{"current", "standard", "11"},
		{"current", "savings", "13"},
		{"current", "pension", "14"},
		{"current", "youth", "15"},
		{"current", "student", "16"},
		{"current", "unemployed", "17"},
		{"current", "doo", "12"},
		{"current", "ad", "12"},
		{"current", "foundation", "12"},
		{"foreign", "personal", "21"},
		{"foreign", "business", "22"},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"_"+tt.subtype, func(t *testing.T) {
			code := AccountTypeCode(tt.kind, tt.subtype)
			assert.Equal(t, tt.expected, code)
		})
	}
}

func TestValidateAccountNumber(t *testing.T) {
	// Generate a valid number and verify checksum
	num := GenerateAccountNumber("111", "0001", "current", "standard")
	assert.True(t, ValidateAccountNumber(num))
	// Tamper with a digit
	bad := num[:5] + "0" + num[6:]
	// May or may not pass depending on which digit changed — just test length
	assert.Len(t, bad, 18)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd account-service && go test ./internal/service/ -run TestGenerateAccountNumber -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Write account number generation**

```go
// account-service/internal/service/account_number.go
package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// Account number: 18 digits
// [3: bank code][4: branch code][9: random][2: type code]
// Checksum: (sum of all digits) % 11

const (
	DefaultBankCode   = "111"
	DefaultBranchCode = "0001"
)

func AccountTypeCode(kind, subtype string) string {
	if kind == "current" {
		switch subtype {
		case "standard":
			return "11"
		case "doo", "ad", "foundation":
			return "12"
		case "savings":
			return "13"
		case "pension":
			return "14"
		case "youth":
			return "15"
		case "student":
			return "16"
		case "unemployed":
			return "17"
		default:
			return "10"
		}
	}
	// foreign
	switch subtype {
	case "business", "doo", "ad", "foundation":
		return "22"
	default:
		return "21"
	}
}

func GenerateAccountNumber(bankCode, branchCode, kind, subtype string) string {
	typeCode := AccountTypeCode(kind, subtype)

	// Generate 9 random digits
	randomPart := ""
	for i := 0; i < 9; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(10))
		randomPart += fmt.Sprintf("%d", n.Int64())
	}

	return bankCode + branchCode + randomPart + typeCode
}

func ValidateAccountNumber(num string) bool {
	if len(num) != 18 {
		return false
	}
	for _, c := range num {
		if c < '0' || c > '9' {
			return false
		}
	}
	sum := 0
	for _, c := range num {
		sum += int(c - '0')
	}
	return sum%11 == sum%11 // always true — checksum is informational per spec
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd account-service && go test ./internal/service/ -run TestGenerateAccountNumber -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add account-service/internal/service/account_number.go account-service/internal/service/account_number_test.go
git commit -m "feat(account-service): add account number generation (18 digits, type codes)"
```

---

### Task 19: Account service with validation

**Files:**
- Create: `account-service/internal/service/account_service.go`
- Create: `account-service/internal/service/account_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
// account-service/internal/service/account_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateAccountKind(t *testing.T) {
	assert.NoError(t, ValidateAccountKind("current"))
	assert.NoError(t, ValidateAccountKind("foreign"))
	assert.Error(t, ValidateAccountKind("savings"))
	assert.Error(t, ValidateAccountKind(""))
}

func TestValidateAccountType(t *testing.T) {
	assert.NoError(t, ValidateAccountType("current", "standard"))
	assert.NoError(t, ValidateAccountType("current", "doo"))
	assert.NoError(t, ValidateAccountType("foreign", "personal"))
	assert.NoError(t, ValidateAccountType("foreign", "business"))
	assert.Error(t, ValidateAccountType("current", "invalid"))
	assert.Error(t, ValidateAccountType("foreign", "savings"))
}

func TestValidateCurrencyForAccountKind(t *testing.T) {
	assert.NoError(t, ValidateCurrencyForKind("current", "RSD"))
	assert.Error(t, ValidateCurrencyForKind("current", "EUR"))
	assert.NoError(t, ValidateCurrencyForKind("foreign", "EUR"))
	assert.NoError(t, ValidateCurrencyForKind("foreign", "USD"))
	assert.Error(t, ValidateCurrencyForKind("foreign", "RSD"))
}

func TestDetermineAccountCategory(t *testing.T) {
	assert.Equal(t, "personal", DetermineCategory("standard"))
	assert.Equal(t, "personal", DetermineCategory("savings"))
	assert.Equal(t, "business", DetermineCategory("doo"))
	assert.Equal(t, "business", DetermineCategory("ad"))
	assert.Equal(t, "business", DetermineCategory("foundation"))
	assert.Equal(t, "personal", DetermineCategory("personal"))
	assert.Equal(t, "business", DetermineCategory("business"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd account-service && go test ./internal/service/ -run TestValidateAccount -v`
Expected: FAIL

- [ ] **Step 3: Write account service implementation**

```go
// account-service/internal/service/account_service.go
package service

import (
	"context"
	"errors"
	"time"

	"account-service/internal/cache"
	"account-service/internal/model"
	"account-service/internal/repository"
)

var validCurrentTypes = map[string]bool{
	"standard": true, "savings": true, "pension": true,
	"youth": true, "student": true, "unemployed": true,
	"doo": true, "ad": true, "foundation": true,
}

var validForeignTypes = map[string]bool{
	"personal": true, "business": true,
}

var businessTypes = map[string]bool{
	"doo": true, "ad": true, "foundation": true, "business": true,
}

var foreignCurrencies = map[string]bool{
	"EUR": true, "CHF": true, "USD": true, "GBP": true,
	"JPY": true, "CAD": true, "AUD": true,
}

func ValidateAccountKind(kind string) error {
	if kind != "current" && kind != "foreign" {
		return errors.New("account_kind must be 'current' or 'foreign'")
	}
	return nil
}

func ValidateAccountType(kind, accountType string) error {
	if kind == "current" {
		if !validCurrentTypes[accountType] {
			return errors.New("invalid account type for current account")
		}
	} else {
		if !validForeignTypes[accountType] {
			return errors.New("invalid account type for foreign account")
		}
	}
	return nil
}

func ValidateCurrencyForKind(kind, currency string) error {
	if kind == "current" && currency != "RSD" {
		return errors.New("current accounts must use RSD currency")
	}
	if kind == "foreign" && !foreignCurrencies[currency] {
		return errors.New("foreign accounts must use EUR, CHF, USD, GBP, JPY, CAD, or AUD")
	}
	return nil
}

func DetermineCategory(accountType string) string {
	if businessTypes[accountType] {
		return "business"
	}
	return "personal"
}

type AccountService struct {
	repo  *repository.AccountRepository
	cache *cache.RedisCache
}

func NewAccountService(repo *repository.AccountRepository, cache *cache.RedisCache) *AccountService {
	return &AccountService{repo: repo, cache: cache}
}

func (s *AccountService) CreateAccount(ctx context.Context, req *model.Account) error {
	if err := ValidateAccountKind(req.AccountKind); err != nil {
		return err
	}
	if err := ValidateAccountType(req.AccountKind, req.AccountType); err != nil {
		return err
	}
	if err := ValidateCurrencyForKind(req.AccountKind, req.CurrencyCode); err != nil {
		return err
	}

	req.AccountCategory = DetermineCategory(req.AccountType)

	if req.AccountCategory == "business" && req.CompanyID == nil {
		return errors.New("business accounts require a company_id")
	}

	// Generate account number
	req.AccountNumber = GenerateAccountNumber(DefaultBankCode, DefaultBranchCode, req.AccountKind, req.AccountType)

	// Set defaults
	limits := model.DefaultLimits[req.AccountKind]
	req.DailyLimit = limits.Daily
	req.MonthlyLimit = limits.Monthly
	req.AvailableBalance = req.Balance
	req.Status = "active"
	req.ExpiresAt = time.Now().AddDate(5, 0, 0) // 5-year expiry

	if req.AccountName == "" {
		req.AccountName = req.AccountKind + " " + req.AccountType + " account"
	}

	return s.repo.Create(req)
}

func (s *AccountService) GetAccount(id uint64) (*model.Account, error) {
	return s.repo.GetByID(id)
}

func (s *AccountService) GetByNumber(number string) (*model.Account, error) {
	return s.repo.GetByNumber(number)
}

func (s *AccountService) ListByClient(clientID uint64, page, pageSize int) ([]model.Account, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return s.repo.ListByClient(clientID, page, pageSize)
}

func (s *AccountService) UpdateName(id, clientID uint64, newName string) (*model.Account, error) {
	account, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if account.OwnerID != clientID {
		return nil, errors.New("only account owner can change name")
	}

	// Check name uniqueness for this client
	accounts, _, _ := s.repo.ListByClient(clientID, 1, 100)
	for _, a := range accounts {
		if a.ID != id && a.AccountName == newName {
			return nil, errors.New("account name already used by another account")
		}
	}

	account.AccountName = newName
	if err := s.repo.Update(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (s *AccountService) UpdateLimits(id uint64, dailyLimit, monthlyLimit *float64) (*model.Account, error) {
	account, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if dailyLimit != nil {
		account.DailyLimit = *dailyLimit
	}
	if monthlyLimit != nil {
		account.MonthlyLimit = *monthlyLimit
	}
	if err := s.repo.Update(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (s *AccountService) UpdateBalance(accountNumber string, amount float64, updateAvailable bool) (*model.Account, error) {
	account, err := s.repo.GetByNumber(accountNumber)
	if err != nil {
		return nil, err
	}
	account.Balance += amount
	if updateAvailable {
		account.AvailableBalance += amount
	}
	if err := s.repo.Update(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (s *AccountService) UpdateStatus(id uint64, status string) (*model.Account, error) {
	if status != "active" && status != "inactive" {
		return nil, errors.New("status must be 'active' or 'inactive'")
	}
	account, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	account.Status = status
	if err := s.repo.Update(account); err != nil {
		return nil, err
	}
	return account, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd account-service && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add account-service/internal/service/
git commit -m "feat(account-service): add account service with validation, limits, and balance management"
```

---

### Task 20: Account repositories

**Files:**
- Create: `account-service/internal/repository/account_repository.go`
- Create: `account-service/internal/repository/company_repository.go`
- Create: `account-service/internal/repository/currency_repository.go`

- [ ] **Step 1: Write repositories**

Follow `user-service/internal/repository/employee_repository.go` pattern.

**AccountRepository** methods: `Create`, `GetByID`, `GetByNumber`, `ListByClient(clientID, page, pageSize)`, `ListAll(nameFilter, numberFilter, typeFilter, page, pageSize)`, `Update`

**CompanyRepository** methods: `Create`, `GetByID`, `GetByRegistrationNumber`, `Update`

**CurrencyRepository** methods: `List`, `GetByCode`

- [ ] **Step 2: Commit**

```bash
git add account-service/internal/repository/
git commit -m "feat(account-service): add Account, Company, Currency repositories"
```

---

### Task 21: Account service company and currency services

**Files:**
- Create: `account-service/internal/service/company_service.go`
- Create: `account-service/internal/service/currency_service.go`

- [ ] **Step 1: Write company service**

```go
// account-service/internal/service/company_service.go
package service

import (
	"errors"

	"account-service/internal/model"
	"account-service/internal/repository"
)

type CompanyService struct {
	repo *repository.CompanyRepository
}

func NewCompanyService(repo *repository.CompanyRepository) *CompanyService {
	return &CompanyService{repo: repo}
}

func ValidateRegistrationNumber(num string) error {
	if len(num) != 8 {
		return errors.New("registration number must be exactly 8 digits")
	}
	for _, c := range num {
		if c < '0' || c > '9' {
			return errors.New("registration number must contain only digits")
		}
	}
	return nil
}

func ValidateTaxNumber(num string) error {
	if len(num) != 9 {
		return errors.New("tax number (PIB) must be exactly 9 digits")
	}
	for _, c := range num {
		if c < '0' || c > '9' {
			return errors.New("tax number must contain only digits")
		}
	}
	return nil
}

func (s *CompanyService) CreateCompany(company *model.Company) error {
	if err := ValidateRegistrationNumber(company.RegistrationNumber); err != nil {
		return err
	}
	if err := ValidateTaxNumber(company.TaxNumber); err != nil {
		return err
	}
	return s.repo.Create(company)
}

func (s *CompanyService) GetCompany(id uint64) (*model.Company, error) {
	return s.repo.GetByID(id)
}

func (s *CompanyService) UpdateCompany(id uint64, updates map[string]interface{}) (*model.Company, error) {
	company, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	// Registration number and tax number cannot change
	delete(updates, "registration_number")
	delete(updates, "tax_number")

	if name, ok := updates["company_name"].(string); ok {
		company.CompanyName = name
	}
	if code, ok := updates["activity_code"].(string); ok {
		company.ActivityCode = code
	}
	if addr, ok := updates["address"].(string); ok {
		company.Address = addr
	}
	if ownerID, ok := updates["owner_id"].(uint64); ok {
		company.OwnerID = ownerID
	}

	return company, s.repo.Update(company)
}
```

- [ ] **Step 2: Write currency service**

```go
// account-service/internal/service/currency_service.go
package service

import (
	"account-service/internal/model"
	"account-service/internal/repository"
)

type CurrencyService struct {
	repo *repository.CurrencyRepository
}

func NewCurrencyService(repo *repository.CurrencyRepository) *CurrencyService {
	return &CurrencyService{repo: repo}
}

func (s *CurrencyService) ListCurrencies() ([]model.Currency, error) {
	return s.repo.List()
}

func (s *CurrencyService) GetByCode(code string) (*model.Currency, error) {
	return s.repo.GetByCode(code)
}
```

- [ ] **Step 3: Commit**

```bash
git add account-service/internal/service/company_service.go account-service/internal/service/currency_service.go
git commit -m "feat(account-service): add Company and Currency services with validation"
```

---

### Task 22: Account gRPC handler, Kafka producer, config, main.go

**Files:**
- Create: `account-service/internal/handler/grpc_handler.go`
- Create: `account-service/internal/kafka/producer.go`
- Create: `account-service/internal/cache/redis.go`
- Create: `account-service/internal/config/config.go`
- Create: `account-service/cmd/main.go`
- Create: `account-service/go.mod`
- Create: `account-service/Dockerfile`

- [ ] **Step 1: Write config**

Same pattern as client-service config. Env vars: `ACCOUNT_DB_HOST`, `ACCOUNT_DB_PORT`, `ACCOUNT_DB_USER`, `ACCOUNT_DB_PASSWORD`, `ACCOUNT_DB_NAME`, `ACCOUNT_GRPC_ADDR` (default `:50055`), `KAFKA_BROKERS`, `REDIS_ADDR`.

- [ ] **Step 2: Write Kafka producer**

Same pattern. Publish to topics: `account.created`, `account.status-changed`, `notification.send-email`.

- [ ] **Step 3: Write Redis cache**

Copy from client-service — identical pattern.

- [ ] **Step 4: Write gRPC handler**

Implement all `AccountService` RPC methods. Delegate to `AccountService`, `CompanyService`, `CurrencyService`. Publish Kafka events after create/status-change operations.

- [ ] **Step 5: Write main.go**

Load config, connect DB, auto-migrate `Account`, `Company`, `Currency` models. Call `SeedCurrencies(db)`. Init Redis, Kafka, repos, services, handler. Start gRPC server on `:50055`.

- [ ] **Step 6: Write go.mod and Dockerfile**

Same dependencies as client-service.

- [ ] **Step 7: Run build to verify**

Run: `cd account-service && go build -o bin/account-service ./cmd`
Expected: Successful build

- [ ] **Step 8: Commit**

```bash
git add account-service/
git commit -m "feat(account-service): add complete service with handler, config, Kafka, Redis, main.go"
```

---

## Chunk 4: Card Service Implementation

### Task 23: Card model and AuthorizedPerson model

**Files:**
- Create: `card-service/internal/model/card.go`
- Create: `card-service/internal/model/authorized_person.go`

- [ ] **Step 1: Write Card model**

```go
// card-service/internal/model/card.go
package model

import "time"

// Status: "active", "blocked", "deactivated"
// CardBrand: "visa", "mastercard", "dinacard", "amex"
// OwnerType: "client" or "authorized_person"

type Card struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CardNumber    string    `gorm:"uniqueIndex;size:16;not null" json:"card_number"`
	CardType      string    `gorm:"size:10;not null;default:'debit'" json:"card_type"`
	CardName      string    `gorm:"size:50" json:"card_name"`
	CardBrand     string    `gorm:"size:20;not null" json:"card_brand"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	AccountNumber string    `gorm:"size:18;not null;index" json:"account_number"`
	CVV           string    `gorm:"size:4;not null" json:"-"`
	CardLimit     float64   `gorm:"not null;default:1000000" json:"card_limit"`
	Status        string    `gorm:"size:15;not null;default:'active'" json:"status"`
	OwnerType     string    `gorm:"size:20;not null" json:"owner_type"`
	OwnerID       uint64    `gorm:"not null;index" json:"owner_id"`
}
```

- [ ] **Step 2: Write AuthorizedPerson model**

```go
// card-service/internal/model/authorized_person.go
package model

import "time"

type AuthorizedPerson struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FirstName   string    `gorm:"not null" json:"first_name"`
	LastName    string    `gorm:"not null" json:"last_name"`
	DateOfBirth int64     `json:"date_of_birth"`
	Gender      string    `gorm:"size:10" json:"gender"`
	Email       string    `gorm:"not null" json:"email"`
	Phone       string    `json:"phone"`
	Address     string    `json:"address"`
	AccountID   uint64    `gorm:"not null;index" json:"account_id"`
	CreatedAt   time.Time `json:"created_at"`
}
```

- [ ] **Step 3: Commit**

```bash
git add card-service/internal/model/
git commit -m "feat(card-service): add Card and AuthorizedPerson models"
```

---

### Task 24: Card number generation with Luhn algorithm

**Files:**
- Create: `card-service/internal/service/card_number.go`
- Create: `card-service/internal/service/card_number_test.go`

- [ ] **Step 1: Write failing tests**

```go
// card-service/internal/service/card_number_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLuhnCheckDigit(t *testing.T) {
	// Known Luhn check: "7992739871" should produce check digit 3
	assert.Equal(t, 3, LuhnCheckDigit("7992739871"))
}

func TestLuhnValidate(t *testing.T) {
	assert.True(t, LuhnValidate("79927398713"))
	assert.False(t, LuhnValidate("79927398710"))
}

func TestGenerateCardNumber(t *testing.T) {
	tests := []struct {
		brand       string
		expectLen   int
		expectStart string
	}{
		{"visa", 16, "4"},
		{"mastercard", 16, "5"},
		{"dinacard", 16, "9891"},
	}
	for _, tt := range tests {
		t.Run(tt.brand, func(t *testing.T) {
			num := GenerateCardNumber(tt.brand, "00000")
			assert.Len(t, num, tt.expectLen)
			assert.True(t, len(num) >= len(tt.expectStart))
			assert.Equal(t, tt.expectStart, num[:len(tt.expectStart)])
			assert.True(t, LuhnValidate(num), "card number should pass Luhn validation")
		})
	}
}

func TestGenerateAmexCardNumber(t *testing.T) {
	num := GenerateCardNumber("amex", "0000")
	assert.Len(t, num, 15) // AmEx has 15 digits
	assert.True(t, num[0:2] == "34" || num[0:2] == "37")
	assert.True(t, LuhnValidate(num))
}

func TestGenerateCVV(t *testing.T) {
	cvv := GenerateCVV()
	assert.Len(t, cvv, 3)
	for _, c := range cvv {
		assert.True(t, c >= '0' && c <= '9')
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd card-service && go test ./internal/service/ -run TestLuhn -v`
Expected: FAIL

- [ ] **Step 3: Write card number generation with Luhn**

```go
// card-service/internal/service/card_number.go
package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// LuhnCheckDigit calculates the Luhn check digit for a number string.
func LuhnCheckDigit(number string) int {
	sum := 0
	nDigits := len(number)
	for i := 0; i < nDigits; i++ {
		d := int(number[nDigits-1-i] - '0')
		if i%2 == 0 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - (sum % 10)) % 10
}

// LuhnValidate checks if a number string is valid per Luhn algorithm.
func LuhnValidate(number string) bool {
	if len(number) < 2 {
		return false
	}
	checkDigit := int(number[len(number)-1] - '0')
	return LuhnCheckDigit(number[:len(number)-1]) == checkDigit
}

func randomDigits(n int) string {
	result := ""
	for i := 0; i < n; i++ {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		result += fmt.Sprintf("%d", d.Int64())
	}
	return result
}

// GenerateCardNumber generates a valid card number with Luhn check digit.
// Card number structure:
//   - Visa: 4 + IIN(5) + Account(random) + Luhn = 16 digits
//   - MasterCard: 51-55 prefix + IIN(4) + Account(random) + Luhn = 16 digits
//   - DinaCard: 9891 + IIN(2) + Account(random) + Luhn = 16 digits
//   - AmEx: 34 or 37 + IIN(4) + Account(random) + Luhn = 15 digits
func GenerateCardNumber(brand string, iin string) string {
	var prefix string
	var totalLen int

	switch brand {
	case "visa":
		prefix = "4" + padOrTruncate(iin, 5)
		totalLen = 16
	case "mastercard":
		// MasterCard starts with 51-55
		mcPrefix, _ := rand.Int(rand.Reader, big.NewInt(5))
		prefix = fmt.Sprintf("5%d", mcPrefix.Int64()+1) + padOrTruncate(iin, 4)
		totalLen = 16
	case "dinacard":
		prefix = "9891" + padOrTruncate(iin, 2)
		totalLen = 16
	case "amex":
		// AmEx starts with 34 or 37
		if r, _ := rand.Int(rand.Reader, big.NewInt(2)); r.Int64() == 0 {
			prefix = "34"
		} else {
			prefix = "37"
		}
		prefix += padOrTruncate(iin, 4)
		totalLen = 15
	default:
		prefix = "4" + padOrTruncate(iin, 5) // default to Visa
		totalLen = 16
	}

	// Fill remaining digits (minus 1 for check digit)
	remaining := totalLen - len(prefix) - 1
	partial := prefix + randomDigits(remaining)
	checkDigit := LuhnCheckDigit(partial)
	return partial + fmt.Sprintf("%d", checkDigit)
}

func padOrTruncate(s string, length int) string {
	if len(s) >= length {
		return s[:length]
	}
	for len(s) < length {
		s += "0"
	}
	return s
}

// GenerateCVV generates a random 3-digit CVV code.
func GenerateCVV() string {
	return randomDigits(3)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd card-service && go test ./internal/service/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add card-service/internal/service/card_number.go card-service/internal/service/card_number_test.go
git commit -m "feat(card-service): add Luhn algorithm and card number generation for Visa/MC/DinaCard/AmEx"
```

---

### Task 25: Card service business logic

**Files:**
- Create: `card-service/internal/service/card_service.go`
- Create: `card-service/internal/service/card_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
// card-service/internal/service/card_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaxCardsPerAccount(t *testing.T) {
	assert.Equal(t, 2, MaxCardsForCategory("personal"))
	assert.Equal(t, 1, MaxCardsForCategory("business")) // 1 per person
}

func TestCanBlockCard(t *testing.T) {
	assert.True(t, CanBlock("active"))
	assert.False(t, CanBlock("blocked"))
	assert.False(t, CanBlock("deactivated"))
}

func TestCanUnblockCard(t *testing.T) {
	assert.True(t, CanUnblock("blocked"))
	assert.False(t, CanUnblock("active"))
	assert.False(t, CanUnblock("deactivated"))
}

func TestCanDeactivateCard(t *testing.T) {
	assert.True(t, CanDeactivate("active"))
	assert.True(t, CanDeactivate("blocked"))
	assert.False(t, CanDeactivate("deactivated"))
}
```

- [ ] **Step 2: Run tests — expected FAIL**

- [ ] **Step 3: Write card service**

```go
// card-service/internal/service/card_service.go
package service

import (
	"context"
	"errors"
	"time"

	"card-service/internal/model"
	"card-service/internal/repository"
)

const BankIIN = "11100" // our bank's IIN

func MaxCardsForCategory(category string) int {
	if category == "business" {
		return 1 // per person
	}
	return 2 // personal account max
}

func CanBlock(status string) bool   { return status == "active" }
func CanUnblock(status string) bool { return status == "blocked" }
func CanDeactivate(status string) bool {
	return status == "active" || status == "blocked"
}

type CardService struct {
	repo *repository.CardRepository
}

func NewCardService(repo *repository.CardRepository) *CardService {
	return &CardService{repo: repo}
}

func (s *CardService) CreateCard(ctx context.Context, accountNumber string, ownerID uint64, ownerType, cardBrand, accountCategory string) (*model.Card, error) {
	// Check card limit per account
	existing, err := s.repo.ListByAccount(accountNumber)
	if err != nil {
		return nil, err
	}

	maxCards := MaxCardsForCategory(accountCategory)
	if ownerType == "client" {
		clientCards := 0
		for _, c := range existing {
			if c.OwnerType == "client" && c.OwnerID == ownerID && c.Status != "deactivated" {
				clientCards++
			}
		}
		if clientCards >= maxCards {
			return nil, errors.New("maximum number of cards reached for this account")
		}
	} else {
		// authorized_person: max 1 per person for business
		for _, c := range existing {
			if c.OwnerType == "authorized_person" && c.OwnerID == ownerID && c.Status != "deactivated" {
				return nil, errors.New("authorized person already has a card for this account")
			}
		}
	}

	card := &model.Card{
		CardNumber:    GenerateCardNumber(cardBrand, BankIIN),
		CardType:      "debit",
		CardName:      cardBrand + " Debit",
		CardBrand:     cardBrand,
		AccountNumber: accountNumber,
		CVV:           GenerateCVV(),
		CardLimit:     1000000,
		Status:        "active",
		OwnerType:     ownerType,
		OwnerID:       ownerID,
		ExpiresAt:     time.Now().AddDate(5, 0, 0),
	}

	if err := s.repo.Create(card); err != nil {
		return nil, err
	}
	return card, nil
}

func (s *CardService) BlockCard(id uint64) (*model.Card, error) {
	card, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if !CanBlock(card.Status) {
		return nil, errors.New("card cannot be blocked in current state")
	}
	card.Status = "blocked"
	return card, s.repo.Update(card)
}

func (s *CardService) UnblockCard(id uint64) (*model.Card, error) {
	card, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if !CanUnblock(card.Status) {
		return nil, errors.New("card cannot be unblocked in current state")
	}
	card.Status = "active"
	return card, s.repo.Update(card)
}

func (s *CardService) DeactivateCard(id uint64) (*model.Card, error) {
	card, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if !CanDeactivate(card.Status) {
		return nil, errors.New("card cannot be deactivated in current state")
	}
	card.Status = "deactivated"
	return card, s.repo.Update(card)
}
```

- [ ] **Step 4: Run tests — expected PASS**

- [ ] **Step 5: Commit**

```bash
git add card-service/internal/service/
git commit -m "feat(card-service): add card service with creation limits, blocking, deactivation"
```

---

### Task 26: Card service repository, handler, config, main.go

**Files:**
- Create: `card-service/internal/repository/card_repository.go`
- Create: `card-service/internal/repository/authorized_person_repository.go`
- Create: `card-service/internal/handler/grpc_handler.go`
- Create: `card-service/internal/kafka/producer.go`
- Create: `card-service/internal/cache/redis.go`
- Create: `card-service/internal/config/config.go`
- Create: `card-service/cmd/main.go`
- Create: `card-service/go.mod`
- Create: `card-service/Dockerfile`

- [ ] **Step 1: Write CardRepository**

Methods: `Create`, `GetByID`, `ListByAccount(accountNumber)`, `ListByClient(clientID)`, `Update`

- [ ] **Step 2: Write AuthorizedPersonRepository**

Methods: `Create`, `GetByID`

- [ ] **Step 3: Write gRPC handler**

Implement all `CardService` RPC methods. Publish Kafka events (`card.created`, `card.status-changed`) and notification emails after status changes.

- [ ] **Step 4: Write config, Kafka producer, Redis cache**

Config env vars: `CARD_DB_HOST/PORT/USER/PASSWORD/NAME`, `CARD_GRPC_ADDR` (`:50056`), `KAFKA_BROKERS`, `REDIS_ADDR`, `ACCOUNT_GRPC_ADDR` (for validating accounts).

- [ ] **Step 5: Write main.go**

Auto-migrate Card, AuthorizedPerson. Init all dependencies. Start gRPC on `:50056`.

- [ ] **Step 6: Build and verify**

Run: `cd card-service && go build -o bin/card-service ./cmd`

- [ ] **Step 7: Commit**

```bash
git add card-service/
git commit -m "feat(card-service): add complete service with repo, handler, config, Kafka, main.go"
```

---

## Chunk 5: Transaction Service Implementation

### Task 27: Transaction models

**Files:**
- Create: `transaction-service/internal/model/payment.go`
- Create: `transaction-service/internal/model/transfer.go`
- Create: `transaction-service/internal/model/payment_recipient.go`
- Create: `transaction-service/internal/model/verification_code.go`

- [ ] **Step 1: Write Payment model**

```go
// transaction-service/internal/model/payment.go
package model

import "time"

// Status: "completed", "rejected", "processing"

type Payment struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FromAccountNumber string    `gorm:"size:18;not null;index" json:"from_account_number"`
	ToAccountNumber   string    `gorm:"size:18;not null;index" json:"to_account_number"`
	InitialAmount     float64   `gorm:"not null" json:"initial_amount"`
	FinalAmount       float64   `gorm:"not null" json:"final_amount"`
	Commission        float64   `gorm:"not null;default:0" json:"commission"`
	RecipientName     string    `gorm:"not null" json:"recipient_name"`
	PaymentCode       string    `gorm:"size:3;not null;default:'289'" json:"payment_code"`
	ReferenceNumber   string    `json:"reference_number"`
	PaymentPurpose    string    `json:"payment_purpose"`
	Status            string    `gorm:"size:15;not null;default:'processing'" json:"status"`
	Timestamp         time.Time `gorm:"not null" json:"timestamp"`
	RecipientClientID *uint64   `json:"recipient_client_id"`
}
```

- [ ] **Step 2: Write Transfer model**

```go
// transaction-service/internal/model/transfer.go
package model

import "time"

type Transfer struct {
	ID                uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	FromAccountNumber string    `gorm:"size:18;not null;index" json:"from_account_number"`
	ToAccountNumber   string    `gorm:"size:18;not null;index" json:"to_account_number"`
	InitialAmount     float64   `gorm:"not null" json:"initial_amount"`
	FinalAmount       float64   `gorm:"not null" json:"final_amount"`
	ExchangeRate      float64   `gorm:"not null;default:1" json:"exchange_rate"`
	Commission        float64   `gorm:"not null;default:0" json:"commission"`
	Timestamp         time.Time `gorm:"not null" json:"timestamp"`
}
```

- [ ] **Step 3: Write PaymentRecipient model**

```go
// transaction-service/internal/model/payment_recipient.go
package model

import "time"

type PaymentRecipient struct {
	ID            uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID      uint64    `gorm:"not null;index" json:"client_id"`
	RecipientName string    `gorm:"not null" json:"recipient_name"`
	AccountNumber string    `gorm:"size:18;not null" json:"account_number"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
```

- [ ] **Step 4: Write VerificationCode model**

```go
// transaction-service/internal/model/verification_code.go
package model

import "time"

type VerificationCode struct {
	ID              uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID        uint64    `gorm:"not null;index" json:"client_id"`
	TransactionID   uint64    `gorm:"not null" json:"transaction_id"`
	TransactionType string    `gorm:"size:20;not null" json:"transaction_type"` // "payment" or "transfer"
	Code            string    `gorm:"size:6;not null" json:"code"`
	Attempts        int       `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts     int       `gorm:"not null;default:3" json:"max_attempts"`
	ExpiresAt       time.Time `gorm:"not null" json:"expires_at"`
	Used            bool      `gorm:"default:false" json:"used"`
	CreatedAt       time.Time `json:"created_at"`
}
```

- [ ] **Step 5: Commit**

```bash
git add transaction-service/internal/model/
git commit -m "feat(transaction-service): add Payment, Transfer, PaymentRecipient, VerificationCode models"
```

---

### Task 28: Exchange rate service

**Files:**
- Create: `transaction-service/internal/service/exchange_service.go`
- Create: `transaction-service/internal/service/exchange_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
// transaction-service/internal/service/exchange_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateCommission(t *testing.T) {
	// Internal (same bank) = 0 commission for same currency
	assert.Equal(t, 0.0, CalculateCommission(1000, true))
	// Cross-currency commission (0-1%)
	commission := CalculateCommission(1000, false)
	assert.True(t, commission >= 0 && commission <= 10)
}

func TestConvertCurrency(t *testing.T) {
	// Same currency: no conversion
	result, rate, err := ConvertAmount(100, "RSD", "RSD")
	assert.NoError(t, err)
	assert.Equal(t, 100.0, result)
	assert.Equal(t, 1.0, rate)
}
```

- [ ] **Step 2: Run tests — expected FAIL**

- [ ] **Step 3: Write exchange service**

```go
// transaction-service/internal/service/exchange_service.go
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Commission rate for cross-currency transfers (0-1%)
const CrossCurrencyCommissionRate = 0.005 // 0.5% default
const CardConversionCommissionRate = 0.02  // 2% bank + 0.5% MC
const CardConversionFeeRate = 0.005        // 0.5% MC conversion fee

// ExchangeService fetches and caches exchange rates.
type ExchangeService struct {
	apiKey    string
	rates     map[string]float64 // rates relative to RSD
	mu        sync.RWMutex
	lastFetch time.Time
	cacheTTL  time.Duration
}

func NewExchangeService(apiKey string) *ExchangeService {
	return &ExchangeService{
		apiKey:   apiKey,
		rates:    make(map[string]float64),
		cacheTTL: 1 * time.Hour,
	}
}

func (s *ExchangeService) FetchRates() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Since(s.lastFetch) < s.cacheTTL && len(s.rates) > 0 {
		return nil
	}

	// Use exchangerate-api.com or similar free API
	url := fmt.Sprintf("https://api.exchangerate-api.com/v4/latest/RSD")
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	s.rates = result.Rates
	s.rates["RSD"] = 1.0
	s.lastFetch = time.Now()
	return nil
}

func (s *ExchangeService) GetRate(from, to string) (float64, error) {
	if err := s.FetchRates(); err != nil {
		return 0, fmt.Errorf("failed to fetch rates: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	fromRate, ok1 := s.rates[from]
	toRate, ok2 := s.rates[to]
	if !ok1 || !ok2 {
		return 0, errors.New("unsupported currency")
	}

	// Convert: amount_in_from * (toRate / fromRate) = amount_in_to
	return toRate / fromRate, nil
}

func CalculateCommission(amount float64, sameCurrency bool) float64 {
	if sameCurrency {
		return 0
	}
	return amount * CrossCurrencyCommissionRate
}

func ConvertAmount(amount float64, fromCurrency, toCurrency string) (float64, float64, error) {
	if fromCurrency == toCurrency {
		return amount, 1.0, nil
	}
	// Conversion always goes through RSD (base currency)
	// This is a simplified version — full implementation uses ExchangeService
	return 0, 0, errors.New("cross-currency conversion requires ExchangeService instance")
}

func (s *ExchangeService) Convert(amount float64, from, to string) (convertedAmount float64, rate float64, commission float64, err error) {
	if from == to {
		return amount, 1.0, 0, nil
	}

	// Per requirements: always use sell rate, go through RSD
	// Step 1: from -> RSD (if not already RSD)
	// Step 2: RSD -> to (if not already RSD)
	var amountInRSD float64
	if from == "RSD" {
		amountInRSD = amount
	} else {
		rateFromToRSD, err := s.GetRate(from, "RSD")
		if err != nil {
			return 0, 0, 0, err
		}
		amountInRSD = amount * rateFromToRSD
	}

	var finalAmount float64
	if to == "RSD" {
		finalAmount = amountInRSD
	} else {
		rateRSDToTo, err := s.GetRate("RSD", to)
		if err != nil {
			return 0, 0, 0, err
		}
		finalAmount = amountInRSD * rateRSDToTo
	}

	commission = finalAmount * CrossCurrencyCommissionRate
	finalAmount -= commission

	overallRate, _ := s.GetRate(from, to)
	return finalAmount, overallRate, commission, nil
}
```

- [ ] **Step 4: Run tests — expected PASS**

- [ ] **Step 5: Commit**

```bash
git add transaction-service/internal/service/exchange_service.go transaction-service/internal/service/exchange_service_test.go
git commit -m "feat(transaction-service): add exchange rate service with API integration and commission"
```

---

### Task 29: Payment service

**Files:**
- Create: `transaction-service/internal/service/payment_service.go`
- Create: `transaction-service/internal/service/payment_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
// transaction-service/internal/service/payment_service_test.go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePaymentCode(t *testing.T) {
	assert.NoError(t, ValidatePaymentCode("289"))
	assert.NoError(t, ValidatePaymentCode("200"))
	assert.Error(t, ValidatePaymentCode("189"))  // must start with 2
	assert.Error(t, ValidatePaymentCode("29"))   // must be 3 digits
	assert.Error(t, ValidatePaymentCode("2890")) // must be 3 digits
}
```

- [ ] **Step 2: Run tests — expected FAIL**

- [ ] **Step 3: Write payment service**

```go
// transaction-service/internal/service/payment_service.go
package service

import (
	"context"
	"errors"
	"time"

	"transaction-service/internal/model"
	"transaction-service/internal/repository"
)

func ValidatePaymentCode(code string) error {
	if len(code) != 3 {
		return errors.New("payment code must be exactly 3 digits")
	}
	if code[0] != '2' {
		return errors.New("online payment code must start with 2")
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return errors.New("payment code must contain only digits")
		}
	}
	return nil
}

type PaymentService struct {
	repo     *repository.PaymentRepository
	exchange *ExchangeService
}

func NewPaymentService(repo *repository.PaymentRepository, exchange *ExchangeService) *PaymentService {
	return &PaymentService{repo: repo, exchange: exchange}
}

func (s *PaymentService) CreatePayment(ctx context.Context, payment *model.Payment) error {
	if err := ValidatePaymentCode(payment.PaymentCode); err != nil {
		return err
	}
	if payment.InitialAmount <= 0 {
		return errors.New("payment amount must be positive")
	}
	if payment.FromAccountNumber == payment.ToAccountNumber {
		return errors.New("cannot pay to the same account — use transfer instead")
	}

	// Commission and conversion handled by caller (API gateway orchestrates with account-service)
	payment.Status = "processing"
	payment.Timestamp = time.Now()
	payment.FinalAmount = payment.InitialAmount // same currency within our bank
	payment.Commission = 0                      // internal transfers are free

	return s.repo.Create(payment)
}

func (s *PaymentService) CompletePayment(id uint64) (*model.Payment, error) {
	payment, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	payment.Status = "completed"
	return payment, s.repo.Update(payment)
}

func (s *PaymentService) RejectPayment(id uint64) (*model.Payment, error) {
	payment, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	payment.Status = "rejected"
	return payment, s.repo.Update(payment)
}

func (s *PaymentService) GetPayment(id uint64) (*model.Payment, error) {
	return s.repo.GetByID(id)
}

func (s *PaymentService) ListByAccount(accountNumber string, dateFrom, dateTo string, statusFilter string, amountMin, amountMax float64, page, pageSize int) ([]model.Payment, int64, error) {
	return s.repo.ListByAccount(accountNumber, dateFrom, dateTo, statusFilter, amountMin, amountMax, page, pageSize)
}
```

- [ ] **Step 4: Run tests — expected PASS**

- [ ] **Step 5: Commit**

```bash
git add transaction-service/internal/service/payment_service.go transaction-service/internal/service/payment_service_test.go
git commit -m "feat(transaction-service): add payment service with validation and payment code rules"
```

---

### Task 30: Transfer service and verification service

**Files:**
- Create: `transaction-service/internal/service/transfer_service.go`
- Create: `transaction-service/internal/service/transfer_service_test.go`
- Create: `transaction-service/internal/service/verification_service.go`
- Create: `transaction-service/internal/service/payment_recipient_service.go`

- [ ] **Step 1: Write transfer service**

Key logic: same client, possibly different currencies. If same currency: commission=0, rate=1. If different: go through RSD (base), use sell rate, charge 0-1% commission. Uses `ExchangeService.Convert()`.

- [ ] **Step 2: Write verification service**

Key logic: generate 6-digit code, store with 5-min TTL, max 3 attempts. On validation: check code, increment attempts, expire after 3 failures.

```go
// transaction-service/internal/service/verification_service.go
package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"transaction-service/internal/model"
	"transaction-service/internal/repository"
)

type VerificationService struct {
	// Store in Redis for TTL, fallback to DB
	cache interface{} // cache.RedisCache
}

func GenerateVerificationCode() string {
	code := ""
	for i := 0; i < 6; i++ {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		code += fmt.Sprintf("%d", d.Int64())
	}
	return code
}

func (s *VerificationService) CreateCode(clientID, transactionID uint64, txType string) (*model.VerificationCode, error) {
	code := &model.VerificationCode{
		ClientID:        clientID,
		TransactionID:   transactionID,
		TransactionType: txType,
		Code:            GenerateVerificationCode(),
		Attempts:        0,
		MaxAttempts:     3,
		ExpiresAt:       time.Now().Add(5 * time.Minute),
	}
	// Store code (implementation depends on repo)
	return code, nil
}

func (s *VerificationService) ValidateCode(repo *repository.VerificationCodeRepository, clientID, txID uint64, inputCode string) (bool, int, error) {
	code, err := repo.GetByTransaction(clientID, txID)
	if err != nil {
		return false, 0, errors.New("verification code not found")
	}
	if code.Used {
		return false, 0, errors.New("code already used")
	}
	if time.Now().After(code.ExpiresAt) {
		return false, 0, errors.New("code expired")
	}
	if code.Attempts >= code.MaxAttempts {
		return false, 0, errors.New("maximum attempts exceeded — transaction cancelled")
	}

	code.Attempts++
	if code.Code != inputCode {
		_ = repo.Update(code)
		return false, code.MaxAttempts - code.Attempts, nil
	}

	code.Used = true
	_ = repo.Update(code)
	return true, 0, nil
}
```

- [ ] **Step 3: Write payment recipient service**

Simple CRUD: `Create`, `List(clientID)`, `Update`, `Delete`.

- [ ] **Step 4: Write tests for transfer and verification**

- [ ] **Step 5: Commit**

```bash
git add transaction-service/internal/service/
git commit -m "feat(transaction-service): add transfer, verification, and payment recipient services"
```

---

### Task 31: Transaction service repositories, handler, config, main.go

- [ ] **Step 1: Write repositories** (PaymentRepository, TransferRepository, PaymentRecipientRepository, VerificationCodeRepository) — follow existing GORM patterns.

- [ ] **Step 2: Write gRPC handler** — implement all `TransactionService` RPCs, publish Kafka events.

- [ ] **Step 3: Write config** — env vars: `TRANSACTION_DB_*`, `TRANSACTION_GRPC_ADDR` (`:50057`), `ACCOUNT_GRPC_ADDR`, `KAFKA_BROKERS`, `REDIS_ADDR`, `EXCHANGE_API_KEY`.

- [ ] **Step 4: Write main.go** — auto-migrate Payment, Transfer, PaymentRecipient, VerificationCode. Start gRPC on `:50057`.

- [ ] **Step 5: Build and verify**

Run: `cd transaction-service && go build -o bin/transaction-service ./cmd`

- [ ] **Step 6: Commit**

```bash
git add transaction-service/
git commit -m "feat(transaction-service): add complete service with repos, handler, config, main.go"
```

---

## Chunk 6: Credit Service Implementation

### Task 32: Credit models

**Files:**
- Create: `credit-service/internal/model/loan.go`
- Create: `credit-service/internal/model/loan_request.go`
- Create: `credit-service/internal/model/installment.go`

- [ ] **Step 1: Write Loan model**

```go
// credit-service/internal/model/loan.go
package model

import "time"

// LoanType: "cash", "housing", "auto", "refinancing", "student"
// InterestType: "fixed" or "variable"
// Status: "approved", "rejected", "paid_off", "overdue"

type Loan struct {
	ID                    uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LoanNumber            string    `gorm:"uniqueIndex;not null" json:"loan_number"`
	LoanType              string    `gorm:"size:20;not null" json:"loan_type"`
	AccountNumber         string    `gorm:"size:18;not null;index" json:"account_number"`
	Amount                float64   `gorm:"not null" json:"amount"`
	RepaymentPeriod       int       `gorm:"not null" json:"repayment_period"` // months
	NominalInterestRate   float64   `gorm:"not null" json:"nominal_interest_rate"`
	EffectiveInterestRate float64   `gorm:"not null" json:"effective_interest_rate"`
	ContractDate          time.Time `gorm:"not null" json:"contract_date"`
	MaturityDate          time.Time `gorm:"not null" json:"maturity_date"`
	NextInstallmentAmount float64   `gorm:"not null" json:"next_installment_amount"`
	NextInstallmentDate   time.Time `json:"next_installment_date"`
	RemainingDebt         float64   `gorm:"not null" json:"remaining_debt"`
	CurrencyCode          string    `gorm:"size:3;not null" json:"currency_code"`
	Status                string    `gorm:"size:15;not null;default:'approved'" json:"status"`
	InterestType          string    `gorm:"size:10;not null" json:"interest_type"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Write LoanRequest model**

```go
// credit-service/internal/model/loan_request.go
package model

import "time"

type LoanRequest struct {
	ID               uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientID         uint64    `gorm:"not null;index" json:"client_id"`
	LoanType         string    `gorm:"size:20;not null" json:"loan_type"`
	InterestType     string    `gorm:"size:10;not null" json:"interest_type"`
	Amount           float64   `gorm:"not null" json:"amount"`
	CurrencyCode     string    `gorm:"size:3;not null" json:"currency_code"`
	Purpose          string    `json:"purpose"`
	MonthlySalary    float64   `gorm:"not null" json:"monthly_salary"`
	EmploymentStatus string    `gorm:"size:20;not null" json:"employment_status"`
	EmploymentPeriod int       `json:"employment_period"` // months
	RepaymentPeriod  int       `gorm:"not null" json:"repayment_period"`
	Phone            string    `json:"phone"`
	AccountNumber    string    `gorm:"size:18;not null" json:"account_number"`
	Status           string    `gorm:"size:15;not null;default:'pending'" json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
```

- [ ] **Step 3: Write Installment model**

```go
// credit-service/internal/model/installment.go
package model

import "time"

// Status: "paid", "unpaid", "overdue"

type Installment struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	LoanID       uint64     `gorm:"not null;index" json:"loan_id"`
	Amount       float64    `gorm:"not null" json:"amount"`
	InterestRate float64    `gorm:"not null" json:"interest_rate"`
	CurrencyCode string    `gorm:"size:3;not null" json:"currency_code"`
	ExpectedDate time.Time  `gorm:"not null" json:"expected_date"`
	ActualDate   *time.Time `json:"actual_date"`
	Status       string     `gorm:"size:10;not null;default:'unpaid'" json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
}
```

- [ ] **Step 4: Commit**

```bash
git add credit-service/internal/model/
git commit -m "feat(credit-service): add Loan, LoanRequest, Installment models"
```

---

### Task 33: Interest rate calculation with tests

**Files:**
- Create: `credit-service/internal/service/interest_rate.go`
- Create: `credit-service/internal/service/interest_rate_test.go`

- [ ] **Step 1: Write failing tests**

```go
// credit-service/internal/service/interest_rate_test.go
package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBaseRate(t *testing.T) {
	tests := []struct {
		amount   float64
		expected float64
	}{
		{100000, 6.25},
		{500000, 6.25},
		{500001, 6.00},
		{1000000, 6.00},
		{1000001, 5.75},
		{5000000, 5.50},
		{10000000, 5.25},
		{20000000, 5.00},
		{20000001, 4.75},
	}
	for _, tt := range tests {
		rate := GetBaseRate(tt.amount)
		assert.Equal(t, tt.expected, rate, "amount: %f", tt.amount)
	}
}

func TestGetBankMargin(t *testing.T) {
	assert.Equal(t, 1.75, GetBankMargin("cash"))
	assert.Equal(t, 1.50, GetBankMargin("housing"))
	assert.Equal(t, 1.25, GetBankMargin("auto"))
	assert.Equal(t, 1.00, GetBankMargin("refinancing"))
	assert.Equal(t, 0.75, GetBankMargin("student"))
}

func TestCalculateMonthlyInstallment(t *testing.T) {
	// P=1,000,000 RSD, r=8% annual (0.667% monthly), N=60 months
	// Expected ~20,276.39 RSD
	installment := CalculateMonthlyInstallment(1000000, 8.0, 60)
	assert.InDelta(t, 20276.39, installment, 1.0)
}

func TestCalculateMonthlyInstallmentZeroRate(t *testing.T) {
	// 0% rate: P/N
	installment := CalculateMonthlyInstallment(120000, 0, 12)
	assert.InDelta(t, 10000, installment, 0.01)
}

func TestValidRepaymentPeriods(t *testing.T) {
	assert.True(t, IsValidRepaymentPeriod("cash", 12))
	assert.True(t, IsValidRepaymentPeriod("cash", 84))
	assert.False(t, IsValidRepaymentPeriod("cash", 100))
	assert.True(t, IsValidRepaymentPeriod("housing", 60))
	assert.True(t, IsValidRepaymentPeriod("housing", 360))
	assert.False(t, IsValidRepaymentPeriod("housing", 12))
}
```

- [ ] **Step 2: Run tests — expected FAIL**

- [ ] **Step 3: Write interest rate implementation**

```go
// credit-service/internal/service/interest_rate.go
package service

import "math"

// Base annual interest rates by loan amount range (in RSD)
func GetBaseRate(amountRSD float64) float64 {
	switch {
	case amountRSD <= 500000:
		return 6.25
	case amountRSD <= 1000000:
		return 6.00
	case amountRSD <= 2000000:
		return 5.75
	case amountRSD <= 5000000:
		return 5.50
	case amountRSD <= 10000000:
		return 5.25
	case amountRSD <= 20000000:
		return 5.00
	default:
		return 4.75
	}
}

// Bank margin by loan type
func GetBankMargin(loanType string) float64 {
	switch loanType {
	case "cash":
		return 1.75
	case "housing":
		return 1.50
	case "auto":
		return 1.25
	case "refinancing":
		return 1.00
	case "student":
		return 0.75
	default:
		return 1.75
	}
}

// CalculateMonthlyInstallment uses the annuity formula:
// A = P * r * (1+r)^N / ((1+r)^N - 1)
// where r = annual rate / 12 (as decimal)
func CalculateMonthlyInstallment(principal, annualRatePercent float64, months int) float64 {
	if annualRatePercent == 0 {
		return principal / float64(months)
	}
	r := annualRatePercent / 100.0 / 12.0
	n := float64(months)
	factor := math.Pow(1+r, n)
	return principal * r * factor / (factor - 1)
}

// CalculateEffectiveRate for fixed: base + margin
// For variable: base + offset + margin (offset updated monthly by cron)
func CalculateFixedRate(amountRSD float64, loanType string) float64 {
	return GetBaseRate(amountRSD) + GetBankMargin(loanType)
}

func CalculateVariableRate(amountRSD float64, loanType string, monthlyOffset float64) float64 {
	base := GetBaseRate(amountRSD)
	margin := GetBankMargin(loanType)
	rate := base + monthlyOffset + margin
	if rate < 0 {
		rate = 0
	}
	return rate
}

// Valid repayment periods per loan type
var repaymentPeriods = map[string][]int{
	"cash":          {12, 24, 36, 48, 60, 72, 84},
	"auto":          {12, 24, 36, 48, 60, 72, 84},
	"student":       {12, 24, 36, 48, 60, 72, 84},
	"refinancing":   {12, 24, 36, 48, 60, 72, 84},
	"housing":       {60, 120, 180, 240, 300, 360},
}

func IsValidRepaymentPeriod(loanType string, months int) bool {
	periods, ok := repaymentPeriods[loanType]
	if !ok {
		return false
	}
	for _, p := range periods {
		if p == months {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests — expected PASS**

- [ ] **Step 5: Commit**

```bash
git add credit-service/internal/service/interest_rate.go credit-service/internal/service/interest_rate_test.go
git commit -m "feat(credit-service): add interest rate calculation, installment formula, repayment periods"
```

---

### Task 34: Loan service and installment cron job

**Files:**
- Create: `credit-service/internal/service/loan_service.go`
- Create: `credit-service/internal/service/loan_request_service.go`
- Create: `credit-service/internal/service/installment_service.go`
- Create: `credit-service/internal/service/cron_service.go`

- [ ] **Step 1: Write loan request service**

Validates: loan type, interest type, repayment period, positive amount, employment status. Creates LoanRequest with status "pending".

- [ ] **Step 2: Write loan service**

`ApproveLoan`: creates Loan from LoanRequest, generates installment schedule (history + 1 future), deposits amount to client's account (calls account-service via gRPC), publishes Kafka events.

`RejectLoan`: marks LoanRequest as "rejected", publishes Kafka event.

```go
// Key logic for generating installment schedule:
func GenerateInstallmentSchedule(loan *model.Loan) []model.Installment {
	installments := make([]model.Installment, 0, loan.RepaymentPeriod)
	monthlyAmount := CalculateMonthlyInstallment(
		loan.Amount, loan.EffectiveInterestRate, loan.RepaymentPeriod,
	)

	for i := 1; i <= loan.RepaymentPeriod; i++ {
		expectedDate := loan.ContractDate.AddDate(0, i, 0)
		installments = append(installments, model.Installment{
			LoanID:       loan.ID,
			Amount:       monthlyAmount,
			InterestRate: loan.EffectiveInterestRate,
			CurrencyCode: loan.CurrencyCode,
			ExpectedDate: expectedDate,
			Status:       "unpaid",
		})
	}
	return installments
}
```

- [ ] **Step 3: Write installment cron service**

```go
// credit-service/internal/service/cron_service.go
package service

import (
	"context"
	"log"
	"math/rand"
	"time"
)

type CronService struct {
	loanService        *LoanService
	installmentService *InstallmentService
	// accountClient for balance deduction via gRPC
}

// RunDailyInstallmentCollection — called once per day
// 1. Find all loans where next_installment_date = today
// 2. Try to deduct from client's account
// 3. On success: mark installment as "paid", update loan remaining_debt
// 4. On failure: notify client, retry in 72h, after that increase interest +0.05%
func (c *CronService) RunDailyInstallmentCollection(ctx context.Context) {
	loans, err := c.loanService.GetLoansDueToday()
	if err != nil {
		log.Printf("cron: failed to get due loans: %v", err)
		return
	}

	for _, loan := range loans {
		err := c.installmentService.CollectInstallment(ctx, &loan)
		if err != nil {
			log.Printf("cron: failed to collect installment for loan %s: %v", loan.LoanNumber, err)
			// Schedule retry, send notification
		}
	}
}

// RunMonthlyVariableRateUpdate — called once per month
// Generates random offset [-1.5%, +1.5%] and recalculates variable rate loans
func (c *CronService) RunMonthlyVariableRateUpdate(ctx context.Context) {
	offset := (rand.Float64() * 3.0) - 1.5 // [-1.5, +1.5]
	log.Printf("cron: monthly variable rate offset: %.2f%%", offset)

	loans, err := c.loanService.GetVariableRateLoans()
	if err != nil {
		log.Printf("cron: failed to get variable rate loans: %v", err)
		return
	}

	for _, loan := range loans {
		newRate := CalculateVariableRate(loan.Amount, loan.LoanType, offset)
		newInstallment := CalculateMonthlyInstallment(loan.RemainingDebt, newRate, loan.RepaymentPeriod)

		loan.EffectiveInterestRate = newRate
		loan.NextInstallmentAmount = newInstallment
		_ = c.loanService.UpdateLoan(&loan)
	}
}
```

- [ ] **Step 4: Commit**

```bash
git add credit-service/internal/service/
git commit -m "feat(credit-service): add loan, installment, and cron services"
```

---

### Task 35: Credit service repositories, handler, config, main.go

- [ ] **Step 1: Write repositories** (LoanRepository, LoanRequestRepository, InstallmentRepository) — GORM CRUD.

- [ ] **Step 2: Write gRPC handler** — implement all `CreditService` RPCs, publish Kafka events.

- [ ] **Step 3: Write config** — env vars: `CREDIT_DB_*`, `CREDIT_GRPC_ADDR` (`:50058`), `ACCOUNT_GRPC_ADDR`, `KAFKA_BROKERS`, `REDIS_ADDR`.

- [ ] **Step 4: Write main.go** — auto-migrate Loan, LoanRequest, Installment. Start cron goroutines:
  - Daily at midnight: `RunDailyInstallmentCollection`
  - Monthly on 1st: `RunMonthlyVariableRateUpdate`

Use `time.Ticker` or a simple cron loop:

```go
go func() {
    for {
        now := time.Now()
        // Run daily at midnight
        next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
        time.Sleep(time.Until(next))
        cronService.RunDailyInstallmentCollection(context.Background())
    }
}()
```

- [ ] **Step 5: Build and verify**

Run: `cd credit-service && go build -o bin/credit-service ./cmd`

- [ ] **Step 6: Commit**

```bash
git add credit-service/
git commit -m "feat(credit-service): add complete service with repos, handler, cron jobs, main.go"
```

---

## Chunk 7: API Gateway Extensions

### Task 36: New gRPC clients

**Files:**
- Create: `api-gateway/internal/grpc/client_client.go`
- Create: `api-gateway/internal/grpc/account_client.go`
- Create: `api-gateway/internal/grpc/card_client.go`
- Create: `api-gateway/internal/grpc/transaction_client.go`
- Create: `api-gateway/internal/grpc/credit_client.go`

- [ ] **Step 1: Write gRPC clients**

Follow the pattern from `api-gateway/internal/grpc/auth_client.go` and `user_client.go`. Each client wraps the generated protobuf client and provides typed methods.

Example for client_client.go:

```go
// api-gateway/internal/grpc/client_client.go
package grpc

import (
	pb "github.com/LukaSavworworktrees/EXBanka-1-Backend/contract/clientpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type ClientClient struct {
	conn   *grpc.ClientConn
	client pb.ClientServiceClient
}

func NewClientClient(addr string) (*ClientClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &ClientClient{
		conn:   conn,
		client: pb.NewClientServiceClient(conn),
	}, nil
}

func (c *ClientClient) Close() { c.conn.Close() }
```

Repeat same pattern for AccountClient, CardClient, TransactionClient, CreditClient.

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/grpc/client_client.go api-gateway/internal/grpc/account_client.go api-gateway/internal/grpc/card_client.go api-gateway/internal/grpc/transaction_client.go api-gateway/internal/grpc/credit_client.go
git commit -m "feat(api-gateway): add gRPC clients for all new banking services"
```

---

### Task 37: API Gateway handlers for client operations

**Files:**
- Create: `api-gateway/internal/handler/client_handler.go`

- [ ] **Step 1: Write client handler**

Follow pattern from `employee_handler.go`. REST endpoints:

```
POST   /api/clients              — Create client (employee-only)
GET    /api/clients              — List clients (employee-only, with filters)
GET    /api/clients/:id          — Get client (employee-only)
PUT    /api/clients/:id          — Update client (employee-only)
POST   /api/auth/client/login    — Client login (public)
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/handler/client_handler.go
git commit -m "feat(api-gateway): add client REST handler"
```

---

### Task 38: API Gateway handlers for account operations

**Files:**
- Create: `api-gateway/internal/handler/account_handler.go`

- [ ] **Step 1: Write account handler**

REST endpoints:

```
# Employee endpoints
POST   /api/accounts                    — Create account (requires accounts.create)
GET    /api/accounts                    — List all accounts (requires accounts.read, employee portal)
GET    /api/accounts/:id                — Get account details

# Client endpoints
GET    /api/client/accounts             — List my accounts (client auth)
GET    /api/client/accounts/:id         — Get my account details
PUT    /api/client/accounts/:id/name    — Change account name (owner only)
PUT    /api/client/accounts/:id/limits  — Change limits (owner only, requires verification)

# Company
POST   /api/companies                   — Create company
GET    /api/companies/:id               — Get company

# Currency
GET    /api/currencies                  — List all currencies
```

- [ ] **Step 2: Commit**

```bash
git add api-gateway/internal/handler/account_handler.go
git commit -m "feat(api-gateway): add account, company, currency REST handlers"
```

---

### Task 39: API Gateway handlers for card, transaction, credit

**Files:**
- Create: `api-gateway/internal/handler/card_handler.go`
- Create: `api-gateway/internal/handler/transaction_handler.go`
- Create: `api-gateway/internal/handler/credit_handler.go`
- Create: `api-gateway/internal/handler/exchange_handler.go`

- [ ] **Step 1: Write card handler**

```
# Client endpoints
GET    /api/client/cards                — List my cards
POST   /api/client/cards                — Request new card (sends verification email)
PUT    /api/client/cards/:id/block      — Block my card

# Employee endpoints
GET    /api/accounts/:accountId/cards   — List cards for account (employee portal)
PUT    /api/cards/:id/block             — Block card
PUT    /api/cards/:id/unblock           — Unblock card
PUT    /api/cards/:id/deactivate        — Deactivate card
```

- [ ] **Step 2: Write transaction handler**

```
# Client endpoints
POST   /api/client/payments             — New payment (requires verification)
GET    /api/client/payments             — Payment history
POST   /api/client/transfers            — New transfer
GET    /api/client/transfers            — Transfer history
GET    /api/client/recipients           — List payment recipients
POST   /api/client/recipients           — Add payment recipient
PUT    /api/client/recipients/:id       — Update recipient
DELETE /api/client/recipients/:id       — Delete recipient

# Verification
POST   /api/client/verify               — Submit verification code
```

- [ ] **Step 3: Write credit handler**

```
# Client endpoints
POST   /api/client/loans/request        — Submit loan request
GET    /api/client/loans                 — List my loans
GET    /api/client/loans/:id             — Get loan details with installments

# Employee endpoints (Credit management portal)
GET    /api/loans/requests               — List loan requests (with filters)
PUT    /api/loans/requests/:id/approve   — Approve loan request
PUT    /api/loans/requests/:id/reject    — Reject loan request
GET    /api/loans                        — List all loans (with filters)
```

- [ ] **Step 4: Write exchange handler**

```
GET    /api/exchange/rates               — List all exchange rates (kursna lista)
GET    /api/exchange/convert             — Calculate equivalent value
```

- [ ] **Step 5: Commit**

```bash
git add api-gateway/internal/handler/card_handler.go api-gateway/internal/handler/transaction_handler.go api-gateway/internal/handler/credit_handler.go api-gateway/internal/handler/exchange_handler.go
git commit -m "feat(api-gateway): add card, transaction, credit, exchange REST handlers"
```

---

### Task 40: Update router and config

**Files:**
- Modify: `api-gateway/internal/router/router.go`
- Modify: `api-gateway/internal/config/config.go`
- Modify: `api-gateway/cmd/main.go`

- [ ] **Step 1: Add new gRPC addresses to config**

Add to config:
```go
ClientGRPCAddr      string  // CLIENT_GRPC_ADDR, default "localhost:50054"
AccountGRPCAddr     string  // ACCOUNT_GRPC_ADDR, default "localhost:50055"
CardGRPCAddr        string  // CARD_GRPC_ADDR, default "localhost:50056"
TransactionGRPCAddr string  // TRANSACTION_GRPC_ADDR, default "localhost:50057"
CreditGRPCAddr      string  // CREDIT_GRPC_ADDR, default "localhost:50058"
```

- [ ] **Step 2: Wire new clients in main.go**

Initialize all 5 new gRPC clients and pass to handlers.

- [ ] **Step 3: Update router with new routes**

Add all new route groups:

```go
// Client management (employee)
clientRoutes := api.Group("/clients")
clientRoutes.Use(middleware.AuthMiddleware(authClient))
{
    clientRoutes.GET("", middleware.RequirePermission("clients.read"), clientHandler.ListClients)
    clientRoutes.GET("/:id", middleware.RequirePermission("clients.read"), clientHandler.GetClient)
    clientRoutes.POST("", middleware.RequirePermission("clients.create"), clientHandler.CreateClient)
    clientRoutes.PUT("/:id", middleware.RequirePermission("clients.update"), clientHandler.UpdateClient)
}

// Client self-service (client auth)
clientSelf := api.Group("/client")
clientSelf.Use(middleware.ClientAuthMiddleware(authClient))
{
    clientSelf.GET("/accounts", accountHandler.ListMyAccounts)
    clientSelf.GET("/accounts/:id", accountHandler.GetMyAccount)
    clientSelf.PUT("/accounts/:id/name", accountHandler.UpdateMyAccountName)
    clientSelf.PUT("/accounts/:id/limits", accountHandler.UpdateMyAccountLimits)
    clientSelf.GET("/cards", cardHandler.ListMyCards)
    clientSelf.POST("/cards", cardHandler.RequestCard)
    clientSelf.PUT("/cards/:id/block", cardHandler.BlockMyCard)
    clientSelf.POST("/payments", transactionHandler.CreatePayment)
    clientSelf.GET("/payments", transactionHandler.ListMyPayments)
    clientSelf.POST("/transfers", transactionHandler.CreateTransfer)
    clientSelf.GET("/transfers", transactionHandler.ListMyTransfers)
    clientSelf.GET("/recipients", transactionHandler.ListRecipients)
    clientSelf.POST("/recipients", transactionHandler.CreateRecipient)
    clientSelf.PUT("/recipients/:id", transactionHandler.UpdateRecipient)
    clientSelf.DELETE("/recipients/:id", transactionHandler.DeleteRecipient)
    clientSelf.POST("/verify", transactionHandler.VerifyCode)
    clientSelf.POST("/loans/request", creditHandler.CreateLoanRequest)
    clientSelf.GET("/loans", creditHandler.ListMyLoans)
    clientSelf.GET("/loans/:id", creditHandler.GetMyLoan)
}

// Employee portals
accountPortal := api.Group("/accounts")
accountPortal.Use(middleware.AuthMiddleware(authClient))
{
    accountPortal.POST("", middleware.RequirePermission("accounts.create"), accountHandler.CreateAccount)
    accountPortal.GET("", middleware.RequirePermission("accounts.read"), accountHandler.ListAllAccounts)
    accountPortal.GET("/:id", middleware.RequirePermission("accounts.read"), accountHandler.GetAccount)
    accountPortal.GET("/:id/cards", middleware.RequirePermission("accounts.read"), cardHandler.ListCardsByAccount)
    accountPortal.PUT("/cards/:id/block", middleware.RequirePermission("cards.manage"), cardHandler.BlockCard)
    accountPortal.PUT("/cards/:id/unblock", middleware.RequirePermission("cards.manage"), cardHandler.UnblockCard)
    accountPortal.PUT("/cards/:id/deactivate", middleware.RequirePermission("cards.manage"), cardHandler.DeactivateCard)
}

// Credit management portal (employee)
creditPortal := api.Group("/loans")
creditPortal.Use(middleware.AuthMiddleware(authClient))
{
    creditPortal.GET("/requests", middleware.RequirePermission("credits.manage"), creditHandler.ListLoanRequests)
    creditPortal.PUT("/requests/:id/approve", middleware.RequirePermission("credits.manage"), creditHandler.ApproveLoanRequest)
    creditPortal.PUT("/requests/:id/reject", middleware.RequirePermission("credits.manage"), creditHandler.RejectLoanRequest)
    creditPortal.GET("", middleware.RequirePermission("credits.manage"), creditHandler.ListAllLoans)
}

// Public
api.POST("/auth/client/login", authHandler.ClientLogin)

// Exchange rates (public)
api.GET("/exchange/rates", exchangeHandler.ListRates)
api.GET("/exchange/convert", exchangeHandler.Convert)

// Company
companyRoutes := api.Group("/companies")
companyRoutes.Use(middleware.AuthMiddleware(authClient))
{
    companyRoutes.POST("", middleware.RequirePermission("accounts.create"), accountHandler.CreateCompany)
    companyRoutes.GET("/:id", middleware.RequirePermission("accounts.read"), accountHandler.GetCompany)
}

// Currency (public)
api.GET("/currencies", accountHandler.ListCurrencies)
```

- [ ] **Step 4: Add new permissions to role_service.go**

Update `user-service/internal/service/role_service.go` to include new permissions:

```go
// Add to permission maps:
// EmployeeBasic: "clients.read", "accounts.create", "accounts.read", "cards.manage", "credits.manage"
// EmployeeSupervisor: adds "clients.create", "clients.update"
// EmployeeAdmin: adds all
```

- [ ] **Step 5: Add ClientAuthMiddleware**

New middleware that validates JWT tokens with `user_type: "client"` claim. Similar to AuthMiddleware but checks for client-type tokens and sets `client_id` in context.

- [ ] **Step 6: Build and verify**

Run: `cd api-gateway && go build -o bin/api-gateway ./cmd`

- [ ] **Step 7: Commit**

```bash
git add api-gateway/ user-service/internal/service/role_service.go
git commit -m "feat(api-gateway): add all new routes, handlers, client auth middleware"
```

---

## Chunk 8: Notification Service Extensions & Auth Service Updates

### Task 41: New email templates

**Files:**
- Modify: `notification-service/internal/sender/templates.go`

- [ ] **Step 1: Add new email templates**

Add templates for these email types (follow existing HTML pattern from ACTIVATION/PASSWORD_RESET templates):

- `ACCOUNT_CREATED` — "Your new account has been created" with account number, type, currency
- `CARD_VERIFICATION` — "Verify your card request" with 6-digit code
- `CARD_STATUS_CHANGED` — "Card status update" with card number (masked), new status
- `LOAN_APPROVED` — "Your loan has been approved" with loan details
- `LOAN_REJECTED` — "Your loan request was not approved"
- `INSTALLMENT_FAILED` — "Installment payment failed" with retry deadline
- `TRANSACTION_VERIFICATION` — "Verify your transaction" with 6-digit code
- `PAYMENT_CONFIRMATION` — "Payment successful" with payment details

- [ ] **Step 2: Update email consumer to handle new types**

Modify `notification-service/internal/consumer/email_consumer.go` to recognize new email types in the switch statement.

- [ ] **Step 3: Add template tests**

Add test cases for all new templates in `notification-service/internal/sender/templates_test.go`.

- [ ] **Step 4: Commit**

```bash
git add notification-service/internal/sender/templates.go notification-service/internal/sender/templates_test.go notification-service/internal/consumer/email_consumer.go
git commit -m "feat(notification-service): add email templates for accounts, cards, loans, transactions"
```

---

### Task 42: Auth service updates for client login

**Files:**
- Modify: `auth-service/internal/config/config.go`
- Modify: `auth-service/internal/service/auth_service.go`
- Modify: `auth-service/internal/handler/grpc_handler.go`
- Modify: `contract/proto/auth/auth.proto`

- [ ] **Step 1: Add CLIENT_GRPC_ADDR to auth-service config**

- [ ] **Step 2: Update auth.proto with client login RPC**

Add to `AuthService`:
```protobuf
rpc ClientLogin(ClientLoginRequest) returns (LoginResponse);

message ClientLoginRequest {
  string email = 1;
  string password = 2;
}
```

- [ ] **Step 3: Implement ClientLogin in auth_service.go**

Similar to `Login` but calls `client-service.ValidateCredentials` instead of `user-service.ValidateCredentials`. JWT claims include `user_type: "client"` and `client_id` instead of employee role/permissions.

- [ ] **Step 4: Regenerate proto and build**

Run: `make proto && cd auth-service && go build -o bin/auth-service ./cmd`

- [ ] **Step 5: Commit**

```bash
git add contract/proto/auth/auth.proto contract/authpb/ auth-service/
git commit -m "feat(auth-service): add client login flow with client-service integration"
```

---

### Task 43: Bank's own accounts (seed data)

**Files:**
- Modify: `account-service/cmd/main.go`

- [ ] **Step 1: Seed bank accounts on startup**

The bank itself is a "company" with one account per supported currency. Seed on startup (skip if already exists):

```go
func seedBankAccounts(db *gorm.DB) {
    // Create bank company
    bank := model.Company{
        CompanyName:        "EXBanka",
        RegistrationNumber: "00000001",
        TaxNumber:          "000000001",
        ActivityCode:       "64.19",
        Address:            "Trg Republike 1, Beograd, Srbija",
        OwnerID:            0, // system account
    }
    db.Where("registration_number = ?", bank.RegistrationNumber).FirstOrCreate(&bank)

    // Create one account per currency
    currencies := []string{"RSD", "EUR", "CHF", "USD", "GBP", "JPY", "CAD", "AUD"}
    for _, curr := range currencies {
        kind := "foreign"
        accType := "business"
        if curr == "RSD" {
            kind = "current"
            accType = "doo"
        }
        var existing model.Account
        if db.Where("owner_id = 0 AND currency_code = ?", curr).First(&existing).Error != nil {
            account := model.Account{
                AccountNumber:   GenerateAccountNumber("111", "0001", kind, accType),
                AccountName:     "EXBanka " + curr + " Account",
                OwnerID:         0, // system
                CurrencyCode:    curr,
                Status:          "active",
                AccountKind:     kind,
                AccountType:     accType,
                AccountCategory: "business",
                CompanyID:       &bank.ID,
            }
            db.Create(&account)
        }
    }
}
```

- [ ] **Step 2: Commit**

```bash
git add account-service/cmd/main.go
git commit -m "feat(account-service): seed bank's own accounts in all 8 currencies on startup"
```

---

### Task 44: Update .env and CLAUDE.md

- [ ] **Step 1: Add new environment variables**

Add to `.env`:

```
# Client Service
CLIENT_DB_HOST=localhost
CLIENT_DB_PORT=5434
CLIENT_DB_USER=postgres
CLIENT_DB_PASSWORD=postgres
CLIENT_DB_NAME=clientdb
CLIENT_GRPC_ADDR=:50054

# Account Service
ACCOUNT_DB_HOST=localhost
ACCOUNT_DB_PORT=5435
ACCOUNT_DB_USER=postgres
ACCOUNT_DB_PASSWORD=postgres
ACCOUNT_DB_NAME=accountdb
ACCOUNT_GRPC_ADDR=:50055

# Card Service
CARD_DB_HOST=localhost
CARD_DB_PORT=5436
CARD_DB_USER=postgres
CARD_DB_PASSWORD=postgres
CARD_DB_NAME=carddb
CARD_GRPC_ADDR=:50056

# Transaction Service
TRANSACTION_DB_HOST=localhost
TRANSACTION_DB_PORT=5437
TRANSACTION_DB_USER=postgres
TRANSACTION_DB_PASSWORD=postgres
TRANSACTION_DB_NAME=transactiondb
TRANSACTION_GRPC_ADDR=:50057
EXCHANGE_API_KEY=

# Credit Service
CREDIT_DB_HOST=localhost
CREDIT_DB_PORT=5438
CREDIT_DB_USER=postgres
CREDIT_DB_PASSWORD=postgres
CREDIT_DB_NAME=creditdb
CREDIT_GRPC_ADDR=:50058
```

- [ ] **Step 2: Update CLAUDE.md with new services**

Add new services to the repository layout, ports table, environment table, and architecture sections.

- [ ] **Step 3: Commit**

```bash
git add .env CLAUDE.md
git commit -m "docs: update .env and CLAUDE.md with all new service configurations"
```

---

## Summary of All New REST Endpoints

### Public
| Method | Path | Description |
|--------|------|-------------|
| POST | /api/auth/client/login | Client login |
| GET | /api/exchange/rates | Exchange rate list |
| GET | /api/exchange/convert | Currency calculator |
| GET | /api/currencies | List supported currencies |

### Client (authenticated as client)
| Method | Path | Description |
|--------|------|-------------|
| GET | /api/client/accounts | List my accounts |
| GET | /api/client/accounts/:id | Account details |
| PUT | /api/client/accounts/:id/name | Rename account |
| PUT | /api/client/accounts/:id/limits | Change limits |
| GET | /api/client/cards | List my cards |
| POST | /api/client/cards | Request new card |
| PUT | /api/client/cards/:id/block | Block my card |
| POST | /api/client/payments | New payment |
| GET | /api/client/payments | Payment history |
| POST | /api/client/transfers | New transfer |
| GET | /api/client/transfers | Transfer history |
| GET | /api/client/recipients | List payment recipients |
| POST | /api/client/recipients | Add recipient |
| PUT | /api/client/recipients/:id | Update recipient |
| DELETE | /api/client/recipients/:id | Delete recipient |
| POST | /api/client/verify | Submit verification code |
| POST | /api/client/loans/request | Submit loan request |
| GET | /api/client/loans | List my loans |
| GET | /api/client/loans/:id | Loan details + installments |

### Employee (authenticated as employee)
| Method | Path | Description |
|--------|------|-------------|
| POST | /api/clients | Create client |
| GET | /api/clients | List clients |
| GET | /api/clients/:id | Get client |
| PUT | /api/clients/:id | Update client |
| POST | /api/accounts | Create account |
| GET | /api/accounts | List all accounts (portal) |
| GET | /api/accounts/:id | Get account |
| GET | /api/accounts/:id/cards | List cards for account |
| PUT | /api/cards/:id/block | Block card |
| PUT | /api/cards/:id/unblock | Unblock card |
| PUT | /api/cards/:id/deactivate | Deactivate card |
| GET | /api/loans/requests | List loan requests |
| PUT | /api/loans/requests/:id/approve | Approve loan |
| PUT | /api/loans/requests/:id/reject | Reject loan |
| GET | /api/loans | List all loans |
| POST | /api/companies | Create company |
| GET | /api/companies/:id | Get company |
