# Remote Negotiation Chain History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give remote (cross-bank) OTC negotiation chains the same full per-move revision history as local chains, recorded into the shared `otc_negotiation_revisions` table, surfaced by both the timeline and the per-chain `/revisions` endpoint, with the exact wire id of each mover.

**Architecture:** Remote mirror mutations route through new repository `…WithRevision` methods that append an `OTCNegotiationRevision` in the same transaction (idempotent). Reads expand a remote chain into one entry per revision (falling back to the current-terms snapshot for legacy chains). A new `action_by_wire_id` proto field carries the mover's opaque id.

**Tech Stack:** Go, GORM, sqlite (tests), protobuf, gRPC.

Spec: `docs/superpowers/specs/2026-06-06-remote-negotiation-chain-history-design.md`

---

### Task 1: Schema — `RemoteActorWireID` on the revision model

**Files:**
- Modify: `stock-service/internal/model/otc_negotiation_revision.go`

- [ ] **Step 1: Add the column**

Add to the `OTCNegotiationRevision` struct, after `ActingEmployeeID`:
```go
	// RemoteActorWireID is the opaque SI-TX wire id of the party who made this
	// move on a REMOTE chain ("client-<N>" / "employee-<N>" / "bank"). Nil on
	// LOCAL revisions (which identify the mover via ModifiedByPrincipalType/ID).
	RemoteActorWireID *string `gorm:"size:128" json:"remote_actor_wire_id,omitempty"`
```

- [ ] **Step 2: Build** — `cd stock-service && go build ./internal/model/` → no errors.

---

### Task 2: Proto — `action_by_wire_id`

**Files:**
- Modify: `contract/proto/stock/stock.proto`

- [ ] **Step 1:** In `message OTCNegotiationRevisionResponse`, after `created_at = 11;` add:
```proto
  string action_by_wire_id = 12; // remote chains: opaque mover id (client-N/employee-N/bank); empty for local
```

- [ ] **Step 2:** In `message OTCTimelineEntry`, after `created_at = 12;` add:
```proto
  string action_by_wire_id = 13; // remote chains: opaque mover id; empty for local
```

- [ ] **Step 3:** Regenerate — from repo root: `make proto`. Expected: `contract/stockpb/stock.pb.go` regenerated with `GetActionByWireId()` accessors.

- [ ] **Step 4:** Build — `cd stock-service && go build ./...` → no errors.

---

### Task 3: Repository `…WithRevision` methods + mover helper

**Files:**
- Modify: `stock-service/internal/repository/otc_negotiation_repository.go`
- Modify: `stock-service/internal/handler/peer_otc_grpc_handler.go` (add `remoteSideAtRouting` helper near `remoteBuyer`/`remoteSeller`)
- Test: `stock-service/internal/repository/otc_negotiation_revision_remote_test.go` (create)

The four methods append a revision in the same TX as the mutation. The `rev` template
carries `Action`, terms, `ModifiedByPrincipalType` (role), `ModifiedByPrincipalID=0`,
`RemoteActorWireID`. The repo sets `NegotiationID`, `RevisionNumber`, `CreatedAt`.

- [ ] **Step 1: Add a shared private appender + revision dedup helper**

In `otc_negotiation_repository.go`:
```go
// appendRemoteRevisionTx appends rev to the chain with surrogate id negID inside
// tx, numbering it NextRevisionNumber. Caller has already decided it should be
// written (idempotency handled by the *WithRevision wrappers).
func (r *OTCNegotiationRepository) appendRemoteRevisionTx(tx *gorm.DB, negID uint64, rev *model.OTCNegotiationRevision) error {
	n, err := r.NextRevisionNumber(tx, negID)
	if err != nil {
		return err
	}
	rev.NegotiationID = negID
	rev.RevisionNumber = n
	return tx.Create(rev).Error
}

// lastRevisionTx returns the most recent revision for negID, or (nil,nil) if none.
func (r *OTCNegotiationRepository) lastRevisionTx(tx *gorm.DB, negID uint64) (*model.OTCNegotiationRevision, error) {
	var rev model.OTCNegotiationRevision
	err := tx.Where("negotiation_id = ?", negID).Order("revision_number DESC").First(&rev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// sameRevisionMove reports whether a and b are the same move (retry) — equal
// action, terms, and remote actor wire id.
func sameRevisionMove(a *model.OTCNegotiationRevision, b *model.OTCNegotiationRevision) bool {
	if a == nil {
		return false
	}
	aw, bw := "", ""
	if a.RemoteActorWireID != nil {
		aw = *a.RemoteActorWireID
	}
	if b.RemoteActorWireID != nil {
		bw = *b.RemoteActorWireID
	}
	return a.Action == b.Action && aw == bw &&
		a.Quantity.Equal(b.Quantity) && a.StrikePrice.Equal(b.StrikePrice) &&
		a.Premium.Equal(b.Premium) && a.SettlementDate.Equal(b.SettlementDate)
}
```

