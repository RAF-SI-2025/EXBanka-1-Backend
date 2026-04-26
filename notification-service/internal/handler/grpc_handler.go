package handler

import (
	"context"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/model"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
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

type GRPCHandler struct {
	notifpb.UnimplementedNotificationServiceServer
	emailSender emailSenderFacade
	inboxRepo   inboxRepoFacade
	notifRepo   notifRepoFacade
}

func NewGRPCHandler(emailSender *sender.EmailSender, inboxRepo *repository.MobileInboxRepository, notifRepo *repository.GeneralNotificationRepository) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo}
}

// newGRPCHandlerForTest constructs a GRPCHandler with interface-typed
// dependencies for use in unit tests.
func newGRPCHandlerForTest(emailSender emailSenderFacade, inboxRepo inboxRepoFacade, notifRepo notifRepoFacade) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo, notifRepo: notifRepo}
}

func (h *GRPCHandler) SendEmail(ctx context.Context, req *notifpb.SendEmailRequest) (*notifpb.SendEmailResponse, error) {
	if req.To == "" {
		return nil, status.Error(codes.InvalidArgument, "recipient email is required")
	}

	emailType := kafkamsg.EmailType(req.EmailType)
	subject, body := sender.BuildEmail(emailType, req.Data)

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
	return nil, status.Error(codes.Unimplemented, "delivery status tracking not yet implemented")
}

func (h *GRPCHandler) GetPendingMobileItems(ctx context.Context, req *notifpb.GetPendingMobileRequest) (*notifpb.PendingMobileResponse, error) {
	items, err := h.inboxRepo.GetPendingByUser(req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch pending items: %v", err)
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
		return nil, status.Errorf(codes.NotFound, "item not found or already delivered")
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
		return nil, status.Errorf(codes.Internal, "failed to list notifications: %v", err)
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
		return nil, status.Errorf(codes.Internal, "failed to count unread: %v", err)
	}
	return &notifpb.GetUnreadCountResponse{Count: count}, nil
}

func (h *GRPCHandler) MarkNotificationRead(ctx context.Context, req *notifpb.MarkNotificationReadRequest) (*notifpb.MarkNotificationReadResponse, error) {
	if err := h.notifRepo.MarkRead(req.Id, req.UserId); err != nil {
		return nil, status.Errorf(codes.NotFound, "notification not found")
	}
	return &notifpb.MarkNotificationReadResponse{Success: true}, nil
}

func (h *GRPCHandler) MarkAllNotificationsRead(ctx context.Context, req *notifpb.MarkAllNotificationsReadRequest) (*notifpb.MarkAllNotificationsReadResponse, error) {
	count, err := h.notifRepo.MarkAllRead(req.UserId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mark all read: %v", err)
	}
	return &notifpb.MarkAllNotificationsReadResponse{Count: count}, nil
}
