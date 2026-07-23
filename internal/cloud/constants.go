// Package cloud defines provider-agnostic interfaces and shared constants
// used across plan layers and cost estimators.
package cloud

// AWS resource type constants — shared across plan layers and cost estimators.
const (
	TypeAWSEC2Instance      = "AWS::EC2::Instance"
	TypeAWSEC2SecurityGroup = "AWS::EC2::SecurityGroup"
	TypeAWSEC2Volume        = "AWS::EC2::Volume"

	TypeAWSIAMRole            = "AWS::IAM::Role"
	TypeAWSIAMInstanceProfile = "AWS::IAM::InstanceProfile"

	TypeAWSS3Bucket      = "AWS::S3::Bucket"
	TypeAWSDynamoDBTable = "AWS::DynamoDB::Table"
)
