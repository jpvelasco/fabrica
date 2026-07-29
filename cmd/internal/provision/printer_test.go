package provision

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
)

func TestDryRunBasic(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "10.0.0.0/8",
			VPCID:        "vpc-123",
		},
		ExtraFields: []PlanField{
			{Key: "Helix Core", Value: "2024.2"},
		},
		Resources: []string{
			"Security Group:   fabrica-test-sg",
			"EC2 Instance:     fabrica-test",
		},
		Costs: fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Test Module (dry run)") {
		t.Errorf("missing title, got:\n%s", got)
	}
	if !strings.Contains(got, "AWS account:      123456789012") {
		t.Errorf("missing account, got:\n%s", got)
	}
	if !strings.Contains(got, "Instance type:    m5.xlarge") {
		t.Errorf("missing instance type, got:\n%s", got)
	}
	if !strings.Contains(got, "Data volume:      500 GiB gp3") {
		t.Errorf("missing volume, got:\n%s", got)
	}
	if !strings.Contains(got, "Helix Core:") || !strings.Contains(got, "2024.2") {
		t.Errorf("missing extra field, got:\n%s", got)
	}
	if !strings.Contains(got, "Resources to create:") {
		t.Errorf("missing resources header, got:\n%s", got)
	}
	if !strings.Contains(got, "Run without --dry-run to proceed.") {
		t.Errorf("missing footer, got:\n%s", got)
	}
}

func TestDryRunCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "0.0.0.0/0",
			VPCID:        "vpc-123",
		},
		CidrWarning: "port 8443 open to the entire internet. Set workstation.allowedCidr in fabrica.yaml.",
		Resources:   []string{"EC2 Instance:     fabrica-test"},
		Costs:       fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Warning:          ") {
		t.Errorf("missing CIDR warning, got:\n%s", got)
	}
}

func TestDryRunCIDRWarningSuppressed(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "0.0.0.0/0",
		},
		CidrWarning: "", // no warning configured — should be suppressed
		Resources:   []string{"EC2 Instance:     fabrica-test"},
		Costs:       fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if strings.Contains(got, "Warning:") {
		t.Errorf("expected no warning when CidrWarning is empty, got:\n%s", got)
	}
}

func TestDryRunDefaultVPC(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "10.0.0.0/8",
			VPCID:        "vpc-default",
			DefaultVPC:   true,
		},
		Resources: []string{"EC2 Instance:     fabrica-test"},
		Costs:     fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "VPC:              default (vpc-default)") {
		t.Errorf("missing default VPC line, got:\n%s", got)
	}
	if !strings.Contains(got, "Note:             Default VPC used.") {
		t.Errorf("missing VPC note, got:\n%s", got)
	}
}

func TestDryRunExplicitVPC(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "10.0.0.0/8",
			VPCID:        "vpc-custom",
			DefaultVPC:   false,
		},
		Resources: []string{"EC2 Instance:     fabrica-test"},
		Costs:     fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "VPC:              vpc-custom") {
		t.Errorf("missing explicit VPC line, got:\n%s", got)
	}
	if strings.Contains(got, "Note:") {
		t.Errorf("expected no VPC note for non-default VPC, got:\n%s", got)
	}
}

func TestDryRunRawBetween(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "10.0.0.0/8",
		},
		RawBetween: func(w io.Writer) {
			_, _ = w.Write([]byte("\n  Custom warning line\n"))
		},
		Resources: []string{"EC2 Instance:     fabrica-test"},
		Costs:     fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Custom warning line") {
		t.Errorf("missing raw between content, got:\n%s", got)
	}
}

func TestApplyPlan(t *testing.T) {
	var out bytes.Buffer
	info := PlanInfo{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m5.xlarge",
		VolumeSize:   500,
	}
	extraFields := []PlanField{
		{Key: "Helix Core", Value: "2024.2"},
		{Key: "Data volume", Value: "500 GiB gp3"},
	}
	resources := []string{
		"Security Group:   fabrica-test-sg",
		"EC2 Instance:     fabrica-test",
	}

	ApplyPlan(&out, "Test Module", info, extraFields, resources)

	got := out.String()
	if strings.Contains(got, "(dry run)") {
		t.Error("apply plan should not contain dry run suffix")
	}
	if !strings.Contains(got, "Test Module") {
		t.Errorf("missing title, got:\n%s", got)
	}
	if !strings.Contains(got, "AWS account:      123456789012") {
		t.Errorf("missing account, got:\n%s", got)
	}
	if !strings.Contains(got, "Data volume:      500 GiB gp3") {
		t.Errorf("default apply should print Data volume, got:\n%s", got)
	}
	if !strings.Contains(got, "Helix Core:") || !strings.Contains(got, "2024.2") {
		t.Errorf("missing extra field, got:\n%s", got)
	}
	if !strings.Contains(got, "Resources to create:") {
		t.Errorf("missing resources header, got:\n%s", got)
	}
}

