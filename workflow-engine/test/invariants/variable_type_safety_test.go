//go:build integration

package invariants_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/expression"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/instance"
)

// init wires the expression evaluator into the instance package the same way
// cmd/engine/main.go does. The shared TestMain in invariants_test.go does not
// do this today, so any integration test that reaches handleDecision/
// handleTransformation needs to set it up itself.
func init() {
	instance.SetExpressionEvaluator(expression.Evaluate)
}

// buildCoercionDef returns a workflow that gates on ${amount > 100}. The
// branch is intentionally typed (number on the RHS) so the test can vary
// the LHS type/value to exercise coercion paths.
func buildCoercionDef(id string) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   id,
		Name: id,
		Steps: []definition.WorkflowStep{
			{
				ID:   "decide",
				Name: "Decide",
				Type: definition.StepTypeDecision,
				ConditionalNextSteps: definition.NewConditionalBranches(
					"${amount > 100}", "end",
				),
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
}

// TestCoercion_StringAmountTakesNumericBranch is the happy path: with
// coercion enabled (the engine default), a string-typed `amount` of "250.5"
// satisfies the numeric comparison `${amount > 100}` and the instance
// completes via the premium branch.
func TestCoercion_StringAmountTakesNumericBranch(t *testing.T) {
	ctx := context.Background()
	def := buildCoercionDef("us1-coerce-happy")

	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": "250.5"}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusCompleted {
		reason := ""
		if terminal.FailureReason != nil {
			reason = *terminal.FailureReason
		}
		t.Fatalf("expected COMPLETED via coerced numeric branch, got %s (reason: %q)", terminal.Status, reason)
	}
}

// TestCoercion_NonNumericStringFailsWithCoercionMessage is the failure
// path: with coercion enabled, `amount` = "abc" cannot be coerced to a
// number, so the DECISION step's expression eval errors and the instance
// fails with the offending value visible in the failure reason.
func TestCoercion_NonNumericStringFailsWithCoercionMessage(t *testing.T) {
	ctx := context.Background()
	def := buildCoercionDef("us1-coerce-bad")

	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": "abc"}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusFailed {
		t.Fatalf("expected FAILED due to non-numeric coercion, got %s", terminal.Status)
	}
	reason := ""
	if terminal.FailureReason != nil {
		reason = *terminal.FailureReason
	}
	if !strings.Contains(reason, "abc") {
		t.Errorf("failure reason should surface the offending value %q; got: %s", "abc", reason)
	}
	if !strings.Contains(strings.ToLower(reason), "coerce") {
		t.Errorf("failure reason should identify this as a coercion failure; got: %s", reason)
	}
}

// TestCoercion_DisabledDoesNotPromoteStringToNumber proves the opt-out:
// with coercion disabled at the engine level, the same string `amount` =
// "250.5" no longer satisfies the numeric comparison. Confirms the patcher is
// genuinely gated by the engine config.
func TestCoercion_DisabledDoesNotPromoteStringToNumber(t *testing.T) {
	ctx := context.Background()
	def := buildCoercionDef("us1-coerce-off")

	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	expression.SetCoercionEnabled(false)
	defer expression.SetCoercionEnabled(true)

	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": "250.5"}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status == instance.InstanceStatusCompleted {
		t.Fatal("expected coercion-disabled run to NOT complete via the numeric branch; the engine promoted a string to a number despite the disable flag")
	}
	reason := ""
	if terminal.FailureReason != nil {
		reason = *terminal.FailureReason
	}
	if strings.Contains(strings.ToLower(reason), "cannot coerce") {
		t.Errorf("disabled-coercion failure should NOT come from the coerce builtins; got: %s", reason)
	}
}

