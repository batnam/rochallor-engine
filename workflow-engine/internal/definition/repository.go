package definition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Summary is the response returned after a successful upload.
type Summary struct {
	ID         string    `json:"id"`
	Version    int       `json:"version"`
	Name       string    `json:"name"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// Repository performs all PostgreSQL reads and writes for workflow definitions.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a Repository backed by pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Upload persists def as a new version. It assigns version = max(existing)+1 for
// the given natural key. If the exact JSON content was already uploaded (idempotent
// re-upload), the existing summary is returned with no error.
//
// The Engine derives jobType from step.id per  before persisting when the
// step's jobType is empty. This derivation is written into parsed_steps but NOT
// mutated in-memory on def.
func (r *Repository) Upload(ctx context.Context, def *WorkflowDefinition) (Summary, error) {
	// Derive jobType where absent and build the parsed_steps representation
	parsedSteps := make([]WorkflowStep, len(def.Steps))
	copy(parsedSteps, def.Steps)
	for i := range parsedSteps {
		if parsedSteps[i].JobType == "" && (parsedSteps[i].Type == StepTypeServiceTask || parsedSteps[i].Type == StepTypeUserTask) {
			parsedSteps[i].JobType = parsedSteps[i].ID //  derivation
		}
	}

	rawJSON, err := json.Marshal(def)
	if err != nil {
		return Summary{}, fmt.Errorf("upload: marshal raw json: %w", err)
	}
	parsedJSON, err := json.Marshal(parsedSteps)
	if err != nil {
		return Summary{}, fmt.Errorf("upload: marshal parsed steps: %w", err)
	}

	var sum Summary
	err = pgx.BeginTxFunc(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Check for idempotent re-upload (same id + exact same raw_json content)
		var existingVersion int
		var existingUploadedAt time.Time
		err := tx.QueryRow(ctx,
			`SELECT version, uploaded_at FROM workflow_definition
			  WHERE id = $1 AND raw_json = $2
			  ORDER BY version DESC LIMIT 1`,
			def.ID, rawJSON,
		).Scan(&existingVersion, &existingUploadedAt)
		if err == nil {
			sum = Summary{ID: def.ID, Version: existingVersion, Name: def.Name, UploadedAt: existingUploadedAt}
			return nil // idempotent
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("upload: check idempotent: %w", err)
		}

		// Assign next version
		var maxVersion int
		_ = tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version), 0) FROM workflow_definition WHERE id = $1`,
			def.ID,
		).Scan(&maxVersion)
		newVersion := maxVersion + 1

		autoStartID := ""
		if def.AutoStartNextWorkflow {
			autoStartID = def.NextWorkflowId
		}

		var uploadedAt time.Time
		err = tx.QueryRow(ctx,
			`INSERT INTO workflow_definition
			  (id, version, name, description, raw_json, parsed_steps, auto_start_next_workflow_id, uploaded_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			 RETURNING uploaded_at`,
			def.ID, newVersion, def.Name, def.Description,
			rawJSON, parsedJSON,
			nullableString(autoStartID),
		).Scan(&uploadedAt)
		if err != nil {
			return fmt.Errorf("upload: insert: %w", err)
		}
		sum = Summary{ID: def.ID, Version: newVersion, Name: def.Name, UploadedAt: uploadedAt}
		return nil
	})
	return sum, err
}

// GetLatest returns the highest-versioned definition for id.
func (r *Repository) GetLatest(ctx context.Context, id string) (*WorkflowDefinition, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT raw_json, version FROM workflow_definition
		  WHERE id = $1 ORDER BY version DESC LIMIT 1`,
		id,
	)
	return scanDefinitionWithVersion(row)
}

// GetVersion returns a specific version of a definition.
func (r *Repository) GetVersion(ctx context.Context, id string, version int) (*WorkflowDefinition, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT raw_json, version FROM workflow_definition WHERE id = $1 AND version = $2`,
		id, version,
	)
	return scanDefinitionWithVersion(row)
}

// ListResult is the page returned by List.
type ListResult struct {
	Items []Summary `json:"items"`
	Total int       `json:"total"`
}

// List returns a page of latest-version summaries, optionally filtered by keyword.
func (r *Repository) List(ctx context.Context, keyword string, page, pageSize int) (ListResult, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := page * pageSize

	var rows pgx.Rows
	var err error
	var countRow pgx.Row

	if keyword != "" {
		like := "%" + keyword + "%"
		rows, err = r.pool.Query(ctx,
			`SELECT DISTINCT ON (id) id, version, name, uploaded_at
			  FROM workflow_definition
			  WHERE name ILIKE $1 OR id ILIKE $1
			  ORDER BY id, version DESC
			  LIMIT $2 OFFSET $3`,
			like, pageSize, offset,
		)
		countRow = r.pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT id) FROM workflow_definition WHERE name ILIKE $1 OR id ILIKE $1`,
			like,
		)
	} else {
		rows, err = r.pool.Query(ctx,
			`SELECT DISTINCT ON (id) id, version, name, uploaded_at
			  FROM workflow_definition
			  ORDER BY id, version DESC
			  LIMIT $1 OFFSET $2`,
			pageSize, offset,
		)
		countRow = r.pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT id) FROM workflow_definition`,
		)
	}
	if err != nil {
		return ListResult{}, fmt.Errorf("list: query: %w", err)
	}
	defer rows.Close()

	var total int
	_ = countRow.Scan(&total)

	var items []Summary
	for rows.Next() {
		var s Summary
		if err = rows.Scan(&s.ID, &s.Version, &s.Name, &s.UploadedAt); err != nil {
			return ListResult{}, fmt.Errorf("list: scan: %w", err)
		}
		items = append(items, s)
	}
	return ListResult{Items: items, Total: total}, rows.Err()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanDefinitionWithVersion(row pgx.Row) (*WorkflowDefinition, error) {
	var rawJSON []byte
	var version int
	if err := row.Scan(&rawJSON, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("definition not found")
		}
		return nil, fmt.Errorf("scan definition: %w", err)
	}
	var def WorkflowDefinition
	if err := json.Unmarshal(rawJSON, &def); err != nil {
		return nil, fmt.Errorf("unmarshal definition: %w", err)
	}
	// Override version with the DB-assigned value, which is correct even if the
	// raw_json was stored before version assignment (omitempty means Version=0 in JSON).
	def.Version = version
	return &def, nil
}

// ListAllJobTypes returns every unique job_type found across all workflow
// definitions in the system. Used for Kafka topic validation at startup (R-008).
func (r *Repository) ListAllJobTypes(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
                SELECT DISTINCT COALESCE(s->>'jobType', s->>'id')
                FROM   workflow_definition, jsonb_array_elements(parsed_steps) s
                WHERE  s->>'type' = 'SERVICE_TASK'`)
	if err != nil {
		return nil, fmt.Errorf("list all job types: %w", err)
	}
	defer rows.Close()

	var types []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		if t != "" {
			types = append(types, t)
		}
	}
	return types, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
