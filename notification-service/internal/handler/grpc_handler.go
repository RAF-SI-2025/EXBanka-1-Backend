package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	"github.com/exbanka/notification-service/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// emailSenderFacade is the narrow interface of *sender.EmailSender used by GRPCHandler.
type emailSenderFacade interface {
	Send(to, subject, body string) error
}

// inboxRepoFacade is the narrow interface of *repository.MobileInboxRepository used by GRPCHandler.
type inboxRepoFacade interface {
	GetPendingByUser(userID uint64) ([]model.MobileInboxItem, error)
	MarkDelivered(id uint64) error
}

// notifRepoFacade is the narrow interface of *repository.GeneralNotificationRepository used by GRPCHandler.
type notifRepoFacade interface {
	ListByUser(userID uint64, readFilter *bool, page, pageSize int) ([]model.GeneralNotification, int64, error)
	UnreadCount(userID uint64) (int64, error)
	MarkRead(id, userID uint64) error
	MarkAllRead(userID uint64) (int64, error)
}

// adminAuditRepoFacade is the narrow interface of *repository.AdminAuditLogRepository used by GRPCHandler.
type adminAuditRepoFacade interface {
	ListAll(filters repository.AdminAuditLogFilters, page, pageSize int) ([]model.AdminAuditLog, int64, error)
}

// templateServiceFacade is the narrow interface of *service.TemplateService used by GRPCHandler.
type templateServiceFacade interface {
	Render(typ, channel string, data map[string]string) (subject, body string, err error)
	List(channel string) ([]service.TemplateView, error)
	GetOne(typ, channel string) (service.TemplateView, error)
	Set(typ, channel, subject, body string, updatedBy uint64) (service.TemplateView, error)
	Reset(typ, channel string) (service.TemplateView, error)
}

type GRPCHandler struct {
	notifpb.UnimplementedNotificationServiceServer
	emailSender    emailSenderFacade
	inboxRepo      inboxRepoFacade
	notifRepo      notifRepoFacade
	templateSvc    templateServiceFacade
	adminAuditRepo adminAuditRepoFacade
}

func NewGRPCHandler(emailSender *sender.EmailSender, inboxRepo *repository.MobileInboxRepository, notifRepo *repository.GeneralNotificationRepository, templateSvc *service.TemplateService, adminAuditRepo *repository.AdminAuditLogRepository) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo, templateSvc: templateSvc, adminAuditRepo: adminAuditRepo}
}

// newGRPCHandlerForTest constructs a GRPCHandler with interface-typed
// dependencies for use in unit tests.
func newGRPCHandlerForTest(emailSender emailSenderFacade, inboxRepo inboxRepoFacade, notifRepo notifRepoFacade, templateSvc templateServiceFacade) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo, templateSvc: templateSvc}
}

