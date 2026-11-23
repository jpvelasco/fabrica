package horde

import "fmt"

// Step states and outcomes reported in GET /api/v1/jobs/{id} batches on Horde 5.8.
const (
	JobStepStateCompleted = "Completed"
	JobStepStateSkipped   = "Skipped"
	JobStepStateAborted   = "Aborted"

	JobStepOutcomeSuccess  = "Success"
	JobStepOutcomeWarnings = "Warnings"
	JobStepOutcomeFailure  = "Failure"

	JobBatchErrorNone           = "None"
	JobBatchErrorNoLongerNeeded = "NoLongerNeeded"
	JobBatchErrorNoAgentsInPool = "NoAgentsInPool"
	JobBatchErrorNoAgentsOnline = "NoAgentsOnline"
)

// MaxListedFailures caps how many failing items an error message names.
const MaxListedFailures = 10

// JobStatus is the part of GET /api/v1/jobs/{id} that submit --wait reads.
// State only says the job stopped; Batches say whether it succeeded.
type JobStatus struct {
	State   string     `json:"state"`
	Batches []JobBatch `json:"batches"`
}

// JobBatch is one agent's share of a job. Error is empty when Horde omits it.
type JobBatch struct {
	State   string    `json:"state"`
	Error   string    `json:"error,omitempty"`
	AgentID string    `json:"agentId,omitempty"`
	Steps   []JobStep `json:"steps"`
}

// JobStep is one BuildGraph step inside a batch.
type JobStep struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
}

// JobOutcome is the verdict for a job that reached Complete.
type JobOutcome struct {
	// Warnings lists steps that finished with warnings; they do not fail the job.
	Warnings []string
	// Failures lists the first MaxListedFailures failing items in job order.
	Failures []string
	// FailureCount is the total number of failing items, including unlisted ones.
	FailureCount int
	// AgentsMissing is set when a batch found no enrolled or online agent.
	AgentsMissing bool
}

// Failed reports whether the job has any failing item.
func (o JobOutcome) Failed() bool {
	return o.FailureCount > 0
}

// EvaluateCompleted judges a job that reached Complete. The job succeeds when
// every batch error is None or NoLongerNeeded and every step completed with a
// Success or Warnings outcome. Callers must check State first.
func EvaluateCompleted(s JobStatus) JobOutcome {
	var o JobOutcome
	for _, b := range s.Batches {
		if b.Error != "" && b.Error != JobBatchErrorNone && b.Error != JobBatchErrorNoLongerNeeded {
			o.addFailure(batchFailure(b))
			if b.Error == JobBatchErrorNoAgentsInPool || b.Error == JobBatchErrorNoAgentsOnline {
				o.AgentsMissing = true
			}
		}
		for _, st := range b.Steps {
			switch {
			case st.State != JobStepStateCompleted,
				st.Outcome != JobStepOutcomeSuccess && st.Outcome != JobStepOutcomeWarnings:
				o.addFailure(fmt.Sprintf("step %s: %s/%s", st.Name, st.State, st.Outcome))
			case st.Outcome == JobStepOutcomeWarnings:
				o.Warnings = append(o.Warnings, fmt.Sprintf("step %s finished with warnings", st.Name))
			}
		}
	}
	return o
}

func (o *JobOutcome) addFailure(item string) {
	o.FailureCount++
	if len(o.Failures) < MaxListedFailures {
		o.Failures = append(o.Failures, item)
	}
}

func batchFailure(b JobBatch) string {
	if b.AgentID == "" {
		return fmt.Sprintf("batch: %s", b.Error)
	}
	return fmt.Sprintf("batch on agent %s: %s", b.AgentID, b.Error)
}
