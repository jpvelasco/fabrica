package horde

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
)

// AgentSGDesiredState returns the Cloud Control desired-state JSON for the
// agent security group. Agents initiate connections TO the coordinator, so
// this SG needs no inbound rules at all — SSM handles management, and agent-
// to-coordinator traffic is authorized on the coordinator side (see
// AgentToCoordinatorIngressDesiredState).
func AgentSGDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	tags := agentTags(plan.SGName)

	doc := map[string]any{
		"GroupName":        plan.SGName,
		"GroupDescription": "Fabrica-managed security group for Horde build agents",
		"VpcId":            plan.VPCID,
		"Tags":             tags,
	}

	return json.Marshal(doc)
}

// agentToCoordinatorIngress returns Cloud Control desired-state for a
// standalone ingress rule on the coordinator security group authorizing
// agent-initiated connections from the agent security group on one port.
// Cloud Control allows one port range per rule, so each coordinator port an
// agent needs is its own resource, tracked as its own state entry and
// deleted before either SG during teardown.
func agentToCoordinatorIngress(plan *AgentsCreatePlan, agentSGID string, port int, description string) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupId":               plan.CoordinatorSGID,
		"SourceSecurityGroupId": agentSGID,
		"IpProtocol":            "tcp",
		"FromPort":              port,
		"ToPort":                port,
		"Description":           description,
	}
	return json.Marshal(doc)
}

// AgentToCoordinatorIngressDesiredState authorizes the coordinator's HTTP
// port (web UI / API) for agent-initiated connections.
func AgentToCoordinatorIngressDesiredState(plan *AgentsCreatePlan, agentSGID string) (json.RawMessage, error) {
	return agentToCoordinatorIngress(plan, agentSGID, plan.CoordinatorPort, "Horde agents to coordinator (Fabrica-managed)")
}

// AgentToCoordinatorGRPCIngressDesiredState authorizes the coordinator's
// gRPC port — the port agents use for enrollment traffic — for
// agent-initiated connections.
func AgentToCoordinatorGRPCIngressDesiredState(plan *AgentsCreatePlan, agentSGID string) (json.RawMessage, error) {
	return agentToCoordinatorIngress(plan, agentSGID, plan.CoordinatorGRPCPort, "Horde agents to coordinator gRPC (Fabrica-managed)")
}

// AgentRoleDesiredState returns Cloud Control desired-state for the agent EC2
// instance role (SSM managed instance core + minimal agent permissions).
func AgentRoleDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	return iamrole.RoleDocument(plan.RoleName, iamrole.ServiceEC2, []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}, []map[string]any{iamrole.SSMOutputPolicy(plan.Region, plan.Account)}, map[string]string{
		"FabricaModule": "horde",
		"FabricaRole":   "agent",
	}, iamrole.Options(plan.IAM.Path, plan.IAM.PermissionsBoundary)...)
}

// AgentInstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the agent EC2 role.
func AgentInstanceProfileDesiredState(plan *AgentsCreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName, ec2state.ProfileOptions(plan.IAM.Path)...)
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
		"TagSpecifications":  []map[string]any{{"ResourceType": "launch-template", "Tags": agentResourceTags(plan, plan.LaunchTemplateName)}},
		"LaunchTemplateData": map[string]any{
			"ImageId":            plan.AmiID,
			"InstanceType":       plan.InstanceType,
			"IamInstanceProfile": map[string]any{"Name": plan.InstanceProfileName},
			"SecurityGroupIds":   []string{sgID},
			"UserData":           userData,
			"MetadataOptions":    map[string]any{"HttpTokens": "required", "HttpPutResponseHopLimit": 1},
			"TagSpecifications":  agentTagSpecifications(plan),
		},
	}

	return json.Marshal(doc)
}

// agentTagSpecifications tags the instance and the volume launched from the
// launch template with the same set: identity tags, then cloud.aws.tags, then
// volumeRequestTags. Auto Scaling launches do not retry through RunInstances.
func agentTagSpecifications(plan *AgentsCreatePlan) []map[string]any {
	tags := agentInstanceTags(plan)
	return []map[string]any{
		{"ResourceType": "instance", "Tags": tags},
		{"ResourceType": "volume", "Tags": tags},
	}
}

// agentResourceTags is the tag set for the launch template and the Auto
// Scaling group: identity tags for name, then cloud.aws.tags.
func agentResourceTags(plan *AgentsCreatePlan, name string) []map[string]string {
	return mergeAgentTags(agentTags(name), plan.OperatorTags)
}

// agentInstanceTags is the tag set for instances and volumes. volumeRequestTags
// is the later layer, so it wins over cloud.aws.tags on a shared key: it is the
// set the root volume is requested with and therefore the more specific one.
func agentInstanceTags(plan *AgentsCreatePlan) []map[string]string {
	return mergeAgentTags(agentTags("fabrica-horde-agent"), plan.OperatorTags, plan.VolumeRequestTags)
}

// agentTags is the identity tag set for the agent pool. The agent security
// group uses it directly; the launch template, Auto Scaling group, and
// instances add operator layers through mergeAgentTags.
func agentTags(name string) []map[string]string {
	return []map[string]string{
		{"Key": fabricatags.ManagedByKey, "Value": fabricatags.ManagedByValue},
		{"Key": "Name", "Value": name},
		{"Key": "FabricaModule", "Value": "horde"},
		{"Key": "FabricaRole", "Value": "agent"},
	}
}

// mergeAgentTags appends operator layers in order to the identity base. Base
// keys always win, including Name and FabricaRole, which are not on the shared
// identity list. Later layers win over earlier ones on the same key. Empty keys
// are dropped because tag APIs reject them.
func mergeAgentTags(base []map[string]string, layers ...map[string]string) []map[string]string {
	seen := make(map[string]struct{}, len(base))
	out := make([]map[string]string, 0, len(base))
	for _, t := range base {
		seen[t["Key"]] = struct{}{}
		out = append(out, t)
	}

	values := make(map[string]string)
	for _, layer := range layers {
		for k, v := range layer {
			if k == "" || fabricatags.IdentityKey(k) {
				continue
			}
			values[k] = v
		}
	}
	for _, k := range slices.Sorted(maps.Keys(values)) {
		if _, ok := seen[k]; ok {
			continue
		}
		out = append(out, map[string]string{"Key": k, "Value": values[k]})
	}
	return out
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
	var propagated []map[string]any
	for _, tag := range agentResourceTags(plan, "fabrica-horde-agent") {
		propagated = append(propagated, map[string]any{"Key": tag["Key"], "Value": tag["Value"], "PropagateAtLaunch": true})
	}
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
		"Tags": propagated,
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
