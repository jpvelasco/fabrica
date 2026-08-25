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
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template — renders
	// local YAML/JSON/HCL/MD build artifacts, not HTML; no server or browser consumes this output.
	"text/template"

	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/spf13/cobra"
)

//go:embed templates
var templateFS embed.FS

const (
	maxNameLength      = 127
	defaultBaseImage   = "ami-0c7217cdde317cfec"
	defaultLoreVersion = "5.5.0"
	defaultRegion      = "us-east-1"
	defaultOutputDir   = "lore-ami"
)

var (
	versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)
	amiRE     = regexp.MustCompile(`^ami-[0-9a-f]{8,}$`)
	regionRE  = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// BuildConfig holds the parameters for AMI file generation.
type BuildConfig struct {
	Version       string
	BaseImage     string
	Region        string
	Name          string
	OutputDir     string
	IncludePacker bool
	DryRun        bool
}

type buildCommand struct {
	out io.Writer
	cfg BuildConfig

	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
}

type templateData struct {
	BuildConfig
	InstallScript             string
	BakeVerificationScript    string
	RuntimeVerificationScript string
	Contract                  lore.AMIContract
}

func newBuildCmd(out io.Writer) *cobra.Command {
	var cfg BuildConfig

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate files needed to build a Lore AMI",
		Long: `Generate the files needed to build a Lore AMI for use with "fabrica lore create".

Produces an EC2 Image Builder component (component.yaml) and recipe
(image-builder-recipe.json). With --include-packer, also emits a Packer HCL
template. A build-guide.md with step-by-step instructions is always included.
No AWS calls are made — all output is written locally.

The Lore AMI contains only the loreserver binary — no MongoDB, Redis, or
Horde server. Fabrica's cloud-init handles store configuration and startup.

Examples:
  # Generate with defaults (us-east-1)
  fabrica lore ami build

  # Target a specific Lore version
  fabrica lore ami build --lore-version 5.5.0

  # Pin a specific region and output directory
  fabrica lore ami build --region us-west-2 --output-dir build/lore-ami

  # Include Packer template for air-gapped studios
  fabrica lore ami build --include-packer --output-dir build/lore

  # Preview what would be generated without writing any files
  fabrica lore ami build --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

	cmd.Flags().StringVar(&cfg.Version, "lore-version", defaultLoreVersion, `Lore server version in X.Y or X.Y.Z format, or "latest"`)
	cmd.Flags().StringVar(&cfg.BaseImage, "base-image", defaultBaseImage, "Base Ubuntu 22.04 LTS AMI ID")
	cmd.Flags().StringVar(&cfg.Region, "region", defaultRegion, "AWS region for the AMI build (used in component ARNs)")
	cmd.Flags().StringVar(&cfg.Name, "name", "", `AMI name (default: "fabrica-lore-<version>")`)
	cmd.Flags().StringVar(&cfg.OutputDir, "output-dir", defaultOutputDir, "Directory to write generated files to")
	cmd.Flags().BoolVar(&cfg.IncludePacker, "include-packer", false, "Also generate a Packer HCL template")

	return cmd
}

func (b *buildCommand) run() error {
	if err := b.validate(); err != nil {
		return err
	}
	if b.cfg.Name == "" {
		b.cfg.Name = fmt.Sprintf("fabrica-lore-%s", b.cfg.Version)
	}
	data, err := b.templateData()
	if err != nil {
		return err
	}

	plannedFiles := []string{
		"image-builder-recipe.json",
		"component.yaml",
		"install-lore.sh",
		"verify-lore-ami-bake.sh",
		"verify-lore-ami-runtime.sh",
	}
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

	if err := b.writeRendered("image-builder.json.tmpl", "image-builder-recipe.json", data, validateImageBuilderJSON); err != nil {
		return err
	}
	if err := b.writeRendered("component.yaml.tmpl", "component.yaml", data, validateComponentYAML); err != nil {
		return err
	}
	if err := b.writeScript("install-lore.sh", data.InstallScript); err != nil {
		return err
	}
	if err := b.writeScript("verify-lore-ami-bake.sh", data.BakeVerificationScript); err != nil {
		return err
	}
	if err := b.writeScript("verify-lore-ami-runtime.sh", data.RuntimeVerificationScript); err != nil {
		return err
	}
	if b.cfg.IncludePacker {
		if err := b.writeRendered("packer.hcl.tmpl", "packer.pkr.hcl", data, nil); err != nil {
			return err
		}
	}
	if err := b.writeRendered("build-guide.md.tmpl", "build-guide.md", data, nil); err != nil {
		return err
	}

	fmt.Fprintln(b.out)
	b.printSuccess(plannedFiles)
	b.printNextSteps()
	return nil
}

func (b *buildCommand) validate() error {
	if b.cfg.Version == "" {
		return errors.New("--lore-version is required")
	}
	if b.cfg.Version != "latest" && !versionRE.MatchString(b.cfg.Version) {
		return fmt.Errorf("--lore-version must be in the format X.Y or X.Y.Z (e.g. 5.5.0), or the literal %q; got %q", "latest", b.cfg.Version)
	}
	if !amiRE.MatchString(b.cfg.BaseImage) {
		return fmt.Errorf("--base-image must be a valid AMI ID (ami- followed by 8+ hex digits); got %q", b.cfg.BaseImage)
	}
	if !regionRE.MatchString(b.cfg.Region) {
		return fmt.Errorf("--region must be a valid AWS region (e.g. us-east-1, eu-west-2); got %q", b.cfg.Region)
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
	fmt.Fprintf(b.out, "%s Lore AMI build files\n", verb)
	fmt.Fprintf(b.out, "  Lore version:   %s\n", b.cfg.Version)
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

func planVerb(dryRun bool) string {
	if dryRun {
		return "Would write"
	}
	return "Writing"
}

func (b *buildCommand) templateData() (templateData, error) {
	contract := lore.DefaultAMIContract()
	installScript, err := contract.InstallScript("/tmp/lore-bin")
	if err != nil {
		return templateData{}, fmt.Errorf("generating Lore AMI install script: %w", err)
	}
	bakeVerificationScript, err := contract.BakeVerificationScript()
	if err != nil {
		return templateData{}, fmt.Errorf("generating Lore AMI bake verification script: %w", err)
	}
	runtimeVerificationScript, err := contract.VerificationScript()
	if err != nil {
		return templateData{}, fmt.Errorf("generating Lore AMI runtime verification script: %w", err)
	}
	return templateData{
		BuildConfig:               b.cfg,
		InstallScript:             installScript,
		BakeVerificationScript:    bakeVerificationScript,
		RuntimeVerificationScript: runtimeVerificationScript,
		Contract:                  contract,
	}, nil
}

func (b *buildCommand) writeRendered(tmplName, outName string, data any, validate func([]byte) error) error {
	rendered, err := b.renderTemplate(tmplName, data)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(rendered); err != nil {
			return fmt.Errorf("rendered %s is invalid: %w", outName, err)
		}
	}
	path := filepath.Join(b.cfg.OutputDir, outName)
	if err := b.writeFile(path, rendered, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(b.out, "  wrote %s\n", path)
	return nil
}

func (b *buildCommand) writeScript(name, script string) error {
	path := filepath.Join(b.cfg.OutputDir, name)
	if err := b.writeFile(path, []byte(script), 0755); err != nil {
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
	tmpl, err := template.New(name).Funcs(template.FuncMap{"indent": indent}).Option("missingkey=error").Parse(string(raw))
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
	fmt.Fprintf(b.out, "         --name %s \\\n", b.cfg.Name)
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
	fmt.Fprintln(b.out, "     When the pipeline finishes, set lore.amiId in fabrica.yaml.")
	if b.cfg.IncludePacker {
		fmt.Fprintln(b.out)
		fmt.Fprintln(b.out, "  Alternative: build with Packer instead of Image Builder:")
		fmt.Fprintf(b.out, "       packer init %s\n", filepath.Join(b.cfg.OutputDir, "packer.pkr.hcl"))
		fmt.Fprintf(b.out, "       packer build %s\n", filepath.Join(b.cfg.OutputDir, "packer.pkr.hcl"))
	}
}
