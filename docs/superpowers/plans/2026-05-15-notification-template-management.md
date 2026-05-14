# Notification Template Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let bank admins view and customize notification template subject/body text, with a discovery endpoint that lists which `{{variable}}` placeholders each template type supports.

**Architecture:** A code-defined registry in notification-service maps each notification type → channel + variable list + default subject/body. A DB override table holds admin customizations per `(type, channel)`. A `TemplateService.Render` resolves override-or-default and substitutes `{{var}}` placeholders. Four new gRPC RPCs (List/Get/Set/Reset) are proxied by four new admin-gated REST routes.

**Tech Stack:** Go, gRPC/protobuf (`contract/notificationpb`), GORM + PostgreSQL (notification-service), Gin (api-gateway), golangci-lint, the repo's `make` targets.

**Source spec:** `docs/superpowers/specs/2026-05-15-notification-template-management-design.md`

---

## File Structure

**Created:**
- `notification-service/internal/templates/registry.go` — code-defined registry: `Definition`, `Variable`, the 13 email entries, `All()`, `Get()`, `KnownVars()`
- `notification-service/internal/templates/registry_test.go`
- `notification-service/internal/model/notification_template.go` — DB override model
- `notification-service/internal/repository/template_repository.go` — override CRUD (upsert via `OnConflict`)
- `notification-service/internal/repository/template_repository_test.go`
- `notification-service/internal/service/template_service.go` — `Render` + `List`/`GetOne`/`Set`/`Reset` + validation
- `notification-service/internal/service/template_service_test.go`
- `notification-service/internal/handler/template_grpc_test.go` — tests for the 4 new RPCs
- `api-gateway/internal/handler/notification_template_handler.go` — 4 REST handlers (methods on `*NotificationHandler`)
- `api-gateway/internal/handler/notification_template_handler_test.go`
- `test-app/workflows/notification_templates_test.go` — integration tests

**Modified:**
- `contract/proto/notification/notification.proto` — 4 RPCs + 6 messages (+regen `notificationpb`)
- `contract/permissions/catalog.yaml` — `notifications.templates.manage` (+regen `perms.gen.go`)
- `notification-service/internal/sender/templates.go` — **delete `BuildEmail`** (file becomes empty → delete the file)
- `notification-service/internal/consumer/email_consumer.go` — call `templateSvc.Render` instead of `sender.BuildEmail`
- `notification-service/internal/consumer/verification_consumer.go` — call `templateSvc.Render` on the email path
- `notification-service/internal/handler/grpc_handler.go` — `SendEmail` calls `templateSvc.Render`; add the 4 template RPCs; struct gains `templateSvc`
- `notification-service/cmd/main.go` — `AutoMigrate(&model.NotificationTemplate{})`, construct registry-backed repo + `TemplateService`, inject into consumers + handler
- `api-gateway/internal/router/router_v3.go` — 4 new routes
- `docs/Specification.md`, `docs/api/REST_API_v3.md`, `api-gateway/docs/*`

> **Build-order note:** the proto change (Task 2) and the `BuildEmail` deletion (Task 8) leave notification-service and api-gateway temporarily non-compiling. The phases are ordered so the cascade resolves; run `make build` only at the end of Task 11.

---

## Phase 1 — Permission

### Task 1: Add `notifications.templates.manage` permission

**Files:**
- Modify: `contract/permissions/catalog.yaml`
- Regenerate: `contract/permissions/perms.gen.go`

- [ ] **Step 1: Add the permission to the catalog**

In `contract/permissions/catalog.yaml`, the flat permission list has an `# exchange` block near the end:

```yaml
  # exchange
  - exchange_rates.read.any
  - exchange_rates.update.any

default_roles:
```

Insert a `# notifications` block right before `default_roles:`:

```yaml
  # exchange
  - exchange_rates.read.any
  - exchange_rates.update.any

  # notifications
  - notifications.templates.manage

default_roles:
```

`EmployeeAdmin` grants `"*"`, so it picks this up automatically — no role-block edit needed.

- [ ] **Step 2: Regenerate**

Run: `make permissions`
Expected: `contract/permissions/perms.gen.go` rewritten; `git diff` shows a new `Notifications` struct with `Templates.Manage` = `"notifications.templates.manage"`.

- [ ] **Step 3: Verify it compiles**

