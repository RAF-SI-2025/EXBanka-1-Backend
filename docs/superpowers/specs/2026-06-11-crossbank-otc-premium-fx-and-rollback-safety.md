# Cross-bank OTC premium: seller-side FX + settlement-rollback safety

**Date:** 2026-06-11
**Status:** implemented
**Reproduced live:** bank1 (111) ↔ bank2 (222), two EXBanka stacks.

## Problem (root-caused live)

A cross-bank OTC option accept where the premium currency is one the **seller has no
account for** silently destroys the listing and forms no contract — the user's
"saga faulted, listing is deleted, no contract is made, says not enough money for
premium/stocks when we had both".

Reproduction: bank1 client-1 (RSD+EUR accounts, no CHF) posts a GOOGL sell option;
bank2 client-2 bids binding a **CHF** account (premium → CHF); bank1 accepts. Result:

```
outbound_peer_txes: status=rolled_back, last_error="peer voted NO: NO_SUCH_ACCOUNT"
  postings[1]: seller CREDIT 40 CHF → client-1@111   ← seller has no CHF account
→ offer 683 CONSUMED, GOOGL contract 0 rows, shares reserved 0
```

### Two compounding defects

1. **No FX, buyer-currency denomination.** The cross-bank accept postings carry
   `AssetId = offer.PremiumCurrency`, which is the **buyer's bid-account currency**
   (set at bid time). The seller-credit leg targets the seller's PERSON id, resolved
   by the seller's bank to "first active <premium-ccy> account". If the seller has no
   account in the buyer's currency → `resolveOwnerAccount` returns NO_SUCH_ACCOUNT →
   the bank votes NO → the whole NEW_TX rolls back. (The **local** `buildAcceptSaga`
   FX-converts the premium; the cross-bank path never did.)

2. **False success + listing loss.** `InitiateOutboundTxWithPostings` returns
   `Status:"pending"` unconditionally even when the peer voted NO and the row is
   already `rolled_back`. Neither the inbound peer handler nor the outbound
   `acceptRemoteNegotiation` inspects the real outcome, so bank1 consumes the listing
   and reports `accepted` with no contract. (Fix A / `restoreListingOnFormationFailure`
   only covers the LOCAL mint path, not this cross-bank path.)

## Fix (user chose: seller-side FX in the executor)

### A. Seller-side FX in the interbank posting executor

`interbank-service/internal/sitx/posting_executor.go` — `reserveIncomingCredit`
(shared by premium-accept AND exercise-strike seller credits) becomes FX-aware:

- Resolve the credit **target account** for the participant: prefer an active account
  in the leg currency (no FX); else fall back to the participant's first active
  account in **any** currency.
- If the target account's currency == leg currency → reserve as today (no FX).
- Else → `exchange.Convert(legCcy → targetCcy, amount)` and reserve the **converted**
  amount in the target account's currency. The reservation key carries the converted
  amount, so COMMIT/ROLLBACK settle/release it unchanged (no commit-path change).
- When no exchange client is wired (tests / older deploys) the old behaviour is
  preserved (NO_SUCH_ACCOUNT / NO_SUCH_ASSET), so this is purely additive.

The executor gains an optional `Converter` (`exchangepb.ExchangeServiceClient`),
wired in `cmd/main.go` from `EXCHANGE_GRPC_ADDR`. The **debit** side is left strict:
the buyer always holds the premium currency (it IS their bid-account currency), so
no buyer-side FX is needed.

### B. Settlement-rollback safety

- `interbank-service/.../peer_tx_grpc_handler.go` — `InitiateOutboundTxWithPostings`
  returns the **actual** terminal row status (`committed` / `committing` /
  `rolled_back` / `pending`) instead of a hard-coded `"pending"`.
- `stock-service/.../peer_otc_grpc_handler.go` — `AcceptNegotiation`, after dispatch,
  treats a `rolled_back` / `failed` status as failure: revert the acceptance claim
  (accepted → ongoing), record NO accept revision, do NOT consume the listing, and
  return a `FailedPrecondition` error. The outbound `acceptRemoteNegotiation` then
  fails at its HTTP-code guard before consuming the listing, so the seller keeps
  their listing on any genuinely-unsatisfiable accept.

With A in place the cross-currency premium now settles (lands FX-converted in the
seller's account); B is the backstop for genuinely-unsatisfiable accepts (true
insufficiency, seller has zero accounts, peer unreachable).

## Testing

- **Unit (interbank):** `reserveIncomingCredit` FX path — participant with no
  leg-currency account → converts + reserves in the fallback account; same-currency
  path unchanged; nil-exchange preserves NO_SUCH_ACCOUNT.
- **Unit (stock):** `AcceptNegotiation` returns error + reverts claim + does not
  consume on a `rolled_back` dispatch status; success path unchanged.
- **Live (two stacks):** the GOOGL/CHF repro now settles (premium FX→seller account,
  contract active, shares reserved); a genuine-insufficiency accept fails cleanly with
  the listing intact.

## Limitations

Seller-side FX lands the premium in the seller's leg-currency account if they have
one, else their first active account — it does NOT honour the exact nominated account
cross-bank in Direction 2 (the nomination is not carried on the /accept handshake).
Honouring the exact nominated account cross-bank would require carrying it on the
SI-TX wire (a protocol change, out of scope here).
