package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/batnam/e2e/runner/scenarios"
)

// Result captures the outcome of one scenario run.
type Result struct {
	Transport string
	SDK       string
	Scenario  string
	Passed    bool
	Logs      []string
}

// SimpleReporter implements scenarios.TestReporter for the runner binary.
type SimpleReporter struct {
	failed bool
	logs   []string
}

func (r *SimpleReporter) Errorf(format string, args ...any) {
	r.failed = true
	r.logs = append(r.logs, "ERROR: "+fmt.Sprintf(format, args...))
}

func (r *SimpleReporter) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *SimpleReporter) AuditLog(instanceID string, eventType string, message string) {
	scenarios.LogEvent(instanceID, eventType, message)
}

func (r *SimpleReporter) Failed() bool { return r.failed }
func main() {
	engineURL := flag.String("engine", "http://localhost:18080", "workflow engine REST base URL")
	grpcEngineAddr := flag.String("grpc-engine", "localhost:19090", "workflow engine gRPC address (host:port)")
	sdkFlag := flag.String("sdk", "all", "SDK to test: go|all")
	transportFlag := flag.String("transport", "rest", "client transport: rest|grpc|all")
	scenariosDirFlag := flag.String("scenarios", "", "path to scenarios directory (default: ../scenarios relative to this binary)")
	flag.Parse()

	scenariosDir := *scenariosDirFlag
	if scenariosDir == "" {
		// When run via `go run .` from e2e/runner/, the source is at CWD.
		// Resolve scenarios relative to the source file's directory.
		_, srcFile, _, ok := runtime.Caller(0)
		if ok {
			scenariosDir = filepath.Join(filepath.Dir(srcFile), "..", "scenarios")
		} else {
			scenariosDir = filepath.Join(".", "..", "scenarios")
		}
	}

	sdks := resolveSdks(*sdkFlag)

	// Configure audit log directory
	logDir := os.Getenv("E2E_AUDIT_LOG_DIR")
	if logDir == "" {
		// Default to e2e/logs relative to the project root.
		// Since runner runs from e2e/runner/, it's ../logs
		logDir = "../logs"
	}
	scenarios.SetLogDir(logDir)

	transports := resolveTransports(*transportFlag)

	var results []Result
	for _, transport := range transports {
		client, cleanup, err := buildClient(transport, *engineURL, *grpcEngineAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s client: %v\n", transport, err)
			os.Exit(1)
		}
		for _, sdk := range sdks {
			results = append(results, runSDKSuite(transport, client, sdk, scenariosDir)...)
		}
		cleanup()
	}

	printReport(results)

	for _, r := range results {
		if !r.Passed {
			os.Exit(1)
		}
	}
}

func resolveTransports(flag string) []string {
	switch strings.ToLower(flag) {
	case "rest":
		return []string{"rest"}
	case "grpc":
		return []string{"grpc"}
	case "all":
		return []string{"rest", "grpc"}
	default:
		fmt.Fprintf(os.Stderr, "unknown transport %q; valid values: rest, grpc, all\n", flag)
		os.Exit(1)
		return nil
	}
}

func buildClient(transport, restURL, grpcAddr string) (scenarios.ClientIface, func(), error) {
	switch transport {
	case "rest":
		return NewClient(restURL), func() {}, nil
	case "grpc":
		c, err := NewGrpcClient(grpcAddr)
		if err != nil {
			return nil, nil, err
		}
		return c, func() { c.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown transport %q", transport)
	}
}

func resolveSdks(sdkFlag string) []string {
	switch strings.ToLower(sdkFlag) {
	case "go":
		return []string{"go"}
	case "python":
		return []string{"python"}
	case "node":
		return []string{"node"}
	case "java":
		return []string{"java"}
	case "all":
		return []string{"go", "python", "node", "java"}
	default:
		fmt.Fprintf(os.Stderr, "unknown sdk %q; valid values: go, python, node, java, all\n", sdkFlag)
		os.Exit(1)
		return nil
	}
}

func runSDKSuite(transport string, client scenarios.ClientIface, sdk, scenariosDir string) []Result {
	type scenarioFn func(t scenarios.TestReporter, client scenarios.ClientIface, scenariosDir, prefix string)
	type entry struct {
		name string
		fn   scenarioFn
	}
	suite := []entry{
		{"linear", scenarios.RunLinear},
		{"decision", scenarios.RunDecision},
		{"parallel", scenarios.RunParallel},
		{"user-task", scenarios.RunUserTask},
		{"timer", scenarios.RunTimer},
		{"wait-signal", scenarios.RunWaitSignal},
		{"retry-fail", scenarios.RunRetryFail}, {"chaining", scenarios.RunChaining},
		{"signalwaitstep-completeusertask", scenarios.RunSignalWaitStepCompleteUserTask},
		{"loan-approval", scenarios.RunLoanApproval},
	}

	var results []Result
	for _, s := range suite {
		r := &SimpleReporter{}
		s.fn(r, client, scenariosDir, sdk)
		results = append(results, Result{
			Transport: transport,
			SDK:       sdk,
			Scenario:  s.name,
			Passed:    !r.Failed(),
			Logs:      r.logs,
		})
	}
	return results
}

func printReport(results []Result) {
	fmt.Println()
	fmt.Println("=== E2E Test Results ===")
	passed := 0
	failed := 0
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		fmt.Printf("  [%s] %s/%s/%s\n", status, r.Transport, r.SDK, r.Scenario)
		if !r.Passed {
			for _, l := range r.Logs {
				fmt.Printf("        %s\n", l)
			}
		}
	}
	fmt.Println()
	fmt.Printf("Results: %d passed, %d failed\n", passed, failed)
}
