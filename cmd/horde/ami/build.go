package ami

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/amirecipe"
	"github.com/jpvelasco/fabrica/internal/amissm"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/tags"
	"github.com/spf13/cobra"
)

//go:embed templates
var templateFS embed.FS

const (
	maxNameLength       = 127
	defaultBaseImage    = amirecipe.DefaultBaseImage
	defaultHordeVersion = "5.5.0"
	defaultOutputDir    = "horde-ami"
)

var (
	versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// BuildConfig holds the parameters for AMI file generation.
type BuildConfig struct {
	Identity      tags.Identity
	CloudTags     map[string]string
	Version       string
	Install       string
	BaseImage     string
	Region        string
	Name          string
	OutputDir     string
	IncludePacker bool
	DryRun        bool
	// Role is roleCoordinator (default; "" is treated the same) or roleAgent.
	Role          string
	AgentArtifact string
	AgentSHA256   string
	// NamePrefix names the default AMI (#705); zero value is the default prefix.
	NamePrefix naming.Prefix
}

// IsAgent reports whether the build targets the stock Horde agent rather than
// the coordinator stack.
func (c BuildConfig) IsAgent() bool {
	return c.Role == roleAgent
}

// RecipeTags is the tag set for the AMI, its recipe, and its snapshots.
func (c BuildConfig) RecipeTags() map[string]string {
	generated := map[string]string{
		"Name":          c.Name,
		"FabricaModule": "horde",
		"HordeVersion":  c.Version,
		"InstallMethod": c.Install,
	}
	if c.IsAgent() {
		generated["HordeRole"] = roleAgent
	}
	return amirecipe.Tags(c.Identity, c.CloudTags, generated)
}

type buildCommand struct {
	out io.Writer
	cfg BuildConfig

	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
}

func newBuildCmd(runtimeSource globals.RuntimeSource, out io.Writer) *cobra.Command {
	var cfg BuildConfig

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate files needed to build a Horde AMI",
		Long: `Generate the files needed to build a Horde AMI for use with "fabrica horde create".

Produces an EC2 Image Builder component (component.yaml) and recipe
(image-builder-recipe.json). With --include-packer, also emits a Packer HCL
template. A build-guide.md with step-by-step instructions is always included.
No AWS calls are made — all output is written locally.

Two install methods are supported:
  --install docker   Install Docker CE and run Horde via docker compose (default)
  --install native   Install .NET 8, MongoDB 7, and Redis directly from apt

Examples:
  # Generate with defaults (docker install, us-east-1)
  fabrica horde ami build

  # Native install targeting a specific Horde version
  fabrica horde ami build --install native --horde-version 5.4.0

  # Pin a specific region and output directory
  fabrica horde ami build --region us-west-2 --output-dir build/horde-ami

  # Native install with Packer template for air-gapped studios
  fabrica horde ami build --install native --include-packer --output-dir build/horde

  # Agent AMI: stage the agent tarball in S3 first (see the generated build guide)
  fabrica horde ami build --role agent --install native \
    --agent-artifact s3://my-bucket/horde/horde-agent-linux-x64.tar.gz --agent-sha256 <64-hex>

  # Preview what would be generated without writing any files
  fabrica horde ami build --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			identity, err := rt.Config.ResolveIdentity()
			if err != nil {
				return err
			}
			prefix, err := rt.Config.ResolveNamePrefix()
			if err != nil {
				return err
			}
			cfg.NamePrefix = prefix
			cfg.Identity = identity
			cfg.Region = amirecipe.ResolveRegion(cfg.Region, rt.Config.Cloud.AWS.Region)
			cfg.CloudTags = rt.Config.Cloud.AWS.Tags
			if f := cmd.InheritedFlags().Lookup("dry-run"); f != nil && f.Value.String() == "true" {
				cfg.DryRun = true
			}
			bc := &buildCommand{
				out:       out,
				cfg:       cfg,
				writeFile: os.WriteFile,
				mkdirAll:  os.MkdirAll,
			}
			return bc.run()
		},
	}

	cmd.Flags().StringVar(&cfg.Version, "horde-version", defaultHordeVersion, `Horde server version in X.Y or X.Y.Z format, or "latest"`)
	cmd.Flags().StringVar(&cfg.Install, "install", "docker", `Install method: "docker" (compose stack) or "native" (.NET + MongoDB + Redis)`)
	cmd.Flags().StringVar(&cfg.BaseImage, "base-image", defaultBaseImage, "Base Ubuntu 22.04 LTS AMI ID, or the default SSM parameter that resolves the latest Canonical image")
	cmd.Flags().StringVar(&cfg.Region, "region", "", "AWS region for the AMI build (default: cloud.aws.region, else us-east-1)")
	cmd.Flags().StringVar(&cfg.Name, "name", "", `AMI name (default: "<namePrefix>-horde-<version>", "fabrica-horde-<version>" unless namePrefix is set)`)
	cmd.Flags().StringVar(&cfg.OutputDir, "output-dir", defaultOutputDir, "Directory to write generated files to")
	cmd.Flags().BoolVar(&cfg.IncludePacker, "include-packer", false, "Also generate a Packer HCL template")
	cmd.Flags().StringVar(&cfg.Role, "role", roleCoordinator, `AMI role: "coordinator" (Horde server stack) or "agent" (stock Horde Linux agent; requires --install native)`)
	cmd.Flags().StringVar(&cfg.AgentArtifact, "agent-artifact", "", "s3:// URI of the Horde agent Linux tarball (.tar.gz); required with --role agent")
	cmd.Flags().StringVar(&cfg.AgentSHA256, "agent-sha256", "", "SHA-256 of the agent tarball (64 hex characters); required with --role agent, no default")

	return cmd
}

func (b *buildCommand) run() error {
	if err := b.validate(); err != nil {
		return err
	}
	if b.cfg.Name == "" {
		b.cfg.Name = b.defaultName()
	}
	// Image Builder rejects dots in component/recipe names, so a version like
	// 5.8.0 must not leak into the name the operator passes to
	// create-component or create-image-recipe.
	b.cfg.Name = amissm.SanitizeImageBuilderName(b.cfg.Name)

	plannedFiles := []string{"image-builder-recipe.json", "component.yaml"}
	if b.cfg.IncludePacker {
		plannedFiles = append(plannedFiles, "packer.pkr.hcl")
	}
	plannedFiles = append(plannedFiles, "build-guide.md")

	b.printHeader(plannedFiles)

	if b.cfg.DryRun {
		fmt.Fprintln(b.out, "Dry run — no files written.")
		fmt.Fprintln(b.out)
		b.printNextSteps()
		return nil
	}

	if err := b.mkdirAll(b.cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating --output-dir %q: %w", b.cfg.OutputDir, err)
	}

	if err := b.writeRendered("image-builder.json.tmpl", "image-builder-recipe.json", validateImageBuilderJSON); err != nil {
		return err
	}
	if err := b.writeRendered(b.componentTemplate(), "component.yaml", func(data []byte) error {
		return validateComponentYAML(data, b.cfg.Install)
	}); err != nil {
		return err
	}
	if b.cfg.IncludePacker {
		if err := b.writeRendered("packer.hcl.tmpl", "packer.pkr.hcl", nil); err != nil {
			return err
		}
	}
	if err := b.writeRendered("build-guide.md.tmpl", "build-guide.md", nil); err != nil {
		return err
	}

	fmt.Fprintln(b.out)
	b.printSuccess(plannedFiles)
	b.printNextSteps()
	return nil
}

// defaultName is the AMI name when --name is not set. The role is part of
// the name so a coordinator and an agent built from the same version never
// collide.
func (b *buildCommand) defaultName() string {
	if b.cfg.IsAgent() {
		return fmt.Sprintf("%s-%s", b.cfg.NamePrefix.For("horde", "agent"), b.cfg.Version)
	}
	return fmt.Sprintf("%s-%s", b.cfg.NamePrefix.For("horde"), b.cfg.Version)
}

// componentTemplate selects the Image Builder component document for the role.
func (b *buildCommand) componentTemplate() string {
	if b.cfg.IsAgent() {
		return "agent-component.yaml.tmpl"
	}
	return "component.yaml.tmpl"
}

// validateRole checks the role and its agent-only flags. An empty role is
// the coordinator, so programmatic callers that predate --role keep working.
func (b *buildCommand) validateRole() error {
	switch b.cfg.Role {
	case roleCoordinator, "":
		if b.cfg.AgentArtifact != "" || b.cfg.AgentSHA256 != "" {
			return errors.New("--agent-artifact and --agent-sha256 are only valid with --role agent")
		}
		return nil
	case roleAgent:
		if b.cfg.Install != "native" {
			return fmt.Errorf("--role agent requires --install native (the agent is the stock Linux agent, not the Docker stack); got --install %q", b.cfg.Install)
		}
		return validateAgentArtifact(b.cfg.AgentArtifact, b.cfg.AgentSHA256)
	default:
		return fmt.Errorf("--role must be %q or %q; got %q", roleCoordinator, roleAgent, b.cfg.Role)
	}
}

func (b *buildCommand) validate() error {
	if err := b.validateRole(); err != nil {
		return err
	}
	if b.cfg.Version == "" {
		return errors.New("--horde-version is required")
	}
	if b.cfg.Version != "latest" && !versionRE.MatchString(b.cfg.Version) {
		return fmt.Errorf("--horde-version must be in the format X.Y or X.Y.Z (e.g. 5.4.0), or the literal %q; got %q", "latest", b.cfg.Version)
	}
	if b.cfg.Install != "docker" && b.cfg.Install != "native" {
		return fmt.Errorf("--install must be %q or %q; got %q", "docker", "native", b.cfg.Install)
	}
	if err := amirecipe.ValidateBaseImage(b.cfg.BaseImage); err != nil {
		return err
	}
	if err := amirecipe.ValidateRegion(b.cfg.Region); err != nil {
		return err
	}
	if b.cfg.Name != "" {
		if !nameRE.MatchString(b.cfg.Name) {
			return fmt.Errorf("--name can only contain letters, numbers, dots, underscores, and hyphens; got %q", b.cfg.Name)
		}
		if len(b.cfg.Name) > maxNameLength {
			return fmt.Errorf("--name must be %d characters or fewer (Image Builder limit); got %d", maxNameLength, len(b.cfg.Name))
		}
	}
	if b.cfg.OutputDir == "" {
		return errors.New("--output-dir is required")
	}
	return nil
}

func (b *buildCommand) printHeader(planned []string) {
	verb := "Generating"
	if b.cfg.DryRun {
		verb = "Would generate"
	}
	fmt.Fprintf(b.out, "%s %s build files\n", verb, b.amiKind())
	fmt.Fprintf(b.out, "  Horde version:  %s\n", b.cfg.Version)
	fmt.Fprintf(b.out, "  Install method: %s\n", b.cfg.Install)
	fmt.Fprintf(b.out, "  Base image:     %s\n", b.cfg.BaseImage)
	fmt.Fprintf(b.out, "  Region:         %s\n", b.cfg.Region)
	fmt.Fprintf(b.out, "  AMI name:       %s\n", b.cfg.Name)
	fmt.Fprintf(b.out, "  Output dir:     %s\n", b.cfg.OutputDir)
	fmt.Fprintln(b.out)
	fmt.Fprintf(b.out, "%s %d files in %s/:\n", planVerb(b.cfg.DryRun), len(planned), b.cfg.OutputDir)
	for _, f := range planned {
		fmt.Fprintf(b.out, "  %s\n", f)
	}
	fmt.Fprintln(b.out)
}

// amiKind names the AMI in user-facing output.
func (b *buildCommand) amiKind() string {
	if b.cfg.IsAgent() {
		return "Horde agent AMI"
	}
	return "Horde AMI"
}

func planVerb(dryRun bool) string {
	if dryRun {
		return "Would write"
	}
	return "Writing"
}

func (b *buildCommand) writeRendered(tmplName, outName string, validate func([]byte) error) error {
	data, err := b.renderTemplate(tmplName, b.cfg)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(data); err != nil {
			return fmt.Errorf("rendered %s is invalid: %w", outName, err)
		}
	}
	path := filepath.Join(b.cfg.OutputDir, outName)
	if err := b.writeFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(b.out, "  wrote %s\n", path)
	return nil
}

func (b *buildCommand) renderTemplate(name string, data any) ([]byte, error) {
	raw, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading template %s: %w", name, err)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	tmpl, err := template.New(name).Funcs(amirecipe.TemplateFuncs()).Funcs(template.FuncMap{
		"indent":                 indent,
		"ensureSSM":              amissm.EnsureScript,
		"requireSSM":             func() string { return amissm.RequireEnabledScript(false) },
		"awsCliPresenceCheck":    awsCliPresenceCheckScript,
		"awsCliVerifiedInstall":  awsCliVerifiedInstallScript,
		"dockerInstall":          dockerInstallScript,
		"dotnetInstall":          dotnetInstallScript,
		"mongoDBInstall":         mongoDBInstallScript,
		"dockerBake":             dockerBakeScript,
		"hordeDockerUnit":        func() string { return hordeDockerUnitText },
		"hclInline":              hclInline,
		"hordeDockerUnitCommand": hordeDockerUnitCommand,
		"agentBake":              agentBakeScript,
		"agentValidate":          agentValidateScript,
	}).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parsing template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func indent(spaces int, value string) string {
	prefix := strings.Repeat(" ", spaces)
	return prefix + strings.ReplaceAll(strings.TrimSuffix(value, "\n"), "\n", "\n"+prefix)
}

// pipelineTargetHint says which fabrica.yaml key takes the baked AMI ID.
func (b *buildCommand) pipelineTargetHint() string {
	if b.cfg.IsAgent() {
		return "     When the pipeline finishes, set horde.agents.amiId in fabrica.yaml."
	}
	return "     When the pipeline finishes, set horde.amiId in fabrica.yaml."
}

func (b *buildCommand) printSuccess(files []string) {
	fmt.Fprintf(b.out, "Generated %d files in %s/:\n", len(files), b.cfg.OutputDir)
	for _, f := range files {
		fmt.Fprintf(b.out, "  %s/%s\n", b.cfg.OutputDir, f)
	}
	fmt.Fprintln(b.out)
}

func (b *buildCommand) printNextSteps() {
	fmt.Fprintln(b.out, "Next steps:")
	fmt.Fprintln(b.out)
	fmt.Fprintf(b.out, "  1. Read %s\n", filepath.Join(b.cfg.OutputDir, "build-guide.md"))
	fmt.Fprintln(b.out)
	fmt.Fprintln(b.out, "  2. Create the Image Builder component:")
	fmt.Fprintf(b.out, "       aws imagebuilder create-component \\\n")
	fmt.Fprintf(b.out, "         --region %s \\\n", b.cfg.Region)
	fmt.Fprintf(b.out, "         --name %s-%s \\\n", b.cfg.Name, b.cfg.Install)
	fmt.Fprintf(b.out, "         --semantic-version 1.0.0 \\\n")
	fmt.Fprintf(b.out, "         --platform Linux \\\n")
	fmt.Fprintf(b.out, "         --supported-os-versions \"Ubuntu 22\" \\\n")
	fmt.Fprintf(b.out, "         --data file://%s\n", filepath.Join(b.cfg.OutputDir, "component.yaml"))
	fmt.Fprintln(b.out)
	fmt.Fprintln(b.out, "  3. Replace REPLACE_WITH_CUSTOM_COMPONENT_ARN in image-builder-recipe.json")
	fmt.Fprintln(b.out, "     with the ARN returned in step 2, then:")
	fmt.Fprintf(b.out, "       aws imagebuilder create-image-recipe \\\n")
	fmt.Fprintf(b.out, "         --region %s \\\n", b.cfg.Region)
	fmt.Fprintf(b.out, "         --cli-input-json file://%s\n", filepath.Join(b.cfg.OutputDir, "image-builder-recipe.json"))
	fmt.Fprintln(b.out)
	fmt.Fprintln(b.out, "  4. Wire up an Image Builder pipeline (one-time per account) and run it.")
	fmt.Fprintln(b.out, b.pipelineTargetHint())
	if b.cfg.IncludePacker {
		fmt.Fprintln(b.out)
		fmt.Fprintln(b.out, "  Alternative: build with Packer instead of Image Builder:")
		fmt.Fprintf(b.out, "       packer init %s\n", filepath.Join(b.cfg.OutputDir, "packer.pkr.hcl"))
		fmt.Fprintf(b.out, "       packer build %s\n", filepath.Join(b.cfg.OutputDir, "packer.pkr.hcl"))
	}
}
