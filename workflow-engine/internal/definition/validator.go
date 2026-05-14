package definition

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var idRegexp = regexp.MustCompile(`^[A-Za-z0-9_:\-]+$`)

// ValidationErrors is a slice of validation error strings, returned when
// multiple validation failures are found.
type ValidationErrors []string

func (e ValidationErrors) Error() string {
	return "validation errors:\n  " + strings.Join(e, "\n  ")
}

// Validate checks every rule and returns a
// ValidationErrors slice if any rule is violated. Returns nil on success.
func Validate(def *WorkflowDefinition) error {
	var errs ValidationErrors

	// ── Top-level field rules ──────────────────────────────────────────────────
	if def.ID == "" {
		errs = append(errs, "id is required")
	} else if len(def.ID) > 256 {
		errs = append(errs, fmt.Sprintf("id must be ≤ 256 characters, got %d", len(def.ID)))
	} else if !idRegexp.MatchString(def.ID) {
		errs = append(errs, fmt.Sprintf("id %q must match ^[A-Za-z0-9_:\\-]+$", def.ID))
	}

	if def.Name == "" {
		errs = append(errs, "name is required")
	}

	if len(def.Steps) == 0 {
		errs = append(errs, "steps must not be empty")
	}

	if def.AutoStartNextWorkflow && def.NextWorkflowId == "" {
		errs = append(errs, "nextWorkflowId is required when autoStartNextWorkflow is true")
	}

	if len(errs) > 0 && len(def.Steps) == 0 {
		return errs // stop early if no steps
	}

	// ── Build step lookup map ─────────────────────────────────────────────────
	stepByID := make(map[string]*WorkflowStep, len(def.Steps))
	for i := range def.Steps {
		s := &def.Steps[i]
		if s.ID == "" {
			errs = append(errs, fmt.Sprintf("step[%d] id is required", i))
			continue
		}
		if _, dup := stepByID[s.ID]; dup {
			errs = append(errs, fmt.Sprintf("duplicate step id: %q", s.ID))
		}
		stepByID[s.ID] = s
	}

	// ── Per-step rules ────────────────────────────────────────────────────────
	validTypes := map[StepType]bool{
		StepTypeServiceTask:     true,
		StepTypeUserTask:        true,
		StepTypeDecision:        true,
		StepTypeTransformation:  true,
		StepTypeWait:            true,
		StepTypeParallelGateway: true,
		StepTypeJoinGateway:     true,
		StepTypeEnd:             true,
		StepTypeDecisionTable:   true,
	}

	for _, s := range def.Steps {
		if s.ID == "" {
			continue // already reported
		}

		if s.Name == "" {
			errs = append(errs, fmt.Sprintf("step %q: name is required", s.ID))
		}

		if !validTypes[s.Type] {
			errs = append(errs, fmt.Sprintf("step %q: unsupported type %q", s.ID, s.Type))
		}

		switch s.Type {
		case StepTypeServiceTask, StepTypeUserTask:
			if s.Type == StepTypeServiceTask && s.JobType == "" {
				errs = append(errs, fmt.Sprintf("step %q (SERVICE_TASK): jobType is required", s.ID))
			}
			if s.NextStep != "" {
				checkRef(s.ID, "nextStep", s.NextStep, stepByID, &errs)
			}

		case StepTypeDecision:
			if s.ConditionalNextSteps.Len() == 0 {
				errs = append(errs, fmt.Sprintf("step %q (DECISION): conditionalNextSteps must not be empty", s.ID))
			}
			if s.ConditionalNextSteps != nil {
				for _, target := range s.ConditionalNextSteps.Targets {
					checkRef(s.ID, "conditionalNextSteps target", target, stepByID, &errs)
				}
			}

		case StepTypeTransformation:
			if s.NextStep == "" {
				errs = append(errs, fmt.Sprintf("step %q (TRANSFORMATION): nextStep is required", s.ID))
			} else {
				checkRef(s.ID, "nextStep", s.NextStep, stepByID, &errs)
			}
			if len(s.Transformations) == 0 {
				errs = append(errs, fmt.Sprintf("step %q (TRANSFORMATION): transformations must not be empty", s.ID))
			}

		case StepTypeWait:
			if s.NextStep == "" {
				errs = append(errs, fmt.Sprintf("step %q (WAIT): nextStep is required", s.ID))
			} else {
				checkRef(s.ID, "nextStep", s.NextStep, stepByID, &errs)
			}

		case StepTypeParallelGateway:
			if len(s.ParallelNextSteps) < 2 {
				errs = append(errs, fmt.Sprintf("step %q (PARALLEL_GATEWAY): parallelNextSteps must have at least 2 entries", s.ID))
			}
			if s.JoinStep == "" {
				errs = append(errs, fmt.Sprintf("step %q (PARALLEL_GATEWAY): joinStep is required", s.ID))
			} else {
				checkRef(s.ID, "joinStep", s.JoinStep, stepByID, &errs)
			}
			for _, pns := range s.ParallelNextSteps {
				checkRef(s.ID, "parallelNextSteps", pns, stepByID, &errs)
			}

		case StepTypeJoinGateway:
			if s.NextStep == "" {
				errs = append(errs, fmt.Sprintf("step %q (JOIN_GATEWAY): nextStep is required", s.ID))
			} else {
				checkRef(s.ID, "nextStep", s.NextStep, stepByID, &errs)
			}

		case StepTypeEnd:
			// no mandatory fields

		case StepTypeDecisionTable:
			validateDecisionTable(s, stepByID, &errs)
		}

		// Forbid DecisionTable on non-DECISION_TABLE steps (symmetric guard).
		if s.Type != StepTypeDecisionTable && s.DecisionTable != nil {
			errs = append(errs, fmt.Sprintf("step %q (%s): decisionTable is not valid on this step type", s.ID, s.Type))
		}

		// Boundary events
		for j, be := range s.BoundaryEvents {
			if be.Type != BoundaryEventTypeTimer {
				errs = append(errs, fmt.Sprintf("step %q boundaryEvents[%d]: unsupported type %q (only TIMER is in scope)", s.ID, j, be.Type))
			}
			if be.Duration == "" {
				errs = append(errs, fmt.Sprintf("step %q boundaryEvents[%d]: duration is required for TIMER events", s.ID, j))
			}
			if be.TargetStepId == "" {
				errs = append(errs, fmt.Sprintf("step %q boundaryEvents[%d]: targetStepId is required", s.ID, j))
			} else {
				checkRef(s.ID, fmt.Sprintf("boundaryEvents[%d].targetStepId", j), be.TargetStepId, stepByID, &errs)
			}
		}
	}

	// ── Reachability: walk the graph from the first step ──────────────────────
	if len(def.Steps) > 0 && def.Steps[0].ID != "" {
		reachable := make(map[string]bool)
		graphWalk(def.Steps[0].ID, stepByID, reachable)

		// Every step in the definition must be reachable
		for _, s := range def.Steps {
			if s.ID != "" && !reachable[s.ID] {
				errs = append(errs, fmt.Sprintf("step %q is unreachable from the first step", s.ID))
			}
		}

		// At least one END step must be reachable
		hasEnd := false
		for id := range reachable {
			if s, ok := stepByID[id]; ok && s.Type == StepTypeEnd {
				hasEnd = true
				break
			}
		}
		if !hasEnd {
			errs = append(errs, "no END step is reachable from the first step")
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func checkRef(stepID, field, target string, stepByID map[string]*WorkflowStep, errs *ValidationErrors) {
	if _, ok := stepByID[target]; !ok {
		*errs = append(*errs, fmt.Sprintf("step %q %s references unknown step %q", stepID, field, target))
	}
}

// validHitPolicies is the closed set of recognised hitPolicy values.
var validHitPolicies = map[string]bool{
	"U": true, "F": true, "A": true, "R": true, "C": true,
	"C+": true, "C#": true, "C>": true, "C<": true,
}

// validateDecisionTable enforces the upload-time invariants for a
// DECISION_TABLE step (007 wire format): payload present, at least one rule,
// step-level nextStep is required and resolves, hitPolicy is recognised,
// outputs values are well-formed JSON, and no foreign step-type fields
func validateDecisionTable(s WorkflowStep, stepByID map[string]*WorkflowStep, errs *ValidationErrors) {
	// V1 — payload present.
	if s.DecisionTable == nil {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): decisionTable payload is required", s.ID))
		return
	}
	dt := s.DecisionTable
	// V2 — rules non-empty.
	if len(dt.Rules) == 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): rules must not be empty", s.ID))
	}
	// V7 — outputs values are well-formed JSON.
	for i, r := range dt.Rules {
		for k, raw := range r.Outputs {
			var probe any
			if err := json.Unmarshal(raw, &probe); err != nil {
				*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE) rules[%d].outputs[%q]: invalid JSON value: %v", s.ID, i, k, err))
			}
		}
	}

	// V3 / V4 — nextStep is required and must resolve.
	if s.NextStep == "" {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): nextStep is required", s.ID))
	} else {
		checkRef(s.ID, "nextStep", s.NextStep, stepByID, errs)
	}

	// V5 / V6 — hitPolicy enum + aggregator-only-on-C check.
	if s.HitPolicy != "" {
		if !validHitPolicies[s.HitPolicy] {
			// Distinguish "aggregator-on-non-C" (V6) from "unknown code" (V5).
			if len(s.HitPolicy) >= 2 && (s.HitPolicy[1] == '+' || s.HitPolicy[1] == '#' || s.HitPolicy[1] == '>' || s.HitPolicy[1] == '<') && s.HitPolicy[0] != 'C' {
				*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): aggregator %q is only valid on hitPolicy \"C\" (got %q)", s.ID, string(s.HitPolicy[1]), s.HitPolicy))
			} else {
				*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): hitPolicy %q is not recognised; expected one of U, F, A, R, C, C+, C#, C>, C<", s.ID, s.HitPolicy))
			}
		}
	}

	// V10 — foreign-step fields must not appear on a DECISION_TABLE step.
	if s.ConditionalNextSteps.Len() > 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): conditionalNextSteps is not valid on this step type", s.ID))
	}
	if len(s.Transformations) > 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): transformations is not valid on this step type", s.ID))
	}
	if len(s.ParallelNextSteps) > 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): parallelNextSteps is not valid on this step type", s.ID))
	}
	if s.JoinStep != "" {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): joinStep is not valid on this step type", s.ID))
	}
	if s.JobType != "" {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): jobType is not valid on this step type", s.ID))
	}
	if s.DelegateClass != "" {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): delegateClass is not valid on this step type", s.ID))
	}
	if s.RetryCount != 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): retryCount is not valid on this step type", s.ID))
	}
	if len(s.BoundaryEvents) > 0 {
		*errs = append(*errs, fmt.Sprintf("step %q (DECISION_TABLE): boundaryEvents is not valid on this step type", s.ID))
	}
}

// graphWalk performs a DFS from start, marking all reachable step IDs.
func graphWalk(id string, steps map[string]*WorkflowStep, visited map[string]bool) {
	if visited[id] {
		return
	}
	visited[id] = true
	s, ok := steps[id]
	if !ok {
		return
	}
	if s.NextStep != "" {
		graphWalk(s.NextStep, steps, visited)
	}
	if s.ConditionalNextSteps != nil {
		for _, t := range s.ConditionalNextSteps.Targets {
			graphWalk(t, steps, visited)
		}
	}
	for _, t := range s.ParallelNextSteps {
		graphWalk(t, steps, visited)
	}
	if s.JoinStep != "" {
		graphWalk(s.JoinStep, steps, visited)
	}
	for _, be := range s.BoundaryEvents {
		graphWalk(be.TargetStepId, steps, visited)
	}
	// DECISION_TABLE's only outbound edge is the step-level NextStep (already
	// walked above). Per-rule routing was removed in 007; rules produce
	// outputs only.
}
