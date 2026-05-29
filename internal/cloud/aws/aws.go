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

func init() {
	fabricac.Register("aws", newProvider)
}
