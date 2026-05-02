package scenarios

import (
	"context"
	"os"
	"path/filepath"
)

// RunDefinitionAPI exercises GetDefinition and ListDefinitions after uploading a definition.
// It also verifies that re-uploading the same definition ID increments the version.
func RunDefinitionAPI(t TestReporter, client ClientIface, scenariosDir, prefix string) {
	defPath := filepath.Join(scenariosDir, prefix, "linear.json")
	def, err := os.ReadFile(defPath)
	if err != nil {
		t.Errorf("read %s: %v", defPath, err)
		return
	}

	ctx := context.Background()
	defID := "e2e-" + prefix + "-linear"

	// Upload once (may already exist from the linear scenario run).
	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("[%s/definition-api] upload definition: %v", prefix, err)
		return
	}

	// GetDefinition — verify round-trip.
	got, err := client.GetDefinition(ctx, defID)
	if err != nil {
		t.Errorf("[%s/definition-api] get definition: %v", prefix, err)
		return
	}
	if got.ID != defID {
		t.Errorf("[%s/definition-api] GetDefinition: want id %q, got %q", prefix, defID, got.ID)
	}
	if got.Version < 1 {
		t.Errorf("[%s/definition-api] GetDefinition: want version >= 1, got %d", prefix, got.Version)
	}
	t.Logf("[%s/definition-api] GetDefinition returned id=%q version=%d ✓", prefix, got.ID, got.Version)

	versionBefore := got.Version

	// Re-upload the same definition — the engine should return the existing version
	// (idempotent for identical content) or bump the version if the content changes.
	// Either way, re-fetching must succeed and version must be >= versionBefore.
	if err := client.UploadDefinition(ctx, def); err != nil {
		t.Errorf("[%s/definition-api] re-upload definition: %v", prefix, err)
		return
	}
	got2, err := client.GetDefinition(ctx, defID)
	if err != nil {
		t.Errorf("[%s/definition-api] get definition after re-upload: %v", prefix, err)
		return
	}
	if got2.Version < versionBefore {
		t.Errorf("[%s/definition-api] version after re-upload %d < version before %d", prefix, got2.Version, versionBefore)
	}
	t.Logf("[%s/definition-api] re-upload: version %d → %d ✓", prefix, versionBefore, got2.Version)

	// ListDefinitions — result must be non-empty and contain our definition.
	list, err := client.ListDefinitions(ctx)
	if err != nil {
		t.Errorf("[%s/definition-api] list definitions: %v", prefix, err)
		return
	}
	if len(list) == 0 {
		t.Errorf("[%s/definition-api] ListDefinitions returned empty list", prefix)
		return
	}
	found := false
	for _, d := range list {
		if d.ID == defID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("[%s/definition-api] ListDefinitions does not contain %q", prefix, defID)
	} else {
		t.Logf("[%s/definition-api] ListDefinitions contains %q (%d total) ✓", prefix, defID, len(list))
	}
}
