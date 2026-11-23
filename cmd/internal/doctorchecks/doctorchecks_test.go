package doctorchecks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/egress"
)

func newTestRuntime(bucket, table, region string) globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    region,
				},
			},
			State: config.State{
				Bucket: bucket,
				Table:  table,
			},
		},
	}
}

func TestRunChecks_Count(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)
	if len(checks) != 10 {
		t.Fatalf("expected 10 checks, got %d", len(checks))
	}
}

func TestRunChecks_Names(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)

	expectedNames := []string{
		"Go version",
		"Fabrica version",
		"AWS credentials",
		"Region",
		"S3 state bucket",
		"DynamoDB lock table",
		"Perforce CIDR",
	}

	for i, want := range expectedNames {
		if checks[i].Name != want {
			t.Errorf("checks[%d].Name = %q, want %q", i, checks[i].Name, want)
		}
	}
}

func TestRunChecks_NilBackend_Warnings(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "warning" {
				t.Errorf("S3 state bucket status = %q, want warning", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "warning" {
				t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
			}
		}
	}
}

func TestRunChecks_NoConfig(t *testing.T) {
	rt := globals.Runtime{}
	checks := RunChecks(context.Background(), rt, nil)

	if len(checks) != 10 {
		t.Fatalf("expected 10 checks, got %d", len(checks))
	}

	// Region should be warning when config is nil
	if checks[3].Status != "warning" {
		t.Errorf("region check status = %q, want warning", checks[3].Status)
	}

	// Backend checks should be warnings when config is nil
	for _, c := range checks {
		if c.Name == "S3 state bucket" && c.Status != "warning" {
			t.Errorf("S3 state bucket status = %q, want warning", c.Status)
		}
		if c.Name == "DynamoDB lock table" && c.Status != "warning" {
			t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
		}
	}
}

func TestRunChecks_NoProvider(t *testing.T) {
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
	checks := RunChecks(context.Background(), rt, nil)

	// Credentials should be warning when no provider
	if checks[2].Status != "warning" {
		t.Errorf("credentials check status = %q, want warning", checks[2].Status)
	}

	// Backend checks should be warnings when no bucket configured
	for _, c := range checks {
		if c.Name == "S3 state bucket" && c.Status != "warning" {
			t.Errorf("S3 state bucket status = %q, want warning", c.Status)
		}
		if c.Name == "DynamoDB lock table" && c.Status != "warning" {
			t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
		}
	}
}

func TestRunChecks_BackendExists(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketExists: true,
		tableExists:  true,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "ok" {
				t.Errorf("S3 state bucket status = %q, want ok", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "ok" {
				t.Errorf("DynamoDB lock table status = %q, want ok", c.Status)
			}
		}
	}
}

func TestRunChecks_BackendNotFound(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketExists: false,
		tableExists:  false,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "warning" {
				t.Errorf("S3 state bucket status = %q, want warning", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "warning" {
				t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
			}
		}
	}
}

func TestRunChecks_BackendError(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketErr: true,
		tableErr:  true,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "fail" {
				t.Errorf("S3 state bucket status = %q, want fail", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "fail" {
				t.Errorf("DynamoDB lock table status = %q, want fail", c.Status)
			}
		}
	}
}

func TestCheckGo(t *testing.T) {
	c := checkGo()
	if c.Name != "Go version" || c.Status != "ok" || c.Message == "" {
		t.Errorf("checkGo = %+v, want name=Go version, status=ok, non-empty message", c)
	}
}

func TestCheckVersion(t *testing.T) {
	c := checkVersion()
	if c.Name != "Fabrica version" || c.Status != "ok" {
		t.Errorf("checkVersion = %+v, want name=Fabrica version, status=ok", c)
	}
}

