package deploy

import (
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// RoleDesiredState returns the Cloud Control desired-state JSON for the IAM role
// GameLift assumes to read the build from S3.
func RoleDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	bucketArn := fmt.Sprintf("arn:aws:s3:::%s/*", plan.BuildBucket)
	doc := map[string]any{
		"RoleName":                 plan.RoleName,
		"AssumeRolePolicyDocument": iamrole.AssumeRolePolicyDocument(iamrole.ServiceGameLift),
		"Policies": []map[string]any{{
			"PolicyName": "fabrica-deploy-s3-read",
			"PolicyDocument": map[string]any{
				"Version": "2012-10-17",
				"Statement": []map[string]any{{
					"Effect":   "Allow",
					"Action":   []string{"s3:GetObject"},
					"Resource": bucketArn,
				}},
			},
		}},
		"Tags": iamrole.RoleTags(plan.RoleName, nil),
	}
	return json.Marshal(doc)
}

// AliasDesiredState returns the desired state for the setup alias. Until the
// first promote there is no fleet, so the alias uses TERMINAL routing with a
// MESSAGE — valid and resolvable, just not pointing at a fleet yet.
func AliasDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	doc := map[string]any{
		"Name": plan.AliasName,
		"RoutingStrategy": map[string]any{
			"Type":    "TERMINAL",
			"Message": "Fabrica deploy alias — run 'fabrica deploy promote <build-version>' to point this at a fleet.",
		},
	}
	return json.Marshal(doc)
}

// BuildDesiredState returns the desired state for the GameLift build that
// references the packaged server in S3.
func BuildDesiredState(plan *PromotePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"Name":            plan.BuildName,
		"Version":         plan.BuildVersion,
		"OperatingSystem": plan.BuildOS,
		"StorageLocation": map[string]any{
			"Bucket":  plan.S3Bucket,
			"Key":     plan.S3Key,
			"RoleArn": plan.RoleARN,
		},
	}
	return json.Marshal(doc)
}

// FleetDesiredState returns the desired state for a managed EC2 fleet running
// the given build.
func FleetDesiredState(plan *PromotePlan, buildID string) (json.RawMessage, error) {
	doc := map[string]any{
		"Name":            plan.FleetName,
		"BuildId":         buildID,
		"ComputeType":     "EC2",
		"EC2InstanceType": plan.InstanceType,
		"FleetType":       plan.FleetType,
		"CertificateConfiguration": map[string]any{
			"CertificateType": "DISABLED",
		},
		"EC2InboundPermissions": []map[string]any{{
			"FromPort": plan.FromPort,
			"ToPort":   plan.ToPort,
			"IpRange":  "0.0.0.0/0",
			"Protocol": "UDP",
		}},
		"RuntimeConfiguration": map[string]any{
			"ServerProcesses": []map[string]any{{
				"ConcurrentExecutions": 1,
				"LaunchPath":           plan.LaunchPath,
			}},
		},
		"Locations": []map[string]any{
			{"Location": plan.Region},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.FleetName},
			{"Key": "BuildVersion", "Value": plan.BuildVersion},
		},
	}
	return json.Marshal(doc)
}

// AliasFlipPatch returns an RFC-6902 patch document that repoints an alias's
// routing strategy to SIMPLE/<fleetID>. Applied via ResourceClient.Update.
func AliasFlipPatch(fleetID string) (json.RawMessage, error) {
	patch := []map[string]any{{
		"op":   "replace",
		"path": "/RoutingStrategy",
		"value": map[string]any{
			"Type":    "SIMPLE",
			"FleetId": fleetID,
		},
	}}
	return json.Marshal(patch)
}
