// Command validate-fixture runs the Go workflow-engine's authoritative parser
// and validator against a single workflow JSON fixture and prints a JSON
// verdict `{"accepted": bool, "error": string}` on stdout.
//
// It is invoked by the workflow-modeller drift-guard harness (T068) to verify
// that every fixture accepted by the TypeScript validator is also accepted by
// the Go engine — the mechanical guarantee for SC-002.
//
// The command lives inside the workflow-engine module because the engine's
// `internal/definition` package can only be imported from within that module.
// Invocation from workflow-modeller:
//
//	(cd ../workflow-engine && go run ./cmd/validate-fixture <fixture-path>)
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/batnam/rochallor-engine/workflow-engine/internal/definition"
)

type verdict struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-fixture <path-to-fixture.json>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		emit(verdict{Accepted: false, Error: fmt.Sprintf("read: %v", err)})
		return
	}

	def, err := definition.ParseBytes(data)
	if err != nil {
		emit(verdict{Accepted: false, Error: fmt.Sprintf("parse: %v", err)})
		return
	}

	if vErr := definition.Validate(def); vErr != nil {
		emit(verdict{Accepted: false, Error: vErr.Error()})
		return
	}

	emit(verdict{Accepted: true})
}

func emit(v verdict) {
	out, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "json marshal: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
}