func (h *GRPCHandler) SendEmail(ctx context.Context, req *notifpb.SendEmailRequest) (*notifpb.SendEmailResponse, error) {
	if req.To == "" {
		return nil, fmt.Errorf("SendEmail: recipient required: %w", service.ErrInvalidEmailRequest)
	}

	subject, body, err := h.templateSvc.Render(req.EmailType, "email", req.Data)
	if err != nil {
		return &notifpb.SendEmailResponse{Success: false, Message: err.Error()}, nil
	}

	if err := h.emailSender.Send(req.To, subject, body); err != nil {
		log.Printf("gRPC SendEmail failed for %s: %v", req.To, err)
		return &notifpb.SendEmailResponse{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	log.Printf("gRPC SendEmail succeeded for %s", req.To)
	return &notifpb.SendEmailResponse{
		Success: true,
		Message: "email sent",
	}, nil
}

func (h *GRPCHandler) GetDeliveryStatus(ctx context.Context, req *notifpb.GetDeliveryStatusRequest) (*notifpb.GetDeliveryStatusResponse, error) {
	// Placeholder — will be backed by a database or Redis in a future iteration
	return nil, service.ErrDeliveryStatusUnimplemented
}

func (h *GRPCHandler) GetPendingMobileItems(ctx context.Context, req *notifpb.GetPendingMobileRequest) (*notifpb.PendingMobileResponse, error) {
	items, err := h.inboxRepo.GetPendingByUser(req.UserId)
	if err != nil {
		return nil, fmt.Errorf("GetPendingMobileItems(user=%d): %v: %w", req.UserId, err, service.ErrInboxLookupFailed)
	}

	entries := make([]*notifpb.MobileInboxEntry, len(items))
	for i, item := range items {
		entries[i] = &notifpb.MobileInboxEntry{
			Id:          item.ID,
			ChallengeId: item.ChallengeID,
			Method:      item.Method,
			DisplayData: string(item.DisplayData),
			ExpiresAt:   item.ExpiresAt.Format(time.RFC3339),
		}
	}
	return &notifpb.PendingMobileResponse{Items: entries}, nil
}

func (h *GRPCHandler) AckMobileItem(ctx context.Context, req *notifpb.AckMobileRequest) (*notifpb.AckMobileResponse, error) {
	if err := h.inboxRepo.MarkDelivered(req.Id); err != nil {
		return nil, fmt.Errorf("AckMobileItem(id=%d): %v: %w", req.Id, err, service.ErrInboxItemNotFound)
	}
	return &notifpb.AckMobileResponse{Success: true}, nil
}

// ── General notifications ────────────────────────────────────────────────────

func (h *GRPCHandler) ListNotifications(ctx context.Context, req *notifpb.ListNotificationsRequest) (*notifpb.ListNotificationsResponse, error) {
	page := int(req.Page)
	if page < 1 {
		page = 1
	}
	pageSize := int(req.PageSize)
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var readFilter *bool
	switch req.ReadFilter {
	case "read":
		v := true
		readFilter = &v
	case "unread":
		v := false
		readFilter = &v
	}

	items, total, err := h.notifRepo.ListByUser(req.UserId, readFilter, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("ListNotifications(user=%d): %v: %w", req.UserId, err, service.ErrNotificationLookupFailed)
	}

	entries := make([]*notifpb.NotificationEntry, len(items))
	for i, item := range items {
		entries[i] = &notifpb.NotificationEntry{
			Id:        item.ID,
			Type:      item.Type,
			Title:     item.Title,
			Message:   item.Message,
			IsRead:    item.IsRead,
			RefType:   item.RefType,
			RefId:     item.RefID,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		}
	}
	return &notifpb.ListNotificationsResponse{Notifications: entries, Total: total}, nil
}

func (h *GRPCHandler) GetUnreadCount(ctx context.Context, req *notifpb.GetUnreadCountRequest) (*notifpb.GetUnreadCountResponse, error) {
	count, err := h.notifRepo.UnreadCount(req.UserId)
	if err != nil {
		return nil, fmt.Errorf("GetUnreadCount(user=%d): %v: %w", req.UserId, err, service.ErrNotificationLookupFailed)
	}
	return &notifpb.GetUnreadCountResponse{Count: count}, nil
}

func (h *GRPCHandler) MarkNotificationRead(ctx context.Context, req *notifpb.MarkNotificationReadRequest) (*notifpb.MarkNotificationReadResponse, error) {
	if err := h.notifRepo.MarkRead(req.Id, req.UserId); err != nil {
		return nil, fmt.Errorf("MarkNotificationRead(id=%d, user=%d): %v: %w", req.Id, req.UserId, err, service.ErrNotificationNotFound)
	}
	return &notifpb.MarkNotificationReadResponse{Success: true}, nil
}

func (h *GRPCHandler) MarkAllNotificationsRead(ctx context.Context, req *notifpb.MarkAllNotificationsReadRequest) (*notifpb.MarkAllNotificationsReadResponse, error) {
	count, err := h.notifRepo.MarkAllRead(req.UserId)
	if err != nil {
		return nil, fmt.Errorf("MarkAllNotificationsRead(user=%d): %v: %w", req.UserId, err, service.ErrNotificationUpdateFailed)
	}
	return &notifpb.MarkAllNotificationsReadResponse{Count: count}, nil
}

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

// ── Admin audit logs ─────────────────────────────────────────────────────────

// ListAdminAuditLogs returns paginated admin cron-action audit log entries
// (global view, admin-only).
func (h *GRPCHandler) ListAdminAuditLogs(ctx context.Context, req *notifpb.ListAdminAuditLogsRequest) (*notifpb.ListAdminAuditLogsResponse, error) {
	if h.adminAuditRepo == nil {
		return nil, status.Error(codes.Unimplemented, "admin audit log repository not wired")
	}

	page := int(req.GetPage())
	pageSize := int(req.GetPageSize())
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	filters := repository.AdminAuditLogFilters{
		Since:   req.GetSince(),
		Until:   req.GetUntil(),
		ActorID: req.GetActorId(),
		Action:  req.GetAction(),
	}

	entries, total, err := h.adminAuditRepo.ListAll(filters, page, pageSize)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	protoEntries := make([]*notifpb.AdminAuditLogEntry, len(entries))
	for i, e := range entries {
		protoEntries[i] = &notifpb.AdminAuditLogEntry{
			Id:         e.ID,
			Action:     e.Action,
			Service:    e.Service,
			CronName:   e.CronName,
			EmployeeId: e.EmployeeID,
			Reason:     e.Reason,
			Timestamp:  e.Timestamp.Unix(),
		}
	}
	return &notifpb.ListAdminAuditLogsResponse{
		Entries:  protoEntries,
		Total:    total,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}, nil
}
