# Notification Template Management — Design

**Date:** 2026-05-15
**Status:** Approved (brainstorming)
**Scope:** Spec 3a of the 2026-05-14 request. Spec 1 (OTC client access) and Spec 2 (employee bank-account activity) are done. **Spec 3 was split into 3a (this — template management + discovery) and 3b (notification-coverage expansion).** 3b builds on 3a's registry and gets its own spec→plan→implementation cycle.

## Problem

Email bodies are hardcoded Go strings in `notification-service/internal/sender/templates.go` — a `BuildEmail()` switch with one `fmt.Sprintf` case per `EmailType`. There is no way for a bank admin to customize the wording of an activation email, a loan-approval email, etc. There is also no surface telling an admin *which variables* a given template can reference. The user wants: an admin API to edit email (and, as the surface grows, push) templates, and a discovery endpoint that lists which variables each template type supports.

## Goals

- Admins (EmployeeAdmin) can view and customize the subject/body of every notification template type.
- A discovery endpoint lists every template type with its channel, description, and the variables it supports — so a UI can show "available variables" next to an editor.
- Customizations are stored per `(type, channel)` and override the code default; reverting deletes the override.
- Substitution uses `{{variable_name}}` placeholders.
- The infrastructure is channel-agnostic (`email` | `push`) so 3b can register push types without re-plumbing.

## Non-goals

