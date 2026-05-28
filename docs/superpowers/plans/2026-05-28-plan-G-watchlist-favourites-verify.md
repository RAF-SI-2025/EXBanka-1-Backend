# Plan G — Watchlist + Favourites + Price-Alerts Verification

> **For agentic workers:** Use superpowers:subagent-driven-development. **No unification work** — verification only.

**Goal:** Confirm that users can use watchlist, favourites, and price alerts for a ticker. The user clarified they do NOT want unification — they want a confirmation that the routes exist and work.

**Approach:** Read code; do not invent missing features. Report findings; only build if a documented feature is missing entirely.

---

## Task G1: Inventory the relevant routes

For each of watchlist / favourites / price-alerts:

1. List every gateway route + its auth + permission.
2. List the underlying gRPC RPC.
3. List the underlying repo + service method.
4. Confirm there's a working "add ticker", "remove ticker", "list mine" path.
5. Confirm there's a working "create alert on ticker", "list alerts", "delete alert" path.

Report this in a markdown file `docs/api/WATCHLIST_FAVOURITES_PRICEALERTS_2026-05-28.md` with one section per feature and a table of routes.

## Task G2: Verify the routes actually work

For each route confirmed in G1, do a code-read check (not Docker):

1. Trace the gateway handler → gRPC client → service-layer handler → repository call.
2. Look for obvious bugs: wrong owner, missing auth, wrong table queried, response shape that doesn't match the model.
3. Verify validation on the input (e.g. ticker symbol format, alert price > 0).

If any check fails, flag the issue in the report with file:line and a one-sentence description. Do NOT fix the bug as part of this plan — log it as a follow-up.

## Task G3: Test surface

Look at existing unit and integration tests for each feature. Report:
- Are there tests for add/remove/list?
- Are there tests for add the same ticker twice (idempotency)?
- Are there tests for cross-user privacy (user A cannot see user B's watchlist)?

If tests are missing for these three behaviours, list them as follow-ups. Do NOT add the tests as part of this plan.

## Task G4: Identify if "favourites" exists

If favourites does not exist anywhere in the codebase:
- Report this as a finding.
- Do NOT build it (user clarified: just check if it exists; if not, surface as a follow-up).

## Definition of done

A single markdown report committed to `docs/api/WATCHLIST_FAVOURITES_PRICEALERTS_2026-05-28.md` with three sections (one per feature) plus a "Findings and follow-ups" section.

No code changes, no new routes, no tests added (unless an obvious P0 bug is found, in which case it's a separate commit and called out).
