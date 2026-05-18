package instance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/dispatch"
)

// fakeTx is a no-op db.Tx used by mockDB. Only its identity matters; the
// fakeStore methods do not inspect it.
type fakeTx struct{}

func (fakeTx) TxMarker() {}

// mockDB is an in-memory db.DB. RunInTx invokes fn synchronously with a
// fakeTx and surfaces any error fn returns. TryAcquireAdvisoryLock always
// succeeds with a no-op release function.
type mockDB struct {
	txTypes []string
}

func (m *mockDB) RunInTx(ctx context.Context, txType string, fn func(db.Tx) error) error {
	m.txTypes = append(m.txTypes, txType)
	return fn(fakeTx{})
}

func (m *mockDB) TryAcquireAdvisoryLock(ctx context.Context, key int64) (bool, func(), error) {
	return true, func() {}, nil
}

// mockStore is an in-memory instance.Store sufficient for the handler unit
// tests. It records the calls each method receives so tests can assert on
// behaviour without inspecting SQL.
type mockStore struct {
	mu sync.Mutex

	// instances by id
	instances map[string]*WorkflowInstance

	// step_execution rows
	stepExecsByID   map[string]string // stepExecID → "instanceID:stepID"
	stepExecsByStep map[string]string // "instanceID:stepID" → status

	// Counters that tests assert on.
	insertedJobs              []string
	insertedUserTasks         []string
	insertedBoundarySchedules []string
	completedStepExecsByID    []string
	completedStepExecsByStep  []string
	failedStepExecsByStep     []string
	updatedVariablesPartial   []map[string]any
	updatedInstanceStatus     []InstanceStatus
	failedInstances           []string
	completedInstances        []string

	// Configurable return values.
	branchLeafsCompleted int

	// When set, the next InsertInstance call returns ErrBusinessKeyConflict
	// (and the flag is cleared). Used by the business-key conflict unit test
	// to verify Start propagates the sentinel through fmt.Errorf wrapping.
	forceInsertConflict bool
}

func newMockStore() *mockStore {
	return &mockStore{
		instances:       map[string]*WorkflowInstance{},
		stepExecsByID:   map[string]string{},
		stepExecsByStep: map[string]string{},
	}
}

// ─── reads ────────────────────────────────────────────────────────────────────

func (m *mockStore) GetInstanceDefinitionInfo(ctx context.Context, instanceID string) (string, int, error) {
	if inst, ok := m.instances[instanceID]; ok {
		return inst.DefinitionID, inst.DefinitionVersion, nil
	}
	return "", 0, ErrInstanceNotFound
}

func (m *mockStore) GetInstance(ctx context.Context, instanceID string) (*WorkflowInstance, error) {
	if inst, ok := m.instances[instanceID]; ok {
		cp := *inst
		return &cp, nil
	}
	return nil, ErrInstanceNotFound
}

func (m *mockStore) ListInstances(ctx context.Context, definitionID, status, businessKey string, page, pageSize int) (ListResult, error) {
	return ListResult{}, nil
}

func (m *mockStore) GetHistory(ctx context.Context, instanceID string) ([]StepExecution, error) {
	return nil, nil
}

func (m *mockStore) CancelInstance(ctx context.Context, instanceID, reason string) (*WorkflowInstance, error) {
	if inst, ok := m.instances[instanceID]; ok {
		inst.Status = InstanceStatusCancelled
		cp := *inst
		return &cp, nil
	}
	return nil, ErrInstanceNotFound
}

func (m *mockStore) GetJobInstanceAndStepExec(ctx context.Context, jobID string) (string, string, error) {
	return "", "", errors.New("mockStore.GetJobInstanceAndStepExec not implemented")
}

func (m *mockStore) GetStepExecutionStepIDByID(ctx context.Context, stepExecID string) (string, error) {
	if v, ok := m.stepExecsByID[stepExecID]; ok {
		if idx := strings.Index(v, ":"); idx >= 0 {
			return v[idx+1:], nil
		}
	}
	return "", errors.New("step_execution not found")
}

// ─── writes ───────────────────────────────────────────────────────────────────

