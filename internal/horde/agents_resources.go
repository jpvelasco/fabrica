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

// ScaleOutPolicyDesiredState returns Cloud Control desired-state for the
// scale-out SimpleScaling policy. When triggered by the scale-out alarm,
// it adds one instance to the ASG. The ASG's MinSize/MaxSize act as hard bounds.
func ScaleOutPolicyDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	// Cloud Control schema for AWS::AutoScaling::ScalingPolicy:
	// - Cooldown is a String property (not an integer).
	// - ScalingAdjustment is an integer.
	doc := map[string]any{
		"AutoScalingGroupName": plan.ASGName,
		"PolicyName":           plan.ScaleOutPolicyName,
		"PolicyType":           "SimpleScaling",
		"ScalingAdjustment":    1,
		"Cooldown":             fmt.Sprintf("%d", plan.ScaleInCooldown),
		"AdjustmentType":       "ChangeInCapacity",
	}

	return json.Marshal(doc)
}

// ScaleInPolicyDesiredState returns Cloud Control desired-state for the
// scale-in SimpleScaling policy. When triggered by the scale-in alarm,
// it removes one instance from the ASG. The ASG's MinSize/MaxSize act as hard bounds.
func ScaleInPolicyDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	// Cloud Control schema for AWS::AutoScaling::ScalingPolicy:
	// - Cooldown is a String property (not an integer).
	// - ScalingAdjustment is an integer.
	doc := map[string]any{
		"AutoScalingGroupName": plan.ASGName,
		"PolicyName":           plan.ScaleInPolicyName,
		"PolicyType":           "SimpleScaling",
		"ScalingAdjustment":    -1,
		"Cooldown":             fmt.Sprintf("%d", plan.ScaleInCooldown),
		"AdjustmentType":       "ChangeInCapacity",
	}

	return json.Marshal(doc)
}

// ScaleOutAlarmDesiredState returns Cloud Control desired-state for the scale-out
// CloudWatch alarm. This alarm fires when the queue depth metric exceeds the
// configured scale-out threshold, triggering the scale-out scaling policy to
// add instances. The policyARN is the real ARN returned by Cloud Control after
// creating the scaling policy.
func ScaleOutAlarmDesiredState(plan *AgentsCreatePlan, policyARN string) (json.RawMessage, error) {
	doc := map[string]any{
		"AlarmName":          plan.ScaleOutAlarmName,
		"AlarmDescription":   "Scale out Horde agent pool when queue depth exceeds threshold",
		"MetricName":         plan.MetricName,
		"Namespace":          plan.MetricNamespace,
		"Statistic":          "Average",
		"Dimensions":         []map[string]any{{"Name": "AutoScalingGroupName", "Value": plan.ASGName}},
		"Period":             300,
		"EvaluationPeriods":  2,
		"Threshold":          plan.ScaleOutThreshold,
		"ComparisonOperator": "GreaterThanThreshold",
		"AlarmActions":       []string{policyARN},
		"TreatMissingData":   "notBreaching",
	}

	return json.Marshal(doc)
}

// ScaleInAlarmDesiredState returns Cloud Control desired-state for the scale-in
// CloudWatch alarm. This alarm fires when the queue depth metric drops below the
// configured scale-in threshold, triggering the scale-in scaling policy to
// remove instances. The policyARN is the real ARN returned by Cloud Control after
// creating the scaling policy.
func ScaleInAlarmDesiredState(plan *AgentsCreatePlan, policyARN string) (json.RawMessage, error) {
	doc := map[string]any{
		"AlarmName":          plan.ScaleInAlarmName,
		"AlarmDescription":   "Scale in Horde agent pool when queue depth drops below threshold",
		"MetricName":         plan.MetricName,
		"Namespace":          plan.MetricNamespace,
		"Statistic":          "Average",
		"Dimensions":         []map[string]any{{"Name": "AutoScalingGroupName", "Value": plan.ASGName}},
		"Period":             300,
		"EvaluationPeriods":  2,
		"Threshold":          plan.ScaleInThreshold,
		"ComparisonOperator": "LessThanThreshold",
		"AlarmActions":       []string{policyARN},
		"TreatMissingData":   "notBreaching",
	}

	return json.Marshal(doc)
}
