# Plan: cross-bank buyer/sender funds — reserve-then-settle + timeout release

**Date:** 2026-05-30 · **Base:** Development · **Status:** ✅ Implemented 2026-05-30

> Implemented: `OutgoingReservation` model/repo/service + `OutgoingReservationTimeoutCron`
> (account-service); `ReserveOutgoing`/`SettleOutgoing`/`ReleaseOutgoing` RPCs on internal
> `AccountService`; `PostingExecutor.Reserve`/`ReverseLocal`/`SettleLocal` and
> `PeerTxGRPCHandler` (`HandleCommitTx`/`HandleRollbackTx`/`InitiateOutboundTx`/
> `CommitOutboundLocal`/`ReverseOutboundLocal`) converted from immediate-debit to
> reserve-then-settle. Unit tests added/updated in both services; all suites + lint green.
> `OUTGOING_RESERVATION_TTL` env (default 10m) wired in config + docker-compose.

## Context

The cross-bank (SI-TX) money DEBIT leg currently does an **immediate debit**
(`UpdateBalance(-X)`) at dispatch/vote and **credits back** on NO/rollback. The
Celina-5 spec (OTC accept SAGA step 1; simple-transfer §2) instead requires a
**reservation** at prepare (`rezervisanaSredstva += amount` → AvailableBalance
drops, Balance unchanged) and the actual debit only at **commit**
(`sredstva -= amount`), with the hold **released** on NO/timeout. This mirrors
the share-side fix just shipped (5137d83) and the intra-bank order path. The
user also requires **time-safety**: if the peer never responds or votes NO, the
hold must be lifted automatically.

Money-safe today, but not spec-shaped and the customer's *Balance* dips during
the in-flight window. After this change only *AvailableBalance* dips until
commit.

## Design

Add a **string-keyed outgoing reservation** to account-service — the debit-side
mirror of the existing `IncomingReservation` trio
(`account-service/internal/service/incoming_reservation_service.go`):

- `ReserveOutgoing(accountNumber, amount, currency, key)` — guard
  `AvailableBalance >= amount`; `AvailableBalance -= amount` (Balance unchanged);
  write a pending `OutgoingReservation` row. Idempotent on key. Returns
  `FailedPrecondition` (insufficient/inactive/currency) so the caller votes
  NO / rejects.
- `SettleOutgoing(key)` — the commit: under `FOR UPDATE`, `Balance -= amount`
  (AvailableBalance already reduced at reserve), write a debit ledger entry,
  mark `settled`. Idempotent (ledger idempotency_key dedupes).
- `ReleaseOutgoing(key)` — the rollback/timeout: `AvailableBalance += amount`
  (Balance untouched), mark `released`. Idempotent (no-op on non-pending).

New model `OutgoingReservation` (mirror of `IncomingReservation`: statuses
pending/settled/released, `ReservationKey` uniqueIndex, `Version`,
`CreatedAt index`). New repo with `GetByKey`, `MarkSettled`, `MarkReleased`,
`ListStalePendingOlderThan(before, limit)`. AutoMigrate in account cmd/main.go.

New proto RPCs on `AccountService` (account.proto, internal):
`ReserveOutgoing`, `SettleOutgoing`, `ReleaseOutgoing` (+ messages), `make proto`.

### Timeout safety — a wired sweeper (the gap the incoming side never closed)

A receiver that voted YES holds an outgoing reservation until the sender sends
COMMIT/ROLLBACK; if the sender vanishes, the hold must auto-release. The
incoming side has a `ListStaleByCreatedBefore` repo method but **no wired cron**
(harmless for credits, which have no balance impact). For **outgoing** (real
holds) we MUST wire it:

- New `OutgoingReservationTimeoutCron` in account-service (mirror
  `MaintenanceCronService` shape; cronreg-registered, ctx-cancellable, ticker
  with `defer Stop()`): every interval, `ListStalePendingOlderThan(now - TTL)`
  → `ReleaseOutgoing(key)` for each. TTL configurable
  (`OUTGOING_RESERVATION_TTL`, default e.g. 10m — comfortably longer than the
  SI-TX dispatch+commit window, which is seconds). Wired in cmd/main.go +
  docker-compose env.
- This is a **backstop**. The primary release stays inline (NO vote) and via
  transaction-service's `OutboundReplayCron` (which already drives stuck
  outbound rows to rolled_back + reversal). Both paths are idempotent, so the
  backstop and the inline/replay paths can race harmlessly.

### transaction-service wiring (reserve → settle/release)

The DEBIT **money** legs (NOT the option-marker legs) become reserve-then-settle.
Three call sites, all keyed on the existing per-posting tag `<peer>:<idem>:<i>`
(already used as `DebitedItem.IdempotencyTag`):

