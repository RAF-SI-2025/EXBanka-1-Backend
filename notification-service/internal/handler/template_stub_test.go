package handler

import "github.com/exbanka/notification-service/internal/service"

// stubTemplateSvc is a test double implementing templateServiceFacade.
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
func (s *stubTemplateSvc) GetOne(_, _ string) (service.TemplateView, error) {
	return s.one, s.err
}
func (s *stubTemplateSvc) Set(_, _, _, _ string, _ uint64) (service.TemplateView, error) {
	return s.one, s.err
}
func (s *stubTemplateSvc) Reset(_, _ string) (service.TemplateView, error) { return s.one, s.err }
