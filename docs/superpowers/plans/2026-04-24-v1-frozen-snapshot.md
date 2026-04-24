# V1 Frozen-Snapshot Implementation Plan

> **Status:** Plan only. Do NOT execute yet. Implementation is deferred until the user explicitly authorizes it. This document describes the plan; it is not the work itself.

**Goal:** Turn `/api/v1/*` into a frozen, read-only snapshot of the current REST surface so future v2 (and v3) changes cannot accidentally alter v1 behavior. Today v1 and v2 share `RegisterCoreRoutes`, which means any handler change affects both versions simultaneously — the opposite of what API versioning promises.

**Architecture:**
1. Copy the current `RegisterCoreRoutes` into a v1-only function (`RegisterCoreV1Routes`) that owns its own handler wiring and calls into today's handler package.
2. Rename the shared function to `RegisterCoreV2Routes` so v2 evolves independently of v1.
3. Snapshot the current handler package into a `handler/v1/` subpackage, so if the handler signature / response shape / validation logic changes in v2, v1 keeps answering exactly as it does today.
4. Snapshot the current Swagger output into a versioned docs directory so Swagger UI can show v1 and v2 separately without one overwriting the other.

**Tech Stack:** Go, Gin, gRPC stubs from `contract/`, Swag CLI.

---

## Scope Boundaries

**In scope:**
- Splitting the shared `RegisterCoreRoutes` into two per-version registration functions.
- Snapshotting handlers that v1 uses so later v2 edits don't bleed into v1.
- Splitting Swagger docs so `/swagger/v1/index.html` and `/swagger/v2/index.html` each reflect their own version.
- Updating `cmd/main.go` wiring.
- Adding compile-time + runtime tests that prove v1 response shapes haven't drifted.

**Out of scope:**
- Changing any v1 response shape (the whole point is that v1 stops changing).
- Changing gRPC service contracts. v1 snapshots only the REST layer; gRPC changes still affect both v1 and v2.
- Splitting gRPC clients (v1 and v2 share the same gRPC backend).
- Database or protobuf changes.

---

## File Structure

**Create:**
- `api-gateway/internal/router/router_v1_core.go` — new file containing `RegisterCoreV1Routes` (moved from `router_v1.go`). Wires the v1-frozen handlers.
- `api-gateway/internal/handler/v1/` — new subpackage, copy of the current `internal/handler/` package at the moment of the split. Constructor signatures unchanged so main.go wiring stays short.
- `api-gateway/docs/v1/` — new directory produced by `swag init --output docs/v1 --tags …`. Contains `docs.go`, `swagger.json`, `swagger.yaml` for v1.
- `api-gateway/docs/v2/` — same, for v2.
- `api-gateway/internal/router/v1_snapshot_test.go` — golden-file test that POSTs/GETs each v1 route and compares the JSON against a recorded fixture.

**Modify:**
- `api-gateway/internal/router/router_v1.go` — keep the file but reduce it to `SetupV1Routes(r, …)` which calls the new `RegisterCoreV1Routes`. Delete the body of `RegisterCoreRoutes` from this file.
- `api-gateway/internal/router/router_v2.go` — rename `RegisterCoreRoutes` call to `RegisterCoreV2Routes` and keep the v2-only additions (`/options/:id/orders`, `/options/:id/exercise`).
- `api-gateway/cmd/main.go` — no change to the number of arguments; same deps still passed into both `SetupV1Routes` and `SetupV2Routes`. Swagger handler registration changes to serve two docs trees.
- `api-gateway/docs/api/REST_API_v1.md` — already exists; mark the header as "frozen snapshot as of YYYY-MM-DD, no further edits."
- `Makefile` — `make swagger` runs `swag init` twice (once per version).

**Delete:** nothing at the source level. Tests and docs get split, not removed.

---

## Task Breakdown

### Task 1: Snapshot the handler package into handler/v1/

**Files:**
- Create: `api-gateway/internal/handler/v1/*.go` (copy of every file currently in `api-gateway/internal/handler/`).

- [ ] **Step 1: Copy every `.go` file from `internal/handler/` into `internal/handler/v1/`**

  ```bash
  cd api-gateway
  mkdir -p internal/handler/v1
  cp internal/handler/*.go internal/handler/v1/
  ```

- [ ] **Step 2: Change the package declaration on every copied file**

  Every file in `internal/handler/v1/` must have `package v1` (not `package handler`) on line 1. Use a sed replace:

  ```bash
  sed -i '' -e '1 s/^package handler$/package v1/' api-gateway/internal/handler/v1/*.go
  ```

- [ ] **Step 3: Fix internal import paths inside v1**

  Any file that referenced its own package implicitly still works, but cross-file references inside the copy may need qualified imports. Build to find errors:

  ```bash
  cd api-gateway && go build ./...
  ```

  Expected: compiles cleanly because the copy is self-contained and imports only `contract/*pb` and stdlib.

- [ ] **Step 4: Commit**

  ```bash
  git add api-gateway/internal/handler/v1/
  git commit -m "refactor(api-gateway): snapshot handler package into handler/v1 for v1 freeze"
  ```