- Creating *new* template types or *new* variables — those are code-defined (a template type's variables are exactly the keys its publishing service puts in the `Data` map). Admins customize text, they do not invent types.
- Notification-coverage expansion (wiring the ~25 unconsumed Kafka events) — that is Spec 3b.
- Converting existing hardcoded in-app (`GeneralNotification`) messages to templates — 3b territory; 3a builds the push infra but push *types* are registered as 3b creates them.
- Rich-text / HTML editing UX, template versioning/history — YAGNI.

## Key facts established during exploration

- 13 `EmailType` constants exist in `contract/kafka/messages.go`; `BuildEmail()` in `notification-service/internal/sender/templates.go` renders each via `fmt.Sprintf`, reading specific keys from a `Data map[string]string`. Those keys *are* the variable set for each type.
- `SendEmailMessage{To, EmailType, Data}` is the Kafka contract; `EmailConsumer` calls `BuildEmail`. `VerificationConsumer` also calls `BuildEmail` for the email-delivery path.
- notification-service already has a PostgreSQL DB (`AutoMigrate` in `cmd/main.go`) and a gRPC `NotificationService` (8 RPCs) with a handler in `internal/handler/grpc_handler.go`.
- api-gateway already proxies notification RPCs (`h.Notification`, routes under `/api/v3/me/notifications`).
- Permissions are code-generated from `contract/permissions/catalog.yaml` via `make permissions`.

## Design

### 1. Template model — registry + overrides

**Code-defined registry** — `notification-service/internal/templates/registry.go`. For each notification type:

```go
type TemplateVariable struct {
	Name        string // "first_name"
	Description string // "Recipient's first name"
	Example     string // "Ana"
}

type TemplateType struct {
	Type           string             // "ACTIVATION" — matches the EmailType string
	Channel        string             // "email" | "push"
	Description    string             // "Sent when an employee account is created"
	Variables      []TemplateVariable
	DefaultSubject string             // seeded from the current BuildEmail switch
	DefaultBody    string             // current body, rewritten with {{var}} placeholders
}
```

The registry is a package-level slice/map built at init. The 13 email types seed it: each `BuildEmail` switch case's subject/body string is rewritten from `fmt.Sprintf("...%s...", data["x"])` form to `"...{{x}}..."` form and moved into the registry as `DefaultSubject`/`DefaultBody`. Each type's `Variables` list is the set of `Data` keys that case reads today.

**DB override table** — `notification-service/internal/model/notification_template.go`:

```go
type NotificationTemplate struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Type      string    `gorm:"size:64;not null;uniqueIndex:ux_tmpl_type_channel,priority:1" json:"type"`
	Channel   string    `gorm:"size:16;not null;uniqueIndex:ux_tmpl_type_channel,priority:2" json:"channel"`
	Subject   string    `gorm:"type:text;not null" json:"subject"`
	Body      string    `gorm:"type:text;not null" json:"body"`
	UpdatedBy uint64    `gorm:"not null" json:"updated_by"` // employee principal id, audit
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

A row exists only when an admin has customized that `(type, channel)`. Absence ⇒ the registry default is authoritative. Added to `AutoMigrate` in `cmd/main.go`.

### 2. Rendering — the substitution engine

`notification-service/internal/service/template_service.go` exposes:

```go
func (s *TemplateService) Render(typ, channel string, data map[string]string) (subject, body string, err error)
```

1. Look up the DB override for `(typ, channel)`. If found → use its `Subject`/`Body`.
2. Else → look up the registry default. Not in registry → return an error.
3. Substitute: for both subject and body, replace every `{{token}}` match of `\{\{(\w+)\}\}` with `data[token]`. A token whose key is absent from `data` is replaced with the empty string (defensive — a stale override never ships a literal `{{x}}` to a user).

`BuildEmail` in `sender/templates.go` is **deleted**. `EmailConsumer` and `VerificationConsumer` call `templateService.Render(string(msg.EmailType), "email", msg.Data)` instead. The `sender` package keeps only the SMTP `Send(to, subject, body)` function.

### 3. API — CRUD + discovery

**Four new RPCs** on `NotificationService` (`contract/proto/notification/notification.proto`):

- `ListTemplates(ListTemplatesRequest{channel}) → ListTemplatesResponse{templates: []TemplateInfo}` — `channel` empty means all. **The discovery endpoint.**
- `GetTemplate(GetTemplateRequest{type, channel}) → TemplateInfo`
- `SetTemplate(SetTemplateRequest{type, channel, subject, body, updated_by}) → TemplateInfo` — upsert override.
- `ResetTemplate(ResetTemplateRequest{type, channel}) → TemplateInfo` — delete override, return the now-default entry.

`TemplateInfo` carries: `type`, `channel`, `description`, `variables` (repeated `{name, description, example}`), `default_subject`, `default_body`, `current_subject`, `current_body`, `is_customized` (bool). `current_*` = the override if present, else the default.

**REST routes** — api-gateway, new `/api/v3/notification-templates` group, all gated by `RequirePermission(perms.Notifications.Templates.Manage)`:

| Method | Route | Purpose |
|--------|-------|---------|
| `GET` | `/api/v3/notification-templates` (optional `?channel=email\|push`) | List every type with variables + defaults + current text |
| `GET` | `/api/v3/notification-templates/:channel/:type` | Single type detail |
| `PUT` | `/api/v3/notification-templates/:channel/:type` | Set override — body `{ "subject": "...", "body": "..." }` |
| `DELETE` | `/api/v3/notification-templates/:channel/:type` | Reset to the registry default |

No `POST` — types are code-defined. `updated_by` is taken from the JWT principal, never the request body. Handler lives in a new `api-gateway/internal/handler/notification_template_handler.go`.

### 4. Validation

Enforced gateway-side (per CLAUDE.md) and mirrored in the notification-service so the gRPC boundary is also safe. On `SetTemplate`:

- `channel` must be `email` or `push` — `400 validation_error` via the `oneOf()` helper.
- `(type, channel)` must exist in the registry — `404 not_found` if not.
- `subject` and `body` non-empty — `400`.
- **Placeholder check** — extract every `{{token}}` from subject + body; each token must be a known variable for that type. Unknown token → `400 validation_error` with the offending tokens in `details` (e.g. `{"unknown_variables": ["acount_number"]}`). Unused variables are allowed; only unknown ones are rejected.

`GetTemplate` / `ResetTemplate` validate `(type, channel)` exists → `404` otherwise.

### 5. Permission

New permission `notifications.templates.manage` in `contract/permissions/catalog.yaml`, granted to `EmployeeAdmin` only. Regenerated via `make permissions` → `perms.Notifications.Templates.Manage`. All four routes (including the discovery `GET`) sit behind it — this is an admin tool, not a public catalog.

## Concurrency & safety

- `SetTemplate` is an upsert on `(type, channel)` — implemented with GORM `clause.OnConflict{}` (per CLAUDE.md's upsert rule), so concurrent admin edits don't race into duplicate rows.
- `NotificationTemplate` has no `Version` field — it is not a money/ledger entity and last-write-wins is acceptable for admin-edited copy. (No optimistic-lock requirement.)
- `Render` is a read; under concurrent edits it sees either the old or new override row — both are valid renders. No caching in 3a (email volume is low, the lookup is one indexed row); a cache can be added later if needed.

## Testing

**Unit:**
- *Registry* — all 13 email types present; each has a valid channel, a non-empty variable list, non-empty default subject/body; every `{{var}}` in a default body is in that type's variable list (registry self-consistency).
- *`Render`* — override wins over default; default used when no override; `{{var}}` substituted from `data`; leftover `{{unknown}}` → empty string; missing `data` key → empty; unknown type → error.
- *`SetTemplate` validation* — unknown type → 404; bad channel → 400; empty subject/body → 400; unknown placeholder token → 400 with `details`; valid → upserts and round-trips; second `SetTemplate` on the same `(type, channel)` updates, doesn't duplicate.
- *`ResetTemplate`* — deletes the override row; subsequent `Render` uses the default.
- *gRPC handler* — the 4 RPCs against an in-memory SQLite DB (`testutil.SetupTestDB`).
- *Gateway handler* — the 4 routes: success shapes, validation errors, permission gating (non-admin → 403).

**Integration (`test-app/workflows/`):**
- Admin `GET`s `/notification-templates` → sees all types, each with a `variables` array.
- Admin `PUT`s an override for one type, `GET`s it back → `is_customized: true`, `current_body` matches; `DELETE` → `is_customized: false`.
- Non-admin (client or basic employee) → `403` on all four routes.

## Docs to update

- `docs/Specification.md` — new section for notification templates; the `NotificationTemplate` entity; the 4 new RPCs; the 4 new routes (§17); the new `notifications.templates.manage` permission (§6).
- `docs/api/REST_API_v3.md` — new section documenting the 4 endpoints.
- Swagger annotations on the new gateway handler + `make swagger`.
- No `docker-compose.yml` change — notification-service already has its DB; no new env var or service.

## File map

**New:**
- `notification-service/internal/templates/registry.go` — code-defined registry + lookup helpers
- `notification-service/internal/model/notification_template.go` — DB override model
- `notification-service/internal/repository/template_repository.go` — override CRUD (upsert via `OnConflict`)
- `notification-service/internal/service/template_service.go` — `Render` + `List`/`Get`/`Set`/`Reset` with validation
- `api-gateway/internal/handler/notification_template_handler.go` — 4 REST handlers

**Modified:**
- `contract/proto/notification/notification.proto` — 4 RPCs + messages (+regen `notificationpb`)
- `contract/permissions/catalog.yaml` — `notifications.templates.manage` (+regen `perms.gen.go`)
- `notification-service/internal/sender/templates.go` — delete `BuildEmail`; keep only SMTP `Send`
- `notification-service/internal/consumer/email_consumer.go` — call `templateService.Render`
- `notification-service/internal/consumer/verification_consumer.go` — call `templateService.Render` on the email path
- `notification-service/internal/handler/grpc_handler.go` — 4 new RPC handlers
- `notification-service/cmd/main.go` — `AutoMigrate(&model.NotificationTemplate{})`, construct the registry + repository + `TemplateService`, inject into the consumers and the gRPC handler
- `api-gateway/internal/router/router_v3.go` — 4 new routes + handler wiring
- `docs/Specification.md`, `docs/api/REST_API_v3.md`, `api-gateway/docs/*`

## Open questions

None. Placeholder syntax (`{{variable_name}}`), the email-now/push-as-it-appears scope, and the 3a/3b split were settled during brainstorming.