Run: `cd contract && go build ./...`
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add contract/permissions/catalog.yaml contract/permissions/perms.gen.go
git commit -m "feat(perms): add notifications.templates.manage permission"
```

---

## Phase 2 — Proto

### Task 2: Add template RPCs to `notification.proto`

**Files:**
- Modify: `contract/proto/notification/notification.proto`
- Regenerate: `contract/notificationpb/*.pb.go`

- [ ] **Step 1: Add the 4 RPCs to the service block**

In `contract/proto/notification/notification.proto`, the service block ends:

```proto
  // General notifications (persistent, read/unread, no expiry)
  rpc ListNotifications(ListNotificationsRequest) returns (ListNotificationsResponse);
  rpc GetUnreadCount(GetUnreadCountRequest) returns (GetUnreadCountResponse);
  rpc MarkNotificationRead(MarkNotificationReadRequest) returns (MarkNotificationReadResponse);
  rpc MarkAllNotificationsRead(MarkAllNotificationsReadRequest) returns (MarkAllNotificationsReadResponse);
}
```

Change to:

```proto
  // General notifications (persistent, read/unread, no expiry)
  rpc ListNotifications(ListNotificationsRequest) returns (ListNotificationsResponse);
  rpc GetUnreadCount(GetUnreadCountRequest) returns (GetUnreadCountResponse);
  rpc MarkNotificationRead(MarkNotificationReadRequest) returns (MarkNotificationReadResponse);
  rpc MarkAllNotificationsRead(MarkAllNotificationsReadRequest) returns (MarkAllNotificationsReadResponse);

  // Notification templates (admin-managed copy + variable discovery)
  rpc ListTemplates(ListTemplatesRequest) returns (ListTemplatesResponse);
  rpc GetTemplate(GetTemplateRequest) returns (TemplateInfo);
  rpc SetTemplate(SetTemplateRequest) returns (TemplateInfo);
  rpc ResetTemplate(ResetTemplateRequest) returns (TemplateInfo);
}
```

- [ ] **Step 2: Add the messages at the end of the file**

Append to `contract/proto/notification/notification.proto`:

```proto

// ── Notification templates ───────────────────────────────────────────────────

message TemplateVariable {
  string name = 1;
  string description = 2;
  string example = 3;
}

message TemplateInfo {
  string type = 1;
  string channel = 2;
  string description = 3;
  repeated TemplateVariable variables = 4;
  string default_subject = 5;
  string default_body = 6;
  string current_subject = 7;
  string current_body = 8;
  bool is_customized = 9;
}

message ListTemplatesRequest {
  string channel = 1; // "" = all, "email", or "push"
}

message ListTemplatesResponse {
  repeated TemplateInfo templates = 1;
}

message GetTemplateRequest {
  string type = 1;
  string channel = 2;
}

message SetTemplateRequest {
  string type = 1;
  string channel = 2;
  string subject = 3;
  string body = 4;
  uint64 updated_by = 5;
}

message ResetTemplateRequest {
  string type = 1;
  string channel = 2;
}
```

- [ ] **Step 3: Regenerate**

Run: `make proto`
Expected: `contract/notificationpb/notification.pb.go` and `notification_grpc.pb.go` rewritten with the new types/RPCs.

- [ ] **Step 4: Confirm the contract module builds**

Run: `cd contract && go build ./...`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
git add contract/proto/notification/notification.proto contract/notificationpb/
git commit -m "feat(proto): notification template management RPCs"
```

---

## Phase 3 — notification-service registry

### Task 3: The code-defined template registry

**Files:**
- Create: `notification-service/internal/templates/registry.go`
- Create: `notification-service/internal/templates/registry_test.go`

- [ ] **Step 1: Write the registry**

Create `notification-service/internal/templates/registry.go`. The 13 entries are the current `BuildEmail` cases, with each `fmt.Sprintf` body rewritten to `{{variable}}` form (the `%%` HTML-width escapes become plain `%` in these raw strings):

```go
// Package templates holds the code-defined notification template registry.
// A template type's set of variables is fixed by the publishing service —
// admins customize the subject/body text, they do not invent types or
// variables. The registry seeds the default text; the DB override table
// (model.NotificationTemplate) holds admin customizations.
package templates

// Variable is one substitutable field a template type supports.
type Variable struct {
	Name        string
	Description string
	Example     string
}

// Definition is one notification template type.
type Definition struct {
	Type           string
	Channel        string // "email" | "push"
	Description    string
	Variables      []Variable
	DefaultSubject string
	DefaultBody    string
}

// registry is the immutable set of known template types, keyed by
// type+channel via the All/Get helpers below.
var registry = []Definition{
	{
		Type: "ACTIVATION", Channel: "email",
		Description: "Sent when an employee or client account is created — carries the activation link.",
		Variables: []Variable{
			{"first_name", "Recipient's first name", "Ana"},
			{"link", "Account activation URL", "https://app.exbanka.rs/activate?token=..."},
		},
		DefaultSubject: "Activate Your EXBanka Account",
		DefaultBody: `<h2>Welcome, {{first_name}}!</h2>
<p>Your account has been created. Click the link below to activate your account and set your password:</p>
<p><a href="{{link}}" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Activate Account</a></p>
<p>Or copy this link into your browser:</p>
<p>{{link}}</p>
<p>This link expires in 24 hours.</p>`,
	},
	{
		Type: "PASSWORD_RESET", Channel: "email",
		Description: "Sent when a password reset is requested.",
		Variables: []Variable{
			{"link", "Password reset URL", "https://app.exbanka.rs/reset?token=..."},
		},
		DefaultSubject: "Password Reset Request",
		DefaultBody: `<h2>Password Reset</h2>
<p>Click the link below to reset your password:</p>
<p><a href="{{link}}" style="display:inline-block;padding:12px 24px;background:#1a73e8;color:#fff;text-decoration:none;border-radius:4px;">Reset Password</a></p>
<p>Or copy this link into your browser:</p>
<p>{{link}}</p>
<p>This link expires in 1 hour. If you did not request this, ignore this email.</p>`,
	},
	{
		Type: "CONFIRMATION", Channel: "email",
		Description: "Sent after an account is successfully activated.",
		Variables: []Variable{
			{"first_name", "Recipient's first name", "Ana"},
		},
		DefaultSubject: "Account Activated Successfully",
		DefaultBody: `<h2>Welcome aboard, {{first_name}}!</h2>
<p>Your EXBanka account has been successfully activated.</p>`,
	},
	{
		Type: "ACCOUNT_CREATED", Channel: "email",
		Description: "Sent when a new bank account is opened for a client.",
		Variables: []Variable{
			{"owner_name", "Account owner's name", "Ana Anić"},
			{"account_number", "The new account number", "265-1234567890123-45"},
			{"currency", "Account currency code", "RSD"},
			{"account_kind", "Account kind", "current"},
		},
		DefaultSubject: "Your New EXBanka Account Has Been Opened",
		DefaultBody: `<h2>Account Successfully Opened</h2>
<p>Dear {{owner_name}},</p>
<p>Your new bank account has been successfully created. Here are the details:</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{account_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Currency</strong></td><td style="padding:8px;border:1px solid #ddd;">{{currency}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Account Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{account_kind}}</td></tr>
</table>
<p>You can now use this account for transactions through your EXBanka online banking.</p>`,
	},
	{
		Type: "CARD_VERIFICATION", Channel: "email",
		Description: "Sent when a new card is issued — carries the CVV.",
		Variables: []Variable{
			{"card_last_four", "Last four digits of the card", "4242"},
			{"cvv", "Card security code", "123"},
		},
		DefaultSubject: "Your EXBanka Card Details",
		DefaultBody: `<h2>Card Verification Details</h2>
<p>Your card ending in <strong>{{card_last_four}}</strong> has been issued.</p>
<p>Your card security code (CVV): <strong>{{cvv}}</strong></p>
<p>Please keep this information confidential and do not share it with anyone.</p>
<p>If you did not request this card, please contact us immediately.</p>`,
	},
	{
		Type: "CARD_STATUS_CHANGED", Channel: "email",
		Description: "Sent when a card is blocked, unblocked, or deactivated.",
		Variables: []Variable{
			{"card_last_four", "Last four digits of the card", "4242"},
			{"account_number", "Account the card is linked to", "265-1234567890123-45"},
			{"new_status", "The card's new status", "blocked"},
		},
		DefaultSubject: "EXBanka Card Status Update",
		DefaultBody: `<h2>Card Status Changed</h2>
<p>The status of your card ending in <strong>{{card_last_four}}</strong> linked to account <strong>{{account_number}}</strong> has been updated.</p>
<p>New status: <strong>{{new_status}}</strong></p>
<p>If you did not request this change, please contact our support team immediately.</p>`,
	},
	{
		Type: "LOAN_APPROVED", Channel: "email",
		Description: "Sent when a loan request is approved.",
		Variables: []Variable{
			{"loan_number", "The loan number", "LN-000123"},
			{"loan_type", "Loan type", "cash"},
			{"amount", "Approved amount", "500000.00"},
			{"currency", "Loan currency code", "RSD"},
		},
		DefaultSubject: "Your EXBanka Loan Has Been Approved",
		DefaultBody: `<h2>Loan Approved</h2>
<p>Congratulations! Your loan application has been approved.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_type}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}} {{currency}}</td></tr>
</table>
<p>The funds will be disbursed to your designated account shortly.</p>`,
	},
	{
		Type: "LOAN_REJECTED", Channel: "email",
		Description: "Sent when a loan request is rejected.",
		Variables: []Variable{
			{"loan_type", "Loan type", "cash"},
			{"amount", "Requested amount", "500000.00"},
			{"reason", "Rejection reason", "Insufficient income"},
		},
		DefaultSubject: "EXBanka Loan Application Update",
		DefaultBody: `<h2>Loan Application Decision</h2>
<p>We regret to inform you that your loan application has not been approved at this time.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Type</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_type}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Requested Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Reason</strong></td><td style="padding:8px;border:1px solid #ddd;">{{reason}}</td></tr>
</table>
<p>You may reapply after addressing the above concerns. For assistance, please contact our support team.</p>`,
	},
	{
		Type: "INSTALLMENT_FAILED", Channel: "email",
		Description: "Sent when a scheduled loan installment collection fails.",
		Variables: []Variable{
			{"loan_number", "The loan number", "LN-000123"},
			{"amount", "Amount due", "12500.00"},
			{"currency", "Currency code", "RSD"},
			{"retry_deadline", "Deadline to ensure funds are available", "2026-06-01"},
		},
		DefaultSubject: "EXBanka Loan Installment Payment Failed",
		DefaultBody: `<h2>Installment Payment Failed</h2>
<p>We were unable to collect your scheduled loan installment payment.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Loan Number</strong></td><td style="padding:8px;border:1px solid #ddd;">{{loan_number}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount Due</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}} {{currency}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Retry Deadline</strong></td><td style="padding:8px;border:1px solid #ddd;">{{retry_deadline}}</td></tr>
</table>
<p>Please ensure sufficient funds are available in your account before the retry deadline to avoid late payment fees.</p>`,
	},
	{
		Type: "VERIFICATION_CODE", Channel: "email",
		Description: "Sent for a generic one-time verification code.",
		Variables: []Variable{
			{"code", "One-time verification code", "482913"},
			{"expires_in", "Human-readable expiry", "5 minutes"},
		},
		DefaultSubject: "EXBanka Verification Code",
		DefaultBody: `<h2>Verification Code</h2>
<p>Your one-time verification code is:</p>
<p style="font-size:32px;font-weight:bold;letter-spacing:8px;text-align:center;padding:20px;background:#f5f5f5;border-radius:4px;">{{code}}</p>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not initiate this request, please contact us immediately and do not share this code with anyone.</p>`,
	},
	{
		Type: "TRANSACTION_VERIFICATION", Channel: "email",
		Description: "Sent for a transaction-verification one-time code.",
		Variables: []Variable{
			{"verification_code", "One-time verification code", "482913"},
			{"expires_in", "Human-readable expiry", "5 minutes"},
		},
		DefaultSubject: "EXBanka Transaction Verification Code",
		DefaultBody: `<h2>Transaction Verification</h2>
<p>Your one-time verification code for the pending transaction is:</p>
<p style="font-size:32px;font-weight:bold;letter-spacing:8px;text-align:center;padding:20px;background:#f5f5f5;border-radius:4px;">{{verification_code}}</p>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not initiate this transaction, please contact us immediately and do not share this code with anyone.</p>`,
	},
	{
		Type: "PAYMENT_CONFIRMATION", Channel: "email",
		Description: "Sent to confirm a processed payment.",
		Variables: []Variable{
			{"status", "Payment status", "Completed"},
			{"from_account", "Source account number", "265-1111111111111-11"},
			{"to_account", "Destination account number", "265-2222222222222-22"},
			{"amount", "Payment amount", "10000.00 RSD"},
		},
		DefaultSubject: "EXBanka Payment Confirmation",
		DefaultBody: `<h2>Payment {{status}}</h2>
<p>Your payment has been processed.</p>
<table style="border-collapse:collapse;width:100%">
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>From Account</strong></td><td style="padding:8px;border:1px solid #ddd;">{{from_account}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>To Account</strong></td><td style="padding:8px;border:1px solid #ddd;">{{to_account}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Amount</strong></td><td style="padding:8px;border:1px solid #ddd;">{{amount}}</td></tr>
  <tr><td style="padding:8px;border:1px solid #ddd;"><strong>Status</strong></td><td style="padding:8px;border:1px solid #ddd;">{{status}}</td></tr>
</table>
<p>Thank you for banking with EXBanka.</p>`,
	},
	{
		Type: "MOBILE_ACTIVATION", Channel: "email",
		Description: "Sent with the mobile-app activation code.",
		Variables: []Variable{
			{"code", "Mobile activation code", "482913"},
			{"expires_in", "Human-readable expiry", "15 minutes"},
		},
		DefaultSubject: "Your EXBanka Mobile App Activation Code",
		DefaultBody: `<html><body style="font-family:Arial,sans-serif;max-width:600px;margin:0 auto;">
<h2 style="color:#1a365d;">Mobile App Activation</h2>
<p>Use the following code to activate your EXBanka mobile app:</p>
<div style="background:#f0f4f8;padding:20px;text-align:center;border-radius:8px;margin:20px 0;">
	<span style="font-size:32px;font-weight:bold;letter-spacing:8px;color:#2d3748;">{{code}}</span>
</div>
<p>This code expires in <strong>{{expires_in}}</strong>.</p>
<p>If you did not request this, please ignore this email.</p>
<p style="color:#718096;font-size:12px;">EXBanka Security Team</p>
</body></html>`,
	},
}

// All returns every registered template definition. If channel is non-empty,
// only definitions for that channel are returned.
func All(channel string) []Definition {
	if channel == "" {
		out := make([]Definition, len(registry))
		copy(out, registry)
		return out
	}
	var out []Definition
	for _, d := range registry {
		if d.Channel == channel {
			out = append(out, d)
		}
	}
	return out
}

// Get returns the definition for a (type, channel) pair.
func Get(typ, channel string) (Definition, bool) {
	for _, d := range registry {
		if d.Type == typ && d.Channel == channel {
			return d, true
		}
	}
	return Definition{}, false
}

// KnownVars returns the set of variable names a (type, channel) supports.
func KnownVars(typ, channel string) (map[string]bool, bool) {
	d, ok := Get(typ, channel)
	if !ok {
		return nil, false
	}
	set := make(map[string]bool, len(d.Variables))
	for _, v := range d.Variables {
		set[v.Name] = true
	}
	return set, true
}
```

- [ ] **Step 2: Write the registry self-consistency test**

Create `notification-service/internal/templates/registry_test.go`:

```go
package templates

import (
	"regexp"
	"testing"
)

var placeholderRE = regexp.MustCompile(`\{\{(\w+)\}\}`)

func TestRegistry_AllEmailTypesPresent(t *testing.T) {
	want := []string{
		"ACTIVATION", "PASSWORD_RESET", "CONFIRMATION", "ACCOUNT_CREATED",
		"CARD_VERIFICATION", "CARD_STATUS_CHANGED", "LOAN_APPROVED", "LOAN_REJECTED",
		"INSTALLMENT_FAILED", "VERIFICATION_CODE", "TRANSACTION_VERIFICATION",
		"PAYMENT_CONFIRMATION", "MOBILE_ACTIVATION",
	}
	for _, typ := range want {
		if _, ok := Get(typ, "email"); !ok {
			t.Errorf("registry missing email type %q", typ)
		}
	}
	if got := len(All("email")); got != len(want) {
		t.Errorf("All(email) returned %d, want %d", got, len(want))
	}
}

func TestRegistry_SelfConsistent(t *testing.T) {
	for _, d := range All("") {
		if d.Type == "" || d.Channel == "" || d.DefaultSubject == "" || d.DefaultBody == "" {
			t.Errorf("%s/%s: empty required field", d.Type, d.Channel)
		}
		if len(d.Variables) == 0 {
			t.Errorf("%s/%s: no variables declared", d.Type, d.Channel)
		}
		known, _ := KnownVars(d.Type, d.Channel)
		// Every {{placeholder}} in the default subject+body must be a declared variable.
		for _, m := range placeholderRE.FindAllStringSubmatch(d.DefaultSubject+d.DefaultBody, -1) {
			if !known[m[1]] {
				t.Errorf("%s/%s: default text uses undeclared variable %q", d.Type, d.Channel, m[1])
			}
		}
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `cd notification-service && go test ./internal/templates/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add notification-service/internal/templates/
git commit -m "feat(notification-service): code-defined template registry"
```

---

## Phase 4 — notification-service model + repository

### Task 4: `NotificationTemplate` model + repository

**Files:**
- Create: `notification-service/internal/model/notification_template.go`
- Create: `notification-service/internal/repository/template_repository.go`
- Create: `notification-service/internal/repository/template_repository_test.go`

- [ ] **Step 1: Write the model**

Create `notification-service/internal/model/notification_template.go`:

```go
package model

import "time"

// NotificationTemplate is an admin-customized override of a registry template's
// subject/body. A row exists only when an admin has customized that
// (type, channel); absence means "use the code-defined registry default".
type NotificationTemplate struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Type      string    `gorm:"size:64;not null;uniqueIndex:ux_tmpl_type_channel,priority:1" json:"type"`
	Channel   string    `gorm:"size:16;not null;uniqueIndex:ux_tmpl_type_channel,priority:2" json:"channel"`
	Subject   string    `gorm:"type:text;not null" json:"subject"`
	Body      string    `gorm:"type:text;not null" json:"body"`
	UpdatedBy uint64    `gorm:"not null" json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Write the failing repository test**

Create `notification-service/internal/repository/template_repository_test.go`:

```go
package repository

import (
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/notification-service/internal/model"
)

func TestTemplateRepository_UpsertGetDelete(t *testing.T) {
	db := testutil.SetupTestDB(t, &model.NotificationTemplate{})
	repo := NewTemplateRepository(db)

	// Get on a missing row → gorm.ErrRecordNotFound.
	if _, err := repo.Get("ACTIVATION", "email"); err == nil {
		t.Fatal("expected error for missing override")
	}

	// Upsert inserts.
	tmpl := &model.NotificationTemplate{Type: "ACTIVATION", Channel: "email", Subject: "Hi", Body: "Hello {{first_name}}", UpdatedBy: 7}
	if err := repo.Upsert(tmpl); err != nil {
		t.Fatalf("upsert insert: %v", err)
	}
	got, err := repo.Get("ACTIVATION", "email")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "Hi" || got.UpdatedBy != 7 {
		t.Errorf("got %+v", got)
	}

	// Upsert again updates the same row (no duplicate).
	tmpl2 := &model.NotificationTemplate{Type: "ACTIVATION", Channel: "email", Subject: "Updated", Body: "x", UpdatedBy: 9}
	if err := repo.Upsert(tmpl2); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ = repo.Get("ACTIVATION", "email")
	if got.Subject != "Updated" || got.UpdatedBy != 9 {
		t.Errorf("update did not take: %+v", got)
	}
	var count int64
	db.Model(&model.NotificationTemplate{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 row after re-upsert, got %d", count)
	}

	// Delete removes it.
	if err := repo.Delete("ACTIVATION", "email"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get("ACTIVATION", "email"); err == nil {
		t.Error("expected error after delete")
	}
}
```

- [ ] **Step 3: Run to confirm failure**

Run: `cd notification-service && go test ./internal/repository/ -run TestTemplateRepository`
Expected: FAIL — `NewTemplateRepository` undefined.

- [ ] **Step 4: Write the repository**

Create `notification-service/internal/repository/template_repository.go`:

```go
package repository

import (
	"github.com/exbanka/notification-service/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TemplateRepository struct {
	db *gorm.DB
}

func NewTemplateRepository(db *gorm.DB) *TemplateRepository {
	return &TemplateRepository{db: db}
}

// Get returns the override row for (type, channel), or gorm.ErrRecordNotFound
// when the admin has not customized that template.
func (r *TemplateRepository) Get(typ, channel string) (*model.NotificationTemplate, error) {
	var t model.NotificationTemplate
	if err := r.db.Where("type = ? AND channel = ?", typ, channel).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// Upsert inserts or updates the override row for (type, channel). Uses
// ON CONFLICT on the (type, channel) unique index so concurrent admin edits
// do not race into duplicate rows.
func (r *TemplateRepository) Upsert(t *model.NotificationTemplate) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "type"}, {Name: "channel"}},
		DoUpdates: clause.AssignmentColumns([]string{"subject", "body", "updated_by", "updated_at"}),
	}).Create(t).Error
}

// Delete removes the override row, reverting (type, channel) to the registry
// default. Deleting a non-existent row is a no-op (not an error).
func (r *TemplateRepository) Delete(typ, channel string) error {
	return r.db.Where("type = ? AND channel = ?", typ, channel).
		Delete(&model.NotificationTemplate{}).Error
}
```

- [ ] **Step 5: Run the test**

Run: `cd notification-service && go test ./internal/repository/ -run TestTemplateRepository`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add notification-service/internal/model/notification_template.go notification-service/internal/repository/template_repository.go notification-service/internal/repository/template_repository_test.go
git commit -m "feat(notification-service): NotificationTemplate model + repository"
```

---

## Phase 5 — notification-service TemplateService

### Task 5: `TemplateService` — Render + List/GetOne/Set/Reset

**Files:**
- Create: `notification-service/internal/service/template_service.go`
- Create: `notification-service/internal/service/template_service_test.go`

- [ ] **Step 1: Write the failing test**

Create `notification-service/internal/service/template_service_test.go`:

```go
package service

import (
	"errors"
	"testing"

	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
)

func newTemplateSvc(t *testing.T) *TemplateService {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.NotificationTemplate{})
	return NewTemplateService(repository.NewTemplateRepository(db))
}

func TestTemplateService_Render_DefaultWhenNoOverride(t *testing.T) {
	svc := newTemplateSvc(t)
	subject, body, err := svc.Render("CONFIRMATION", "email", map[string]string{"first_name": "Ana"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if subject != "Account Activated Successfully" {
		t.Errorf("subject = %q", subject)
	}
	if want := "Welcome aboard, Ana!"; !contains(body, want) {
		t.Errorf("body %q missing %q", body, want)
	}
}

func TestTemplateService_Render_OverrideWins(t *testing.T) {
	svc := newTemplateSvc(t)
	if _, err := svc.Set("CONFIRMATION", "email", "Custom {{first_name}}", "Body {{first_name}}", 7); err != nil {
		t.Fatalf("set: %v", err)
	}
	subject, body, err := svc.Render("CONFIRMATION", "email", map[string]string{"first_name": "Ana"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if subject != "Custom Ana" || body != "Body Ana" {
		t.Errorf("got subject=%q body=%q", subject, body)
	}
}

func TestTemplateService_Render_MissingDataAndUnknownToken(t *testing.T) {
	svc := newTemplateSvc(t)
	// first_name not supplied → substituted with empty string.
	_, body, err := svc.Render("CONFIRMATION", "email", map[string]string{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if contains(body, "{{") {
		t.Errorf("body still has a literal placeholder: %q", body)
	}
}

func TestTemplateService_Render_UnknownType(t *testing.T) {
	svc := newTemplateSvc(t)
	if _, _, err := svc.Render("NOPE", "email", nil); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestTemplateService_Set_RejectsUnknownVariable(t *testing.T) {
	svc := newTemplateSvc(t)
	_, err := svc.Set("CONFIRMATION", "email", "Hi", "Hello {{frist_name}}", 7) // typo
	if !errors.Is(err, ErrTemplateValidation) {
		t.Errorf("expected ErrTemplateValidation, got %v", err)
	}
}

func TestTemplateService_Set_RejectsUnknownType(t *testing.T) {
	svc := newTemplateSvc(t)
	_, err := svc.Set("NOPE", "email", "s", "b", 7)
	if !errors.Is(err, ErrTemplateTypeNotFound) {
		t.Errorf("expected ErrTemplateTypeNotFound, got %v", err)
	}
}

func TestTemplateService_Set_RejectsEmpty(t *testing.T) {
	svc := newTemplateSvc(t)
	if _, err := svc.Set("CONFIRMATION", "email", "", "b", 7); !errors.Is(err, ErrTemplateValidation) {
		t.Errorf("empty subject: expected ErrTemplateValidation, got %v", err)
	}
}

func TestTemplateService_GetOneAndList(t *testing.T) {
	svc := newTemplateSvc(t)
	v, err := svc.GetOne("ACTIVATION", "email")
	if err != nil {
		t.Fatalf("getone: %v", err)
	}
	if v.IsCustomized {
		t.Error("fresh template should not be customized")
	}
	if v.CurrentBody != v.DefaultBody {
		t.Error("current should equal default when not customized")
	}
	if len(v.Variables) == 0 {
		t.Error("expected variables on the view")
	}
	all, err := svc.List("email")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 13 {
		t.Errorf("List(email) = %d, want 13", len(all))
	}
}

func TestTemplateService_Reset(t *testing.T) {
	svc := newTemplateSvc(t)
	if _, err := svc.Set("CONFIRMATION", "email", "Custom", "Body", 7); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := svc.Reset("CONFIRMATION", "email")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if v.IsCustomized {
		t.Error("after reset, should not be customized")
	}
	if v.CurrentSubject != v.DefaultSubject {
		t.Error("after reset, current should equal default")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd notification-service && go test ./internal/service/ -run TestTemplateService`
Expected: FAIL — `NewTemplateService` undefined.

- [ ] **Step 3: Write the service**

Create `notification-service/internal/service/template_service.go`:

```go
package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/templates"
	"gorm.io/gorm"
)

// Template-management sentinel errors. The gRPC handler maps these to
// codes.NotFound / codes.InvalidArgument.
var (
	ErrTemplateTypeNotFound = errors.New("notification template type not found")
	ErrTemplateValidation   = errors.New("notification template validation failed")
)

var templatePlaceholderRE = regexp.MustCompile(`\{\{(\w+)\}\}`)

// TemplateView is the rich, API-facing shape of a template: registry metadata
// plus the current (override-or-default) text.
type TemplateView struct {
	Type           string
	Channel        string
	Description    string
	Variables      []templates.Variable
	DefaultSubject string
	DefaultBody    string
	CurrentSubject string
	CurrentBody    string
	IsCustomized   bool
}

type TemplateService struct {
	repo *repository.TemplateRepository
}

func NewTemplateService(repo *repository.TemplateRepository) *TemplateService {
	return &TemplateService{repo: repo}
}

// Render resolves the override-or-default text for (typ, channel) and
// substitutes {{var}} placeholders from data. A {{token}} whose key is absent
// from data is replaced with the empty string (a stale override never ships a
// literal {{x}} to a user).
func (s *TemplateService) Render(typ, channel string, data map[string]string) (subject, body string, err error) {
	def, ok := templates.Get(typ, channel)
	if !ok {
		return "", "", fmt.Errorf("render %s/%s: %w", typ, channel, ErrTemplateTypeNotFound)
	}
	subject, body = def.DefaultSubject, def.DefaultBody
	if override, gerr := s.repo.Get(typ, channel); gerr == nil {
		subject, body = override.Subject, override.Body
	} else if !errors.Is(gerr, gorm.ErrRecordNotFound) {
		return "", "", fmt.Errorf("render %s/%s: %w", typ, channel, gerr)
	}
	return substitute(subject, data), substitute(body, data), nil
}

func substitute(s string, data map[string]string) string {
	return templatePlaceholderRE.ReplaceAllStringFunc(s, func(match string) string {
		name := templatePlaceholderRE.FindStringSubmatch(match)[1]
		return data[name] // "" when absent
	})
}

// List returns a TemplateView for every registry type (optionally filtered by
// channel), reflecting any DB override.
func (s *TemplateService) List(channel string) ([]TemplateView, error) {
	defs := templates.All(channel)
	out := make([]TemplateView, 0, len(defs))
	for _, d := range defs {
		v, err := s.viewFor(d)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetOne returns the TemplateView for a single (typ, channel).
func (s *TemplateService) GetOne(typ, channel string) (TemplateView, error) {
	def, ok := templates.Get(typ, channel)
	if !ok {
		return TemplateView{}, fmt.Errorf("get %s/%s: %w", typ, channel, ErrTemplateTypeNotFound)
	}
	return s.viewFor(def)
}

// Set upserts an admin override after validating it. Unknown type → 404-ish;
// bad channel / empty subject|body / unknown placeholder token → validation.
func (s *TemplateService) Set(typ, channel, subject, body string, updatedBy uint64) (TemplateView, error) {
	known, ok := templates.KnownVars(typ, channel)
	if !ok {
		return TemplateView{}, fmt.Errorf("set %s/%s: %w", typ, channel, ErrTemplateTypeNotFound)
	}
	if strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "" {
		return TemplateView{}, fmt.Errorf("subject and body must be non-empty: %w", ErrTemplateValidation)
	}
	var unknown []string
	for _, m := range templatePlaceholderRE.FindAllStringSubmatch(subject+body, -1) {
		if !known[m[1]] {
			unknown = append(unknown, m[1])
		}
	}
	if len(unknown) > 0 {
		return TemplateView{}, fmt.Errorf("unknown variables %v: %w", unknown, ErrTemplateValidation)
	}
	if err := s.repo.Upsert(&model.NotificationTemplate{
		Type: typ, Channel: channel, Subject: subject, Body: body, UpdatedBy: updatedBy,
	}); err != nil {
		return TemplateView{}, fmt.Errorf("set %s/%s: %w", typ, channel, err)
	}
	return s.GetOne(typ, channel)
}

// Reset deletes the override, reverting (typ, channel) to the registry default.
func (s *TemplateService) Reset(typ, channel string) (TemplateView, error) {
	if _, ok := templates.Get(typ, channel); !ok {
		return TemplateView{}, fmt.Errorf("reset %s/%s: %w", typ, channel, ErrTemplateTypeNotFound)
	}
	if err := s.repo.Delete(typ, channel); err != nil {
		return TemplateView{}, fmt.Errorf("reset %s/%s: %w", typ, channel, err)
	}
	return s.GetOne(typ, channel)
}

func (s *TemplateService) viewFor(d templates.Definition) (TemplateView, error) {
	v := TemplateView{
		Type: d.Type, Channel: d.Channel, Description: d.Description,
		Variables:      d.Variables,
		DefaultSubject: d.DefaultSubject, DefaultBody: d.DefaultBody,
		CurrentSubject: d.DefaultSubject, CurrentBody: d.DefaultBody,
	}
	override, err := s.repo.Get(d.Type, d.Channel)
	if err == nil {
		v.CurrentSubject, v.CurrentBody, v.IsCustomized = override.Subject, override.Body, true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return TemplateView{}, err
	}
	return v, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd notification-service && go test ./internal/service/ -run TestTemplateService`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add notification-service/internal/service/template_service.go notification-service/internal/service/template_service_test.go
git commit -m "feat(notification-service): TemplateService — render + CRUD + validation"
```

---

## Phase 6 — notification-service: swap rendering to the registry

### Task 6: Replace `BuildEmail` with `TemplateService.Render`

**Files:**
- Modify: `notification-service/internal/consumer/email_consumer.go`
- Modify: `notification-service/internal/consumer/verification_consumer.go`
- Modify: `notification-service/internal/handler/grpc_handler.go`
- Delete: `notification-service/internal/sender/templates.go`

- [ ] **Step 1: Define the renderer interface and thread it through `EmailConsumer`**

In `email_consumer.go`, add a renderer interface and a struct field. Change the imports + struct + constructors. The `emailDispatcher` / `emailSentPublisher` interfaces stay. Add:

```go
// templateRenderer is the minimal subset of *service.TemplateService used here.
type templateRenderer interface {
	Render(typ, channel string, data map[string]string) (subject, body string, err error)
}
```

Change the struct to:

```go
type EmailConsumer struct {
	reader    *kafkago.Reader
	sender    emailDispatcher
	producer  emailSentPublisher
	templates templateRenderer
}
```

Change `NewEmailConsumer` to accept the service and store it:

```go
func NewEmailConsumer(brokers string, emailSender *sender.EmailSender, producer *kafkaprod.Producer, templateSvc *svc.TemplateService) *EmailConsumer {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  strings.Split(brokers, ","),
		Topic:    kafkamsg.TopicSendEmail,
		GroupID:  "notification-service",
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &EmailConsumer{
		reader:    reader,
		sender:    emailSender,
		producer:  producer,
		templates: templateSvc,
	}
}
```

Change `newEmailConsumerForTest` to accept the renderer:

```go
func newEmailConsumerForTest(d emailDispatcher, p emailSentPublisher, r templateRenderer) *EmailConsumer {
	return &EmailConsumer{sender: d, producer: p, templates: r}
}
```

In `handleMessage`, replace:

```go
	subject, body := sender.BuildEmail(emailMsg.EmailType, emailMsg.Data)
```

with:

```go
	subject, body, renderErr := c.templates.Render(string(emailMsg.EmailType), "email", emailMsg.Data)
	if renderErr != nil {
		log.Printf("failed to render email template %s: %v", emailMsg.EmailType, renderErr)
		if pubErr := c.producer.PublishEmailSent(ctx, kafkamsg.EmailSentMessage{
			To: emailMsg.To, EmailType: emailMsg.EmailType, Success: false, Error: renderErr.Error(),
		}); pubErr != nil {
			log.Printf("failed to publish email-sent confirmation: %v", pubErr)
		}
		return
	}
```

The `sender` import is still used (`*sender.EmailSender` in `NewEmailConsumer`); keep it.

- [ ] **Step 2: Update `EmailConsumer` tests**

Find `notification-service/internal/consumer/email_consumer_test.go`. Every `newEmailConsumerForTest(d, p)` call gains a third arg — a stub renderer. Add this stub to the test file:

```go
type stubRenderer struct {
	subject, body string
	err           error
}

func (s *stubRenderer) Render(_, _ string, _ map[string]string) (string, string, error) {
	return s.subject, s.body, s.err
}
```

and update every `newEmailConsumerForTest(d, p)` → `newEmailConsumerForTest(d, p, &stubRenderer{subject: "S", body: "B"})`. If a test asserted on `BuildEmail`-rendered output, update it to expect the stub's `"S"`/`"B"`.

- [ ] **Step 3: Thread the renderer through `VerificationConsumer`**

In `verification_consumer.go`, add a `templates templateRenderer` field to `VerificationConsumer` (the `templateRenderer` interface is already declared in `email_consumer.go`, same package). Change `NewVerificationConsumer` to accept `templateSvc *svc.TemplateService` and store it; add `svc "github.com/exbanka/notification-service/internal/service"` to imports (the alias `svc` matches `email_consumer.go`). Change `newVerificationConsumerForTest` to accept a `templateRenderer`. In `handleEmailDelivery`, replace:

```go
	subject, body := sender.BuildEmail(kafkamsg.EmailTypeTransactionVerify, map[string]string{
		"verification_code": code,
		"expires_in":        "5 minutes",
	})
```

with:

```go
	subject, body, renderErr := c.templates.Render(string(kafkamsg.EmailTypeTransactionVerify), "email", map[string]string{
		"verification_code": code,
		"expires_in":        "5 minutes",
	})
	if renderErr != nil {
		log.Printf("verification consumer: render error: %v", renderErr)
		return
	}
```

After this change `sender` is still imported (`*sender.EmailSender` in `NewVerificationConsumer`); keep it. Update `verification_consumer_test.go` the same way as Step 2 (`newVerificationConsumerForTest` gains a `&stubRenderer{...}` arg).

- [ ] **Step 4: Update the gRPC handler's `SendEmail`**

In `grpc_handler.go`, add a `templateSvc` dependency. Add to the struct:

```go
type GRPCHandler struct {
	notifpb.UnimplementedNotificationServiceServer
	emailSender emailSenderFacade
	inboxRepo   inboxRepoFacade
	notifRepo   notifRepoFacade
	templateSvc templateServiceFacade
}
```

Add the facade interface near the others:

```go
// templateServiceFacade is the narrow interface of *service.TemplateService used by GRPCHandler.
type templateServiceFacade interface {
	Render(typ, channel string, data map[string]string) (subject, body string, err error)
	List(channel string) ([]service.TemplateView, error)
	GetOne(typ, channel string) (service.TemplateView, error)
	Set(typ, channel, subject, body string, updatedBy uint64) (service.TemplateView, error)
	Reset(typ, channel string) (service.TemplateView, error)
}
```

Change `NewGRPCHandler` and `newGRPCHandlerForTest` to accept and store `templateSvc`:

```go
func NewGRPCHandler(emailSender *sender.EmailSender, inboxRepo *repository.MobileInboxRepository, notifRepo *repository.GeneralNotificationRepository, templateSvc *service.TemplateService) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo, templateSvc: templateSvc}
}

func newGRPCHandlerForTest(emailSender emailSenderFacade, inboxRepo inboxRepoFacade, notifRepo notifRepoFacade, templateSvc templateServiceFacade) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo, templateSvc: templateSvc}
}
```

In `SendEmail`, replace:

```go
	emailType := kafkamsg.EmailType(req.EmailType)
	subject, body := sender.BuildEmail(emailType, req.Data)
```

with:

```go
	subject, body, err := h.templateSvc.Render(req.EmailType, "email", req.Data)
	if err != nil {
		return &notifpb.SendEmailResponse{Success: false, Message: err.Error()}, nil
	}
```

The `kafkamsg` import is still used elsewhere in the file? Check — if `kafkamsg` becomes unused after this edit, remove it from the import block. (`sender` is still used: `*sender.EmailSender` in `NewGRPCHandler`.)

- [ ] **Step 5: Delete `BuildEmail`**

`notification-service/internal/sender/templates.go` now has no callers. Delete the file:

```bash
git rm notification-service/internal/sender/templates.go
```

The `sender` package keeps `email_sender.go` (`EmailSender` + `Send`).

- [ ] **Step 6: Update existing grpc-handler tests**

Find `notification-service/internal/handler/*_test.go`. Every `newGRPCHandlerForTest(...)` call gains a 4th arg. Add a stub implementing `templateServiceFacade` to the handler test package:

```go
type stubTemplateSvc struct {
	renderSubject, renderBody string
	renderErr                 error
	views                     []service.TemplateView
	one                       service.TemplateView
	err                       error
}

func (s *stubTemplateSvc) Render(_, _ string, _ map[string]string) (string, string, error) {
	return s.renderSubject, s.renderBody, s.renderErr
}
func (s *stubTemplateSvc) List(_ string) ([]service.TemplateView, error) { return s.views, s.err }
func (s *stubTemplateSvc) GetOne(_, _ string) (service.TemplateView, error) { return s.one, s.err }
func (s *stubTemplateSvc) Set(_, _, _, _ string, _ uint64) (service.TemplateView, error) {
	return s.one, s.err
}
func (s *stubTemplateSvc) Reset(_, _ string) (service.TemplateView, error) { return s.one, s.err }
```

Update every `newGRPCHandlerForTest(es, ir, nr)` → `newGRPCHandlerForTest(es, ir, nr, &stubTemplateSvc{renderSubject: "S", renderBody: "B"})`. Any existing `SendEmail` test that asserted `BuildEmail` output now expects the stub's `"S"`/`"B"`.

- [ ] **Step 7: Build the notification-service non-test code**

Run: `cd notification-service && go build ./...`
Expected: builds clean (cmd/main.go still calls the old `NewEmailConsumer`/`NewGRPCHandler` signatures — that's fixed in Task 7; if `go build ./...` fails only on `cmd/main.go`, that is expected and Task 7 fixes it. Build the changed packages directly to confirm them: `go build ./internal/consumer/ ./internal/handler/ ./internal/sender/`).

Run: `cd notification-service && go build ./internal/consumer/ ./internal/handler/ ./internal/sender/`
Expected: builds clean.

- [ ] **Step 8: Run the consumer + handler tests**

Run: `cd notification-service && go test ./internal/consumer/ ./internal/handler/`
Expected: PASS (after the test updates in Steps 2, 3, 6).

- [ ] **Step 9: Commit**

```bash
git add notification-service/internal/consumer/ notification-service/internal/handler/ notification-service/internal/sender/
git commit -m "refactor(notification-service): render emails via TemplateService, drop BuildEmail"
```

---

## Phase 7 — notification-service: template gRPC RPCs + wiring

### Task 7: The 4 template gRPC handlers + `main.go` wiring

**Files:**
- Modify: `notification-service/internal/handler/grpc_handler.go`
- Create: `notification-service/internal/handler/template_grpc_test.go`
- Modify: `notification-service/cmd/main.go`

- [ ] **Step 1: Write the failing gRPC handler tests**

Create `notification-service/internal/handler/template_grpc_test.go`:

```go
package handler

import (
	"context"
	"testing"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/contract/testutil"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTemplateHandler(t *testing.T) *GRPCHandler {
	t.Helper()
	db := testutil.SetupTestDB(t, &model.NotificationTemplate{})
	svc := service.NewTemplateService(repository.NewTemplateRepository(db))
	return newGRPCHandlerForTest(nil, nil, nil, svc)
}

func TestGRPC_ListTemplates(t *testing.T) {
	h := newTemplateHandler(t)
	resp, err := h.ListTemplates(context.Background(), &notifpb.ListTemplatesRequest{Channel: "email"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.Templates) != 13 {
		t.Errorf("got %d templates, want 13", len(resp.Templates))
	}
	if len(resp.Templates[0].Variables) == 0 {
		t.Error("expected variables on each template")
	}
}

func TestGRPC_SetAndGetTemplate(t *testing.T) {
	h := newTemplateHandler(t)
	set, err := h.SetTemplate(context.Background(), &notifpb.SetTemplateRequest{
		Type: "CONFIRMATION", Channel: "email",
		Subject: "Custom {{first_name}}", Body: "Body {{first_name}}", UpdatedBy: 7,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !set.IsCustomized || set.CurrentSubject != "Custom {{first_name}}" {
		t.Errorf("set returned %+v", set)
	}
	got, err := h.GetTemplate(context.Background(), &notifpb.GetTemplateRequest{Type: "CONFIRMATION", Channel: "email"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.IsCustomized {
		t.Error("expected is_customized after set")
	}
}

func TestGRPC_SetTemplate_UnknownVariable(t *testing.T) {
	h := newTemplateHandler(t)
	_, err := h.SetTemplate(context.Background(), &notifpb.SetTemplateRequest{
		Type: "CONFIRMATION", Channel: "email", Subject: "x", Body: "{{frist_name}}", UpdatedBy: 7,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestGRPC_SetTemplate_UnknownType(t *testing.T) {
	h := newTemplateHandler(t)
	_, err := h.SetTemplate(context.Background(), &notifpb.SetTemplateRequest{
		Type: "NOPE", Channel: "email", Subject: "x", Body: "y", UpdatedBy: 7,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("got code %v, want NotFound", status.Code(err))
	}
}

func TestGRPC_ResetTemplate(t *testing.T) {
	h := newTemplateHandler(t)
	if _, err := h.SetTemplate(context.Background(), &notifpb.SetTemplateRequest{
		Type: "CONFIRMATION", Channel: "email", Subject: "Custom", Body: "Body", UpdatedBy: 7,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}
	reset, err := h.ResetTemplate(context.Background(), &notifpb.ResetTemplateRequest{Type: "CONFIRMATION", Channel: "email"})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset.IsCustomized {
		t.Error("expected not customized after reset")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

Run: `cd notification-service && go test ./internal/handler/ -run TestGRPC_`
Expected: FAIL — `h.ListTemplates` etc. undefined.

- [ ] **Step 3: Implement the 4 RPCs**

Append to `notification-service/internal/handler/grpc_handler.go`. Add `"errors"`, `"google.golang.org/grpc/codes"`, `"google.golang.org/grpc/status"` to the import block:

```go
// ── Notification templates ───────────────────────────────────────────────────

func templateViewToProto(v service.TemplateView) *notifpb.TemplateInfo {
	vars := make([]*notifpb.TemplateVariable, len(v.Variables))
	for i, x := range v.Variables {
		vars[i] = &notifpb.TemplateVariable{Name: x.Name, Description: x.Description, Example: x.Example}
	}
	return &notifpb.TemplateInfo{
		Type: v.Type, Channel: v.Channel, Description: v.Description, Variables: vars,
		DefaultSubject: v.DefaultSubject, DefaultBody: v.DefaultBody,
		CurrentSubject: v.CurrentSubject, CurrentBody: v.CurrentBody,
		IsCustomized: v.IsCustomized,
	}
}

// templateErr maps a TemplateService error to a gRPC status.
func templateErr(err error) error {
	switch {
	case errors.Is(err, service.ErrTemplateTypeNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, service.ErrTemplateValidation):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func (h *GRPCHandler) ListTemplates(ctx context.Context, req *notifpb.ListTemplatesRequest) (*notifpb.ListTemplatesResponse, error) {
	views, err := h.templateSvc.List(req.Channel)
	if err != nil {
		return nil, templateErr(err)
	}
	out := &notifpb.ListTemplatesResponse{Templates: make([]*notifpb.TemplateInfo, len(views))}
	for i, v := range views {
		out.Templates[i] = templateViewToProto(v)
	}
	return out, nil
}

func (h *GRPCHandler) GetTemplate(ctx context.Context, req *notifpb.GetTemplateRequest) (*notifpb.TemplateInfo, error) {
	v, err := h.templateSvc.GetOne(req.Type, req.Channel)
	if err != nil {
		return nil, templateErr(err)
	}
	return templateViewToProto(v), nil
}

func (h *GRPCHandler) SetTemplate(ctx context.Context, req *notifpb.SetTemplateRequest) (*notifpb.TemplateInfo, error) {
	v, err := h.templateSvc.Set(req.Type, req.Channel, req.Subject, req.Body, req.UpdatedBy)
	if err != nil {
		return nil, templateErr(err)
	}
	return templateViewToProto(v), nil
}

func (h *GRPCHandler) ResetTemplate(ctx context.Context, req *notifpb.ResetTemplateRequest) (*notifpb.TemplateInfo, error) {
	v, err := h.templateSvc.Reset(req.Type, req.Channel)
	if err != nil {
		return nil, templateErr(err)
	}
	return templateViewToProto(v), nil
}
```

- [ ] **Step 4: Wire `main.go`**

In `notification-service/cmd/main.go`:

Add `&model.NotificationTemplate{}` to the `AutoMigrate` call:

```go
	if err := db.AutoMigrate(&model.MobileInboxItem{}, &model.GeneralNotification{}, &model.NotificationTemplate{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
```

In the `// Repositories` block, add the template repo and service (after `notifRepo`):

```go
	// Repositories
	inboxRepo := repository.NewMobileInboxRepository(db)
	notifRepo := repository.NewGeneralNotificationRepository(db)
	templateRepo := repository.NewTemplateRepository(db)

	// Template service (registry-backed render + admin CRUD)
	templateSvc := service.NewTemplateService(templateRepo)
```

Update the `NewEmailConsumer` call:

```go
	emailConsumer := consumer.NewEmailConsumer(cfg.KafkaBrokers, emailSender, producer, templateSvc)
```

Update the `NewVerificationConsumer` call:

```go
	verificationConsumer := consumer.NewVerificationConsumer(cfg.KafkaBrokers, emailSender, producer, inboxRepo, templateSvc)
```

Update the `NewGRPCHandler` call in the `Register` func:

```go
			notifpb.RegisterNotificationServiceServer(s, handler.NewGRPCHandler(emailSender, inboxRepo, notifRepo, templateSvc))
```

- [ ] **Step 5: Build, test, lint the whole notification-service**

Run: `cd notification-service && go build ./... && go test ./... && golangci-lint run ./...`
Expected: builds, all tests pass, no new lint warnings.

- [ ] **Step 6: Commit**

```bash
git add notification-service/internal/handler/ notification-service/cmd/main.go
git commit -m "feat(notification-service): template management gRPC RPCs + wiring"
```

---

## Phase 8 — api-gateway: REST routes

### Task 8: Notification-template REST handlers

**Files:**
- Create: `api-gateway/internal/handler/notification_template_handler.go`
- Create: `api-gateway/internal/handler/notification_template_handler_test.go`

- [ ] **Step 1: Write the handler**

Create `api-gateway/internal/handler/notification_template_handler.go`. These are methods on the existing `*NotificationHandler` (it already holds `notificationClient`), kept in their own file:

```go
package handler

import (
	"net/http"

	notificationpb "github.com/exbanka/contract/notificationpb"
	"github.com/gin-gonic/gin"
)

// templateInfoToJSON renders a proto TemplateInfo as the REST shape.
func templateInfoToJSON(t *notificationpb.TemplateInfo) gin.H {
	vars := make([]gin.H, 0, len(t.Variables))
	for _, v := range t.Variables {
		vars = append(vars, gin.H{"name": v.Name, "description": v.Description, "example": v.Example})
	}
	return gin.H{
		"type":            t.Type,
		"channel":         t.Channel,
		"description":     t.Description,
		"variables":       vars,
		"default_subject": t.DefaultSubject,
		"default_body":    t.DefaultBody,
		"current_subject": t.CurrentSubject,
		"current_body":    t.CurrentBody,
		"is_customized":   t.IsCustomized,
	}
}

// ListNotificationTemplates godoc
// @Summary      List notification templates (discovery)
// @Description  Lists every notification template type with its supported {{variables}}, default text, and current (possibly customized) text. The discovery endpoint for the template editor.
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel query string false "Filter by channel: email | push"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      403 {object} map[string]interface{}
// @Router       /api/v3/notification-templates [get]
func (h *NotificationHandler) ListNotificationTemplates(c *gin.Context) {
	channel := c.Query("channel")
	if channel != "" {
		if _, err := oneOf("channel", channel, "email", "push"); err != nil {
			apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
			return
		}
	}
	resp, err := h.notificationClient.ListTemplates(c.Request.Context(), &notificationpb.ListTemplatesRequest{Channel: channel})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	out := make([]gin.H, 0, len(resp.Templates))
	for _, t := range resp.Templates {
		out = append(out, templateInfoToJSON(t))
	}
	c.JSON(http.StatusOK, gin.H{"templates": out})
}

// GetNotificationTemplate godoc
// @Summary      Get one notification template
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type, e.g. ACCOUNT_CREATED"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [get]
func (h *NotificationHandler) GetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.notificationClient.GetTemplate(c.Request.Context(), &notificationpb.GetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}

type setNotificationTemplateRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// SetNotificationTemplate godoc
// @Summary      Customize a notification template
// @Description  Sets the subject/body override for a template type. Placeholders must use {{variable_name}} and reference only that type's known variables.
// @Tags         notification-templates
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type"
// @Param        body    body setNotificationTemplateRequest true "subject + body"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [put]
func (h *NotificationHandler) SetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	var req setNotificationTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, "invalid body")
		return
	}
	if req.Subject == "" || req.Body == "" {
		apiError(c, http.StatusBadRequest, ErrValidation, "subject and body are required")
		return
	}
	updatedBy := uint64(c.GetInt64("principal_id"))
	resp, err := h.notificationClient.SetTemplate(c.Request.Context(), &notificationpb.SetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
		Subject: req.Subject, Body: req.Body, UpdatedBy: updatedBy,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}

// ResetNotificationTemplate godoc
// @Summary      Reset a notification template to its default
// @Tags         notification-templates
// @Produce      json
// @Security     BearerAuth
// @Param        channel path string true "Channel: email | push"
// @Param        type    path string true "Template type"
// @Success      200 {object} map[string]interface{}
// @Failure      404 {object} map[string]interface{}
// @Router       /api/v3/notification-templates/{channel}/{type} [delete]
func (h *NotificationHandler) ResetNotificationTemplate(c *gin.Context) {
	channel, err := oneOf("channel", c.Param("channel"), "email", "push")
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrValidation, err.Error())
		return
	}
	resp, err := h.notificationClient.ResetTemplate(c.Request.Context(), &notificationpb.ResetTemplateRequest{
		Type: c.Param("type"), Channel: channel,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, templateInfoToJSON(resp))
}
```

- [ ] **Step 2: Write the handler tests**

Create `api-gateway/internal/handler/notification_template_handler_test.go`. Check `notification_handler_test.go` for an existing `notificationpb.NotificationServiceClient` stub; if one exists, extend it with the 4 new methods instead of redefining. Otherwise add:

```go
package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exbanka/api-gateway/internal/handler"
	notificationpb "github.com/exbanka/contract/notificationpb"
)

// ntStubClient implements notificationpb.NotificationServiceClient; only the
// template RPCs are exercised here.
type ntStubClient struct {
	notificationpb.NotificationServiceClient
	listFn func(*notificationpb.ListTemplatesRequest) (*notificationpb.ListTemplatesResponse, error)
	getFn  func(*notificationpb.GetTemplateRequest) (*notificationpb.TemplateInfo, error)
	setFn  func(*notificationpb.SetTemplateRequest) (*notificationpb.TemplateInfo, error)
	resetFn func(*notificationpb.ResetTemplateRequest) (*notificationpb.TemplateInfo, error)
}

func (s *ntStubClient) ListTemplates(_ context.Context, in *notificationpb.ListTemplatesRequest, _ ...grpc.CallOption) (*notificationpb.ListTemplatesResponse, error) {
	if s.listFn != nil {
		return s.listFn(in)
	}
	return &notificationpb.ListTemplatesResponse{}, nil
}
func (s *ntStubClient) GetTemplate(_ context.Context, in *notificationpb.GetTemplateRequest, _ ...grpc.CallOption) (*notificationpb.TemplateInfo, error) {
	if s.getFn != nil {
		return s.getFn(in)
	}
	return &notificationpb.TemplateInfo{}, nil
}
func (s *ntStubClient) SetTemplate(_ context.Context, in *notificationpb.SetTemplateRequest, _ ...grpc.CallOption) (*notificationpb.TemplateInfo, error) {
	if s.setFn != nil {
		return s.setFn(in)
	}
	return &notificationpb.TemplateInfo{}, nil
}
func (s *ntStubClient) ResetTemplate(_ context.Context, in *notificationpb.ResetTemplateRequest, _ ...grpc.CallOption) (*notificationpb.TemplateInfo, error) {
	if s.resetFn != nil {
		return s.resetFn(in)
	}
	return &notificationpb.TemplateInfo{}, nil
}

func ntRouter(h *handler.NotificationHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) { c.Set("principal_id", int64(99)); c.Next() }
	r.GET("/notification-templates", withCtx, h.ListNotificationTemplates)
	r.GET("/notification-templates/:channel/:type", withCtx, h.GetNotificationTemplate)
	r.PUT("/notification-templates/:channel/:type", withCtx, h.SetNotificationTemplate)
	r.DELETE("/notification-templates/:channel/:type", withCtx, h.ResetNotificationTemplate)
	return r
}

func TestNotificationTemplate_List(t *testing.T) {
	cl := &ntStubClient{listFn: func(in *notificationpb.ListTemplatesRequest) (*notificationpb.ListTemplatesResponse, error) {
		require.Equal(t, "email", in.Channel)
		return &notificationpb.ListTemplatesResponse{Templates: []*notificationpb.TemplateInfo{
			{Type: "ACTIVATION", Channel: "email", Variables: []*notificationpb.TemplateVariable{{Name: "first_name"}}},
		}}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/notification-templates?channel=email", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"first_name"`)
}

func TestNotificationTemplate_List_BadChannel(t *testing.T) {
	r := ntRouter(handler.NewNotificationHandler(&ntStubClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/notification-templates?channel=sms", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Set_Success(t *testing.T) {
	cl := &ntStubClient{setFn: func(in *notificationpb.SetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		require.Equal(t, "CONFIRMATION", in.Type)
		require.Equal(t, "email", in.Channel)
		require.Equal(t, uint64(99), in.UpdatedBy)
		return &notificationpb.TemplateInfo{Type: in.Type, IsCustomized: true}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi","body":"Hello {{first_name}}"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestNotificationTemplate_Set_MissingFields(t *testing.T) {
	r := ntRouter(handler.NewNotificationHandler(&ntStubClient{}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Set_GRPCValidationError(t *testing.T) {
	cl := &ntStubClient{setFn: func(*notificationpb.SetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		return nil, status.Error(codes.InvalidArgument, "unknown variables [frist_name]")
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("PUT", "/notification-templates/email/CONFIRMATION",
		strings.NewReader(`{"subject":"Hi","body":"{{frist_name}}"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNotificationTemplate_Reset(t *testing.T) {
	cl := &ntStubClient{resetFn: func(in *notificationpb.ResetTemplateRequest) (*notificationpb.TemplateInfo, error) {
		return &notificationpb.TemplateInfo{Type: in.Type, IsCustomized: false}, nil
	}}
	r := ntRouter(handler.NewNotificationHandler(cl))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("DELETE", "/notification-templates/email/CONFIRMATION", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}
```

If `notification_handler_test.go` already defines a `NotificationServiceClient` stub with a different name, reuse it (add the 4 template methods to it) and delete the `ntStubClient` definition above to avoid a duplicate-stub conflict.

- [ ] **Step 3: Run the handler tests**

Run: `cd api-gateway && go test ./internal/handler/ -run TestNotificationTemplate`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/handler/notification_template_handler.go api-gateway/internal/handler/notification_template_handler_test.go
git commit -m "feat(api-gateway): notification-template REST handlers"
```

---

## Phase 9 — api-gateway: routes

### Task 9: Register the notification-template routes

**Files:**
- Modify: `api-gateway/internal/router/router_v3.go`

- [ ] **Step 1: Add the route group**

In `router_v3.go`, find the `bankAccountsRead` group inside the `protected` group (added in Spec 2). After the bank-accounts groups (before the `peerBanksAdmin` group), add:

```go
		// Notification templates — admin-managed copy + variable discovery.
		notifTemplates := protected.Group("/notification-templates")
		notifTemplates.Use(middleware.RequirePermission(perms.Notifications.Templates.Manage))
		{
			notifTemplates.GET("", h.Notification.ListNotificationTemplates)
			notifTemplates.GET("/:channel/:type", h.Notification.GetNotificationTemplate)
			notifTemplates.PUT("/:channel/:type", h.Notification.SetNotificationTemplate)
			notifTemplates.DELETE("/:channel/:type", h.Notification.ResetNotificationTemplate)
		}
```

(Place it adjacent to the other `protected` admin groups; the exact neighbour doesn't matter as long as it's inside the `protected` group block where `perms` and `middleware` are in scope.)

- [ ] **Step 2: Build the gateway**

Run: `cd api-gateway && go build ./...`
Expected: builds clean.

- [ ] **Step 3: Regenerate swagger**

Run: `make swagger`
Expected: `api-gateway/docs/` regenerates with the 4 new `notification-templates` annotations.

- [ ] **Step 4: Commit**

```bash
git add api-gateway/internal/router/router_v3.go api-gateway/docs/
git commit -m "feat(api-gateway): wire notification-template admin routes"
```

---

## Phase 10 — Integration tests

### Task 10: Notification-template integration test

**Files:**
- Create: `test-app/workflows/notification_templates_test.go`

> Requires the docker-compose stack (`make docker-up`). Compile-checked with `go vet -tags integration` regardless.

- [ ] **Step 1: Write the integration tests**

Create `test-app/workflows/notification_templates_test.go`:

```go
//go:build integration

package workflows

import (
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestNotificationTemplates_AdminDiscoveryAndCustomize: an admin lists the
// templates (each with its variables), customizes one, reads it back as
// customized, then resets it.
func TestNotificationTemplates_AdminDiscoveryAndCustomize(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)

	listResp, err := adminC.GET("/api/v3/notification-templates?channel=email")
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if listResp.StatusCode == 404 {
		t.Skip("notification-templates endpoint not deployed")
	}
	helpers.RequireStatus(t, listResp, 200)
	templates, _ := listResp.Body["templates"].([]interface{})
	if len(templates) == 0 {
		t.Fatal("expected at least one template")
	}
	first, _ := templates[0].(map[string]interface{})
	if vars, _ := first["variables"].([]interface{}); len(vars) == 0 {
		t.Error("expected each template to expose a variables array")
	}

	// Customize CONFIRMATION (variable: first_name).
	putResp, err := adminC.PUT("/api/v3/notification-templates/email/CONFIRMATION", map[string]interface{}{
		"subject": "Custom Welcome",
		"body":    "Hi {{first_name}}, custom body.",
	})
	if err != nil {
		t.Fatalf("set template: %v", err)
	}
	helpers.RequireStatus(t, putResp, 200)

	getResp, err := adminC.GET("/api/v3/notification-templates/email/CONFIRMATION")
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	helpers.RequireStatus(t, getResp, 200)
	if isCustom, _ := getResp.Body["is_customized"].(bool); !isCustom {
		t.Error("expected is_customized=true after PUT")
	}

	// Reset.
	delResp, err := adminC.DELETE("/api/v3/notification-templates/email/CONFIRMATION")
	if err != nil {
		t.Fatalf("reset template: %v", err)
	}
	helpers.RequireStatus(t, delResp, 200)
	if isCustom, _ := delResp.Body["is_customized"].(bool); isCustom {
		t.Error("expected is_customized=false after DELETE")
	}
}

// TestNotificationTemplates_UnknownVariableRejected: a body referencing a
// variable the type doesn't support is rejected with 400.
func TestNotificationTemplates_UnknownVariableRejected(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	resp, err := adminC.PUT("/api/v3/notification-templates/email/CONFIRMATION", map[string]interface{}{
		"subject": "x",
		"body":    "Hi {{frist_name}}",
	})
	if err != nil {
		t.Fatalf("set template: %v", err)
	}
	if resp.StatusCode == 404 {
		t.Skip("notification-templates endpoint not deployed")
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for unknown variable, got %d", resp.StatusCode)
	}
}

// TestNotificationTemplates_NonAdminForbidden: a client cannot reach the
// admin template routes.
func TestNotificationTemplates_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	adminC := loginAsAdmin(t)
	_, _, clientC, _ := setupActivatedClient(t, adminC)
	resp, err := clientC.GET("/api/v3/notification-templates")
	if err != nil {
		t.Fatalf("list as client: %v", err)
	}
	if resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Errorf("expected 403 for a client, got %d", resp.StatusCode)
	}
}
```

If the test-app `client.APIClient` has no `DELETE` / `PUT` method, check `test-app/internal/client/` for the available verbs and use whatever the existing workflow tests use for `PUT`/`DELETE` (other workflow files exercise `PUT`/`DELETE` routes — match their call style).

- [ ] **Step 2: Compile-check with the integration tag**

Run: `cd test-app && go vet -tags integration ./...`
Expected: no errors.

- [ ] **Step 3: Run the integration tests** (requires `make docker-up`)

Run the integration suite filtered to `TestNotificationTemplates`.
Expected: PASS (or `Skip` if endpoints aren't deployed).

- [ ] **Step 4: Commit**

```bash
git add test-app/workflows/notification_templates_test.go
git commit -m "test(notifications): notification-template integration tests"
```

---

## Phase 11 — Documentation + final verification

### Task 11: Docs + full build/test/lint

**Files:**
- Modify: `docs/Specification.md`, `docs/api/REST_API_v3.md`

- [ ] **Step 1: `REST_API_v3.md`**

Add a new top-level section (after the bank-accounts section, before whatever follows) titled `## Notification Templates`. Document the 4 endpoints, each with auth (`Employee JWT + notifications.templates.manage`), params, request/response shape, and error codes:

- `GET /api/v3/notification-templates` (optional `?channel=`) → `{ "templates": [ { type, channel, description, variables: [{name, description, example}], default_subject, default_body, current_subject, current_body, is_customized } ] }`. Note this is the **discovery endpoint** — it lists which `{{variables}}` each template supports.
- `GET /api/v3/notification-templates/:channel/:type` → a single `TemplateInfo` object.
- `PUT /api/v3/notification-templates/:channel/:type` — body `{ "subject": "...", "body": "..." }`; `400` if a `{{placeholder}}` references an unknown variable; `404` for an unknown type.
- `DELETE /api/v3/notification-templates/:channel/:type` — reverts to the registry default.

Include a worked example: customizing `email/CONFIRMATION` with body `"Hi {{first_name}}!"`.

- [ ] **Step 2: `Specification.md`**

- §6 permissions: add `notifications.templates.manage` — "allows EmployeeAdmin to customize notification template subject/body text".
- §17 routes table: add the 4 `/api/v3/notification-templates` rows, middleware `AuthMiddleware + RequirePermission(notifications.templates.manage)`, handler `NotificationHandler.{List,Get,Set,Reset}NotificationTemplate`.
- §18 entities: add `NotificationTemplate` (`notification_templates`) — "admin override of a registry template's subject/body, unique on (type, channel); absence ⇒ the code-defined registry default is used".
- §19/gRPC section: add the 4 `NotificationService` RPCs (`ListTemplates`, `GetTemplate`, `SetTemplate`, `ResetTemplate`).
- Add a short subsection describing the registry + override model and the `{{variable}}` substitution rule.

- [ ] **Step 3: Commit docs**

```bash
git add docs/Specification.md docs/api/REST_API_v3.md
git commit -m "docs(notifications): document notification template management"
```

- [ ] **Step 4: Full build + test + lint**

Run: `make build`
Expected: all services compile, swagger generates.

Run: `make test`
Expected: all unit tests pass across all services.

Run: `make lint`
Expected: no new lint warnings in `contract`, `notification-service`, or `api-gateway`.

- [ ] **Step 5: Integration** (requires Docker)

Run the integration suite filtered to `TestNotificationTemplates` against `make docker-up`.
Expected: green.

---

## Self-Review Notes

- **Spec coverage:** Registry + overrides (spec §1) → Tasks 3, 4. Rendering engine (§2) → Tasks 5, 6. API CRUD + discovery (§3) → Tasks 2, 7, 8, 9. Validation (§4) → Task 5 (service) + Task 8 (gateway shallow checks). Permission (§5) → Task 1 + Task 9. Testing (§Testing) → unit in Tasks 3–8, integration in Task 10. Docs → Task 11. Every spec section maps to tasks.
- **Placeholder scan:** none — every step has concrete code or an exact command. The registry (Task 3) is given in full; the doc tasks (11) describe concrete content to add to existing files.
- **Type consistency:** `TemplateService.Render/List/GetOne/Set/Reset` signatures (Task 5) match the `templateServiceFacade` interface (Task 6) and the gRPC handler calls (Task 7). `templates.Definition`/`Variable`/`Get`/`All`/`KnownVars` (Task 3) are used verbatim in Task 5. `service.TemplateView` fields (`Type`,`Channel`,`Description`,`Variables`,`Default*`,`Current*`,`IsCustomized`) are mapped 1:1 in `templateViewToProto` (Task 7) and `templateInfoToJSON` (Task 8). Proto message/field names (Task 2) — `TemplateInfo`, `SetTemplateRequest{Type,Channel,Subject,Body,UpdatedBy}` — match every call site. `perms.Notifications.Templates.Manage` (Task 1) is used in Task 9.
- **Build-order:** Tasks 1–5 each end at a compiling state. Task 6 leaves `cmd/main.go` temporarily broken (old constructor signatures) — Task 6 Step 7 explicitly builds only the changed packages; Task 7 fixes `main.go` and the full notification-service build/test/lint runs at Task 7 Step 5. The api-gateway compiles independently from Task 2 onward (proto regen is backward-compatible — only additions).
- **Note:** `make docker-up` may not be running in every environment — Task 10 Step 3 and Task 11 Step 5 are guarded with `t.Skip` and can be deferred; the unit tests in Tasks 3–8 fully cover registry, rendering, validation, and both handler layers.
