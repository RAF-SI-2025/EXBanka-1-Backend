package handler

import (
	"context"
	"log"

	kafkamsg "github.com/exbanka/contract/kafka"
	notifpb "github.com/exbanka/contract/notificationpb"
	"github.com/exbanka/notification-service/internal/sender"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCHandler struct {
	notifpb.UnimplementedNotificationServiceServer
	emailSender *sender.EmailSender
}

func NewGRPCHandler(emailSender *sender.EmailSender) *GRPCHandler {
	return &GRPCHandler{emailSender: emailSender}
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
