# Database Overview — Cross-Service Relationships

Seven separate PostgreSQL instances. Foreign key constraints are **not** enforced across databases — relationships are maintained at the application layer.

| Database | Port | Service | Key Entities |
|---|---|---|---|
| user_db | 5432 | user-service | Employee |
| auth_db | 5433 | auth-service | RefreshToken, ActivationToken, PasswordResetToken, LoginAttempt, AccountLock, TOTPSecret, ActiveSession |
| client_db | 5434 | client-service | Client |
| account_db | 5435 | account-service | Currency, Company, Account, LedgerEntry |
| card_db | 5436 | card-service | Card, AuthorizedPerson |
| transaction_db | 5437 | transaction-service | Payment, Transfer, ExchangeRate, PaymentRecipient, VerificationCode |
| credit_db | 5438 | credit-service | LoanRequest, Loan, Installment |

## Cross-Service Reference Map

```
user_db.employees.id
    ← auth_db.refresh_tokens.user_id
    ← auth_db.activation_tokens.user_id
    ← auth_db.password_reset_tokens.user_id
    ← auth_db.totp_secrets.user_id
    ← auth_db.active_sessions.user_id (when role = employee)
    ← account_db.accounts.employee_id

client_db.clients.id
    ← auth_db.refresh_tokens.user_id (when role = client)
    ← auth_db.active_sessions.user_id (when role = client)
    ← account_db.companies.owner_id
    ← transaction_db.payment_recipients.client_id
    ← transaction_db.verification_codes.client_id
    ← credit_db.loan_requests.client_id
    ← credit_db.loans.client_id

account_db.accounts.account_number
    ← card_db.cards.account_number
    ← transaction_db.payments.from_account_number / to_account_number
    ← transaction_db.transfers.from_account_number / to_account_number
    ← credit_db.loan_requests.account_number
    ← credit_db.loans.account_number
    ← account_db.ledger_entries.account_number

account_db.accounts.id
    ← card_db.authorized_persons.account_id

credit_db.loans.id
    ← credit_db.installments.loan_id  (enforced by FK)
```
