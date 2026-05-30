# Plan: reserve cross-bank OTC seller shares at NEW_TX (vote), not at COMMIT

**Date:** 2026-05-30
**Branch base:** Development

## Context / problem

The Celina-5 SI-TX protocol spec (`docs/Celina-5(Nova).md`, OTC accept SAGA, step 2
"Provera i rezervacija hartija – Banka B") requires the seller's bank, when it
processes the prepare/NEW_TX, to **reserve the shares** (`rezervisaneHartije +=
zahtevaniBroj`) and reply `RESERVE_SHARES_CONFIRM` — symmetric with the buyer-funds
reservation in step 1. The actual ownership transfer is a later commit step
(step 4 `TRANSFER_OWNERSHIP`). I.e. **reserve-at-prepare → settle-at-commit** for
both legs.

Today the code only **checks** shares at NEW_TX and reserves at COMMIT:

- `transaction-service/internal/sitx/posting_executor.go:179-208` — for a DEBIT
  option posting on our routing it calls `CheckSellerCanDeliver` (read-only:
  `available = Quantity - ReservedQuantity`, no hold) and records an `OptionItem`.
- `stock-service/internal/handler/peer_otc_grpc_handler.go:898-931`
  (`RecordOptionContract`, called at COMMIT_TX) is where
  `ReserveForPeerOptionContract` actually increments `ReservedQuantity`.

**The gap:** between vote-YES and COMMIT the seller can sell/transfer the shares.
The buyer's money already moved at vote time, so COMMIT's reserve then fails
(`FailedPrecondition`), the commit can't ack, and the trade is stuck ("money moved,
shares not held"). The "Fix #6" pre-check reduced the odds but does not close the
window the spec's RESERVE_SHARES step exists to close. A teammate flagged this; the
spec confirms it is a real deviation, not a style choice.

**Scope:** shares only (per user). The buyer-funds leg (currently immediate-debit
rather than reserve-then-settle) is money-safe and out of scope here.

**Protocol safety:** the relevant RPCs (`PeerOTCService.CheckSellerCanDeliver`,
`RecordOptionContract`) are an INTERNAL stock↔transaction-service gRPC
(`contract/proto/stock/stock.proto:1777`), NOT the frozen peer wire protocol
(`contract/sitx/` has none of these). The peer HTTP envelopes (NEW_TX / COMMIT_TX /
ROLLBACK_TX) and their JSON are unchanged. Only local behavior + an internal gRPC
change.

## Design

