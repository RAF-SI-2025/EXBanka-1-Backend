# Bank-to-Bank Asset Exchange Protocol — Specification

> **Readable spec** distilled from *"A protocol for bank-to-bank asset exchange"*
> by Arsen Arsenović & Dimitrije Andžić (draft, 2025-10-21).
> Source document: [`docs/A protocol for bank-to-bank asset exchange.htm`](A protocol for bank-to-bank asset exchange.htm).
> Internally this is the protocol our SI-TX / interbank code (`contract/sitx/`,
> `transaction-service/internal/sitx/`, `stock-service/internal/otccache/`) conforms to.
>
> This file **reorganizes** the original for readability — endpoints up front with
> paths + bodies, every field marked mandatory/optional — but does **not change**
> any wire semantics. Where the original prose and its TypeScript types disagree,
> the disagreement is flagged inline. The original is the normative source.
>
> Licensing: the prose is GNU FDL 1.3; the embedded type definitions are CC0 1.0
> (API definitions are not copyrightable). See the source document for full notices.

---

## Contents

1. [Overview & how it works](#1-overview--how-it-works)
2. [Roles glossary](#2-roles-glossary)
3. [Conventions](#3-conventions)
4. [Endpoint reference (all endpoints at a glance)](#4-endpoint-reference-all-endpoints-at-a-glance)
5. [Shared data types](#5-shared-data-types)
6. [Transaction execution protocol (the 2PC layer)](#6-transaction-execution-protocol-the-2pc-layer)
   - 6.1 [`POST /interbank` — the one transport endpoint](#61-post-interbank--the-one-transport-endpoint)
   - 6.2 [Message envelope](#62-message-envelope)
   - 6.3 [Message types & bodies](#63-message-types--bodies)
   - 6.4 [Voting (`TransactionVote` / `NoVoteReason`)](#64-voting-transactionvote--novotereason)
   - 6.5 [Transaction lifecycle (formation → prepare → commit/rollback)](#65-transaction-lifecycle-formation--prepare--commitrollback)
   - 6.6 [Verifying a received transaction](#66-verifying-a-received-transaction)
   - 6.7 [Reliable delivery & idempotency](#67-reliable-delivery--idempotency)
   - 6.8 [Authentication](#68-authentication)
7. [Options & option contracts](#7-options--option-contracts)
8. [OTC negotiation protocol](#8-otc-negotiation-protocol)
   - 8.1 [`GET /public-stock`](#81-get-public-stock)
   - 8.2 [`POST /negotiations`](#82-post-negotiations)
   - 8.3 [`PUT /negotiations/{rn}/{id}` — counter-offer](#83-put-negotiationsrnid--counter-offer)
   - 8.4 [`GET /negotiations/{rn}/{id}` — read current state](#84-get-negotiationsrnid--read-current-state)
   - 8.5 [`DELETE /negotiations/{rn}/{id}` — close](#85-delete-negotiationsrnid--close)
   - 8.6 [`GET /negotiations/{rn}/{id}/accept` — accept & form contract](#86-get-negotiationsrnidaccept--accept--form-contract)
9. [Resolving friendly names](#9-resolving-friendly-names)
10. [Reason-code reference](#10-reason-code-reference)
11. [Appendix A — full type definitions (TypeScript)](#appendix-a--full-type-definitions-typescript)

---

## 1. Overview & how it works

This protocol lets independent banks **exchange assets** (money, stocks, options) and
**negotiate OTC deals** with each other. It is built around a coordinator service that
executes a single logical transaction across multiple microservices — or multiple banks.

It has **two sub-protocols**:

| Sub-protocol | Purpose | Endpoints |
|---|---|---|
| **Transaction execution** (§6) | Move balanced sets of assets atomically across banks via a two-phase commit. | `POST /interbank` (one endpoint, message-typed) |
| **OTC negotiation** (§8) | Discover public stocks, negotiate an option deal turn-by-turn, and accept it. | `GET /public-stock`, `POST/PUT/GET/DELETE /negotiations/...`, `GET /user/...` |

**Transport & encoding**

- All data is exchanged as **JSON over HTTP** requests/responses.
- All JSON **must be UTF-8** encoded.
- Each bank exposes its endpoints under **its own base URL**. The same base URL serves
  both the `/interbank` transport endpoint and all OTC endpoints. Partner banks are
  configured with each other's base URLs out of band.

**The core idea — double-entry, two-phase commit**

A transaction is a **balanced list of postings** (credits and debits). To execute one
that touches more than one bank, the bank that formed it promotes itself to
**coordinator** and runs a 2PC:

1. **Prepare** — every involved bank reserves the assets it must give up and votes
   `YES`/`NO`.
2. **Commit** — if *all* vote `YES`, the coordinator tells everyone to commit (turn
   reservations into real debits/credits). If *any* vote is `NO`, the coordinator tells
   everyone to roll back (release reservations).

Reliability comes from **idempotence keys** + **retransmission**: every message is
retried until acknowledged, and receivers deduplicate by key so each message takes
effect at most once.

---

## 2. Roles glossary

| Term | Meaning |
|---|---|
| **Routing number** | The first 3 digits of an account number; uniquely identifies a bank. Assigned ahead of time and well-known. |
| **Initiating Bank (IB)** / **Coordinator** | The bank that forms a transaction and drives the 2PC across all involved banks. |
| **Participating Bank** | Any bank holding an account touched by the transaction. The IB is also a participant. |
| **Buyer** | The party in an OTC negotiation who wants to buy a publicly-listed stock (initiates the negotiation). |
| **Seller** | The party who has declared stocks public. Holds the **authoritative** copy of the negotiation. |
| **Executing Bank** | The bank that later exercises an option contract. |
| **Option pseudo-account** | A virtual `OPTION`-typed account, keyed by negotiation ID, used to move an option contract and reserve the seller's stock. Always lives in the **seller's** bank. |

---

## 3. Conventions

- **Mandatory vs optional fields.** In the field tables below, **Required = Yes** means
  the field must always be present. **Required = No** marks an optional field (written
  with a trailing `?` in the TypeScript source). In this entire protocol, the *only*
  optional field is `Transaction.callNumber`; everything else is mandatory.
- **Money is not a float.** Every monetary `amount` is emitted as a JSON **number**
  production (per ECMA-404 §8) but **must be handled as an arbitrary-precision decimal**
  (`BigDecimal` in Java). Never parse or store it as IEEE-754 `float64`. Example JSON:
  `{ "account": …, "amount": 260, "asset": … }`.
- **IDs between banks are always `ForeignBankId`.** A bank **must not** interpret the
  opaque `id` field of an ID minted by another bank — treat it as opaque text.
- **Sign convention for postings (double-entry).** A posting's `amount` sign chooses
  credit vs debit:
  - **Negative amount = credit** → *reduces* the asset on that account (the source/payer side).
  - **Positive amount = debit** → *increases* the asset on that account (the destination/receiver side).
  - A transaction is **balanced** when all amounts sum to **zero** (across all accounts).
  - *Example:* "I owe my buddy coffee" →
    `444000100182503611: −260 RSD` (credited) and `111000141215476411: +260 RSD` (debited).
- **String length caps.** `IdempotenceKey.locallyGeneratedKey` ≤ **64 bytes**;
  `ForeignBankId.id` ≤ **64 bytes**.
- **Timestamps** are ISO-8601 with timezone, e.g. `2025-04-16T15:32:44+02:00`.

---

## 4. Endpoint reference (all endpoints at a glance)

All paths are relative to a partner bank's configured **base URL**. `{rn}` = routing
number, `{id}` = opaque ID (together they form a `ForeignBankId`).

| # | Method & Path | Sub-protocol | Auth | Request body | Success response |
|---|---|---|---|---|---|
| 1 | `POST /interbank` | TX execution | `X-Api-Key` | `Message<NEW_TX \| COMMIT_TX \| ROLLBACK_TX>` | `200` (body = `TransactionVote` for `NEW_TX`), `202` (still processing), `204` (done, no body) |
| 2 | `GET /public-stock` | OTC | `X-Api-Key` | — | `200` `PublicStocksResponse` |
| 3 | `POST /negotiations` | OTC | `X-Api-Key` | `OtcOffer` | `200` `ForeignBankId` (new negotiation ID) |
| 4 | `PUT /negotiations/{rn}/{id}` | OTC | `X-Api-Key` | `OtcOffer` (counter-offer) | `200`; `409 Conflict` if not your turn / closed |
| 5 | `GET /negotiations/{rn}/{id}` | OTC | `X-Api-Key` | — | `200` `OtcNegotiation` (offer + `isOngoing`) |
| 6 | `DELETE /negotiations/{rn}/{id}` | OTC | `X-Api-Key` | — | `200` (sets `isOngoing = false`) |
| 7 | `GET /negotiations/{rn}/{id}/accept` | OTC | `X-Api-Key` | — | `200` once the option-forming transaction is submitted |
| 8 | `GET /user/{rn}/{id}` | OTC | `X-Api-Key` | — | `200` `UserInformation`; `404` if unknown |

> The `/interbank` endpoint is "message-typed": one HTTP route carries three logical
> operations (`NEW_TX`, `COMMIT_TX`, `ROLLBACK_TX`) distinguished by the `messageType`
> field in the body. See §6.

---

## 5. Shared data types

These primitives are reused throughout both sub-protocols.

### 5.1 RoutingNumber

The first three digits of an account number; identifies a bank. Assigned ahead of time
and well-known.

```ts
type RoutingNumber = number;
```

### 5.2 IdempotenceKey

Tags every interbank message so the receiver can deduplicate. Senders **must not** reuse
a key; receivers **must** remember keys **indefinitely** and record them in the *same
local DB transaction* that moves assets (so a replay returns the original response).

| Field | Type | Required | Notes |
|---|---|---|---|
| `routingNumber` | `RoutingNumber` | Yes | Must be the **sender's** routing number. |
| `locallyGeneratedKey` | `string` | Yes | Sender-chosen, any scheme. **≤ 64 bytes.** |

```ts
type IdempotenceKey = {
    routingNumber: RoutingNumber;
    locallyGeneratedKey: string;
};
```

### 5.3 ForeignBankId

The only kind of object ID exchanged between banks (used for users, negotiations,
option pseudo-accounts, transaction IDs).

| Field | Type | Required | Notes |
|---|---|---|---|
| `routingNumber` | `RoutingNumber` | Yes | Which bank holds/owns the object. |
| `id` | `string` | Yes | Opaque to all other banks. **≤ 64 bytes.** Never interpreted by non-owning banks. |

```ts
type ForeignBankId = {
    routingNumber: RoutingNumber;
    id: string;
};
```

### 5.4 Timestamp

```ts
// Example: 2025-04-16T15:32:44+02:00
type ISO8601DateTimeWithTimeZone = string;
```

### 5.5 MonetaryValue & CurrencyCode

| Field | Type | Required | Notes |
|---|---|---|---|
| `currency` | `CurrencyCode` | Yes | One of the enum below. |
| `amount` | `number` (decimal) | Yes | JSON number; handle as `BigDecimal`, **not** `float64`. |

```ts
type CurrencyCode =
    | 'RSD' | 'EUR' | 'USD' | 'CHF'
    | 'JPY' | 'AUD' | 'CAD' | 'GBP';

type MonetaryValue = {
    currency: CurrencyCode;
    amount: number;
};
```

### 5.6 Accounts (`TxAccount`)

What a posting can credit/debit. A **tagged union** on `type`:

| `type` | Discriminant field(s) | Meaning |
|---|---|---|
| `PERSON` | `id: ForeignBankId` | A person holding options or stocks. |
| `ACCOUNT` | `num: CurrencyAccountNumber` (`string`) | A currency (current/foreign) account that holds money. |
| `OPTION` | `id: ForeignBankId` | Option pseudo-account; `id` is the **negotiation ID** of the option to execute (see §7). |

```ts
type CurrencyAccountNumber = string;
type TxAccount =
    | { type: 'PERSON',  id: ForeignBankId }
    | { type: 'ACCOUNT', num: CurrencyAccountNumber }
    | { type: 'OPTION',  id: ForeignBankId };
```

### 5.7 Assets (`Asset`)

What can be moved by a posting. A **tagged union** on `type`:

| `type` | Carries | Meaning |
|---|---|---|
| `MONAS` | `asset: MonetaryAsset` | Money in a currency account. |
| `STOCK` | `asset: StockDescription` | A stock, identified by ticker. |
| `OPTION` | `asset: OptionDescription` | An option contract. |

```ts
type Asset =
    | { type: 'MONAS',  asset: MonetaryAsset }
    | { type: 'STOCK',  asset: StockDescription }
    | { type: 'OPTION', asset: OptionDescription };
```

**MonetaryAsset**

| Field | Type | Required |
|---|---|---|
| `currency` | `CurrencyCode` | Yes |

```ts
type MonetaryAsset = { currency: CurrencyCode };
```

**StockDescription** — a stock is uniquely identified by its ticker; all banks share the
same stock universe (same data sources).

| Field | Type | Required |
|---|---|---|
| `ticker` | `string` | Yes |

```ts
type StockDescription = { ticker: string };
```

**OptionDescription** — see §7 for full semantics.

| Field | Type | Required | Notes |
|---|---|---|---|
| `negotiationId` | `ForeignBankId` | Yes | ID of the negotiation that created this option; ties the pseudo-account to the seller's bank. |
| `stock` | `StockDescription` | Yes | Underlying stock. |
| `pricePerUnit` | `MonetaryValue` | Yes | Price π per unit stock. |
| `settlementDate` | `ISO8601DateTimeWithTimeZone` | Yes | After this passes, the option can no longer be exercised. |
| `amount` | `number` | Yes | Number of stocks k. **Integer > 0.** |

```ts
type OptionDescription = {
    negotiationId: ForeignBankId,
    stock: StockDescription,
    pricePerUnit: MonetaryValue,
    settlementDate: ISO8601DateTimeWithTimeZone,
    amount: number,
};
```

### 5.8 Posting

One credit or debit line in a transaction.

| Field | Type | Required | Notes |
|---|---|---|---|
| `account` | `TxAccount` | Yes | The account being moved. |
| `amount` | `number` (decimal) | Yes | Negative = credit, positive = debit (see §3). Handle as `BigDecimal`. |
| `asset` | `Asset` | Yes | The asset being moved. Must be compatible with the account type. |

```ts
type Posting = {
    account: TxAccount;
    amount: number;
    asset: Asset;
};
```

### 5.9 Transaction

A **balanced** collection of one or more postings plus metadata.

| Field | Type | Required | Notes |
|---|---|---|---|
| `postings` | `Posting[]` | Yes | Must be balanced (amounts sum to zero). |
| `transactionId` | `ForeignBankId` | Yes | Globally identifies this transaction (minted by the IB). |
| `message` | `string` | Yes | Human-readable memo recorded in affected users' ledgers. Opaque. |
| `callNumber` | `string` | **No (optional)** | Optional payment reference / call number. |
| `paymentCode` | `string` | Yes | Payment code metadata. |
| `paymentPurpose` | `string` | Yes | Payment purpose metadata. |

```ts
type Transaction = {
    postings: Posting[];
    transactionId: ForeignBankId;

    /* Metadata. */
    message: string;
    callNumber?: string;     // the ONLY optional field in the protocol
    paymentCode: string;
    paymentPurpose: string;
};
```

---

## 6. Transaction execution protocol (the 2PC layer)

### 6.1 `POST /interbank` — the one transport endpoint

Every transaction-protocol operation is delivered as a `POST /interbank` carrying a
`Message<Type>` envelope. The endpoint is secured (see §6.8).

```
POST {baseUrl}/interbank
X-Api-Key: <opaque token issued to you by the receiving bank>
Content-Type: application/json

<Message<NEW_TX | COMMIT_TX | ROLLBACK_TX>>
```

**Response status codes** (these describe *delivery*, not the higher-layer outcome):

| Status | Meaning | Sender action |
|---|---|---|
| `200 OK` | Accepted **and** processing finished. Body = the operation's response (e.g. a `TransactionVote` for `NEW_TX`). | Stop retransmitting. |
| `202 Accepted` | Accepted and logged locally, but **not finished yet** (e.g. a local saga is still running). Body ignored. | Retransmit later to collect the result. |
| `204 No Content` | Accepted and finished, **no body**. Only valid when the operation expects no response (e.g. `COMMIT_TX`, `ROLLBACK_TX`). | Stop retransmitting. |
| any other / network error | Delivery failed. | Retransmit later. |

> **Important:** status codes are a *transport* signal only. A higher-layer failure —
> such as a `NO` vote on a transaction — is still returned as **`200 OK`** with the `NO`
> vote in the body. The request *succeeded* even though the transaction layer chose not
> to execute it.
>
> The receiver **must** record the idempotence key and commit its local part of the
> transaction **before** sending its response. Otherwise the sender may consider the
> message delivered while the receiver never actually acted on it.

### 6.2 Message envelope

A `Message<Type>` wraps a typed body with an idempotence key.

| Field | Type | Required | Notes |
|---|---|---|---|
| `idempotenceKey` | `IdempotenceKey` | Yes | For dedup / reliable delivery. |
| `messageType` | `'NEW_TX' \| 'COMMIT_TX' \| 'ROLLBACK_TX'` | Yes | Selects the body type. |
| `message` | `MessageBody<Type>` | Yes | Body whose shape depends on `messageType` (see §6.3). |

```ts
type Message<Type extends MessageTypes> = {
    idempotenceKey: IdempotenceKey,
    messageType: Type,
    message: MessageBody<Type>,
};

type MessageBodyMapping = {
    NEW_TX:      Transaction;
    COMMIT_TX:   CommitTransaction;
    ROLLBACK_TX: RollbackTransaction;
};
type MessageTypes = keyof MessageBodyMapping;
type MessageBody<Type extends MessageTypes> = MessageBodyMapping[Type];
```

### 6.3 Message types & bodies

| `messageType` | Body type | Expected response | Purpose |
|---|---|---|---|
| `NEW_TX` | `Transaction` | `TransactionVote` (in a `200`) | Locally prepare the transaction, then vote. |
| `COMMIT_TX` | `CommitTransaction` | none (`204`) | Commit a previously-prepared transaction. |
| `ROLLBACK_TX` | `RollbackTransaction` | none (`204`) | Roll back / un-reserve a previously-prepared transaction. |

**`NEW_TX`** carries a full `Transaction` (§5.9). The receiver prepares it and replies
with a `TransactionVote` (§6.4).

**`COMMIT_TX`** — commit the named transaction.

| Field | Type | Required |
|---|---|---|
| `transactionId` | `ForeignBankId` | Yes |

```ts
type CommitTransaction = { transactionId: Transaction['transactionId']; };
```

**`ROLLBACK_TX`** — roll back the named transaction and release its reservations.

| Field | Type | Required |
|---|---|---|
| `transactionId` | `ForeignBankId` | Yes |

```ts
type RollbackTransaction = { transactionId: Transaction['transactionId']; };
```

### 6.4 Voting (`TransactionVote` / `NoVoteReason`)

The response body to a `NEW_TX` message. A **tagged union** on `vote`:

| `vote` | Extra field | Meaning |
|---|---|---|
| `YES` | — | The receiver regards the transaction as valid and permissible; reservations are held. |
| `NO` | `reasons: NoVoteReason[]` | The receiver refuses. One or more reasons explain why. |

```ts
type TransactionVote =
    | { vote: 'YES' }
    | { vote: 'NO', reasons: NoVoteReason[] };
```

`NoVoteReason` is itself a tagged union on `reason`. Every reason except `UNBALANCED_TX`
carries the offending `posting`. See the [reason-code reference](#10-reason-code-reference)
for what each means.

```ts
type NoVoteReason =
    | { reason: 'UNBALANCED_TX' }
    | { reason: 'NO_SUCH_ACCOUNT',             posting: Posting }
    | { reason: 'NO_SUCH_ASSET',               posting: Posting }
    | { reason: 'UNACCEPTABLE_ASSET',          posting: Posting }
    | { reason: 'INSUFFICIENT_ASSET',          posting: Posting }
    | { reason: 'OPTION_AMOUNT_INCORRECT',     posting: Posting }
    | { reason: 'OPTION_USED_OR_EXPIRED',      posting: Posting }
    | { reason: 'OPTION_NEGOTIATION_NOT_FOUND', posting: Posting };
```

> ⚠️ **Spec discrepancy to be aware of:** the original prose names the insufficient-funds
> reason `INSUFFICIENT_ASSETS` (plural), while the normative TypeScript type uses
> `INSUFFICIENT_ASSET` (singular). The type is the wire form — emit/accept
> **`INSUFFICIENT_ASSET`**.

### 6.5 Transaction lifecycle (formation → prepare → commit/rollback)

**Formation.** To exchange assets, a bank builds a `Transaction` with one posting per
account involved; the result **must balance**. It then collects the routing numbers of
all postings to learn which banks are involved.

- If **only the forming bank** is involved → execute **fully locally** (no `/interbank`
  traffic). The two phases below run as two **separate local DB transactions in sequence**.
- If **other banks** are involved → the forming bank promotes itself to **coordinator**
  and runs the distributed 2PC below.

**Phase 1 — Prepare (per participant).** On receiving the transaction, a participant
*reserves* the assets on every **credited** account. If reservation is impossible (e.g.
the account would be overdrafted) it **fails locally, records the failure, and votes
`NO`**. Otherwise it votes `YES`. Recording the transaction, performing the reservation,
and recording the idempotence key + vote result **must all happen in one local DB
transaction**.

**Phase 2 — Commit (per participant).** If **all** participants voted `YES`, the
coordinator sends `COMMIT_TX` to each. On commit, a participant **erases its reservations
and debits the new assets** — again, transactionally.

**Coordinator bookkeeping (remote case):**

1. Format one `Message<NEW_TX>` per remote participant and write them to the message log
   **in the same local transaction** as the coordinator's own local prepare. If local
   prepare fails, the whole thing rolls back and **no messages are sent**.
2. After committing prepare + outgoing messages, send the `NEW_TX` messages and collect
   `TransactionVote` responses. Record each vote **in the same local transaction that
   marks its message as sent** (so a lost vote means the message is retransmitted and the
   vote re-collected).
3. The IB itself counts as a **`YES`** vote if its local prepare passed.
4. When all votes are in and all are `YES`, format one `Message<COMMIT_TX>` per remote
   participant, written in the **same local transaction as the coordinator's local commit**.

**Rollback.**

- **Local rollback** — if a transaction fails after local prepare, un-reserve everything
  reserved by its **local credit postings** and mark it failed in the transaction log.
  A transaction **cannot** be rolled back after a local **commit**.
- **Remote rollback** — when the IB gets a `NO` vote from *any* participant, it:
  1. marks all outgoing `NEW_TX` messages for this transaction as **not to be sent**;
  2. in the *same* local transaction, logs one `Message<ROLLBACK_TX>` per participant.
  The response to a `ROLLBACK_TX` is empty.

### 6.6 Verifying a received transaction

Before executing its local part (i.e. during prepare), a receiver **must** verify all of
the following. Any failure → vote `NO` with the matching reason from §10:

1. The transaction is **balanced**.
2. Every **account exists**.
3. Every **credited** account has **sufficient funds** to reserve.
   *Exception:* OTC option contracts are exempt from this check (see §7).
4. Every **debited** account can **hold the asset** sent to it (possibly via conversion).
5. Every invoked **option account** is credited **exactly k stocks** and debited
   **exactly k·π** in monetary assets, where k = option amount and π = price per unit.
6. Every invoked **option** is **not used and not expired**.

### 6.7 Reliable delivery & idempotency

- Messages are **retransmitted until acknowledged** (a `200` or `204`), giving
  at-least-once delivery on the wire.
- **Idempotence keys** turn that into **at-most-once effect**: the sender never reuses a
  key; the receiver tracks keys **indefinitely** and, on a replay, returns the *same*
  response it produced the first time (recorded in the same local transaction that moved
  assets).
- This is what makes the protocol safe across network faults: if bank A's request to
  bank B succeeds but the response is lost, A retries with the same key and B replays the
  original response instead of double-executing.

### 6.8 Authentication

- Each bank **issues an opaque API token to every partner bank**.
- The token is sent in the **`X-Api-Key`** header on every `/interbank` (and OTC) request
  and authenticates the **sender**.

---

## 7. Options & option contracts

An **option contract** gives a person the right to purchase a stock. It is created when
an OTC offer is accepted, in exchange for a previously-agreed **premium**. The option's
`amount` (number of stocks k) must be an **integer > 0**.

**Creating an option (on accept).** When an OTC offer is accepted, the option contract is
**credited from the seller** and **debited to the buyer** (the seller is the source of the
contract). At that moment the seller's bank:

- records the option contract, and
- **reserves** the correct amount of the correct stock on the **seller's** account, so the
  contract can be executed later.

The option's `negotiationId` is the ID of the negotiation that produced it — which is also
why the **option pseudo-account always lives in the seller's bank**.

**Exercising an option.** To execute an option, the **Executing Bank** forms a transaction
with these postings (π = price per unit, k = amount of stocks):

| Posting | Account | Amount | Asset |
|---|---|---|---|
| Debit option pseudo-account | `OPTION` (id = negotiation ID) | +π·k | money |
| Credit the buyer | buyer | −π·k | money |
| Credit option pseudo-account | `OPTION` (id = negotiation ID) | −k | stock |
| Debit receiving account(s) | buyer's stock holding(s) | +k | stock |

The **option pseudo-account** is a `TxAccount` of type `OPTION` whose `id` is the option's
`negotiationId`.

**Single use & expiry.**

- On correct use, the bank **marks the option as used** and prevents any further use
  (transactionally).
- If the **settlement date has passed**, the option **cannot** be exercised.
- When the settlement date passes on an **unused** option, the bank must **un-reserve** the
  stuck stock — and mark the option used **transactionally** so this happens exactly once.

> This is why option accounts are *exempt from the sufficient-funds check* during
> verification (§6.6 step 3): the stock backing them is already reserved at creation time.

---

## 8. OTC negotiation protocol

An OTC negotiation is a series of updates to a single **OTC offer** object, exchanged
**turn by turn**: only the party who did **not** make the last offer may make the next
one. A negotiation is **initiated by the Buyer**.

- **Seller** — has stocks they've declared public.
- **Buyer** — is interested in those publicly-listed stocks; starts the negotiation.

The Buyer's bank first asks other banks for their public stocks (§8.1). When the Buyer
picks one and makes an offer across banks, it sends a create-negotiation request (§8.2).
The Seller's bank mints a negotiation ID; **both banks track the negotiation by that ID**,
and the **Seller's bank holds the authoritative copy**.

**`OtcOffer`** — the negotiated object. All fields mandatory.

| Field | Type | Required | Notes |
|---|---|---|---|
| `stock` | `StockDescription` | Yes | Stock being optioned. |
| `settlementDate` | `ISO8601DateTimeWithTimeZone` | Yes | Option settlement date. |
| `pricePerUnit` | `MonetaryValue` | Yes | Strike price π per unit. |
| `premium` | `MonetaryValue` | Yes | Premium the buyer pays the seller to form the option. |
| `buyerId` | `ForeignBankId` | Yes | The Buyer. |
| `sellerId` | `ForeignBankId` | Yes | The Seller. |
| `amount` | `number` | Yes | Number of stocks (integer > 0). |
| `lastModifiedBy` | `ForeignBankId` | Yes | Who made the current offer — drives whose turn it is. |

```ts
type OtcOffer = {
    stock: StockDescription;
    settlementDate: ISO8601DateTimeWithTimeZone;
    pricePerUnit: MonetaryValue;
    premium: MonetaryValue;
    buyerId: ForeignBankId;
    sellerId: ForeignBankId;
    amount: number;
    lastModifiedBy: ForeignBankId;
};
```

**Whose turn is it?** It is the **buyer's turn** to post a counter-offer when
`offer.lastModifiedBy ≠ offer.buyerId` (i.e. the seller moved last), and vice-versa. Only
the party whose turn it is may modify the offer.

### 8.1 `GET /public-stock`

Fetch all OTC-traded stocks (and their sellers) offered by a bank.

```
GET {baseUrl}/public-stock
X-Api-Key: <token>
```

**Response `200`** — `PublicStocksResponse` (an array):

| Field | Type | Required | Notes |
|---|---|---|---|
| `stock` | `StockDescription` | Yes | The public stock. |
| `sellers[]` | array | Yes | One entry per seller offering it. |
| `sellers[].seller` | `ForeignBankId` | Yes | A seller of the stock. |
| `sellers[].amount` | `number` | Yes | Amount that seller offers. |

```ts
type PublicStock = {
    stock: StockDescription;
    sellers: {
        seller: ForeignBankId;
        amount: number;
    }[];
};
type PublicStocksResponse = PublicStock[];
```

### 8.2 `POST /negotiations`

Sent from the **Buyer's bank → Seller's bank** to open a negotiation. Creates a new
negotiation and returns its ID.

```
POST {baseUrl}/negotiations
X-Api-Key: <token>
Content-Type: application/json

<OtcOffer>
```

**Response `200`** — a `ForeignBankId`, the new negotiation's ID. Both banks use it from
now on.

### 8.3 `PUT /negotiations/{rn}/{id}` — counter-offer

Post a counter-offer (by either party). `{rn}` / `{id}` are the negotiation ID's
`routingNumber` / `id`. The request notifies the **opposing** bank with the updated offer.

```
PUT {baseUrl}/negotiations/{id.routingNumber}/{id.id}
X-Api-Key: <token>
Content-Type: application/json

<OtcOffer>   // the updated offer
```

**Responses**

| Status | When |
|---|---|
| `200 OK` | Counter-offer accepted into the negotiation. |
| `409 Conflict` | It is **not the sender's turn** (the receiving bank — e.g. the seller's — believes it is *its* turn), or the negotiation is **closed**. |

### 8.4 `GET /negotiations/{rn}/{id}` — read current state

Fetch a fresh **authoritative** copy of the negotiation from the Seller's bank (used to
refresh a stale local copy).

```
GET {baseUrl}/negotiations/{id.routingNumber}/{id.id}
X-Api-Key: <token>
```

**Response `200`** — `OtcNegotiation`: every `OtcOffer` field **plus** `isOngoing`.

| Field | Type | Required | Notes |
|---|---|---|---|
| *(all `OtcOffer` fields)* | — | Yes | See §8. |
| `isOngoing` | `boolean` | Yes | `false` ⇒ negotiations are closed. |

```ts
type OtcNegotiation = OtcOffer & {
    isOngoing: boolean;
};
```

### 8.5 `DELETE /negotiations/{rn}/{id}` — close

Either party may back out by notifying the other bank. This sets `isOngoing = false`.

```
DELETE {baseUrl}/negotiations/{id.routingNumber}/{id.id}
X-Api-Key: <token>
```

### 8.6 `GET /negotiations/{rn}/{id}/accept` — accept & form contract

The party whose turn it is may **accept** the other party's offer, agreeing to form an
option contract. The accepting bank sends this `GET` to the other bank.

```
GET {baseUrl}/negotiations/{id.routingNumber}/{id.id}/accept
X-Api-Key: <token>
```

On accept, the other bank forms this transaction (let the offer be `O`) and submits it to
the transaction executor (§6):

| Posting | Party | Direction | Amount |
|---|---|---|---|
| 1 | Buyer | **Credit** `O.premium` | buyer pays the premium |
| 2 | Seller | **Debit** `O.premium` | seller receives the premium |
| 3 | Buyer | **Debit** one `optionContract(O)` | buyer receives the option |
| 4 | Seller | **Credit** one `optionContract(O)` | seller gives up the option (reserves the stock) |

The seller's credit of the option (posting 4) **reserves the seller's stock** as part of
the contract (see §7). The response is returned **only after** the transaction is
successfully submitted.

**Forming the option contract — `optionContract(o: OtcOffer) → OptionDescription`:**

| Target field | Set from |
|---|---|
| `negotiationId` | the ID of the negotiation that led to this contract (keeps the option pseudo-account in the **seller's** bank) |
| `stock` | `o.stock` |
| `pricePerUnit` | `o.pricePerUnit` |
| `settlementDate` | `o.settlementDate` |
| `amount` | `o.amount` |

> Note: the original prose says "transfer `o.stockDescription`", but the `OtcOffer` field
> is named **`stock`** — use `o.stock`.

---

## 9. Resolving friendly names

Translate a `ForeignBankId` (e.g. a buyer/seller) into human-readable names.

```
GET {baseUrl}/user/{userId.routingNumber}/{userId.id}
X-Api-Key: <token>
```

**Responses**

| Status | Body |
|---|---|
| `200 OK` | `UserInformation` |
| `404 Not Found` | the user ID is invalid |

| Field | Type | Required | Notes |
|---|---|---|---|
| `bankDisplayName` | `string` | Yes | Display name of the user's bank. |
| `displayName` | `string` | Yes | Display name of the user. |

```ts
type UserInformation = {
    bankDisplayName: string;
    displayName: string;
};
```

---

## 10. Reason-code reference

Values for `NoVoteReason.reason` in a `NO` `TransactionVote` (§6.4). All except
`UNBALANCED_TX` carry the offending `posting`.

| Reason | Carries | Meaning |
|---|---|---|
| `UNBALANCED_TX` | — | The received transaction did not balance. This is a **protocol violation** (for administrators, not users). |
| `NO_SUCH_ACCOUNT` | `posting` | An account referenced by a posting does not exist. |
| `NO_SUCH_ASSET` | `posting` | An asset referenced by a posting does not exist. |
| `UNACCEPTABLE_ASSET` | `posting` | The asset cannot be deposited to / credited from that account at all (e.g. stocks into a currency account, or the wrong currency). |
| `INSUFFICIENT_ASSET` | `posting` | A credited account could not have the necessary funds reserved. *(Prose calls it `INSUFFICIENT_ASSETS`; the wire form is singular.)* |
| `OPTION_AMOUNT_INCORRECT` | `posting` | A credit/debit for an option pseudo-account had the wrong amount (see §7). |
| `OPTION_USED_OR_EXPIRED` | `posting` | An option in the transaction was already used or its settlement date has passed. |
| `OPTION_NEGOTIATION_NOT_FOUND` | `posting` | The option's negotiation ID was invalid. |

---

## Appendix A — full type definitions (TypeScript)

Verbatim from the source document (the normative wire definitions; CC0 1.0).

```ts
// ── Identification ─────────────────────────────────────────────
type RoutingNumber = number;

type IdempotenceKey = {
    routingNumber: RoutingNumber;
    locallyGeneratedKey: string;        // ≤ 64 bytes
};

type ForeignBankId = {
    routingNumber: RoutingNumber;
    id: string;                         // ≤ 64 bytes, opaque to other banks
};

// Example: 2025-04-16T15:32:44+02:00
type ISO8601DateTimeWithTimeZone = string;

// ── Money, accounts, assets ────────────────────────────────────
type CurrencyCode =
    | 'RSD' | 'EUR' | 'USD' | 'CHF'
    | 'JPY' | 'AUD' | 'CAD' | 'GBP';

type MonetaryValue = {
    currency: CurrencyCode;
    amount: number;                     // decimal, NOT float64
};

type CurrencyAccountNumber = string;
type TxAccount =
    | { type: 'PERSON',  id: ForeignBankId }
    | { type: 'ACCOUNT', num: CurrencyAccountNumber }
    | { type: 'OPTION',  id: ForeignBankId };

type MonetaryAsset = { currency: CurrencyCode };
type StockDescription = { ticker: string };
type OptionDescription = {
    negotiationId: ForeignBankId,
    stock: StockDescription,
    pricePerUnit: MonetaryValue,
    settlementDate: ISO8601DateTimeWithTimeZone,
    amount: number,                     // integer > 0
};

type Asset =
    | { type: 'MONAS',  asset: MonetaryAsset }
    | { type: 'STOCK',  asset: StockDescription }
    | { type: 'OPTION', asset: OptionDescription };

// ── Transactions ───────────────────────────────────────────────
type Posting = {
    account: TxAccount;
    amount: number;                     // −credit / +debit; decimal
    asset: Asset;
};

type Transaction = {
    postings: Posting[];
    transactionId: ForeignBankId;
    message: string;
    callNumber?: string;                // optional
    paymentCode: string;
    paymentPurpose: string;
};

type CommitTransaction   = { transactionId: Transaction['transactionId']; };
type RollbackTransaction = { transactionId: Transaction['transactionId']; };

// ── Messaging ──────────────────────────────────────────────────
type Message<Type extends MessageTypes> = {
    idempotenceKey: IdempotenceKey,
    messageType: Type,
    message: MessageBody<Type>,
};
type MessageBodyMapping = {
    NEW_TX:      Transaction;
    COMMIT_TX:   CommitTransaction;
    ROLLBACK_TX: RollbackTransaction;
};
type MessageTypes = keyof MessageBodyMapping;
type MessageBody<Type extends MessageTypes> = MessageBodyMapping[Type];

type TransactionVote =
    | { vote: 'YES' }
    | { vote: 'NO', reasons: NoVoteReason[] };

type NoVoteReason =
    | { reason: 'UNBALANCED_TX' }
    | { reason: 'NO_SUCH_ACCOUNT',              posting: Posting }
    | { reason: 'NO_SUCH_ASSET',                posting: Posting }
    | { reason: 'UNACCEPTABLE_ASSET',           posting: Posting }
    | { reason: 'INSUFFICIENT_ASSET',           posting: Posting }
    | { reason: 'OPTION_AMOUNT_INCORRECT',      posting: Posting }
    | { reason: 'OPTION_USED_OR_EXPIRED',       posting: Posting }
    | { reason: 'OPTION_NEGOTIATION_NOT_FOUND', posting: Posting };

// ── OTC negotiation ────────────────────────────────────────────
type OtcOffer = {
    stock: StockDescription;
    settlementDate: ISO8601DateTimeWithTimeZone;
    pricePerUnit: MonetaryValue;
    premium: MonetaryValue;
    buyerId: ForeignBankId;
    sellerId: ForeignBankId;
    amount: number;
    lastModifiedBy: ForeignBankId;
};
type OtcNegotiation = OtcOffer & { isOngoing: boolean; };

type PublicStock = {
    stock: StockDescription;
    sellers: { seller: ForeignBankId; amount: number; }[];
};
type PublicStocksResponse = PublicStock[];

type UserInformation = {
    bankDisplayName: string;
    displayName: string;
};
```
