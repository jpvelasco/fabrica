package provision

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/egress"
)

// NetworkTarget is the subnet a create flow is about to place resources in,
// after its plan has resolved the VPC and subnet.
type NetworkTarget struct {
	// Module names the command in messages, e.g. "perforce".
	Module string
	// Placement names how to set explicit placement, e.g. "perforce.vpcId and
	// perforce.subnetId" or "--vpc-id and --subnet-id".
	Placement string
	VPCID     string
	// SubnetID is the subnet the resources land in.
	SubnetID string
	// DefaultVPC is true when the plan fell back to the account default VPC.
	DefaultVPC bool
	// InternetAtBoot is true when the module downloads packages during boot.
	InternetAtBoot bool
}

// NetworkPreflight checks the target against VPC Block Public Access. It
// returns advisory warnings, and a non-nil error that blocks the create. A nil
// inspector, or an inspection denied by IAM, degrades to a warning.
func NetworkPreflight(ctx context.Context, insp cloud.NetworkInspector, t NetworkTarget) ([]string, error) {
	if insp == nil {
		return []string{"network placement was not checked: this provider cannot inspect VPC Block Public Access"}, nil
	}
	bpa, err := insp.BlockPublicAccess(ctx)
	if err != nil {
		return inspectionFailure("VPC Block Public Access", err)
	}
	if !bpa.On() {
		return nil, nil
	}
	if t.DefaultVPC {
		return nil, fmt.Errorf("VPC Block Public Access is on (%s); fabrica will not fall back to the default VPC. Set %s to a private subnet", bpa.Mode, t.Placement)
	}
	net, err := insp.SubnetEgress(ctx, t.SubnetID)
	if err != nil {
		return inspectionFailure("egress for subnet "+t.SubnetID, err)
	}
	v := egress.Classify(bpa, net, t.InternetAtBoot)
	switch v.Level {
	case egress.LevelFail:
		return nil, fmt.Errorf("%s cannot be placed: %s", t.Module, v.Message)
	case egress.LevelWarn:
		return []string{v.Message}, nil
	}
	return nil, nil
}

// inspectionFailure degrades an IAM denial to a warning and fails closed on
// any other inspection error, naming the step that failed.
func inspectionFailure(step string, err error) ([]string, error) {
	if errors.Is(err, cloud.ErrInspectionDenied) {
		return []string{"VPC Block Public Access and subnet egress were not checked (" + err.Error() + "). Grant ec2:DescribeVpcBlockPublicAccessOptions, ec2:DescribeVpcBlockPublicAccessExclusions, ec2:DescribeRouteTables, ec2:DescribeVpcEndpoints and ec2:DescribeSubnets to enable this check."}, nil
	}
	return nil, fmt.Errorf("checking %s: %w. Retry, or check AWS connectivity and credentials", step, err)
}

// WriteDryRunWarnings prints preflight warnings as aligned dry-run fields.
func WriteDryRunWarnings(out io.Writer, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(out, "  Warning:          "+w)
	}
}

// WriteWarnings prints preflight warnings ahead of a create.
func WriteWarnings(out io.Writer, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(out, "Warning: "+w)
	}
}

// PreflightFor binds NetworkPreflight to a provider and target, producing the
// CreateSpec.Preflight hook.
func PreflightFor(p cloud.Provider, t NetworkTarget) func(context.Context) ([]string, error) {
	insp := NetworkInspector(p)
	return func(ctx context.Context) ([]string, error) {
		return NetworkPreflight(ctx, insp, t)
	}
}