- [ ] **Step 2: `UpsertRemoteNegWithRevision` (BID iff no revisions yet)**
```go
// UpsertRemoteNegWithRevision upserts a remote chain (same as UpsertRemoteNeg) and,
// when the chain has NO revisions yet, appends the supplied BID revision — all in
// one TX. A retried create (chain already has a BID) is a no-op for the revision.
func (r *OTCNegotiationRepository) UpsertRemoteNegWithRevision(n *model.OTCNegotiation, rev *model.OTCNegotiationRevision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txr := &OTCNegotiationRepository{db: tx}
		if err := txr.UpsertRemoteNeg(n); err != nil {
			return err
		}
		var row model.OTCNegotiation
		if err := tx.Where("routing_number = ? AND native_id = ? AND local = ?",
			n.RoutingNumber, n.NativeID, false).First(&row).Error; err != nil {
			return err
		}
		last, err := r.lastRevisionTx(tx, row.ID)
		if err != nil {
			return err
		}
		if last != nil {
			return nil // already has history → retry, no-op
		}
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
}
```
> NOTE: `UpsertRemoteNeg` uses `r.db` internally; construct a tx-scoped repo (`&OTCNegotiationRepository{db: tx}`) so the upsert runs inside the TX. Verify the struct's field is named `db` (it is).

- [ ] **Step 3: `UpdateRemoteNegOfferWithRevision` (COUNTER unless retry)**
```go
// UpdateRemoteNegOfferWithRevision updates the offer JSON and appends a COUNTER
// revision, unless the chain's latest revision is the SAME move (a retry).
func (r *OTCNegotiationRepository) UpdateRemoteNegOfferWithRevision(routing int64, native, offerJSON string, rev *model.OTCNegotiationRevision) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
			Updates(map[string]any{"remote_offer_json": offerJSON, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		last, err := r.lastRevisionTx(tx, row.ID)
		if err != nil {
			return err
		}
		if sameRevisionMove(last, rev) {
			return nil // retry → no-op
		}
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
}

// txGetRemoteNeg loads a remote chain by natural key inside tx.
func txGetRemoteNeg(tx *gorm.DB, routing int64, native string) (*model.OTCNegotiation, error) {
	var row model.OTCNegotiation
	err := tx.Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}
```

- [ ] **Step 4: `CompareAndSetRemoteNegStatusWithRevision` (ACCEPT on real transition)**
```go
// CompareAndSetRemoteNegStatusWithRevision CASes status from→to and, only when the
// CAS matched exactly one row (a real transition), appends rev. Returns whether it
// transitioned. Idempotent: a second call matches 0 rows → no revision.
func (r *OTCNegotiationRepository) CompareAndSetRemoteNegStatusWithRevision(routing int64, native, from, to string, rev *model.OTCNegotiationRevision) (bool, error) {
	var transitioned bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		res := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND status = ? AND local = ?", routing, native, from, false).
			Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return nil
		}
		transitioned = true
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
	return transitioned, err
}
```

- [ ] **Step 5: `SetRemoteNegStatusWithRevision` (REJECT on non-terminal→terminal)**
```go
// SetRemoteNegStatusWithRevision flips status to `to` and appends rev only when the
// chain is currently NON-terminal (a real party-driven terminal move). Idempotent.
func (r *OTCNegotiationRepository) SetRemoteNegStatusWithRevision(routing int64, native, to string, rev *model.OTCNegotiationRevision) (bool, error) {
	var transitioned bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		row, err := txGetRemoteNeg(tx, routing, native)
		if err != nil {
			return err
		}
		if row.Status != "ongoing" { // remote non-terminal vocabulary
			return nil
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			Model(&model.OTCNegotiation{}).
			Where("routing_number = ? AND native_id = ? AND local = ?", routing, native, false).
			Updates(map[string]any{"status": to, "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		transitioned = true
		return r.appendRemoteRevisionTx(tx, row.ID, rev)
	})
	return transitioned, err
}
```

