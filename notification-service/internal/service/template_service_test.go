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
