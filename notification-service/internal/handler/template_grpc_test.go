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
