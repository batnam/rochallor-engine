package instance

import (
	"encoding/json"
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/db"
	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// matchedRule pairs a rule's document index with the rule itself, so the
// hit-policy switch can refer to both without re-iterating the rules slice.
type matchedRule struct {
	idx  int
	rule *definition.DecisionTableRule
}

// handleDecisionTable evaluates a DECISION_TABLE step. Rules are evaluated in
// document order against the pre-step variable map. Matched rules
// are collapsed by the step's HitPolicy into an output map, which is merged
// into the instance variable map; the instance then unconditionally advances
// to step.NextStep. No-match under any policy fails the step with "DecisionTableNoRuleMatched".
func (s *Service) handleDecisionTable(ctx context.Context, tx db.Tx, inst *WorkflowInstance, def *definition.WorkflowDefinition, step *definition.WorkflowStep) error {
	vars, err := variablesToMap(inst.Variables)
	if err != nil {
		return s.failInstance(ctx, tx, inst, step.ID, fmt.Sprintf("corrupt instance variables: %v", err))
	}
	if step.DecisionTable == nil {
		return s.failInstance(ctx, tx, inst, step.ID, "DecisionTable payload missing")
	}

	// Match-collection pass — single walk over the rules slice.
	matched := make([]matchedRule, 0, len(step.DecisionTable.Rules))
	for idx := range step.DecisionTable.Rules {
		rule := &step.DecisionTable.Rules[idx]
		ok, ferr := ruleMatches(rule, vars, step.ID, idx)
		if ferr != nil {
			return s.failInstance(ctx, tx, inst, step.ID, ferr.Error())
		}
		if ok {
			matched = append(matched, matchedRule{idx: idx, rule: rule})
		}
	}

	if len(matched) == 0 {
		return s.failInstance(ctx, tx, inst, step.ID, DecisionTableNoRuleMatched)
	}

	policy := step.HitPolicy
	if policy == "" {
		policy = "U" // default
	}

	// Evaluate each matched rule's outputs once, against the pre-step vars,
	// in document order. The hit-policy switch below collapses the per-rule
	// output maps into a single output map for the variable merge.
	evaluated := make([]map[string]any, len(matched))
	for i, m := range matched {
		out, err := evaluateRuleOutputs(m.rule, vars, step.ID, m.idx)
		if err != nil {
			return s.failInstance(ctx, tx, inst, step.ID, err.Error())
		}
		evaluated[i] = out
	}

	var outputs map[string]any
	switch policy {
	case "U":
		if len(matched) > 1 {
			indices := make([]int, len(matched))
			for i, m := range matched {
				indices[i] = m.idx
			}
			return s.failInstance(ctx, tx, inst, step.ID,
				fmt.Sprintf("%s: step %q, matching rule indices %v", DecisionTableUniqueViolation, step.ID, indices))
		}
		outputs = evaluated[0]
	case "F":
		outputs = evaluated[0]
	case "A":
		// Every matched rule must produce structurally-equal values per
		// column. On disagreement, fail with DecisionTableAnyConflict.
		outputs = evaluated[0]
		for col := range outputs {
			for j := 1; j < len(evaluated); j++ {
				if !reflect.DeepEqual(evaluated[0][col], evaluated[j][col]) {
					return s.failInstance(ctx, tx, inst, step.ID,
						fmt.Sprintf("%s: step %q, column=%q values=[%v, %v] (ruleIndex %d vs %d)",
							DecisionTableAnyConflict, step.ID, col,
							evaluated[0][col], evaluated[j][col],
							matched[0].idx, matched[j].idx))
				}
			}
		}
	case "R", "C":
		// Per-column lists in matched-document order. Rules omitting a
		// column contribute nil to their slot.
		outputs = buildColumnLists(evaluated)
	case "C+", "C#", "C>", "C<":
		// Collapse the per-column list into a scalar via the suffix operator.
		lists := buildColumnLists(evaluated)
		collapsed := make(map[string]any, len(lists))
		for col, raw := range lists {
			vals := raw.([]any)
			scalar, aerr := aggregateColumn(policy[1], vals)
			if aerr != nil {
				return s.failInstance(ctx, tx, inst, step.ID,
					fmt.Sprintf("%s: step %q, column=%q: %v", DecisionTableAggregatorTypeError, step.ID, col, aerr))
			}
			collapsed[col] = scalar
		}
		outputs = collapsed
	}

	if len(outputs) > 0 {
		for k, v := range outputs {
			vars[k] = v
		}
		merged, err := json.Marshal(vars)
		if err != nil {
			return fmt.Errorf("decision_table: marshal merged vars: %w", err)
		}
		inst.Variables = merged
		if err := s.store.UpdateInstanceVariablesPartial(ctx, tx, inst.ID, outputs); err != nil {
			return err
		}
	}
	if err := s.store.CompleteStepExecutionByStepNoOutput(ctx, tx, inst.ID, step.ID); err != nil {
		return err
	}
	removeFromCurrentSteps(inst, step.ID)
	return s.dispatchStep(ctx, tx, inst, def, step.NextStep)
}

// ruleMatches evaluates a rule's When cells against vars. Empty/whitespace
// cell expressions are wildcards. Returns true iff every cell evaluates to
// a truthy bool. On evaluator error or non-bool cell result the returned
// error carries the DecisionTableCellError prefix.
func ruleMatches(rule *definition.DecisionTableRule, vars map[string]any, stepID string, idx int) (bool, error) {
	for col, cellExpr := range rule.When {
		if strings.TrimSpace(cellExpr) == "" {
			continue
		}
		result, err := evaluateExpr(cellExpr, vars)
		if err != nil {
			return false, fmt.Errorf("%s: ruleIndex=%d column=%q expr=%q err=%v", DecisionTableCellError, idx, col, cellExpr, err)
		}
		b, ok := result.(bool)
		if !ok {
			return false, fmt.Errorf("%s: ruleIndex=%d column=%q expr=%q result=%T not bool", DecisionTableCellError, idx, col, cellExpr, result)
		}
		if !b {
			return false, nil
		}
	}
	return true, nil
}

// buildColumnLists transposes a slice of per-rule output maps into a single
// map of column → []any in matched-document order. Rules omitting a column
// contribute nil for their slot. Used by hit policies R, C, and the C±#><
// aggregators (which then collapse each list to a scalar).
func buildColumnLists(evaluated []map[string]any) map[string]any {
	cols := map[string]struct{}{}
	for _, m := range evaluated {
		for k := range m {
			cols[k] = struct{}{}
		}
	}
	out := make(map[string]any, len(cols))
	for col := range cols {
		list := make([]any, len(evaluated))
		for i, m := range evaluated {
			list[i] = m[col]
		}
		out[col] = list
	}
	return out
}

// aggregateColumn collapses a per-column value list according to a Collect
// aggregator suffix character ('+', '#', '>', '<'). '#' returns the count
// of matched rules; the others require numeric values and return their
// scalar collapse. Non-numeric values (other than under '#') produce an
// error so the caller can fail the step with DecisionTableAggregatorTypeError.
func aggregateColumn(suffix byte, values []any) (any, error) {
	if suffix == '#' {
		return len(values), nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no values to aggregate")
	}
	nums := make([]float64, len(values))
	for i, v := range values {
		n, ok := toFloat64(v)
		if !ok {
			return nil, fmt.Errorf("value %v (type %T) at index %d is not numeric", v, v, i)
		}
		nums[i] = n
	}
	switch suffix {
	case '+':
		var sum float64
		for _, n := range nums {
			sum += n
		}
		return sum, nil
	case '>':
		m := nums[0]
		for _, n := range nums[1:] {
			if n > m {
				m = n
			}
		}
		return m, nil
	case '<':
		m := nums[0]
		for _, n := range nums[1:] {
			if n < m {
				m = n
			}
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown aggregator suffix %q", suffix)
	}
}

// toFloat64 converts the numeric Go types produced by the JSON evaluator
// into a float64. Returns false on a non-numeric value.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// evaluateRuleOutputs materialises a rule's Outputs map against the pre-step
// variable snapshot. Each value is JSON-unmarshalled; a "${expr}" string is
// evaluated via the expression evaluator (matching TRANSFORMATION's encoding).
func evaluateRuleOutputs(rule *definition.DecisionTableRule, vars map[string]any, stepID string, idx int) (map[string]any, error) {
	if len(rule.Outputs) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(rule.Outputs))
	for k, rawVal := range rule.Outputs {
		var v any
		if err := json.Unmarshal(rawVal, &v); err != nil {
			return nil, fmt.Errorf("%s: ruleIndex=%d output=%q unmarshal: %v", DecisionTableOutputError, idx, k, err)
		}
		if strVal, ok := v.(string); ok && strings.HasPrefix(strVal, "${") && strings.HasSuffix(strVal, "}") {
			inner := strings.TrimSpace(strVal[2 : len(strVal)-1])
			if inner == "now()" {
				v = time.Now().UTC().Format(time.RFC3339)
			} else {
				result, err := evaluateExpr(inner, vars)
				if err != nil {
					return nil, fmt.Errorf("%s: ruleIndex=%d output=%q expr=%q err=%v", DecisionTableOutputError, idx, k, inner, err)
				}
				v = result
			}
		}
		out[k] = v
	}
	return out, nil
}
