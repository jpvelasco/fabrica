package statusreport

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestBuildStatusReport_EmptyState(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-east-1",
				},
			},
		},
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if report.Summary.ModuleCount != 0 {
		t.Errorf("moduleCount = %d, want 0", report.Summary.ModuleCount)
	}
	if report.Backend.BucketExists != "unknown" {
		t.Errorf("bucketExists = %q, want unknown", report.Backend.BucketExists)
	}
}

func TestBuildStatusReport_WithModules(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
		{
			Name:   "horde",
			Status: "provisioning",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-456"},
			},
		},
	}
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-east-1",
				},
			},
		},
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if report.Summary.ModuleCount != 2 {
		t.Errorf("moduleCount = %d, want 2", report.Summary.ModuleCount)
	}
	if report.Summary.Healthy != 1 {
		t.Errorf("healthy = %d, want 1", report.Summary.Healthy)
	}
	if report.Summary.Provisioning != 1 {
		t.Errorf("provisioning = %d, want 1", report.Summary.Provisioning)
	}
	if report.Summary.ResourceCount != 3 {
		t.Errorf("resourceCount = %d, want 3", report.Summary.ResourceCount)
	}
}

func TestBuildStatusReport_ModuleFields(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:    "perforce",
			Status:  "ready",
			Version: "p4-2024.1",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
	}
	rt := globals.Runtime{Config: &config.Config{}}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if len(report.Modules) != 1 {
		t.Fatalf("modules count = %d, want 1", len(report.Modules))
	}
	m := report.Modules[0]
	if m.Name != "perforce" {
		t.Errorf("name = %q, want perforce", m.Name)
	}
	if m.Status != "ready" {
		t.Errorf("status = %q, want ready", m.Status)
	}
	if m.Version != "p4-2024.1" {
		t.Errorf("version = %q, want p4-2024.1", m.Version)
	}
	if m.ResourceCount != 2 {
		t.Errorf("resourceCount = %d, want 2", m.ResourceCount)
	}
	if m.SGID != "sg-123" {
		t.Errorf("sgId = %q, want sg-123", m.SGID)
	}
	if m.InstanceID != "i-abc" {
		t.Errorf("instanceId = %q, want i-abc", m.InstanceID)
	}
}

func TestSummaryLine(t *testing.T) {
	tests := []struct {
		name    string
		summary StatusSummary
		wantSub string
	}{
		{
			name:    "empty",
			summary: StatusSummary{},
			wantSub: "No modules provisioned",
		},
		{
			name: "one module",
			summary: StatusSummary{
				ModuleCount:   1,
				ResourceCount: 2,
				Healthy:       1,
			},
			wantSub: "1 module",
		},
		{
			name: "multiple modules",
			summary: StatusSummary{
				ModuleCount:   3,
				ResourceCount: 7,
				Healthy:       2,
				Provisioning:  1,
			},
			wantSub: "3 modules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummaryLine(tt.summary)
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("SummaryLine() = %q, want to contain %q", got, tt.wantSub)
			}
		})
	}
}

func TestNextSteps(t *testing.T) {
	modules := []StatusModule{
		{Name: "perforce", Status: "ready"},
		{Name: "horde", Status: "provisioning"},
	}

	steps := NextSteps(modules)

	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if !strings.Contains(steps[0], "horde") {
		t.Errorf("step = %q, want horde provisioning step", steps[0])
	}
}

func TestNextSteps_None(t *testing.T) {
	modules := []StatusModule{
		{Name: "perforce", Status: "ready"},
	}

	steps := NextSteps(modules)

	if len(steps) != 0 {
		t.Errorf("got %d steps, want 0", len(steps))
	}
}

func TestParseInstanceState(t *testing.T) {
	raw := []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.1.5"}`)
	state, ip := parseInstanceState(raw)
	if state != "running" {
		t.Errorf("state = %q, want running", state)
	}
	if ip != "10.0.1.5" {
		t.Errorf("ip = %q, want 10.0.1.5", ip)
	}
}

func TestParseInstanceState_Empty(t *testing.T) {
	state, ip := parseInstanceState(nil)
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
	if ip != "" {
		t.Errorf("ip = %q, want empty", ip)
	}
}

func TestYesNo(t *testing.T) {
	if yesNo(true, nil) != "yes" {
		t.Error("yesNo(true, nil) should be yes")
	}
	if yesNo(false, nil) != "no" {
		t.Error("yesNo(false, nil) should be no")
	}
	if yesNo(false, context.DeadlineExceeded) != "unknown" {
		t.Error("yesNo(false, err) should be unknown")
	}
}
