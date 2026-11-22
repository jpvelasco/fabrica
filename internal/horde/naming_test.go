package horde

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// acme is a non-default prefix.
var acme = naming.MustResolve("acme")

const (
	acmeNonce       = "0123456789abcdef0123456789abcdef"
	acmeTokenParam  = "/acme/horde/" + acmeNonce + "/service-token"
	acmeTokenARN    = "arn:aws:ssm:us-east-1:123456789012:parameter" + acmeTokenParam
	acmeAgentNameTg = "acme-horde-agent"
)

func TestNewCreatePlanNamesFollowPrefix(t *testing.T) {
	cfg := config.HordeConfig{AmiID: "ami-horde", VPCId: "vpc-1", SubnetId: "subnet-1"}
	plan, err := NewCreatePlan(context.Background(), acme, cfg, "123456789012", "us-east-1", nil, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	got := []string{plan.SGName, plan.InstanceName, plan.RoleName, plan.InstanceProfileName}
	want := []string{"acme-horde-sg", "acme-horde", "acme-horde-role", "acme-horde-profile"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	plan := &CreatePlan{
		Prefix:                acme,
		Account:               "123456789012",
		Region:                "us-east-1",
		RoleName:              "acme-horde-role",
		ServiceTokenParameter: acmeTokenParam,
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	for _, want := range []string{"acme-ssm-output", "acme-horde-service-token", acmeTokenARN} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("role document missing %q", want)
		}
	}
	if strings.Contains(string(raw), "fabrica-") {
		t.Errorf("role document still carries a fabrica name: %s", raw)
	}
}

func TestServiceTokenFollowsPrefix(t *testing.T) {
	name, err := ServiceTokenParameterFor(acme, acmeNonce)
	if err != nil || name != acmeTokenParam {
		t.Fatalf("ServiceTokenParameterFor = %q, %v; want %q", name, err, acmeTokenParam)
	}
	if !IsServiceTokenName(acmeTokenParam) {
		t.Errorf("IsServiceTokenName rejects the acme token name %q", acmeTokenParam)
	}
	if got := ServiceTokenPolicyName(acme); got != "acme-horde-service-token" {
		t.Errorf("ServiceTokenPolicyName = %q", got)
	}
	if _, err := ServiceTokenParameterFor(acme, "not-a-nonce"); err == nil {
		t.Error("ServiceTokenParameterFor accepted a malformed nonce")
	}
}

func TestNewAgentsCreatePlanNamesFollowPrefix(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 10,
			ScaleInThreshold:  2,
			ScaleInCooldown:   120,
		},
	}
	coord := CoordinatorRef{PrivateIP: "10.0.1.10", Port: 5000, SGID: "sg-coord", VPCID: "vpc-1", SubnetID: "subnet-1"}
	plan, err := NewAgentsCreatePlan(context.Background(), acme, cfg, coord, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("NewAgentsCreatePlan: %v", err)
	}
	got := []string{
		plan.SGName, plan.RoleName, plan.InstanceProfileName, plan.LaunchTemplateName, plan.ASGName,
		plan.ScaleOutAlarmName, plan.ScaleInAlarmName, plan.ScaleOutPolicyName, plan.ScaleInPolicyName,
	}
	want := []string{
		"acme-horde-agents-sg", "acme-horde-agents-role", "acme-horde-agents-profile", "acme-horde-agents-lt", "acme-horde-agents-asg",
		"acme-horde-agents-scale-out", "acme-horde-agents-scale-in", "acme-horde-agents-scale-out-policy", "acme-horde-agents-scale-in-policy",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}

	ltID := "lt-0123"
	asg, err := ASGDesiredState(plan, ltID)
	if err != nil {
		t.Fatalf("ASGDesiredState: %v", err)
	}
	ltDoc, err := LaunchTemplateDesiredState(plan, "sg-agents", "userdata")
	if err != nil {
		t.Fatalf("LaunchTemplateDesiredState: %v", err)
	}
	tagSpecs, err := json.Marshal(agentTagSpecifications(plan))
	if err != nil {
		t.Fatalf("marshal tag specifications: %v", err)
	}
	alarm, err := ScaleOutAlarmDesiredState(plan, "arn:aws:autoscaling:policy")
	if err != nil {
		t.Fatalf("ScaleOutAlarmDesiredState: %v", err)
	}
	for _, doc := range []string{string(asg), string(ltDoc), string(tagSpecs)} {
		if !strings.Contains(doc, acmeAgentNameTg) {
			t.Errorf("agent document lacks the %s Name tag: %s", acmeAgentNameTg, doc)
		}
	}
	if !strings.Contains(string(asg), "acme-horde-agents-asg") || !strings.Contains(string(ltDoc), "acme-horde-agents-lt") {
		t.Errorf("ASG or launch template is not acme-named: %s %s", asg, ltDoc)
	}
	if !strings.Contains(string(alarm), "acme-horde-agents-scale-out") {
		t.Errorf("scale-out alarm is not acme-named: %s", alarm)
	}
	for _, doc := range []string{string(asg), string(ltDoc), string(alarm)} {
		if strings.Contains(doc, "fabrica-") {
			t.Errorf("agent document still carries a fabrica name: %s", doc)
		}
	}
}