Move the share **reservation** to NEW_TX time and have COMMIT **attach/settle** it,
ROLLBACK **release** it. The wrinkle: the existing reservation API keys on
`PeerOptionContractID`, but at vote time **no `peer_option_contracts` row exists yet**
(it's minted at COMMIT in `RecordOptionContract`). So the vote-time reservation must
be keyed on the SI-TX transaction identity (`crossbank_tx_id = "<peerCode>:<idem>"`),
then linked to the contract at COMMIT.

### Persistence: add a CrossbankTxID-keyed reservation

`HoldingReservation` (stock-service/internal/model/holding_reservation.go) currently
keys on exactly one of OrderID / OTCContractID / PeerOptionContractID. Add a fourth
optional key + its unique index:

- `CrossbankTxID *string` with `uniqueIndex:ux_holding_reservation_crossbank_tx`.
- Extend the `BeforeCreate` "exactly one of" invariant to include it.
- Extend `HoldingReservationRepository.InsertIfAbsent` conflict-column selection
  (repository/holding_reservation_repository.go:42-50) to use `crossbank_tx_id` when
  set, plus `GetByCrossbankTxIDForUpdate` (mirror of
  `GetByPeerOptionContractIDForUpdate`).
- AutoMigrate already covers `&model.HoldingReservation{}` — the new nullable column +
  index are added automatically; no manual migration. (The unique index is partial-by-
  nullability in Postgres: multiple NULLs are allowed, so existing order/otc rows are
  unaffected.)

### New stock-service service methods (mirror the PeerOptionContract trio, keyed by crossbank_tx_id)

In `holding_reservation_service.go`, add:

1. `ReserveForCrossBankNewTx(ctx, sellerOwnerType, sellerOwnerID, securityType, ticker, crossbankTxID string, qty)` —
   copy of `ReserveForPeerOptionContract` (line 336) but writing `CrossbankTxID` instead
   of `PeerOptionContractID`. Same `FOR UPDATE` + available-check + `ReservedQuantity +=`
   + idempotent `InsertIfAbsent`. Returns FailedPrecondition on insufficient/none.
2. `AttachCrossBankReservationToContract(ctx, crossbankTxID string, peerOptionContractID)` —
   at COMMIT, set the existing crossbank-keyed reservation's `PeerOptionContractID` so the
   later exercise/settlement path (which consumes by `PeerOptionContractID`) keeps working
   unchanged. Idempotent (no-op if already attached). Alternatively: keep the reservation
   keyed by crossbank_tx_id end-to-end and change consume/release to accept either key —
   but attaching is the smaller blast radius (exercise/settlement code stays as-is).
3. `ReleaseForCrossBankNewTx(ctx, crossbankTxID string)` — mirror of
   `ReleaseForPeerOptionContract` (line 797) keyed on crossbank_tx_id; used on ROLLBACK
   when the contract was never minted (vote-YES but rolled back before COMMIT).

### Wire it into the SI-TX phases

**NEW_TX (stock side):** add `ReserveSellerShares` to `PeerOTCService` (internal proto)
OR overload the existing `CheckSellerCanDeliver` path. Cleanest: keep
`CheckSellerCanDeliver` read-only for the cheap pre-filter, and add a new internal RPC
`ReserveSellerSharesForNewTx(seller_id, ticker, quantity, crossbank_tx_id)` →
`ReserveForCrossBankNewTx`. Returns ok/insufficient.

**NEW_TX (transaction-service executor):** in `posting_executor.go` DEBIT-option branch
(line 180), replace the `CheckSellerCanDeliver`-only call with a real reserve:
call `ReserveSellerSharesForNewTx(...)` with `crossbankTxID = peerBankCode + ":" +
locallyGeneratedKey`. On failure → `noVote(NoVoteReasonInsufficientAsset, i)` (same as
today). The executor's `holdingChecker` dependency becomes a "holdingReserver"
(interface gains the reserve method; production wires the same stock client). Record the
reservation in `ReserveResult` so it can be released if a *later* posting in the same
NEW_TX votes NO.

**NEW_TX failure mid-loop / NO vote:** if any later posting fails after shares were
reserved, release the crossbank reservation (call `ReleaseForCrossBankNewTx`) — analogous
to how the executor must already undo CREDIT `ReserveIncoming` reservations. Confirm/extend
the existing rollback-of-partial-reserve path (the credit side uses `ReleaseIncoming`;
add the shares release alongside).

**COMMIT_TX:** in `RecordOptionContract` (peer_otc_grpc_handler.go:898), for a DEBIT
contract: instead of `ReserveForPeerOptionContract` (which re-checks + reserves and FAILS
if shares moved), call `AttachCrossBankReservationToContract(crossbankTxID, row.ID)` — the
shares are ALREADY reserved from vote time, so COMMIT just links the reservation to the
freshly-minted contract row. The crossbank_tx_id is already passed as `CrossbankTxId` into
`RecordOptionContract`. Keep a fallback: if no crossbank reservation exists (e.g. a contract
minted by an older NEW_TX before this change, or transaction-service didn't reserve), fall
back to the current `ReserveForPeerOptionContract` so COMMIT still works during rollout.

**ROLLBACK_TX (transaction-service):** in `HandleRollbackTx` (peer_tx_grpc_handler.go:247),
in addition to releasing CREDIT `ReleaseIncoming` and crediting back DEBIT money, release the
share reservation: call the stock side `ReleaseForCrossBankNewTx(crossbankTxID)`. Persist
enough at NEW_TX to know shares were reserved — reuse the existing `OptionsJSON` on the
idempotence record (it already lists the option legs) to know a DEBIT-side share reservation
exists for this idem; release per option item with `Direction==DEBIT` on our routing.

### Sender side (InitiateOutboundTxWithPostings)

The dispatching bank applies its OWN postings locally via `executor.Reserve(...)` at line
~502 and materialises options on YES. The same reserve-at-local-apply change flows through
(it calls the same executor). No separate work beyond the executor change, but verify the
sender-local path also releases shares on a NO/rollback (it already has a local-creditback +
ReverseLocal path; extend with the shares release).

## Files to change

- `stock-service/internal/model/holding_reservation.go` — add `CrossbankTxID *string` +
  unique index; extend `BeforeCreate` invariant.
- `stock-service/internal/repository/holding_reservation_repository.go` — `InsertIfAbsent`
  conflict column for crossbank_tx_id; add `GetByCrossbankTxIDForUpdate`.
- `stock-service/internal/service/holding_reservation_service.go` — add
  `ReserveForCrossBankNewTx`, `AttachCrossBankReservationToContract`,
  `ReleaseForCrossBankNewTx`.
- `contract/proto/stock/stock.proto` — add `ReserveSellerSharesForNewTx` RPC +
  request/response (and `ReleaseSellerSharesForNewTx` for rollback); `make proto`.
- `stock-service/internal/handler/peer_otc_grpc_handler.go` — implement the new RPC(s);
  change COMMIT path (`RecordOptionContract`) to attach-not-reserve with fallback.
- `transaction-service/internal/handler/peer_tx_grpc_handler.go` — call shares-reserve at
  NEW_TX (executor), shares-release at ROLLBACK; thread crossbank_tx_id.
- `transaction-service/internal/sitx/posting_executor.go` — DEBIT-option branch reserves
  (not just checks); `SellerHoldingChecker` → reserver interface; release on partial NO.
- transaction-service main wiring: the stock client already wired as `holdingChecker` →
  also satisfies the reserver interface.

## Testing

- **Unit (stock-service):** `ReserveForCrossBankNewTx` happy/insufficient/idempotent;
  `AttachCrossBankReservationToContract` links + idempotent; `ReleaseForCrossBankNewTx`
  releases + no-op-when-absent. Reservation actually raises `ReservedQuantity` and lowers
  available.
- **Unit (transaction-service):** posting_executor DEBIT-option path now reserves (mock
  reserver asserts reserve called with crossbank_tx_id); NO on a later posting releases the
  earlier share reservation; ROLLBACK releases shares.
- **Unit (peer_otc handler):** COMMIT attaches the vote-time reservation; falls back to
  reserve when none exists.
- **Regression — the actual bug:** a test proving that after vote-YES the seller's shares
  are HELD: simulate "seller tries to sell the reserved shares between vote and commit" →
  the local sell sees reduced available and is rejected/limited; COMMIT then succeeds
  (attach, no re-check failure). This is the assertion the spec wants and the current code
  fails.
- **Integration (two-bank, +test clients only):** extend the planned two-bank cross-bank
  OTC test — after the buyer's bank dispatches accept and the seller's bank votes YES,
  assert the seller's `available_quantity` dropped by the contract qty BEFORE commit;
  after commit, contract active + reservation linked.
- `make test` + `golangci-lint` on the three services; rebuild stock + transaction images
  and run the SG suite (must stay 5/5) plus the cohort/cross-bank flows.

## Rollout / compatibility

- COMMIT keeps the `ReserveForPeerOptionContract` fallback so in-flight trades that voted
  YES under the OLD code (no crossbank reservation) still commit. Safe to deploy both
  services together or stock-first.
- No peer wire-protocol change → cohort interop with other teams' banks is unaffected; a
  peer that doesn't reserve at its own vote is its own concern (we only fix OUR seller-side
  behavior, which is exactly what the spec assigns to "Banka B").

## Out of scope
- Buyer-funds reserve-then-settle realignment (currently immediate-debit; money-safe).
- Any change to NEW_TX/COMMIT/ROLLBACK HTTP envelopes or the sitx contract package.