func (m *mockStore) InsertInstance(ctx context.Context, _ db.Tx,
	id, defID string, defVersion int, status InstanceStatus,
	currentStepIDs []string, variables []byte, businessKey *string,
) (*WorkflowInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forceInsertConflict {
		m.forceInsertConflict = false
		return nil, ErrBusinessKeyConflict
	}
	inst := &WorkflowInstance{
		ID:                id,
		DefinitionID:      defID,
		DefinitionVersion: defVersion,
		Status:            status,
		CurrentStepIDs:    append([]string(nil), currentStepIDs...),
		Variables:         json.RawMessage(append([]byte(nil), variables...)),
		StartedAt:         time.Now(),
		BusinessKey:       businessKey,
	}
	m.instances[id] = inst
	cp := *inst
	return &cp, nil
}

func (m *mockStore) GetInstanceForUpdate(ctx context.Context, _ db.Tx, instanceID string) (*WorkflowInstance, error) {
	if inst, ok := m.instances[instanceID]; ok {
		cp := *inst
		return &cp, nil
	}
	return nil, ErrInstanceNotFound
}

func (m *mockStore) UpdateInstanceCurrentSteps(ctx context.Context, _ db.Tx, instanceID string, stepIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[instanceID]; ok {
		inst.CurrentStepIDs = append([]string(nil), stepIDs...)
	}
	return nil
}

func (m *mockStore) UpdateInstanceStatus(ctx context.Context, _ db.Tx, instanceID string, status InstanceStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedInstanceStatus = append(m.updatedInstanceStatus, status)
	if inst, ok := m.instances[instanceID]; ok {
		inst.Status = status
	}
	return nil
}

func (m *mockStore) UpdateInstanceStatusAndSteps(ctx context.Context, _ db.Tx, instanceID string, status InstanceStatus, stepIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[instanceID]; ok {
		inst.Status = status
		inst.CurrentStepIDs = append([]string(nil), stepIDs...)
	}
	return nil
}

func (m *mockStore) CompleteInstance(ctx context.Context, _ db.Tx, instanceID string, stepIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completedInstances = append(m.completedInstances, instanceID)
	if inst, ok := m.instances[instanceID]; ok {
		inst.Status = InstanceStatusCompleted
		inst.CurrentStepIDs = append([]string(nil), stepIDs...)
	}
	return nil
}

func (m *mockStore) FailInstance(ctx context.Context, _ db.Tx, instanceID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedInstances = append(m.failedInstances, instanceID)
	if inst, ok := m.instances[instanceID]; ok {
		inst.Status = InstanceStatusFailed
		r := reason
		inst.FailureReason = &r
	}
	return nil
}

func (m *mockStore) UpdateInstanceVariablesPartial(ctx context.Context, _ db.Tx, instanceID string, patch map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]any, len(patch))
	for k, v := range patch {
		cp[k] = v
	}
	m.updatedVariablesPartial = append(m.updatedVariablesPartial, cp)
	return nil
}

func (m *mockStore) CountStepAttempts(ctx context.Context, _ db.Tx, instanceID, stepID string) (int, error) {
	return 0, nil
}

func (m *mockStore) InsertStepExecution(ctx context.Context, _ db.Tx,
	id, instanceID, stepID, stepType string, attempt int, inputSnapshot []byte,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepExecsByID[id] = instanceID + ":" + stepID
	m.stepExecsByStep[instanceID+":"+stepID] = "RUNNING"
	return nil
}

func (m *mockStore) CompleteStepExecutionByID(ctx context.Context, _ db.Tx, stepExecID string, outputSnapshot []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completedStepExecsByID = append(m.completedStepExecsByID, stepExecID)
	return nil
}

func (m *mockStore) CompleteStepExecutionByStep(ctx context.Context, _ db.Tx, instanceID, stepID string, outputSnapshot []byte) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := instanceID + ":" + stepID
	if m.stepExecsByStep[key] == "RUNNING" {
		m.stepExecsByStep[key] = "COMPLETED"
		m.completedStepExecsByStep = append(m.completedStepExecsByStep, key)
		return 1, nil
	}
	return 0, nil
}

