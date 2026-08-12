package destroy

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestNewTeardownWiring(t *testing.T) {
	rt := globals.Runtime{Config: config.Defaults()}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "horde" {
		t.Errorf("ModuleName = %q, want horde", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", tc.Spec.Verb)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm must be true (set by shared NewTeardown)")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes must be true (set by shared NewTeardown)")
	}
	if tc.ReadState == nil {
		t.Error("ReadState must be wired")
	}
	if tc.WriteState == nil {
		t.Error("WriteState must be wired")
	}
	if tc.Confirm == nil {
		t.Error("Confirm must be wired")
	}
	// Without a provider, delete seams are nil.
	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when provider is nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when provider is nil")
	}
}

func TestNewTeardownWithProvider(t *testing.T) {
	rt := globals.Runtime{
		Config:   config.Defaults(),
		Provider: &testutil.TestProvider{},
	}
	tc := NewTeardown(rt, io.Discard)

	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is non-nil")
	}
}

func TestNewTeardownSpecStrings(t *testing.T) {
	tc := teardown.Command{Spec: spec}

	if tc.Spec.ModuleName != "horde" {
		t.Errorf("ModuleName = %q, want horde", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", tc.Spec.Verb)
	}
	if tc.Spec.VersionLabel != "AMI ID" {
		t.Errorf("VersionLabel = %q, want AMI ID", tc.Spec.VersionLabel)
	}
	if tc.Spec.Title != "Unreal Horde build coordinator" {
		t.Errorf("Title = %q, want Unreal Horde build coordinator", tc.Spec.Title)
	}
	for _, field := range []struct{ name, value string }{
		{"NotProvisioned", tc.Spec.NotProvisioned},
		{"PlanHeader", tc.Spec.PlanHeader},
		{"DryRunHeader", tc.Spec.DryRunHeader},
		{"Irreversible", tc.Spec.Irreversible},
		{"SuccessMessage", tc.Spec.SuccessMessage},
	} {
		if field.value == "" {
			t.Errorf("%s must not be empty", field.name)
		}
	}
}

func TestHordeResourceOrder_ScalingBeforeASG(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord"},
			{TypeName: "AWS::IAM::Role", Identifier: "role-coord"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "profile-coord"},
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "profile-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::IAM::Role", Identifier: "role-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::AutoScaling::ScalingPolicy", Identifier: "policy-out", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::AutoScaling::ScalingPolicy", Identifier: "policy-in", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::CloudWatch::Alarm", Identifier: "alarm-out", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::CloudWatch::Alarm", Identifier: "alarm-in", Properties: map[string]string{"role": "agent"}},
		},
	}

	resources := hordeResourceOrder(m)
	if len(resources) == 0 {
		t.Fatal("hordeResourceOrder returned empty list")
	}

	// Build index of each resource by identifier for order assertions.
	type entry struct {
		typ string
		idx int
	}
	byID := make(map[string]entry)
	for i, r := range resources {
		byID[r.Identifier] = entry{r.TypeName, i}
	}

	// Scaling policies must come before ASG.
	for _, id := range []string{"policy-out", "policy-in"} {
		e, ok := byID[id]
		if !ok {
			t.Errorf("ScalingPolicy %s not found in destroy order", id)
			continue
		}
		asg, okAsg := byID["asg-agent"]
		if !okAsg {
			t.Errorf("ASG not found in destroy order")
			continue
		}
		if e.idx >= asg.idx {
			t.Errorf("ScalingPolicy %s (idx %d) must be deleted before ASG (idx %d)", id, e.idx, asg.idx)
		}
	}

	// Alarms must come before ASG.
	for _, id := range []string{"alarm-out", "alarm-in"} {
		e, ok := byID[id]
		if !ok {
			t.Errorf("Alarm %s not found in destroy order", id)
			continue
		}
		asg, okAsg := byID["asg-agent"]
		if !okAsg {
			t.Errorf("ASG not found in destroy order")
			continue
		}
		if e.idx >= asg.idx {
			t.Errorf("Alarm %s (idx %d) must be deleted before ASG (idx %d)", id, e.idx, asg.idx)
		}
	}

	// ASG must come before LaunchTemplate.
	asg, okAsg := byID["asg-agent"]
	lt, okLt := byID["lt-agent"]
	if okAsg && okLt {
		if asg.idx >= lt.idx {
			t.Errorf("ASG (idx %d) must be deleted before LaunchTemplate (idx %d)", asg.idx, lt.idx)
		}
	}

	// Instance must come after agent resources (coordinator is destroyed after agents).
	inst, okInst := byID["i-coord"]
	if okInst && okAsg {
		if inst.idx < asg.idx {
			t.Errorf("Instance (idx %d) must be deleted after ASG (idx %d)", inst.idx, asg.idx)
		}
	}
}

func TestHordeResourceOrder_NoScalingResources(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord"},
			{TypeName: "AWS::IAM::Role", Identifier: "role-coord"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "profile-coord"},
		},
	}

	resources := hordeResourceOrder(m)
	// Should return all 4 coordinator resources.
	if len(resources) != 4 {
		t.Errorf("want 4 resources, got %d", len(resources))
	}

	// Instance should be first (destroyed before IAM/SG).
	if len(resources) > 0 && resources[0].TypeName != "AWS::EC2::Instance" {
		t.Errorf("first resource = %s, want AWS::EC2::Instance", resources[0].TypeName)
	}
}
