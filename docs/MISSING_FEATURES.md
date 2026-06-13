# EXBanka — Missing & Incomplete Features

**Generated:** 2026-06-07 · **Source:** the requirements (Celina 1–5 + `TODO_final.pdf` + defense flow) reconciled against the implementation by the test plan in [`docs/test-plan/`](./test-plan/). The authoritative row-by-row checklist is [`docs/test-plan/coverage-matrix.md`](./test-plan/coverage-matrix.md); this document is the **curated, detailed** view of the genuine functional gaps.

## How to read this

Each entry states **what the requirement wants** (the feature intent, from the spec docs), the **current state**, the **source** (which requirements doc), the **test-case id** that will pass once it's built, and **what to build**.

Gaps are tagged:
- **MISSING** — the requirement has no implementing behaviour at all.
- **DIVERGES** — something is implemented, but it behaves differently from the requirement.
- **PARTIAL** — implemented for some channels/cases but not all (most often: in-app works, email/push don't).
- **FRONTEND** — a UI-only requirement with no backend surface (listed for completeness; not a backend gap).
- **VERIFY** — the requirement may be met but could not be confirmed; needs a check.

> **Not included here:** ~70 rows in the coverage matrix marked `partial` only because they lack an *integration test* (the behaviour exists and has unit coverage). Those are test-coverage gaps, not missing features — see the matrix gap list.

Priority is a rough call: **P1** = required for the defense / money-correctness / security; **P2** = required feature, lower blast radius; **P3** = nice-to-have / explicitly "if time permits".

---

## 1. Notifications (cross-cutting — highest concentration of gaps)

`TODO_final.pdf` (and Celina 2/3/4) require that **the user is notified of every significant change to their account, through email *and* in-app, with mobile push where applicable.** Today most events produce an in-app inbox item only; email is wired for a handful of events; mobile push carries verification challenges only — never business events.

### 1.1 Account-locked email — **MISSING** · P1
- **Wants:** After 5 consecutive failed logins the account locks; the system **emails the user** that the account was locked, including a reset-password link that unlocks it. (Celina 1 "Bezbednosne mere"; `TODO_final` Celina 1.)
- **Current:** Lock happens, but no `EmailTypeAccountLocked`/template exists; the user is never told.
- **TC:** TC-C1-LOCK-050 / TC-NOTIF-001. **Build:** add a lock email template + publish on lock.

### 1.2 Dividend-received notification — **MISSING** · P2
- **Wants:** When a shareholder receives a dividend payout, notify them (amount, account, source currency). (Celina 3 dividends; `TODO_final` Celina 3.)
- **Current:** No notification of any kind (no in-app, email, or push).
- **TC:** TC-NOTIF-016. **Build:** emit notification on each `DividendPayout`.

### 1.3 Tax-deducted notification — **MISSING** · P2
- **Wants:** When the monthly capital-gains tax is deducted from the user's account, notify them (amount, period). (Celina 3 tax; `TODO_final`.)
- **Current:** No notification at all.
- **TC:** TC-NOTIF-017. **Build:** emit on tax collection.

### 1.4 Business-event mobile push — **MISSING** · P2
- **Wants:** Mobile push for important business events (orders, payments, transfers, OTC). (`TODO_final` — "email i mobilne notifikacije".)
- **Current:** `notification.mobile-push` carries only verification challenges; in-app is poll-only; no push for business events.
- **TC:** TC-NOTIF-023. **Build:** fan business notifications out to the mobile-push topic too.

### 1.5 Email for events that are in-app-only — **PARTIAL** · P2
The following events create an in-app item but send **no email** (the requirement asks for both). Add the missing email leg for each:
- Payment executed, transfer executed (TC-NOTIF-002/003). (Celina 2.)
- Credit created, credit approved (TC-NOTIF-007/008 — `LoanApproved` email type is defined but never published). (Celina 2.)
- Order created→Pending, order approved, order rejected, order fully executed, order partially filled, order auto-cancelled, price-alert fired, recurring-order skipped (TC-NOTIF-009…015, 018). (Celina 3; `TODO_final` Celina 3.)
- OTC counter-offer received, OTC offer accepted, OTC offer withdrawn, option-contract expiring in N days (TC-NOTIF-019…022; TC-C4-OTCNOTE-003). (Celina 4; `TODO_final` Celina 4 — "Notifikacije za OTC pregovaranje".)

### 1.6 Settlement-expiry order auto-cancel — **MISSING** · P2
- **Wants:** Orders on a security whose settlement date has passed are **automatically cancelled**, and the user is notified ("Kada se order automatski otkaže (npr. istekao settlement date)"). (`TODO_final` Celina 3.)
- **Current:** No settlement-expiry auto-cancel cron; expiry maps onto a generic manual cancel.
- **TC:** TC-NOTIF-014 (note). **Build:** scheduler that cancels expired-settlement orders + notifies.

---

## 2. Celina 1 — User Management

### 2.1 Password reset does not unlock a locked account — ✅ **FIXED (2026-06-07)** · was P1
- **Wants:** "Reset lozinke otključava nalog i resetuje broj neuspešnih pokušaja" — resetting the password must clear the brute-force lock and zero the failed-attempt counter. (Celina 1; `TODO_final` Celina 1.)
- **Was:** `ResetPassword` did not call `UnlockAccount`; a locked user stayed locked for the full 30 min even after a successful reset.
- **Fix:** injected an `accountUnlocker` into `AccountService`; `ResetPassword` now calls `UnlockAccount(email)`. Test: `auth-service ... TestResetPassword_UnlocksLockedAccount`.
- **TC:** TC-C1-LOCK-051.

### 2.2 Create employee as inactive — **MISSING** · P2
- **Wants:** When an admin creates an employee, "po default-u … aktivan, ali moguće je napraviti i korisnika koji nije aktivan" — there must be a way to create an *inactive* employee. (Celina 1 "Kreiranje i aktivacija naloga".)
- **Current:** No create-time `active` flag; inactive is only reachable via a follow-up `PUT {active:false}`.
- **TC:** TC-C1-EMP-100b. **Build:** accept an `active` field on create.

### 2.3 Lockout threshold/duration divergence — **DIVERGES** · P3
- **Wants:** Celina 1 / `TODO_final` say **5 attempts → 10-minute lock**; the E2E doc says **3 attempts**.
- **Current:** Implementation is **5 attempts → 30-minute lock** (within a 15-min window). Pick the authoritative number and align docs + code.
- **TC:** TC-C1-LOCK-041/043.

### 2.4 Reset-link TTL divergence — **DIVERGES** · P3
- **Wants:** E2E doc says the reset link **expires after 15 minutes**.
- **Current:** Reset-token TTL is **1 hour** (activation token is 24h). Reconcile.
- **TC:** TC-C1-PWD-063.

### 2.5 Client field validations — **VERIFY** · P3
- **Wants:** DOB must not be in the future; phone may contain only digits and a leading `+`; email unique + valid format. (Celina 1/2 "Validacija podataka".)
- **Current:** Email format/uniqueness is enforced; DOB-not-future and phone digits/`+` enforcement is unverified.
- **TC:** TC-C1-CLI-143/144. **Action:** confirm enforcement; add validators if absent.

---

## 3. Celina 2 — Core Banking

### 3.1 Cross-currency payment between different clients — **MISSING** · P1
- **Wants:** A client can **pay another client in a currency different from the source account**, with on-the-fly conversion. (Celina 2 payments; E2E "Plaćanje između računa različitih klijenata — različita valuta"; defense **Provera 4**.)
- **Current:** Payments are single-currency; FX exists only in the *transfer* (own-account) flow.
- **TC:** TC-C2-PAY-020. **Build:** allow a target currency on payments with conversion + commission (mirror the transfer FX path).

### 3.2 Virtual card `unlimited` usage type — **MISSING** · P2
- **Wants:** Virtual cards support three usage types: `single_use`, `multi_use` (with `max_uses`), and **`unlimited`**. (Celina 2 cards / virtual cards.)
- **Current:** Gateway `oneOf` validation accepts only `single_use` and `multi_use`; `unlimited` cannot be created.
- **TC:** TC-C2-CARD-009. **Build:** allow `unlimited` end-to-end (gateway validation + service).

### 3.3 Card spend with multi-currency fee — **MISSING** · P2
- **Wants:** Spending on a card in a currency other than the account currency incurs a fee (e.g. conversion 2% + 0.5%). (Celina 2 "Multi-currency card fees".)
- **Current:** There is no card-spend / POS transaction endpoint at all, so the fee has nowhere to apply.
- **TC:** TC-C2-CARD-015. **Build:** a card-spend transaction path that converts + charges the fee. (Lower priority if card spend is out of project scope — confirm.)

### 3.4 Automatic monthly installment deduction + overdue notice — **VERIFY / PARTIAL** · P1
- **Wants:** Loan installments are **deducted automatically each month**; on success the client is debited and notified; on failure the installment goes **overdue** and the client gets a failure notice (like a missed payment). (Celina 2 loans "Automatic installment deduction".)
- **Current:** Implemented as a cron with no manual trigger and no test; the overdue + failure-notice path is unverified.
- **TC:** TC-C2-INST-005/006. **Action:** confirm the cron debits + handles insufficient funds (overdue + notice); add an admin trigger to make it testable.

---

## 4. Celina 3 — Securities Trading

### 4.1 Exchange-closed order rejection — **DIVERGES** · P1
- **Wants:** An order placed while the exchange is **closed** must be **rejected** with the message "Berza je zatvorena". (Celina 3 exchange hours; E2E "Nalog odbijen van radnog vremena berze".)
- **Current:** The order is accepted and its fill is merely deferred; there is no server-side closed-market rejection.
- **TC:** TC-C3-EXC-030. **Build:** reject (or hold with explicit status) when the listing's exchange is closed.

### 4.2 Client forex/options visibility restriction — ✅ **FIXED (2026-06-07)** · was P1
- **Wants:** Clients may view/trade **only stocks and futures** — never forex pairs or options. (Celina 3 portal access matrix; E2E.)
- **Was:** the backend served forex and option listings with a `200` to client tokens; the restriction was UI-only and trivially bypassed via the API.
- **Fix:** new `DenyClientToken()` gateway middleware on `/securities/forex*` and `/securities/options*` → `403` for client principals (candles stay open for stocks/futures). Live-verified: client→403, agent→200. Test: `api-gateway ... TestDenyClientToken_*`.
- **TC:** TC-C3-VIS-002/003.

### 4.3 Margin trading prerequisites not enforced — **MISSING** · P1
- **Wants:** A **margin** order is allowed only if the trader is eligible: an employee needs explicit margin permission; a client needs approved credit; and available credit **or** cash must be **≥ the initial margin cost** (= maintenance margin × 1.1). Otherwise the order is rejected. (Celina 3 "Margin Order".)
- **Current:** The `margin` flag is persisted but no eligibility/funding check is enforced server-side (initial margin cost is display-only).
- **TC:** TC-C3-MGN-002/003. **Build:** gate margin orders on permission/credit and the IMC funding check.

### 4.4 Order-approval condition is a conjunction, not a disjunction — ✅ **FIXED (2026-06-07)** · was P1
- **Wants:** An agent's order needs supervisor approval if **any** of: the agent has `needApproval=true`, **OR** the agent's daily limit is exhausted, **OR** the order would exceed the remaining daily limit. (Celina 3 approval workflow.)
- **Was:** the gate was a **conjunction** (`needApproval` AND over-limit), so an over-limit order from an agent without the flag auto-approved — money could move past the daily limit without review.
- **Fix:** extracted `decideNeedsApproval` as the spec'd disjunction (flag OR used+amount>limit) and wired it into the placement saga. Live-verified: need_approval=false + over-limit order → `pending`. Test: `stock-service ... TestDecideNeedsApproval` (9 cases).
- **TC:** TC-C3-APV-003.

### 4.5 Quarterly automatic dividend payout — **MISSING** · P2
- **Wants:** Dividends are paid **automatically, quarterly** (last business day of Mar/Jun/Sep/Dec) to every holder, `Dividend = Quantity × Price × (DividendYield / 4)`, in the listing currency, taxed at 15% (except bank-held). (Celina 3 / `TODO_final` "Isplata dividendi".)
- **Current:** Dividends are admin-declared with a manual payout; no quarterly scheduler and the per-holder `yield/4` formula isn't auto-applied.
- **TC:** TC-C3-DIV-030. **Build:** quarterly cron computing the formula per holder.

### 4.6 Dividend account-routing fallback — **VERIFY / PARTIAL** · P3
- **Wants:** Pay the dividend to the account the stock was bought from; if that account is gone, the client's default account in that currency; if none, convert to RSD via the menjačnica (no commission). (Celina 3 / `TODO_final`.)
- **Current:** Fallback chain is unverified.
- **TC:** TC-C3-DIV-010. **Action:** confirm/implement the fallback.

### 4.7 Tax report: PDF export — **MISSING** · P2
- **Wants:** The user's tax report can be **exported to PDF**. (Celina 3 / E2E "omogući izvoz u PDF".)
- **Current:** No PDF export endpoint.
- **TC:** TC-C3-TAX-040. **Build:** PDF rendering of the tax report.

### 4.8 Tax report: filter by fiscal year — **MISSING** · P2
- **Wants:** View the tax report for a **previous fiscal year** (filter by year). (Celina 3 / E2E "Pregled poreskog izveštaja za prethodnu fiskalnu godinu".)
- **Current:** No year filter.
- **TC:** TC-C3-TAX-041. **Build:** a `year` filter on the tax report.

### 4.9 Tax profit-discrepancy flagging — **MISSING** · P2
- **Wants:** If reported profit and system-computed profit disagree, **flag the transaction** for manual reconciliation. (Celina 3 / E2E "Otkrivanje neslaganja u profitu i označavanje transakcije".)
- **Current:** No discrepancy detection/flag.
- **TC:** TC-C3-TAX-042. **Build:** discrepancy check + a "flagged for review" state.

---

## 5. Celina 4 — OTC & Investment Funds

### 5.1 Fund partial liquidation on insufficient liquidity — **MISSING** · P1
- **Wants:** When a redemption exceeds the fund's liquid cash, the system **automatically sells fund securities** to cover the shortfall and notifies the client that payout will follow shortly. (Celina 4 funds; E2E "Delimična likvidacija sredstava".)
- **Current:** The redemption simply returns `409` on short cash; no auto-liquidation, no deferred-payout notice.
- **TC:** TC-C4-FUND-010. **Build:** liquidation step (sell securities → cover) + deferred-payout notification.

### 5.2 Block deposit while a withdrawal is pending — **MISSING** · P2
- **Wants:** If a client has a pending withdrawal from a fund, **block new deposits** into that same fund until it resolves. (Celina 4 funds; E2E "Blokirati uplatu ako je isplata na čekanju".)
- **Current:** No such guard (tied to the liquidation follow-up above).
- **TC:** TC-C4-FUND-011. **Build:** reject deposit when a redemption is pending for that client+fund.

### 5.3 Local SAGA refund-retry → admin alert — **PARTIAL** · P2
- **Wants:** If a fund/option refund (compensation) fails, retry up to 3 times and **alert administrators** if all attempts fail. (Celina 4 SAGA; E2E "Ponovni pokušaj povraćaja … obavesti administratore".)
- **Current:** Realized on the cross-bank SI-TX path; the **local** admin-alert email is not wired.
- **TC:** TC-C4-SAGA-005. **Build:** admin-alert notification on exhausted local compensation retries.

### 5.4 OTC deviation color bands ±5/±20% — **FRONTEND** · P3
- **Wants:** Active-offers are color-coded by how far an offer deviates from a reference (green ≤5%, yellow 5–20%, red >20%). (Celina 4 / `TODO_final`.)
- **Current:** Frontend-only; the backend exposes the raw revision data needed to compute it. No backend work required unless a computed field is desired.
- **TC:** TC-C4-OTCNEG-011.

---

## 6. Celina 5 — Cross-Bank (SI-TX)

> Note: many cross-bank flows (exercise SAGA, compensation, premium/strike-gain tax, non-buyer-cannot-exercise) **are implemented and pass via local analogues**; they are only "unverified cross-bank" because confirming them needs two running stacks. Those are not listed as missing. The items below are genuine functional gaps against the requirements.

### 6.1 Cross-bank payment in a different currency + fee to the receiving bank — **MISSING** · P1 (defense item)
- **Wants:** A cross-bank payment can be **converted to the recipient's currency**, with the **receiving bank (Bank B)** computing the rate and charging its **commission** (the fee accrues to Bank B). (Celina 5 "Plaćanja" 2PC flow — the "Ready" reply carries *Krajnja vrednost*, *Kurs*, *Provizija*; defense **Provera 3** "plaćanje između banaka, različita valuta … provizija na račun banke primaoca".)
- **Current:** The shipped wire is single-currency balanced postings with a **sender-side** fee; there is no execution-time FX on the wire and no receiving-bank commission. The "Ready{endValue, FX, commission}" message has no representation (the reply is a `TransactionVote`).
- **TC:** TC-C5-PAY-040/041/042, TC-C5-E2E-030. **Build:** a conformant cross-bank FX + receiving-bank-fee path (note the SI-TX wire is frozen — coordinate with the cohort protocol).

### 6.2 Cross-bank buyer exercise tax — **MISSING** · P2 (deliberately deferred)
- **Wants:** A buyer who exercises a cross-bank option pays 15% on the strike gain, same as intra-bank. (Celina 5 "Obračun poreza".)
- **Current:** The frozen exercise wire carries neither the premium nor the market price, so the buyer-side tax cannot be computed cross-bank. Tracked as a known deferral.
- **TC:** TC-C5-TAX-030.

### 6.3 10-second no-response SLA — **DIVERGES** · P3
- **Wants:** If the receiving bank does not respond within **10 seconds**, the payment is cancelled and the sender refunded. (Celina 5 / E2E "Banka B ne odgovara 10 sekundi".)
- **Current:** Refund is driven by a replay-cron cap + ~10-minute reservation TTL, not a literal 10-second timeout.
- **TC:** TC-C5-PAY-043.

### 6.4 Combined inter-bank audit-trail record — **PARTIAL** · P3
- **Wants:** A single audit record per inter-bank transaction with sender bank, receiver bank, send time, receive time, status. (Celina 5 / E2E "Evidentirati kompletan audit trag".)
- **Current:** The data exists but is split across two status endpoints, not exposed as one combined log object.
- **TC:** TC-C5-PAY-030.

---

## 7. Verification & Quick Approve (security-sensitive)

### 7.1 Verification result is not enforced on the gated action — **DIVERGES / MISSING** · P1 (security/money)
- **Wants:** A money action that requires verification proceeds **only after** the challenge is verified; a wrong/expired/exhausted challenge cancels the action. (Celina 2 "Verifikacioni kod".)
- **Current:** `verification.challenge-verified` / `-failed` have **no consumer**, so a failed or expired challenge does not auto-cancel the payment — it can sit in `pending_verification`. The enforcement loop is open.
- **TC:** TC-VERIF-041/050. **Build:** consume the verification result and gate/cancel the action accordingly.

### 7.2 `verification.skip` permission is not enforced — **DIVERGES** · P1 (security)
- **Wants:** Only holders of `verification.skip` (supervisor/admin) may bypass verification. (Spec roles.)
- **Current:** The bypass is **structural** — any caller can skip simply by omitting `challenge_id`; the permission is decorative (no execute path checks it).
- **TC:** TC-VERIF-060/061/062. **Build:** require `verification.skip` on the no-challenge path; reject otherwise.

### 7.3 Challenge submission has no caller-ownership check — **DIVERGES** · P1 (security)
- **Wants:** Only the challenge's owner can submit a response to it.
- **Current:** Code submission performs no caller-ownership check — any caller can verify any challenge id.
- **TC:** TC-VERIF-070. **Build:** bind the challenge to its principal and verify on submit.

### 7.4 Only `code_pull` verification method works — **MISSING** · P2
- **Wants:** Verification methods include `code_pull`, `qr_scan`, `number_match`, and `email`. (Spec enum `verification_method`.)
- **Current:** Only `code_pull` is creatable; `qr_scan`/`number_match`/`email` return `400`; the QR-verify endpoint is therefore unusable.
- **TC:** TC-VERIF-020/021/022/074. **Build:** the other challenge methods (or trim the enum to match reality).

### 7.5 Verification not required on all sensitive actions — **PARTIAL** · P2
- **Wants:** Verification is required for client-initiated money/limit actions — payments, transfers, and **limit changes**; `TODO_final` Quick Approve "se primenjuje na sve akcije koje zahtevaju verifikaciju". (Celina 2/3.)
- **Current:** Only payment and transfer are gated; limit changes and OTC exercise are not.
- **TC:** TC-VERIF gated-action matrix. **Build:** gate limit-change (and any other spec'd sensitive action).

### 7.6 Quick Approve from push — **MISSING** · P3 (if-time-permits)
- **Wants:** Approve a verification-required action directly from the **push notification** (no code typed); the request **expires after 5 minutes** with no response; applies to all verification actions. (`TODO_final` mobile item 7.)
- **Current:** Approximated by the biometric path only; there is no dedicated push-approve endpoint, and the mobile `ack` is delivery-only (not approval).
- **TC:** TC-VERIF-080/081/082, TC-MOBILE-023. **Build:** a push-approve action with a 5-minute expiry.

---

## 8. Mobile App (explicitly "if time permits")

### 8.1 Per-card transaction history — **MISSING** · P3
- **Wants:** Transaction history **per card**, paginated. (`TODO_final` mobile item 3 — "Istorijat za svaku karticu odvaja po stranici".)
- **Current:** History is per-account only; no per-card endpoint.
- **TC:** TC-MOBILE-006. **Build:** per-card, paginated transaction history.

### 8.2 30-day exchange-rate (kursna lista) history — **MISSING** · P3
- **Wants:** View the exchange-rate list over the **last 30 days** before transacting. (`TODO_final` mobile item 5.)
- **Current:** exchange-service stores only the current rate; no history.
- **TC:** TC-MOBILE-009. **Build:** persist daily rate snapshots + a 30-day history endpoint.

> Other mobile items from `TODO_final` (view cards + block, accounts + balances, menjačnica, current kursna lista, upcoming loan installment) **are** available through existing `/api/me/*` endpoints — see the matrix.

---

## Summary by priority

**✅ Fixed 2026-06-07:** 2.1 (reset unlock), 4.2 (client forex/options 403), 4.4 (approval disjunction). 10 P1 items remain.

| Priority | Count | Items |
|---|---|---|
| **P1** (defense / money / security) | 10 | 1.1, 3.1, 3.4, 4.1, 4.3, 5.1, 6.1, 7.1, 7.2, 7.3 |
| **P2** | 16 | 1.2, 1.5, 3.2, 3.3, 4.5, 4.7, 4.8, 4.9, 5.2, 5.3, 6.2, 7.4, 7.5, + notif emails (1.2/1.3/1.4/1.5/1.6) |
| **P3** | rest | thresholds/TTL divergences, FX-SLA, audit object, Quick Approve, mobile niceties, frontend-only |

For the exhaustive non-`covered` list (including pure test-coverage gaps not repeated here), see [`docs/test-plan/coverage-matrix.md` → Gap list](./test-plan/coverage-matrix.md).
