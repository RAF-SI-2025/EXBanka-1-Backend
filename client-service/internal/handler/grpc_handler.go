package handler

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/clientpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"), strings.Contains(msg, "must not"),
		strings.Contains(msg, "must have"), strings.Contains(msg, "must contain"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "exceeds"), strings.Contains(msg, "insufficient funds"),
		strings.Contains(msg, "limit exceeded"), strings.Contains(msg, "spending limit"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "locked"), strings.Contains(msg, "max attempts"),
		strings.Contains(msg, "failed attempts"):
		return codes.ResourceExhausted
	case strings.Contains(msg, "permission"), strings.Contains(msg, "forbidden"):
		return codes.PermissionDenied
	default:
		return codes.Internal
	}
}

type ClientGRPCHandler struct {
	pb.UnimplementedClientServiceServer
	clientService *service.ClientService
	producer      *kafkaprod.Producer
}

func NewClientGRPCHandler(clientService *service.ClientService, producer *kafkaprod.Producer) *ClientGRPCHandler {
	return &ClientGRPCHandler{
		clientService: clientService,
		producer:      producer,
	}
}

func (h *ClientGRPCHandler) CreateClient(ctx context.Context, req *pb.CreateClientRequest) (*pb.ClientResponse, error) {
	client := &model.Client{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		DateOfBirth: req.DateOfBirth,
		Gender:      req.Gender,
		Email:       req.Email,
		Phone:       req.Phone,
		Address:     req.Address,
		JMBG:        req.Jmbg,
	}

	if err := h.clientService.CreateClient(ctx, client); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create client: %v", err)
	}

	// Publish Kafka event
	_ = h.producer.PublishClientCreated(ctx, kafkamsg.ClientCreatedMessage{
		ClientID:  client.ID,
		Email:     client.Email,
		FirstName: client.FirstName,
		LastName:  client.LastName,
	})

	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) GetClient(ctx context.Context, req *pb.GetClientRequest) (*pb.ClientResponse, error) {
	client, err := h.clientService.GetClient(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "client not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get client: %v", err)
	}
	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) GetClientByEmail(ctx context.Context, req *pb.GetClientByEmailRequest) (*pb.ClientResponse, error) {
	client, err := h.clientService.GetByEmail(req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "client not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to get client: %v", err)
	}
	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) ListClients(ctx context.Context, req *pb.ListClientsRequest) (*pb.ListClientsResponse, error) {
	clients, total, err := h.clientService.ListClients(
		req.EmailFilter, req.NameFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list clients: %v", err)
	}

	resp := &pb.ListClientsResponse{Total: total}
	for _, c := range clients {
		c := c
		resp.Clients = append(resp.Clients, toClientResponse(&c))
	}
	return resp, nil
}

func (h *ClientGRPCHandler) UpdateClient(ctx context.Context, req *pb.UpdateClientRequest) (*pb.ClientResponse, error) {
	updates := make(map[string]interface{})
	if req.FirstName != nil {
		updates["first_name"] = *req.FirstName
	}
	if req.LastName != nil {
		updates["last_name"] = *req.LastName
	}
	if req.DateOfBirth != nil {
		updates["date_of_birth"] = *req.DateOfBirth
	}
	if req.Gender != nil {
		updates["gender"] = *req.Gender
	}
	if req.Email != nil {
		updates["email"] = *req.Email
	}
	if req.Phone != nil {
		updates["phone"] = *req.Phone
	}
	if req.Address != nil {
		updates["address"] = *req.Address
	}

	client, err := h.clientService.UpdateClient(req.Id, updates)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "client not found")
		}
		return nil, status.Errorf(mapServiceError(err), "failed to update client: %v", err)
	}

	// Publish Kafka event
	_ = h.producer.PublishClientUpdated(ctx, kafkamsg.ClientCreatedMessage{
		ClientID:  client.ID,
		Email:     client.Email,
		FirstName: client.FirstName,
		LastName:  client.LastName,
	})

	return toClientResponse(client), nil
}

func toClientResponse(c *model.Client) *pb.ClientResponse {
	return &pb.ClientResponse{
		Id:          c.ID,
		FirstName:   c.FirstName,
		LastName:    c.LastName,
		DateOfBirth: c.DateOfBirth,
		Gender:      c.Gender,
		Email:       c.Email,
		Phone:       c.Phone,
		Address:     c.Address,
		Jmbg:      c.JMBG,
		CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
