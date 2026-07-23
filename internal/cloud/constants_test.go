package cloud

import (
	"testing"
)

func TestResourceTypeConstants(t *testing.T) {
	expected := map[string]string{
		"EC2Instance":        "AWS::EC2::Instance",
		"EC2SecurityGroup":   "AWS::EC2::SecurityGroup",
		"EC2Volume":          "AWS::EC2::Volume",
		"IAMRole":            "AWS::IAM::Role",
		"IAMInstanceProfile": "AWS::IAM::InstanceProfile",
		"S3Bucket":           "AWS::S3::Bucket",
		"DynamoDBTable":      "AWS::DynamoDB::Table",
	}

	got := map[string]string{
		"EC2Instance":        TypeAWSEC2Instance,
		"EC2SecurityGroup":   TypeAWSEC2SecurityGroup,
		"EC2Volume":          TypeAWSEC2Volume,
		"IAMRole":            TypeAWSIAMRole,
		"IAMInstanceProfile": TypeAWSIAMInstanceProfile,
		"S3Bucket":           TypeAWSS3Bucket,
		"DynamoDBTable":      TypeAWSDynamoDBTable,
	}

	for k, want := range expected {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}
