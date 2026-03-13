using Amazon.CloudFormation;
using Amazon.CloudWatch;
using Amazon.CostExplorer;
using Amazon.EC2;
using Amazon.S3;
using Amazon.SecretsManager;
using Amazon.SecurityToken;

namespace Fabrica.Operations;

/// <summary>
/// Centralized AWS SDK client factory. Day-2 operations use these directly
/// for runtime tasks that CDK doesn't cover (backup, status, scaling, cost queries).
/// </summary>
public class AwsClients
{
    public AmazonCloudFormationClient CloudFormation { get; } = new();
    public AmazonEC2Client Ec2 { get; } = new();
    public AmazonS3Client S3 { get; } = new();
    public AmazonCloudWatchClient CloudWatch { get; } = new();
    public AmazonCostExplorerClient CostExplorer { get; } = new();
    public AmazonSecretsManagerClient SecretsManager { get; } = new();
    public AmazonSecurityTokenServiceClient Sts { get; } = new();
}