1. **`posting_executor.go` `Reserve` DEBIT money branch** (currently immediate
   `UpdateBalance(-X)`): call `ReserveOutgoing(key=tag)`. On failure →
   `noVote(INSUFFICIENT_ASSET)`. Keep recording the `DebitedItem` (now "reserved"
   not "debited") so COMMIT can settle and ROLLBACK can release. Used by BOTH the
   receiver (`HandleNewTx`) and the sender-local apply
   (`InitiateOutboundTxWithPostings`).
2. **`ReverseLocal`** + **`HandleRollbackTx`** (currently credit-back
   `UpdateBalance(+X)`): call `ReleaseOutgoing(key=tag)` per reserved item.
3. **COMMIT** must now settle: `HandleCommitTx` (receiver) decodes `rec.DebitsJSON`
   and `SettleOutgoing(tag)` per item; the sender's `InitiateOutboundTx*`
   commit-success path and `CommitOutboundLocal` (replay cron) settle their
   reserved debit(s).
4. **Plain `InitiateOutboundTx`** (sender payment): replace the immediate
   `UpdateBalance(-X)` (line 370) with `ReserveOutgoing`; on peer YES+commit →
   `SettleOutgoing`; on NO / dispatch failure / replay-cron give-up →
   `ReleaseOutgoing`. The `outbound_peer_txs` row + `OutboundReplayCron` remain
   the durable driver; only the balance op shape changes (reserve/settle/release
   instead of debit/creditback).

`AccountClient` interface in `posting_executor.go` and the handler's account
client gain `ReserveOutgoing/SettleOutgoing/ReleaseOutgoing`; the real
`accountpb.AccountServiceClient` satisfies them after proto regen.

Note: the SI-TX **wire protocol** (NEW_TX/COMMIT_TX/ROLLBACK_TX HTTP envelopes,
`contract/sitx`) is **unchanged** — this is local balance-handling + an internal
account RPC. Cohort interop unaffected.

## Files

- account-service: `internal/model/outgoing_reservation.go` (new);
  `internal/repository/outgoing_reservation_repository.go` (new);
  `internal/service/outgoing_reservation_service.go` (new);
  `internal/service/outgoing_reservation_timeout_cron.go` (new);
  `internal/handler/grpc_handler.go` (3 RPC handlers);
  `cmd/main.go` (AutoMigrate, service+cron wiring, TTL config);
  `internal/config` (TTL env).
- contract: `proto/account/account.proto` (3 RPCs + messages) → `make proto`.
- transaction-service: `internal/sitx/posting_executor.go` (Reserve/ReverseLocal +
  AccountClient iface); `internal/handler/peer_tx_grpc_handler.go`
  (InitiateOutboundTx, HandleCommitTx settle, HandleRollbackTx release,
  CommitOutboundLocal/ReverseOutboundLocal); account-client interface.
- docker-compose.yml + docker-compose-remote.yml: `OUTGOING_RESERVATION_TTL`.

## Testing

- account-service unit: ReserveOutgoing holds (Available drops, Balance steady) /
  insufficient → FailedPrecondition / idempotent; SettleOutgoing debits Balance
  once + idempotent; ReleaseOutgoing restores Available + idempotent + no-op on
  settled; reserve→settle and reserve→release net-balance invariants; timeout
  cron releases stale pending and skips settled/released.
- transaction-service unit: executor DEBIT-money path reserves (keyed) not
  debits; insufficient → NO vote; ReverseLocal/HandleRollbackTx release;
  HandleCommitTx settles each DebitsJSON item; InitiateOutboundTx reserve→settle
  on YES, reserve→release on NO. Update existing immediate-debit assertions.
- Regression (time-safety): a reserved outgoing hold with no COMMIT/ROLLBACK is
  released by the timeout cron; a concurrent local spend during the hold sees
  reduced Available (can't double-spend).
- `make test` + lint on account-service + transaction-service + contract; SG
  suite must stay green; rebuild + cohort/cross-bank flows.

## Rollout / compat

- account-service must deploy before/with transaction-service (new RPCs). The
  outbound replay + idempotent keys make in-flight rows safe across the swap:
  an old-style immediate-debit row reversed by creditback still nets out; new
  rows use reserve/settle.
- TTL default 10m: long enough that a healthy commit never gets pre-empted,
  short enough that a vanished peer frees the customer's funds promptly.

## Out of scope
- No change to NEW_TX/COMMIT/ROLLBACK envelopes or `contract/sitx`.
- Intra-bank order ReserveFunds path unchanged (already reserve-then-settle).
