// Package horde provides the plan layer for the Horde build coordinator
// and its managed agent pool (Auto Scaling Group).
package horde

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// ScalingPublisherNote is the standing operator warning for queue autoscaling.
// Alarms and policies are provisioned against an external CloudWatch metric;
// Fabrica does not scrape Horde. Printed by agents create, status, and metrics.
const ScalingPublisherNote = "Alarms are ready only after agents (or operator tooling) publish the configured metric. Fabrica does not scrape Horde."

// DefaultAgentsSGName is the agent-pool security group name. Agents dial the
// coordinator, so the group has no inbound rules.
const DefaultAgentsSGName = "fabrica-horde-agents-sg"

// CoordinatorRef describes the live Horde coordinator the agent pool must
// reach: the private IP agents enroll against, the coordinator's ports
// (HTTP + gRPC), the coordinator security group the agent ingress rules
// attach to, and the coordinator's VPC/subnet placement. Placement fields
// are best-effort at resolution time — the pool defaults to the
// coordinator's placement only when both halves are known; explicit
// horde.agents.vpcId/subnetId always win.
type CoordinatorRef struct {
	PrivateIP string
	Port      int
	GRPCPort  int
	SGID      string
	VPCID     string
	SubnetID  string
}

// CoordinatorPlacement is the coordinator's live VPC/subnet placement,
// resolved from the instance (subnet) and the coordinator security group
// (VPC). The pool follows it only when both halves are known; a half-known
// placement falls back to the account default VPC.
type CoordinatorPlacement struct {
	VPCID    string
	SubnetID string
}

// AgentsCreatePlan describes the resources needed to provision a Horde agent
// Auto Scaling Group. It is built from config + the existing coordinator state
// (coordinator private IP for agent enrollment).
type AgentsCreatePlan struct {
	Connection      Connection
	CAPath          string
	Account         string
	Region          string
	AmiID           string
	InstanceType    string
	MinSize         int
	DesiredCapacity int
	MaxSize         int
	VPCID           string
	SubnetID        string
	DefaultVPC      bool
	// CoordinatorPrivateIP is the private IP of the Horde coordinator that
	// agents will enroll against. Resolved from Cloud Control at command time.
	CoordinatorPrivateIP string
	// CoordinatorPort is the HTTP port of the Horde coordinator (default 5000).
	CoordinatorPort int
	// CoordinatorGRPCPort is the gRPC port agents use for enrollment traffic
	// (default 5002).
	CoordinatorGRPCPort int
	// CoordinatorSGID is the security group ID of the coordinator, used as
	// the source for agent → coordinator traffic.
	CoordinatorSGID string

	SGName              string
	RoleName            string
	InstanceProfileName string
	LaunchTemplateName  string
	ASGName             string

	// Scaling fields — queue-based autoscaling for the agent pool.
	// Uses two SimpleScaling policies: one for scale-out (+1) and one for scale-in (-1).
	// Each CloudWatch alarm triggers its corresponding policy.
	ScalingEnabled     bool
	ScaleOutThreshold  float64
	ScaleInThreshold   float64
	ScaleInCooldown    int
	MetricName         string
	MetricNamespace    string
	ScaleOutAlarmName  string
	ScaleInAlarmName   string
	ScaleOutPolicyName string
	ScaleInPolicyName  string

	// IAM is the resolved path and permissions boundary. Agents share horde.iam.
	IAM config.IAM
	// VolumeRequestTags are copied onto the launch template for instance and volume.
	VolumeRequestTags map[string]string

	CostResources []cost.Resource
}

