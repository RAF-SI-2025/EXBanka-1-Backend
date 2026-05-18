# Plan — Real-Time Inter-Bank Transaction Status (Celina 4)

**Parent spec:** `docs/superpowers/specs/2026-05-15-requirements-gap-analysis-design.md`
**Service:** `transaction-service` (state surfacing), `api-gateway` (read + WS), `notification-service` (general)
**Scope:** Client who initiated an SI-TX transfer sees its status (Initiated, Pending, Completed, Failed) in real time. Achieved with a new client-facing GET endpoint + in-app notification emit on every state transition.

The current implementation tracks `outbound_peer_tx.status` and saga-log phases internally but doesn't surface them to the originating client.

---

## Status enum surfaced to clients

| Internal state | Client-facing label |
|---|---|
| `outbound_peer_tx.status = pending`, no SI-TX message sent yet | `INITIATED` |
| SI-TX `MessageTransferRequest` sent, awaiting peer ACK | `PENDING` |
| Peer ACK received + local saga `transfer_funds` committed | `COMPLETED` |
| Saga compensating or terminal `failed` | `FAILED` |

The mapping lives in `transaction-service/internal/service/peer_tx_status_mapper.go` and is single-sourced.

## Repository / model (no schema change required)

We reuse `outbound_peer_tx` and the `inter_bank_saga_logs` table. Add a `LastClientStatus` column to `outbound_peer_tx` so we can detect transitions and notify exactly once per change:

```sql
ALTER TABLE outbound_peer_tx ADD COLUMN last_client_status VARCHAR(16) NOT NULL DEFAULT 'initiated';
```

Auto-migrate via the existing `db.AutoMigrate`.

## Service

`transaction-service/internal/service/peer_tx_status_service.go`:

- `GetClientFacingStatus(ctx, transferID, callerClientID) (Status, error)`:
  1. Load `transfers` row, verify `caller == transfers.from_owner_id`.
  2. Join `outbound_peer_tx` by `transfer_id` to read the SI-TX state.
  3. Return mapped status.
- `OnStateTransition(ctx, transferID, newStatus)`:
  1. Inside `db.Transaction`, `SELECT FOR UPDATE` the `outbound_peer_tx` row.
  2. If `last_client_status != newStatus`, save (version-checked) and publish `SI_TX_<NEW>` general notification.

Hook `OnStateTransition` is called from:

- Initial SI-TX send → `SI_TX_INITIATED` (already emitted close to `TRANSFER_SENT` on the originating side; ensure both fire).
- After SI-TX dispatch ack → `SI_TX_PENDING`.
- Saga `finalize` commit → `SI_TX_COMPLETED`.
- Saga compensation terminal → `SI_TX_FAILED`.

All emits are best-effort, AFTER the DB transaction commits (per CLAUDE.md).

## Notifications (4 new keys)

`SI_TX_INITIATED`, `SI_TX_PENDING`, `SI_TX_COMPLETED`, `SI_TX_FAILED`:

- Data: `{transfer_id, amount, currency, peer_bank_code}`.
- RefType `transfer`, RefID `transfer_id`.
- Push only; SMS/email not requested.

Template registry entries with concise wordings.

## Gateway endpoints

| Method | Path | Auth | Permission |
|---|---|---|---|
| GET | `/api/v3/me/transfers/:id/status` | AnyAuth | (ownership-gated, no extra perm) |
| WS | `/api/v3/ws/notifications` | existing | existing |

The GET endpoint:

- Validates caller owns the transfer (existing `enforceOwnership` over the resolved transfer row).
- Returns `{transfer_id, status, last_changed_at, peer_bank_code}`.

WebSocket is the existing notification stream — `SI_TX_*` notifications flow through the existing `notification.mobile-push` topic.

Note: status string is also added as a top-level field to the existing `GET /api/v3/me/transfers/:id` response (additive, backwards-compatible per the api-versioning rule).

## Tests

Unit:

- Status mapper covers all states.
- `OnStateTransition` deduplicates (no double-publish when called with same status twice).
- Service ownership check rejects wrong client.

Integration (`test-app/workflows/sitx_status_test.go`):

- Initiate an SI-TX transfer from client to peer. Force ack via the existing peer-bank test harness. Poll the new GET endpoint; assert status progresses `INITIATED → PENDING → COMPLETED`. Inbox contains four entries (one per transition).
- Negative path: simulate peer-bank rejection. Status reaches `FAILED`; inbox has `SI_TX_FAILED`.

## Docs

- §17: 1 new GET route.
- §18: `outbound_peer_tx.last_client_status` column.
- §19+§20: 4 new general-notification keys.
- §21: state transition map.
- §25: client-facing status section (link from §25 to this).
- `docs/api/REST_API_v1.md`: section under Transfers.

## Verification

- `make build`, transaction-service tests, integration suite, `make lint`.

## Commits

1. `feat(transaction-service): outbound_peer_tx.last_client_status column + mapper`
2. `feat(transaction-service): OnStateTransition emits + status read`
3. `feat(api-gateway): /api/v3/me/transfers/:id/status`
4. `feat(notification-service): SI_TX_* templates`
5. `test: SI-TX realtime status integration`
6. `docs: SI-TX status spec + REST_API_v1`

All on `Development`.