func TestCheckCreds_NoProvider(t *testing.T) {
	c := checkCreds(context.Background(), globals.Runtime{})
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckCreds_Error(t *testing.T) {
	prov := &fakeProvider{identityErr: context.DeadlineExceeded}
	rt := globals.Runtime{Provider: prov}
	c := checkCreds(context.Background(), rt)
	if c.Status != "fail" {
		t.Errorf("status = %q, want fail", c.Status)
	}
}

func TestCheckCreds_Ok(t *testing.T) {
	prov := &fakeProvider{account: "123456789012", arn: "arn:aws:iam::123456789012:user/test", region: "us-east-1"}
	rt := globals.Runtime{Provider: prov}
	c := checkCreds(context.Background(), rt)
	if c.Status != "ok" {
		t.Errorf("status = %q, want ok", c.Status)
	}
}

func TestCheckPerforceCIDR(t *testing.T) {
	tests := []struct {
		name       string
		rt         globals.Runtime
		wantStatus string
		wantMsg    string
	}{
		{
			name:       "nil config",
			rt:         globals.Runtime{},
			wantStatus: "ok",
			wantMsg:    "not configured",
		},
		{
			name:       "perforce empty",
			rt:         globals.Runtime{Config: &config.Config{}},
			wantStatus: "ok",
			wantMsg:    "not configured",
		},
		{
			name: "perforce configured without allowedCidr",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge"},
			}},
			wantStatus: "warning",
			wantMsg:    "10.0.0.0/8",
		},
		{
			name: "perforce configured with allowedCidr",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge", AllowedCIDR: "172.31.0.0/16"},
			}},
			wantStatus: "ok",
			wantMsg:    "172.31.0.0/16",
		},
		{
			name: "perforce configured via vpc keys only, allowedCidr set",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{VPCId: "vpc-123", SubnetId: "subnet-456", AllowedCIDR: "172.31.0.0/16"},
			}},
			wantStatus: "ok",
			wantMsg:    "configured",
		},
		{
			name: "perforce configured via vpc keys only, no allowedCidr",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{VPCId: "vpc-123"},
			}},
			wantStatus: "warning",
			wantMsg:    "10.0.0.0/8",
		},
		{
			name: "perforce allowedCidr 0.0.0.0/0 warns (world-open)",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge", AllowedCIDR: "0.0.0.0/0"},
			}},
			wantStatus: "warning",
			wantMsg:    "0.0.0.0/0",
		},
		{
			name: "perforce allowedCidr ::/0 warns (world-open IPv6)",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge", AllowedCIDR: "::/0"},
			}},
			wantStatus: "warning",
			wantMsg:    "::/0",
		},
		{
			name: "perforce allowedCidr not a CIDR fails",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge", AllowedCIDR: "not-a-cidr"},
			}},
			wantStatus: "fail",
			wantMsg:    "not-a-cidr is not a valid CIDR",
		},
		{
			name: "perforce allowedCidr prefix out of range fails",
			rt: globals.Runtime{Config: &config.Config{
				Perforce: config.PerforceConfig{InstanceType: "c5.2xlarge", AllowedCIDR: "10.0.0.0/33"},
			}},
			wantStatus: "fail",
			wantMsg:    "10.0.0.0/33 is not a valid CIDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := checkPerforceCIDR(tt.rt)
			if c.Name != "Perforce CIDR" {
				t.Fatalf("name = %q, want %q", c.Name, "Perforce CIDR")
			}
			if c.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", c.Status, tt.wantStatus)
			}
			if !strings.Contains(c.Message, tt.wantMsg) {
				t.Errorf("message = %q, want substring %q", c.Message, tt.wantMsg)
			}
		})
	}
}

