package horde

import (
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// AgentSGDesiredState returns the Cloud Control desired-state JSON for the
// agent security group. Agents only need outbound access to the coordinator
// (HTTP/gRPC) and SSM for management. No inbound from the internet.
//
// Inbound rules: if a coordinator SG ID is available, allow traffic from the
// coordinator SG on the coordinator port. Otherwise the SG has no inbound
// rules (agents are managed via SSM only).
func AgentSGDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	var ingress []map[string]any

	if plan.CoordinatorSGID != "" {
		ingress = append(ingress, map[string]any{
			"IpProtocol":            "tcp",
			"FromPort":              plan.CoordinatorPort,
			"ToPort":                plan.CoordinatorPort,
			"SourceSecurityGroupId": plan.CoordinatorSGID,
			"Description":           "Horde coordinator to agent communication",
		})
	}

	tags := []map[string]string{
		{"Key": "ManagedBy", "Value": "fabrica"},
		{"Key": "Name", "Value": plan.SGName},
		{"Key": "FabricaModule", "Value": "horde"},
		{"Key": "FabricaRole", "Value": "agent"},
	}

	doc := map[string]any{
		"GroupName":        plan.SGName,
		"GroupDescription": "Fabrica-managed security group for Horde build agents",
		"VpcId":            plan.VPCID,
		"Tags":             tags,
	}
	if len(ingress) > 0 {
		doc["SecurityGroupIngress"] = ingress
	}

	return json.Marshal(doc)
}

// AgentRoleDesiredState returns Cloud Control desired-state for the agent EC2
// instance role (SSM managed instance core + minimal agent permissions).
func AgentRoleDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	return iamrole.RoleDocument(plan.RoleName, iamrole.ServiceEC2, []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}, nil, map[string]string{
		"FabricaModule": "horde",
		"FabricaRole":   "agent",
	})
}

// AgentInstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the agent EC2 role.
func AgentInstanceProfileDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}

// LaunchTemplateDesiredState returns Cloud Control desired-state for the agent
// launch template. Configures AMI, instance type, user data, IAM profile, and
// security group for the ASG to launch.
//
// Note: AWS::EC2::LaunchTemplate does not accept a top-level "Tags" key in the
// Cloud Control schema — tags go in TagSpecifications only. The injectFabricaTags
// middleware will add a top-level Tags key, but Cloud Control rejects it. The
// TagSpecifications below are the correct place for launch template tags.
// Subnets are not part of launch template data — they are specified by the ASG.
func LaunchTemplateDesiredState(plan *AgentsCreatePlan, sgID, userData string) (json.RawMessage, error) {
	doc := map[string]any{
		"LaunchTemplateName": plan.LaunchTemplateName,
		"LaunchTemplateData": map[string]any{
			"ImageId":            plan.AmiID,
			"InstanceType":       plan.InstanceType,
			"IamInstanceProfile": map[string]any{"Name": plan.InstanceProfileName},
			"SecurityGroupIds":   []string{sgID},
			"UserData":           userData,
			"MetadataOptions":    map[string]any{"HttpTokens": "required"},
			"TagSpecifications": []map[string]any{
				{
					"ResourceType": "instance",
					"Tags": []map[string]string{
						{"Key": "ManagedBy", "Value": "fabrica"},
						{"Key": "Name", "Value": "fabrica-horde-agent"},
						{"Key": "FabricaModule", "Value": "horde"},
						{"Key": "FabricaRole", "Value": "agent"},
					},
				},
			},
		},
	}

	return json.Marshal(doc)
}

// ASGDesiredState returns Cloud Control desired-state for the agent Auto Scaling
// Group. References the launch template and configures min/desired/max capacity.
//
// Cloud Control schema notes:
// - MinSize, MaxSize, DesiredCapacity must be strings (not integers).
// - VPCZoneIdentifier must be a JSON array of subnet IDs (not a single string).
// - Tags require PropagateAtLaunch; the generic tag injector is skipped for ASGs
//
// because it would add tags without PropagateAtLaunch.
func ASGDesiredState(plan *AgentsCreatePlan, ltID string) (json.RawMessage, error) {
	doc := map[string]any{
		"AutoScalingGroupName": plan.ASGName,
		"MinSize":              fmt.Sprintf("%d", plan.MinSize),
		"MaxSize":              fmt.Sprintf("%d", plan.MaxSize),
		"DesiredCapacity":      fmt.Sprintf("%d", plan.DesiredCapacity),
		"LaunchTemplate": map[string]any{
			"LaunchTemplateId": ltID,
			// Cloud Control (via CloudFormation) does not accept "$Latest" or
			// "$Default" for the LaunchTemplate version. Use version "1" since
			// we just created the template and it has exactly one version.
			"Version": "1",
		},
		"Tags": []map[string]any{
			{"Key": "ManagedBy", "Value": "fabrica", "PropagateAtLaunch": true},
			{"Key": "Name", "Value": "fabrica-horde-agent", "PropagateAtLaunch": true},
			{"Key": "FabricaModule", "Value": "horde", "PropagateAtLaunch": true},
			{"Key": "FabricaRole", "Value": "agent", "PropagateAtLaunch": true},
		},
	}

	// VPC subnets — Cloud Control expects an array of subnet IDs.
	if plan.SubnetID != "" {
		doc["VPCZoneIdentifier"] = []string{plan.SubnetID}
	}

	return json.Marshal(doc)
}
