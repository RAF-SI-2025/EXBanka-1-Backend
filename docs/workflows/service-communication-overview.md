# Service Communication Overview

## Service Topology

```
Client/Browser
     │ HTTP/REST
     ▼
┌─────────────────────────────────────────┐
│             API Gateway :8080           │
│  (Gin router, JWT auth, input validation)│
└─────────────┬───────────────────────────┘
              │ gRPC (protobuf)
   ┌──────────┼──────────────────────────────────────────┐
   │          │                                          │
   ▼          ▼          ▼          ▼          ▼         ▼
auth-svc  user-svc  client-svc  account-svc  card-svc  transaction-svc
:50051    :50052    :50054      :50055       :50056    :50057
                                    ▲              ▲
                                    │              │
                               credit-svc      exchange-svc
                               :50058          :50059
```

## Communication Protocols

| From → To | Protocol | Format | Notes |
|-----------|----------|--------|-------|
| Client → API Gateway | HTTP/REST | JSON | JWT bearer token |
| API Gateway → any service | gRPC | Protobuf | TLS disabled (internal) |
| transaction-service → account-service | gRPC | Protobuf | UpdateBalance calls |
| transaction-service → exchange-service | gRPC | Protobuf | Convert RPC |
| credit-service → account-service | gRPC | Protobuf | UpdateBalance for installments |
| credit-service → client-service | gRPC | Protobuf | GetClient for emails |
| Any service → notification-service | Kafka | JSON | `notification.send-email` topic |
| notification-service → * | Kafka | JSON | `notification.email-sent` (delivery ack) |

## Coordination Patterns per Workflow

| Workflow | Pattern | Compensation | Recovery |
|----------|---------|-------------|---------|
| Payment | **Saga (orchestration)** via `executeWithSaga` | Automatic reverse on step failure | `StartCompensationRecovery` goroutine |
| Transfer (same-currency) | **Saga (orchestration)** 2-step | Automatic reverse | Same recovery goroutine |
| Transfer (cross-currency) | **Saga (orchestration)** 4-step | Automatic reverse | Same recovery goroutine |
| Installment collection | **Sequential with compensation** (cron) | Reverse debit+credit on mark-paid failure | Next cron cycle re-attempts |
| Exchange rate sync | **Atomic DB transaction** | DB rollback on any upsert failure | Next scheduled sync (6h) |
| Loan approval | **Synchronous + soft-fail** | Loan status `disbursement_failed` | Manual retry via re-approve API |
| Auth/Client/Card CRUD | **Synchronous** (single gRPC call) | None needed (single operation) | — |

## Kafka Topic Catalog

| Topic | Publisher | Consumer | Purpose |
|-------|-----------|----------|---------|
| `notification.send-email` | All services | notification-service | Trigger email delivery |
| `notification.email-sent` | notification-service | — | Delivery confirmation |
| `transaction.payment-created` | transaction-service | — | Audit/notification |
| `transaction.payment-completed` | transaction-service | — | Audit/notification |
| `transaction.payment-failed` | transaction-service | — | Alert |
| `transaction.transfer-created` | transaction-service | — | Audit |
| `transaction.transfer-completed` | transaction-service | — | Audit |
| `transaction.transfer-failed` | transaction-service | — | Alert |
| `transaction.saga-dead-letter` | transaction-service | Ops tooling | Manual intervention required |
| `credit.loan-approved` | credit-service | — | Audit |
| `credit.installment-collected` | credit-service | — | Audit |
| `credit.installment-failed` | credit-service | — | Client alert |
| `exchange.rates-updated` | exchange-service | — | Cache invalidation |
| (+ 20 more audit topics) | various | — | Audit trail |

## Persistence

| Service | Database | Notes |
|---------|----------|-------|
| auth-service | PostgreSQL :5432 (auth_db) | JWT tokens, activation tokens |
| user-service | PostgreSQL :5432 (user_db) | Employees, roles, limits |
| client-service | PostgreSQL :5434 | Clients, credentials, limits |
| account-service | PostgreSQL :5435 | Accounts, currencies, ledger |
| card-service | PostgreSQL :5436 | Cards, card blocks |
| transaction-service | PostgreSQL :5437 | Payments, transfers, saga log, fees |
| credit-service | PostgreSQL :5438 | Loan requests, loans, installments |
| exchange-service | PostgreSQL :5439 | Exchange rates |

Caching: Redis (auth-service + user-service; graceful degradation if unavailable).
