package definition_test

import (
	"os"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

func validLoanDef() *definition.WorkflowDefinition {
	return &definition.WorkflowDefinition{
		ID:   "LOS::test-workflow",
		Name: "Test Workflow",
		Steps: []definition.WorkflowStep{
			{ID: "start", Name: "Start", Type: definition.StepTypeServiceTask, JobType: "test-job", NextStep: "end"},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
}

func TestValidateHappyPath(t *testing.T) {
	if err := definition.Validate(validLoanDef()); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateAllLegacyFixtures(t *testing.T) {
	for _, name := range []string{
		"loan-application-workflow.json",
		"document-verification-workflow.json",
		"user-task-test-workflow.json",
		"sample_parallel_service_task.json",
		"sample_parallel_user_task.json",
	} {
		t.Run(name, func(t *testing.T) {
			f := mustOpenFixture(t, name)
			def, err := definition.Parse(f)
			_ = f.Close()
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err = definition.Validate(def); err != nil {
				t.Errorf("validate %s: %v", name, err)
			}
		})
	}
}

func TestValidateUnknownStepType(t *testing.T) {
	d := validLoanDef()
	d.Steps[0].Type = "UNKNOWN_STEP_TYPE"
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for unknown step type")
	}
}

func TestValidateDuplicateStepID(t *testing.T) {
	d := validLoanDef()
	d.Steps = append(d.Steps, definition.WorkflowStep{ID: "start", Name: "Dup", Type: definition.StepTypeServiceTask, NextStep: "end"})
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for duplicate step id")
	}
}

func TestValidateUnknownNextStep(t *testing.T) {
	d := validLoanDef()
	d.Steps[0].NextStep = "does-not-exist"
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for unknown nextStep reference")
	}
}

func TestValidateDecisionNoBranches(t *testing.T) {
	d := &definition.WorkflowDefinition{
		ID:   "dec",
		Name: "Dec",
		Steps: []definition.WorkflowStep{
			{ID: "s1", Name: "S1", Type: definition.StepTypeDecision},
			{ID: "end", Name: "End", Type: definition.StepTypeEnd},
		},
	}
	if err := definition.Validate(d); err == nil {
		t.Fatal("expected error for decision with no branches")
	}
}

func TestValidateMalformedBoundaryEvent(t *testing.T) {
	d := validLoanDef()
	d.Steps[0].BoundaryEvents = []definition.BoundaryEvent{
		{Type: definition.BoundaryEventTypeTimer, Duration: "", TargetStepId: "end"},
	}
	err := definition.Validate(d)
	if err == nil {
		t.Fatal("expected error for boundary event missing duration")
	}
}

func TestValidateMissingID(t *testing.T) {
	d := validLoanDef()
	d.ID = ""
	if err := definition.Validate(d); err == nil {
		t.Fatal("expected error for missing id")
	}
}

// mustOpenFixture opens a fixture file for use in tests.
func mustOpenFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(fixtureDir + "/" + name)
	if err != nil {
		t.Skipf("fixture %s not found: %v", name, err)
	}
	return f
}
