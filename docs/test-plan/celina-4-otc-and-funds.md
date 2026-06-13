# Celina 4 — OTC Trading & Investment Funds — Test Cases

> Scope: Proširenje trgovine hartijama — direktna OTC trgovina akcijama (samo akcije, preko opcionih
> ugovora), opcioni-ugovor SAGA izvršenje, porez na premiju/opciju, investicioni fondovi (pun životni
> ciklus), raspodela dividendi u fondovima, statistika fondova, i Portal: Profit Banke.
>
> **Spec sources:** `Celina 4 2026.docx.md`; `TODO_final.pdf` (Celina-4 block: OTC negotiation
> notifications, Istorija pregovora, Raspodela dividendi u fondovima, Statistika fondova);
> `docs/Specification.md` §24 (Investment Funds), §26 (Intra-bank OTC Options), §20/§21 (enums &
> business rules — option-premium tax resolution-month model §21 lines 2568-2575); `docs/api/REST_API_v3.md`
> (OTC stocks/options + investment-funds + dividends + bank-profit slices); api-gateway handlers
> `otc_stock_handler.go`, `otc_options_handler.go`, `otc_negotiation_handler.go`,
> `investment_fund_handler.go`, `dividend_handler.go`, `recurring_fund_handler.go`; routes in
> `api-gateway/internal/router/router_v3.go`.
>
> **Cross-bank OTC (klijent↔klijent i supervizor↔supervizor između banaka) belongs to Celina 5** —
> noted where the same unified route dispatches remote, but its cases live in `celina-5-cross-bank.md`.
> Odbrana flow §4 "Provera 2 — OTC trade external" is therefore a Celina-5 scenario.
>
> **Template & conventions:** see `00-setup-and-conventions.md`. Error codes are the project standard
> (`validation_error`/400, `unauthorized`/401, `forbidden`/403, `not_found`/404, `conflict`/409,
> `business_rule_violation`/409). All routes are under `/api/v3` (handler godoc still says `/api/v1` for
> a few fund/dividend routes — the router wires them at `/api/v3`; the router is authoritative).
> Money-moving / state-changing cases MUST assert side-effects (premium transfer, share lock/release,
> holdings transfer, fund NAV/position, tax rows, Kafka events) — never status alone.

---

## 0. Route map (authoritative, from router_v3.go)

**OTC stocks marketplace** (browse `AnyAuth`; trade requires `otc.trade.accept` OR `securities.trade.any` for employees; clients allowed by ownership):
- `POST /api/v3/me/otc/stocks` — create sell/buy offer (`direction=sell|buy`)
- `DELETE /api/v3/me/otc/stocks/:id?direction=sell|buy` — cancel own offer
- `GET /api/v3/me/otc/stocks?direction=` — list caller's offers
- `GET /api/v3/otc/stocks?security_type=` — browse the public marketplace
- `POST /api/v3/otc/stocks/:id/buy` — buy from a sell offer
- `POST /api/v3/otc/stocks/:id/sell` — sell into a buy offer
- `POST /api/v3/otc/stocks/:id/buy-on-behalf` — employee buys for a client

**OTC options negotiation** (trade requires BOTH `securities.trade.any` AND `otc.trade.accept` for employees; clients allowed by ownership):
- `POST /api/v3/me/otc/options` — create a listing (`direction=sell_initiated|buy_initiated`)
- `GET /api/v3/me/otc/options` — caller's live involvement; `GET .../posted` — all posted listings
- `GET /api/v3/otc/options` — browse open listings; `GET /api/v3/otc/options/:id` — one listing
- `POST /api/v3/otc/options/:id/bid` — open a negotiation chain (many bidders per listing)
- `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter` — counter (either party)
- `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` — accept current terms (opposite party)
- `POST /api/v3/me/otc/options/:id/negotiations/:nid/reject` — reject (either party)
- `DELETE /api/v3/me/otc/options/:id/negotiations/:nid` — withdraw own chain (bidder only)
- `DELETE /api/v3/me/otc/options/:id` — cancel own listing (poster only; cascade-cancels chains)
- `GET /api/v3/me/otc/options/negotiations` — caller's chains (local+remote merged)
- `GET /api/v3/me/otc/options/negotiations/:nid/revisions` — full old→new revision chain
- `GET /api/v3/otc/options/:id/negotiations` / `.../timeline` — poster-only view of all incoming bids
- `GET /api/v3/me/otc/history` — terminal negotiations (filter status/since/until/counterparty)

**Option contracts & exercise:**
- `GET /api/v3/me/otc/contracts?role=buyer|seller|either` — caller's formed contracts
- `GET /api/v3/otc/contracts/:id` — one contract
- `POST /api/v3/otc/contracts/:id/exercise` — exercise (5-phase SAGA; dispatch local vs cross-bank)

**Investment funds:**
- `POST /api/v3/investment-funds` (`funds.manage.catalog`) — create fund
- `GET /api/v3/investment-funds?search=&active_only=&sort_by=&sort_order=` — discovery (AnyAuth)
- `GET /api/v3/investment-funds/:id` — enriched detail + stats + history (AnyAuth)
- `PUT /api/v3/investment-funds/:id` (`funds.manage.catalog`) — update
- `POST /api/v3/investment-funds/:id/invest` — invest (AnyAuth; `on_behalf_of_type=self|bank`)
- `POST /api/v3/investment-funds/:id/redeem` — redeem (AnyAuth; `on_behalf_of_type=self|bank`)
- `GET /api/v3/me/investment-funds` — caller's positions (Moji fondovi)
- `GET /api/v3/investment-funds/positions` (`funds.read.all`) — bank's positions
- `GET /api/v3/actuaries/performance` (`actuaries.read.all`) — actuary profit board

**Fund dividends:**
- `POST /api/v3/admin/dividends` (`securities.manage.catalog`) — declare a dividend
- `POST /api/v3/admin/dividends/:id/payout` (`securities.manage.catalog`) — fan-out payout
- `GET /api/v3/me/dividends` — caller's dividend history
- `GET /api/v3/investment-funds/:id/dividends` — a fund's dividend history

**Recurring fund investments (DCA into a fund):** `GET|POST /api/v3/me/recurring-funds`,
`GET /api/v3/me/recurring-funds/:id`, `POST .../pause|resume`, `DELETE .../:id`.

---

## 1. OTC Stocks Marketplace (`TC-C4-OTCSTK-*`)

Direct stock trading: a seller publishes shares to the public ("javni režim"); a buyer can either buy
from a sell offer or post a standing buy offer that a seller fills. The shares-available invariant
(`Quantity − ReservedQuantity`) is enforced under `SELECT FOR UPDATE` before any money moves.

#### TC-C4-OTCSTK-001 · Client publishes a sell offer (makes a holding public) (POSITIVE)
- **Feature:** Postavljanje hartije na tržište · **Spec:** Celina 4 "OTC trgovina" · **Existing test:** test-app/workflows/otc_test.go::TestOTC_ListOffers
- **Actor:** client (holder)
- **Preconditions:** client owns a filled stock holding (≥10 shares of a listed stock).
- **Request:** `POST /api/v3/me/otc/stocks`
  - Auth: `Bearer <client>`
  - Body: `{"direction":"sell","holding_id":<H>,"quantity":10,"price_per_unit":"150.00"}`
- **Verification:** n/a
- **Expected:** `201` · returns `offer` with seller owner = client, `public_quantity=10`, asking price `150.00`; the holding now appears in `GET /api/v3/otc/stocks`. Side-effects: holding's `public_quantity` set; no money moves yet.
- **Negative siblings:** missing `holding_id` → 400; missing `price_per_unit` (required for sell since Phase 11) → 400; `quantity<=0` → 400 `validation_error`; `holding_id` not owned by caller → 403 `forbidden`; quantity > shares owned → 412/409.

#### TC-C4-OTCSTK-002 · Client posts a standing buy offer backed by cash reservation (POSITIVE)
- **Feature:** Buy offer · **Spec:** Celina 4 · **Existing test:** test-app/workflows/otc_test.go::TestOTC_BuyOffer_MissingAccountID
- **Actor:** client
- **Preconditions:** client owns an RSD account funded ≥ quantity×price.
- **Request:** `POST /api/v3/me/otc/stocks`
  - Body: `{"direction":"buy","listing_id":<L>,"quantity":5,"price_per_unit":"100.00","buyer_account_id":<A>}`
- **Expected:** `201` · `offer` created; buyer account's `available_balance` drops by 5×100 (cash reserved). Side-effects: cash reservation on `buyer_account_id`.
- **Negative siblings:** missing `listing_id` → 400; missing `buyer_account_id` → 400 (otc_test.go::TestOTC_BuyOffer_MissingAccountID); missing `price_per_unit` → 400; `buyer_account_id` not owned → 403; insufficient funds for reservation → 409 `business_rule_violation`; `quantity<=0` → 400 (otc_test.go::TestOTC_BuyOffer_InvalidQuantity).

#### TC-C4-OTCSTK-003 · Browse the public OTC stocks marketplace (POSITIVE)
- **Feature:** Pregled hartija · **Spec:** Celina 4 · **Existing test:** test-app/workflows/otc_test.go::TestOTC_ListOffers, TestOTC_ListOffers_FilterBySecurityType
- **Actor:** any authenticated (client or employee)
- **Request:** `GET /api/v3/otc/stocks?security_type=stock`
  - Auth: `Bearer <any>`
- **Expected:** `200` · `offers[]` of public sell offers; only `stock` security type (OTC trades only stocks).
- **Negative siblings:** unauthenticated → 401 (otc_test.go::TestOTC_ListOffers_Unauthenticated); invalid `security_type` → 400 (otc_test.go::TestOTC_ListOffers_InvalidSecurityType).

#### TC-C4-OTCSTK-004 · Buyer buys from a sell offer — money + shares move (POSITIVE)
- **Feature:** Izvršenje kupoprodaje · **Spec:** Celina 4 · **Existing test:** test-app/workflows/otc_test.go::TestOTC_BuyOffer_InvalidQuantity (negative axis)
- **Actor:** client (buyer) with funded RSD account
- **Preconditions:** TC-C4-OTCSTK-001 created a sell offer for 10 shares @ 150.
- **Request:** `POST /api/v3/otc/stocks/:id/buy`
  - Body: `{"quantity":4,"buyer_account_id":<A>}` (shape per Portfolio.BuyOTCOffer)