- [ ] **Step 6: `remoteSideAtRouting` helper** (in `peer_otc_grpc_handler.go`, near `remoteSeller`)
```go
// remoteSideAtRouting returns (role, wireID) for whichever side (buyer/seller) of
// the remote chain is hosted at the given routing. ("","") if neither matches.
func remoteSideAtRouting(n *model.OTCNegotiation, routing int64) (string, string) {
	bR, bID := remoteBuyer(n)
	sR, sID := remoteSeller(n)
	if routing == bR {
		return "buyer", bID
	}
	if routing == sR {
		return "seller", sID
	}
	return "", ""
}
```

- [ ] **Step 7: Tests** — `otc_negotiation_revision_remote_test.go`. Use an in-memory sqlite with `OTCNegotiation` + `OTCNegotiationRevision` migrated (mirror `newNegTestEnv`). Cases:
  - `UpsertRemoteNegWithRevision` twice with the same natural key ⇒ exactly **one** BID revision (rev 1).
  - `UpdateRemoteNegOfferWithRevision` with new terms ⇒ appends COUNTER; calling again with identical (terms, wireID) ⇒ no-op (still 1 COUNTER); different wireID, same terms ⇒ appends.
  - `CompareAndSetRemoteNegStatusWithRevision(ongoing→accepted)` ⇒ appends ACCEPT, returns true; second call ⇒ false, no new rev.
  - `SetRemoteNegStatusWithRevision(→cancelled)` on ongoing ⇒ appends REJECT, true; on already-cancelled ⇒ false, no rev.
  - Revision numbers are gap-free (1,2,3…).

- [ ] **Step 8:** Run: `cd stock-service && go test ./internal/repository/ -run RemoteRevision -v` → PASS.

- [ ] **Step 9: Commit** — `git add -A && git commit -m "feat(stock): atomic revision logging for remote negotiation chains (repo)"`

---

### Task 4: Outbound — log BID/COUNTER/ACCEPT/REJECT

**Files:**
- Modify: `stock-service/internal/handler/otc_negotiation_remote.go` (open → BID)
- Modify: `stock-service/internal/handler/otc_negotiation_remote_action.go` (counter/accept/reject)
- Modify: `stock-service/internal/handler/otc_options_handler.go` (extend the `remoteNegOps`/`remoteNegWriter` interfaces with the new methods)

- [ ] **Step 1: Extend the handler interfaces** in `otc_options_handler.go`:
  - Add to the `remoteNegWriter` interface: `UpsertRemoteNegWithRevision(n *model.OTCNegotiation, rev *model.OTCNegotiationRevision) error`
  - Add to the `remoteNegOps` interface: `UpdateRemoteNegOfferWithRevision(routing int64, native, offerJSON string, rev *model.OTCNegotiationRevision) error`, `CompareAndSetRemoteNegStatusWithRevision(routing int64, native, from, to string, rev *model.OTCNegotiationRevision) (bool, error)`, `SetRemoteNegStatusWithRevision(routing int64, native, to string, rev *model.OTCNegotiationRevision) (bool, error)`

- [ ] **Step 2: `openRemoteNegotiation`** — replace `h.remoteNegWriter.UpsertRemoteNeg(mirror)` with `UpsertRemoteNegWithRevision(mirror, rev)`, where:
```go
	bidRev := &model.OTCNegotiationRevision{
		Quantity: qty, StrikePrice: strike, Premium: premium, SettlementDate: settle,
		Action: model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer", // we are the buyer on an outbound bid
		RemoteActorWireID: &buyerID,
	}
	if err := h.remoteNegWriter.UpsertRemoteNegWithRevision(mirror, bidRev); err != nil { ... }
```

