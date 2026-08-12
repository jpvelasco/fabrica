package status

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestParseIntProp_Valid(t *testing.T) {
	props := map[string]string{"minSize": "5"}
	got := parseIntProp(props, "minSize")
	if got != 5 {
		t.Errorf("parseIntProp = %d, want 5", got)
	}
}

func TestParseIntProp_Zero(t *testing.T) {
	props := map[string]string{"minSize": "0"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0", got)
	}
}

func TestParseIntProp_Missing(t *testing.T) {
	props := map[string]string{"other": "5"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for missing key", got)
	}
}

func TestParseIntProp_NilMap(t *testing.T) {
	got := parseIntProp(nil, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for nil map", got)
	}
}

func TestParseIntProp_NonNumeric(t *testing.T) {
	props := map[string]string{"minSize": "abc"}
	got := parseIntProp(props, "minSize")
	if got != 0 {
		t.Errorf("parseIntProp = %d, want 0 for non-numeric", got)
	}
}

func TestFormatASGHealth_AllInService(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       2,
		Pending:         0,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "2/2 InService" {
		t.Errorf("formatASGHealth = %q, want 2/2 InService", got)
	}
}

func TestFormatASGHealth_ScaledToZero(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 0,
		InService:       0,
	}
	got := formatASGHealth(info)
	if got != "scaled to 0" {
		t.Errorf("formatASGHealth = %q, want scaled to 0", got)
	}
}

func TestFormatASGHealth_PendingAndTerminating(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 4,
		InService:       2,
		Pending:         1,
		Terminating:     1,
	}
	got := formatASGHealth(info)
	if got != "2/4 InService (1 pending, 1 terminating)" {
		t.Errorf("formatASGHealth = %q, want 2/4 InService (1 pending, 1 terminating)", got)
	}
}

func TestFormatASGHealth_ZeroInService(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       0,
		Pending:         0,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "0/2 InService" {
		t.Errorf("formatASGHealth = %q, want 0/2 InService", got)
	}
}

func TestFormatASGHealth_PendingOnly(t *testing.T) {
	info := cloud.ASGInfo{
		DesiredCapacity: 2,
		InService:       0,
		Pending:         2,
		Terminating:     0,
	}
	got := formatASGHealth(info)
	if got != "0/2 InService (2 pending, 0 terminating)" {
		t.Errorf("formatASGHealth = %q, want 0/2 InService (2 pending, 0 terminating)", got)
	}
}

func TestPrintNotProvisioned_Text(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out, jsonOut: false}
	c.printNotProvisioned()
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned' in output: %s", out.String())
	}
}

func TestPrintNotProvisioned_JSON(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out, jsonOut: true}
	c.printNotProvisioned()
	if !bytes.Contains(out.Bytes(), []byte(`"provisioned": false`)) {
		t.Errorf("expected '\"provisioned\": false' in JSON output: %s", out.String())
	}
}

func TestLineWidthDash(t *testing.T) {
	dash := lineWidthDash()
	if len(dash) != lineWidth {
		t.Errorf("dash length = %d, want %d", len(dash), lineWidth)
	}
}

func TestPrintText_Pending(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out}
	o := StatusOutput{
		Provisioned:     true,
		ASGID:           "asg-agent123",
		MinSize:         0,
		DesiredCapacity: 2,
		MaxSize:         4,
		Status:          "ready",
		ASGHealth:       "0/2 InService (2 pending, 0 terminating)",
		Pending:         2,
	}
	c.printText(o)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("Pending")) {
		t.Errorf("expected 'Pending' in output: %s", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("2")) {
		t.Errorf("expected '2' pending count in output: %s", got)
	}
}

func TestPrintText_Terminating(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out}
	o := StatusOutput{
		Provisioned:     true,
		ASGID:           "asg-agent123",
		MinSize:         0,
		DesiredCapacity: 2,
		MaxSize:         4,
		Status:          "ready",
		ASGHealth:       "1/2 InService (0 pending, 1 terminating)",
		Terminating:     1,
	}
	c.printText(o)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("Terminating")) {
		t.Errorf("expected 'Terminating' in output: %s", got)
	}
}

func TestPrintText_Full(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out}
	o := StatusOutput{
		Provisioned:         true,
		ASGID:               "asg-agent123",
		LaunchTemplate:      "lt-agent123",
		MinSize:             0,
		DesiredCapacity:     2,
		MaxSize:             4,
		InstanceType:        "c7i.xlarge",
		AmiID:               "ami-12345",
		CoordinatorIP:       "10.0.1.50",
		CoordinatorPort:     5000,
		Status:              "ready",
		LiveDesiredCapacity: 2,
		ASGHealth:           "2/2 InService",
	}
	c.printText(o)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("c7i.xlarge")) {
		t.Errorf("expected instance type in output: %s", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("ami-12345")) {
		t.Errorf("expected AMI in output: %s", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("10.0.1.50:5000")) {
		t.Errorf("expected coordinator in output: %s", got)
	}
}

func TestRun_ReadStateError(t *testing.T) {
	c := &command{
		readState: func() (*fabricastate.State, error) {
			return nil, fmt.Errorf("state error")
		},
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for readState failure")
	}
}

func TestRun_NoModule(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := &command{
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned': %s", out.String())
	}
}

func TestRun_NoASG(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := &command{
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned': %s", out.String())
	}
}

func TestRun_DescribeASGError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"minSize": "0", "desiredCapacity": "2", "maxSize": "4"}},
	})
	c := &command{
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		describeASG: func(ctx context.Context, name string) (cloud.ASGInfo, error) {
			return cloud.ASGInfo{}, fmt.Errorf("access denied")
		},
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: describeASG error should not fail run: %v", err)
	}
	// ASG query error is silently ignored — status still shows without live data.
	if bytes.Contains(out.Bytes(), []byte("access denied")) {
		t.Error("describeASG error should not appear in output")
	}
}

