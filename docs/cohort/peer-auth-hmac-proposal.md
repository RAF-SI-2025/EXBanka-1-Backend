# Peer-bank authentication: HMAC bundle proposal

**Audience:** all four faculty bank teams in the Celina 5 / SI-TX cohort.
**Status:** proposal for cohort-wide adoption.
**Author:** EX Banka team.

## Why a stronger auth than the bare SI-TX `X-Api-Key`

The cohort spec at <https://arsen.srht.site/si-tx-proto/> mandates only an `X-Api-Key`
header for peer-to-peer requests. That is the *minimum* every cohort bank must
accept. It is sufficient for liveness and routing, but it has three operational
gaps that matter for a banking integration:

1. **Static bearer token, indefinite lifetime.** A leaked `X-Api-Key` is a permanent
   forgery key until both peers agree to rotate. There is no built-in way to detect
   a leak in transit (TLS interception, logs, screen-sharing in a demo) before the
   damage is done.
2. **No request integrity.** A man-in-the-middle that captures a `Message<NEW_TX>`
   can replay it verbatim to drain the sender's account on every retry — the spec's
   own idempotence cache will *help* the receiver de-duplicate, but only if the
   `(peer_bank_code, locallyGeneratedKey)` tuple has been seen *on this receiver*.
   The first replay against a different receiver, or after an idem-record purge,
   succeeds.
3. **No replay window.** A captured request can be replayed days later. The body
   has no proof of when it was authored.

A second auth path closes all three gaps without breaking the cohort spec, because
the cohort spec only fixes the *minimum* and is silent on additional security
headers. Any peer that does not understand the HMAC bundle will simply use
`X-Api-Key` and continue to work.

## Wire format

Every peer-to-peer request that wishes to use the strong auth path **adds**, on
top of what SI-TX already requires (`X-Api-Key`, JSON body, etc.):

| Header | Required for HMAC mode | Format | Notes |
|---|---|---|---|
| `X-Bank-Code` | yes | 3-digit string, e.g. `"222"` | the *sender's* code, not the peer's |
| `X-Bank-Signature` | yes | hex-lowercase 64 chars | `HMAC-SHA256(body, key)` over the **exact bytes** of the HTTP request body |
| `X-Timestamp` | yes | RFC 3339 / ISO 8601, UTC, e.g. `2026-05-01T00:50:31Z` | when the message was signed |
| `X-Nonce` | yes | opaque, ≤ 64 bytes, single-use within the window | sender generates; cohort recommends UUIDv4 |

The receiver detects the path by presence of `X-Bank-Signature`:
- header **present** → HMAC path; all four headers above are required.
- header **absent** → fall back to `X-Api-Key` path (the SI-TX baseline).

If both `X-Api-Key` and the HMAC bundle are sent, the receiver MUST verify the HMAC
bundle and ignore `X-Api-Key`. The double-send pattern is the recommended way
for senders to be compatible with both kinds of peers in one request.

### `key` in `HMAC-SHA256(body, key)`

The key is **shared between exactly two peers** and is **direction-specific**:

