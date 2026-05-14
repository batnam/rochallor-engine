package instance

import (
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

func findStep(def *definition.WorkflowDefinition, id string) *definition.WorkflowStep {
	for i := range def.Steps {
		if def.Steps[i].ID == id {
			return &def.Steps[i]
		}
	}
	return nil
}

func findParallelGatewayFor(def *definition.WorkflowDefinition, joinStepID string) *definition.WorkflowStep {
	for i := range def.Steps {
		if def.Steps[i].Type == definition.StepTypeParallelGateway && def.Steps[i].JoinStep == joinStepID {
			return &def.Steps[i]
		}
	}
	return nil
}

// branchLeafsFor returns the step IDs whose COMPLETED rows count as branch
// arrivals at the matching join.
func branchLeafsFor(pg *definition.WorkflowStep) []string {
	return pg.ParallelNextSteps
}

func removeFromCurrentSteps(inst *WorkflowInstance, stepID string) {
	inst.CurrentStepIDs = withoutStep(inst.CurrentStepIDs, stepID)
}

// withStep returns a new slice containing all ids plus stepID if not already
// present. It never mutates the input slice.
func withStep(ids []string, stepID string) []string {
	for _, s := range ids {
		if s == stepID {
			return ids
		}
	}
	out := make([]string, len(ids)+1)
	copy(out, ids)
	out[len(ids)] = stepID
	return out
}

// withoutStep returns a new slice with all ids except stepID. It never
// mutates the input slice.
func withoutStep(ids []string, stepID string) []string {
	out := make([]string, 0, len(ids))
	for _, s := range ids {
		if s != stepID {
			out = append(out, s)
		}
	}
	return out
}

// recomputeInstanceStatus inspects the remaining current_step_ids and returns
// WAITING when any remaining step is USER_TASK or WAIT, otherwise ACTIVE.
func recomputeInstanceStatus(inst *WorkflowInstance, def *definition.WorkflowDefinition) InstanceStatus {
	for _, sid := range inst.CurrentStepIDs {
		st := findStep(def, sid)
		if st == nil {
			continue
		}
		if st.Type == definition.StepTypeUserTask || st.Type == definition.StepTypeWait {
			return InstanceStatusWaiting
		}
	}
	return InstanceStatusActive
}