- **Expected:** `200` · buyer account debited 4×150=600 + any fee; seller account credited; buyer holding +4; seller holding −4 (and `public_quantity` decremented). Side-effects: ledger entries both sides; `notification.send-email` to both.
- **Negative siblings:** `buyer_account_id` not owned → 403; quantity > offered → 409; insufficient funds → 409; offer not active → 412.

#### TC-C4-OTCSTK-005 · Seller fills a buy offer (sells into it) — shares-available guard (POSITIVE)
- **Feature:** Fill buy offer · **Spec:** Celina 4 · **Existing test:** —
- **Actor:** client (seller)
- **Preconditions:** TC-C4-OTCSTK-002 buy offer open for 5 shares; seller owns ≥5 shares.
- **Request:** `POST /api/v3/otc/stocks/:id/sell`
  - Body: `{"quantity":5,"seller_account_id":<A>}`
- **Expected:** `200` · `fill`; seller holding −5; buyer holding +5; reserved cash settles buyer→seller. Side-effects: ledger both sides; reservation consumed.
- **Negative siblings:** `seller_account_id` not owned → 403; `quantity<=0` → 400; offer not active OR seller short on shares (`Quantity−ReservedQuantity < quantity`) → 412 `business_rule_violation` ("Nedovoljno zaliha"); missing `seller_account_id` → 400.

#### TC-C4-OTCSTK-006 · Cancel own sell offer releases the publication (POSITIVE)
- **Feature:** Cancel offer · **Spec:** Celina 4 · **Existing test:** —
- **Actor:** client (offer owner)
- **Request:** `DELETE /api/v3/me/otc/stocks/:holding_id?direction=sell`
- **Expected:** `204` · `public_quantity` zeroed; offer disappears from marketplace.
- **Negative siblings:** cancel buy offer with `?direction=buy` → releases reserved cash; missing/invalid `direction` → 400; `id=0`/non-numeric → 400; cancelling someone else's offer → 403/404.

#### TC-C4-OTCSTK-007 · Employee buys an OTC stock on behalf of a client (POSITIVE)
- **Feature:** OTC on-behalf · **Spec:** §26 `otc.trade.on_behalf` · **Existing test:** —
- **Actor:** employee-on-behalf (holds `otc.trade.on_behalf`)
- **Request:** `POST /api/v3/otc/stocks/:id/buy-on-behalf`
  - Body: `{"quantity":2,"buyer_account_id":<clientAcct>,"on_behalf_of_client_id":<C>}`
- **Expected:** `200` · the client's account is debited and the client's holding is credited.
- **Negative siblings:** employee lacks `otc.trade.on_behalf` → 403; `buyer_account_id` not owned by `on_behalf_of_client_id` → 403; employee with NO `on_behalf_of_client_id` on the on-behalf route → must use a bank account else 403.

#### TC-C4-OTCSTK-008 · List my OTC stock offers, both directions (POSITIVE)
- **Feature:** Moje OTC ponude · **Spec:** Celina 4 · **Existing test:** —
- **Actor:** client
- **Request:** `GET /api/v3/me/otc/stocks?direction=buy`
- **Expected:** `200` · only caller's buy offers; omitting `direction` returns both.
- **Negative siblings:** invalid `direction` value → 400.

---

## 2. OTC Options Negotiation — intra-bank client↔client (`TC-C4-OTCNEG-*`)

OTC stock trading happens **only via option contracts**. A listing (`OTCOffer`) is posted; many bidders
each open their own negotiation chain (`OTCNegotiation`); terms (`quantity`, `strike_price` = price per
share, `premium`, `settlement_date`) are mutable per counter; the first accept wins atomically and
cascade-cancels siblings. Every revision is recorded with old→new + who + timestamp
(`OTCOfferRevision`).

#### TC-C4-OTCNEG-001 · Seller posts a sell_initiated option listing (POSITIVE)
- **Feature:** Pregovaranje — listing · **Spec:** Celina 4 "Trgovina - flow"; §26 · **Existing test:** test-app/workflows/otc_options_test.go::TestOTCOptions_ClientLifecycle
- **Actor:** client (seller) owning the underlying shares
- **Preconditions:** seller holds ≥ quantity shares of `ticker`; owns an RSD account.
- **Request:** `POST /api/v3/me/otc/options`
  - Body: `{"direction":"sell_initiated","ticker":"AAPL","quantity":"100","strike_price":"5000","premium":"50000","settlement_date":"2030-04-05","account_id":<A>}`
- **Expected:** `201` · `offer` with status `open`, `Ticker` recorded, initiator = seller, `InitiatorAccountID` bound (receives premium on accept). Side-effect: `otc.offer-created` Kafka.
- **Negative siblings:** invalid `direction` (not `sell_initiated`/`buy_initiated`) → 400; unknown ticker → 400 `validation_error` (otc_options_test.go::TestOTCOptions_UnknownTickerRejected); missing ticker/quantity/strike/settlement → 400; `account_id` not owned → 403; client supplies a foreign-currency account they don't own → 403 (otc_options_test.go::TestOTCOptions_ClientCannotUseForeignAccount).

#### TC-C4-OTCNEG-002 · Buyer opens a negotiation chain (bid) on a listing (POSITIVE)
- **Feature:** Pregovaranje — bid · **Spec:** Celina 4; §26 OpenNegotiation · **Existing test:** test-app/workflows/otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms
- **Actor:** client (buyer)
- **Preconditions:** TC-C4-OTCNEG-001 listing open; buyer owns funded RSD account.
- **Request:** `POST /api/v3/otc/options/:id/bid`
  - Body: `{"bidder_account_id":<A>,"quantity":"100","strike_price":"5000","premium":"45000","settlement_date":"2030-04-05"}`
- **Expected:** `201` · `negotiation` chain in status `open`; revision #1 records the bid terms + `ModifiedByPrincipal`. Many bidders may each open one chain.
- **Negative siblings:** missing `bidder_account_id` → 400; `quantity`/`strike_price` non-positive → 400 (`positiveDecimalString`); `premium` negative → 400 (`nonNegativeDecimalString`); `bidder_account_id` not owned → 403; bidding twice on the same listing as the same caller → 409 `conflict`; `id=0` → 400.

#### TC-C4-OTCNEG-003 · Counter-offer mutates each field + records old→new history (POSITIVE)
- **Feature:** Kontraponuda — svako polje promenljivo + istorija · **Spec:** Celina 4 (entitet ponude: Amount/Price/SettlementDate/Premium "Po kontraponudi"); TODO_final "Istorija pregovora" · **Existing test:** test-app/workflows/otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms
- **Actor:** either party (the one OPPOSITE to who proposed current terms)
- **Request:** `POST /api/v3/me/otc/options/:id/negotiations/:nid/counter`
  - Body: `{"quantity":"100","strike_price":"4800","premium":"48000","settlement_date":"2030-04-10"}`
- **Verification:** n/a
- **Expected:** `200` · chain status flips to `countered`; a new `OTCOfferRevision` appended with `revision_number+1`, the new values, `LastModified` timestamp, and `ModifiedBy` = caller. `GET /api/v3/me/otc/options/negotiations/:nid/revisions` shows the full old→new chain.
- **Negative siblings:** quantity/strike non-positive → 400; premium negative → 400; missing required fields → 400; caller is not a party to the chain → 403; **countering after `settlement_date` has passed → 409 `business_rule_violation`** (cannot modify an expired negotiation); countering a terminal (accepted/rejected/cancelled) chain → 409.

#### TC-C4-OTCNEG-004 · Accept current terms forms the contract (first-accept-wins) (POSITIVE)
- **Feature:** Postignut dogovor · **Spec:** Celina 4 "Postignut dogovor"; §26 Accept saga · **Existing test:** test-app/workflows/wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers, otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms
- **Actor:** the party opposite to whoever proposed the current terms
- **Preconditions:** chain in `open`/`countered`; acceptor owns the `acceptor_account_id`.
- **Request:** `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept`
  - Body: `{"acceptor_account_id":<A>}`
- **Expected:** `200` · response carries `winning`, `parent_status="consumed"`, `cancelled_siblings[]`, and the minted `contract`. Side-effects: **premium debited buyer → credited seller** (PartialSettle→CreditAccount saga); **seller's underlying shares reserved/locked** (HoldingReservation tied to OptionContract); parent listing → `consumed`; every sibling chain cascade-cancelled; `otc.contract-created` Kafka; **seller premium tax row** written (`SecurityType=option`, `+premium`, taxable at 15% for clients — see TC-C4-OTCTAX-001).
- **Negative siblings:** caller proposed the current terms → 403 `forbidden`; caller not a party → 403; `acceptor_account_id` missing → 400; not owned → 403; parent already consumed (a sibling already accepted) → 409 `conflict`; **contract-formation saga rejected** (seller no longer holds the shares OR buyer short on premium) → 412/409 → chain flips to `failed`, parent stays `consumed`.

#### TC-C4-OTCNEG-005 · Withdraw (cancel) own bidder chain — deletes for both (POSITIVE)
- **Feature:** Odustajanje od ponude · **Spec:** Celina 4 (prihvate/odustanu/protivponudu) · **Existing test:** —
- **Actor:** client (bidder; bidder-only)
- **Request:** `DELETE /api/v3/me/otc/options/:id/negotiations/:nid`
- **Expected:** `204` · chain status → `cancelled`; both parties see it as withdrawn; `notification.send-email` "offer withdrawn" to the poster (TODO_final OTC notif).
- **Negative siblings:** the listing's poster tries to cancel a bidder's chain → 403 (poster must `reject` instead); cancelling a terminal chain → 409; `nid=0` → 400; not the bidder → 403.

