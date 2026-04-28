package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/model"
)

type mockEmailSender struct {
	sendFn func(to, subject, body string) error
	calls  []struct{ to, subject, body string }
}

func (m *mockEmailSender) Send(to, subject, body string) error {
	m.calls = append(m.calls, struct{ to, subject, body string }{to, subject, body})
	if m.sendFn != nil {
		return m.sendFn(to, subject, body)
	}
	return nil
}

type mockInboxRepo struct {
	getFn func(userID uint64) ([]model.MobileInboxItem, error)
	ackFn func(id uint64) error
}

func (m *mockInboxRepo) GetPendingByUser(userID uint64) ([]model.MobileInboxItem, error) {
	if m.getFn != nil {
		return m.getFn(userID)
	}
	return nil, nil
}

func (m *mockInboxRepo) MarkDelivered(id uint64) error {
	if m.ackFn != nil {
		return m.ackFn(id)
	}
	return nil
}

type mockNotifRepo struct {
	listFn        func(userID uint64, readFilter *bool, page, pageSize int) ([]model.GeneralNotification, int64, error)
	unreadFn      func(userID uint64) (int64, error)
	markReadFn    func(id, userID uint64) error
	markAllReadFn func(userID uint64) (int64, error)
}

func (m *mockNotifRepo) ListByUser(userID uint64, readFilter *bool, page, pageSize int) ([]model.GeneralNotification, int64, error) {
	if m.listFn != nil {
		return m.listFn(userID, readFilter, page, pageSize)
	}
	return nil, 0, nil
}

func (m *mockNotifRepo) UnreadCount(userID uint64) (int64, error) {
	if m.unreadFn != nil {
		return m.unreadFn(userID)
	}
	return 0, nil
}

func (m *mockNotifRepo) MarkRead(id, userID uint64) error {
	if m.markReadFn != nil {
		return m.markReadFn(id, userID)
	}
	return nil
}

func (m *mockNotifRepo) MarkAllRead(userID uint64) (int64, error) {
	if m.markAllReadFn != nil {
		return m.markAllReadFn(userID)
	}
	return 0, nil
}

// ---------------------------------------------------------------------------
// SendEmail
// ---------------------------------------------------------------------------

