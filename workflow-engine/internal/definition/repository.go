package definition

import (
	"context"
	"time"
)

// Summary is the response returned after a successful upload.
type Summary struct {
	ID         string    `json:"id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// ListResult is the page returned by List.
type ListResult struct {
	Items []Summary `json:"items"`
	Total int       `json:"total"`
}

// DefinitionRepository is the data-access contract for workflow definitions.
// The concrete SQL implementation lives in internal/storage/postgres.
// Callers depend on this interface, not on any concrete type.
type DefinitionRepository interface {
	Upload(ctx context.Context, def *WorkflowDefinition) (Summary, error)
	GetLatest(ctx context.Context, id string) (*WorkflowDefinition, error)
	GetVersion(ctx context.Context, id string, version int) (*WorkflowDefinition, error)
	List(ctx context.Context, keyword string, page, pageSize int) (ListResult, error)
	ListAllJobTypes(ctx context.Context) ([]string, error)
}
