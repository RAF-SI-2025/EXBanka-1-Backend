# client_db — ER Diagram

PostgreSQL, port 5434

```mermaid
erDiagram
    Client {
        uint64 id PK
        string first_name
        string last_name
        int64 date_of_birth
        string gender
        string email UK
        string phone
        string address
        string jmbg UK
        string password_hash
        string salt
        bool active
        bool activated
        time created_at
        time updated_at
    }
```

> **Cross-DB references** (not enforced by FK constraints):
> - `Client.id` → `auth_db.refresh_tokens.user_id` (client refresh tokens)
> - `Client.id` → `auth_db.active_sessions.user_id` (client sessions)
