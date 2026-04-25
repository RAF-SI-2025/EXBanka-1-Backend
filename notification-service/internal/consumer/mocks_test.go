package consumer

import (
	"context"
	"sync"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/notification-service/internal/model"
)

// stubEmailSender records calls to Send and returns a canned error.
type stubEmailSender struct {
	mu       sync.Mutex
	calls    []emailSendCall
	sendErr  error
	callback func(to, subject, body string)
}

type emailSendCall struct {
	To      string
	Subject string
	Body    string
}

func (s *stubEmailSender) Send(to, subject, body string) error {
	s.mu.Lock()
	s.calls = append(s.calls, emailSendCall{To: to, Subject: subject, Body: body})
	cb := s.callback
	s.mu.Unlock()
	if cb != nil {
		cb(to, subject, body)
	}
	return s.sendErr
}

func (s *stubEmailSender) sentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubEmailSender) lastCall() (emailSendCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return emailSendCall{}, false
	}
	return s.calls[len(s.calls)-1], true
}

// stubEmailSentPublisher records PublishEmailSent calls.
type stubEmailSentPublisher struct {
	mu          sync.Mutex
	confirms    []kafkamsg.EmailSentMessage
	publishErr  error
	callCounter int
}

func (s *stubEmailSentPublisher) PublishEmailSent(_ context.Context, msg kafkamsg.EmailSentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCounter++
	if s.publishErr != nil {
		return s.publishErr
	}
	s.confirms = append(s.confirms, msg)
	return nil
}

func (s *stubEmailSentPublisher) lastConfirm() (kafkamsg.EmailSentMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.confirms) == 0 {
		return kafkamsg.EmailSentMessage{}, false
	}
	return s.confirms[len(s.confirms)-1], true
}

// stubGenericPublisher captures generic Publish calls (used by VerificationConsumer).
type stubGenericPublisher struct {
	mu         sync.Mutex
	topics     []string
	payloads   []interface{}
	publishErr error
}

func (s *stubGenericPublisher) Publish(_ context.Context, topic string, msg interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publishErr != nil {
		return s.publishErr
	}
	s.topics = append(s.topics, topic)
	s.payloads = append(s.payloads, msg)
	return nil
}

func (s *stubGenericPublisher) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.topics)
}

// stubGeneralNotificationCreator captures Create calls.
type stubGeneralNotificationCreator struct {
	mu       sync.Mutex
	created  []*model.GeneralNotification
	createErr error
}

func (s *stubGeneralNotificationCreator) Create(n *model.GeneralNotification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, n)
	return nil
}

// stubInboxItemCreator captures Create calls.
type stubInboxItemCreator struct {
	mu        sync.Mutex
	created   []*model.MobileInboxItem
	createErr error
}

func (s *stubInboxItemCreator) Create(item *model.MobileInboxItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.created = append(s.created, item)
	return nil
}
