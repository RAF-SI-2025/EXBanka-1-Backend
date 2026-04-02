# Transfer Workflow

## Summary

A transfer moves money between accounts, potentially crossing currencies. Same-currency transfers use a 2-step saga; cross-currency transfers use a 4-step saga involving two bank intermediate accounts for currency conversion.

**Coordination pattern:** Saga (orchestration) via `executeWithSaga` in `transaction-service`.

**Services involved:** API Gateway → transaction-service → account-service, exchange-service

## State Machine

Identical structure to the payment state machine:

```
[pending_verification] → [processing] → [completed]
                                      ↘ [failed]
```

Idempotency guard: if already `completed`, `ExecuteTransfer` returns nil immediately. If not in `pending_verification`, returns error.

## Saga: Same-Currency Transfer (2 Steps)

```
ExecuteTransfer (from.currency == to.currency)
    │
    ├─ Step 1: debit_sender
    │   UpdateBalance(fromAccount, -(initialAmount + commission))
    │   Compensation: credit_sender
    │
    └─ Step 2: credit_recipient
        UpdateBalance(toAccount, +finalAmount)
        Compensation: debit_recipient
```

Commission is collected from the sender (totalDebit = initialAmount + commission). The fee is already included in step 1; there is no separate bank commission step for transfers (unlike payments).

## Saga: Cross-Currency Transfer (4 Steps)

```
ExecuteTransfer (from.currency != to.currency)
    │
    ├─ Step 1: debit_user_from
    │   UpdateBalance(fromAccount, -(initialAmount + commission))
    │   Compensation: credit_user_from
    │
    ├─ Step 2: credit_bank_from
    │   UpdateBalance(bankFromCurrencyAccount, +(initialAmount + commission))
    │   Compensation: debit_bank_from
    │
    ├─ Step 3: debit_bank_to
    │   UpdateBalance(bankToCurrencyAccount, -convertedAmount)
    │   Compensation: credit_bank_to
    │
    └─ Step 4: credit_user_to
        UpdateBalance(toAccount, +convertedAmount)
        Compensation: debit_user_to
```

**Currency conversion:** `exchange-service.Convert(fromCurrency, toCurrency, initialAmount)` is called before the saga to determine `convertedAmount`. This is a read-only gRPC call.

**Bank accounts:** `bankAccountClient.ListBankAccounts()` fetches all bank-owned accounts. The correct bank account per currency is resolved by `findBankAccountByCurrency()`.

## Recovery Loop

`StartCompensationRecovery` goroutine (5-minute ticker):

```
Every 5 minutes:
    SagaLogRepo.FindPendingCompensations()
    For each "compensating" entry:
        shared.Retry(UpdateBalance)
        If success: CompleteStep
        If failure: IncrementRetryCount
        If RetryCount >= 10: MarkDeadLetter + publish transaction.saga-dead-letter
```

## Saga Log Schema

Each step is recorded in the `saga_logs` table:

| Field | Purpose |
|-------|---------|
| `saga_id` | Unique ID for this saga run (`{type}-{txID}-{nanos}`) |
| `transaction_id` | FK to the transfer/payment ID |
| `step_number` | Positive for forward steps, negative for compensations |
| `step_name` | e.g. `debit_sender`, `credit_recipient_compensation` |
| `status` | `pending` → `completed` / `failed` / `compensating` → `dead_letter` |
| `is_compensation` | `true` for compensation entries |
| `amount` | Signed: negative = debit, positive = credit |
| `retry_count` | Incremented by recovery loop on each failure |

## Fee for Transfers

Same fee engine as payments (`transfer_fees` table). Transfer commission is embedded into step 1 debit (`totalDebit = initialAmount + commission`). No separate bank commission saga step — the bank receives its portion when it credits/debits its own accounts in steps 2 & 3.
