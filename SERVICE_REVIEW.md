# Service-by-Service Review

A living document. For each service we capture what should change — and only the
things that are genuinely worth changing. Process: **Claude's opinion + your idea =
agreed action items**. Findings are proposals until we both sign off in the
"Decision" block.

Status legend: 🔴 high / 🟡 medium / 🟢 low / ✅ already good (no change).

---

## 1. api-gateway

**Intended responsibilities (your model):** rate-limiting · auth · JWT · token
white/black-lists · REST↔gRPC mapping · some caching · *limited* cross-service
aggregation (never two calls to the **same** service stitched into one response —
that should be one gRPC method) · no stale routes.

### Findings (Claude's opinion)

#### 🔴 1.1 — No rate limiting anywhere — **IN SCOPE, tune for FE polling**
The gateway does zero throttling. `rate_limited` exists only as a gRPC→HTTP error
mapping (`validation.go:285,320`); nothing in the gateway ever produces it.
`/auth/login`, `/auth/refresh`, `/auth/password/reset-request` are wide open to
brute-force / credential-stuffing. The gateway is the correct single choke-point
for this. Redis is already wired in `main.go`, so a distributed limiter is cheap.

**Constraint (yours):** the frontend polls state from multiple routes every ~1s, so
we must NOT throttle normal read traffic. Design = **tiered**, not one global cap:
- **Sensitive endpoints — strict:** `POST /auth/login`, `/auth/password/reset-request`
  (e.g. ~5–10/min per IP+identifier), `/auth/refresh` moderate. These are the real
  brute-force surface.
- **Everything else (GET polling + normal mutations) — generous safety ceiling
  only:** a high per-IP burst (e.g. ~50–100 req/s) purely to stop a runaway/abuse
  client, sized well above legit 1s polling across several routes. Shared NAT/IP
  means we keep this loose. Effectively invisible to the FE.
- Limiter keyed per-IP (and per-identifier on auth). Skip GET read routes from the
  tight bucket entirely.

#### 🔴 1.2 — Auth validates via a gRPC round-trip on *every* request → go asymmetric, verify locally
`AuthMiddleware`/`AnyAuthMiddleware`/`MobileAuthMiddleware` each call
`authClient.ValidateToken` over gRPC for every single request
(`middleware/auth.go:32`). The gateway holds **no** JWT key and does **no** local
verification — it's a pure proxy to auth-service. This is the biggest inefficiency
and it touches three of your bullets at once (JWT, caching, white/black-lists).

**Chosen approach (your idea — asymmetric keys):**
- Switch access-token signing to **asymmetric** (ES256 / EdDSA / RS256).
  **auth-service holds the PRIVATE key** → it is the only party that can mint/sign.
  **api-gateway holds only the PUBLIC key** → it verifies signature + `exp` + claims
  **locally**, with no gRPC call on the hot path. A gateway compromise can't forge
  tokens (only the public half lives there) — safer than a shared `JWT_SECRET`.
- **Caveat → denylist (this IS the gateway white/black-list, item 1.3):** a locally
  verified signature can't see a revocation. So pair local verify with a small
  **Redis denylist**: on logout / RevokeSession / RevokeAllSessions / password change,
  auth-service writes the revoked `jti`/`sid` (or a per-user valid-not-before stamp)
  with TTL = the token's remaining life (≤15 min, so the set stays tiny). Gateway hot
  path = **local signature check + one Redis lookup**, no auth-service hop.
