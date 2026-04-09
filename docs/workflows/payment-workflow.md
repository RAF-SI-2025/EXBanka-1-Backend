# Payment Workflow

## Summary

A payment is a two-party money transfer where the sender pays a service fee. The fee (commission) is deducted from the sender and credited to the bank's RSD account. All balance operations are saga-logged for durable compensation.

**Coordination pattern:** Saga (orchestration) via `executeWithSaga` in `transaction-service`.

**Services involved:** API Gateway → transaction-service → account-service

## State Machine

```
                 CreatePayment
                      │
              [pending_verification]
                      │
               ExecutePayment
               (OTP verified)
                      │
               ┌──────┴──────────────────────────────┐
               │ limit exceeded /                     │ limits OK
               │ account lookup fails                 ▼
               │                              [processing]
               │                      ┌────────────┴──────────┐
               │                      │ saga fails             │ saga succeeds
               │                      ▼                        ▼
               └─────────────────> [failed]            [completed]
                                  (terminal)            (terminal)
```

**State transitions (authoritative):**
| From | To | Trigger | Code location |
|------|----|---------|---------------|
| `pending_verification` | `processing` | `ExecutePayment` called | `payment_service.go:172` |
| `processing` | `failed` | Daily/monthly limit exceeded | `payment_service.go:201,208` |
| `processing` | `failed` | Saga step fails | `payment_service.go:248` |
| `processing` | `completed` | All saga steps succeed | `payment_service.go:270` |

## Saga Steps

```
ExecutePayment
    │
    ├─ Step 1: debit_sender
    │   UpdateBalance(fromAccount, -(initialAmount + commission))
    │   Compensation: credit_sender (reverse debit)
    │
    ├─ Step 2: credit_recipient
    │   UpdateBalance(toAccount, +initialAmount)
    │   Compensation: debit_recipient (reverse credit)
    │
    └─ Step 3: credit_bank_commission  [only if commission > 0]
        UpdateBalance(bankRSDAccount, +commission)
        Compensation: debit_bank (reverse credit)
```

**Compensation order on failure:** reverse N-1 → N-2 → ... → 0 (newest to oldest).

## Sequence Diagram

```
Client → API GW → transaction-svc → account-svc
  │          │           │               │
  │─ POST    │           │               │
  │ /payment─►           │               │
  │          │─ CreatePayment ──────────►│
  │          │◄─ payment(pending) ───────│
  │          │           │               │
  │◄─201─────│           │               │
  │          │           │               │
  │ (OTP via Kafka → email → user submits)
  │          │           │               │
  │─ POST    │           │               │
  │ /payment │           │               │
  │ /{id}/   │           │               │
  │ execute──►           │               │
  │          │─ ExecutePayment ──────────│
  │          │           │─ UpdateBalance(debit sender)─►│
  │          │           │◄─ ok ─────────────────────────│
  │          │           │─ UpdateBalance(credit recip)──►│
  │          │           │◄─ ok ─────────────────────────│
  │          │           │─ UpdateBalance(credit bank comm)►│
  │          │           │◄─ ok ─────────────────────────│
  │          │           │─ UpdateStatus("completed")     │
  │          │◄─ payment(completed) ─────│               │
  │◄─200─────│           │               │               │
```

## Compensation Example

If `credit_recipient` (step 2) fails:
```
Step 2 fails
    → compensate step 1: UpdateBalance(fromAccount, +(initialAmount + commission))
    → UpdateStatus("failed")
    → publish transaction.payment-failed
```

If `credit_bank_commission` (step 3) fails:
```
Step 3 fails
    → compensate step 2: UpdateBalance(toAccount, -initialAmount)
    → compensate step 1: UpdateBalance(fromAccount, +(initialAmount + commission))
    → UpdateStatus("failed")
    → publish transaction.payment-failed
```

## Fee Calculation

Fees are configured in the `transfer_fees` table (type: `percentage` or `fixed`). Multiple matching rules stack. Fee lookup failure **rejects** the payment (not silently ignored). Default seed: 0.1% for amounts ≥ 1000 RSD.

`FinalAmount = InitialAmount + Commission`
`Commission = sum of applicable fee rules applied to InitialAmount`

## Idempotency

`CreatePayment` is idempotent via `idempotency_key`. Re-submitting the same key returns the existing payment without creating a duplicate.

## Verification Code

After `CreatePayment`, a one-time verification code is sent to the client via Kafka → notification-service → email. `ExecutePayment` validates the code before proceeding. Maximum 3 attempts; after 3 failures the payment is cancelled.