- [ ] **Step 3: `counterRemoteNegotiation`** — replace `UpdateRemoteNegOffer` with `UpdateRemoteNegOfferWithRevision`, role/wireID from our hosted side:
```go
	role, wireID := remoteSideAtRouting(rc.row, h.ownRouting) // we host one side
	counterRev := &model.OTCNegotiationRevision{
		Quantity: qty, StrikePrice: strike, Premium: premium, SettlementDate: settle,
		Action: model.OTCNegotiationActionCounter,
		ModifiedByPrincipalType: role, RemoteActorWireID: &wireID,
	}
	if err := h.remoteNegOps.UpdateRemoteNegOfferWithRevision(rc.row.RoutingNumber, rc.foreignID, string(mirrorJSON), counterRev); err != nil { ... }
```

- [ ] **Step 4: `acceptRemoteNegotiation`** — replace `CompareAndSetRemoteNegStatus(...,"ongoing","accepted")` with the `…WithRevision` variant; terms from `rc.offer` (current snapshot), role/wireID from our side:
```go
	role, wireID := remoteSideAtRouting(rc.row, h.ownRouting)
	acceptRev := &model.OTCNegotiationRevision{
		Quantity: decimal.NewFromInt(rc.offer.Amount), StrikePrice: rc.offer.PricePerStock,
		Premium: rc.offer.Premium, SettlementDate: parseSettle(rc.offer.SettlementDate),
		Action: model.OTCNegotiationActionAccept,
		ModifiedByPrincipalType: role, RemoteActorWireID: &wireID,
	}
	if _, serr := h.remoteNegOps.CompareAndSetRemoteNegStatusWithRevision(rc.row.RoutingNumber, rc.foreignID, "ongoing", "accepted", acceptRev); serr != nil { ... }
```
> Add a small `parseSettle(string) time.Time` helper (RFC3339 then date-only; reuse the pattern already in `buildRemoteNeg`), or inline the parse.

- [ ] **Step 5: `cancelRemoteNegotiation`** — it serves BOTH reject and cancel. Add a parameter so only a REJECT records a revision. Change signature to `cancelRemoteNegotiation(ctx, rc, recordReject bool)`; callers: `RejectNegotiation` passes `true`, `CancelNegotiation` passes `false`. When `recordReject`, use `SetRemoteNegStatusWithRevision(...,"cancelled", rejectRev)` (role/wireID from our side, terms from `rc.offer`, `Action: model.OTCNegotiationActionReject`); otherwise keep `UpdateRemoteNegStatus(...,"cancelled")`.

- [ ] **Step 6: Build** — `cd stock-service && go build ./...` → fix any interface/sig mismatches.

- [ ] **Step 7: Commit** — `git commit -am "feat(stock): record outbound remote bid/counter/accept/reject revisions"`

---

### Task 5: Inbound — log BID/COUNTER/ACCEPT/REJECT

**Files:**
- Modify: `stock-service/internal/handler/peer_otc_grpc_handler.go` (`CreateNegotiation`, `UpdateNegotiation`, `DeleteNegotiation`, inbound accept at ~L930)

The inbound handlers use `h.negRepo` (the concrete `*OTCNegotiationRepository`), so they can call the new methods directly.

- [ ] **Step 1: `CreateNegotiation`** — replace `h.negRepo.UpsertRemoteNeg(neg)` with `UpsertRemoteNegWithRevision(neg, bidRev)`:
```go
	buyerWire := req.GetBuyerId().GetId()
	bidRev := &model.OTCNegotiationRevision{
		Quantity: decimal.NewFromInt(offer.Amount), StrikePrice: offer.PricePerStock,
		Premium: offer.Premium, SettlementDate: parseSettle(offer.SettlementDate),
		Action: model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer", RemoteActorWireID: &buyerWire,
	}
```

- [ ] **Step 2: `UpdateNegotiation`** — replace `UpdateRemoteNegOffer` with `UpdateRemoteNegOfferWithRevision`. The mover is the authenticated peer (`peerRouting`); read role/wireID from the just-loaded `existing` row:
```go
	role, wireID := remoteSideAtRouting(existing, peerRouting)
	counterRev := &model.OTCNegotiationRevision{
		Quantity: decimal.NewFromInt(offer.Amount), StrikePrice: offer.PricePerStock,
		Premium: offer.Premium, SettlementDate: parseSettle(offer.SettlementDate),
		Action: model.OTCNegotiationActionCounter,
		ModifiedByPrincipalType: role, RemoteActorWireID: &wireID,
	}
```

