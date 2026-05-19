package instance

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// retryDef is the minimal one-SERVICE_TASK workflow used by the retry tests.
func retryDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID: "test::retry", Version: 1,
		Steps: []definition.WorkflowStep{
			{ID: "svc", Type: definition.StepTypeServiceTask, JobType: "svc", RetryCount: 2, NextStep: "end"},
			{ID: "end", Type: definition.StepTypeEnd},
		},
	}
}

// seedFailedInstance wires a FAILED instance with a FAILED step_execution
// for the named step into the mock store.
func seedFailedInstance(store *mockStore, def *definition.WorkflowDefinition, instanceID, stepID, reason string) {
	store.instances[instanceID] = &WorkflowInstance{
		ID:                instanceID,
		DefinitionID:      def.ID,
		DefinitionVersion: def.Version,
		Status:            InstanceStatusFailed,
		CurrentStepIDs:    []string{stepID},
		Variables:         json.RawMessage(`{}`),
		FailureReason:     &reason,
	}
	store.stepExecsByID[instanceID+"-se"] = instanceID + ":" + stepID
	store.stepExecsByStep[instanceID+":"+stepID] = "FAILED"
}

func TestRetryFailedStep_HappyPath_FlipsToActiveAndRedispatches(t *testing.T) {
	def := retryDef()
	svc, store, dbm, disp := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")

	inst, err := svc.RetryFailedStep(context.Background(), "i1", "svc", nil)
	if err != nil {
		t.Fatalf("RetryFailedStep: unexpected error: %v", err)
	}
	if inst.Status != InstanceStatusActive {
		t.Errorf("inst.Status = %q, want ACTIVE", inst.Status)
	}
	if inst.FailureReason != nil {
		t.Errorf("FailureReason = %v, want nil", *inst.FailureReason)
	}
	if got := store.instances["i1"].Status; got != InstanceStatusActive {
		t.Errorf("store status = %q, want ACTIVE", got)
	}
	// One new SERVICE_TASK job should have been enqueued + dispatched.
	if len(store.insertedJobs) != 1 {
		t.Errorf("expected 1 inserted job, got %d (%v)", len(store.insertedJobs), store.insertedJobs)
	}
	if len(disp.enqueued) != 1 {
		t.Errorf("expected 1 dispatch enqueue, got %d", len(disp.enqueued))
	}
	if got, want := dbm.txTypes, []string{"instance.retry_step"}; !equalStrings(got, want) {
		t.Errorf("txTypes = %v, want %v", got, want)
	}
}

func TestRetryFailedStep_InstanceNotFailed_Rejected(t *testing.T) {
	def := retryDef()
	svc, store, _, disp := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")
	store.instances["i1"].Status = InstanceStatusActive // override: not FAILED

	_, err := svc.RetryFailedStep(context.Background(), "i1", "svc", nil)
	if !errors.Is(err, ErrInstanceNotFailed) {
		t.Fatalf("want ErrInstanceNotFailed, got %v", err)
	}
	if len(disp.enqueued) != 0 {
		t.Errorf("no dispatch should occur when validation fails; got %d", len(disp.enqueued))
	}
}

func TestRetryFailedStep_LatestAttemptNotFailed_Rejected(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")
	store.stepExecsByStep["i1:svc"] = "COMPLETED" // latest attempt is COMPLETED

	_, err := svc.RetryFailedStep(context.Background(), "i1", "svc", nil)
	if !errors.Is(err, ErrStepNotRetryable) {
		t.Fatalf("want ErrStepNotRetryable, got %v", err)
	}
}

func TestRetryFailedStep_NoStepExecution_Rejected(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	// FAILED instance but no step_execution row for "svc".
	store.instances["i1"] = &WorkflowInstance{
		ID: "i1", DefinitionID: def.ID, DefinitionVersion: 1,
		Status:    InstanceStatusFailed,
		Variables: json.RawMessage(`{}`),
	}

	_, err := svc.RetryFailedStep(context.Background(), "i1", "svc", nil)
	if !errors.Is(err, ErrStepNotRetryable) {
		t.Fatalf("want ErrStepNotRetryable, got %v", err)
	}
}

func TestRetryFailedStep_StepNotInDefinition_Rejected(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")

	_, err := svc.RetryFailedStep(context.Background(), "i1", "ghost", nil)
	if err == nil || !contains(err.Error(), "not found in definition") {
		t.Fatalf("want 'not found in definition' error, got %v", err)
	}
}

func TestRetryFailedStep_InstanceNotFound_Rejected(t *testing.T) {
	def := retryDef()
	svc, _, _, _ := newServiceForTest(def)

	_, err := svc.RetryFailedStep(context.Background(), "missing", "svc", nil)
	if !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("want ErrInstanceNotFound, got %v", err)
	}
}

func TestRetryFailedStep_BusinessKeyConflict_PropagatesSentinel(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")
	store.reactivateConflict = true

	_, err := svc.RetryFailedStep(context.Background(), "i1", "svc", nil)
	if !errors.Is(err, ErrBusinessKeyConflict) {
		t.Fatalf("want ErrBusinessKeyConflict, got %v", err)
	}
}

func TestRetryFailedStep_VariablesPatch_MergedBeforeDispatch(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")
	// Seed an existing variable so we can verify the merge keeps it AND
	// applies the patch.
	store.instances["i1"].Variables = json.RawMessage(`{"keep":"me"}`)

	patch := map[string]any{"corrected": true, "amount": 42.0}
	inst, err := svc.RetryFailedStep(context.Background(), "i1", "svc", patch)
	if err != nil {
		t.Fatalf("RetryFailedStep: %v", err)
	}

	// Partial-update should have been issued with exactly the patch keys.
	if len(store.updatedVariablesPartial) != 1 {
		t.Fatalf("expected 1 partial update, got %d", len(store.updatedVariablesPartial))
	}
	got := store.updatedVariablesPartial[0]
	if got["corrected"] != true || got["amount"] != 42.0 {
		t.Errorf("partial update = %v, want corrected=true, amount=42", got)
	}

	// In-memory inst.Variables should reflect existing + patch.
	var vars map[string]any
	if err := json.Unmarshal(inst.Variables, &vars); err != nil {
		t.Fatalf("unmarshal merged vars: %v", err)
	}
	if vars["keep"] != "me" {
		t.Errorf("existing key dropped after merge; got %v", vars)
	}
	if vars["corrected"] != true || vars["amount"] != 42.0 {
		t.Errorf("patch not applied: %v", vars)
	}
}

func TestRetryFailedStep_EmptyVariables_NoPartialUpdate(t *testing.T) {
	def := retryDef()
	svc, store, _, _ := newServiceForTest(def)
	seedFailedInstance(store, def, "i1", "svc", "boom")

	if _, err := svc.RetryFailedStep(context.Background(), "i1", "svc", map[string]any{}); err != nil {
		t.Fatalf("RetryFailedStep: %v", err)
	}
	if len(store.updatedVariablesPartial) != 0 {
		t.Errorf("empty patch should not trigger UpdateInstanceVariablesPartial; got %d calls",
			len(store.updatedVariablesPartial))
	}
}

func TestRetryFailedStep_RequiresInstanceAndStepIDs(t *testing.T) {
	def := retryDef()
	svc, _, _, _ := newServiceForTest(def)

	if _, err := svc.RetryFailedStep(context.Background(), "", "svc", nil); err == nil {
		t.Errorf("want error for empty instance_id, got nil")
	}
	if _, err := svc.RetryFailedStep(context.Background(), "i1", "", nil); err == nil {
		t.Errorf("want error for empty step_id, got nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
