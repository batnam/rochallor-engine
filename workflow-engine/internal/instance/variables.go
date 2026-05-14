package instance

import (
	"encoding/json"
	"fmt"
)

// mergeVariables overlays delta onto existing and returns the merged JSON.
// Keys present in delta always win; other existing keys are preserved.
func mergeVariables(existing json.RawMessage, delta map[string]any) (json.RawMessage, error) {
	base := make(map[string]any)
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &base); err != nil {
			return nil, fmt.Errorf("merge vars: unmarshal existing: %w", err)
		}
		if base == nil {
			base = make(map[string]any)
		}
	}
	for k, v := range delta {
		base[k] = v
	}
	return json.Marshal(base)
}

// variablesToMap unmarshals an instance.Variables blob into a string-keyed map.
func variablesToMap(raw json.RawMessage) (map[string]any, error) {
	m := make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("unmarshal instance variables: %w", err)
		}
	}
	return m, nil
}

// evaluateExpr is a thin adapter to the expression package, injected at
// startup to avoid an import cycle.
var evaluateExpr func(expr string, vars map[string]any) (any, error)

// SetExpressionEvaluator injects the expression evaluator (called from main).
func SetExpressionEvaluator(fn func(expr string, vars map[string]any) (any, error)) {
	evaluateExpr = fn
}
