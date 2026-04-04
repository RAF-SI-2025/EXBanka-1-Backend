package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/client-service/internal/cache"
	kafkaprod "github.com/exbanka/client-service/internal/kafka"
	"github.com/exbanka/client-service/internal/model"
)

// ClientRepo is the interface for client storage operations.
type ClientRepo interface {
	Create(client *model.Client) error
	GetByID(id uint64) (*model.Client, error)
	GetByEmail(email string) (*model.Client, error)
	Update(client *model.Client) error
	List(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error)
}

// ValidateJMBG validates that the JMBG is exactly 13 numeric digits.
func ValidateJMBG(jmbg string) error {
	if len(jmbg) != 13 {
		return errors.New("JMBG must be exactly 13 digits")
	}
	for _, c := range jmbg {
		if c < '0' || c > '9' {
			return errors.New("JMBG must contain only digits")
		}
	}
	return nil
}

// ValidateEmail validates that the email has @ with content on both sides.
func ValidateEmail(email string) error {
	if email == "" {
		return errors.New("email must not be empty")
	}
	at := strings.Index(email, "@")
	if at < 1 {
		return errors.New("email must contain @ with content before it")
	}
	domain := email[at+1:]
	if domain == "" {
		return errors.New("email must have a domain after @")
	}
	return nil
}

// ClientService provides business logic for client management.
type ClientService struct {
	repo     ClientRepo
	producer *kafkaprod.Producer
	cache    *cache.RedisCache
}

// NewClientService constructs a ClientService.
func NewClientService(repo ClientRepo, producer *kafkaprod.Producer, cache *cache.RedisCache) *ClientService {
	return &ClientService{repo: repo, producer: producer, cache: cache}
}

// CreateClient validates and persists a new client.
func (s *ClientService) CreateClient(ctx context.Context, client *model.Client) error {
	if err := ValidateJMBG(client.JMBG); err != nil {
		return err
	}
	if err := ValidateEmail(client.Email); err != nil {
		return err
	}

	if err := s.repo.Create(client); err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	ClientCreatedTotal.Inc()

	if s.producer != nil {
		if err := s.producer.PublishClientCreated(ctx, kafkamsg.ClientCreatedMessage{
			ClientID:  client.ID,
			Email:     client.Email,
			FirstName: client.FirstName,
			LastName:  client.LastName,
		}); err != nil {
			log.Printf("warn: failed to publish client-created event: %v", err)
		}
	}
	return nil
}

// GetClient retrieves a client by ID, checking Redis cache first.
func (s *ClientService) GetClient(id uint64) (*model.Client, error) {
	cacheKey := "client:id:" + strconv.FormatUint(id, 10)
	if s.cache != nil {
		var cached model.Client
		if err := s.cache.Get(context.Background(), cacheKey, &cached); err == nil {
			return &cached, nil
		}
	}

	client, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Set(context.Background(), cacheKey, client, 5*time.Minute)
	}
	return client, nil
}

// GetByEmail retrieves a client by email directly from the repository.
func (s *ClientService) GetByEmail(email string) (*model.Client, error) {
	return s.repo.GetByEmail(email)
}

// UpdateClient applies the given field updates to the client, blocking JMBG and password_hash updates.
func (s *ClientService) UpdateClient(id uint64, updates map[string]interface{}) (*model.Client, error) {
	client, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Block immutable fields
	delete(updates, "jmbg")
	delete(updates, "password_hash")

	if v, ok := updates["first_name"].(string); ok {
		client.FirstName = v
	}
	if v, ok := updates["last_name"].(string); ok {
		client.LastName = v
	}
	if v, ok := updates["date_of_birth"].(int64); ok {
		client.DateOfBirth = v
	}
	if v, ok := updates["gender"].(string); ok {
		client.Gender = v
	}
	if v, ok := updates["email"].(string); ok {
		if err := ValidateEmail(v); err != nil {
			return nil, err
		}
		client.Email = v
	}
	if v, ok := updates["phone"].(string); ok {
		client.Phone = v
	}
	if v, ok := updates["address"].(string); ok {
		client.Address = v
	}
	if err := s.repo.Update(client); err != nil {
		return nil, err
	}

	if s.cache != nil {
		_ = s.cache.Delete(context.Background(), "client:id:"+strconv.FormatUint(id, 10))
	}

	if s.producer != nil {
		if err := s.producer.PublishClientUpdated(context.Background(), kafkamsg.ClientCreatedMessage{
			ClientID:  client.ID,
			Email:     client.Email,
			FirstName: client.FirstName,
			LastName:  client.LastName,
		}); err != nil {
			log.Printf("warn: failed to publish client-updated event: %v", err)
		}
	}
	return client, nil
}

// ListClients returns a paginated list of clients with optional filters.
func (s *ClientService) ListClients(emailFilter, nameFilter string, page, pageSize int) ([]model.Client, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	return s.repo.List(emailFilter, nameFilter, page, pageSize)
}

