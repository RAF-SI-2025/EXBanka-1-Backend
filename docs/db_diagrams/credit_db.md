# credit_db — ER Diagram

PostgreSQL, port 5438

```mermaid
erDiagram
    LoanRequest {
        uint64 id PK
        uint64 client_id
        string loan_type
        string interest_type
        decimal amount
        string currency_code
        string purpose
        decimal monthly_salary
        string employment_status
        int employment_period
        int repayment_period
        string phone
        string account_number
        string status
        int64 version
        time created_at
        time updated_at
    }

    Loan {
        uint64 id PK
        string loan_number UK
        string loan_type
        string account_number
        decimal amount
        int repayment_period
        decimal nominal_interest_rate
        decimal effective_interest_rate
        time contract_date
        time maturity_date
        decimal next_installment_amount
        time next_installment_date
        decimal remaining_debt
        string currency_code
        string status
        string interest_type
        uint64 client_id
        int64 version
        time created_at
        time updated_at
    }

    Installment {
        uint64 id PK
        uint64 loan_id FK
        decimal amount
        decimal interest_rate
        string currency_code
        time expected_date
        time actual_date
        string status
        int64 version
    }

    Loan ||--o{ Installment : "loan_id → id"
```

> **Cross-DB references** (not enforced by FK constraints):
> - `LoanRequest.client_id` → `client_db.clients.id`
> - `LoanRequest.account_number` → `account_db.accounts.account_number`
> - `Loan.client_id` → `client_db.clients.id`
> - `Loan.account_number` → `account_db.accounts.account_number`
