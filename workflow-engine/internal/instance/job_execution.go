package instance

// JobExecution is the state read while holding the instance and job locks.
// A job ID identifies one delivery attempt; retries create a new job ID.
type JobExecution struct {
	InstanceStatus InstanceStatus
	StepStatus     StepExecutionStatus
	JobStatus      JobStatus
	WorkerID       string
	LeaseValid     bool
}

func (e JobExecution) Active() bool {
	return (e.InstanceStatus == InstanceStatusActive || e.InstanceStatus == InstanceStatusWaiting) &&
		e.StepStatus == StepExecutionStatusRunning
}

// AcceptsCallback implements execution ordering, not API authentication.
// Kafka delivers UNLOCKED jobs without a polling lease. Polling callbacks
// must belong to the current, unexpired lease. Terminal attempts are no-ops.
func (e JobExecution) AcceptsCallback(workerID string) bool {
	if !e.Active() {
		return false
	}
	return e.JobStatus == JobStatusUnlocked ||
		(e.JobStatus == JobStatusLocked && e.WorkerID == workerID && e.LeaseValid)
}