func TestGRPCHandler_SendEmail_Success(t *testing.T) {
	sender := &mockEmailSender{}
	h := newGRPCHandlerForTest(sender, &mockInboxRepo{}, &mockNotifRepo{})
	resp, err := h.SendEmail(context.Background(), &notifpb.SendEmailRequest{
		To: "user@example.com", EmailType: "verification", Data: map[string]string{"code": "123456"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got %s", resp.Message)
	}
	if len(sender.calls) != 1 || sender.calls[0].to != "user@example.com" {
		t.Errorf("expected sender called once for user@example.com, got %+v", sender.calls)
	}
}

func TestGRPCHandler_SendEmail_MissingRecipient(t *testing.T) {
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, &mockNotifRepo{})
	_, err := h.SendEmail(context.Background(), &notifpb.SendEmailRequest{To: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestGRPCHandler_SendEmail_SenderError(t *testing.T) {
	sender := &mockEmailSender{
		sendFn: func(_, _, _ string) error { return errors.New("smtp down") },
	}
	h := newGRPCHandlerForTest(sender, &mockInboxRepo{}, &mockNotifRepo{})
	resp, err := h.SendEmail(context.Background(), &notifpb.SendEmailRequest{To: "user@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected Success=false on send error")
	}
	if resp.Message == "" {
		t.Error("expected error message in response")
	}
}

// ---------------------------------------------------------------------------
// GetDeliveryStatus
// ---------------------------------------------------------------------------

func TestGRPCHandler_GetDeliveryStatus_Unimplemented(t *testing.T) {
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, &mockNotifRepo{})
	_, err := h.GetDeliveryStatus(context.Background(), &notifpb.GetDeliveryStatusRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetPendingMobileItems
// ---------------------------------------------------------------------------

func TestGRPCHandler_GetPendingMobileItems_Success(t *testing.T) {
	now := time.Now()
	repo := &mockInboxRepo{
		getFn: func(_ uint64) ([]model.MobileInboxItem, error) {
			return []model.MobileInboxItem{
				{
					ID: 1, ChallengeID: 100, Method: "code_pull",
					DisplayData: []byte(`{"amount":"100"}`), ExpiresAt: now.Add(time.Minute),
				},
			}, nil
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, repo, &mockNotifRepo{})
	resp, err := h.GetPendingMobileItems(context.Background(), &notifpb.GetPendingMobileRequest{UserId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].DisplayData != `{"amount":"100"}` {
		t.Errorf("unexpected display data: %s", resp.Items[0].DisplayData)
	}
}

func TestGRPCHandler_GetPendingMobileItems_Error(t *testing.T) {
	repo := &mockInboxRepo{
		getFn: func(_ uint64) ([]model.MobileInboxItem, error) {
			return nil, errors.New("db down")
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, repo, &mockNotifRepo{})
	_, err := h.GetPendingMobileItems(context.Background(), &notifpb.GetPendingMobileRequest{UserId: 5})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// AckMobileItem
// ---------------------------------------------------------------------------

func TestGRPCHandler_AckMobileItem_Success(t *testing.T) {
	captured := uint64(0)
	repo := &mockInboxRepo{
		ackFn: func(id uint64) error { captured = id; return nil },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, repo, &mockNotifRepo{})
	resp, err := h.AckMobileItem(context.Background(), &notifpb.AckMobileRequest{Id: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
	if captured != 7 {
		t.Errorf("expected MarkDelivered called with 7, got %d", captured)
	}
}

func TestGRPCHandler_AckMobileItem_NotFound(t *testing.T) {
	repo := &mockInboxRepo{
		ackFn: func(_ uint64) error { return errors.New("not found") },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, repo, &mockNotifRepo{})
	_, err := h.AckMobileItem(context.Background(), &notifpb.AckMobileRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// ListNotifications
// ---------------------------------------------------------------------------

func TestGRPCHandler_ListNotifications_Success(t *testing.T) {
	now := time.Now()
	repo := &mockNotifRepo{
		listFn: func(_ uint64, _ *bool, _, _ int) ([]model.GeneralNotification, int64, error) {
			return []model.GeneralNotification{
				{ID: 1, Type: "info", Title: "Hello", Message: "World", IsRead: false, CreatedAt: now},
			}, 1, nil
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	resp, err := h.ListNotifications(context.Background(), &notifpb.ListNotificationsRequest{UserId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 || len(resp.Notifications) != 1 {
		t.Errorf("expected 1 notification, got %d", len(resp.Notifications))
	}
}

func TestGRPCHandler_ListNotifications_ReadFilter(t *testing.T) {
	captured := struct {
		userID uint64
		filter *bool
		page   int
		size   int
	}{}
	repo := &mockNotifRepo{
		listFn: func(userID uint64, filter *bool, page, pageSize int) ([]model.GeneralNotification, int64, error) {
			captured.userID = userID
			captured.filter = filter
			captured.page = page
			captured.size = pageSize
			return nil, 0, nil
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.ListNotifications(context.Background(), &notifpb.ListNotificationsRequest{
		UserId: 5, ReadFilter: "read", Page: 2, PageSize: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.filter == nil || !*captured.filter {
		t.Errorf("expected read filter true, got %v", captured.filter)
	}
	if captured.page != 2 || captured.size != 50 {
		t.Errorf("expected page=2 size=50, got page=%d size=%d", captured.page, captured.size)
	}
}

func TestGRPCHandler_ListNotifications_UnreadFilter(t *testing.T) {
	captured := (*bool)(nil)
	repo := &mockNotifRepo{
		listFn: func(_ uint64, filter *bool, _, _ int) ([]model.GeneralNotification, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.ListNotifications(context.Background(), &notifpb.ListNotificationsRequest{
		UserId: 5, ReadFilter: "unread",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil || *captured {
		t.Errorf("expected unread filter false, got %v", captured)
	}
}

func TestGRPCHandler_ListNotifications_DefaultsPagination(t *testing.T) {
	captured := struct{ page, size int }{}
	repo := &mockNotifRepo{
		listFn: func(_ uint64, _ *bool, page, pageSize int) ([]model.GeneralNotification, int64, error) {
			captured.page = page
			captured.size = pageSize
			return nil, 0, nil
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.ListNotifications(context.Background(), &notifpb.ListNotificationsRequest{UserId: 5, Page: 0, PageSize: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.page != 1 || captured.size != 20 {
		t.Errorf("expected page=1 size=20, got page=%d size=%d", captured.page, captured.size)
	}
}

func TestGRPCHandler_ListNotifications_Error(t *testing.T) {
	repo := &mockNotifRepo{
		listFn: func(_ uint64, _ *bool, _, _ int) ([]model.GeneralNotification, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.ListNotifications(context.Background(), &notifpb.ListNotificationsRequest{UserId: 5})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// GetUnreadCount
// ---------------------------------------------------------------------------

func TestGRPCHandler_GetUnreadCount_Success(t *testing.T) {
	repo := &mockNotifRepo{
		unreadFn: func(_ uint64) (int64, error) { return 5, nil },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	resp, err := h.GetUnreadCount(context.Background(), &notifpb.GetUnreadCountRequest{UserId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 5 {
		t.Errorf("expected 5, got %d", resp.Count)
	}
}

func TestGRPCHandler_GetUnreadCount_Error(t *testing.T) {
	repo := &mockNotifRepo{
		unreadFn: func(_ uint64) (int64, error) { return 0, errors.New("db down") },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.GetUnreadCount(context.Background(), &notifpb.GetUnreadCountRequest{UserId: 5})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// MarkNotificationRead
// ---------------------------------------------------------------------------

func TestGRPCHandler_MarkNotificationRead_Success(t *testing.T) {
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, &mockNotifRepo{})
	resp, err := h.MarkNotificationRead(context.Background(), &notifpb.MarkNotificationReadRequest{Id: 1, UserId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
}

func TestGRPCHandler_MarkNotificationRead_NotFound(t *testing.T) {
	repo := &mockNotifRepo{
		markReadFn: func(_, _ uint64) error { return errors.New("not found") },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.MarkNotificationRead(context.Background(), &notifpb.MarkNotificationReadRequest{Id: 99, UserId: 5})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// MarkAllNotificationsRead
// ---------------------------------------------------------------------------

func TestGRPCHandler_MarkAllNotificationsRead_Success(t *testing.T) {
	repo := &mockNotifRepo{
		markAllReadFn: func(_ uint64) (int64, error) { return 7, nil },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	resp, err := h.MarkAllNotificationsRead(context.Background(), &notifpb.MarkAllNotificationsReadRequest{UserId: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Count != 7 {
		t.Errorf("expected count 7, got %d", resp.Count)
	}
}

func TestGRPCHandler_MarkAllNotificationsRead_Error(t *testing.T) {
	repo := &mockNotifRepo{
		markAllReadFn: func(_ uint64) (int64, error) { return 0, errors.New("db down") },
	}
	h := newGRPCHandlerForTest(&mockEmailSender{}, &mockInboxRepo{}, repo)
	_, err := h.MarkAllNotificationsRead(context.Background(), &notifpb.MarkAllNotificationsReadRequest{UserId: 5})
	if status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}
