# transaction_db — ER Diagram

PostgreSQL, port 5437

```mermaid
erDiagram
    Payment {
        uint64 id PK
        string idempotency_key UK
        string from_account_number
        string to_account_number
        decimal initial_amount
        decimal final_amount
        decimal commission
        string currency_code
        string recipient_name
        string payment_code
        string reference_number
        string payment_purpose
        string status
        string failure_reason
        int64 version
        time timestamp
        time completed_at
    }

    Transfer {
        uint64 id PK
        string idempotency_key UK
        string from_account_number
        string to_account_number
        decimal initial_amount
        decimal final_amount
        decimal exchange_rate
        decimal commission
        string from_currency
        string to_currency
        string status
        string failure_reason
        int64 version
        time timestamp
        time completed_at
    }

    ExchangeRate {
        uint64 id PK
        string from_currency
        string to_currency
        decimal buy_rate
        decimal sell_rate
        int64 version
        time updated_at
    }

    PaymentRecipient {
        uint64 id PK
        uint64 client_id
        string recipient_name
        string account_number
        time created_at
        time updated_at
    }

    VerificationCode {
        uint64 id PK
        uint64 client_id
        uint64 transaction_id
        string transaction_type
        string code
        time expires_at
        int attempts
        bool used
    }
```

> **Cross-DB references** (not enforced by FK constraints):
> - `Payment.from_account_number` / `to_account_number` → `account_db.accounts.account_number`
> - `Transfer.from_account_number` / `to_account_number` → `account_db.accounts.account_number`
> - `PaymentRecipient.client_id` → `client_db.clients.id`
> - `VerificationCode.client_id` → `client_db.clients.id`
> - `VerificationCode.transaction_id` → `transaction_db.payments.id` or `transaction_db.transfers.id`