- The sender uses its own `hmac_outbound_key` (registered with the receiver as
  the receiver's `hmac_inbound_key` for that sender).
- The receiver verifies with the matching `hmac_inbound_key`.

Each direction has its own key. If A talks to B, A's outbound = B's inbound for A;
A's inbound for B = B's outbound. This is symmetric only when peers chose to use
the same key both ways (allowed but discouraged).

Keys are **opaque random bytes**, recommended ≥ 32 bytes. They are never sent
on the wire after the initial out-of-band exchange.

### Body bytes are exact

The signature is over the **raw, unmodified, byte-for-byte body** the sender
puts on the wire. Receivers MUST NOT canonicalise JSON, normalise whitespace,
or re-serialise before verifying — any of those will break the signature for
correct senders. Read body once, sign-or-verify, then parse.

## Receiver verification algorithm

```
1. signature_hex = req.headers["X-Bank-Signature"]
2. bank_code     = req.headers["X-Bank-Code"]      # required
3. ts_str        = req.headers["X-Timestamp"]      # required
4. nonce         = req.headers["X-Nonce"]          # required
5. ts            = parse_rfc3339(ts_str)           # 401 on parse error
6. if abs(now() - ts) > WINDOW: return 401         # default WINDOW = 5 min
7. peer = peer_banks.find_by_bank_code(bank_code)
8. if peer is None or not peer.active or peer.hmac_inbound_key is None: return 401
9. expected = HMAC_SHA256(req.body_bytes, peer.hmac_inbound_key)
10. if not constant_time_equal(hex_decode(signature_hex), expected): return 401
11. if not nonce_store.claim_atomic(bank_code, nonce, ttl=WINDOW): return 401
12. set request.peer_bank_code = peer.bank_code
13. set request.peer_routing_number = peer.routing_number
14. continue to handler
```

Notes:

- **Constant-time compare** is mandatory at step 10. A naive `==` leaks one byte
  per compare to a timing attacker.
- **Nonce claim is atomic** at step 11. EX Banka uses Redis `SET key value NX EX ttl`
  which is atomic by definition. A naive `if not exists then insert` is a TOCTOU
  hole.
- **Fail closed.** Every failure returns `401` with an empty body — no hint about
  which step failed, whether the bank code is registered, or whether the nonce
  was reused. Information leaks about *why* a request failed are themselves a
  side channel.
- **Order of checks matters.** Timestamp first → cheap rejection of stale
  captures. Bank-code lookup next → cheap rejection of unknown senders. HMAC
  compare last → costs CPU regardless of input. Nonce claim last among the
  pure-yes cases → avoid burning a nonce slot on a request that fails HMAC.

## Sender signing algorithm

```
1. body = json_marshal(envelope)              # finalise body bytes here
2. key = peers[receiver].hmac_outbound_key
3. ts = now_utc().format(rfc3339)
4. nonce = uuid4()
5. signature = hex_lower(HMAC_SHA256(body, key))
6. send POST {receiver_base_url}/interbank with headers:
     Content-Type: application/json
     X-Api-Key: peers[receiver].api_token        # optional but recommended for compat
     X-Bank-Code: own_bank_code
     X-Bank-Signature: signature
     X-Timestamp: ts
     X-Nonce: nonce
   body: body                                  # MUST be byte-identical to step 1
```

## Operational requirements on each cohort bank

To accept an HMAC-mode request from peer P, your `peer_banks` row for P needs:

- `bank_code` — P's 3-digit code, used as the lookup key for `X-Bank-Code`.
- `hmac_inbound_key` — the secret P will use to sign requests *to* you.
- `active = true`.

To send HMAC-mode requests to peer P:

- `hmac_outbound_key` — the secret you use to sign requests *to* P. P stores
  this same value as their `hmac_inbound_key` for you.

If `hmac_outbound_key` is null, you fall back to `X-Api-Key` outbound. If
`hmac_inbound_key` is null on a row, the receiver rejects HMAC-mode requests
from that peer (returns 401). API-key inbound still works.

### Key exchange

Out-of-band, once per peer pair. Suggested options:

1. Encrypted email (PGP) or signed Slack DM between the ops contacts of each team.
2. A one-time URL on a private cohort wiki that both teams trust.
3. Generated locally and pasted into the `POST /api/v3/peer-banks` body during the
   peer-registration step at the start of integration testing. Once registered,
   the value is bcrypt-hashed at rest and never re-readable through the API.

The keys are independent per direction and per peer. Rotation is a `PUT
/api/v3/peer-banks/:id` with the new key value; the old key continues to work
until the receiver also updates its row.

## What this proposal does NOT change

- The SI-TX `Message<Type>` envelope is unchanged.
- The 8 SI-TX `NoVote` reasons are unchanged.
- The endpoint surface (`POST /interbank`, `GET /public-stock`,
  `POST/PUT/GET/DELETE /negotiations/...`, `GET /user/{rid}/{id}`) is unchanged.
- Routing-number and idempotence-key rules are unchanged.
- A peer that only speaks `X-Api-Key` interoperates with a peer that speaks both,
  by definition. The HMAC bundle is *additional*, not replacement.

## Decision points for the cohort

These are the only knobs cohort participants need to agree on if they adopt this:

1. **Window.** Default 5 minutes. Stricter (1 min) is safer but less forgiving of
   clock skew between teams' machines. 5 min is what EX Banka ships today.
2. **Nonce TTL.** Equal to the window — there is no reason to remember a nonce
   for longer than a request can be considered fresh.
3. **Hash function.** SHA-256. Larger digests buy nothing here. SHA-1 and MD5
   are not acceptable.
4. **Key length.** ≥ 32 bytes random. Any cryptographically random source.
5. **Body encoding for signature.** UTF-8 JSON, exact bytes. No canonicalisation.
6. **Failure response.** 401 with empty body, no JSON, no error code.

## Reference implementation

EX Banka's middleware is in `api-gateway/internal/middleware/peer_auth.go`,
backed by a Redis-based nonce store at
`api-gateway/internal/cache/peer_nonce_store.go`. Both files are short
(~130 LOC and ~45 LOC respectively) and can be read as a runnable specification.
The unit tests at `api-gateway/internal/middleware/peer_auth_test.go` exercise:

- API-key happy path
- API-key missing / inactive / wrong-token paths
- HMAC happy path
- HMAC wrong signature → 401
- HMAC replayed nonce → 401
- HMAC timestamp skew (±window) → 401
- HMAC missing-header combinations → 401

Reuse, fork, or rewrite — it is offered as evidence the proposal is implementable
in a few hours, not as a binding implementation.
