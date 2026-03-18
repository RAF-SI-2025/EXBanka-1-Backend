# card_db — ER Diagram

PostgreSQL, port 5436

```mermaid
erDiagram
    Card {
        uint64 id PK
        string card_number UK
        string card_number_full
        string cvv
        string card_type
        string card_name
        string card_brand
        string account_number
        uint64 owner_id
        string owner_type
        decimal card_limit
        string status
        int64 version
        time expires_at
        time created_at
        time updated_at
    }

    AuthorizedPerson {
        uint64 id PK
        string first_name
        string last_name
        int64 date_of_birth
        string gender
        string email
        string phone
        string address
        uint64 account_id
        time created_at
        time updated_at
    }
```

> **Cross-DB references** (not enforced by FK constraints):
> - `Card.account_number` → `account_db.accounts.account_number`
> - `Card.owner_id` → `client_db.clients.id` (when owner_type = "client") or `card_db.authorized_persons.id` (when owner_type = "authorized_person")
> - `AuthorizedPerson.account_id` → `account_db.accounts.id`
