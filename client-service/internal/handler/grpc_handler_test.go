package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/exbanka/contract/clientpb"
	"github.com/exbanka/client-service/internal/model"
)

// ---------------------------------------------------------------------------
// stub implementation of clientFacade
// ---------------------------------------------------------------------------

type stubClientSvc struct {
	createFn func(ctx context.Context, c *model.Client) error
	getFn    func(id uint64) (*model.Client, error)
	emailFn  func(email string) (*model.Client, error)
	listFn   func(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error)
	updateFn func(id uint64, updates map[string]interface{}, changedBy int64) (*model.Client, error)
}

func (s *stubClientSvc) CreateClient(ctx context.Context, c *model.Client) error {
	if s.createFn != nil {
		return s.createFn(ctx, c)
	}
	c.ID = 1
	return nil
}

func (s *stubClientSvc) GetClient(id uint64) (*model.Client, error) {
	if s.getFn != nil {
		return s.getFn(id)
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubClientSvc) GetByEmail(email string) (*model.Client, error) {
	if s.emailFn != nil {
		return s.emailFn(email)
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubClientSvc) ListClients(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	if s.listFn != nil {
		return s.listFn(emailFilter, nameFilter, page, pageSize)
	}
	return nil, 0, nil
}

func (s *stubClientSvc) UpdateClient(id uint64, updates map[string]interface{}, changedBy int64) (*model.Client, error) {
	if s.updateFn != nil {
		return s.updateFn(id, updates, changedBy)
	}
	return nil, gorm.ErrRecordNotFound
}

// Compile-time interface check.
var _ clientFacade = (*stubClientSvc)(nil)

// ---------------------------------------------------------------------------
// helper
// ---------------------------------------------------------------------------

func newTestClientHandler() (*ClientGRPCHandler, *stubClientSvc) {
	stub := &stubClientSvc{}
	h := newClientGRPCHandlerForTest(stub)
	return h, stub
}

func sampleClient(id uint64) *model.Client {
	return &model.Client{
		ID:          id,
		FirstName:   "Ana",
		LastName:    "Petrovic",
		Email:       "ana@example.com",
		JMBG:        "1234567890123",
		DateOfBirth: 631152000,
		Gender:      "female",
		Phone:       "+38160123",
		Address:     "Beograd",
		CreatedAt:   time.Now(),
	}
}

// ---------------------------------------------------------------------------
// mapServiceError
// ---------------------------------------------------------------------------

func TestMapServiceError_ClientHandler(t *testing.T) {
	cases := []struct {
		msg  string
		code codes.Code
	}{
		{"client not found", codes.NotFound},
		{"email must not be empty", codes.InvalidArgument},
		{"jmbg must be exactly 13 digits", codes.InvalidArgument},
		{"email must have a domain", codes.InvalidArgument},
		{"email must contain @", codes.InvalidArgument},
		{"value must have content", codes.InvalidArgument},
		{"email already exists", codes.AlreadyExists},
		{"duplicate key constraint", codes.AlreadyExists},
		{"daily limit exceeds employee authorization", codes.FailedPrecondition},
		{"insufficient funds for transfer", codes.FailedPrecondition},
		{"account locked after failed attempts", codes.ResourceExhausted},
		{"max attempts reached", codes.ResourceExhausted},
		{"permission denied for operation", codes.PermissionDenied},
		{"forbidden access to resource", codes.PermissionDenied},
		{"unexpected db error", codes.Internal},
	}
	for _, tc := range cases {
		got := mapServiceError(errors.New(tc.msg))
		assert.Equal(t, tc.code, got, "for error %q", tc.msg)
	}
}

// ---------------------------------------------------------------------------
// CreateClient
// ---------------------------------------------------------------------------

func TestCreateClient_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.createFn = func(_ context.Context, c *model.Client) error {
		c.ID = 42
		c.CreatedAt = time.Now()
		return nil
	}

	resp, err := h.CreateClient(context.Background(), &pb.CreateClientRequest{
		FirstName:   "Ana",
		LastName:    "Petrovic",
		Email:       "ana@example.com",
		Jmbg:        "1234567890123",
		DateOfBirth: 631152000,
		Gender:      "female",
		Phone:       "+38160123",
		Address:     "Beograd",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uint64(42), resp.Id)
	assert.Equal(t, "Ana", resp.FirstName)
	assert.Equal(t, "ana@example.com", resp.Email)
}

func TestCreateClient_ValidationError(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.createFn = func(_ context.Context, _ *model.Client) error {
		return errors.New("JMBG must be exactly 13 digits")
	}

	_, err := h.CreateClient(context.Background(), &pb.CreateClientRequest{
		FirstName: "Test",
		Email:     "test@example.com",
		Jmbg:      "123", // too short
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateClient_DuplicateEmail(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.createFn = func(_ context.Context, _ *model.Client) error {
		return errors.New("email already exists")
	}

	_, err := h.CreateClient(context.Background(), &pb.CreateClientRequest{
		FirstName: "Ana",
		Email:     "ana@example.com",
		Jmbg:      "1234567890123",
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetClient
// ---------------------------------------------------------------------------

func TestGetClient_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.getFn = func(id uint64) (*model.Client, error) {
		return sampleClient(id), nil
	}

	resp, err := h.GetClient(context.Background(), &pb.GetClientRequest{Id: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.Id)
	assert.Equal(t, "Ana", resp.FirstName)
}

func TestGetClient_NotFound(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.getFn = func(_ uint64) (*model.Client, error) {
		return nil, gorm.ErrRecordNotFound
	}

	_, err := h.GetClient(context.Background(), &pb.GetClientRequest{Id: 999})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetClient_ServiceError(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.getFn = func(_ uint64) (*model.Client, error) {
		return nil, errors.New("db connection failed")
	}

	_, err := h.GetClient(context.Background(), &pb.GetClientRequest{Id: 1})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// GetClientByEmail
// ---------------------------------------------------------------------------

func TestGetClientByEmail_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.emailFn = func(email string) (*model.Client, error) {
		c := sampleClient(5)
		c.Email = email
		return c, nil
	}

	resp, err := h.GetClientByEmail(context.Background(), &pb.GetClientByEmailRequest{Email: "ana@example.com"})
	require.NoError(t, err)
	assert.Equal(t, "ana@example.com", resp.Email)
}

func TestGetClientByEmail_NotFound(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.emailFn = func(_ string) (*model.Client, error) {
		return nil, gorm.ErrRecordNotFound
	}

	_, err := h.GetClientByEmail(context.Background(), &pb.GetClientByEmailRequest{Email: "nobody@example.com"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetClientByEmail_ServiceError(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.emailFn = func(_ string) (*model.Client, error) {
		return nil, errors.New("db down")
	}

	_, err := h.GetClientByEmail(context.Background(), &pb.GetClientByEmailRequest{Email: "bad@example.com"})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// ListClients
// ---------------------------------------------------------------------------

func TestListClients_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.listFn = func(_, _ string, _, _ int) ([]model.Client, int64, error) {
		return []model.Client{
			*sampleClient(1),
			*sampleClient(2),
		}, 2, nil
	}

	resp, err := h.ListClients(context.Background(), &pb.ListClientsRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)
	assert.Len(t, resp.Clients, 2)
}

func TestListClients_Empty(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.listFn = func(_, _ string, _, _ int) ([]model.Client, int64, error) {
		return nil, 0, nil
	}

	resp, err := h.ListClients(context.Background(), &pb.ListClientsRequest{Page: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Total)
	assert.Empty(t, resp.Clients)
}

func TestListClients_ServiceError(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.listFn = func(_, _ string, _, _ int) ([]model.Client, int64, error) {
		return nil, 0, errors.New("db down")
	}

	_, err := h.ListClients(context.Background(), &pb.ListClientsRequest{Page: 1, PageSize: 10})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// UpdateClient
// ---------------------------------------------------------------------------

func TestUpdateClient_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.updateFn = func(id uint64, updates map[string]interface{}, _ int64) (*model.Client, error) {
		c := sampleClient(id)
		if v, ok := updates["first_name"].(string); ok {
			c.FirstName = v
		}
		if v, ok := updates["email"].(string); ok {
			c.Email = v
		}
		return c, nil
	}

	firstName := "Milica"
	email := "milica@example.com"
	resp, err := h.UpdateClient(context.Background(), &pb.UpdateClientRequest{
		Id:        10,
		FirstName: &firstName,
		Email:     &email,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(10), resp.Id)
	assert.Equal(t, "Milica", resp.FirstName)
	assert.Equal(t, "milica@example.com", resp.Email)
}

func TestUpdateClient_NotFound(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.updateFn = func(_ uint64, _ map[string]interface{}, _ int64) (*model.Client, error) {
		return nil, gorm.ErrRecordNotFound
	}

	firstName := "X"
	_, err := h.UpdateClient(context.Background(), &pb.UpdateClientRequest{
		Id:        999,
		FirstName: &firstName,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateClient_ServiceError(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.updateFn = func(_ uint64, _ map[string]interface{}, _ int64) (*model.Client, error) {
		return nil, errors.New("db down")
	}

	firstName := "X"
	_, err := h.UpdateClient(context.Background(), &pb.UpdateClientRequest{
		Id:        1,
		FirstName: &firstName,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestUpdateClient_NoFields_Success(t *testing.T) {
	h, stub := newTestClientHandler()
	stub.updateFn = func(id uint64, _ map[string]interface{}, _ int64) (*model.Client, error) {
		return sampleClient(id), nil
	}

	// No optional fields set — should succeed (no-op update)
	resp, err := h.UpdateClient(context.Background(), &pb.UpdateClientRequest{Id: 5})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.Id)
}