---

### Task 2: Extract RegisterCoreV1Routes from the shared function

**Files:**
- Create: `api-gateway/internal/router/router_v1_core.go`
- Modify: `api-gateway/internal/router/router_v1.go`

- [ ] **Step 1: Move the body of `RegisterCoreRoutes` into `router_v1_core.go` as `RegisterCoreV1Routes`**

  The new function has the same signature as `RegisterCoreRoutes` today. Change handler construction to use the snapshot:

  ```go
  package router

  import (
      v1handler "github.com/exbanka/api-gateway/internal/handler/v1"
      // …same gRPC imports as before
  )

  func RegisterCoreV1Routes(group *gin.RouterGroup, /* …deps… */) {
      authHandler := v1handler.NewAuthHandler(authClient)
      empHandler  := v1handler.NewEmployeeHandler(userClient, authClient)
      // …every other handler constructor, prefixed with v1handler.…
      // Route table stays byte-for-byte identical to today's RegisterCoreRoutes.
  }
  ```

- [ ] **Step 2: Shrink `router_v1.go` to the thin wrapper**

  ```go
  func SetupV1Routes(r *gin.Engine, /* same deps */) {
      v1 := r.Group("/api/v1")
      RegisterCoreV1Routes(v1, /* pass deps through */)
  }
  ```

  Delete the old `RegisterCoreRoutes` body from this file.

- [ ] **Step 3: Run existing router tests**

  ```bash
  cd api-gateway && go test ./internal/router/...
  ```

  Expected: PASS. v1 still serves every current route.

- [ ] **Step 4: Commit**

  ```bash
  git add api-gateway/internal/router/router_v1.go api-gateway/internal/router/router_v1_core.go
  git commit -m "refactor(api-gateway): extract RegisterCoreV1Routes from shared function"
  ```

---

### Task 3: Rename shared function to RegisterCoreV2Routes

**Files:**
- Create: `api-gateway/internal/router/router_v2_core.go`
- Modify: `api-gateway/internal/router/router_v2.go`

- [ ] **Step 1: Move the shared route table (the one just extracted in Task 2) into `router_v2_core.go` as `RegisterCoreV2Routes`**

  Same body as `RegisterCoreV1Routes` today, but imports the live (non-snapshotted) `handler` package:

  ```go
  package router

  import (
      "github.com/exbanka/api-gateway/internal/handler"
      // …
  )

  func RegisterCoreV2Routes(group *gin.RouterGroup, /* …deps… */) {
      authHandler := handler.NewAuthHandler(authClient)
      // …
  }
  ```

- [ ] **Step 2: Update `SetupV2Routes` to call `RegisterCoreV2Routes` instead of `RegisterCoreRoutes`**

- [ ] **Step 3: Delete the old `RegisterCoreRoutes` declaration entirely**

  There is no longer a shared function; v1 and v2 each own their registration.

- [ ] **Step 4: Run router tests + build**

  ```bash
  cd api-gateway && go build ./... && go test ./...
  ```

  Expected: PASS at both layers; v2 still serves its full surface.

- [ ] **Step 5: Commit**

  ```bash
  git add api-gateway/internal/router/
  git commit -m "refactor(api-gateway): v2 owns its own RegisterCoreV2Routes, v1 frozen"
  ```

---

### Task 4: Split Swagger output per version

**Files:**
- Modify: `Makefile`
- Modify: `api-gateway/cmd/main.go` (swagger handler registration)
- Create: `api-gateway/docs/v1/` (via `swag init`)
- Create: `api-gateway/docs/v2/` (via `swag init`)

- [ ] **Step 1: Annotate each handler function with a version hint**

  Every swagger-annotated handler already uses `@Router /api/v{N}/…`. Use the `--tags` or `--instanceName` flag to build two separate docs roots.

- [ ] **Step 2: Update Makefile**

  ```make
  swagger:
  	cd api-gateway && swag init -g cmd/main.go --output docs/v1 --instanceName v1 --tags v1
  	cd api-gateway && swag init -g cmd/main.go --output docs/v2 --instanceName v2 --tags v2
  ```

  (The actual flag names depend on the `swag` version in use; the goal is two independent generated trees.)

- [ ] **Step 3: Register two swagger UIs in `main.go`**

  ```go
  r.GET("/swagger/v1/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.InstanceName("v1")))
  r.GET("/swagger/v2/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.InstanceName("v2")))
  ```

- [ ] **Step 4: Commit**

  ```bash
  git add Makefile api-gateway/cmd/main.go api-gateway/docs/
  git commit -m "docs(api-gateway): split Swagger UI per API version"
  ```

---

### Task 5: Snapshot v1 response shapes as regression tests

**Files:**
- Create: `api-gateway/internal/router/v1_snapshot_test.go`
- Create: `api-gateway/internal/router/testdata/v1/*.json` (one fixture per route under test)

