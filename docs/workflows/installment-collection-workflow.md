# Installment Collection Workflow

## Summary

A daily cron job (`CronService`) collects due loan installments by debiting the borrower's account and crediting the bank's RSD account. Each step has compensation to prevent financial inconsistency.

**Coordination pattern:** Sequential orchestration (cron-driven) with explicit step compensation.

**Services involved:** credit-service (cron) → account-service

## Trigger

`CronService.Start(ctx)` runs a 24-hour ticker. On each tick (and immediately at startup):
1. `MarkOverdueInstallments()` — bulk-update `status = "overdue"` for past-due unpaid installments
2. `GetDueInstallments()` — fetch all installments with `status = "unpaid"` and `expected_date ≤ now`
3. For each due installment: `processInstallment(...)`

## processInstallment Flow

```
processInstallment(installmentID, loanID, amount, ...)
    │
    ├─ GetLoan(loanID) — fetch account number
    │
    ├─ Step 1: Debit client account
    │   UpdateBalance(loan.AccountNumber, -amount)
    │   If fails: send failure email + publish installment-failed + apply late penalty → RETURN
    │
    ├─ Step 2: Credit bank RSD account
    │   UpdateBalance(bankRSDAccount, +amount)
    │   If fails: COMPENSATE Step 1 (credit client back) → RETURN (no mark-paid)
    │
    ├─ Step 3: Mark installment as paid
    │   MarkPaid(installmentID)
    │   If fails: COMPENSATE Step 2 (debit bank back) + COMPENSATE Step 1 (credit client back) → RETURN
    │
    └─ Publish credit.installment-collected event
```

## Compensation Matrix

| Step that fails | Step 1 compensated | Step 2 compensated |
|-----------------|-------------------|-------------------|
| Step 1 (debit client) | N/A (never executed) | N/A |
| Step 2 (credit bank) | ✅ credit client back | N/A |
| Step 3 (mark paid) | ✅ credit client back | ✅ debit bank back |

## Late Payment Penalty (Variable-Rate Loans)

When step 1 fails (client cannot pay), if the loan has `interest_type = "variable"`:
1. `BaseRate += 0.05%`
2. `NominalInterestRate = BaseRate + BankMargin`
3. Both `Loan` and all unpaid `Installments` are updated in a **single DB transaction** (prevents partial writes)

## Installment Status Machine

```
[unpaid]
    ├─ (expected_date > now, not collected) → [overdue]  (bulk update, daily)
    └─ (collected successfully) → [paid]
```

## Retry Behaviour

- Debit retry: `shared.Retry` with `DefaultRetryConfig` (3 attempts, exponential backoff)
- Bank credit retry: `shared.Retry` same config
- If debit permanently fails: installment stays `unpaid` or `overdue`; next cron cycle retries
- If bank credit permanently fails: debit is reversed; installment stays `unpaid`/`overdue`; next cron retries
- If mark-paid permanently fails: both steps reversed; next cron retries entire flow

No saga log is used (unlike transfer/payment); compensation is performed inline.
