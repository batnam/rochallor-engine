package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	logDir = "logs"
	mu     sync.Mutex

	// Track last seen state per instance for delta logging
	lastStatuses      = make(map[string]string)
	lastStepSets      = make(map[string]map[string]bool)
	instanceWorkflows = make(map[string]string)
)

// SetLogDir sets the directory where audit logs will be stored.
func SetLogDir(dir string) {
	mu.Lock()
	defer mu.Unlock()
	logDir = dir
}

// LogEvent appends a lifecycle event to the per-instance audit log.
// It is the low-level primitive for writing to the file.
func LogEvent(instanceID string, eventType string, message string) {
	if instanceID == "" {
		return
	}

	mu.Lock()
	wfName := instanceWorkflows[instanceID]
	mu.Unlock()

	var filename string
	if wfName != "" {
		filename = filepath.Join(logDir, fmt.Sprintf("audit-%s-%s.log", wfName, instanceID))
	} else {
		filename = filepath.Join(logDir, fmt.Sprintf("audit-%s.log", instanceID))
	}

	// Ensure directory exists
	if err := os.MkdirAll(logDir, 0755); err != nil {
		fmt.Printf("ERROR: failed to create log directory %s: %v\n", logDir, err)
		return
	}

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("ERROR: failed to open audit log %s: %v\n", filename, err)
		return
	}
	defer f.Close()

	timestamp := time.Now().Format(time.RFC3339Nano)

	prefix := ""
	if eventType == "ERROR" || (eventType == "INSTANCE_TERMINAL" && !strings.Contains(message, "COMPLETED")) {
		prefix = "!!! "
	}

	indent := ""
	if eventType == "STEP_ENTER" || eventType == "STEP_COMPLETE" || eventType == "LIFECYCLE_UPDATE" {
		indent = "  "
	}

	line := fmt.Sprintf("[%s] [%s] %s%s%s\n", timestamp, eventType, prefix, indent, message)
	if _, err := f.WriteString(line); err != nil {
		fmt.Printf("ERROR: failed to write to audit log %s: %v\n", filename, err)
	}
}

// LogInstanceStarted logs the start of a workflow instance and initializes its tracking state.
func LogInstanceStarted(workflowName string, instanceID string, variables map[string]any) {
	mu.Lock()
	lastStatuses[instanceID] = "STARTING"
	lastStepSets[instanceID] = make(map[string]bool)
	instanceWorkflows[instanceID] = workflowName
	mu.Unlock()

	LogEvent(instanceID, "INSTANCE_START", fmt.Sprintf("Workflow: %s | Started with variables: %v", workflowName, variables))
}

// AuditInstance examines the instance state and logs any deltas since the last call.
// This is the primary way to log lifecycle updates during polling.
func AuditInstance(inst Instance) {
	mu.Lock()
	id := inst.ID
	if id == "" {
		mu.Unlock()
		return
	}

	// 1. Log status change
	lastStatus := lastStatuses[id]
	if string(inst.Status) != lastStatus {
		msg := fmt.Sprintf("Status changed to %s", inst.Status)
		if inst.FailureReason != "" {
			msg += fmt.Sprintf(" | Failure: %s", inst.FailureReason)
		}
		mu.Unlock()
		LogEvent(id, "LIFECYCLE_UPDATE", msg)
		mu.Lock()
		lastStatuses[id] = string(inst.Status)
	}

	// 2. Log step deltas
	lastSteps := lastStepSets[id]
	if lastSteps == nil {
		lastSteps = make(map[string]bool)
	}

	currentSteps := make(map[string]bool)
	for _, stepID := range inst.CurrentStepIds {
		currentSteps[stepID] = true
		if !lastSteps[stepID] {
			// New step entered
			mu.Unlock()
			LogEvent(id, "STEP_ENTER", fmt.Sprintf("Step '%s' entered", stepID))
			mu.Lock()
		}
	}

	// Steps completed (present in last but not in current)
	var completed []string
	for stepID := range lastSteps {
		if !currentSteps[stepID] {
			completed = append(completed, stepID)
		}
	}
	// Sort for deterministic log order if multiple steps completed
	sort.Strings(completed)
	for _, stepID := range completed {
		mu.Unlock()
		LogEvent(id, "STEP_COMPLETE", fmt.Sprintf("Step '%s' completed. Current variables: %v", stepID, inst.Variables))
		mu.Lock()
	}

	lastStepSets[id] = currentSteps

	// 3. Log terminal state
	switch inst.Status {
	case "COMPLETED", "FAILED", "CANCELLED":
		mu.Unlock()
		LogEvent(id, "INSTANCE_TERMINAL", fmt.Sprintf("Final Status: %s", inst.Status))
		mu.Lock()
		// Clean up state to prevent memory leak
		delete(lastStatuses, id)
		delete(lastStepSets, id)
		delete(instanceWorkflows, id)
	}
	mu.Unlock()
}

// LogStepEntered is now a legacy helper, AuditInstance is preferred.
func LogStepEntered(instanceID string, stepID string) {
	LogEvent(instanceID, "STEP_ENTER", fmt.Sprintf("Step '%s' entered", stepID))
}

// LogStepCompleted is now a legacy helper, AuditInstance is preferred.
func LogStepCompleted(instanceID string, stepID string, variables map[string]any) {
	LogEvent(instanceID, "STEP_COMPLETE", fmt.Sprintf("Step '%s' completed. Updated variables: %v", stepID, variables))
}

// LogInstanceTerminal is now a legacy helper, AuditInstance is preferred.
func LogInstanceTerminal(instanceID string, status string, reason string) {
	msg := fmt.Sprintf("Status: %s", status)
	if reason != "" {
		msg += fmt.Sprintf(" | Reason: %s", reason)
	}
	LogEvent(instanceID, "INSTANCE_TERMINAL", msg)
}