#### TC-C4-OTCNEG-006 · Reject a negotiation chain (either party) (POSITIVE)
- **Feature:** Odbijanje pregovora · **Spec:** Celina 4 · **Existing test:** —
- **Actor:** either party
- **Request:** `POST /api/v3/me/otc/options/:id/negotiations/:nid/reject`
- **Expected:** `200` · chain ends without a contract; **parent listing stays `open`** (other bidders can still win); appears in negotiation history as `REJECTED`.
- **Negative siblings:** caller not a party → 403; rejecting a terminal chain → 409.

#### TC-C4-OTCNEG-007 · Cancel own listing cascade-cancels all open chains (POSITIVE)
- **Feature:** Cancel listing · **Spec:** §26 CancelListing · **Existing test:** —
- **Actor:** client (listing poster; poster-only)
- **Request:** `DELETE /api/v3/me/otc/options/:id`
- **Expected:** `204` · parent → `cancelled`; every still-open child chain cascade-cancelled in the same TX; per-chain `OTC_OFFER_CASCADE_CANCELLED` notifications. No share/fund unwinding (listings hold no reservations).
- **Negative siblings:** caller is not the poster → 403 `forbidden` (gateway pre-checks via GetOffer); listing not `open` (already consumed/cancelled) → 409; offer not found → 404.

#### TC-C4-OTCNEG-008 · Active-offers page: list caller's live negotiations (POSITIVE)
- **Feature:** Stranica: Aktivne ponude · **Spec:** Celina 4 · **Existing test:** test-app/workflows/otc_unified_read_test.go::TestSP1_MyNegotiations_HasProvenanceFields, TestSP2b_OfferList_StampsMyNegotiationForBidder
- **Actor:** client
- **Request:** `GET /api/v3/me/otc/options/negotiations?statuses=open,countered`
- **Expected:** `200` · `negotiations[]` with `kind` (local|remote), `me_owner`, counterparty, current quantity/strike/settlement; filterable by `statuses`.
- **Negative siblings:** unauthenticated → 401; bad page params default silently (no 400).

#### TC-C4-OTCNEG-009 · Poster views all incoming bids; competing bidder is forbidden (NEGATIVE-centric)
- **Feature:** Aktivne ponude — poster view · **Spec:** §26 ListNegotiationsByListing · **Existing test:** test-app/workflows/otc_timeline_test.go::TestOTCTimeline_PosterAllowed_BidderForbidden
- **Actor:** poster (allowed) vs competing bidder (forbidden)
- **Request:** `GET /api/v3/otc/options/:id/negotiations` and `GET /api/v3/otc/options/:id/timeline`
- **Expected:** poster → `200` with every chain / merged chronological timeline; a competing bidder → `403 forbidden` (sees only their own chain via `/me/otc/options/negotiations`).

#### TC-C4-OTCNEG-010 · Concluded-contracts page (Sklopljeni ugovori) (POSITIVE)
- **Feature:** Stranica: Sklopljeni ugovori · **Spec:** Celina 4 · **Existing test:** test-app/workflows/otc_options_test.go::TestOTCOptions_ListMyContractsEmpty, otc_unified_read_test.go::TestSP1_MyContracts_BuyerIsOwner
- **Actor:** client
- **Request:** `GET /api/v3/me/otc/contracts?role=buyer`
- **Expected:** `200` · `contracts[]` (LOCAL+REMOTE merged) each with `kind`, `me_owner` (true when caller is buyer/holder), Stock/Amount/Strike/Premium/Settlement/Seller-info/Profit. Empty for a fresh client returns `[]` with correct shape.
- **Negative siblings:** invalid `role` defaults to `either` (no 400).

#### TC-C4-OTCNEG-011 · Deviation color bands ±5% / ±20% (NO-ENDPOINT — FE visualization)
- **Feature:** Vizualizacija (zelena ±5%, žuta ±5..±20%, crvena >±20%) · **Spec:** Celina 4 "Vizualizacija" · **Existing test:** —
- **Actor:** n/a
- **Expected:** **NO-ENDPOINT.** The backend returns raw terms and the full revision chain (old→new) via
  `GET /api/v3/me/otc/options/negotiations/:nid/revisions`; the green/yellow/red deviation coloring is
  computed client-side from those values. No dedicated API. Mark as a frontend-only requirement.

#### TC-C4-OTCNEG-012 · Unread-negotiations indicator (PARTIAL)
- **Feature:** Indikator broja nepročitanih pregovora (opciono) · **Spec:** Celina 4 "Obaveštenja - opciono" · **Existing test:** —
- **Actor:** client
- **Expected:** **PARTIAL.** `OTCOfferReadReceipt` persists per-owner last-seen `updated_at` and drives an
  `unread` flag exposed on the offer/negotiation read shapes (the "modifiedBy ≠ current user" heuristic).
  There is no explicit "mark all read" mutation endpoint — the receipt advances implicitly on read.
  Test: open a chain as A, counter as B, then `GET /api/v3/me/otc/options/negotiations` as A and assert
  the item reflects unread/last-modified-by-B.

---

## 3. Option Contract Formation, Premium & Share Lock (`TC-C4-OTCCON-*`)

#### TC-C4-OTCCON-001 · On accept: premium moves buyer→seller, seller shares lock (POSITIVE)
- **Feature:** Postignut dogovor — premija + zaključavanje akcija · **Spec:** Celina 4 "Postignut dogovor"; §26 Accept saga · **Existing test:** test-app/workflows/wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers
- **Actor:** acceptor (seller accepting a buyer's bid, or buyer accepting a seller's counter)
- **Preconditions:** chain ready to accept; buyer funded; seller holds shares.
- **Request:** `POST /api/v3/me/otc/options/:id/negotiations/:nid/accept` Body `{"acceptor_account_id":<A>}`
- **Expected:** `200` · OptionContract minted (status `ACTIVE`, Buyer/Seller owner + accounts bound, Ticker set). Side-effects (assert all): buyer account `available_balance` − premium; seller account + premium; a `HoldingReservation` (`otc_contract_id` set) locks `quantity` seller shares; contract appears in both parties' `GET /me/otc/contracts`.
- **Negative siblings:** buyer short on premium → saga fails (412), no reservation persists, chain `failed`; seller no longer holds shares → 412, chain `failed`.

#### TC-C4-OTCCON-002 · Cross-currency premium converts at live rate (POSITIVE)
- **Feature:** Premija u valuti akcije, konverzija buyer-side · **Spec:** §26 "Cross-currency support" · **Existing test:** —
- **Actor:** buyer with non-RSD account vs seller RSD
- **Expected:** `200` · premium denominated in seller's currency; buyer reserve/settle in buyer's currency at the live `exchange-service.Convert` rate; seller credited in their currency. Same-currency flows skip conversion.
- **Negative siblings:** no rate available for the pair → 409/500 surfaced from exchange-service.

#### TC-C4-OTCCON-003 · Multiple contracts per seller: Σ committed ≤ owned (POSITIVE + boundary)
- **Feature:** Više opcionih ugovora — Σ ≤ owned · **Spec:** Celina 4 (12 AAPL → 3+7, pregovara o 2) · **Existing test:** —
- **Actor:** client (seller owning 12 shares)
- **Steps:** accept contract A (3 shares) → accept contract B (7 shares) → start negotiating C for 2 (3+7+2=12, OK). Then attempt to accept a 4th for 3 shares.
- **Expected:** A and B accept (`200`); committed=10, available=2. Attempting to commit beyond the 12 owned → 412 `business_rule_violation` (seller short on shares). Boundary: a contract for exactly the remaining 2 succeeds; 3 fails.
- **Negative siblings:** over-commit on the boundary → 412.

#### TC-C4-OTCCON-004 · Expired unused contract frees the locked shares (POSITIVE)
- **Feature:** Istekao neiskorišćen ugovor oslobađa akcije · **Spec:** Celina 4 (prvi ugovor istekne → 3 akcije ponovo raspoložive) · **Existing test:** —
- **Actor:** system cron (OTCExpiryCron, daily 02:00 UTC) — verify via state after settlement_date passes
- **Preconditions:** an ACTIVE contract whose `settlement_date` is in the past and was never exercised.
- **Expected:** contract → `EXPIRED`; the seller's `HoldingReservation` released; the freed shares become available for new negotiations again; `otc.contract-expired` Kafka. After expiry the seller can negotiate for the freed quantity.
- **Negative siblings:** exercising an already-EXPIRED contract → 409 (see TC-C4-SAGA-002).

#### TC-C4-OTCCON-005 · Get a contract by id (POSITIVE + ownership)
- **Feature:** Contract detail · **Spec:** §26 GetContract · **Existing test:** —
- **Actor:** buyer/seller/employee
- **Request:** `GET /api/v3/otc/contracts/:id`
- **Expected:** `200` · `kind=local`, `me_owner` true for buyer/holder. 404 only when neither local nor remote exists.
- **Negative siblings:** unrelated client → 403 `forbidden` (or 404 where existence must not leak); bad id → 400.

---

## 4. Exercise via SAGA — 5 phases + compensation (`TC-C4-SAGA-*`)

Exercise runs the SAGA: (1) reserve funds (buyer strike) → (2) reserve seller shares → (3) transfer funds
buyer→seller → (4) transfer share ownership → (5) final double-check. Each phase has a failure event +
compensation; the system must recognize the reached step and compensate accordingly. The buyer alone
may exercise. (Local exercise here; cross-bank SI-TX exercise → Celina 5.)

#### TC-C4-SAGA-001 · Happy-path exercise: buyer pays strike, gets shares, seller paid & loses shares (POSITIVE)
- **Feature:** Uspešna realizacija OTC trgovine · **Spec:** Celina 4 SAGA; odbrana §4 Provera 1; E2E "Uspešna realizacija" · **Existing test:** test-app/workflows/saga_sg_test.go::TestSG01_HappyPath, wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Actor:** client (buyer/holder of an ACTIVE contract, market > strike)
- **Preconditions:** ACTIVE contract; market price > strike; buyer funded for the strike.
- **Request:** `POST /api/v3/otc/contracts/:id/exercise`
  - Auth: `Bearer <buyer>` · Body: `{}` (local contract — accounts come from the contract)
