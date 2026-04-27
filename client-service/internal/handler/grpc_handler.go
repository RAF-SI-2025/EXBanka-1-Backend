package handler

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/exbanka/client-service/internal/model"
	"github.com/exbanka/client-service/internal/service"
	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/clientpb"
)

// clientFacade is the subset of *service.ClientService used by the gRPC handler.
// Extracted as an interface to allow stub-based unit tests.
type clientFacade interface {
	CreateClient(ctx context.Context, c *model.Client) error
	GetClient(id uint64) (*model.Client, error)
	GetByEmail(email string) (*model.Client, error)
	ListClients(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error)
	UpdateClient(id uint64, updates map[string]interface{}, changedBy int64) (*model.Client, error)
}

type ClientGRPCHandler struct {
	pb.UnimplementedClientServiceServer
	clientService clientFacade
}

func NewClientGRPCHandler(clientService *service.ClientService) *ClientGRPCHandler {
	return &ClientGRPCHandler{
		clientService: clientService,
	}
}

// newClientGRPCHandlerForTest constructs a handler with a stub facade, for use in unit tests only.
func newClientGRPCHandlerForTest(svc clientFacade) *ClientGRPCHandler {
	return &ClientGRPCHandler{clientService: svc}
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
		return nil, err
	}

	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) GetClient(ctx context.Context, req *pb.GetClientRequest) (*pb.ClientResponse, error) {
	client, err := h.clientService.GetClient(req.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrClientNotFound
		}
		return nil, err
	}
	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) GetClientByEmail(ctx context.Context, req *pb.GetClientByEmailRequest) (*pb.ClientResponse, error) {
	client, err := h.clientService.GetByEmail(req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrClientNotFound
		}
		return nil, err
	}
	return toClientResponse(client), nil
}

func (h *ClientGRPCHandler) ListClients(ctx context.Context, req *pb.ListClientsRequest) (*pb.ListClientsResponse, error) {
	clients, total, err := h.clientService.ListClients(
		req.EmailFilter, req.NameFilter,
		int(req.Page), int(req.PageSize),
	)
	if err != nil {
		return nil, err
	}

	resp := &pb.ListClientsResponse{Total: total, Clients: make([]*pb.ClientResponse, 0, len(clients))}
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

	changedBy := changelog.ExtractChangedBy(ctx)
	client, err := h.clientService.UpdateClient(req.Id, updates, changedBy)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, service.ErrClientNotFound
		}
		return nil, err
	}

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
		Jmbg:        c.JMBG,
		CreatedAt:   c.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
