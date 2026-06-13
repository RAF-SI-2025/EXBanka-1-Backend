package handler

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/service"
)

// TestTemplateHandlers_ErrorMapping drives templateErr's three switch arms
// (NotFound / InvalidArgument / Internal) across every template handler.
func TestTemplateHandlers_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"type-not-found", service.ErrTemplateTypeNotFound, codes.NotFound},
		{"validation", service.ErrTemplateValidation, codes.InvalidArgument},
		{"generic-internal", errors.New("db exploded"), codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubTemplateSvc{err: tc.err}
			h := newGRPCHandlerForTest(nil, nil, nil, svc)

			if _, err := h.ListTemplates(context.Background(), &notifpb.ListTemplatesRequest{Channel: "email"}); status.Code(err) != tc.want {
				t.Errorf("ListTemplates: got %v, want %v", status.Code(err), tc.want)
			}
			if _, err := h.GetTemplate(context.Background(), &notifpb.GetTemplateRequest{Type: "X", Channel: "email"}); status.Code(err) != tc.want {
				t.Errorf("GetTemplate: got %v, want %v", status.Code(err), tc.want)
			}
			if _, err := h.SetTemplate(context.Background(), &notifpb.SetTemplateRequest{Type: "X", Channel: "email", Subject: "s", Body: "b"}); status.Code(err) != tc.want {
				t.Errorf("SetTemplate: got %v, want %v", status.Code(err), tc.want)
			}
			if _, err := h.ResetTemplate(context.Background(), &notifpb.ResetTemplateRequest{Type: "X", Channel: "email"}); status.Code(err) != tc.want {
				t.Errorf("ResetTemplate: got %v, want %v", status.Code(err), tc.want)
			}
		})
	}
}
