# account_db — ER Diagram

PostgreSQL, port 5435

```mermaid
erDiagram
    Currency {
        uint64 id PK
        string name
        string code UK
        string symbol
        string country
        string description
        bool active
    }

    Company {
        uint64 id PK
        string company_name
        string registration_number UK
        string tax_number UK
        string activity_code
        string address
        uint64 owner_id
        int64 version
        time created_at
        time updated_at
    }

    Account {
        uint64 id PK
        string account_number UK
        string account_name
        uint64 owner_id
        string owner_name
        decimal balance
        decimal available_balance
        uint64 employee_id
        time expires_at
        string currency_code
        string status
        string account_kind
        string account_type
        string account_category
        decimal maintenance_fee
        decimal daily_limit
        decimal monthly_limit
        decimal daily_spending
        decimal monthly_spending
        uint64 company_id FK
        int64 version
        time created_at
        time updated_at
        time deleted_at
    }

    LedgerEntry {
        uint64 id PK
        string account_number
        string entry_type
        decimal amount
        decimal balance_before
        decimal balance_after
        string description
        string reference_id
        string reference_type
        time created_at
    }

    Account }o--|| Currency : "currency_code → code"
    Account }o--o| Company : "company_id → id"
    LedgerEntry }o--|| Account : "account_number → account_number"
```

> **Cross-DB references** (not enforced by FK constraints):
> - `Account.owner_id` → `client_db.clients.id` (personal accounts) or `company.owner_id` (corporate accounts)
> - `Account.employee_id` → `user_db.employees.id`
> - `Company.owner_id` → `client_db.clients.id`
