package definition_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

// fixtureDir is the path to the copied legacy fixtures.
const fixtureDir = "../../test/fixtures"

func TestParseAllLegacyFixtures(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Skip("no fixtures found at " + fixtureDir)
	}

	for _, path := range fixtures {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()

			def, err := definition.Parse(f)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if def.ID == "" {
				t.Errorf("id is empty")
			}
			if len(def.Steps) == 0 {
				t.Errorf("steps is empty")
			}

			// Round-trip: re-serialize and re-parse
			data, err := def.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			def2, err := definition.ParseBytes(data)
			if err != nil {
				t.Fatalf("re-parse: %v", err)
			}
			if def2.ID != def.ID {
				t.Errorf("round-trip id mismatch: want %q, got %q", def.ID, def2.ID)
			}
			if len(def2.Steps) != len(def.Steps) {
				t.Errorf("round-trip steps len: want %d, got %d", len(def.Steps), len(def2.Steps))
			}
		})
	}
}