- **Dynamic key rotation (your call):** keys are NOT static. auth-service exposes a
  **gRPC route** (e.g. `GetSigningKeys` → a JWKS-style set of `{kid, public_key}`,
  current + previous during overlap). The gateway fetches at startup, caches, and
  **refreshes** (periodic tick + on-demand when it sees a `kid` it doesn't know).
  Tokens carry `kid` in the header so the gateway picks the right key. Rotation =
  auth mints a new keypair, signs with the new `kid`, keeps serving the old public
  key until the last token signed with it expires (≤15 min), then drops it. No
  gateway redeploy needed to rotate.
- **Moving parts:** signing-alg change in auth-service; access tokens need `jti`
  (ideally `sid`) + `kid` header + a reliable `iat`. Refresh tokens stay
  opaque/DB-stored (already revocable) — only access-token verify moves local.
- Drops the earlier "light cache" idea — local verify is already faster than a cache
  hit, so no `ValidateToken` cache is needed.
- Spans **api-gateway + auth-service** — see auth-service section when we get there.

#### 🟡 1.3 — Gateway-owned denylists: TWO distinct purposes
Under 1.2 the gateway checks Redis after local verify. There are **two** separate
mechanisms (different outcomes), both written by auth-service, both checked at the
gateway:

**(a) Hard revocation — token is dead.** logout / RevokeSession / RevokeAllSessions →
auth adds the `jti`/`sid` to a revocation set (TTL = remaining token life). Gateway
hit → **401 `unauthorized`** (distinct from expiry), client must **re-authenticate**
— the refresh token is also revoked, so this drives a **logout** in the FE.

**(b) Force-refresh — claims are stale (your idea).** When ANY in-token data changes
(permissions, roles, `account_active`, lock state, name, biometrics flag…), the user
must obtain a fresh access token before continuing — the refresh token stays valid, so
the client **silently refreshes** and gets updated claims.
- **Cleanest implementation:** a per-principal **`stale_after[user]` = now** timestamp
  in Redis (not a per-token list). Gateway compares the token's `iat` to it: `iat <
  stale_after` ⇒ stale. One key invalidates *all* that user's outstanding access
  tokens across devices at once (which is what you want on a permission change), TTL =
  access-token lifetime (auto-cleans). This realises your "denylist of tokens that must
  refresh" with a single key instead of enumerating jtis.
- **Gateway response on stale ⇒ the SAME 401 as a normal access-token expiry**
  (`token_expired`, your call), so the FE's existing expiry→refresh path triggers
  transparently — it refreshes and gets new claims, **never logs out**. This is the
  deliberate opposite of (a): stale ⇒ `token_expired` (refresh); revoked ⇒
  `unauthorized` (logout). The gateway tells them apart because they're separate Redis
  structures (stale-after timestamp vs. revocation set).
- **Wiring (cross-service):** whoever mutates token-relevant data must bump
  `stale_after` — permissions/roles live in **user-service**, client active state in
  **client-service**, account-active/lock in **auth-service**. Signal via a Kafka
  `*.claims-invalidated` event consumed by auth (which owns the Redis key), or a direct
  shared-Redis write. Decide the exact channel when we hit those services.

#### 🔴 1.4 / OWN-1 — Move ALL ownership checks OUT of the gateway, INTO each owning service
**Your decision, and it reverses a current CLAUDE.md hard rule.** Today the gateway
verifies ownership *before* forwarding (CLAUDE.md "Resource Ownership Verification
Requirement"). New principle: **the gateway does NOT check ownership — each service
checks ownership of its own resources.** The gateway's only job becomes propagating
the *caller identity* so the owning service can decide.

This also dissolves the "double gRPC" smell: most gateway double-calls are an
**ownership pre-fetch** (`GetAccount`/`GetHolding` just to read `owner_id`, then act).
Once the owning service enforces, those pre-fetch RPCs disappear and the calls
collapse to one — e.g. `GetMyAccountActivity` becomes a single account-service RPC
that checks ownership and returns the ledger.

**What the gateway loses (delete these):**
- Helpers in `validation.go`: `enforceOwnership` (:146), `ResolveAndCheckAccount`
  (:174), `ResolveAndCheckAccountByNumber` (:193), `checkAccountOwnership` (:213).
- All call sites: `transaction_handler.go` (692,719,772,802,1076),
  `account_handler.go` inline (671,716,775), `card_handler.go` (403,761,897,1019),
  `credit_handler.go` (831,886), `stock_order_handler.go` (106,123,158,413,425),
  `portfolio_handler.go` (478,528), `otc_options_handler.go` (~475),
  `peer_tx_dispatcher_handler.go` (109).
- The ownership-only pre-fetch gRPC calls those sites depend on.

**What the gateway gains:** propagate full caller identity to services on every gRPC
call via metadata (extend the existing `changed_by.go` `x-changed-by` pattern into a
proper identity context: `principal_id`, `principal_type`, `on_behalf_of_client_id`,
`acting_employee_id`). `ResolveIdentity` middleware stays only as the place that
builds that metadata.

**Where each check lands (owning service enforces, returns PermissionDenied/NotFound
→ gateway maps to 403/404):**
| Resource | Owning service | Checks to absorb |
|---|---|---|
| accounts, ledger, account-number bindings | account-service | account detail/activity, every "is this the caller's account" check, order/transfer/OTC account bindings (acting service passes identity; account-service enforces at reserve/debit) |
| cards, authorized persons | card-service | card detail, PIN, block, card-by-account |
| loans, installments | credit-service | loan/installment reads & actions |
| holdings, orders, offers, contracts, portfolios | stock-service | holding/order/portfolio ownership, OTC offer/negotiation/contract ownership, exercise strike-account binding |
| payments, transfers | transaction-service | payment/transfer reads, cross-bank outbound source-account ownership |

**Risks / must-preserve (do NOT regress):**
- Several of these checks are on the **cross-bank / SI-TX money path** and were added
  to fix real money-creation bugs (see `project_crossbank_adversarial_findings`,
  ROUTE-CHANGES.md §6/§8). Relocating them must keep the exact guarantee — the
  inbound peer path already enforces inside stock-service, which is the model.
- Identity-in-metadata is only trustworthy at the gateway↔service boundary. Services
  must trust it from the gateway only (same trust model as today's gRPC calls) — do
  not expose those gRPC ports outside the cluster.
- **404-vs-403 semantics:** today client mismatch → 404 (existence hiding), employee
  mismatch → 403. Services must reproduce that, and any change gets logged in
  ROUTE-CHANGES.md.
- **CLAUDE.md** "Resource Ownership Verification Requirement" must be rewritten to the
  new principle as part of this work.

#### ✅ 1.5 — Cross-service aggregations are correct
`GetMe` (user/client + auth), `ListClients`/`GetClient` (client + auth batch),
`ListEmployees` (user + auth), `CreateAccount` (account + card),
`ListCardsByAccount` (account + card) — these combine **different** services into one
REST response. This is exactly your allowed pattern. No change.

#### ✅ 1.6 — Routes are clean
177 routes, no stale/dead/stub routes, no commented-out routes, no v1/v2 leftovers
(v1+v2 retired 2026-04-27). Recent deprecations were hard-deleted, not left dangling.
Nothing to do here.

#### 🟡 1.7 — Edge logging — **IN SCOPE** (CORS: no change)
- **Logging (DO):** `gin.Default()` only gives an unstructured text logger. Add
  **structured request logging + a request-ID correlation** middleware at the edge:
  per-request `request_id` (generate, or honor inbound `X-Request-Id`), method, path,
  status, latency, principal_id, and propagate the request_id downstream via gRPC
  metadata so service logs can be tied together. For a bank, an edge access log is
  worth having.
- **CORS (your call): leave as-is.** `AllowAllOrigins: true` (`router_v3.go:26`) stays
  — low risk (`AllowCredentials:false` + Bearer-header auth, no cookies). No change.
- `FaultHeaderForwarder` (saga fault-injection) is wired globally but compile-time
  no-op in prod builds — fine, just noting it's test scaffolding at the edge.

### Decision / agreed action items

- [x] **1.1 Rate limiting — IN SCOPE.** Tiered: strict on `/auth/login` +
  `/auth/password/reset-request` (+ moderate `/auth/refresh`); generous per-IP safety
  ceiling everywhere else so the FE's ~1s multi-route polling is never throttled.
  Redis-backed.
- [x] **1.2 Auth path — asymmetric JWT + dynamic rotation (DECIDED, your idea).**
  auth-service signs with a PRIVATE key; api-gateway verifies locally with the PUBLIC
  key. **Keys are dynamic:** auth exposes a **gRPC `GetSigningKeys`** route; the
  gateway fetches + caches + refreshes (periodic + on unknown `kid`), supporting
  rotation with `kid` + current/previous overlap. Drop the light-cache idea. Spans
  gateway + auth-service.
  - Open sub-choice (defer to impl): alg = **ES256 / EdDSA / RS256** (lean ES256/EdDSA).
- [x] **1.3 Two gateway-owned denylists.** (a) **Hard revocation** (`jti`/`sid` set,
  logout/revoke → 401 `unauthorized` → FE logs out); (b) **Force-refresh on claims
  change** — per-principal `stale_after[user]` vs token `iat` → 401 **`token_expired`**
  (same as a real expiry, your call) → FE silently refreshes for new claims, never logs
  out. Auth owns the Redis keys; claims changes signalled by **direct Redis write**
  (decided) from user-service / client-service / auth.
- [x] **1.4 / OWN-1 Ownership relocation — DECIDED.** Gateway stops checking ownership;
  each owning service enforces ownership of its own resources using caller identity
  propagated via gRPC metadata. Cross-cutting — every service gets a matching item in
  its section. Rewrite CLAUDE.md's ownership rule. Preserve cross-bank money-path
  guarantees + 404/403 semantics.
- [x] **1.7 Edge logging — IN SCOPE; CORS — no change.** Add structured request logging
  + request-ID correlation (propagate downstream via gRPC metadata). Keep
  `AllowAllOrigins`.

---

## 2. auth-service

**Responsibilities:** JWT token lifecycle (sign/validate/refresh), password & activation
workflows, mobile device activation/biometrics, sessions + login history, account status.
Backed by its own Postgres + Redis (token-validation cache + revocation).

### Findings (Claude's opinion)

#### 🔴 2.1 / Phase B — Asymmetric ES256 + local verify, REUSING the denylists auth already has
**Big discovery:** auth-service already implements *both* denylists Phase B needs:
- `blacklist:<jti>` — hard revocation, checked in `ValidateToken`
  (`auth_service.go:337,356`), written by `RevokeAccessToken` (`:378`).
- `user_revoked_at:<userID>` — per-user **epoch**; `checkRevokedByEpoch` rejects a token
  whose `iat < revokedAt` (`auth_service.go:394`, `cache/redis.go:79`). **This is exactly
  the `stale_after` force-refresh epoch from §1.3b** — already wired, bumped by the
  `role_perm_change_consumer` on role/permission change (`:43`).

So Phase B is mostly *relocation + key change*, not greenfield:
- **AUTH-A:** switch signing HS256 → **ES256** (`jwt_service.go` currently HS256 at
  `:76,99`). Hold an `*ecdsa.PrivateKey` + `kid`; add a `sid` claim (session id, links
  access tokens to `active_session` rows for targeted revoke). `jti`+`iat` already exist.
  Expose **gRPC `GetSigningKeys`** (JWKS-style, current + previous during rotation).
- **AUTH-B (gateway):** the gateway verifies ES256 locally (by `kid`) and reads the SAME
  two Redis keys directly — `blacklist:<jti|sid>` and `user_revoked_at:<id>`. The key
  schema becomes a shared `contract/authredis` package (writer=auth, reader=gateway).
- **Outcome distinction (§1.3):** blacklist hit → **401 `unauthorized`** (logout); epoch
  hit (`iat < revokedAt`) → **401 `token_expired`** (silent refresh). Today both return
  the same "please log in again" — Phase B splits them.
- **AUTH-D:** `ValidateToken` gRPC stops being the per-request hot path. Keep it only for
  the non-gateway caller (verification-service `CheckBiometricsEnabled` is separate) or retire.
- **Reconcile the claims-invalidation channel:** §1.3 decided "direct Redis write", but auth
  ALREADY bumps the epoch via a **Kafka consumer** (`role_perm_change_consumer`). Two clean
  options: (a) keep the consumer (auth owns its Redis; user/client-service just publish the
  event they already publish), or (b) switch those services to write `user_revoked_at`
  directly. Option (a) reuses working code — lean that way unless you want zero Kafka hop.

#### 🔴 2.2 — Revocation wiring is half-dead (HIGH, security-staleness)
- **`RevokeAccessToken` is never called in production** (only a test). So Logout /
  RevokeSession / RevokeAllSessions **do not blacklist the access token** — a logged-out
  user's access token keeps validating (cache hit) until it expires (≤15 min)
  (`auth_service.go:610-658`).
- **Account-disable** (`SetAccountStatus`, `:883`) revokes refresh tokens but neither
  blacklists nor bumps the epoch → a disabled account's access token still works ≤15 min.
- **Fix (folds into Phase B):** wire hard revocation by **`sid`** (logout/revoke-session →
  `blacklist:sid:<sid>`; revoke-all / account-disable → bump `user_revoked_at` as a hard
  cut, or blacklist all the user's sids). Then logout/disable take effect immediately at
  the gateway. This is the change that makes the denylists actually load-bearing.

#### 🟡 2.3 — Unused RPC: `CreateAccount` → remove
`AuthService.CreateAccount` (`grpc_handler.go:175`) has **zero callers** anywhere — account
creation goes through account-service. Dead RPC + handler + service method. Remove from
proto + handler + service (regen authpb). (All other 25 RPCs are used.)

#### 🔴 2.4 — Error handling is not standardized (CRITICAL + MED)
The gateway maps gRPC codes → HTTP; auth-service frequently returns **bare errors** →
everything collapses to **500**, and several handlers **overwrite** the service's intent:
- **Bare/uncoded errors → 500:** `RefreshToken` (`auth_service.go:408,411,417,420,449`),
  `ValidateToken` (`:339,343,358,363`), `jwt_service.go:106,111,115` (`invalid token` should
  be `Unauthenticated`/401, not 500).
- **Handler overwrites the real code:** `RefreshToken` handler hardcodes `Unauthenticated`
  for ALL failures (`grpc_handler.go:110`) — a disabled account looks like a bad token;
  `SetAccountStatus`/`GetAccountStatus`/`...Batch`/`ListSessions`/`GetLoginHistory` hardcode
  `Internal`/`NotFound` (`:151,159,185,307,344`), masking the real cause (DB error reads as
  404 → account enumeration).
- **PII in error chains:** `Login` wraps the **email** into `%w` error chains
  (`auth_service.go:180,183,195,224,…`) — leaks to server logs / error introspection.
- **Fix:** one consistent pattern — services return sentinel errors (`errors.go`), a single
  handler-level `mapErr(err) → status.Error(code, cleanMsg)` translates sentinel→code, no
  per-method hardcoding, no PII in messages. Invalid credentials → `Unauthenticated`;
  locked/disabled → `FailedPrecondition`; not-found → don't leak (same response as bad
  password, anti-enumeration).

#### 🟢 2.5 — Layering is clean (minor nits)
Handler is a thin translator (no DB/Redis), repos are query-only, business logic sits in
service. Fine. Nits: `auth_service.go` is **1091 lines** — cohesive but a candidate to split
into TokenService / SessionService / AccountService later (not urgent); `detectDeviceType`
is duplicated (`handler/metadata.go:49` & `service/auth_service.go:968`) — DRY it.

#### 🟢 2.6 — Caching: degradation good, staleness is 2.2
Redis-down degrades gracefully (nil-checks, fail-open) — good. The only correctness issues
are the eviction/blacklist gaps in 2.2. TTLs (= token lifetime) are right.

### Decision / agreed action items (DECIDED 2026-06-06)
- [x] **2.1/Phase B** — ES256; gateway verifies locally and reads the EXISTING
  `blacklist:*` + `user_revoked_at:*` Redis keys via a shared `contract/authredis`;
  retire the per-request `ValidateToken` hop.
- [x] **2.1 claims channel — KEEP the existing Kafka consumer** (`role_perm_change_consumer`
  bumps the epoch). user/client-service publish events; auth owns its Redis. No direct writes.
  (Supersedes the earlier §1.3 "direct Redis write" default.)
- [x] **2.2 hard-revoke by `sid`** — add a `sid` claim (= `active_session` id); logout /
  revoke-session → `blacklist:sid:<sid>` (gateway → 401 `unauthorized`); revoke-all /
  account-disable → bump `user_revoked_at` as a hard cut. Wire `RevokeAccessToken`-equivalent.
- [x] **2.3 remove `CreateAccount`** RPC + handler + service method (0 callers).
- [x] **2.4 error standardization — NOW, with Phase B** (sentinel→code mapping, no handler
  overwrites, strip email/PII, anti-enumeration).
- [x] **2.5 cleanups — ALL:** DRY `detectDeviceType` (done); **split `auth_service.go`**
  (done). First a file split, then (per your follow-up) promoted to **DI-separable types**:
  `TokenService` / `SessionService` / `AccountService` are each independently constructable
  (`NewTokenService`/`NewSessionService`/`NewAccountService`) and unit-testable in isolation.
  The one cross-concern edge (`AccountService.ResetPassword` → revoke sessions) is a typed
  `SessionRevoker` dependency (injected). Shared revocation primitives are package functions
  (no inter-service coupling). `AuthService` is now a thin composition root that embeds the
  three (so the handler's method-set is unchanged) and owns `Login`+2FA orchestration.

**Implementation order (each a logical commit):** (1) remove CreateAccount, (2) DRY
detectDeviceType, (3) split auth_service.go [no behavior change, tests green], (4) error
standardization, (5) ES256 signing + `sid` + `GetSigningKeys` + rotation, (6) sid-based
revocation wiring, (7) gateway: `contract/authredis` + JWKS cache + local verify + denylist
reads + `token_expired`/`unauthorized` split, (8) docs + VERSION + lint + tests.

## 3. user-service

**Responsibilities:** Employee CRUD + credentials, roles & permissions (seeded),
employee limits, actuary limits, limit blueprints, changelog/audit reads. Four gRPC
services (UserService, EmployeeLimitService, ActuaryService, BlueprintService). Own
Postgres + Redis (employee lookup cache) + transactional **outbox** + crons.

### Findings (Claude's opinion)

**Overall: user-service is well-built** — much healthier than auth-service was. Layering
is clean, caching is correct, no god-objects, no dead RPCs. Two things genuinely worth
doing.

#### 🔴 3.1 — Force-refresh gap on PER-EMPLOYEE permission changes (HIGH, completes Phase B)
auth's `role_perm_change_consumer` bumps the revocation epoch (`user_revoked_at`) only on
`TopicUserRolePermissionsChanged`. user-service publishes that event when a **role's**
permissions change (`role_service.go:294`, listing all affected employees) ✅ — but
**`SetEmployeeRoles` and `SetEmployeeAdditionalPermissions`** (changing ONE employee's
access directly) publish **nothing** auth consumes (`employee_service.go` ~212–315 has no
publish). `UpdateEmployee` publishes `employee-updated`, which auth does NOT consume for
the epoch either.
- **Effect:** an admin changing an individual employee's roles/permissions does **not**
  force-refresh them — their access token keeps the OLD permissions until it expires
  (≤15 min). This is the §1.3b force-refresh we just built, with a hole on the
  per-employee path.
- **Fix:** any path that changes an employee's *effective* permissions
  (`SetEmployeeRoles`, `SetEmployeeAdditionalPermissions`, and `UpdateEmployee` if it
  touches roles) must publish `RolePermissionsChanged` for that employee id. This is
  user-service's slice of Phase D (claims-invalidation), via the existing Kafka path
  (decided: keep the consumer).

#### 🟡 3.2 — Error standardization (your explicit concern; mostly good, one real bug)
user-service already has typed sentinels (`service/errors.go` via `svcerr`) — good. But
usage is inconsistent:
- **Real bug:** `CreateEmployee` does NOT map a duplicate email/JMBG (DB unique-constraint)
  to `ErrEmployeeAlreadyExists` — it wraps the raw DB error (`employee_service.go:81`) →
  handler passthrough → `codes.Unknown` → **HTTP 500 instead of 409**. Same risk on
  `UpdateEmployee` email/JMBG change. → catch the constraint (`gorm`/pg unique violation)
  and return `ErrEmployeeAlreadyExists`.
- **Consistency:** several handlers use ad-hoc `status.Errorf(codes.NotFound, …)`
  (`grpc_handler.go` GetEmployee:~98, GetRole:~186, UpdateRolePermissions:~206,
  ListChangelog/ListAllChangelogs:~278/311) instead of the existing sentinels. They mostly
  return the *right* code, so this is style-consistency, not bugs — but you asked for
  standard errors, so align them on the sentinel→passthrough pattern `blueprint_handler`
  already uses (`errors.Is(err, gorm.ErrRecordNotFound)` → sentinel).
- `hierarchy.go` returns `status.Error(codes.PermissionDenied, …)` directly (works,
  inconsistent) — could use an `ErrHierarchyDenied` sentinel.
- **NOT problems (audit over-flagged):** employee-id embedded in error *chains* is
  server-log-only (the wire message comes from the sentinel, like auth's email); JMBG
  validation messages ("must be 13 digits") are helpful UX, not a leak. Leave those.

#### 🟢 3.3 — Minor cache staleness on role-template perm change (optional)
A **role's** permission change bumps auth's epoch (force-refresh) but does NOT evict
user-service's `employee:id` cache → `GetEmployee` can return stale resolved-permissions
for ≤5 min. It's metadata only (NOT the authz gate — auth/token is), so acceptable.
Optional: on role-perm change, evict the cache of employees holding that role.

#### ✅ 3.4 — No dead RPCs (verified)
The audit flagged `ListEmployeeFullNames` + `UpdateUsedLimit` as unused, but BOTH are used
by **stock-service** (`investment_fund_handler.go:398` fund-manager names;
`stock-service/internal/grpc/user_client.go:88,105` actuary used-limit tracking) — the
agent only checked the gateway. **Keep both.** All 33 RPCs are used. The
`UpdateRolePermissions` (bulk) vs `AssignPermissionToRole`/`RevokePermissionFromRole`
(granular) pair is intentional (different REST verbs), not redundant.

#### ✅ 3.5 — Layering / caching / structure are clean
Handlers are thin translators (no DB/Redis), repos query-only, logic in services. Outbox
pattern (`outbox_relay.go`) is correct and used for the reliability-critical
`supervisor-demoted` cross-service event; relay honors `ctx.Done()`. Cache degrades
gracefully (nil-guarded). `grpc_client/client_limit_adapter.go` (user→client-service for
blueprint application) is a thin, appropriate adapter. No god-objects (largest ~409 lines).

#### OWN-1 (ownership relocation) — barely applies here
Employees are admin-managed resources, not client-`/me`-owned, so there's no per-resource
ownership check to relocate. The `/me` employee-profile read is self-access derived from
the JWT (gateway-side), no change needed.

### Decision / agreed action items (DECIDED 2026-06-06)
- [x] **3.1 — fix the force-refresh gap.** Publish `RolePermissionsChanged` from
  `SetEmployeeRoles` / `SetEmployeeAdditionalPermissions` (+ `UpdateEmployee` if it changes
  roles) so per-employee access changes force-refresh that employee. Completes Phase B.
- [x] **3.2 — error standardization: BOTH.** Map duplicate email/JMBG → `ErrEmployeeAlreadyExists`
  (409, not 500), AND replace ad-hoc `status.Errorf` with sentinel passthrough across handlers.
- [x] **3.3 — add cache eviction.** On a role-perm change, evict the `employee:id` cache of
  employees holding that role.

## 4. notification-service

**Responsibilities:** Email (SMTP) + push delivery, mobile inbox, general
notifications, notification templates, admin/business audit-log storage. **Mostly
Kafka-consumer-driven** (6 consumers) + a gRPC read/manage surface. Own Postgres;
no Redis.

### Findings (Claude's opinion)

#### 🔴 4.1 — Kafka consumers lose messages on error + aren't idempotent (the big one)
All 6 consumers use `reader.ReadMessage(ctx)`, which **auto-commits the offset**
(segmentio/kafka-go with a GroupID), then log-and-`continue` on a processing error
(`email_consumer.go:64,71`, same shape in the others). Consequences:
- **Silent message loss (worse problem):** a transient DB or SMTP failure → error →
  `continue` → the failed message's offset is already committed → the event is **lost
  forever** (no retry, no DLQ). This drops **audit logs** (compliance), **notifications**,
  and **emails** whenever Postgres/SMTP briefly hiccups. Effectively at-most-once-on-failure.
- **Duplicates on crash:** a crash between processing and the next commit redelivers →
  double-process. Only `watchlist_alert_consumer` is idempotent (`CreateWithIdempotency`
  + `ON CONFLICT` on `idempotency_key`, `general_notification_repository.go:31`). The
  other 5 (email, verification, admin_audit, business_audit, general_notification) do
  plain `Create`/`Send` → duplicate emails, duplicate inbox rows, **duplicate audit rows**.
- ✅ ctx cancellation + EnsureTopics are handled correctly in all consumers.
- **Proper fix = both, together:** switch to manual commit (`FetchMessage` +
  `CommitMessages` only after success; on error don't commit → redelivery) for at-least-
  once, AND add idempotency (so redelivery is safe). Doing only one is incomplete (manual
  commit alone → duplicates; idempotency alone → still lost-on-error). Idempotency needs a
  dedup key: most messages lack one (`SendEmailMessage`, audit messages) → contract +
  producer change; `GeneralNotificationMessage` has `RefType`/`RefID` (usable);
  `watchlist` already carries `IdempotencyKey`. **Cross-cutting** (6 consumers + message
  contracts + every producer that emits these) → sizable. Scope TBD with you.

#### 🟡 4.2 — Unused gRPC RPCs: `SendEmail` + `GetDeliveryStatus` → remove
`SendEmail` (gRPC) has **0 callers** — every email goes via the Kafka `notification.send-email`
topic → `email_consumer`. The gRPC RPC is dead. `GetDeliveryStatus` is an unimplemented
stub (returns `ErrDeliveryStatusUnimplemented`, `grpc_handler.go:104`) with 0 callers.
Remove both (proto + handler). All other 12 RPCs are used by the gateway.

#### ✅ 4.3 — gRPC error handling is standardized
`service/errors.go` has typed `svcerr` sentinels with proper codes; the comment notes
handlers passthrough. Good — same pattern as auth/user. (Spot-check handler passthrough
during any impl.) The consumer error handling (4.1) is the gap, not the gRPC surface.

#### ✅ 4.4 — Layering is clean; no caching needed
consumers / sender (SMTP) / push (noop provider, pluggable) / repos / services / template
registry are well-separated. No Redis — correct for this service. inbox_cleanup cron exists.

#### Cross-cutting notes
- **OWN-1 (ownership):** `ListNotifications`/`GetUnreadCount`/`MarkRead` are `/me`-scoped by
  `user_id` from the JWT (gateway-derived) — no resource-ownership check to relocate.
- **Phase D (claims-invalidation):** notification-service is not involved (no token data).

### Decision / agreed action items (DECIDED 2026-06-06)
- [x] **4.1 — FULL fix.** Manual-commit + retry/DLQ + idempotency across ALL 6 consumers,
  including adding idempotency-key fields to the message contracts and stamping them at
  every producer. Safe phase order (each green + committable):
  (A) remove dead RPCs → (B) contract: add `IdempotencyKey` to the affected messages +
  idempotent repo writes (`ON CONFLICT`) → (C) producers stamp the key (auto-gen UUID in
  each service's producer Publish path — "set at every producer" without touching every
  call site) → (D) consumers: `FetchMessage`+`CommitMessages` (commit only after success),
  bounded retry, dead-letter on exhaustion, idempotent processing keyed on `IdempotencyKey`.
- [x] **4.2 — remove `SendEmail` (gRPC) + `GetDeliveryStatus`** (0 callers; email is Kafka-only;
  GetDeliveryStatus is an unimplemented stub). **DONE — phase A, commit fce9c8d (2.13.1).**

#### Phase progress + concrete design for B–D (remaining)
- **A — dead RPCs: DONE** (fce9c8d).
- **B — reliability core: DONE** — shared `runConsumer` (FetchMessage + CommitMessages manual
  commit, bounded retry w/ backoff, `notification.dead-letter` topic on exhaustion, no-commit if
  the DLQ write itself fails); all 6 consumers refactored (`handleMessage` returns error;
  retryable=transient DB/SMTP, non-retryable=malformed/bad-template→nil; verification push is
  best-effort so it never re-inserts the inbox on retry). `Producer.WriteDeadLetter` + DLQ topic
  in EnsureTopics. Stops the silent message loss independently of idempotency. Original design ↓.
- ~~**B — reliability core (manual-commit + retry + DLQ), notification-side only, self-contained
  & green, the BIGGEST win (stops silent loss):**~~ add a shared `runConsumer(ctx, name, reader,
  dlq, handle MessageHandler) ` in `internal/consumer/` using `FetchMessage` + `CommitMessages`
  (commit ONLY after success); bounded retry w/ backoff; on exhaustion write to a new
  `notification.dead-letter` topic then commit (poison message never stalls the partition); if
  the DLQ write itself fails, do NOT commit (retry rather than lose). Refactor each consumer's
  `handleMessage` to RETURN error; `Start` delegates to `runConsumer`. Add the DLQ topic to
  EnsureTopics. Single-insert handlers are atomic so retry is duplicate-safe without keys.
  **Email caveat:** its `EmailSentMessage` confirmation semantics need care — only publish the
  final outcome (after retries/DLQ), and return the send error to trigger retry. Tests: each
  `handleMessage` now returns error.
- **C — idempotency: DONE** (b7f63c6 + this commit). Realized via a Kafka **header**, not
  per-message struct fields: the shared producer (used by all 12 services) stamps an
  `idempotency-key` header on every message — one place, no per-service edits, no contract-struct
  churn. Consumer side: a generic dedup in `runConsumer` (check `processed_messages` before, mark
  after success) keyed on the header — covers ALL 6 consumers uniformly and subsumes the original
  separate phase D. watchlist keeps its extra business-key dedup. Net: at-least-once (Phase B) +
  redelivery dedup (Phase C) = effectively exactly-once for the common case. **§4.1 FULL fix DONE.**
- ~~**C — idempotency keys (cross-service producer sweep):** add `IdempotencyKey string` to~~
  `SendEmailMessage`, `GeneralNotificationMessage`, the admin/business audit messages, and the
  verification-challenge message (`contract/kafka/messages.go`); stamp a UUID in each service's
  producer `Publish*` path (auto-gen if empty — "set at every producer" without touching every
  call site). Touches ~8 services that emit these. Then add `idempotency_key` columns +
  `ON CONFLICT DO NOTHING` writes to `admin_audit_log`, `business_audit_log`, `mobile_inbox`
  (general_notification already has `CreateWithIdempotency`); email dedup via a small
  `processed_messages(key)` claim table. Consumers dedup on the key (skip when empty → safe
  during rollout).
- **D — wire consumer dedup** once C lands (consumers call the idempotent writes / claim).

## 5. client-service

**Responsibilities:** Bank client CRUD (profile), client limits, changelog. Own
Postgres + Redis cache. NOTE: client *credentials* live in auth-service's unified
Account table — client-service has **no** ValidateCredentials RPC (the CLAUDE.md
client-login note is stale), and client *active-state* is auth's `SetAccountStatus`.

### Findings (Claude's opinion)

**Overall: small, clean service that mirrors user-service.** The one real issue is the
same duplicate-→-500 bug as user-service, and here it also leaks PII.

#### 🔴 5.1 — Duplicate email/JMBG → 500 **and leaks PII** (the real bug)
`CreateClient` wraps the raw DB error (`client_service.go:89` → `fmt.Errorf("create
client: %w", err)`), `UpdateClient` likewise (`:175`). The `ErrClientAlreadyExists`
sentinel (`errors.go:21`) is defined but **unused**, and `TranslateError` is off in the
gorm config (`cmd/main.go:31`). So a duplicate returns **HTTP 500** instead of 409 —
and worse, the Postgres unique-constraint error string (which includes the **email /
JMBG value**) propagates as the wire error message. → enable `TranslateError`; map
`gorm.ErrDuplicatedKey` → `ErrClientAlreadyExists` in Create + Update (the clean
sentinel message also closes the PII leak). Same fix shape as user-service §3.2.

#### 🟢 5.2 — Minor error-consistency (match user-service decisions)
- `SetClientLimits` upsert error returned raw (`client_limit_service.go:104`) → wrap so
  it doesn't surface as `Unknown`/500 (low value; optional).
- `ListChangelog`/`ListAllChangelogs` use ad-hoc `status.Errorf` (InvalidArgument/Internal)
  — **leave as-is**, consistent with the call I made in user-service §3.2 (defensible codes).

#### ✅ 5.3 — No dead RPCs (GetClientByEmail kept)
All 9 RPCs are used. `GetClientByEmail` is **seeder-only** now (production client login
moved to auth's Account table) but the seeder genuinely depends on it for a create
pre-check — **keep it** (removing breaks the seeder; not dead like notification's SendEmail).

#### 🟢 5.5 — Dead sentinels (vestiges of the moved credential flow)
`ErrInvalidCredentials` + `ErrAccountNotActivated` (`errors.go`) are referenced **only** by
a sentinel→code table test — no production path uses them since client credential/login
validation moved to auth-service's Account table (same root cause as the stale CLAUDE.md
client-login note). → **remove** both sentinels + their test rows (dead-code cleanup).

#### ✅ 5.4 — Layering / caching clean; no cross-cutting gaps
Handler thin, repo query-only, logic in service; Redis cache invalidated on update,
graceful degradation; optimistic-lock (Version) handled via `BeforeUpdate` + RowsAffected.
- **Phase D (claims-invalidation):** client *deactivation* is auth's `SetAccountStatus`,
  which already bumps the revocation epoch (done in the auth pass) → **no gap here**.
- **No force-refresh gap** (clients have a fixed `client` role; no per-client permission
  changes like employees' §3.1).
- **OWN-1:** client profile `/me` read is self-access (gateway-derived); CRUD is admin —
  nothing to relocate.

### Decision / agreed action items (DONE — VERSION 2.15.0→2.15.1)
- [x] **5.1** — enabled `TranslateError`; map duplicate email/JMBG → `ErrClientAlreadyExists`
  (409, no PII) in CreateClient + UpdateClient. Unit tests now assert the 409 sentinel and
  that the message does **not** echo the email/JMBG.
- [x] **5.2** — wrapped the SetClientLimits upsert error in a coded `ErrLimitPersistFailed`
  (Internal); left the changelog ad-hoc codes (consistent with user-service). No RPC removal.
- [x] **5.5** — removed dead sentinels `ErrInvalidCredentials` + `ErrAccountNotActivated`
  (+ their table-test rows).
- **5.3 / 5.4** — no action: RPCs all used, layering/caching clean, no Phase-D / force-refresh
  / OWN-1 gap (client deactivation is auth's `SetAccountStatus`, already epoch-bumped).

## 6. account-service

**Responsibilities:** Accounts (balance/limits/spending), the AUTHORITATIVE spending-limit
enforcer, ledger, reservations (3 families: generic/incoming/outgoing), companies, currencies,
bank-owned accounts, changelog, idempotency, reconciliation, crons. Own Postgres + Redis cache.
Largest, money-critical service: 2 gRPC services, ~38 RPCs.

### What's GOOD (verified, leave alone)
- Transaction isolation is exemplary: `UpdateBalance`, all reservation reserve/settle/release
  use `SELECT FOR UPDATE` + `RowsAffected==0` checks. Idempotency via ledger `idempotency_key`
  + `idempotency_record` (ON CONFLICT). Crons honor `ctx.Done()` + `defer ticker.Stop()` + use
  `SkipHooks` for bulk spending resets. DeleteBankAccount enforces the ≥1-RSD/≥1-foreign
  invariant atomically under FOR UPDATE. The spending limit IS enforced authoritatively inside
  `UpdateBalance` under the lock.
- **Two audit "CRITICAL"s were FALSE POSITIVES:** (a) `UpdateBalance` using
  `Session{SkipHooks}.Updates(map)` is *correct* — it uses `gorm.Expr("balance + ?")` atomic
  increments inside FOR UPDATE with explicit `WHERE id`, which is the documented-acceptable
  pattern, NOT the forbidden zero-version trap. (b) The balance cache does NOT enable
  double-spend — every authoritative money path reads via FOR UPDATE from the DB, never the cache.

### Findings (Claude's opinion)

#### A — Error standardization (the clear bad things; same proven pattern as §2/§3/§5)
- **A1 🔴 CreateCompany duplicate → 500 + PII.** `TranslateError` is OFF (`cmd/main.go`), and
  `company_service.Create`→`repo.Create` returns the raw DB error. Company has uniqueIndex on
  `RegistrationNumber` + `TaxNumber`, so a duplicate returns 500 AND leaks those numbers in the
  PG constraint string. → enable `TranslateError`; map `gorm.ErrDuplicatedKey`→`ErrCompanyDuplicate`.
- **A2 🔴 UpdateBalance returns raw error strings.** The repo returns `fmt.Errorf("limit_exceeded: …")`
  and `"insufficient funds: …"` (raw, with account number) and bare `gorm.ErrRecordNotFound`
  (`account_repository.go:230-242,267`). These reach the caller as **500 Unknown** instead of
  429 (`ErrSpendingLimitExceeded`) / 409 (`ErrInsufficientBalance`) / 404 (`ErrAccountNotFound`).
  → return repo-level coded sentinels (like the existing `ErrInsufficientBankLiquidity`).
- **A3 🟡 `ErrCompanyDuplicate` reused for account-NAME conflicts** (`account_service.go:88,186,326`).
  Correct 409 code but the wire message is the misleading "company already exists" for an
  account-name clash. → add `ErrAccountNameDuplicate` ("account name already exists").
- (bank_account_handler's `status.Errorf` calls are actually reasonable repo-sentinel mapping —
  only nit is `%v err` in the Internal fallback; not worth a dedicated change.)

#### B — Caching (decision): balance cache is fragile
`GetAccount`/`GetAccountByNumber` cache the FULL account incl. balance/available/spending for
**2 min** (`accountCacheTTL`). The account-service write paths invalidate, BUT the
incoming/outgoing reservation services mutate balance and invalidate NOTHING → `GetAccount`
returns a stale balance for up to 2 min after a cross-bank reserve/settle. Not a double-spend
(authoritative paths bypass cache), but a real stale-money-read for a bank. **Opinion: remove
the account cache** — it's a cheap indexed PK/number lookup; caching mutable money across this
many mutation paths is permanently fragile.

#### C — Cross-bank outgoing debits bypass the spending limit (decision)
`ReserveOutgoing`/`SettleOutgoing` check AvailableBalance sufficiency and ACCRUE
daily/monthly spending, but never CHECK the daily/monthly limit. Domestic debits (via
`UpdateBalance`) ARE limit-gated. So a client's cross-bank (SI-TX) outflows are not subject to
their daily/monthly limit. Touches the FROZEN interbank money path ([[feedback_interbank_protocol_frozen]])
— changing it alters NO-vote semantics, so this is the user's call, not an autonomous fix.

#### D — OWN-1 ownership (decision): not implemented here
account-service does NO ownership/authorization — it trusts the gateway entirely; `changed_by`
is used only for changelog attribution, never authz. Per the OWN-1 decision, ownership should
move into the owning service. This is the largest such migration (~38 RPCs, needs caller
identity over gRPC metadata). Opinion: **defer to a dedicated OWN-1 pass** rather than bundle
into this review.

#### E — Unused RPCs (cleanup): GetCompany, UpdateCompany, GetCurrency
Zero callers anywhere (verified: the `GetCurrency` grep hits are all protobuf field getters,
not RPC calls). `CreateCompany`/`ListCurrencies` ARE used. Removing requires editing
`account.proto` + `make proto`. The 3 reservation families are all USED and NON-redundant.

#### F — Kafka publish from handler (layering): CreateAccount/UpdateAccountName/Limits/Status
publish events from `grpc_handler.go`, not the service layer (violates the CLAUDE.md "publish
from service/" rule). Larger refactor; decision.

### Decision / agreed action items (DECIDED 2026-06-06 — maximal scope, phased commits)
- [x] **A1–A3** — DONE (2.15.4): TranslateError on; company dup→ErrCompanyDuplicate (409, no PII);
  UpdateBalance maps repo ErrSpendingLimit/ErrInsufficientFunds/NotFound → 429/409/404 coded
  sentinels (was 500 leaking account number); added ErrAccountNameDuplicate (was mislabeled
  "company already exists"). +tests.
- [x] **B** — DONE (2.15.4): extracted one shared `evictAccountCache` (single key-format source;
  AccountService + ReservationService now route through it); injected the cache into incoming/
  outgoing reservation services via `WithCache` and evict on every balance mutation
  (CommitIncoming, ReserveOutgoing/SettleOutgoing/ReleaseOutgoing). +end-to-end miniredis tests.
- [x] **C** — DONE (2.15.5): `ReserveOutgoing` now checks daily/monthly limits for client accounts
  (mirrors `UpdateBalance`); over-limit → `FailedPrecondition` (→ interbank NO vote, like
  insufficient funds), generic message (no spending/limit leak to the peer bank). +tests.
  Limit checked at reserve against committed spending (settle accrues it). Bank accounts exempt.
- [~] **D** — OWN-1 PLANNED, not yet implemented. Investigation revealed it is a CROSS-SERVICE
  platform change, not an account-scoped one: today only `x-changed-by` (a single user id, for
  audit) is propagated — there is NO principal_type/principal_id/on_behalf identity in gRPC
  metadata, so the foundation must be built in shared `contract/` first (D0), used by ALL services.
  And the money RPCs (UpdateBalance, Reserve*/Commit*/Settle*/Release*, ReserveFunds, PartialSettle)
  are SERVICE-TO-SERVICE with no caller identity — ownership can only apply to the user-facing tier
  (GetAccount, UpdateAccountName/Limits/Status, ListAccountsByClient, GetLedgerEntries, CreateAccount).
  Getting the trusted-service-vs-user split wrong breaks transfers/OTC/loans or opens an auth hole in
  the money service. Full plan: docs/superpowers/plans/2026-06-06-own1-account-ownership-in-service.md
  (D0 foundation → D1 enforce additively → D2 remove gateway checks). User chose to implement.
  IN PROGRESS: **D0 DONE** (43a2197b, 2.15.7 — contract/identity + gateway stamps caller identity);
  **D1 account-service DONE** (fc233996, 2.16.0 — enforces ownership on direct reads/list, additive,
  tested). Remaining (turnkey steps in the plan): card/credit/transaction (same pattern),
  stock + service→service identity forwarding for the indirect OTC/order account-binding, then D2
  gateway-check removal per service. The expanded scope ("ownership in ALL services + remove gateway
  checks") is a repo-wide platform refactor — proceeding service by service, additive-first.
  **SERVICE-SIDE ENFORCEMENT NOW DONE in all applicable services**: account (fc233996), card
  (1fbd71b7), credit (8a77cada), transaction (fe29933f) — each enforces owner-matching on its
  user-facing reads/lists via identity.OwnsResource (additive; gateway still checks; NO gap; full
  tests). stock-service is EXEMPT (its OTC/order check validates a caller-SUPPLIED account at the
  boundary — counterparty/bank accounts are record-sourced, so it must stay at the gateway; portfolio
  access is permission-entangled). transaction list-by-client stays at the gateway (keyed by
  gateway-resolved account numbers). ONLY remaining: **D2 gateway-check removal** — pure de-dup (the
  services already enforce, so removing the redundant gateway ownership checks is not a security
  change; leaving them is strictly safer). VERSION 2.16.3.
  **OWN-1 SUBSTANTIALLY COMPLETE (VERSION 2.16.5):** D2 gateway de-dup done for account (/me + lists,
  d469db2a), and card/credit/transaction + account-list (226a7ca1). Foreign-resource access is now
  404 (no leak). KEPT at gateway (correct): stock OTC/order + portfolio account-binding, peer-tx +
  card-creation account-binding, transaction list-by-client (account-number resolution), payment
  recipients (not yet service-enforced). RBAC/RequirePermission never touched. All 6 touched modules
  (contract, account, card, credit, transaction, api-gateway) build+test+lint green.
- [~] **E** — DROPPED. `GetCompany`/`UpdateCompany` are the read/update half of a **live**
  Company resource (the gateway has a `/companies` group, `CreateCompany` is used, accounts carry
  `company_id`) — "no caller today" ≠ "dead"; the FE creates companies + company accounts and will
  need to view/edit them. `GetCurrency` is the only genuinely-redundant one, but removing any of
  these cascades to ~33 stub methods across 5 services' test suites (every service mocks the full
  account-client interface), so it's not worth it for one redundant RPC. Reverted; kept all 3.
  LESSON: verify a feature is actually retired before deleting its "unused" RPCs.
- [x] **F** — DONE (2.15.6): all Kafka publishing for CreateAccount/UpdateAccountName/Limits/Status
  + CreateBankAccount moved into the service layer (new `account_events.go`: `eventPublisher`/
  `clientLookup` interfaces, `WithEvents`/`WithClientLookup` builders, `emit*` helpers). Handlers
  (account + bank) are now thin; their `producer`/`clientClient` fields removed; main.go wires the
  producer + client into the service. The account-created email lookup uses a bounded (5s) context.
  Handler-test publish assertions migrated to service-level tests (`account_events_test.go`).

Phase order (each its own commit + VERSION bump): A → B → C → E → F → D.

## 7. card-service
_pending_

## 8. transaction-service
_pending_

## 9. credit-service
_pending_

## 10. exchange-service
_pending_

## 11. verification-service
_pending_

## 12. stock-service
_pending_
