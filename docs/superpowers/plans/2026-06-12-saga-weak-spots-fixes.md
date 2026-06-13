# Saga weak-spot fixes (2026-06-12)

Fixes for the 5 open findings from the 2026-06-12 adversarial saga audit. Each is
an independent commit. Order: #1, #2 (stock-service), then #3, #4, #5 (interbank).

## #1 — Share-hold attach→fallback double-reserve (HIGH)
**Root cause.** `RecordOptionContract` (stock-service `peer_otc_grpc_handler.go`
~1589) attaches the vote-time share hold by `req.GetCrossbankTxId()`
(`AttachCrossBankReservationToContract`). On NotFound it falls back to
`ReserveForPeerOptionContract` (keyed on contract id). If a vote-time hold EXISTS
under a DIFFERENT key than the commit looks up (a `crossbank_tx_id` mismatch
vote↔commit), attach misses, the fallback re-reserves, and the seller now has TWO
active holds → over-reserved → phantom INSUFFICIENT on a later order/exercise.
Both paths already read `req.GetCrossbankTxId()`, so a conformant peer matches;
the hazard is a silent miss.
**Fix.**
- On attach NotFound: log at ERROR with the `crossbank_tx_id` + contract id
  (currently silent), so any vote↔commit id mismatch is immediately visible.
- Before the legacy fresh-reserve fallback, defensively RELEASE any orphaned
  vote-time hold for this `crossbank_tx_id` (`ReleaseForCrossBankNewTx`) so the
  fallback can never double-count, then reserve once.
- Keep the fallback (older peers that never reserved at vote legitimately have no
  hold) but it is now the explicit, logged, double-reserve-safe path.
**Files.** `stock-service/internal/handler/peer_otc_grpc_handler.go`. The
`CrossBankReserver` interface already exposes `ReleaseForCrossBankNewTx`.
**Tests.** unit: attach-miss with an orphaned vote-time hold → exactly one hold
remains after RecordOptionContract (no double-count); attach-hit → no fallback.
**Risk.** Low/additive; release-before-fallback only runs on the NotFound branch.

## #2 — FX rate-drift on LOCAL saga recovery (MEDIUM)
**Root cause.** `buildAcceptSaga` (otc_accept_saga.go ~67) FX-converts the premium
to the buyer's currency at build time and stores it only in volatile saga state
(`step:reserve_premium:amount`). Recovery rebuilds the saga → re-`Convert`s at the
recovery-time rate → the reserve/settle amount drifts from what was originally
held. Same shape in the exercise saga (strike conversion).
**Fix.** Persist the converted buyer-side amount on the `OptionContract` at first
build and REUSE it on recovery instead of re-converting.
- Add columns `BuyerPremiumAmount decimal`, `BuyerPremiumCurrency string` (accept)
  — `0`/empty means "not yet locked" (first run converts + persists; recovery
  reuses). For exercise, reuse the existing strike fields + add
  `BuyerStrikeAmount`/`BuyerStrikeCurrency` if the strike is FX'd.
- `buildAcceptSaga`: if `contract.BuyerPremiumAmount` is set, use it; else convert,
  then persist on the contract row (best-effort, before saga execute).
**Files.** `stock-service/internal/model/option_contract.go` (+AutoMigrate is
automatic), `otc_accept_saga.go`, `otc_exercise_saga.go`.
**Tests.** unit: build twice with a CHANGED FX rate → second build (recovery)
reuses the first's persisted amount, not the new rate.
**Risk.** Medium — schema add (additive, nullable/default-0, back-compat).

## #3 — Option-materialise after money-settle window (MEDIUM)
**Root cause.** In `HandleCommitTx` + `InitiateOutboundTxWithPostings` (interbank
`peer_tx_grpc_handler.go`) money settles (CommitIncoming/SettleOutgoing/SettleLocal)
BEFORE `materialiseOptions`. A crash/failure between them leaves money-moved but
contract-missing until cron retry.
**Fix.** Reorder: `materialiseOptions` FIRST (it's idempotent on
`crossbank_tx_id,posting_index` and does NOT depend on the money having moved),
then settle the money. Both stay idempotent so the cron heals either order; doing
the user-visible contract first shrinks the inconsistent window to "contract
exists, money settling".
**Files.** `interbank-service/internal/handler/peer_tx_grpc_handler.go` (two
call sites). Verify materialise has no data dependency on settle.
**Tests.** existing commit tests must stay green; add one asserting a settle
failure AFTER materialise leaves the contract present (recoverable).
**Risk.** Low/medium — reordering within an already-idempotent sequence.

## #4 — CREDIT multi-posting reservation-key collision (LATENT)
**Root cause.** `reserveIncomingCredit`/`fxReserveCredit` reserve under a SHARED
key `peer:key`; `SettleLocal`/`ReverseLocal` commit/release that one key. Two money
CREDITs to the same bank in one tx would collide (2nd treated as a dup, silently
unreserved). Today's OTC/payment flows have ≤1 credit per bank, so latent.
**Fix.** Key credits PER-POSTING `peer:key:i` (like debits), and make the
commit/release iterate credit postings, committing/releasing each `peer:key:i`.
Keep backward-compat for in-flight pre-upgrade txs is N/A (holds are short-lived;
upgrade during a quiet window) — but to be safe, the change is symmetric across
reserve+settle+reverse so a tx that reserved per-posting also settles per-posting.
**Files.** `interbank-service/internal/sitx/posting_executor.go` (reserveIncomingCredit
returns per-posting key; SettleLocal/ReverseLocal iterate credits), and the commit
path in `peer_tx_grpc_handler.go` if it commits credits by the shared key.
**Tests.** unit: a tx with TWO credits to the same bank reserves BOTH (distinct
holds), and both settle/release.
**Risk.** Medium — touches the credit settle/reverse; verify single-credit (today's
real flows) is unchanged.

## #5 — "committing" stuck dead-letter alert (DESIGN LIMIT)
**Root cause.** `OutboundReplayCron.driveCommit` retries a "committing" row forever
on a permanent downstream outage (money-safe by 2-phase design) but with no
escalation, so ops can't see a wedged tx.
**Fix.** When a "committing" row exceeds the attempt cap, emit a loud ALERT log
(and increment a metric if one exists) once, then keep retrying (do NOT roll back —
committing is the point of no return). Visibility only.
**Files.** `interbank-service/internal/service/outbound_replay_cron.go`.
**Tests.** unit: a committing row past the cap logs the alert; still retried.
**Risk.** Very low — log/metric only.

## Versioning
PATCH bumps per fix (4.4.1 → 4.4.6) or grouped; all backward-compatible. Run the
affected-module tests + gofmt + build after each.
