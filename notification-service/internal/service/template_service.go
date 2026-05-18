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
