package aws

import (
	"context"
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
	ec2Mgr                   ec2Manager
	loadConfig               stateBackendConfigLoader
	newS3StateClient         stateBackendS3ClientFactory
	newDynamoDBStateClient   stateBackendDynamoDBClientFactory
	newBucketNotExistsWaiter stateBackendBucketWaiterFactory
	newTableNotExistsWaiter  stateBackendTableWaiterFactory
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

func newProvider(cfg *config.Config) (fabricac.Provider, error) {
	return &awsProvider{
		cfg: cfg,
		awsCfg: awsConfig{
			region:  cfg.Cloud.AWS.Region,
			profile: cfg.Cloud.AWS.Profile,
		},
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

func (p *awsProvider) EC2Manager() fabricac.EC2InstanceManager {
	if p.ec2Mgr.awsCfg == (awsConfig{}) {
		p.ec2Mgr.awsCfg = p.awsCfg
	}
	return &p.ec2Mgr
}

// StopInstance delegates to the ec2Manager, satisfying the cloud.EC2InstanceManager
// interface so that type assertions in workstation commands work correctly.
func (p *awsProvider) StopInstance(ctx context.Context, instanceID string) error {
	return p.EC2Manager().StopInstance(ctx, instanceID)
}

// StartInstance delegates to the ec2Manager.
func (p *awsProvider) StartInstance(ctx context.Context, instanceID string) error {
	return p.EC2Manager().StartInstance(ctx, instanceID)
}

func init() {
	fabricac.Register("aws", newProvider)
}
