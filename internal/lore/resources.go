package lore

import (
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Lore
// security group. Opens TCP 41337 (gRPC), UDP 41337 (QUIC), and TCP 41339
// (HTTP health) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica-managed security group for Lore loreserver",
		plan.VPCID,
		[]ec2state.SGIngressRule{
			{IpProtocol: "tcp", FromPort: plan.GRPCPort, ToPort: plan.GRPCPort, CidrIp: plan.AllowedCIDR, Description: "Lore gRPC"},
			{IpProtocol: "udp", FromPort: plan.GRPCPort, ToPort: plan.GRPCPort, CidrIp: plan.AllowedCIDR, Description: "Lore QUIC"},
			{IpProtocol: "tcp", FromPort: plan.HTTPPort, ToPort: plan.HTTPPort, CidrIp: plan.AllowedCIDR, Description: "Lore HTTP health"},
		},
		map[string]string{"FabricaModule": "lore"},
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Lore EC2 instance. ImageId is the user-provided AMI ID from LoreConfig.
// DeleteOnTermination is true: the EBS store dies with the instance.
// When S3 store is enabled, the instance profile is attached for S3 access.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	spec := ec2state.InstanceSpec{
		ImageID:         plan.AmiID,
		InstanceType:    plan.InstanceType,
		SubnetID:        plan.SubnetID,
		SecurityGroupID: sgID,
		UserData:        userData,
		VolumeSize:      plan.VolumeSize,
		InstanceName:    plan.InstanceName,
	}
	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithExtraTags("FabricaModule", "lore"),
	}
	if plan.StoreBackend == StoreBackendS3 {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(plan.InstanceProfileName))
	}
	return ec2state.Build(spec, dsOpts...)
}

// StoreTableDesiredState returns the Cloud Control desired-state for one Lore
// store DynamoDB table (suffix is one of the StoreTables() suffixes). Only used
// when StoreBackend is "s3". The key schema, attribute types, and the locks
// table's three GSIs match the Lore 0.8.6 aws store plugin.
func StoreTableDesiredState(plan *CreatePlan, suffix string) (json.RawMessage, error) {
	spec, ok := StoreTableSpecByName(suffix)
	if !ok {
		return nil, fmt.Errorf("unknown Lore store table %q — known suffixes: fragments, metadata, mutable, locks", suffix)
	}

	// Attribute declarations: every key attribute of the base key and each
	// GSI needs a type declaration before KeySchema can reference it.
	attrs := map[string]string{spec.PK: spec.PKType}
	if spec.SK != "" {
		attrs[spec.SK] = spec.SKType
	}
	for _, gsi := range spec.GSIs {
		attrs[gsi.PK] = gsi.PKType
		attrs[gsi.SK] = gsi.SKType
	}
	attributeDefinitions := make([]map[string]any, 0, len(attrs))
	for name, typ := range attrs {
		attributeDefinitions = append(attributeDefinitions, map[string]any{"AttributeName": name, "AttributeType": typ})
	}

	keySchema := []map[string]any{{"AttributeName": spec.PK, "KeyType": "HASH"}}
	if spec.SK != "" {
		keySchema = append(keySchema, map[string]any{"AttributeName": spec.SK, "KeyType": "RANGE"})
	}

	doc := map[string]any{
		"TableName":            plan.StoreBucket + "-" + suffix,
		"KeySchema":            keySchema,
		"AttributeDefinitions": attributeDefinitions,
		"BillingMode":          "PAY_PER_REQUEST",
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.StoreBucket + "-" + suffix},
			{"Key": "FabricaModule", "Value": "lore"},
		},
	}
	if len(spec.GSIs) > 0 {
		gsis := make([]map[string]any, 0, len(spec.GSIs))
		for _, gsi := range spec.GSIs {
			gsis = append(gsis, map[string]any{
				"IndexName":  gsi.Name,
				"KeySchema":  []map[string]any{{"AttributeName": gsi.PK, "KeyType": "HASH"}, {"AttributeName": gsi.SK, "KeyType": "RANGE"}},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			})
		}
		doc["GlobalSecondaryIndexes"] = gsis
	}
	return json.Marshal(doc)
}

// BucketDesiredState returns Cloud Control desired-state for the Lore S3 store
// bucket. Only used when StoreBackend is "s3".
func BucketDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"BucketName": plan.StoreBucket,
		"PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls":       true,
			"BlockPublicPolicy":     true,
			"IgnorePublicAcls":      true,
			"RestrictPublicBuckets": true,
		},
		"BucketEncryption": map[string]any{
			"ServerSideEncryptionConfiguration": []map[string]any{
				{"ServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"}},
			},
		},
		"VersioningConfiguration": map[string]any{"Status": "Enabled"},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.StoreBucket},
			{"Key": "FabricaModule", "Value": "lore"},
		},
	}
	return json.Marshal(doc)
}

// RoleDesiredState returns the EC2 instance role for S3 access on the Lore
// store bucket + SSM core. Only used when StoreBackend is "s3". When
// StoreTables is non-empty a second inline policy grants the DynamoDB
// permissions the 0.8.6 aws store plugin needs on the four store tables (and
// the locks table's GSIs), scoped to arn:aws:dynamodb:<region>:<account>:
// table/<name>. Region/Account fall back to partition-agnostic placeholders
// when unset (tests).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	region, account := plan.Region, plan.Account
	if region == "" {
		region = "*"
	}
	if account == "" {
		account = "*"
	}
	policies := []map[string]any{
		iamrole.S3BucketPolicy("fabrica-lore-store-s3", plan.StoreBucket,
			[]string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketVersions"},
			[]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion"},
			"*",
		),
	}
	if len(plan.StoreTables) > 0 {
		tableARNs := make([]string, 0, len(plan.StoreTables))
		for _, name := range plan.StoreTables {
			tableARNs = append(tableARNs, fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, account, name))
		}
		// The plugin queries through the locks table's GSIs; grant on its index/*.
		tableARNs = append(tableARNs, fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s-locks/index/*", region, account, plan.StoreBucket))
		policies = append(policies, map[string]any{
			"PolicyName": "fabrica-lore-store-dynamodb",
			"PolicyDocument": map[string]any{
				"Version": "2012-10-17",
				"Statement": []map[string]any{
					{
						"Effect":   "Allow",
						"Action":   []string{"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:BatchGetItem", "dynamodb:DescribeTable", "dynamodb:TransactWriteItems"},
						"Resource": tableARNs,
					},
				},
			},
		})
	}
	return iamrole.RoleDocument(
		plan.RoleName,
		iamrole.ServiceEC2,
		[]string{"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		policies,
		map[string]string{"FabricaModule": "lore"},
	)
}

// InstanceProfileDesiredState wraps the Lore role for EC2 attachment.
// Only used when StoreBackend is "s3".
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}
