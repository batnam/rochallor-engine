package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// DefinitionStore implements definition.DefinitionRepository.
type DefinitionStore struct {
	pool *pgxpool.Pool
}

// NewDefinitionStore returns a DefinitionRepository backed by pool.
func NewDefinitionStore(pool *pgxpool.Pool) definition.DefinitionRepository {
	return &DefinitionStore{pool: pool}
}

func (s *DefinitionStore) Upload(ctx context.Context, def *definition.WorkflowDefinition) (definition.Summary, error) {
	parsedSteps := make([]definition.WorkflowStep, len(def.Steps))
	copy(parsedSteps, def.Steps)
	for i := range parsedSteps {
		if parsedSteps[i].JobType == "" && (parsedSteps[i].Type == definition.StepTypeServiceTask || parsedSteps[i].Type == definition.StepTypeUserTask) {
			parsedSteps[i].JobType = parsedSteps[i].ID
		}
	}

	rawJSON, err := json.Marshal(def)
	if err != nil {
		return definition.Summary{}, fmt.Errorf("upload: marshal raw json: %w", err)
	}
	parsedJSON, err := json.Marshal(parsedSteps)
	if err != nil {
		return definition.Summary{}, fmt.Errorf("upload: marshal parsed steps: %w", err)
	}

	var sum definition.Summary
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var existingVersion int
		var existingUploadedAt time.Time
		err := tx.QueryRow(ctx,
			`SELECT version, uploaded_at FROM workflow_definition
			   WHERE id = $1 AND raw_json = $2
			   ORDER BY version DESC LIMIT 1`,
			def.ID, rawJSON,
		).Scan(&existingVersion, &existingUploadedAt)
		if err == nil {
			sum = definition.Summary{ID: def.ID, Version: existingVersion, Name: def.Name, UploadedAt: existingUploadedAt}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("upload: check idempotent: %w", err)
		}

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
		sum = definition.Summary{ID: def.ID, Version: newVersion, Name: def.Name, UploadedAt: uploadedAt}
		return nil
	})
	return sum, err
}

func (s *DefinitionStore) GetLatest(ctx context.Context, id string) (*definition.WorkflowDefinition, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT raw_json, version FROM workflow_definition
		   WHERE id = $1 ORDER BY version DESC LIMIT 1`,
		id,
	)
	return scanDefinitionWithVersion(row)
}

func (s *DefinitionStore) GetVersion(ctx context.Context, id string, version int) (*definition.WorkflowDefinition, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT raw_json, version FROM workflow_definition WHERE id = $1 AND version = $2`,
		id, version,
	)
	return scanDefinitionWithVersion(row)
}

func (s *DefinitionStore) List(ctx context.Context, keyword string, page, pageSize int) (definition.ListResult, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := page * pageSize

	var rows pgx.Rows
	var err error
	var countRow pgx.Row

	if keyword != "" {
		like := "%" + keyword + "%"
		rows, err = s.pool.Query(ctx,
			`SELECT DISTINCT ON (id) id, version, name, uploaded_at
			   FROM workflow_definition
			   WHERE name ILIKE $1 OR id ILIKE $1
			   ORDER BY id, version DESC
			   LIMIT $2 OFFSET $3`,
			like, pageSize, offset,
		)
		countRow = s.pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT id) FROM workflow_definition WHERE name ILIKE $1 OR id ILIKE $1`,
			like,
		)
	} else {
		rows, err = s.pool.Query(ctx,
			`SELECT DISTINCT ON (id) id, version, name, uploaded_at
			   FROM workflow_definition
			   ORDER BY id, version DESC
			   LIMIT $1 OFFSET $2`,
			pageSize, offset,
		)
		countRow = s.pool.QueryRow(ctx,
			`SELECT COUNT(DISTINCT id) FROM workflow_definition`,
		)
	}
	if err != nil {
		return definition.ListResult{}, fmt.Errorf("list: query: %w", err)
	}
	defer rows.Close()

	var total int
	_ = countRow.Scan(&total)

	var items []definition.Summary
	for rows.Next() {
		var sm definition.Summary
		if err = rows.Scan(&sm.ID, &sm.Version, &sm.Name, &sm.UploadedAt); err != nil {
			return definition.ListResult{}, fmt.Errorf("list: scan: %w", err)
		}
		items = append(items, sm)
	}
	return definition.ListResult{Items: items, Total: total}, rows.Err()
}

func (s *DefinitionStore) ListAllJobTypes(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
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

func scanDefinitionWithVersion(row pgx.Row) (*definition.WorkflowDefinition, error) {
	var rawJSON []byte
	var version int
	if err := row.Scan(&rawJSON, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("definition not found")
		}
		return nil, fmt.Errorf("scan definition: %w", err)
	}
	var def definition.WorkflowDefinition
	if err := json.Unmarshal(rawJSON, &def); err != nil {
		return nil, fmt.Errorf("unmarshal definition: %w", err)
	}
	def.Version = version
	return &def, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Compile-time interface assertion.
var _ definition.DefinitionRepository = (*DefinitionStore)(nil)