- [ ] **Step 1: Write a golden-file test that boots the gateway with mocked gRPC clients and hits every v1 route**

  For each route, the test records the JSON response (header + body shape, not dynamic IDs/timestamps) and compares against a fixture in `testdata/v1/`.

  ```go
  func TestV1RouteSnapshot(t *testing.T) {
      r := NewRouter()
      SetupV1Routes(r, mockAuth, mockUser, /* …other mocks… */)

      cases := []struct{ method, path string }{
          {"POST", "/api/v1/auth/login"},
          {"GET",  "/api/v1/me"},
          // …every route…
      }
      for _, tc := range cases {
          req := httptest.NewRequest(tc.method, tc.path, nil)
          w := httptest.NewRecorder()
          r.ServeHTTP(w, req)

          golden := filepath.Join("testdata/v1", sanitize(tc.path)+".json")
          if *update {
              os.WriteFile(golden, canonicalize(w.Body.Bytes()), 0644)
              continue
          }
          want, _ := os.ReadFile(golden)
          if !bytes.Equal(canonicalize(w.Body.Bytes()), want) {
              t.Fatalf("v1 %s drifted from snapshot\n%s", tc.path, diff(want, w.Body.Bytes()))
          }
      }
  }
  ```

- [ ] **Step 2: Record initial fixtures**

  ```bash
  cd api-gateway && go test ./internal/router/ -run TestV1RouteSnapshot -update
  ```

- [ ] **Step 3: Run the test without `-update` and confirm it passes**

  ```bash
  cd api-gateway && go test ./internal/router/ -run TestV1RouteSnapshot -v
  ```

  Expected: PASS.

- [ ] **Step 4: Commit**

  ```bash
  git add api-gateway/internal/router/v1_snapshot_test.go api-gateway/internal/router/testdata/
  git commit -m "test(api-gateway): golden snapshot for v1 response shapes"
  ```

---

### Task 6: Mark v1 docs + README as frozen

**Files:**
- Modify: `docs/api/REST_API_v1.md`
- Modify: `CLAUDE.md` (API versioning section)

- [ ] **Step 1: Add a header banner to `REST_API_v1.md`**

  ```markdown
  > **FROZEN:** This is the v1 snapshot. No new routes are added, and no existing
  > routes change. New features go to v2 (see REST_API_v2.md). To change v1 behavior,
  > you must explicitly lift the freeze.
  ```

- [ ] **Step 2: Add a paragraph to CLAUDE.md's "API Versioning Compatibility Requirement" section**

  Explain: v1 lives in `handler/v1/` and `router/router_v1_core.go`; v2 lives in `handler/` and `router/router_v2_core.go`. Modifying files in `handler/v1/` requires explicit user consent.

- [ ] **Step 3: Commit**

  ```bash
  git add docs/api/REST_API_v1.md CLAUDE.md
  git commit -m "docs: mark v1 as frozen snapshot"
  ```

---

## Self-Review Checklist

1. **v1 cannot drift**: after the split, a handler edit in `internal/handler/X.go` affects v2 only. v1 reads from `internal/handler/v1/X.go`.
2. **No user-visible change**: every v1 URL returns the same status code, body shape, and headers as before the split.
3. **No gRPC split**: v1 and v2 still call the same gRPC services. This is intentional — versioning the gRPC contract is a separate concern.
4. **Shared middleware is fine**: `AuthMiddleware`, `RequirePermission`, etc. are imported by both versions. Middleware is infrastructure, not public API.
5. **Swagger**: two UIs at `/swagger/v1/*` and `/swagger/v2/*`, each locked to its own generated tree.
6. **Tests**: `v1_snapshot_test.go` fails if anyone accidentally changes a v1 response shape.
7. **Rollback path**: if something breaks in prod, revert the Task-3 commit and v1/v2 share routes again.

---

## Risks & Open Questions

- **Handler duplication cost**: v1 handlers will diverge from v2 over time. That's the point, but it means bug fixes in v2 must be consciously ported (or not ported) to v1. The CLAUDE.md note should make this explicit.
- **Test fixture maintenance**: golden-file fixtures will need `-update` whenever a dependency returns a new enum value. Judge case-by-case whether that's a real drift or a compatible addition.
- **gRPC evolution**: if a gRPC response adds a new field, v1 may start returning it. Per CLAUDE.md "adding optional fields is non-breaking," so this is acceptable, but flag it in code review.
- **Swag tool limitations**: some `swag init` versions don't support `--instanceName`. If the tool doesn't, fall back to two separate binaries or a single merged docs tree with tag-based filtering.

---

## Execution Handoff

This plan is **not ready for execution**. Before running it:

1. Confirm with the user that the timing is right (no concurrent v1 work in flight).
2. Confirm that the golden-snapshot approach is acceptable (alternatives: no test, or hand-written assertion tests per route).
3. Confirm the `swag` flag names that actually work in our toolchain — the Makefile snippet above may need adjustment.

Once those three are settled, execute tasks in order 1 → 2 → 3 → 4 → 5 → 6. Tasks 1, 2, 3 must run sequentially (each depends on the previous); Tasks 4, 5, 6 can run in any order after Task 3.