// TestCoercion_NumericAmountStillWorks is a regression guard: when the caller
// already supplies a numeric `amount`, coercion is a no-op and the workflow
// behaves identically to today.
func TestCoercion_NumericAmountStillWorks(t *testing.T) {
	ctx := context.Background()
	def := buildCoercionDef("us1-coerce-regression")

	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}

	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": 250.5}, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusCompleted {
		t.Fatalf("expected COMPLETED for numeric amount, got %s", terminal.Status)
	}
}

// ─── Input schema ────────────────────────────────────────────────────────────

func buildInputSchemaDef(id string) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   id,
		Name: id,
		InputSchema: &definition.Schema{
			Properties: map[string]definition.PropertyDescriptor{
				"amount":      {Type: definition.SchemaTypeNumber},
				"customer_id": {Type: definition.SchemaTypeString},
			},
			Required: []string{"amount", "customer_id"},
		},
		Steps: []definition.WorkflowStep{{ID: "end", Name: "End", Type: definition.StepTypeEnd}},
	}
}

func TestInputSchema_ValidPayloadStartsInstance(t *testing.T) {
	ctx := context.Background()
	def := buildInputSchemaDef("us2-input-ok")
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": 99.0, "customer_id": "c1"}, "")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if _, getErr := gInstSvc.Get(ctx, inst.ID); getErr != nil {
		t.Errorf("instance not persisted: %v", getErr)
	}
}

func TestInputSchema_TypeMismatchRejectsBeforePersist(t *testing.T) {
	ctx := context.Background()
	def := buildInputSchemaDef("us2-input-bad-type")
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	_, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"amount": "abc", "customer_id": "c1"}, "")
	if err == nil {
		t.Fatal("expected schema violation error")
	}
	sv, ok := definition.IsSchemaViolation(err)
	if !ok {
		t.Fatalf("expected *SchemaViolationError, got %T: %v", err, err)
	}
	if !strings.Contains(sv.Error(), "expected number") {
		t.Errorf("missing type detail: %s", sv.Error())
	}

	// Verify no instance row was written.
	var n int
	if err := gPool.QueryRow(ctx, `SELECT COUNT(*) FROM workflow_instance WHERE definition_id = $1`, def.ID).Scan(&n); err != nil {
		t.Fatalf("count instances: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 instances, found %d", n)
	}
}

func TestInputSchema_RequiredMissingListsAllViolations(t *testing.T) {
	ctx := context.Background()
	def := buildInputSchemaDef("us2-input-missing")
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	_, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{}, "")
	sv, ok := definition.IsSchemaViolation(err)
	if !ok {
		t.Fatalf("expected *SchemaViolationError, got %T: %v", err, err)
	}
	if len(sv.Violations) != 2 {
		t.Errorf("expected both required violations enumerated, got %d: %v", len(sv.Violations), sv.Violations)
	}
}