func TestPerforceSectionConfigured(t *testing.T) {
	cases := []struct {
		name string
		p    config.PerforceConfig
		want bool
	}{
		{"zero value", config.PerforceConfig{}, false},
		{"version", config.PerforceConfig{Version: "2025.2"}, true},
		{"instance type", config.PerforceConfig{InstanceType: "c5.2xlarge"}, true},
		{"volume size", config.PerforceConfig{VolumeSize: 200}, true},
		{"vpc id", config.PerforceConfig{VPCId: "vpc-1"}, true},
		{"subnet id", config.PerforceConfig{SubnetId: "subnet-1"}, true},
		{"allowed cidr", config.PerforceConfig{AllowedCIDR: "10.0.0.0/8"}, true},
		{"backup path", config.PerforceConfig{Backup: config.PerforceBackupConfig{Path: "/hxdepots"}}, true},
		{"backup s3 export", config.PerforceConfig{Backup: config.PerforceBackupConfig{S3Export: true}}, true},
		{"backup s3 bucket", config.PerforceConfig{Backup: config.PerforceBackupConfig{S3Bucket: "bkt"}}, true},
		{"backup s3 prefix", config.PerforceConfig{Backup: config.PerforceBackupConfig{S3Prefix: "p"}}, true},
		{"backup schedule", config.PerforceConfig{Backup: config.PerforceBackupConfig{Schedule: "0 3 * * *"}}, true},
		{"backup retain", config.PerforceConfig{Backup: config.PerforceBackupConfig{Retain: 7}}, true},
		{"iam path", config.PerforceConfig{IAM: config.IAM{Path: "/fabrica/"}}, true},
		{"iam boundary", config.PerforceConfig{IAM: config.IAM{PermissionsBoundary: "arn:aws:iam::123456789012:policy/b"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := perforceSectionConfigured(tc.p); got != tc.want {
				t.Errorf("perforceSectionConfigured(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestCheckRegion_Empty(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{Cloud: config.Cloud{AWS: config.AWS{}}}}
	c := checkRegion(rt)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckRegion_Set(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{Cloud: config.Cloud{AWS: config.AWS{Region: "eu-west-1"}}}}
	c := checkRegion(rt)
	if c.Status != "ok" || c.Message != "eu-west-1" {
		t.Errorf("checkRegion = %+v, want status=ok, message=eu-west-1", c)
	}
}

func TestCheckBucket_NoBackend(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{State: config.State{Bucket: "my-bucket"}}}
	c := checkBucket(context.Background(), rt, nil)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckTable_NoBucket(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{State: config.State{Table: "my-table"}}}
	c := checkTable(context.Background(), rt, nil)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

type fakeBackendChecker struct {
	bucketExists bool
	tableExists  bool
	bucketErr    bool
	tableErr     bool
}

func (f *fakeBackendChecker) StateBucketExists(_ context.Context, _ string) (bool, error) {
	if f.bucketErr {
		return false, context.DeadlineExceeded
	}
	return f.bucketExists, nil
}

func (f *fakeBackendChecker) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	if f.tableErr {
		return false, context.DeadlineExceeded
	}
	return f.tableExists, nil
}

type fakeProvider struct {
	account     string
	arn         string
	region      string
	identityErr error
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func (f *fakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return f.account, f.arn, f.region, f.identityErr
}

func (f *fakeProvider) Resources() cloud.ResourceClient {
	return nil
}

func TestClassifyNetworkPlacement(t *testing.T) {
	ingress := cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}
	igw := placement{modulePin{"perforce", "subnet-a", true}, testutil.IGWOnlySubnet("subnet-a")}
	ssm := placement{modulePin{"horde", "subnet-b", false}, testutil.SSMOnlySubnet("subnet-b")}
	ssmBoot := placement{modulePin{"workstation", "subnet-c", true}, testutil.SSMOnlySubnet("subnet-c")}
	nat := placement{modulePin{"ci", "subnet-d", false}, cloud.SubnetNetwork{SubnetID: "subnet-d", DefaultRoute: cloud.RouteNAT}}
	tests := []struct {
		name       string
		bpa        cloud.BPAStatus
		placed     []placement
		wantStatus string
		wantParts  []string
	}{
		{"bpa off", cloud.BPAStatus{Mode: cloud.BPAModeOff}, []placement{igw}, "ok", []string{"VPC Block Public Access: off"}},
		{"bpa empty mode is off", cloud.BPAStatus{}, nil, "ok", []string{"VPC Block Public Access: off"}},
		{"bpa on without pinned subnets", ingress, nil, "warning", []string{"block-ingress", "no module pins a subnetId", "perforce, horde, horde.agents, lore, ddc, workstation or ci"}},
		{"single nat module ok", ingress, []placement{nat}, "ok", []string{"ci: ok (nat)"}},
		{"single igw module fails", ingress, []placement{igw}, "fail", []string{"perforce: subnet subnet-a reaches the internet only through an internet gateway"}},
		{"endpoints module ok", ingress, []placement{ssm}, "ok", []string{"horde: ok (endpoints)"}},
		{"endpoints module warns at boot", ingress, []placement{ssmBoot}, "warning", []string{"workstation: subnet subnet-c reaches AWS through VPC endpoints only"}},
		{"warn beats ok", ingress, []placement{nat, ssmBoot}, "warning", []string{"ci: ok (nat)", "workstation: "}},
		{"fail beats warn and ok", ingress, []placement{ssmBoot, nat, igw}, "fail", []string{"workstation: ", "ci: ok (nat)", "perforce: subnet subnet-a"}},
		{"warn after fail keeps fail", ingress, []placement{igw, ssmBoot}, "fail", []string{"perforce: ", "; workstation: "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyNetworkPlacement(tt.bpa, tt.placed)
			if got.Name != networkPlacementName {
				t.Errorf("Name = %q, want %q", got.Name, networkPlacementName)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q (message %q)", got.Status, tt.wantStatus, got.Message)
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(got.Message, part) {
					t.Errorf("Message = %q, want substring %q", got.Message, part)
				}
			}
		})
	}
}

func TestClassifyNetworkPlacement_JoinsModuleLines(t *testing.T) {
	ingress := cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}
	placed := []placement{
		{modulePin{"ci", "subnet-d", false}, cloud.SubnetNetwork{SubnetID: "subnet-d", DefaultRoute: cloud.RouteNAT}},
		{modulePin{"horde", "subnet-b", false}, testutil.SSMOnlySubnet("subnet-b")},
	}
	got := classifyNetworkPlacement(ingress, placed)
	want := "ci: ok (nat); horde: ok (endpoints)"
	if got.Message != want {
		t.Fatalf("Message = %q, want %q", got.Message, want)
	}
}

func TestVerdictText(t *testing.T) {
	if got := verdictText(egress.Verdict{Level: egress.LevelOK, Path: egress.PathNAT}); got != "ok (nat)" {
		t.Errorf("ok verdict = %q, want %q", got, "ok (nat)")
	}
	if got := verdictText(egress.Verdict{Level: egress.LevelFail, Message: "blocked"}); got != "blocked" {
		t.Errorf("fail verdict = %q, want %q", got, "blocked")
	}
}

func TestLevelRankAndCheckStatus(t *testing.T) {
	tests := []struct {
		level  egress.Level
		rank   int
		status string
	}{
		{egress.LevelOK, 0, "ok"},
		{egress.LevelWarn, 1, "warning"},
		{egress.LevelFail, 2, "fail"},
	}
	for _, tt := range tests {
		if got := levelRank(tt.level); got != tt.rank {
			t.Errorf("levelRank(%s) = %d, want %d", tt.level, got, tt.rank)
		}
		if got := checkStatus(tt.level); got != tt.status {
			t.Errorf("checkStatus(%s) = %q, want %q", tt.level, got, tt.status)
		}
	}
}

func TestPinnedModules(t *testing.T) {
	if got := pinnedModules(nil); got != nil {
		t.Fatalf("nil config = %v, want nil", got)
	}
	cfg := config.Defaults()
	cfg.Perforce.SubnetId = "subnet-p"
	cfg.Horde.Agents.SubnetID = "subnet-a"
	cfg.CI.SubnetId = "subnet-c"
	cfg.Horde.VPCId = "vpc-only" // a vpcId without a subnetId does not pin
	got := pinnedModules(cfg)
	want := []modulePin{
		{"perforce", "subnet-p", true},
		{"horde.agents", "subnet-a", false},
		{"ci", "subnet-c", false},
	}
	if len(got) != len(want) {
		t.Fatalf("pinnedModules = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pinnedModules[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestPinnedModules_InternetAtBoot(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.SubnetId = "subnet-p"
	cfg.Workstation.SubnetId = "subnet-w"
	cfg.Lore.SubnetId = "subnet-l"
	for _, m := range pinnedModules(cfg) {
		wantBoot := m.module == "perforce" || m.module == "workstation"
		if m.internetAtBoot != wantBoot {
			t.Errorf("%s internetAtBoot = %v, want %v", m.module, m.internetAtBoot, wantBoot)
		}
	}
}

func TestCheckNetworkPlacement(t *testing.T) {
	ingress := cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}
	withPin := func() *config.Config {
		c := config.Defaults()
		c.Perforce.SubnetId = "subnet-p"
		return c
	}
	denied := fmt.Errorf("describe: %w", cloud.ErrInspectionDenied)
	tests := []struct {
		name       string
		rt         globals.Runtime
		wantStatus string
		wantMsg    string
	}{
		{"no provider", globals.Runtime{}, "warning", provision.NotInspectableMessage},
		{"provider cannot inspect", globals.Runtime{Provider: &testutil.TestProvider{}}, "warning", provision.NotInspectableMessage},
		{"bpa denied", globals.Runtime{Provider: &testutil.NetworkProvider{BPAErr: denied}}, "warning", "ec2:DescribeVpcBlockPublicAccessOptions"},
		{"bpa other error", globals.Runtime{Provider: &testutil.NetworkProvider{BPAErr: errors.New("throttled")}}, "fail", "check failed: throttled"},
		{"bpa off", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: cloud.BPAStatus{Mode: cloud.BPAModeOff}}, Config: withPin()}, "ok", "VPC Block Public Access: off"},
		{"bpa on no pins", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress}, Config: config.Defaults()}, "warning", "no module pins a subnetId"},
		{"bpa on nil config", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress}}, "warning", "no module pins a subnetId"},
		{"bpa on subnet denied", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress, SubnetErr: denied}, Config: withPin()}, "warning", "ec2:DescribeVpcBlockPublicAccessOptions"},
		{"bpa on subnet other error", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress, SubnetErr: errors.New("boom")}, Config: withPin()}, "fail", "check failed: boom"},
		{"bpa on igw pin fails", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress, Subnet: testutil.IGWOnlySubnet("subnet-p")}, Config: withPin()}, "fail", "perforce: subnet subnet-p reaches the internet only"},
		{"bpa on endpoints pin ok", globals.Runtime{Provider: &testutil.NetworkProvider{BPA: ingress, Subnet: testutil.SSMOnlySubnet("subnet-p")}, Config: func() *config.Config {
			c := config.Defaults()
			c.Lore.SubnetId = "subnet-p"
			return c
		}()}, "ok", "lore: ok (endpoints)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkNetworkPlacement(context.Background(), tt.rt)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q (message %q)", got.Status, tt.wantStatus, got.Message)
			}
			if !strings.Contains(got.Message, tt.wantMsg) {
				t.Errorf("Message = %q, want substring %q", got.Message, tt.wantMsg)
			}
		})
	}
}
