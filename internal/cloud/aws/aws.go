package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
)

const defaultWaitTimeout = 15 * time.Minute

type awsProvider struct {
	cfg                      *config.Config
	awsCfg                   awsConfig
	clients                  resourceClients
	ec2                      ec2Service
	loadConfig               stateBackendConfigLoader
	newS3StateClient         stateBackendS3ClientFactory
	newDynamoDBStateClient   stateBackendDynamoDBClientFactory
	newBucketNotExistsWaiter stateBackendBucketWaiterFactory
	newTableNotExistsWaiter  stateBackendTableWaiterFactory
	newTableExistsWaiter     stateBackendTableExistsWaiterFactory
	newCodeBuildClient       codeBuildClientFactory
	newCWLogsClient          cwLogsClientFactory
	newGameLiftClient        gameLiftClientFactory
	newSSMClient             ssmClientFactory
	newAutoScalingClient     autoScalingClientFactory
}

type awsConfig struct {
	region  string
	profile string
}

type resourceClients struct {
	cc          ccAPIClient
	waiter      ccWaiter
	awsCfg      awsConfig
	version     string
	waitTimeout time.Duration // 0 → defaultWaitTimeout

	// seams for testing — nil means use real SDK constructors
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ccAPIClient
	newWaiter func(ccAPIClient) ccWaiter
}

var _ fabricac.Provider = (*awsProvider)(nil)
var _ fabricac.EC2InstanceManager = (*awsProvider)(nil)
var _ fabricac.StateBackendBootstrapper = (*awsProvider)(nil)
var _ fabricac.AMIResolver = (*awsProvider)(nil)
var _ fabricac.VPCResolver = (*awsProvider)(nil)
var _ fabricac.VPCCIDRResolver = (*awsProvider)(nil)
var _ fabricac.RegionProvider = (*awsProvider)(nil)

func newProvider(cfg *config.Config) (fabricac.Provider, error) {
	awsCfg := awsConfig{
		region:  cfg.Cloud.AWS.Region,
		profile: cfg.Cloud.AWS.Profile,
	}
	return &awsProvider{
		cfg:    cfg,
		awsCfg: awsCfg,
		ec2:    ec2Service{awsCfg: awsCfg},
	}, nil
}

func (p *awsProvider) Name() string {
	return "aws"
}

func (p *awsProvider) Identity(ctx context.Context) (account, arn, region string, err error) {
	return callerIdentity(ctx, p.awsCfg)
}

func (p *awsProvider) Resources() fabricac.ResourceClient {
	if p.clients.awsCfg == (awsConfig{}) {
		p.clients.awsCfg = p.awsCfg
		p.clients.version = fabricav.Version
	}
	return &p.clients
}

// StopInstance delegates to the EC2 service, satisfying the cloud.EC2InstanceManager
// interface so that type assertions in workstation commands work correctly.
func (p *awsProvider) StopInstance(ctx context.Context, instanceID string) error {
	return p.ec2.StopInstance(ctx, instanceID)
}

// StartInstance delegates to the EC2 service.
func (p *awsProvider) StartInstance(ctx context.Context, instanceID string) error {
	return p.ec2.StartInstance(ctx, instanceID)
}

// ResolveUbuntuAMI delegates to the AMI resolver, satisfying the
// cloud.AMIResolver interface so that type assertions in module commands work.
func (p *awsProvider) ResolveUbuntuAMI(ctx context.Context, region string) (string, error) {
	return p.ec2.ResolveUbuntuAMI(ctx, region)
}

// ResolveDefaultVPC delegates to the EC2 service, satisfying the
// cloud.VPCResolver interface so that type assertions in module commands work.
func (p *awsProvider) ResolveDefaultVPC(ctx context.Context) (string, string, error) {
	return p.ec2.ResolveDefaultVPC(ctx)
}

// ResolveVPCCIDR delegates to the EC2 service, satisfying the
// cloud.VPCCIDRResolver interface so that type assertions in module commands work.
func (p *awsProvider) ResolveVPCCIDR(ctx context.Context, vpcID string) (string, error) {
	return p.ec2.ResolveVPCCIDR(ctx, vpcID)
}

// WithRegion returns a RegionView bound to the given region, reusing the
// provider's credential profile. Multi-region modules (DDC edges) use it to
// provision resources outside the home region. The receiver is never mutated.
func (p *awsProvider) WithRegion(_ context.Context, region string) (fabricac.RegionView, error) {
	if region == "" {
		return fabricac.RegionView{}, fmt.Errorf("region is required")
	}
	scopedCfg := awsConfig{region: region, profile: p.awsCfg.profile}
	scoped := &awsProvider{
		cfg:    p.cfg,
		awsCfg: scopedCfg,
		ec2:    ec2Service{awsCfg: scopedCfg},
	}
	return fabricac.RegionView{Resources: scoped.Resources(), VPCs: scoped}, nil
}

func init() {
	fabricac.Register("aws", newProvider)
}