func TestInputSchema_NoSchemaIsRegressionFree(t *testing.T) {
	ctx := context.Background()
	def := &definition.WorkflowDefinition{
		ID:    "us2-no-input-schema",
		Name:  "no schema",
		Steps: []definition.WorkflowStep{{ID: "end", Name: "End", Type: definition.StepTypeEnd}},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := gInstSvc.Start(ctx, def.ID, 0, map[string]any{"anything": "goes"}, ""); err != nil {
		t.Errorf("expected success without schema, got: %v", err)
	}
}

// ─── Output schema on SERVICE_TASK ───────────────────────────────────────────

func buildOutputSchemaDef(id, jobType string, retryCount int) *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   id,
		Name: id,
		Steps: []definition.WorkflowStep{
			{
				ID:         "task",
				Name:       "Task",
				Type:       definition.StepTypeServiceTask,
				JobType:    jobType,
				RetryCount: retryCount,
				NextStep:   "end",
				OutputsSchema: &definition.Schema{
					Properties: map[string]definition.PropertyDescriptor{
						"payment_id": {Type: definition.SchemaTypeString},
					},
					Required: []string{"payment_id"},
				},
			},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
}

func TestOutputSchema_BadPayloadFailsInstanceAndBypassesRetry(t *testing.T) {
	ctx := context.Background()
	def := buildOutputSchemaDef("us3-bad-output", "us3-bad-job", 3)
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	j := pollJob(ctx, t, "us3-bad-job")

	// Worker returns payment_id as a number — violates the SERVICE_TASK's outputs_schema.
	completeErr := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", map[string]any{"payment_id": 12345})
	sv, ok := definition.IsSchemaViolation(completeErr)
	if !ok {
		t.Fatalf("expected *SchemaViolationError, got %T: %v", completeErr, completeErr)
	}
	if !strings.Contains(sv.Error(), `field "payment_id" expected string, got number`) {
		t.Errorf("violation message: %s", sv.Error())
	}

	// Instance must end in FAILED with the violation message in failure_reason.
	got, err := gInstSvc.Get(ctx, inst.ID)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	if got.Status != instance.InstanceStatusFailed {
		t.Errorf("expected FAILED, got %s", got.Status)
	}
	reason := ""
	if got.FailureReason != nil {
		reason = *got.FailureReason
	}
	if !strings.Contains(reason, `field "payment_id" expected string, got number`) {
		t.Errorf("instance failure_reason should carry violation, got: %s", reason)
	}

	// Job must be CANCELLED (not retried) — verifies "retry_count is not consulted".
	var jobStatus string
	if err := gPool.QueryRow(ctx, `SELECT status FROM job WHERE id = $1`, j.ID).Scan(&jobStatus); err != nil {
		t.Fatalf("read job status: %v", err)
	}
	if jobStatus != "CANCELLED" {
		t.Errorf("expected job CANCELLED (retry bypassed), got %s", jobStatus)
	}

	// Step execution must carry the violation in failure_reason — verifies the audit trail.
	var seReason *string
	if err := gPool.QueryRow(ctx,
		`SELECT failure_reason FROM step_execution WHERE instance_id = $1 AND step_id = 'task'`,
		inst.ID,
	).Scan(&seReason); err != nil {
		t.Fatalf("read step_execution: %v", err)
	}
	if seReason == nil || !strings.Contains(*seReason, "expected string") {
		t.Errorf("step_execution failure_reason missing violation; got %v", seReason)
	}
}

func TestOutputSchema_ValidPayloadProceeds(t *testing.T) {
	ctx := context.Background()
	def := buildOutputSchemaDef("us3-ok-output", "us3-ok-job", 0)
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	j := pollJob(ctx, t, "us3-ok-job")
	if err := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", map[string]any{"payment_id": "pay_abc"}); err != nil {
		t.Fatalf("CompleteJobAndAdvance: %v", err)
	}
	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusCompleted {
		t.Errorf("expected COMPLETED, got %s", terminal.Status)
	}
}

func TestOutputSchema_NoSchemaIsRegressionFree(t *testing.T) {
	ctx := context.Background()
	def := &definition.WorkflowDefinition{
		ID:   "us3-no-output-schema",
		Name: "no output schema",
		Steps: []definition.WorkflowStep{
			{ID: "task", Name: "Task", Type: definition.StepTypeServiceTask, JobType: "us3-noschema-job", NextStep: "end"},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	if _, err := gDefRepo.Upload(ctx, def); err != nil {
		t.Fatalf("upload: %v", err)
	}
	inst, err := gInstSvc.Start(ctx, def.ID, 0, nil, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	j := pollJob(ctx, t, "us3-noschema-job")
	// Anything goes — same payload that would violate the schema above must succeed here.
	if err := gInstSvc.CompleteJobAndAdvance(ctx, j.ID, "test-worker", map[string]any{"payment_id": 12345}); err != nil {
		t.Errorf("expected success without schema, got: %v", err)
	}
	terminal, err := awaitTerminal(ctx, inst.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("await terminal: %v", err)
	}
	if terminal.Status != instance.InstanceStatusCompleted {
		t.Errorf("expected COMPLETED, got %s", terminal.Status)
	}
}