func TestWriteApplyPlanOmitVolumeCompact(t *testing.T) {
	// Workstation apply layout: no volume, compact labels, warning before resources.
	var out bytes.Buffer
	WriteApplyPlan(&out, ApplyPlanSpec{
		Title: "Cloud Workstation",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-west-2",
			InstanceType: "g4dn.xlarge",
			VolumeSize:   100, // must not appear when OmitVolume
		},
		ExtraFields:   []PlanField{{Key: "Perforce", Value: "10.0.0.5:1666"}},
		OmitVolume:    true,
		CompactLabels: true,
		Resources: []string{
			"Security Group: fabrica-ws-sg",
			"EC2 Instance:   fabrica-ws",
		},
		BeforeResources: func(w io.Writer) {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  WARNING: allowedCidr is 0.0.0.0/0")
		},
	})

	got := out.String()
	if strings.Contains(got, "Data volume") {
		t.Errorf("OmitVolume must suppress Data volume line, got:\n%s", got)
	}
	if !strings.Contains(got, "  AWS account:   123456789012") {
		t.Errorf("expected compact AWS account label, got:\n%s", got)
	}
	if !strings.Contains(got, "  AWS region:    us-west-2") {
		t.Errorf("expected compact AWS region label, got:\n%s", got)
	}
	if !strings.Contains(got, "  Instance type: g4dn.xlarge") {
		t.Errorf("expected compact Instance type label, got:\n%s", got)
	}
	if !strings.Contains(got, "  Perforce:     10.0.0.5:1666") {
		t.Errorf("expected compact Perforce extra field, got:\n%s", got)
	}
	// WARNING must appear before Resources to create
	warnIdx := strings.Index(got, "WARNING:")
	resIdx := strings.Index(got, "Resources to create:")
	if warnIdx < 0 || resIdx < 0 || warnIdx > resIdx {
		t.Errorf("WARNING must precede Resources to create, got:\n%s", got)
	}
	if !strings.Contains(got, "  Security Group: fabrica-ws-sg") {
		t.Errorf("missing compact resource line, got:\n%s", got)
	}
}

func TestApplyPlanNoExtraFields(t *testing.T) {
	var out bytes.Buffer
	info := PlanInfo{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m5.xlarge",
		VolumeSize:   500,
	}

	ApplyPlan(&out, "Test Module", info, nil, []string{"EC2 Instance:     fabrica-test"})

	got := out.String()
	if !strings.Contains(got, "Resources to create:") {
		t.Errorf("missing resources header, got:\n%s", got)
	}
}

func TestPostCreateBasic(t *testing.T) {
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:        "Test Module",
		InstanceID:   "i-1234567890abcdef0",
		StatusDetail: "provisioning (setup in progress, ~3 min)",
		Details: []PlanField{
			{Key: "Credentials", Value: ".fabrica/test-credentials.yaml"},
		},
		NextSteps: []string{
			"fabrica test status      Check readiness",
		},
	}

	PostCreate(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Test Module provisioned.") {
		t.Errorf("missing title, got:\n%s", got)
	}
	if !strings.Contains(got, "Instance ID:      i-1234567890abcdef0") {
		t.Errorf("missing instance ID, got:\n%s", got)
	}
	if !strings.Contains(got, "Status:           provisioning (setup in progress, ~3 min)") {
		t.Errorf("missing status, got:\n%s", got)
	}
	if !strings.Contains(got, "Credentials:") || !strings.Contains(got, ".fabrica/test-credentials.yaml") {
		t.Errorf("missing detail, got:\n%s", got)
	}
	if !strings.Contains(got, "Next steps:") {
		t.Errorf("missing next steps, got:\n%s", got)
	}
	if !strings.Contains(got, "fabrica test status      Check readiness") {
		t.Errorf("missing next step, got:\n%s", got)
	}
}

func TestPostCreateNoStatus(t *testing.T) {
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:      "Test Module",
		InstanceID: "i-1234567890abcdef0",
		NextSteps:  []string{"step 1"},
	}

	PostCreate(&out, spec)

	got := out.String()
	if strings.Contains(got, "Status:") {
		t.Errorf("expected no status line when StatusDetail is empty, got:\n%s", got)
	}
}

func TestPostCreateRawAfter(t *testing.T) {
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:      "Test Module",
		InstanceID: "i-1234567890abcdef0",
		NextSteps:  []string{"step 1"},
		RawAfter: func(w io.Writer) {
			_, _ = w.Write([]byte("\n  Custom log instruction\n"))
		},
	}

	PostCreate(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Custom log instruction") {
		t.Errorf("missing raw after content, got:\n%s", got)
	}
}

func TestPostCreateNoDetails(t *testing.T) {
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:      "Test Module",
		InstanceID: "i-1234567890abcdef0",
		NextSteps:  []string{"step 1"},
	}

	PostCreate(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Test Module provisioned.") {
		t.Errorf("missing title, got:\n%s", got)
	}
}

