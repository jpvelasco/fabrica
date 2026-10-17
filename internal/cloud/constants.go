// Package cloud defines provider-agnostic interfaces and shared constants
// used across plan layers and cost estimators.
package cloud

// AWS resource type constants — shared across plan layers and cost estimators.
const (
	TypeAWSEC2Instance      = "AWS::EC2::Instance"
	TypeAWSEC2SecurityGroup = "AWS::EC2::SecurityGroup"
	// TypeAWSEC2SecurityGroupIngress is a standalone ingress rule referencing
	// two security groups (e.g. agent-to-coordinator authorization).
	TypeAWSEC2SecurityGroupIngress = "AWS::EC2::SecurityGroupIngress"
	TypeAWSEC2Volume               = "AWS::EC2::Volume"

	TypeAWSIAMRole            = "AWS::IAM::Role"
	TypeAWSIAMInstanceProfile = "AWS::IAM::InstanceProfile"

	// TypeAWSSSMParameter is the SSM Parameter Store type. Workstation uses it
	// only as a terminate target: the SecureString is not recorded in state,
	// because a drift Get would return the secret.
	TypeAWSSSMParameter = "AWS::SSM::Parameter"

	TypeAWSS3Bucket      = "AWS::S3::Bucket"
	TypeAWSDynamoDBTable = "AWS::DynamoDB::Table"

	TypeAWSEC2LaunchTemplate           = "AWS::EC2::LaunchTemplate"
	TypeAWSAutoScalingAutoScalingGroup = "AWS::AutoScaling::AutoScalingGroup"
	TypeAWSAutoScalingScalingPolicy    = "AWS::AutoScaling::ScalingPolicy"
	TypeAWSCloudWatchAlarm             = "AWS::CloudWatch::Alarm"
)

// CreateNonceTag is the instance tag the create layers stamp with the
// per-create random nonce at launch. The SDK launch path matches pending or
// running instances on it before adopting one, so a re-create under the same
// Name tag can never adopt another create's instance, and the RunInstances
// ClientToken derives from it so only retries of the same create reuse a
// token.
const CreateNonceTag = "FabricaCreateNonce"
