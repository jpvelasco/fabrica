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

	TypeAWSS3Bucket      = "AWS::S3::Bucket"
	TypeAWSDynamoDBTable = "AWS::DynamoDB::Table"

	TypeAWSEC2LaunchTemplate           = "AWS::EC2::LaunchTemplate"
	TypeAWSAutoScalingAutoScalingGroup = "AWS::AutoScaling::AutoScalingGroup"
	TypeAWSAutoScalingScalingPolicy    = "AWS::AutoScaling::ScalingPolicy"
	TypeAWSCloudWatchAlarm             = "AWS::CloudWatch::Alarm"
)
