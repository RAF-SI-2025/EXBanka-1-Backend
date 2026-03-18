# auth_db — ER Diagram

PostgreSQL, port 5433

```mermaid
erDiagram
    RefreshToken {
        int64 id PK
        int64 user_id
        string token UK
        time expires_at
        bool revoked
        time created_at
    }

    ActivationToken {
        int64 id PK
        int64 user_id
        string token UK
        time expires_at
        bool used
        time created_at
    }

    PasswordResetToken {
        int64 id PK
        int64 user_id
        string token UK
        time expires_at
        bool used
        time created_at
    }

    LoginAttempt {
        int64 id PK
        string email
        string ip_address
        bool success
        time created_at
    }

    AccountLock {
        int64 id PK
        string email
        string reason
        time locked_at
        time expires_at
        time unlocked_at
    }

    TOTPSecret {
        int64 id PK
        int64 user_id UK
        string secret
        bool enabled
        time created_at
        time updated_at
    }

    ActiveSession {
        int64 id PK
        int64 user_id
        string user_role
        string ip_address
        string user_agent
        time last_active_at
        time created_at
        time revoked_at
    }
```

> **Cross-DB references** (not enforced by FK constraints):
> - `RefreshToken.user_id` → `user_db.employees.id` or `client_db.clients.id`
> - `ActivationToken.user_id` → `user_db.employees.id`
> - `PasswordResetToken.user_id` → `user_db.employees.id`
> - `TOTPSecret.user_id` → `user_db.employees.id`
> - `ActiveSession.user_id` → `user_db.employees.id` or `client_db.clients.id`
