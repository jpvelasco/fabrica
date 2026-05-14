package aws

import (
	"context"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/jpvelasco/fabrica/internal/config"
)

type awsProvider struct {
	cfg    *config.Config
	awsCfg awsConfig
	clients resourceClients
}

type awsConfig struct {
	region  string
	profile string
}

type resourceClients struct {
	cc *ccClient
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
	if p.clients.cc == nil {
		p.clients.cc = &ccClient{}
	}
	return &p.clients
}

// injectTags calls the tags helper with the real version.
func (p *awsProvider) injectTags(r *fabricac.Resource) {
	r.DesiredState = injectFabricaTags(r.DesiredState, "fabrica", fabricav.Version, nil)
}

func init() {
	fabricac.Register("aws", newProvider)
}
