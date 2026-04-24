package definition

import (
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

// Validate checks every rule from data-model §1.1 and returns a
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
			if len(s.ConditionalNextSteps) == 0 {
				errs = append(errs, fmt.Sprintf("step %q (DECISION): conditionalNextSteps must not be empty", s.ID))
			}
			for _, target := range s.ConditionalNextSteps {
				checkRef(s.ID, "conditionalNextSteps target", target, stepByID, &errs)
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
	for _, t := range s.ConditionalNextSteps {
		graphWalk(t, steps, visited)
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
}
