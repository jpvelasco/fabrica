package horde

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNewAgentsCreatePlanReferencedProfileUsesARNName(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		VPCID:           "vpc-test",
		SubnetID:        "subnet-test",
		InstanceProfile: "arn:aws:iam::123456789012:instance-profile/platform/workload",
	}
	plan, err := NewAgentsCreatePlan(context.Background(), naming.Default, cfg, CoordinatorRef{PrivateIP: "10.0.1.10", Port: 5000, SGID: "sg-coord"}, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("NewAgentsCreatePlan: %v", err)
	}
	if !plan.Profile.External() || plan.Profile.Name != "workload" {
		t.Errorf("Profile = %+v, want external Ref named workload", plan.Profile)
	}
	if plan.InstanceProfileName != "workload" {
		t.Errorf("InstanceProfileName = %q, want the referenced name", plan.InstanceProfileName)
	}
}

func TestNewAgentsCreatePlanRejectsMalformedProfile(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		VPCID:           "vpc-test",
		SubnetID:        "subnet-test",
		InstanceProfile: "arn:aws:iam::123456789012:role/workload",
	}
	_, err := NewAgentsCreatePlan(context.Background(), naming.Default, cfg, CoordinatorRef{PrivateIP: "10.0.1.10", Port: 5000, SGID: "sg-coord"}, "123456789012", "us-east-1", nil)
	if err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Fatalf("NewAgentsCreatePlan error = %v, want an instanceProfile error", err)
	}
}

func TestLaunchTemplateAttachesReferencedProfile(t *testing.T) {
	plan := newTestAgentsPlan()
	plan.Profile = instanceprofile.Ref{Name: "workload"}
	plan.InstanceProfileName = "workload"

	ds, err := LaunchTemplateDesiredState(plan, "sg-agent123", "dXNlci1kYXRh")
	if err != nil {
		t.Fatalf("LaunchTemplateDesiredState: %v", err)
	}
	var doc struct {
		LaunchTemplateData struct {
			IamInstanceProfile struct {
				Name string `json:"Name"`
			} `json:"IamInstanceProfile"`
		} `json:"LaunchTemplateData"`
	}
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := doc.LaunchTemplateData.IamInstanceProfile.Name; got != "workload" {
		t.Errorf("launch template IamInstanceProfile.Name = %q, want the referenced profile", got)
	}
}