- [ ] **Step 3: inbound accept (~L930)** — replace `CompareAndSetRemoteNegStatus(...,"ongoing","accepted")` with the `…WithRevision` variant. Mover = the peer (`peerRouting`); load the row first to read role/wireID + terms.

- [ ] **Step 4: `DeleteNegotiation`** — when it's a real terminal (it already loads `row`), use `SetRemoteNegStatusWithRevision(...,"cancelled", rejectRev)` with role/wireID = `remoteSideAtRouting(row, peerRouting)`, terms from the row's parsed offer, `Action: REJECT`. Keep behavior identical when `row`/`gerr` is missing (fall back to the plain `UpdateRemoteNegStatus`).

- [ ] **Step 5: Build + existing tests** — `cd stock-service && go build ./... && go test ./internal/handler/ -run 'PeerOTC|Negotiation' 2>&1 | tail` → green.

- [ ] **Step 6: Commit** — `git commit -am "feat(stock): record inbound remote bid/counter/accept/reject revisions"`

---

### Task 6: Read path — timeline expands remote chains into per-revision entries

**Files:**
- Modify: `stock-service/internal/handler/otc_negotiation_handler.go` (`GetOfferTimeline` B2 merge + `remoteOfferTimeline`)
- Modify: `stock-service/internal/handler/otc_options_handler.go` (`peerNegs` interface needs a revisions lister) OR reuse `h.negotiations.ListRevisions`-style access
- Test: `stock-service/internal/handler/otc_negotiation_local_remote_parity_test.go` (extend)

- [ ] **Step 1: Add a revisions accessor for remote chains.** The handler already has `h.negotiations` (the service). Add a thin service method `ListRevisionsUnchecked(negID uint64) ([]model.OTCNegotiationRevision, error)` that returns `negRepo.ListRevisions(negID)` with NO auth (the caller already authorized via the remote lot-key match). Use it from the timeline merges.

- [ ] **Step 2: Factor a `remoteChainTimelineEntries(row)` helper** in `otc_negotiation_handler.go`:
```go
// remoteChainTimelineEntries expands a remote chain into timeline entries: one per
// recorded revision (full history). Falls back to a single current-terms entry when
// the chain has no revisions yet (legacy rows created before history logging).
func (h *OTCOptionsHandler) remoteChainTimelineEntries(row *model.OTCNegotiation) []*stockpb.OTCTimelineEntry {
	revs, _ := h.negotiations.ListRevisionsUnchecked(row.ID)
	if len(revs) > 0 {
		out := make([]*stockpb.OTCTimelineEntry, 0, len(revs))
		for i := range revs {
			r := &revs[i]
			wire := ""
			if r.RemoteActorWireID != nil {
				wire = *r.RemoteActorWireID
			}
			out = append(out, &stockpb.OTCTimelineEntry{
				NegotiationId: row.ID,
				RevisionNumber: int32(r.RevisionNumber),
				Action: r.Action,
				Quantity: r.Quantity.String(), StrikePrice: r.StrikePrice.String(),
				Premium: r.Premium.String(), SettlementDate: r.SettlementDate.UTC().Format(time.RFC3339),
				ActionByPrincipalType: r.ModifiedByPrincipalType,
				ActionByWireId: wire,
				CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		return out
	}
	// Legacy fallback: single current-terms entry from RemoteOfferJSON.
	var off contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(remoteOfferJSONOf(row)), &off)
	return []*stockpb.OTCTimelineEntry{{
		NegotiationId: row.ID, Quantity: strconv.FormatInt(off.Amount, 10),
		StrikePrice: off.PricePerStock.String(), Premium: off.Premium.String(),
		SettlementDate: off.SettlementDate, Action: "COUNTER",
		CreatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}}
}
```

- [ ] **Step 3:** In `GetOfferTimeline` (Task B2 block) replace the single-entry-per-row loop with `timeline = append(timeline, h.remoteChainTimelineEntries(row)...)`. Keep the final `sort.SliceStable` by `created_at`.

