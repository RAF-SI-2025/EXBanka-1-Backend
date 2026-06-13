package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	"github.com/exbanka/notification-service/internal/service"
)

// --- mock audit repos -------------------------------------------------------

type mockAdminAuditRepo struct {
	listFn func(f repository.AdminAuditLogFilters, page, pageSize int) ([]model.AdminAuditLog, int64, error)
	got    struct {
		filters  repository.AdminAuditLogFilters
		page     int
		pageSize int
	}
}

func (m *mockAdminAuditRepo) ListAll(f repository.AdminAuditLogFilters, page, pageSize int) ([]model.AdminAuditLog, int64, error) {
	m.got.filters, m.got.page, m.got.pageSize = f, page, pageSize
	if m.listFn != nil {
		return m.listFn(f, page, pageSize)
	}
	return nil, 0, nil
}

type mockBusinessAuditRepo struct {
	listFn func(f repository.BusinessAuditLogFilters, page, pageSize int) ([]model.BusinessAuditLog, int64, error)
	got    struct {
		filters  repository.BusinessAuditLogFilters
		page     int
		pageSize int
	}
}

func (m *mockBusinessAuditRepo) ListAll(f repository.BusinessAuditLogFilters, page, pageSize int) ([]model.BusinessAuditLog, int64, error) {
	m.got.filters, m.got.page, m.got.pageSize = f, page, pageSize
	if m.listFn != nil {
		return m.listFn(f, page, pageSize)
	}
	return nil, 0, nil
}

// --- NewGRPCHandler ---------------------------------------------------------

func TestNewGRPCHandler_WiresRealRepos(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(
		&model.AdminAuditLog{}, &model.BusinessAuditLog{},
		&model.MobileInboxItem{}, &model.GeneralNotification{}, &model.NotificationTemplate{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := NewGRPCHandler(
		sender.NewEmailSender("h", "1", "u", "p", "f"),
		repository.NewMobileInboxRepository(db),
		repository.NewGeneralNotificationRepository(db),
		service.NewTemplateService(repository.NewTemplateRepository(db)),
		repository.NewAdminAuditLogRepository(db),
		repository.NewBusinessAuditLogRepository(db),
	)
	if h == nil {
		t.Fatal("NewGRPCHandler returned nil")
	}

	// Real (non-nil) audit repos wired → success with an empty result, NOT the
	// Unimplemented sentinel returned when the repo is missing.
	adminResp, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{})
	if err != nil {
		t.Fatalf("admin list: unexpected error %v", err)
	}
	if adminResp.Total != 0 || len(adminResp.Entries) != 0 {
		t.Errorf("admin: expected empty result, got total=%d", adminResp.Total)
	}
	bizResp, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{})
	if err != nil {
		t.Fatalf("business list: unexpected error %v", err)
	}
	if bizResp.Total != 0 || len(bizResp.Entries) != 0 {
		t.Errorf("business: expected empty result, got total=%d", bizResp.Total)
	}
}

// --- ListAdminAuditLogs -----------------------------------------------------

func TestListAdminAuditLogs_Success(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0).UTC()
	repo := &mockAdminAuditRepo{
		listFn: func(repository.AdminAuditLogFilters, int, int) ([]model.AdminAuditLog, int64, error) {
			return []model.AdminAuditLog{
				{ID: 9, Action: "pause", Service: "credit-service", CronName: "installment", EmployeeID: 3, Reason: "maintenance", Timestamp: ts},
			}, 1, nil
		},
	}
	h := &GRPCHandler{adminAuditRepo: repo}

	resp, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{
		Page: 2, PageSize: 25, Since: 100, Until: 200, ActorId: 3, Action: "pause",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 || len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got total=%d len=%d", resp.Total, len(resp.Entries))
	}
	e := resp.Entries[0]
	if e.Id != 9 || e.Action != "pause" || e.Service != "credit-service" || e.CronName != "installment" ||
		e.EmployeeId != 3 || e.Reason != "maintenance" || e.Timestamp != ts.Unix() {
		t.Errorf("entry mapping mismatch: %+v", e)
	}
	if resp.Page != 2 || resp.PageSize != 25 {
		t.Errorf("expected page=2 size=25, got page=%d size=%d", resp.Page, resp.PageSize)
	}
	// Filters propagated verbatim.
	if repo.got.filters.Since != 100 || repo.got.filters.Until != 200 || repo.got.filters.ActorID != 3 || repo.got.filters.Action != "pause" {
		t.Errorf("filters not propagated: %+v", repo.got.filters)
	}
	if repo.got.page != 2 || repo.got.pageSize != 25 {
		t.Errorf("pagination not propagated: page=%d size=%d", repo.got.page, repo.got.pageSize)
	}
}

