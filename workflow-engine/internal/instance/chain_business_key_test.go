package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// multiDefRepo is a definition.DefinitionRepository that resolves by id.
// Used to wire a parent + chained child definition for the chain
// businessKey propagation test.
type multiDefRepo struct {
	defs map[string]*definition.WorkflowDefinition
}

func (r multiDefRepo) Upload(ctx context.Context, def *definition.WorkflowDefinition) (definition.Summary, error) {
	return definition.Summary{}, nil
}
func (r multiDefRepo) GetLatest(ctx context.Context, id string) (*definition.WorkflowDefinition, error) {
	if d, ok := r.defs[id]; ok {
		return d, nil
	}
	return nil, errors.New("definition not found: " + id)
}
func (r multiDefRepo) GetVersion(ctx context.Context, id string, version int) (*definition.WorkflowDefinition, error) {
	return r.GetLatest(ctx, id)
}
func (r multiDefRepo) List(ctx context.Context, keyword string, page, pageSize int) (definition.ListResult, error) {
	return definition.ListResult{}, nil
}
func (r multiDefRepo) ListAllJobTypes(ctx context.Context) ([]string, error) {
	return nil, nil
}

// TestHandleEnd_PropagatesBusinessKeyToChainedWorkflow verifies that when a
// parent workflow with autoStartNextWorkflow=true reaches END, the chained
// child instance is started carrying the parent's business_key (instead of
// NULL, which was the prior behaviour).
func TestHandleEnd_PropagatesBusinessKeyToChainedWorkflow(t *testing.T) {
	parent := &definition.WorkflowDefinition{
		ID:                    "test::parent",
		Version:               1,
		AutoStartNextWorkflow: true,
		NextWorkflowId:        "test::child",
		Steps: []definition.WorkflowStep{
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	child := &definition.WorkflowDefinition{
		ID:      "test::child",
		Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}

	store := newMockStore()
	dbm := &mockDB{}
	disp := &noopDispatcher{}
	repo := multiDefRepo{defs: map[string]*definition.WorkflowDefinition{
		parent.ID: parent,
		child.ID:  child,
	}}
	svc := NewService(context.Background(), dbm, store, repo, disp)

	const wantBK = "APP-20240417-001"
	if _, err := svc.Start(context.Background(), parent.ID, parent.Version, nil, wantBK); err != nil {
		t.Fatalf("Start parent: %v", err)
	}

	// Chain is fired in a goroutine; poll the store until the child appears.
	deadline := time.Now().Add(2 * time.Second)
	var childInst *WorkflowInstance
	for time.Now().Before(deadline) {
		store.mu.Lock()
		for _, inst := range store.instances {
			if inst.DefinitionID == child.ID {
				childInst = inst
				break
			}
		}
		store.mu.Unlock()
		if childInst != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childInst == nil {
		t.Fatalf("chained child instance was never started")
	}
	if childInst.BusinessKey == nil {
		t.Fatalf("child business_key = NULL, want %q (regression: chain start dropped businessKey)", wantBK)
	}
	if got := *childInst.BusinessKey; got != wantBK {
		t.Errorf("child business_key = %q, want %q", got, wantBK)
	}
}

// TestStart_PropagatesBusinessKeyConflict verifies that when the store
// signals a uniqueness violation (mapped from Postgres 23505 on the
// uniq_workflow_instance_bk_def_active index), Service.Start surfaces the
// sentinel through its fmt.Errorf wrapping so the REST / gRPC layers can
// translate it to 409 / ALREADY_EXISTS via errors.Is.
func TestStart_PropagatesBusinessKeyConflict(t *testing.T) {
	def := &definition.WorkflowDefinition{
		ID: "test::dup", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
	svc, store, _, _ := newServiceForTest(def)
	store.forceInsertConflict = true

	_, err := svc.Start(context.Background(), def.ID, def.Version, nil, "APP-001")
	if err == nil {
		t.Fatalf("Start with forced conflict returned nil error")
	}
	if !errors.Is(err, ErrBusinessKeyConflict) {
		t.Errorf("Start error = %v, want errors.Is(...ErrBusinessKeyConflict)", err)
	}
}