- [ ] **Step 4:** In `remoteOfferTimeline` replace the single-entry-per-row append with `timeline = append(timeline, h.remoteChainTimelineEntries(row)...)`, then add a `sort.SliceStable(timeline, ...)` by `created_at` so multi-revision chains are chronological.

- [ ] **Step 5: Test** — extend the parity test: a production-faithful remote listing where a chain has 3 revisions (BID, COUNTER, COUNTER) seeded into the revisions table ⇒ the timeline returns 3 ordered entries with the right `action`/`action_by_wire_id`; a chain with 0 revisions ⇒ 1 fallback entry.

- [ ] **Step 6:** Run `cd stock-service && go test ./internal/handler/ -run 'TestParity|Timeline' -v` → PASS.

- [ ] **Step 7: Commit** — `git commit -am "feat(stock): timeline expands remote chains into full revision history"`

---

### Task 7: `/revisions` endpoint — remote-aware authorization + wire id

**Files:**
- Modify: `stock-service/internal/handler/otc_negotiation_handler.go` (`ListNegotiationRevisions`)
- Test: `stock-service/internal/handler/otc_negotiation_local_remote_parity_test.go`

- [ ] **Step 1:** In `ListNegotiationRevisions`, after the local `h.negotiations.ListRevisions(...)` call, on `isOTCNegotiationNotFound(err)` add a remote fallback: resolve the chain via `resolveRemoteNegAction(in.GetNegotiationId(), ot, oid)` (it authorizes the caller as the hosted party and returns the row). On `ok`, return `negRepo.ListRevisions(rc.row.ID)` mapped to `OTCNegotiationRevisionResponse`, populating `ActionByWireId` from `RemoteActorWireID` (empty when nil) and `ActionByPrincipalType` from `ModifiedByPrincipalType`. A non-party caller already gets `NotFound` from `resolveRemoteNegAction`.

- [ ] **Step 2:** Map `RemoteActorWireID` in the LOCAL mapping path too — set `ActionByWireId: ""` for local revisions (the field stays empty), so the response shape is uniform.

- [ ] **Step 3: Test** — production-faithful remote chain with seeded revisions: the hosted client (and the bank) can `ListNegotiationRevisions` and get the full list with `action_by_wire_id`; a non-party caller gets `NotFound`.

- [ ] **Step 4:** Run `cd stock-service && go test ./internal/handler/ -run 'Revisions' -v` → PASS.

- [ ] **Step 5: Commit** — `git commit -am "feat(stock): /revisions endpoint returns full remote chain history"`

---

### Task 8: Docs + VERSION + full verify

**Files:**
- Modify: `docs/api/REST_API_v3.md` (timeline + `/revisions`: full remote history; document `action_by_wire_id`)
- Modify: `VERSION` (2.12.1 → 2.13.0)
- Modify: `api-gateway/internal/version/version.go` (sync to 2.13.0)

- [ ] **Step 1:** Update the timeline section: remote chains now return one entry per recorded move (BID/COUNTER/ACCEPT/REJECT) with `action_by_wire_id`; legacy chains fall back to one current-terms entry. Update the `/revisions` (`ListNegotiationRevisions`) section: now returns full history for remote chains too. Add `action_by_wire_id` to the documented entry fields.
- [ ] **Step 2:** `printf '2.13.0' > VERSION`; set `var Version = "2.13.0"`.
- [ ] **Step 3: Full verify** — from repo root: `cd stock-service && go build ./... && go test ./... 2>&1 | tail` (all `ok`); `golangci-lint run ./internal/...` (exit 0); `cd ../api-gateway && go build ./...`.
- [ ] **Step 4: Commit** — `git commit -am "docs+chore: remote negotiation history docs + VERSION 2.13.0"`

---

## Self-review notes
- Spec coverage: schema (T1), proto (T2), atomic+idempotent repo methods (T3), outbound logging (T4), inbound logging (T5), timeline full-history read + fallback (T6), `/revisions` remote parity (T7), docs+version (T8). All spec sections covered.
- Parity: records exactly BID/COUNTER/ACCEPT/REJECT (matches local); cascade/reconciler cancels keep plain status updates (no revision), matching local.
- Idempotency: BID-iff-no-revisions, COUNTER-dedup-on-last-move, terminal-on-real-transition.
