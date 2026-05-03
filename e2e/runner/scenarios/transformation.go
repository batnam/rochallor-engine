package scenarios

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunTransformation uploads the transformation workflow for prefix, starts an instance,
// and asserts the TRANSFORMATION step correctly rewrites instance variables.
func RunTransformation(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "transformation.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()

	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("upload transformation definition (%s): %v", prefix, err)
		return
	}

	instanceID, err := client.StartInstance(ctx, "e2e-"+prefix+"-transformation", nil)
	if err != nil {
		t.Errorf("start transformation instance (%s): %v", prefix, err)
		return
	}

	t.Logf("[%s/transformation] instance %s started", prefix, instanceID)
	LogInstanceStarted("e2e-"+prefix+"-transformation", instanceID, nil)

	inst, err := PollUntilTerminal(ctx, client, instanceID, 15*time.Second)
	if err != nil {
		t.Errorf("[%s/transformation] poll timeout: %v", prefix, err)
		return
	}

	if inst.Status != "COMPLETED" {
		t.Errorf("[%s/transformation] want COMPLETED, got %s (failureReason: %q)", prefix, inst.Status, inst.FailureReason)
		return
	}

	// Assert variable merging: ${firstName} expression resolves, literals pass through.
	assertVar(t, prefix, "transformation", inst.Variables, "fullName", "Alice")
	assertVar(t, prefix, "transformation", inst.Variables, "greeting", "Hello")
	assertVar(t, prefix, "transformation", inst.Variables, "score", float64(42))

	t.Logf("[%s/transformation] COMPLETED with correct variables ✓", prefix)
}

func assertVar(t TestReporter, prefix, scenario string, vars map[string]any, key string, want any) {
	got, ok := vars[key]
	if !ok {
		t.Errorf("[%s/%s] variable %q missing from instance variables", prefix, scenario, key)
		return
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Errorf("[%s/%s] variable %q: want %v, got %v", prefix, scenario, key, want, got)
	}
}