- **Verification:** n/a (employee path may need verification.skip; client self-service → full flow)
- **Expected:** `201` · contract → `EXERCISED`. Side-effects (assert all): buyer account − strike×qty; seller account + strike×qty; **buyer holding += qty** at **market cost basis** (tax step-up, §21); seller's reserved shares consumed (seller holding − qty, reservation cleared); `otc.contract-exercised` Kafka; buyer tax row `(market−strike)×qty − premium` in exercise month (TC-C4-OTCTAX-002).
- **Negative siblings:** see TC-C4-SAGA-002..006.

#### TC-C4-SAGA-002 · Cannot exercise after settlement date / when expired (NEGATIVE)
- **Feature:** Ne može se iskoristiti istekla opcija · **Spec:** Celina 4 (istekli ugovori vidljivi radi evidencije); odbrana §4 Provera 4 ("opcija više ne može da se iskoristi") · **Existing test:** —
- **Actor:** buyer
- **Request:** `POST /api/v3/otc/contracts/:id/exercise` on an EXPIRED/past-settlement contract
- **Expected:** `409 business_rule_violation` (`OPTION_USED_OR_EXPIRED`); no money moves; contract stays EXPIRED.
- **Negative siblings:** exercising an already-EXERCISED contract → 409 (replay blocked by `active→exercising` CAS).

#### TC-C4-SAGA-003 · Non-buyer cannot exercise (NEGATIVE)
- **Feature:** Authorization · **Spec:** §26 · **Existing test:** test-app/workflows/saga_sg_test.go::TestSG02a_NonBuyerRejected, TestSG02b_UnknownContract
- **Actor:** the seller / a third party
- **Request:** `POST /api/v3/otc/contracts/:id/exercise`
- **Expected:** `403 forbidden` (non-buyer); unknown contract → `404 not_found`.

#### TC-C4-SAGA-004 · Phase-3/4 failure compensates fully (refund + return shares + "Poništena") (NEGATIVE)
- **Feature:** Neuspešan prenos vlasništva pokreće poništavanje · **Spec:** Celina 4 SAGA Napomena; E2E "Neuspešan prenos vlasništva" · **Existing test:** test-app/workflows/saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds, TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds
- **Actor:** buyer; fault injected via `X-Saga-Force-Fail` header (stock-service built `-tags sagafaults`, `SAGA_FAULTS_OK=1`, `SAGA_SG=1`)
- **Request:** `POST /api/v3/otc/contracts/:id/exercise` with header `X-Saga-Force-Fail: credit_strike_seller` (F3) or `mark_contract_exercised` (F5)
- **Expected:** exercise fails; **all prior steps compensate** — buyer refunded, seller shares returned, contract left **ACTIVE** (proven by a subsequent clean retry succeeding — invariants I1/I2/I3/I6). No partial money/share movement persists.
- **Negative siblings:** F5 (after share transfer) must also fully compensate (pivot removed, Phase 0) — contract ACTIVE, not stuck EXERCISED.

#### TC-C4-SAGA-005 · Fund-refund retry ≤3 then alert admin (NEGATIVE)
- **Feature:** Retry povraćaja ≤3 + obaveštenje administratora · **Spec:** Celina 4 SAGA "Primer dobre prakse"; E2E "Ponovni pokušaj povraćaja" · **Existing test:** — (NO-ENDPOINT for direct trigger)
- **Actor:** system (compensation recovery)
- **Expected:** **PARTIAL.** The exercise saga compensates synchronously; the failed-compensation +
  bounded-retry-then-admin-alert path is the cross-bank `OutboundReplayCron` 4-attempt cap → `failed`
  for SI-TX (Celina 5). For local exercise, failed compensation steps remain in `compensating` for
  background recovery (saga-log pattern). No REST trigger to force a network-flapping refund retry;
  assert the saga-log row transitions instead. Mark partial (admin-alert email not wired locally).

#### TC-C4-SAGA-006 · Double-reserve prevention on concurrent negotiations (NEGATIVE)
- **Feature:** Sprečavanje duplog rezervisanja akcija · **Spec:** Celina 4; E2E "Sprečavanje duplog rezervisanja" ("Nedovoljno zaliha za ovu ponudu") · **Existing test:** test-app/workflows/wf_stock_concurrent_orders_test.go (concurrency pattern); saga_sg_test.go invariants
- **Actor:** two buyers concurrently accepting two chains that each need the seller's full share count
- **Expected:** exactly one accept forms a contract; the other → 412 `business_rule_violation`
  ("Nedovoljno zaliha za ovu ponudu"). Share reservation is row-locked (SELECT FOR UPDATE); the second
  loses the CAS. No double lock; seller's committed ≤ owned holds.

#### TC-C4-SAGA-007 · Funds consumed before exercise → cancel + refund (NEGATIVE)
- **Feature:** OTC trgovina ne uspe zbog nedovoljnih sredstava tokom izvršenja · **Spec:** Celina 4; E2E "OTC trgovina ne uspe zbog nedovoljnih sredstava" · **Existing test:** —
- **Actor:** buyer whose account is drained by another transaction between accept and exercise
- **Request:** `POST /api/v3/otc/contracts/:id/exercise`
- **Expected:** Phase-1 reserve-funds fails → saga aborts cleanly, no shares move, contract stays ACTIVE; `409 business_rule_violation` (insufficient funds). The seller's lock remains until expiry/exercise.

#### TC-C4-SAGA-008 · CHECK_STATUS resume of an interrupted exercise (PARTIAL)
- **Feature:** CHECK_STATUS resume · **Spec:** Celina 4 SAGA Napomena 2 (recognize reached step) · **Existing test:** test-app/workflows/saga_sg_test.go (retry-after-fault proves resumability)
- **Actor:** system
- **Expected:** **PARTIAL.** After a crash mid-saga the saga-log lets recovery recognize the reached step
  and either forward-resume or compensate; locally this is proven by the force-fail-then-clean-retry
  tests (SG-05/SG-07). The cross-bank CHECK_STATUS wire message belongs to Celina 5. No dedicated REST
  endpoint to query a local exercise saga's step — mark partial.

---

## 5. Option / Premium Tax (`TC-C4-OTCTAX-*`)

Resolution-month model (§21, lines 2568-2575). Seller premium taxed at accept; buyer taxed at
resolution (exercise OR expiry); bank/aktuar exempt → goes to Profit Banke.

#### TC-C4-OTCTAX-001 · Seller premium taxed at accept (15% for clients) (POSITIVE)
- **Feature:** Porez na premiju — prodavac, pri accept · **Spec:** §21 "Seller (writer) — at accept" · **Existing test:** test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Actor:** client seller
- **Preconditions:** a chain accepted (TC-C4-OTCNEG-004).
- **Expected:** at accept, one `SecurityType=option`, `OTC=true`, `+premium` tax row written for the seller in the accept month, currency = `PremiumCurrency`. On the monthly tax run the seller pays 15% of the premium income.
- **Verification:** assert via the tax-tracking portal (Celina 3) / monthly tax collection that the seller has a +premium gain row.

#### TC-C4-OTCTAX-002 · Buyer taxed at exercise: (market−strike)×qty − premium (POSITIVE)
- **Feature:** Porez na opciju — kupac, pri izvršenju · **Spec:** §21 "Buyer — at resolution / On exercise" · **Existing test:** test-app/workflows/wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle
- **Actor:** client buyer
- **Preconditions:** profitable exercise (TC-C4-SAGA-001), market price snapshotted pre-saga.
- **Expected:** buyer tax row `TotalGain = (market − strike) × qty − premium` in the exercise month; buyer holding credited at **market** cost basis (prevents double taxation on later sale). Row may be negative when premium > bargain → reduces monthly gain. Best-effort: skipped (basis kept at strike) when market price unknown — never blocks exercise.
- **Negative siblings:** market unknown → no buyer tax row, exercise still succeeds.

#### TC-C4-OTCTAX-003 · Expired option: buyer's premium loss reduces month's gains; seller adds nothing (POSITIVE)
- **Feature:** Istekla opcija — porez · **Spec:** §21 "On expiry" · **Existing test:** —
- **Actor:** system (OTCExpiryCron)
- **Preconditions:** ACTIVE contract expires unexercised.
- **Expected:** buyer `−premium` loss row written in the expiry month (idempotent key `expire-contract-<id>-buyer-premium-loss`); the seller adds nothing (already taxed at accept). Net: buyer's monthly taxable gain is reduced by the premium.

#### TC-C4-OTCTAX-004 · Bank / aktuar premium & exercise gains are exempt → Profit Banke (POSITIVE)
- **Feature:** Izuzeće banke (aktuar u ime banke) · **Spec:** §21 "Bank (Profit Banke) exemption"; Celina 4 (aktuari ne plaćaju 15%) · **Existing test:** —
- **Actor:** employee (actuary) acting as the bank
- **Expected:** when the buyer/seller is `owner_type='bank'`, **no** capital-gains tax row is collected
  (`ListOwnersWithGains` filters to `owner_type='client'`); the gain stays with the bank and surfaces in
  the actuary performance / Profit Banke views (TC-C4-PROFIT-001).
- **Negative siblings:** a client in the same trade is still taxed (only the bank leg is exempt).

---

## 6. Investment Funds — lifecycle (`TC-C4-FUND-*`)

#### TC-C4-FUND-001 · Supervisor creates a fund (unique name, min deposit, manager, auto RSD account) (POSITIVE)
- **Feature:** Create investment fund · **Spec:** Celina 4 "Create investment fund page"; §24 · **Existing test:** test-app/workflows/investment_funds_test.go::TestInvestmentFunds_CreateAndList
- **Actor:** supervisor/admin (`funds.manage.catalog`)
- **Request:** `POST /api/v3/investment-funds`
  - Body: `{"name":"Alpha Growth Fund","description":"IT sektor","minimum_contribution_rsd":"1000.00","dividend_mode":"payout"}`
- **Expected:** `201` · `fund` with `manager_employee_id` = creating supervisor, an auto-provisioned bank-owned RSD account (`rsd_account_number`), `active=true`, `dividend_mode=payout`. Side-effect: account-service creates the fund RSD account; `stock.fund-created` Kafka.
- **Negative siblings:** missing `name` → 400; duplicate name → 409 `conflict` (investment_funds_test.go::TestInvestmentFunds_DuplicateNameRejected); invalid `dividend_mode` (not payout/reinvest) → 400; client/agent caller → 403 `forbidden` (investment_funds_test.go::TestInvestmentFunds_ClientCannotCreate); unauthenticated → 401.

