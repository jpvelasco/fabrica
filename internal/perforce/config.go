package perforce

import "context"

const DefaultHelixVersion = "2024.2"

// PerforceConfig holds the perforce section of fabrica.yaml.
type PerforceConfig struct {
	Version      string `mapstructure:"version"      yaml:"version"`
	InstanceType string `mapstructure:"instanceType" yaml:"instanceType"`
	VolumeSize   int    `mapstructure:"volumeSize"   yaml:"volumeSize"`
	VPCId        string `mapstructure:"vpcId"        yaml:"vpcId"`
	SubnetId     string `mapstructure:"subnetId"     yaml:"subnetId"`
}

// VPCResolver resolves VPC and subnet IDs. The AWS provider implements this
// via ec2:DescribeVpcs so that internal/perforce stays free of AWS SDK imports.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
