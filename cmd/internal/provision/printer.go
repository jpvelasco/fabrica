package provision

import (
	"fmt"
	"io"
	"strings"

	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
)

const lineWidth = 58

// PlanInfo holds the shared fields printed by every create command's plan output.
type PlanInfo struct {
	Account      string
	Region       string
	InstanceType string
	VolumeSize   int
	AllowedCIDR  string
	VPCID        string
	DefaultVPC   bool
}

// PlanField is one line of module-specific detail in the plan output.
type PlanField struct {
	Key   string // label (e.g. "Helix Core")
	Value string // value (e.g. "2024.2")
}

// DryRunSpec describes the inputs for a dry-run plan display.
type DryRunSpec struct {
	Title         string
	Info          PlanInfo
	ExtraFields   []PlanField
	Resources     []string
	CostResources []fabricacost.Resource
	Costs         *fabricacost.Registry
	// CidrWarning is the module-specific warning printed when AllowedCIDR is
	// "0.0.0.0/0". Pass empty string to suppress.
	CidrWarning string
	// VPCNote is the note printed when DefaultVPC is true. Defaults to the
	// standard message when empty.
	VPCNote string
	// RawBetween is called between the VPC block and the blank line before
	// "Resources to create:". Use for module-specific warnings, tips, etc.
	RawBetween func(io.Writer)
}

// DryRun prints the full dry-run plan: title with "(dry run)" suffix,
// common fields (account, region, instance type, volume), module-specific
// extra fields, CIDR warning when applicable, VPC line, any raw output from
// RawBetween, resource list, cost estimate, and the "run without --dry-run" footer.
func DryRun(out io.Writer, spec DryRunSpec) {
	fmt.Fprintln(out, spec.Title+" (dry run)")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  AWS account:      %s\n", spec.Info.Account)
	fmt.Fprintf(out, "  AWS region:       %s\n", spec.Info.Region)
	fmt.Fprintf(out, "  Instance type:    %s\n", spec.Info.InstanceType)
	fmt.Fprintf(out, "  Data volume:      %d GiB gp3\n", spec.Info.VolumeSize)
	for _, f := range spec.ExtraFields {
		fmt.Fprintf(out, "  %-18s%s\n", f.Key+":", f.Value)
	}
	if spec.Info.AllowedCIDR == "0.0.0.0/0" && spec.CidrWarning != "" {
		fmt.Fprintln(out, "  Warning:          "+spec.CidrWarning)
	}
	if spec.Info.DefaultVPC {
		fmt.Fprintf(out, "  VPC:              default (%s)\n", spec.Info.VPCID)
		note := spec.VPCNote
		if note == "" {
			note = "Default VPC used. Configure a dedicated VPC for production."
		}
		fmt.Fprintln(out, "  Note:             "+note)
	} else if spec.Info.VPCID != "" {
		fmt.Fprintf(out, "  VPC:              %s\n", spec.Info.VPCID)
	}

	if spec.RawBetween != nil {
		spec.RawBetween(out)
	}

	fmt.Fprintln(out)

	fmt.Fprintln(out, "Resources to create:")
	for _, r := range spec.Resources {
		fmt.Fprintf(out, "  %s\n", r)
	}
	fmt.Fprintln(out)

	spec.Costs.EstimateAll(spec.CostResources).Render(out, lineWidth)
	fmt.Fprintln(out, "Run without --dry-run to proceed.")
}

// ApplyPlan prints the apply plan: title, common fields (account, region,
// instance type, volume), module-specific extra fields, and resource list.
// Does not print cost estimate or VPC note.
func ApplyPlan(out io.Writer, title string, info PlanInfo, extraFields []PlanField, resources []string) {
	fmt.Fprintln(out, title)
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  AWS account:      %s\n", info.Account)
	fmt.Fprintf(out, "  AWS region:       %s\n", info.Region)
	fmt.Fprintf(out, "  Instance type:    %s\n", info.InstanceType)
	fmt.Fprintf(out, "  Data volume:      %d GiB gp3\n", info.VolumeSize)
	for _, f := range extraFields {
		fmt.Fprintf(out, "  %-18s%s\n", f.Key+":", f.Value)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Resources to create:")
	for _, r := range resources {
		fmt.Fprintf(out, "  %s\n", r)
	}
}

// PostCreateSpec describes the inputs for a post-creation success message.
type PostCreateSpec struct {
	Title        string
	InstanceID   string
	StatusDetail string
	Details      []PlanField
	NextSteps    []string
	// RawAfter is called after the standard post-create output.
	// Use for module-specific warnings, notes, and logging instructions.
	RawAfter func(io.Writer)
}

// PostCreate prints the standard post-creation success message with
// instance ID, status detail, module-specific details, and next steps.
func PostCreate(out io.Writer, spec PostCreateSpec) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, spec.Title+" provisioned.")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Instance ID:   %s\n", spec.InstanceID)
	if spec.StatusDetail != "" {
		fmt.Fprintf(out, "  Status:        %s\n", spec.StatusDetail)
	}
	for _, d := range spec.Details {
		fmt.Fprintf(out, "  %-16s %s\n", d.Key+":", d.Value)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	for _, step := range spec.NextSteps {
		fmt.Fprintln(out, "  "+step)
	}
	if spec.RawAfter != nil {
		spec.RawAfter(out)
	}
}
