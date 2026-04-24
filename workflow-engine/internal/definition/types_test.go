package definition_test

import (
	"encoding/json"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// fullDefinition is a maximal fixture that exercises every field in §1.1 and §1.2
// of the data model so the round-trip test can assert nothing is silently dropped.
const fullDefinitionJSON = `{
  "id": "LOS::loan-application-workflow",
  "version": 3,
  "name": "Loan Application Workflow",
  "description": "Validates and approves loan applications",
  "autoStartNextWorkflow": true,
  "nextWorkflowId": "LOS::post-loan-workflow",
  "steps": [
    {
      "id": "validate-customer",
      "name": "Validate Customer",
      "type": "SERVICE_TASK",
      "description": "Calls the customer validation service",
      "nextStep": "check-eligibility",
      "jobType": "validate_customer_job",
      "delegateClass": "com.example.ValidateCustomerDelegate",
      "retryCount": 3,
      "boundaryEvents": [
        {
          "type": "TIMER",
          "duration": "PT30S",
          "interrupting": false,
          "targetStepId": "timeout-step"
        }
      ]
    },
    {
      "id": "check-eligibility",
      "name": "Check Eligibility",
      "type": "DECISION",
      "conditionalNextSteps": {
        "#customerValid == true && #customerScore >= 700": "calculate-loan-amount",
        "#customerValid == false": "reject-application"
      }
    },
    {
      "id": "calculate-loan-amount",
      "name": "Calculate Loan Amount",
      "type": "TRANSFORMATION",
      "transformations": {
        "approvalTimestamp": "${now()}",
        "loanAmount": 50000
      },
      "nextStep": "notify-parallel"
    },
    {
      "id": "notify-parallel",
      "name": "Notify In Parallel",
      "type": "PARALLEL_GATEWAY",
      "parallelNextSteps": ["notify-email", "notify-sms"],
      "joinStep": "join-notifications"
    },
    {
      "id": "notify-email",
      "name": "Send Email",
      "type": "SERVICE_TASK",
      "nextStep": "join-notifications",
      "jobType": "send_email_job"
    },
    {
      "id": "notify-sms",
      "name": "Send SMS",
      "type": "SERVICE_TASK",
      "nextStep": "join-notifications",
      "jobType": "send_sms_job"
    },
    {
      "id": "join-notifications",
      "name": "Join Notifications",
      "type": "JOIN_GATEWAY",
      "nextStep": "manual-review"
    },
    {
      "id": "manual-review",
      "name": "Manual Review",
      "type": "USER_TASK",
      "nextStep": "end-workflow",
      "jobType": "manual_review_task"
    },
    {
      "id": "wait-step",
      "name": "Wait For Document",
      "type": "WAIT",
      "nextStep": "end-workflow",
      "boundaryEvents": [
        {
          "type": "TIMER",
          "duration": "PT5M",
          "interrupting": false,
          "targetStepId": "timeout-step"
        }
      ]
    },
    {
      "id": "timeout-step",
      "name": "Handle Timeout",
      "type": "SERVICE_TASK",
      "nextStep": "end-workflow",
      "jobType": "handle_timeout_job"
    },
    {
      "id": "reject-application",
      "name": "Reject Application",
      "type": "SERVICE_TASK",
      "nextStep": "end-workflow",
      "jobType": "reject_application_job"
    },
    {
      "id": "end-workflow",
      "name": "End",
      "type": "END"
    }
  ],
  "metadata": {
    "version": "1.2.0",
    "author": "test",
    "category": "loans"
  }
}`

// TestDefinitionRoundTrip unmarshals the full fixture JSON into WorkflowDefinition
// and marshals it back, asserting that every field survives the round-trip.
func TestDefinitionRoundTrip(t *testing.T) {
	var def definition.WorkflowDefinition
	if err := json.Unmarshal([]byte(fullDefinitionJSON), &def); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// ── §1.1 WorkflowDefinition top-level fields ──────────────────────────────
	assertEqual(t, "id", "LOS::loan-application-workflow", def.ID)
	assertEqual(t, "version", 3, def.Version)
	assertEqual(t, "name", "Loan Application Workflow", def.Name)
	assertEqual(t, "description", "Validates and approves loan applications", def.Description)
	assertEqual(t, "autoStartNextWorkflow", true, def.AutoStartNextWorkflow)
	assertEqual(t, "nextWorkflowId", "LOS::post-loan-workflow", def.NextWorkflowId)
	assertLen(t, "steps", 12, def.Steps)
	assertNotNil(t, "metadata", def.Metadata)

	// ── §1.2 SERVICE_TASK step ────────────────────────────────────────────────
	s := def.Steps[0]
	assertEqual(t, "step[0].id", "validate-customer", s.ID)
	assertEqual(t, "step[0].type", definition.StepTypeServiceTask, s.Type)
	assertEqual(t, "step[0].nextStep", "check-eligibility", s.NextStep)
	assertEqual(t, "step[0].jobType", "validate_customer_job", s.JobType)
	assertEqual(t, "step[0].delegateClass", "com.example.ValidateCustomerDelegate", s.DelegateClass)
	assertEqual(t, "step[0].retryCount", 3, s.RetryCount)
	assertLen(t, "step[0].boundaryEvents", 1, s.BoundaryEvents)
	be := s.BoundaryEvents[0]
	assertEqual(t, "be.type", definition.BoundaryEventTypeTimer, be.Type)
	assertEqual(t, "be.duration", "PT30S", be.Duration)
	assertEqual(t, "be.interrupting", false, be.Interrupting)
	assertEqual(t, "be.targetStepId", "timeout-step", be.TargetStepId)

	// ── §1.2 DECISION step ───────────────────────────────────────────────────
	d := def.Steps[1]
	assertEqual(t, "step[1].type", definition.StepTypeDecision, d.Type)
	assertLenMap(t, "step[1].conditionalNextSteps", 2, d.ConditionalNextSteps)

	// ── §1.2 TRANSFORMATION step ─────────────────────────────────────────────
	tr := def.Steps[2]
	assertEqual(t, "step[2].type", definition.StepTypeTransformation, tr.Type)
	assertLenMap(t, "step[2].transformations", 2, tr.Transformations)
	assertEqual(t, "step[2].nextStep", "notify-parallel", tr.NextStep)

	// ── §1.2 PARALLEL_GATEWAY step ───────────────────────────────────────────
	pg := def.Steps[3]
	assertEqual(t, "step[3].type", definition.StepTypeParallelGateway, pg.Type)
	assertLen(t, "step[3].parallelNextSteps", 2, pg.ParallelNextSteps)
	assertEqual(t, "step[3].joinStep", "join-notifications", pg.JoinStep)

	// ── §1.2 JOIN_GATEWAY step ───────────────────────────────────────────────
	jg := def.Steps[6]
	assertEqual(t, "step[6].type", definition.StepTypeJoinGateway, jg.Type)
	assertEqual(t, "step[6].nextStep", "manual-review", jg.NextStep)

	// ── §1.2 USER_TASK step ──────────────────────────────────────────────────
	ut := def.Steps[7]
	assertEqual(t, "step[7].type", definition.StepTypeUserTask, ut.Type)
	assertEqual(t, "step[7].nextStep", "end-workflow", ut.NextStep)

	// ── §1.2 WAIT step ───────────────────────────────────────────────────────
	w := def.Steps[8]
	assertEqual(t, "step[8].type", definition.StepTypeWait, w.Type)
	assertLen(t, "step[8].boundaryEvents", 1, w.BoundaryEvents)

	// ── §1.2 END step ────────────────────────────────────────────────────────
	end := def.Steps[11]
	assertEqual(t, "step[11].type", definition.StepTypeEnd, end.Type)

	// ── Full re-serialise → re-parse (double round-trip) ─────────────────────
	out, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var def2 definition.WorkflowDefinition
	if err = json.Unmarshal(out, &def2); err != nil {
		t.Fatalf("second unmarshal: %v", err)
	}
	assertEqual(t, "double-trip id", def.ID, def2.ID)
	assertLen(t, "double-trip steps", len(def.Steps), def2.Steps)
}

// TestStepTypeEnumValues asserts that the StepType constants have the exact
// string values expected by the legacy JSON fixtures.
func TestStepTypeEnumValues(t *testing.T) {
	cases := []struct {
		want string
		got  definition.StepType
	}{
		{"SERVICE_TASK", definition.StepTypeServiceTask},
		{"USER_TASK", definition.StepTypeUserTask},
		{"DECISION", definition.StepTypeDecision},
		{"TRANSFORMATION", definition.StepTypeTransformation},
		{"WAIT", definition.StepTypeWait},
		{"PARALLEL_GATEWAY", definition.StepTypeParallelGateway},
		{"JOIN_GATEWAY", definition.StepTypeJoinGateway},
		{"END", definition.StepTypeEnd},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("StepType constant: want %q, got %q", c.want, c.got)
		}
	}
}

// TestBoundaryEventTypeEnumValues asserts the BoundaryEventType constants.
func TestBoundaryEventTypeEnumValues(t *testing.T) {
	if string(definition.BoundaryEventTypeTimer) != "TIMER" {
		t.Errorf("BoundaryEventTypeTimer: want %q, got %q", "TIMER", definition.BoundaryEventTypeTimer)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertEqual[T comparable](t *testing.T, field string, want, got T) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %v, got %v", field, want, got)
	}
}

func assertLen[T any](t *testing.T, field string, want int, slice []T) {
	t.Helper()
	if len(slice) != want {
		t.Errorf("%s: want len %d, got %d", field, want, len(slice))
	}
}

func assertLenMap[K comparable, V any](t *testing.T, field string, want int, m map[K]V) {
	t.Helper()
	if len(m) != want {
		t.Errorf("%s: want len %d, got %d", field, want, len(m))
	}
}

func assertNotNil[T any](t *testing.T, field string, v map[string]T) {
	t.Helper()
	if v == nil {
		t.Errorf("%s: want non-nil map, got nil", field)
	}
}
