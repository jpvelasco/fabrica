package testutil

import (
	"context"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// NilResourceProvider is a provider whose resource client is unavailable.
// Use it to test the boundary between a configured provider and a provider
// that cannot supply resource operations.
type NilResourceProvider struct {
	TestProvider
}

var _ cloud.Provider = (*NilResourceProvider)(nil)

func (*NilResourceProvider) Resources() cloud.ResourceClient { return nil }

// UbuntuAMIProvider extends TestProvider with configurable Ubuntu AMI
// resolution. It is intentionally separate so TestProvider continues to
// exercise providers that do not implement cloud.AMIResolver.
type UbuntuAMIProvider struct {
	TestProvider

	AMIID  string
	AMIErr error
}

var _ cloud.AMIResolver = (*UbuntuAMIProvider)(nil)

func (p *UbuntuAMIProvider) ResolveUbuntuAMI(context.Context, string) (string, error) {
	if p.AMIErr != nil {
		return "", p.AMIErr
	}
	if p.AMIID == "" {
		return "ami-fake-ubuntu", nil
	}
	return p.AMIID, nil
}

// EC2InstanceProvider extends TestProvider with configurable EC2 instance
// lifecycle behavior.
type EC2InstanceProvider struct {
	TestProvider

	StartErr error
	StopErr  error
	StartIDs []string
	StopIDs  []string
}

var _ cloud.EC2InstanceManager = (*EC2InstanceProvider)(nil)

func (p *EC2InstanceProvider) StartInstance(_ context.Context, instanceID string) error {
	p.StartIDs = append(p.StartIDs, instanceID)
	return p.StartErr
}

func (p *EC2InstanceProvider) StopInstance(_ context.Context, instanceID string) error {
	p.StopIDs = append(p.StopIDs, instanceID)
	return p.StopErr
}

// RemoteCommandProvider extends TestProvider with configurable remote command
// execution behavior.
type RemoteCommandProvider struct {
	TestProvider

	RemoteResult             cloud.RemoteResult
	RemoteErr                error
	RunCommandCalls          int
	LastRunCommandInstanceID string
	LastRunCommands          []string
}

var _ cloud.RemoteRunner = (*RemoteCommandProvider)(nil)

func (p *RemoteCommandProvider) RunCommand(_ context.Context, instanceID string, commands []string) (cloud.RemoteResult, error) {
	p.RunCommandCalls++
	p.LastRunCommandInstanceID = instanceID
	p.LastRunCommands = append([]string(nil), commands...)
	return p.RemoteResult, p.RemoteErr
}

// VPCResolverProvider extends TestProvider with configurable default-VPC
// resolution. It is intentionally separate so TestProvider continues to
// exercise providers that do not implement cloud.VPCResolver.
type VPCResolverProvider struct {
	TestProvider

	VPCID      string
	SubnetID   string
	VPCErr     error
	Calls      int
	VPCCIDR    string
	VPCCIDRErr error
	CIDRCalls  int
}

var _ cloud.VPCResolver = (*VPCResolverProvider)(nil)
var _ cloud.VPCCIDRResolver = (*VPCResolverProvider)(nil)

func (p *VPCResolverProvider) ResolveDefaultVPC(context.Context) (string, string, error) {
	p.Calls++
	if p.VPCErr != nil {
		return "", "", p.VPCErr
	}
	vpcID := p.VPCID
	if vpcID == "" {
		vpcID = "vpc-fake"
	}
	subnetID := p.SubnetID
	if subnetID == "" {
		subnetID = "subnet-fake"
	}
	return vpcID, subnetID, nil
}

func (p *VPCResolverProvider) ResolveVPCCIDR(context.Context, string) (string, error) {
	p.CIDRCalls++
	if p.VPCCIDRErr != nil {
		return "", p.VPCCIDRErr
	}
	return p.VPCCIDR, nil
}

// CodeBuildVPCProvider implements both cloud.CodeBuildRunner and
// cloud.VPCResolver in one fake, so cobra tests can exercise the full ci setup
// wiring (VPC resolution → SG → project with VpcConfig) through New().
type CodeBuildVPCProvider struct {
	CodeBuildProvider

	VPCID    string
	SubnetID string
	VPCErr   error
	Calls    int
}

var (
	_ cloud.CodeBuildRunner = (*CodeBuildVPCProvider)(nil)
	_ cloud.VPCResolver     = (*CodeBuildVPCProvider)(nil)
)

func (p *CodeBuildVPCProvider) ResolveDefaultVPC(context.Context) (string, string, error) {
	p.Calls++
	if p.VPCErr != nil {
		return "", "", p.VPCErr
	}
	vpcID := p.VPCID
	if vpcID == "" {
		vpcID = "vpc-fake"
	}
	subnetID := p.SubnetID
	if subnetID == "" {
		subnetID = "subnet-fake"
	}
	return vpcID, subnetID, nil
}

// CodeBuildProvider extends TestProvider with configurable CodeBuild behavior.
// It is intentionally separate so TestProvider continues to exercise providers
// that do not implement cloud.CodeBuildRunner.
type CodeBuildProvider struct {
	TestProvider

	ProjectAlreadyExists bool
	EnsureProjectErr     error
	EnsureProjectCalls   int

	DeleteProjectErr   error
	DeleteProjectCalls int

	StartBuildID          string
	StartBuildErr         error
	StartBuildCalls       int
	LastStartBuildProject string
	LastStartBuildEnv     map[string]string

	BuildInfo      cloud.BuildInfo
	BuildStatusErr error
	BuildLogOutput string
	BuildLogErr    error

	// LastEnsureProjectSpec captures the most recent spec passed to
	// EnsureProject for VPC wiring assertions.
	LastEnsureProjectSpec cloud.CodeBuildProjectSpec
}

var _ cloud.CodeBuildRunner = (*CodeBuildProvider)(nil)

func (p *CodeBuildProvider) EnsureProject(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
	p.EnsureProjectCalls++
	p.LastEnsureProjectSpec = spec
	return !p.ProjectAlreadyExists, p.EnsureProjectErr
}

func (p *CodeBuildProvider) DeleteProject(context.Context, string) error {
	p.DeleteProjectCalls++
	return p.DeleteProjectErr
}

func (p *CodeBuildProvider) StartBuild(_ context.Context, project string, env map[string]string) (string, error) {
	p.StartBuildCalls++
	p.LastStartBuildProject = project
	p.LastStartBuildEnv = env
	if p.StartBuildID == "" {
		return "build-1", p.StartBuildErr
	}
	return p.StartBuildID, p.StartBuildErr
}

func (p *CodeBuildProvider) BuildStatus(context.Context, string) (cloud.BuildInfo, error) {
	return p.BuildInfo, p.BuildStatusErr
}

func (p *CodeBuildProvider) BuildLog(context.Context, string) (string, error) {
	return p.BuildLogOutput, p.BuildLogErr
}

// GameLiftProvider extends TestProvider with configurable GameLift behavior.
// It is intentionally separate so TestProvider continues to exercise providers
// that do not implement cloud.GameLiftManager.
type GameLiftProvider struct {
	TestProvider

	FleetIdentifier       string
	CreateFleetAsyncErr   error
	CreateFleetAsyncCalls int

	FleetStatusByID  map[string]string
	FleetStatusErr   error
	FleetStatusCalls int

	FleetEventsResult []cloud.FleetEvent
	FleetEventsErr    error
	FleetEventsCalls  int
}

var _ cloud.GameLiftManager = (*GameLiftProvider)(nil)

func (p *GameLiftProvider) CreateFleetAsync(_ context.Context, resource *cloud.Resource) error {
	p.CreateFleetAsyncCalls++
	if p.CreateFleetAsyncErr != nil {
		return p.CreateFleetAsyncErr
	}
	if resource == nil || resource.Identifier != "" {
		return nil
	}
	resource.Identifier = p.FleetIdentifier
	if resource.Identifier == "" {
		resource.Identifier = "fleet-new"
	}
	return nil
}

func (p *GameLiftProvider) FleetStatus(_ context.Context, fleetID string) (cloud.FleetInfo, error) {
	p.FleetStatusCalls++
	if p.FleetStatusErr != nil {
		return cloud.FleetInfo{}, p.FleetStatusErr
	}
	status := "ACTIVE"
	if configured, ok := p.FleetStatusByID[fleetID]; ok {
		status = configured
	}
	return cloud.FleetInfo{FleetID: fleetID, Status: status}, nil
}

func (p *GameLiftProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	p.FleetEventsCalls++
	return p.FleetEventsResult, p.FleetEventsErr
}