func TestRun_JSONOutput(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"minSize": "0", "desiredCapacity": "2", "maxSize": "4"}},
	})
	c := &command{
		out:       &out,
		jsonOut:   true,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"provisioned": true`)) {
		t.Errorf("expected JSON provisioned: %s", out.String())
	}
}

func TestPopulateScaling_WithScalingResources(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::AutoScaling::ScalingPolicy", Identifier: "policy-out", Properties: map[string]string{
				"role":              "agent",
				"scalingPolicy":     "scale-out",
				"scaleOutThreshold": "10",
			}},
			{TypeName: "AWS::AutoScaling::ScalingPolicy", Identifier: "policy-in", Properties: map[string]string{
				"role":             "agent",
				"scalingPolicy":    "scale-in",
				"scaleInThreshold": "2",
				"cooldown":         "120",
			}},
			{TypeName: "AWS::CloudWatch::Alarm", Identifier: "alarm-out", Properties: map[string]string{
				"role":         "agent",
				"scalingAlarm": "scale-out",
				"metricName":   "ASGQueueDepth",
				"metricNs":     "Fabrica/HordeAgents",
			}},
			{TypeName: "AWS::CloudWatch::Alarm", Identifier: "alarm-in", Properties: map[string]string{
				"role":         "agent",
				"scalingAlarm": "scale-in",
			}},
		},
	}

	o := StatusOutput{}
	o.populateScaling(m)

	if !o.ScalingEnabled {
		t.Error("ScalingEnabled should be true")
	}
	if o.ScaleOutPolicyID != "policy-out" {
		t.Errorf("ScaleOutPolicyID = %q, want policy-out", o.ScaleOutPolicyID)
	}
	if o.ScaleInPolicyID != "policy-in" {
		t.Errorf("ScaleInPolicyID = %q, want policy-in", o.ScaleInPolicyID)
	}
	if o.ScaleOutThreshold != "10" {
		t.Errorf("ScaleOutThreshold = %q, want 10", o.ScaleOutThreshold)
	}
	if o.ScaleInThreshold != "2" {
		t.Errorf("ScaleInThreshold = %q, want 2", o.ScaleInThreshold)
	}
	if o.ScaleInCooldown != "120s" {
		t.Errorf("ScaleInCooldown = %q, want 120s", o.ScaleInCooldown)
	}
	if o.MetricName != "ASGQueueDepth" {
		t.Errorf("MetricName = %q, want ASGQueueDepth", o.MetricName)
	}
	if o.MetricNamespace != "Fabrica/HordeAgents" {
		t.Errorf("MetricNamespace = %q, want Fabrica/HordeAgents", o.MetricNamespace)
	}
	if o.ScaleOutAlarmID != "alarm-out" {
		t.Errorf("ScaleOutAlarmID = %q, want alarm-out", o.ScaleOutAlarmID)
	}
	if o.ScaleInAlarmID != "alarm-in" {
		t.Errorf("ScaleInAlarmID = %q, want alarm-in", o.ScaleInAlarmID)
	}
}

func TestPopulateScaling_WithoutScalingResources(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
		},
	}

	o := StatusOutput{}
	o.populateScaling(m)

	if o.ScalingEnabled {
		t.Error("ScalingEnabled should be false when no scaling resources")
	}
}

func TestPopulateScaling_NilModule(t *testing.T) {
	o := StatusOutput{}
	o.populateScaling(nil)

	if o.ScalingEnabled {
		t.Error("ScalingEnabled should be false for nil module")
	}
}

func TestPopulateScaling_NonAgentResources(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::ScalingPolicy", Identifier: "policy-123", Properties: map[string]string{
				"role":              "coordinator",
				"scalingPolicy":     "scale-out",
				"scaleOutThreshold": "10",
			}},
		},
	}

	o := StatusOutput{}
	o.populateScaling(m)

	if o.ScalingEnabled {
		t.Error("ScalingEnabled should be false for non-agent resources")
	}
}

func TestPrintText_ScalingEnabled(t *testing.T) {
	var out bytes.Buffer
	c := &command{out: &out}
	o := StatusOutput{
		Provisioned:       true,
		ASGID:             "asg-agent123",
		MinSize:           0,
		DesiredCapacity:   2,
		MaxSize:           4,
		Status:            "ready",
		ScalingEnabled:    true,
		MetricName:        "ASGQueueDepth",
		MetricNamespace:   "Fabrica/HordeAgents",
		ScaleOutThreshold: "10",
		ScaleInThreshold:  "2",
		ScaleInCooldown:   "120s",
		ScaleOutPolicyID:  "policy-out",
		ScaleInPolicyID:   "policy-in",
		ScaleOutAlarmID:   "alarm-out",
		ScaleInAlarmID:    "alarm-in",
	}
	c.printText(o)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("Queue Scaling")) {
		t.Errorf("expected 'Queue Scaling' in output: %s", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("ASGQueueDepth")) {
		t.Errorf("expected metric name in output: %s", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Fabrica/HordeAgents")) {
		t.Errorf("expected metric namespace in output: %s", got)
	}
}