func (m *mockStore) CompleteStepExecutionByStepNoOutput(ctx context.Context, _ db.Tx, instanceID, stepID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := instanceID + ":" + stepID
	m.stepExecsByStep[key] = "COMPLETED"
	m.completedStepExecsByStep = append(m.completedStepExecsByStep, key)
	return nil
}

func (m *mockStore) FailStepExecutionByID(ctx context.Context, _ db.Tx, stepExecID, reason string) error {
	return nil
}

func (m *mockStore) FailStepExecutionByStep(ctx context.Context, _ db.Tx, instanceID, stepID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedStepExecsByStep = append(m.failedStepExecsByStep, instanceID+":"+stepID)
	return nil
}

func (m *mockStore) GetStepExecutionStepID(ctx context.Context, _ db.Tx, stepExecID string) (string, error) {
	if v, ok := m.stepExecsByID[stepExecID]; ok {
		if idx := strings.Index(v, ":"); idx >= 0 {
			return v[idx+1:], nil
		}
	}
	return "", errors.New("step_execution not found")
}

func (m *mockStore) InsertJob(ctx context.Context, _ db.Tx,
	id, instanceID, stepExecID, jobType string, retriesRemaining int, payload []byte,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertedJobs = append(m.insertedJobs, id+"/"+jobType)
	return nil
}

func (m *mockStore) GetJobStatusForUpdate(ctx context.Context, _ db.Tx, jobID string) (string, error) {
	return "UNLOCKED", nil
}

func (m *mockStore) MarkJobCompleted(ctx context.Context, _ db.Tx, jobID, workerID string) error {
	return nil
}

func (m *mockStore) CancelJobByStepExecution(ctx context.Context, _ db.Tx, stepExecID string) error {
	return nil
}

func (m *mockStore) InsertUserTask(ctx context.Context, _ db.Tx,
	id, instanceID, stepExecID, stepID string, payload []byte,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertedUserTasks = append(m.insertedUserTasks, id)
	return nil
}

func (m *mockStore) CompleteUserTask(ctx context.Context, _ db.Tx, instanceID, stepID string, result []byte) (int64, error) {
	return 1, nil
}

func (m *mockStore) CancelUserTaskByStepExecution(ctx context.Context, _ db.Tx, stepExecID string) error {
	return nil
}

func (m *mockStore) InsertBoundaryEventSchedule(ctx context.Context, _ db.Tx,
	id, instanceID, stepExecID, targetStepID string,
	fireAt time.Time, interrupting bool,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertedBoundarySchedules = append(m.insertedBoundarySchedules, id)
	return nil
}

func (m *mockStore) CountCompletedBranchLeafs(ctx context.Context, _ db.Tx, instanceID string, leafStepIDs []string) (int, error) {
	return m.branchLeafsCompleted, nil
}

// fakeDefRepo is a tiny in-memory definition.DefinitionRepository for tests.
type fakeDefRepo struct {
	def *definition.WorkflowDefinition
}

func (f fakeDefRepo) Upload(ctx context.Context, def *definition.WorkflowDefinition) (definition.Summary, error) {
	return definition.Summary{}, nil
}
func (f fakeDefRepo) GetLatest(ctx context.Context, id string) (*definition.WorkflowDefinition, error) {
	return f.def, nil
}
func (f fakeDefRepo) GetVersion(ctx context.Context, id string, version int) (*definition.WorkflowDefinition, error) {
	return f.def, nil
}
func (f fakeDefRepo) List(ctx context.Context, keyword string, page, pageSize int) (definition.ListResult, error) {
	return definition.ListResult{}, nil
}
func (f fakeDefRepo) ListAllJobTypes(ctx context.Context) ([]string, error) {
	return nil, nil
}

// noopDispatcher is a dispatch.Dispatcher that records every Enqueue call.
type noopDispatcher struct {
	enqueued []string
}

func (n *noopDispatcher) Enqueue(ctx context.Context, _ db.Tx, j dispatch.DispatchJob) error {
	n.enqueued = append(n.enqueued, j.ID)
	return nil
}
