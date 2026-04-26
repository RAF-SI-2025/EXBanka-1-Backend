# SI-TX-PROTO 2024/25 placeholder → canonical field mapping

The canonical SI-TX-PROTO spec lives at https://arsen.srht.site/si-tx-proto/ but
that page was not directly fetchable from this implementation session. The
authoritative on-disk source for field shapes is `docs/Celina 5(Nova).docx.md`
plus the design spec at `docs/superpowers/specs/2026-04-24-interbank-2pc-transfers-design.md` §6.

The names below match design-spec §6 exactly. If a future inter-bank interop
session reveals a divergence with the canonical SI-TX-PROTO page, update this
doc and rename the JSON tags in `transaction-service/internal/messaging/inter_bank_envelope.go`
and the JSON-encoder paths in the api-gateway internal handlers.

| Spec field        | Canonical SI-TX-PROTO field |
|-------------------|-----------------------------|
| transactionId     | transactionId               |
| action            | action                      |
| senderBankCode    | senderBankCode              |
| receiverBankCode  | receiverBankCode            |
| timestamp         | timestamp                   |
| body              | body                        |
| senderAccount     | senderAccount               |
| receiverAccount   | receiverAccount             |
| amount            | amount                      |
| currency          | currency                    |
| memo              | memo                        |
| status (Ready/NotReady inner) | status              |
| originalAmount    | originalAmount              |
| originalCurrency  | originalCurrency            |
| finalAmount       | finalAmount                 |
| finalCurrency     | finalCurrency               |
| fxRate            | fxRate                      |
| fees              | fees                        |
| validUntil        | validUntil                  |
| reason            | reason                      |
| creditedAt        | creditedAt                  |
| creditedAmount    | creditedAmount              |
| creditedCurrency  | creditedCurrency            |
| role              | role                        |
| updatedAt         | updatedAt                   |
| protocolVersion   | protocolVersion             |

Action enum string values (case-sensitive): `Prepare`, `Ready`, `NotReady`,
`Commit`, `Committed`, `CheckStatus`, `Status`.

NotReady reasons (case-sensitive): `insufficient_funds`, `account_not_found`,
`account_inactive`, `currency_not_supported`, `limit_exceeded`, `bank_inactive`,
`commit_mismatch`, `unknown`.
