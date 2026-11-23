package horde

import (
	"fmt"
	"reflect"
	"testing"
)

func okStep(name string) JobStep {
	return JobStep{Name: name, State: JobStepStateCompleted, Outcome: JobStepOutcomeSuccess}
}

func TestEvaluateCompleted(t *testing.T) {
	tests := []struct {
		name         string
		status       JobStatus
		wantFailed   bool
		wantWarnings []string
		wantFailures []string
		wantAgents   bool
	}{
		{
			name:   "all steps succeeded",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{State: JobStateComplete, Error: JobBatchErrorNone, Steps: []JobStep{okStep("Compile"), okStep("Cook")}}}},
		},
		{
			name: "warnings are not failures",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: []JobStep{
				okStep("Compile"),
				{Name: "Cook", State: JobStepStateCompleted, Outcome: JobStepOutcomeWarnings},
			}}}},
			wantWarnings: []string{"step Cook finished with warnings"},
		},
		{
			name: "failed step",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: []JobStep{
				{Name: "Compile", State: JobStepStateCompleted, Outcome: JobStepOutcomeFailure},
			}}}},
			wantFailed:   true,
			wantFailures: []string{"step Compile: Completed/Failure"},
		},
		{
			name: "skipped step is a failure",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: []JobStep{
				{Name: "Cook", State: JobStepStateSkipped},
			}}}},
			wantFailed:   true,
			wantFailures: []string{"step Cook: Skipped/"},
		},
		{
			name: "aborted step is a failure",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: []JobStep{
				{Name: "Cook", State: JobStepStateAborted, Outcome: JobStepOutcomeFailure},
			}}}},
			wantFailed:   true,
			wantFailures: []string{"step Cook: Aborted/Failure"},
		},
		{
			name: "step that completed without an outcome is a failure",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: []JobStep{
				{Name: "Cook", State: JobStepStateCompleted},
			}}}},
			wantFailed:   true,
			wantFailures: []string{"step Cook: Completed/"},
		},
		{
			name: "batch error NoAgentsOnline names the agent and asks for the agents hint",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{
				Error: JobBatchErrorNoAgentsOnline, AgentType: "Linux", AgentID: "agent-1",
			}}},
			wantFailed:   true,
			wantFailures: []string{"batch on Linux (agent agent-1): NoAgentsOnline"},
			wantAgents:   true,
		},
		{
			name: "batch error NoAgentsInPool without an agent ID",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{
				Error: JobBatchErrorNoAgentsInPool,
			}}},
			wantFailed:   true,
			wantFailures: []string{"batch: NoAgentsInPool"},
			wantAgents:   true,
		},
		{
			name: "other batch errors fail without the agents hint",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{
				Error: "ExecutionError", AgentID: "agent-2",
			}}},
			wantFailed:   true,
			wantFailures: []string{"batch (agent agent-2): ExecutionError"},
		},
		{
			name: "batch error NoLongerNeeded is not a failure",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{
				Error: JobBatchErrorNoLongerNeeded, Steps: []JobStep{okStep("Compile")},
			}}},
		},
		{
			name:   "null batches with Complete succeed",
			status: JobStatus{State: JobStateComplete},
		},
		{
			name: "batch with no error and no steps succeeds",
			status: JobStatus{State: JobStateComplete, Batches: []JobBatch{{
				State: JobStateComplete, Error: JobBatchErrorNone,
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateCompleted(tt.status)
			if got.Failed() != tt.wantFailed {
				t.Errorf("Failed() = %v, want %v (failures %v)", got.Failed(), tt.wantFailed, got.Failures)
			}
			if !reflect.DeepEqual(got.Warnings, tt.wantWarnings) {
				t.Errorf("Warnings = %v, want %v", got.Warnings, tt.wantWarnings)
			}
			if !reflect.DeepEqual(got.Failures, tt.wantFailures) {
				t.Errorf("Failures = %v, want %v", got.Failures, tt.wantFailures)
			}
			if got.AgentsMissing != tt.wantAgents {
				t.Errorf("AgentsMissing = %v, want %v", got.AgentsMissing, tt.wantAgents)
			}
		})
	}
}

func TestEvaluateCompletedCapsListedFailures(t *testing.T) {
	var steps []JobStep
	for i := range 12 {
		steps = append(steps, JobStep{Name: fmt.Sprintf("s%02d", i), State: JobStepStateAborted})
	}
	got := EvaluateCompleted(JobStatus{State: JobStateComplete, Batches: []JobBatch{{Steps: steps}}})

	if got.FailureCount != 12 {
		t.Errorf("FailureCount = %d, want 12", got.FailureCount)
	}
	if len(got.Failures) != MaxListedFailures {
		t.Errorf("len(Failures) = %d, want %d", len(got.Failures), MaxListedFailures)
	}
	if MaxListedFailures != 10 {
		t.Errorf("MaxListedFailures = %d, want 10", MaxListedFailures)
	}
	if got.Failures[0] != "step s00: Aborted/" {
		t.Errorf("first failure = %q, want the first step in order", got.Failures[0])
	}
}