#### TC-C4-FUND-002 · Discovery page: list/filter/sort funds (POSITIVE)
- **Feature:** Discovery page + filtriranje/sortiranje · **Spec:** Celina 4 "Discovery page"; TODO_final "Statistika fondova" · **Existing test:** test-app/workflows/investment_funds_test.go::TestInvestmentFunds_CreateAndList, wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** any authenticated (clients + actuaries)
- **Request:** `GET /api/v3/investment-funds?search=Alpha&active_only=true&sort_by=annualized_return&sort_order=desc`
- **Expected:** `200` · `funds[]` each with name/description/value_rsd/profit_rsd/minimum_contribution_rsd plus the statistics columns (annualized_return_pct, volatility_pct, reward_to_variability, max_drawdown_pct, metrics_available).
- **Negative siblings:** invalid `sort_by` (not in the allowed set) → 400; invalid `sort_order` → 400.

#### TC-C4-FUND-003 · Detailed fund view (NAV, liquidity, holdings, profit, history) (POSITIVE)
- **Feature:** Detaljan prikaz fonda · **Spec:** Celina 4 "Detaljan prikaz fonda" · **Existing test:** test-app/workflows/wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** any authenticated
- **Request:** `GET /api/v3/investment-funds/:id`
- **Expected:** `200` · fund + `holdings[]` (with `current_value_rsd`), `investor_count`, `total_contributed_rsd`, `liquid_rsd_balance`, `total_holdings_value_rsd`, `total_value_rsd` (VrednostFonda), `profit_rsd`/`profit_pct`, metrics + `history[]`/`average_history[]` charts.
- **Negative siblings:** unknown fund id → 404; non-numeric id → 400.

#### TC-C4-FUND-004 · Update a fund (supervisor only) (POSITIVE)
- **Feature:** Update fund · **Spec:** §24 UpdateFund · **Existing test:** —
- **Actor:** supervisor/admin (`funds.manage.catalog`)
- **Request:** `PUT /api/v3/investment-funds/:id` Body `{"minimum_contribution_rsd":"2000.00","dividend_mode":"reinvest"}`
- **Expected:** `200` · updated fields persisted (omitted fields unchanged); `stock.fund-updated` Kafka.
- **Negative siblings:** invalid `dividend_mode` → 400; client/agent → 403; unknown id → 404.

#### TC-C4-FUND-005 · Client invests ≥ min from own account; position created (POSITIVE)
- **Feature:** Klijent ulaže u aktivni fond · **Spec:** Celina 4 (proveriti minimumContribution); §24 Invest saga; E2E "Klijent ulaže u aktivni fond" · **Existing test:** test-app/workflows/investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient (position-shape axis)
- **Actor:** client
- **Preconditions:** fund min = 1000 RSD; client owns a funded RSD account.
- **Request:** `POST /api/v3/investment-funds/:id/invest`
  - Body: `{"source_account_id":<A>,"amount":"2000","currency":"RSD","on_behalf_of_type":"self"}`
- **Expected:** `201` · `contribution` status `completed`; client account − 2000; fund `liquid_rsd` + 2000; a `ClientFundPosition` upserted (`TotalContributedRSD` += 2000); `GET /api/v3/me/investment-funds` shows the position with `pct_of_fund` and `TrenutnaVrednostPozicije`. Side-effect: `stock.fund-invested` Kafka (owner_type=client).
- **Negative siblings:** amount < minimum_contribution_rsd → 409/400 (reject below min — E2E "Odbijanje uplate ispod minimalnog iznosa"); `source_account_id`/`amount`/`currency` missing → 400; account not owned → 403; insufficient funds → 409; investing into an inactive fund → 409.

#### TC-C4-FUND-006 · Cross-currency invest converts before debit (POSITIVE)
- **Feature:** Invest u valuti ≠ RSD · **Spec:** §24 "Cross-currency invest converts via exchange-service" · **Existing test:** —
- **Actor:** client with EUR account
- **Request:** `POST /api/v3/investment-funds/:id/invest` Body `{"source_account_id":<eurAcct>,"amount":"100","currency":"EUR","on_behalf_of_type":"self"}`
- **Expected:** `201` · EUR debited, converted to RSD via `exchange-service.Convert`, fund RSD credited the converted amount; position in RSD.
- **Negative siblings:** no rate for the pair → error surfaced; currency not matching account → 400/409.

#### TC-C4-FUND-007 · Client redeem — full and partial; chosen target account; fee applies (POSITIVE)
- **Feature:** Povlačenje novca iz fonda · **Spec:** Celina 4 ClientFundRedemption; §24 Redeem saga; Napomena 4 (klijent plaća proviziju) · **Existing test:** —
- **Actor:** client (holds a position)
- **Request:** `POST /api/v3/investment-funds/:id/redeem`
  - Body: `{"amount_rsd":"1000","target_account_id":<A>,"on_behalf_of_type":"self"}`
- **Expected:** `201` · fund cash − (amount + 0.5% fee); target account + amount; **`fund_redemption_fee_pct=0.5%` charged to the client** and credited to the bank; `ClientFundPosition.TotalContributedRSD` decremented; position recomputed. Full redemption (amount = whole position value) zeroes the position. Side-effect: `stock.fund-redeemed` Kafka.
- **Negative siblings:** `amount_rsd`/`target_account_id` missing → 400; target not owned → 403; redeeming more than the position is worth → 409; fund cash short → see TC-C4-FUND-010.

#### TC-C4-FUND-008 · Supervisor invests on behalf of the bank — no conversion fee (POSITIVE)
- **Feature:** Supervizor uplaćuje u fond u ime banke · **Spec:** Celina 4 (Uplata u ime banke); Napomena 4 (banka ne plaća proviziju); E2E "Supervizor uplaćuje u fond u ime banke" · **Existing test:** —
- **Actor:** supervisor acting as bank
- **Request:** `POST /api/v3/investment-funds/:id/invest`
  - Body: `{"source_account_id":<bankAcct>,"amount":"100000","currency":"RSD","on_behalf_of_type":"bank"}`
- **Expected:** `201` · bank account − 100000; fund value + 100000; a bank `ClientFundPosition` (`owner_type=bank`, `owner_id NULL`). On bank **redeem**, `fund_redemption_fee_pct` = 0 (no conversion fee). Visible in Profit Banke → Pozicije u fondovima (TC-C4-PROFIT-002).
- **Negative siblings:** supervisor selects a client account for `on_behalf_of_type=bank` → 403/400 (must be a bank account); agent without `funds` permission acting as bank → 403.

#### TC-C4-FUND-009 · Moji fondovi: client vs supervisor views (POSITIVE)
- **Feature:** Moj portfolio → Moji fondovi · **Spec:** Celina 4 "Dodatak za: portal Moj portfolio" · **Existing test:** test-app/workflows/investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient
- **Actor:** client (positions held) / supervisor (funds managed)
- **Request:** `GET /api/v3/me/investment-funds`
- **Expected:** client → `positions[]` with udeo (pct + money), profit = (money udeo − uložen iznos); fresh client → `[]`. Supervisor view surfaces managed funds with value + liquidity (manager-scoped).
- **Negative siblings:** unauthenticated → 401.

#### TC-C4-FUND-010 · Redeem when fund cash short — partial liquidation + deferred-payout notice (NO-ENDPOINT/PARTIAL)
- **Feature:** Delimična likvidacija zbog nedovoljne likvidnosti · **Spec:** Celina 4 Napomena (automatska likvidacija + obaveštenje); §24 Napomena 3; E2E "Delimična likvidacija sredstava" · **Existing test:** —
- **Actor:** client requesting more than fund liquid cash
- **Request:** `POST /api/v3/investment-funds/:id/redeem` Body `{"amount_rsd":"50000",...}` while fund liquid = 20000
- **Expected:** **NO-ENDPOINT (follow-up).** Per §24 the liquidation sub-saga (FIFO sell securities to
  free cash + "obaveštenje o odloženoj isplati") is a documented follow-up (Tasks 16-17). Today the
  redeem saga returns `ErrInsufficientFundCash` → **409 `business_rule_violation`** when cash is short.
  Test asserts the 409 today and flags the auto-liquidation+notify path as a coverage gap.

#### TC-C4-FUND-011 · Block deposit while a withdrawal is pending (NO-ENDPOINT/PARTIAL)
- **Feature:** Blokirati uplatu ako je isplata na čekanju · **Spec:** E2E "Blokirati uplatu ako je isplata na čekanju" · **Existing test:** —
- **Actor:** client with a pending redemption
- **Expected:** **NO-ENDPOINT (follow-up).** A "pending withdrawal" state only exists once the liquidation
  sub-saga (TC-C4-FUND-010) lands; redeem currently completes synchronously or 409s. The block-deposit
  guard has no endpoint yet — mark as a gap tied to the liquidation follow-up.

#### TC-C4-FUND-012 · Position/NAV/Profit recompute after asset value change (POSITIVE)
- **Feature:** Preračunavanje pozicije nakon promene vrednosti imovine · **Spec:** Celina 4 (ProcenatFonda/TrenutnaVrednostPozicije izvedeni); E2E "Preračunavanje pozicije u fondu" · **Existing test:** test-app/workflows/wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** client holding 10% of a fund whose holdings drop 20%
- **Request:** `GET /api/v3/me/investment-funds` (and `GET /api/v3/investment-funds/:id`)
- **Expected:** `TrenutnaVrednostPozicije` and `profit` reflect the new mark-to-market NAV (Σ fund_holding.qty × current price); derived `pct_of_fund` unchanged by price moves alone but money value follows. No DB write needed — values computed on read.

