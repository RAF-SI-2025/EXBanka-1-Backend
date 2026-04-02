package handler

import (
	"context"
	"log"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/repository"
	"github.com/exbanka/notification-service/internal/sender"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCHandler struct {
	notifpb.UnimplementedNotificationServiceServer
	emailSender *sender.EmailSender
	inboxRepo   *repository.MobileInboxRepository
}

func NewGRPCHandler(emailSender *sender.EmailSender, inboxRepo *repository.MobileInboxRepository) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender, inboxRepo: inboxRepo}
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
	items, err := h.inboxRepo.GetPendingByUserAndDevice(req.UserId, req.DeviceId)
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
	if err := h.inboxRepo.MarkDelivered(req.Id, req.DeviceId); err != nil {
		return nil, status.Errorf(codes.NotFound, "item not found or already delivered")
	}
	return &notifpb.AckMobileResponse{Success: true}, nil
}
