package service

import (
	"fmt"

	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/repository"
)

// ChangelogService is a thin read-only wrapper around ChangelogRepository,
// exposed via gRPC ListChangelog RPC for the API gateway.
type ChangelogService struct {
	repo *repository.ChangelogRepository
}

func NewChangelogService(repo *repository.ChangelogRepository) *ChangelogService {
	return &ChangelogService{repo: repo}
}

// ListChangelog returns paginated changelog rows for the given entity.
// page defaults to 1, pageSize defaults to 20 and is capped at 200.
func (s *ChangelogService) ListChangelog(entityType string, entityID int64, page, pageSize int) ([]model.Changelog, int64, error) {
	if entityType == "" {
		return nil, 0, fmt.Errorf("entity_type is required")
	}
	if entityID <= 0 {
		return nil, 0, fmt.Errorf("entity_id must be positive")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return s.repo.ListByEntity(entityType, entityID, page, pageSize)
}