// NewAgentsCreatePlan builds an AgentsCreatePlan from config and the live
// coordinator reference. The coordinator must already be provisioned.
func NewAgentsCreatePlan(ctx context.Context, cfg config.HordeAgentsConfig, coord CoordinatorRef, account, region string, resolver cloud.VPCResolver) (*AgentsCreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("horde.agents.amiId is required. Provide an AMI ID that contains the Horde agent software.\nSee: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-agent-ami.md")
	}
	if coord.PrivateIP == "" {
		return nil, fmt.Errorf("coordinator private IP is not available. Ensure 'fabrica horde create' has been run and the coordinator instance is running")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "c7i.xlarge"
	}

	minSize := cfg.MinSize
	desiredCapacity := cfg.DesiredCapacity
	maxSize := cfg.MaxSize

	// Apply defaults: if nothing is set, default to 0/1/2.
	if desiredCapacity <= 0 {
		desiredCapacity = 1
	}
	if maxSize <= 0 {
		maxSize = 2
	}
	if minSize < 0 {
		minSize = 0
	}

	// Validate capacity invariants.
	if minSize > desiredCapacity {
		return nil, fmt.Errorf("agents minSize (%d) must not exceed desiredCapacity (%d)", minSize, desiredCapacity)
	}
	if desiredCapacity > maxSize {
		return nil, fmt.Errorf("agents desiredCapacity (%d) must not exceed maxSize (%d)", desiredCapacity, maxSize)
	}
	if minSize > maxSize {
		return nil, fmt.Errorf("agents minSize (%d) must not exceed maxSize (%d)", minSize, maxSize)
	}

	port := coord.Port
	if port <= 0 {
		port = DefaultPort
	}
	grpcPort := coord.GRPCPort
	if grpcPort <= 0 {
		grpcPort = DefaultGRPCPort
	}

	vpcID, subnetID, defaultVPC, err := resolveAgentPlacement(ctx, cfg, coord, resolver)
	if err != nil {
		return nil, err
	}

	// Apply scaling defaults and validate.
	scaling := cfg.Scaling
	if scaling.Enabled {
		// Default thresholds if not set.
		if scaling.ScaleOutThreshold <= 0 {
			scaling.ScaleOutThreshold = 5.0
		}
		if scaling.ScaleInThreshold <= 0 {
			scaling.ScaleInThreshold = 1.0
		}
		if scaling.ScaleInCooldown <= 0 {
			// Default cooldown: 300 seconds (5 minutes).
			scaling.ScaleInCooldown = 300
		}
		if scaling.ScaleInCooldown < 60 {
			return nil, fmt.Errorf("agents scaling.scaleInCooldown must be at least 60 seconds (got %d)", scaling.ScaleInCooldown)
		}
		if scaling.MetricName == "" {
			scaling.MetricName = "ASGQueueDepth"
		}
		if scaling.MetricNamespace == "" {
			scaling.MetricNamespace = "Fabrica/HordeAgents"
		}
	}

	return &AgentsCreatePlan{
		Account:              account,
		Region:               region,
		AmiID:                cfg.AmiID,
		InstanceType:         instanceType,
		MinSize:              minSize,
		DesiredCapacity:      desiredCapacity,
		MaxSize:              maxSize,
		VPCID:                vpcID,
		SubnetID:             subnetID,
		DefaultVPC:           defaultVPC,
		CoordinatorPrivateIP: coord.PrivateIP,
		CoordinatorPort:      port,
		CoordinatorGRPCPort:  grpcPort,
		CoordinatorSGID:      coord.SGID,
		SGName:               DefaultAgentsSGName,
		RoleName:             "fabrica-horde-agents-role",
		InstanceProfileName:  "fabrica-horde-agents-profile",
		LaunchTemplateName:   "fabrica-horde-agents-lt",
		ASGName:              "fabrica-horde-agents-asg",
		// Scaling
		ScalingEnabled:     scaling.Enabled,
		ScaleOutThreshold:  scaling.ScaleOutThreshold,
		ScaleInThreshold:   scaling.ScaleInThreshold,
		ScaleInCooldown:    scaling.ScaleInCooldown,
		MetricName:         scaling.MetricName,
		MetricNamespace:    scaling.MetricNamespace,
		ScaleOutAlarmName:  "fabrica-horde-agents-scale-out",
		ScaleInAlarmName:   "fabrica-horde-agents-scale-in",
		ScaleOutPolicyName: "fabrica-horde-agents-scale-out-policy",
		ScaleInPolicyName:  "fabrica-horde-agents-scale-in-policy",
		CostResources:      AgentsCostResources(cfg),
	}, nil
}

// resolveAgentPlacement picks the VPC + subnet the agent pool launches in.
// Explicit horde.agents.vpcId/subnetId win and must be set together;
// otherwise the pool follows the coordinator's placement when both halves
// are known; otherwise the account default VPC is resolved (legacy
// behavior). A half-known placement is never mixed in — a coordinator VPC
// with a default-VPC subnet (or vice versa) would break the agent SG and
// the ASG.
func resolveAgentPlacement(ctx context.Context, cfg config.HordeAgentsConfig, coord CoordinatorRef, resolver cloud.VPCResolver) (string, string, bool, error) {
	if cfg.VPCID != "" || cfg.SubnetID != "" {
		return topology.ResolveVPC(ctx, cfg.VPCID, cfg.SubnetID, nil)
	}
	if coord.VPCID != "" && coord.SubnetID != "" {
		return coord.VPCID, coord.SubnetID, false, nil
	}
	return topology.ResolveVPC(ctx, "", "", resolver)
}