#### TC-C4-FUND-013 · Supervisor buys a security on behalf of a fund (sufficient fund cash) (POSITIVE)
- **Feature:** Dodatak za "Hartije od vrednosti" — kupovina za fond · **Spec:** Celina 4 (izaberu fond, proveriti dovoljno novca); §24 Order.FundID · **Existing test:** —
- **Actor:** supervisor (fund manager)
- **Request:** `POST /api/v3/me/orders` Body `{"listing_id":<L>,"order_type":"market","direction":"buy","quantity":N,"account_id":<fundRsdAcct>,"on_behalf_of":{"type":"fund","fund_id":<F>}}`
- **Expected:** `201` · fills credit `fund_holdings` (not the supervisor's holdings); fund RSD cash debited; fund value/holdings recompute. Spent funds count toward the actuary's daily limit (TODO_final DCA note carries over).
- **Negative siblings:** fund cash insufficient → 409; caller not the fund's manager → 403; on_behalf_of fund + non-fund account → 400.

#### TC-C4-FUND-014 · Fund ownership transfer when supervisor permission removed (POSITIVE, cross-service)
- **Feature:** Ako admin ukloni isSupervisor → fondovi prelaze adminu · **Spec:** Celina 4 "Dodatak za: Upravljanje zaposlenima"; §24 outbox flow · **Existing test:** —
- **Actor:** admin removing `funds.manage`/`isSupervisor` from a fund-managing supervisor (Celina-1 employee permission route)
- **Request:** `PUT /api/v3/employees/:id/permissions` (drop the supervisor permission)
- **Expected:** user-service writes `user.supervisor-demoted` to its outbox → relay → stock-service `SupervisorDemotedConsumer` reassigns every fund managed by the demoted supervisor to the demoting admin in one TX → `stock.funds-reassigned` Kafka. Assert `manager_employee_id` flips to the admin on `GET /api/v3/investment-funds/:id`.
- **Negative siblings:** removing permission from a supervisor managing no funds → no reassignment, no error.

#### TC-C4-FUND-015 · Recurring fund investment (monthly DCA into a fund) (POSITIVE)
- **Feature:** RecurringFund (DCA u fond) · **Spec:** §24; TODO_final DCA · **Existing test:** —
- **Actor:** client
- **Request:** `POST /api/v3/me/recurring-funds` Body `{"fund_id":<F>,"amount_rsd":"5000","source_account_id":<A>,"day_of_month":1}`
- **Expected:** `201` · template created; cron places a fund investment on `day_of_month`; insufficient funds → skipped + client notified (parity with missed loan installment).
- **Negative siblings:** `day_of_month` <1 or >28 → 400; missing fund_id/source_account_id/amount_rsd → 400; employee (non-client) caller → 403 ("only clients can create recurring fund investments"); pause/resume/cancel transitions (`POST .../pause`, `.../resume`, `DELETE .../:id`).

---

## 7. Fund Dividends — auto-inflow + reinvest + distribute (`TC-C4-FUNDIV-*`)

Must be consistent with Celina 3 dividend payout. A fund holding a dividend-paying stock receives the
dividend automatically; per `dividend_mode` it is either distributed (cash) or reinvested (DRIP).

#### TC-C4-FUNDIV-001 · Declare a dividend (admin) (POSITIVE)
- **Feature:** Declare dividend · **Spec:** §24 DividendPayment · **Existing test:** —
- **Actor:** admin/supervisor (`securities.manage.catalog`)
- **Request:** `POST /api/v3/admin/dividends` Body `{"security_id":<S>,"ticker":"AAPL","amount_per_share_rsd":"12.50","payment_date":"2026-06-15"}`
- **Expected:** `201` · `dividend_payment` status `declared`; idempotent on `(security_id, payment_date)`.
- **Negative siblings:** missing security_id/ticker/amount/payment_date → 400; non-admin → 403; duplicate (security_id, payment_date) → idempotent (same row, no dup).

#### TC-C4-FUNDIV-002 · Payout fans out to holders incl. funds; client 15% tax, fund/bank exempt (POSITIVE)
- **Feature:** Automatski priliv dividendi u fond + Celina-3 isplata · **Spec:** Celina 4 "Raspodela dividendi u fondovima"; §24 DividendPayout · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** admin
- **Preconditions:** the security is held by a client, the bank, and ≥1 fund.
- **Request:** `POST /api/v3/admin/dividends/:id/payout`
- **Expected:** `200` · `payouts_created`, `fund_payouts`, `total_amount_rsd`. Side-effects: each client holder credited `qty × amount_per_share` with `tax_amount_rsd` = 15%; bank-held and **fund-held** payouts have `tax=0`; funds receive the dividend into their RSD account (auto-inflow); `FundDividendPayment` snapshot written. Idempotent (`idempotency_key` UNIQUE).
- **Negative siblings:** payout twice → idempotent no double-credit; non-admin → 403.

#### TC-C4-FUNDIV-003 · Reinvest mode (DRIP) buys more shares for the fund (POSITIVE)
- **Feature:** Reinvestiranje dividendi · **Spec:** Celina 4 (sistem automatski kupuje nove hartije); §24 `dividend_mode=reinvest` · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** admin payout against a fund with `dividend_mode=reinvest`
- **Expected:** the fund's dividend cash is used to buy `floor(grossRSD / priceRSD)` more shares of the dividend-paying stock on behalf of the fund (best-effort — cash retained on failure). `fund_holdings` increases; remainder stays as cash.
- **Negative siblings:** DRIP buy fails → dividend retained as cash (no crash); `payout` mode → cash left in fund + distributed per investor share.

#### TC-C4-FUNDIV-004 · Distribute mode: investors credited proportional to fund share (POSITIVE)
- **Feature:** Isplata dividendi klijentima proporcionalno udelu · **Spec:** Celina 4 · **Existing test:** test-app/workflows/wf_fund_dividend_mode_test.go::TestWF_FundDividendMode
- **Actor:** admin payout against a fund with `dividend_mode=payout`
- **Expected:** the `per_investor_snapshot` records each investor's share at payout time; `dividends_received_rsd` surfaces in portfolio positions. Sum of distributed = fund dividend gross.
- **Negative siblings:** —

#### TC-C4-FUNDIV-005 · Fund dividend history & my-dividend history (POSITIVE)
- **Feature:** Dividend history · **Spec:** §24; dividend_handler · **Existing test:** —
- **Actor:** fund manager / client
- **Request:** `GET /api/v3/investment-funds/:id/dividends` and `GET /api/v3/me/dividends`
- **Expected:** `200` · paginated `payments[]` (fund-level) / `payouts[]` (caller's holdings), most-recent first.
- **Negative siblings:** bad fund id → 400; unauthenticated `/me/dividends` → 401.

---

## 8. Fund Statistics (Discovery metrics) (`TC-C4-FUNSTAT-*`)

#### TC-C4-FUNSTAT-001 · Statistics surface on discovery once min-snapshots reached (POSITIVE)
- **Feature:** Statistika fondova (annual return / RtV / max drawdown / volatility) · **Spec:** Celina 4 / TODO_final "Statistika fondova" · **Existing test:** test-app/workflows/wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** any authenticated
- **Request:** `GET /api/v3/investment-funds?sort_by=reward_to_variability&sort_order=desc`
- **Expected:** `200` · each fund carries `annualized_return_pct`, `volatility_pct`, `reward_to_variability`, `max_drawdown_pct`, `metrics_available`. Metrics computed from `FundValueSnapshot` rows (std-dev/Sharpe on monthly-resampled returns; drawdown on daily series).
- **Negative siblings:** invalid `sort_by`/`sort_order` → 400.

#### TC-C4-FUNSTAT-002 · Min-snapshots gate: metrics_available=false until ≥2 monthly returns (NEGATIVE/boundary)
- **Feature:** Minimalan broj snimaka pre prikaza metrika · **Spec:** Celina 4 (metrike imaju smisla tek uz dovoljno istorijskih podataka); §24 `FUND_METRICS_MIN_MONTHLY_RETURNS=2` · **Existing test:** test-app/workflows/wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** any authenticated
- **Request:** `GET /api/v3/investment-funds/:id` on a fresh fund (< 2 monthly returns)
- **Expected:** `metrics_available=false`; the four metrics are omitted/zero and not shown until ≥ `FUND_METRICS_MIN_MONTHLY_RETURNS` (default 2). Boundary: at exactly 2 monthly returns → `metrics_available=true`.

#### TC-C4-FUNSTAT-003 · Detail charts: fund history + average-of-all-funds comparison (POSITIVE)
- **Feature:** Grafikon istorijske vrednosti + uporedni sa prosekom svih fondova · **Spec:** Celina 4 · **Existing test:** test-app/workflows/wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort
- **Actor:** any authenticated
- **Request:** `GET /api/v3/investment-funds/:id`
- **Expected:** `history[]` (this fund's NAV series) and `average_history[]` (system-wide average) returned; both `[]` until snapshots exist.

---

## 9. Portal: Profit Banke (`TC-C4-PROFIT-*`)

Supervisor-only. All values in RSD.

#### TC-C4-PROFIT-001 · Actuary Performances board (POSITIVE + RBAC)
- **Feature:** Profit aktuara · **Spec:** Celina 4 "Profit aktuara" · **Existing test:** —
- **Actor:** supervisor/admin (`actuaries.read.all`)
- **Request:** `GET /api/v3/actuaries/performance`
- **Expected:** `200` · `actuaries[]` each with name + realised profit (RSD) from trading on behalf of the bank (option premiums, exercise gains, dividends, stock — all bank-exempt from tax, so they accrue as bank profit).
- **Negative siblings:** agent/client without `actuaries.read.all` → 403; unauthenticated → 401.

#### TC-C4-PROFIT-002 · Bank positions in funds (POSITIVE + RBAC)
- **Feature:** Pozicije u fondovima · **Spec:** Celina 4 "Pozicije u fondovima"; §24 `funds.read.all` · **Existing test:** —
- **Actor:** supervisor/admin (`funds.read.all`)
- **Request:** `GET /api/v3/investment-funds/positions`
- **Expected:** `200` · `positions[]` of funds where the bank holds a stake, each with fund name, manager, bank udeo (pct + RSD), realised profit (RSD).
- **Negative siblings:** missing `funds.read.all` → 403.

#### TC-C4-PROFIT-003 · Deposit/withdraw to a fund as the bank (POSITIVE)
- **Feature:** Uplata/Povlačenje u ime banke iz Profit Banke · **Spec:** Celina 4 "Pozicije u fondovima" akcije · **Existing test:** —
- **Actor:** supervisor
- **Request:** `POST /api/v3/investment-funds/:id/invest` (`on_behalf_of_type=bank`) and `POST .../redeem` (`on_behalf_of_type=bank`)
- **Expected:** same as TC-C4-FUND-008 — bank account chosen for deposit; chosen bank account for redeem; **no conversion fee** on bank redeem. Bank position recomputed.
- **Negative siblings:** non-bank account chosen → 403/400.

---

## 10. OTC Notifications & Negotiation History (TODO_final) (`TC-C4-OTCNOTE-*`)

#### TC-C4-OTCNOTE-001 · Counter-offer received → email notification (POSITIVE)
- **Feature:** Obaveštenje kada druga strana pošalje kontraponudu · **Spec:** TODO_final Celina-4 OTC notif · **Existing test:** —
- **Actor:** the party who receives the counter
- **Trigger:** TC-C4-OTCNEG-003 counter
- **Expected:** `notification.send-email` (+ in-app inbox) to the opposite party; `otc.offer-countered` Kafka. Assert the Kafka event AND the recipient's inbox item. (Detailed notification matrix also in `todo-final-notifications-and-mobile.md`.)
- **Negative siblings:** no notification to the actor who made the counter (self-action).

#### TC-C4-OTCNOTE-002 · Offer accepted / withdrawn → email notification (POSITIVE)
- **Feature:** Obaveštenje kada druga strana prihvati ili odustane · **Spec:** TODO_final · **Existing test:** —
- **Actor:** the counterparty
- **Trigger:** accept (TC-C4-OTCNEG-004) / withdraw (TC-C4-OTCNEG-005)
- **Expected:** accept → `otc.contract-created` + email to both; withdraw/cancel → cascade-cancel notification to the poster. Assert events + inbox.
- **Negative siblings:** rejecting (not accepting) → reject notification, parent stays open.

#### TC-C4-OTCNOTE-003 · Contract expiring in N days reminder (POSITIVE/PARTIAL)
- **Feature:** Obaveštenje kada opcioni ugovor ističe za N dana (npr. 3 dana pre) · **Spec:** TODO_final · **Existing test:** —
- **Actor:** system cron
- **Expected:** **PARTIAL** — verify whether a pre-expiry reminder cron emits "ugovor ističe za N dana"
  notifications (N=3) before `settlement_date`. The expiry cron itself (`otc.contract-expired`) fires at
  settlement; if the N-days-before reminder is not wired, mark NO-ENDPOINT and flag as a gap.

#### TC-C4-OTCNOTE-004 · Negotiation history page with full counter-offer history + filters (POSITIVE)
- **Feature:** Istorija pregovora (stare/nove vrednosti, timestamp, ko je izvršio izmenu; filteri status/datum/druga strana) · **Spec:** TODO_final "Istorija pregovora" · **Existing test:** test-app/workflows/otc_unified_read_test.go (negotiation read shapes)
- **Actor:** client
- **Request:** `GET /api/v3/me/otc/history?status=ACCEPTED&status=REJECTED&since=2026-01-01&until=2026-12-31&counterparty_id=<C>`
- **Expected:** `200` · terminal negotiations (`ACCEPTED|REJECTED|EXPIRED|FAILED`) filtered by status (repeatable), date range, and counterparty; each links to its full revision chain via `GET /api/v3/me/otc/options/negotiations/:nid/revisions` (old→new values + `ModifiedBy` + timestamp per `OTCOfferRevision`).
- **Negative siblings:** invalid `status` value → 400; `since` not YYYY-MM-DD → 400; `until` not YYYY-MM-DD → 400; `since > until` → 400.

---

## 11. Defense flow (odbrana) end-to-end scenarios

#### TC-C4-E2E-001 · Provera 1 — OTC trade internal (full chain) (POSITIVE)
- **Feature:** odbrana §4 Provera 1 — OTC trade internal · **Spec:** odbrana flow · **Existing test:** test-app/workflows/wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers
- **Actor:** Klijent 1 (buyer) + Klijent 2 (seller), same bank
- **Chain:** Klijent 2 lists shares (TC-C4-OTCNEG-001) → Klijent 1 bids (TC-C4-OTCNEG-002) → Klijent 2 counters, **not offering more than available shares** (TC-C4-OTCNEG-003) → Klijent 1 accepts the counter (TC-C4-OTCNEG-004 — contract minted, premium moves) → Klijent 1 opens Sklopljeni ugovori and **Iskoristi** (TC-C4-SAGA-001).
- **Expected:** assert (per odbrana checklist): money debited from Klijent 1 (strike), shares assigned to Klijent 1, Klijent 2 no longer holds those shares. Counter cannot exceed Klijent 2's available shares (412 if attempted).

> **Provera 2 — OTC trade external (supervisor↔supervisor across banks):** identical chain, cross-bank.
> Covered in `celina-5-cross-bank.md` (same unified routes dispatch remote by listing routing).

---

## 12. Field-validation matrices

### 12.1 OTC stock offer (`POST /api/v3/me/otc/stocks` — `createOTCStockOfferRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `direction` | `"sell"` / `"buy"` | other value → 400 `validation_error`; missing → 400 |
| `holding_id` (sell) | `42` | missing on sell → 400; not owned by caller → 403 |
| `listing_id` (buy) | `7` | missing on buy → 400 |
| `quantity` | `10` | `0`/negative → 400 `validation_error`; > shares owned (sell-fill) → 412 |
| `price_per_unit` | `"150.00"` | missing (sell or buy) → 400 |
| `buyer_account_id` (buy) | `3` | missing on buy → 400; not owned → 403; underfunded → 409 |

### 12.2 OTC option listing (`POST /api/v3/me/otc/options` — `createOTCOfferRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `direction` | `"sell_initiated"`/`"buy_initiated"` | other → 400; missing → 400 |
| `ticker` | `"AAPL"` | empty → 400; unknown ticker → 400 `validation_error` |
| `quantity` | `"100"` | empty → 400; non-positive → 400 |
| `strike_price` | `"5000"` | empty → 400 |
| `premium` | `"50000"` | negative → 400 (downstream) |
| `settlement_date` | `"2030-04-05"` | empty → 400; past date → cannot counter/exercise (409 later) |
| `account_id` | `12` | not owned → 403; foreign acct not owned → 403 |
| `on_behalf_of_client_id` | `55` | employee without `otc.trade.on_behalf` → 403 |

### 12.3 OTC negotiation bid/counter (`openNegotiationRequest` / `counterNegotiationRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `bidder_account_id` (bid) | `12` | missing → 400; not owned → 403 |
| `quantity` | `"100"` | empty → 400; non-positive → 400 (`positiveDecimalString`) |
| `strike_price` | `"4800"` | empty → 400; non-positive → 400 |
| `premium` | `"48000"` | negative → 400 (`nonNegativeDecimalString`); empty allowed (treated as unset) |
| `settlement_date` | `"2030-04-10"` | empty → 400; past → counter rejected 409 |
| (chain state) | — | counter/accept on terminal chain → 409; accept by proposer of current terms → 403; second accept on consumed parent → 409 |

### 12.4 Accept / exercise (`acceptNegotiationRequest` / `exerciseRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `acceptor_account_id` (accept) | `12` | missing → 400; not owned → 403; saga reject (short premium/shares) → 412 |
| `on_behalf_of_fund_id` (accept) | `9` | caller not the fund's manager → 403; acceptor acct ≠ fund RSD acct → 400 |
| `buyer_account_number` (exercise, cross-bank only) | `"111…"` | not owned → 403; local contract ignores it |
| `on_behalf_of_fund_id` (exercise) | `9` | caller not fund manager → 403 |
| (contract state) | — | non-buyer → 403; unknown → 404; expired/exercised → 409 |

### 12.5 InvestmentFund (`createFundRequest` / `updateFundRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `name` | `"Alpha Growth Fund"` | empty → 400; duplicate → 409 `conflict` |
| `description` | `"IT sektor"` | (free text) |
| `minimum_contribution_rsd` | `"1000.00"` | non-numeric → downstream 400; (min-enforced on invest) |
| `dividend_mode` | `"payout"`/`"reinvest"` | other → 400 `validation_error` |
| (authz) | supervisor/admin token | client/agent → 403; unauthenticated → 401 |

### 12.6 Fund invest / redeem (`investRequest` / `redeemRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `source_account_id` (invest) | `12` | missing → 400; not owned → 403; underfunded → 409 |
| `amount` (invest) | `"2000"` | missing → 400; < `minimum_contribution_rsd` → 409/400 |
| `currency` (invest) | `"RSD"` | missing → 400; ≠ account currency → 400/409 |
| `amount_rsd` (redeem) | `"1000"` | missing → 400; > position value → 409; fund cash short → 409 (`ErrInsufficientFundCash`) |
| `target_account_id` (redeem) | `3` | missing → 400; not owned → 403 |
| `on_behalf_of_type` | `"self"`/`"bank"` | bank + non-bank account → 403/400; (default `self`) |

### 12.7 Dividend (`declareDividendRequest`) & recurring fund (`createRecurringFundRequest`)

| Field | Valid example | Invalid forms → expected |
|---|---|---|
| `security_id` | `5` | `0`/missing → 400 |
| `ticker` | `"AAPL"` | empty → 400 |
| `amount_per_share_rsd` | `"12.50"` | empty → 400 |
| `payment_date` | `"2026-06-15"` | empty → 400 |
| (authz declare/payout) | `securities.manage.catalog` | non-admin → 403 |
| `fund_id`/`source_account_id`/`amount_rsd` (recurring) | `9`/`12`/`"5000"` | missing → 400 |
| `day_of_month` (recurring) | `1` | <1 or >28 → 400; employee caller → 403 |

---

## Coverage rows

```
| feature | TC IDs | existing Go test | status |
| OTC stocks — publish sell offer (make public) | TC-C4-OTCSTK-001 | otc_test.go::TestOTC_ListOffers | covered |
| OTC stocks — standing buy offer + cash reservation | TC-C4-OTCSTK-002 | otc_test.go::TestOTC_BuyOffer_MissingAccountID, TestOTC_BuyOffer_InvalidQuantity | covered |
| OTC stocks — browse marketplace + filter | TC-C4-OTCSTK-003 | otc_test.go::TestOTC_ListOffers, TestOTC_ListOffers_FilterBySecurityType, TestOTC_ListOffers_Unauthenticated, TestOTC_ListOffers_InvalidSecurityType | covered |
| OTC stocks — buy from sell offer | TC-C4-OTCSTK-004 | otc_test.go::TestOTC_BuyOffer_InvalidQuantity | covered |
| OTC stocks — sell into buy offer (shares-available guard) | TC-C4-OTCSTK-005 | — | covered |
| OTC stocks — cancel offer (sell/buy) | TC-C4-OTCSTK-006 | — | covered |
| OTC stocks — buy on behalf of client | TC-C4-OTCSTK-007 | — | covered |
| OTC stocks — list my offers | TC-C4-OTCSTK-008 | — | covered |
| OTC options — post listing (sell/buy initiated) | TC-C4-OTCNEG-001 | otc_options_test.go::TestOTCOptions_ClientLifecycle, TestOTCOptions_UnknownTickerRejected, TestOTCOptions_ClientCannotUseForeignAccount | covered |
| OTC options — bid (open negotiation chain) | TC-C4-OTCNEG-002 | otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — counter (each field mutable + old→new history) | TC-C4-OTCNEG-003 | otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — accept (first-accept-wins, cascade-cancel) | TC-C4-OTCNEG-004 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers, otc_sp2b_test.go::TestSP2b_UnifiedLocalLifecycle_BidCounterAcceptForms | covered |
| OTC options — withdraw own chain | TC-C4-OTCNEG-005 | — | covered |
| OTC options — reject (parent stays open) | TC-C4-OTCNEG-006 | — | covered |
| OTC options — cancel listing cascade | TC-C4-OTCNEG-007 | — | covered |
| OTC options — active-offers page | TC-C4-OTCNEG-008 | otc_unified_read_test.go::TestSP1_MyNegotiations_HasProvenanceFields, TestSP2b_OfferList_StampsMyNegotiationForBidder | covered |
| OTC options — poster sees all bids / bidder forbidden | TC-C4-OTCNEG-009 | otc_timeline_test.go::TestOTCTimeline_PosterAllowed_BidderForbidden | covered |
| OTC options — concluded-contracts page | TC-C4-OTCNEG-010 | otc_options_test.go::TestOTCOptions_ListMyContractsEmpty, otc_unified_read_test.go::TestSP1_MyContracts_BuyerIsOwner | covered |
| OTC options — deviation color bands ±5/±20% | TC-C4-OTCNEG-011 | — | NO-ENDPOINT |
| OTC options — unread-negotiations indicator | TC-C4-OTCNEG-012 | — | partial |
| OTC contract — accept moves premium + locks seller shares | TC-C4-OTCCON-001 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers | covered |
| OTC contract — cross-currency premium conversion | TC-C4-OTCCON-002 | — | covered |
| OTC contract — multiple per seller, Σ committed ≤ owned | TC-C4-OTCCON-003 | — | covered |
| OTC contract — expired unused frees shares | TC-C4-OTCCON-004 | — | covered |
| OTC contract — get by id + ownership | TC-C4-OTCCON-005 | otc_unified_read_test.go::TestSP1_GetOffer_LocalKindAndMeOwner | covered |
| SAGA exercise — happy path | TC-C4-SAGA-001 | saga_sg_test.go::TestSG01_HappyPath, wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| SAGA exercise — cannot exercise expired/used | TC-C4-SAGA-002 | — | covered |
| SAGA exercise — non-buyer/unknown rejected | TC-C4-SAGA-003 | saga_sg_test.go::TestSG02a_NonBuyerRejected, TestSG02b_UnknownContract | covered |
| SAGA exercise — phase 3/4 failure full compensation ("Poništena") | TC-C4-SAGA-004 | saga_sg_test.go::TestSG05_ForceFailCreditSeller_CompensatesAndRetrySucceeds, TestSG07_ForceFailMarkExercised_FullCompensationAndRetrySucceeds | covered |
| SAGA exercise — refund retry ≤3 then admin alert | TC-C4-SAGA-005 | — | partial |
| SAGA exercise — double-reserve prevention (concurrent) | TC-C4-SAGA-006 | saga_sg_test.go (invariants), wf_stock_concurrent_orders_test.go | covered |
| SAGA exercise — funds consumed before exec → cancel+refund | TC-C4-SAGA-007 | — | covered |
| SAGA exercise — CHECK_STATUS resume | TC-C4-SAGA-008 | saga_sg_test.go (retry-after-fault) | partial |
| Option tax — seller premium at accept (15% client) | TC-C4-OTCTAX-001 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| Option tax — buyer at exercise (market−strike)×qty−premium | TC-C4-OTCTAX-002 | wf_option_tax_test.go::TestWF_OptionExerciseTaxCycle | covered |
| Option tax — expired: buyer premium loss, seller none | TC-C4-OTCTAX-003 | — | covered |
| Option tax — bank/aktuar exemption → Profit Banke | TC-C4-OTCTAX-004 | — | covered |
| Fund — create (unique name, min, manager, auto RSD acct) | TC-C4-FUND-001 | investment_funds_test.go::TestInvestmentFunds_CreateAndList, TestInvestmentFunds_DuplicateNameRejected, TestInvestmentFunds_ClientCannotCreate | covered |
| Fund — discovery list/filter/sort | TC-C4-FUND-002 | investment_funds_test.go::TestInvestmentFunds_CreateAndList, wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — detailed view (NAV/liquidity/holdings/profit) | TC-C4-FUND-003 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — update (supervisor) | TC-C4-FUND-004 | — | covered |
| Fund — client invest ≥ min from own account | TC-C4-FUND-005 | investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient | covered |
| Fund — cross-currency invest conversion | TC-C4-FUND-006 | — | covered |
| Fund — client redeem full/partial + fee + target acct | TC-C4-FUND-007 | — | covered |
| Fund — supervisor invest on behalf of bank (no fee) | TC-C4-FUND-008 | — | covered |
| Fund — Moji fondovi (client vs supervisor) | TC-C4-FUND-009 | investment_funds_test.go::TestInvestmentFunds_MyPositionsEmptyForFreshClient | covered |
| Fund — partial liquidation when illiquid + notify | TC-C4-FUND-010 | — | NO-ENDPOINT |
| Fund — block deposit while withdrawal pending | TC-C4-FUND-011 | — | NO-ENDPOINT |
| Fund — position/NAV/profit recompute on value change | TC-C4-FUND-012 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund — supervisor buys security on behalf of fund | TC-C4-FUND-013 | — | covered |
| Fund — ownership transfer on supervisor-permission removal | TC-C4-FUND-014 | — | covered |
| Fund — recurring DCA into fund | TC-C4-FUND-015 | — | covered |
| Fund dividends — declare | TC-C4-FUNDIV-001 | — | covered |
| Fund dividends — payout fan-out (client 15%, fund/bank exempt) | TC-C4-FUNDIV-002 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — reinvest (DRIP) | TC-C4-FUNDIV-003 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — distribute proportional to share | TC-C4-FUNDIV-004 | wf_fund_dividend_mode_test.go::TestWF_FundDividendMode | covered |
| Fund dividends — history (fund + me) | TC-C4-FUNDIV-005 | — | covered |
| Fund stats — metrics surface + sort | TC-C4-FUNSTAT-001 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund stats — min-snapshots gate (metrics_available) | TC-C4-FUNSTAT-002 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Fund stats — detail charts (history + average) | TC-C4-FUNSTAT-003 | wf_fund_stats_test.go::TestWF_FundStatistics_SurfaceAndSort | covered |
| Profit Banke — actuary performances | TC-C4-PROFIT-001 | — | covered |
| Profit Banke — bank positions in funds | TC-C4-PROFIT-002 | — | covered |
| Profit Banke — deposit/withdraw as bank | TC-C4-PROFIT-003 | — | covered |
| OTC notif — counter-offer received | TC-C4-OTCNOTE-001 | — | covered |
| OTC notif — offer accepted/withdrawn | TC-C4-OTCNOTE-002 | — | covered |
| OTC notif — contract expiring in N days | TC-C4-OTCNOTE-003 | — | partial |
| OTC negotiation history + filters (status/date/counterparty) | TC-C4-OTCNOTE-004 | otc_unified_read_test.go (read shapes) | covered |
| Defense — Provera 1 OTC trade internal (E2E) | TC-C4-E2E-001 | wf_otc_trading_test.go::TestWF_OTCTradingBetweenUsers | covered |
| Defense — Provera 2 OTC trade external | (see celina-5-cross-bank.md) | otc_sp2b_test.go::TestSP2b_UnifiedRemote* | covered (Celina 5) |
```

Summary: 68 test cases authored across OTC stocks, OTC options negotiation, contract formation,
SAGA exercise + compensation, option/premium tax, investment funds, fund dividends, fund statistics,
Profit Banke, OTC notifications/history, and one defense end-to-end. Notable gaps: partial fund
liquidation when illiquid + deferred-payout notification (TC-C4-FUND-010) and block-deposit-while-
withdrawal-pending (TC-C4-FUND-011) are documented §24 follow-ups with **NO-ENDPOINT** (redeem 409s on
short cash today); the deviation color bands (TC-C4-OTCNEG-011) are frontend-only (NO-ENDPOINT, raw
revision data is exposed); and three items are **partial** — unread-negotiation indicator
(TC-C4-OTCNEG-012, implicit receipt, no mark-read mutation), local refund-retry→admin-alert
(TC-C4-SAGA-005) and CHECK_STATUS resume (TC-C4-SAGA-008) which are fully realized only on the
cross-bank SI-TX path (Celina 5), and the "contract expiring in N days" reminder (TC-C4-OTCNOTE-003).
Cross-bank OTC (odbrana Provera 2) is deferred to celina-5-cross-bank.md.