func TestListAdminAuditLogs_PaginationDefaultsAndClamp(t *testing.T) {
	repo := &mockAdminAuditRepo{}
	h := &GRPCHandler{adminAuditRepo: repo}

	// page<1 and pageSize<=0 → defaults (1, 50).
	if _, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{Page: 0, PageSize: 0}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.got.page != 1 || repo.got.pageSize != 50 {
		t.Errorf("defaults: expected page=1 size=50, got page=%d size=%d", repo.got.page, repo.got.pageSize)
	}

	// pageSize>200 → clamped to 200.
	if _, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{Page: 1, PageSize: 9999}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.got.pageSize != 200 {
		t.Errorf("clamp: expected size=200, got %d", repo.got.pageSize)
	}
}

func TestListAdminAuditLogs_RepoError(t *testing.T) {
	repo := &mockAdminAuditRepo{
		listFn: func(repository.AdminAuditLogFilters, int, int) ([]model.AdminAuditLog, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := &GRPCHandler{adminAuditRepo: repo}
	if _, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{}); status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestListAdminAuditLogs_Unimplemented(t *testing.T) {
	h := &GRPCHandler{} // adminAuditRepo is nil
	if _, err := h.ListAdminAuditLogs(context.Background(), &notifpb.ListAdminAuditLogsRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", status.Code(err))
	}
}

// --- ListBusinessAuditLogs --------------------------------------------------

func TestListBusinessAuditLogs_Success(t *testing.T) {
	ts := time.Unix(1_700_000_500, 0).UTC()
	repo := &mockBusinessAuditRepo{
		listFn: func(repository.BusinessAuditLogFilters, int, int) ([]model.BusinessAuditLog, int64, error) {
			return []model.BusinessAuditLog{
				{ID: 4, Action: "limit.set", ActorID: 7, TargetType: "employee", TargetID: "12", Detail: "max=5000", Timestamp: ts},
			}, 1, nil
		},
	}
	h := &GRPCHandler{businessAuditRepo: repo}

	resp, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{
		Page: 3, PageSize: 10, Since: 50, Until: 60, ActorId: 7, Action: "limit.set", TargetType: "employee",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 || len(resp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got total=%d len=%d", resp.Total, len(resp.Entries))
	}
	e := resp.Entries[0]
	if e.Id != 4 || e.Action != "limit.set" || e.ActorId != 7 || e.TargetType != "employee" ||
		e.TargetId != "12" || e.Detail != "max=5000" || e.Timestamp != ts.Unix() {
		t.Errorf("entry mapping mismatch: %+v", e)
	}
	if resp.Page != 3 || resp.PageSize != 10 {
		t.Errorf("expected page=3 size=10, got page=%d size=%d", resp.Page, resp.PageSize)
	}
	if repo.got.filters.Since != 50 || repo.got.filters.Until != 60 || repo.got.filters.ActorID != 7 ||
		repo.got.filters.Action != "limit.set" || repo.got.filters.TargetType != "employee" {
		t.Errorf("filters not propagated: %+v", repo.got.filters)
	}
}

func TestListBusinessAuditLogs_PaginationDefaultsAndClamp(t *testing.T) {
	repo := &mockBusinessAuditRepo{}
	h := &GRPCHandler{businessAuditRepo: repo}

	if _, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{Page: -1, PageSize: -5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.got.page != 1 || repo.got.pageSize != 50 {
		t.Errorf("defaults: expected page=1 size=50, got page=%d size=%d", repo.got.page, repo.got.pageSize)
	}

	if _, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{Page: 1, PageSize: 500}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.got.pageSize != 200 {
		t.Errorf("clamp: expected size=200, got %d", repo.got.pageSize)
	}
}

func TestListBusinessAuditLogs_RepoError(t *testing.T) {
	repo := &mockBusinessAuditRepo{
		listFn: func(repository.BusinessAuditLogFilters, int, int) ([]model.BusinessAuditLog, int64, error) {
			return nil, 0, errors.New("db down")
		},
	}
	h := &GRPCHandler{businessAuditRepo: repo}
	if _, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{}); status.Code(err) != codes.Internal {
		t.Errorf("expected Internal, got %v", status.Code(err))
	}
}

func TestListBusinessAuditLogs_Unimplemented(t *testing.T) {
	h := &GRPCHandler{} // businessAuditRepo is nil
	if _, err := h.ListBusinessAuditLogs(context.Background(), &notifpb.ListBusinessAuditLogsRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", status.Code(err))
	}
}