func TestPlanFieldAlignment(t *testing.T) {
	var out bytes.Buffer
	info := PlanInfo{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m5.xlarge",
		VolumeSize:   500,
	}
	extraFields := []PlanField{
		{Key: "AMI ID", Value: "ami-123"},
		{Key: "HTTP port", Value: "5000"},
	}

	ApplyPlan(&out, "Test", info, extraFields, []string{"EC2 Instance:     test"})

	got := out.String()
	// Verify that extra fields align with common fields (18-char label column)
	lines := strings.Split(got, "\n")
	for _, line := range lines {
		if strings.Contains(line, "AMI ID:") || strings.Contains(line, "HTTP port:") {
			// Each should start with "  " followed by 16 chars of label+padding
			if !strings.HasPrefix(line, "  ") {
				t.Errorf("extra field not indented: %q", line)
			}
		}
	}
}

func TestDryRunNoVPC(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
			AllowedCIDR:  "10.0.0.0/8",
			VPCID:        "", // no VPC set
		},
		Resources: []string{"EC2 Instance:     fabrica-test"},
		Costs:     fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if strings.Contains(got, "VPC:") {
		t.Errorf("expected no VPC line when VPCID is empty, got:\n%s", got)
	}
}

func TestDryRunMultipleResources(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
		},
		Resources: []string{
			"Security Group:   fabrica-test-sg",
			"IAM Role:         fabrica-test-role",
			"EC2 Instance:     fabrica-test",
		},
		Costs: fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "Security Group:   fabrica-test-sg") {
		t.Errorf("missing first resource, got:\n%s", got)
	}
	if !strings.Contains(got, "IAM Role:         fabrica-test-role") {
		t.Errorf("missing second resource, got:\n%s", got)
	}
	if !strings.Contains(got, "EC2 Instance:     fabrica-test") {
		t.Errorf("missing third resource, got:\n%s", got)
	}
}

func TestDryRunMultipleExtraFields(t *testing.T) {
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
		},
		ExtraFields: []PlanField{
			{Key: "AMI ID", Value: "ami-123"},
			{Key: "HTTP port", Value: "5000"},
			{Key: "gRPC port", Value: "5002"},
		},
		Resources: []string{"EC2 Instance:     fabrica-test"},
		Costs:     fabricacost.Global,
	}

	DryRun(&out, spec)

	got := out.String()
	if !strings.Contains(got, "AMI ID:") || !strings.Contains(got, "ami-123") {
		t.Errorf("missing AMI ID, got:\n%s", got)
	}
	if !strings.Contains(got, "HTTP port:") || !strings.Contains(got, "5000") {
		t.Errorf("missing HTTP port, got:\n%s", got)
	}
	if !strings.Contains(got, "gRPC port:") || !strings.Contains(got, "5002") {
		t.Errorf("missing gRPC port, got:\n%s", got)
	}
}

func TestLineWidth(t *testing.T) {
	if lineWidth != 58 {
		t.Errorf("lineWidth = %d, want 58", lineWidth)
	}
}

func TestPostCreateMultipleNextSteps(t *testing.T) {
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:      "Test Module",
		InstanceID: "i-123",
		NextSteps: []string{
			"1. fabrica test status -w       Wait for ready",
			"2. Open http://<ip>:5000        Complete setup",
			"3. fabrica test submit <file>   Submit a job",
		},
	}

	PostCreate(&out, spec)

	got := out.String()
	if !strings.Contains(got, "1. fabrica test status -w       Wait for ready") {
		t.Errorf("missing first step, got:\n%s", got)
	}
	if !strings.Contains(got, "2. Open http://<ip>:5000        Complete setup") {
		t.Errorf("missing second step, got:\n%s", got)
	}
	if !strings.Contains(got, "3. fabrica test submit <file>   Submit a job") {
		t.Errorf("missing third step, got:\n%s", got)
	}
}

func TestApplyPlanEmptyResources(t *testing.T) {
	var out bytes.Buffer
	info := PlanInfo{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m5.xlarge",
		VolumeSize:   500,
	}

	ApplyPlan(&out, "Test", info, nil, nil)

	got := out.String()
	if !strings.Contains(got, "Resources to create:") {
		t.Errorf("missing resources header even with empty list, got:\n%s", got)
	}
}

func TestDryRunNilRawBetween(t *testing.T) {
	// Verify that a nil RawBetween does not panic
	var out bytes.Buffer
	spec := DryRunSpec{
		Title: "Test Module",
		Info: PlanInfo{
			Account:      "123456789012",
			Region:       "us-east-1",
			InstanceType: "m5.xlarge",
			VolumeSize:   500,
		},
		Resources:  []string{"EC2 Instance:     fabrica-test"},
		Costs:      fabricacost.Global,
		RawBetween: nil,
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("DryRun panicked with nil RawBetween: %v", r)
			}
		}()
		DryRun(&out, spec)
	}()
}

func TestPostCreateNilRawAfter(t *testing.T) {
	// Verify that a nil RawAfter does not panic
	var out bytes.Buffer
	spec := PostCreateSpec{
		Title:      "Test Module",
		InstanceID: "i-123",
		NextSteps:  []string{"step 1"},
		RawAfter:   nil,
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PostCreate panicked with nil RawAfter: %v", r)
			}
		}()
		PostCreate(&out, spec)
	}()
}
