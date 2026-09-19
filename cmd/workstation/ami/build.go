package ami

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template — renders
	// local YAML/JSON/MD build artifacts, not HTML; no server or browser consumes this output.
	"text/template"

	"github.com/jpvelasco/fabrica/internal/amissm"
	"github.com/spf13/cobra"
)

const (
	defaultBaseImage = "ami-0c7217cdde317cfec"
	defaultRegion    = "us-east-1"
	defaultOutputDir = "workstation-ami"
	defaultName      = "fabrica-workstation-dcv"
)

var (
	amiRE    = regexp.MustCompile(`^ami-[0-9a-f]{8,}$`)
	regionRE = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]$`)
	nameRE   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type BuildConfig struct {
	BaseImage string
	Region    string
	Name      string
	OutputDir string
}

type buildCommand struct {
	out       io.Writer
	cfg       BuildConfig
	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
}

func newBuildCmd(out io.Writer) *cobra.Command {
	var cfg BuildConfig
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate files needed to build a NICE DCV workstation AMI",
		Long: `Generate Image Builder artifacts for a NICE DCV workstation AMI.

The AMI must include NICE DCV server and Amazon SSM Agent. Fabrica's
workstation create only configures a DCV session — it does not install DCV.
Stock Ubuntu AMIs are rejected at create time.

No AWS calls. Record the resulting AMI ID in workstation.amiId.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := buildCommand{out: out, cfg: cfg, writeFile: os.WriteFile, mkdirAll: os.MkdirAll}
			return c.run()
		},
	}
	cmd.Flags().StringVar(&cfg.BaseImage, "base-image", defaultBaseImage, "Base AMI ID (Ubuntu 22.04)")
	cmd.Flags().StringVar(&cfg.Region, "region", defaultRegion, "AWS region")
	cmd.Flags().StringVar(&cfg.Name, "name", defaultName, "Image name")
	cmd.Flags().StringVar(&cfg.OutputDir, "output-dir", defaultOutputDir, "Output directory")
	return cmd
}

func (c buildCommand) run() error {
	if err := c.validate(); err != nil {
		return err
	}
	files := []string{"component.yaml", "image-builder-recipe.json", "build-guide.md"}
	fmt.Fprintf(c.out, "Generating workstation DCV AMI build files\n")
	fmt.Fprintf(c.out, "  Base image: %s\n  Region:     %s\n  Name:       %s\n  Output dir: %s\n\n", c.cfg.BaseImage, c.cfg.Region, c.cfg.Name, c.cfg.OutputDir)
	if err := c.mkdirAll(c.cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating --output-dir %q: %w", c.cfg.OutputDir, err)
	}
	data := struct {
		BuildConfig
		EnsureSSM  string
		RequireSSM string
	}{BuildConfig: c.cfg, EnsureSSM: amissm.EnsureScript(), RequireSSM: amissm.RequireEnabledScript(false)}
	for _, pair := range []struct{ tmpl, out string }{
		{"component.yaml.tmpl", "component.yaml"},
		{"image-builder.json.tmpl", "image-builder-recipe.json"},
		{"build-guide.md.tmpl", "build-guide.md"},
	} {
		body, err := c.render(pair.tmpl, data)
		if err != nil {
			return err
		}
		path := filepath.Join(c.cfg.OutputDir, pair.out)
		if err := c.writeFile(path, body, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(c.out, "  wrote %s\n", path)
	}
	fmt.Fprintf(c.out, "\nGenerated %d files in %s/. See %s.\n", len(files), c.cfg.OutputDir, filepath.Join(c.cfg.OutputDir, "build-guide.md"))
	return nil
}

func (c buildCommand) validate() error {
	if !amiRE.MatchString(c.cfg.BaseImage) {
		return fmt.Errorf("--base-image must be a valid AMI ID; got %q", c.cfg.BaseImage)
	}
	if !regionRE.MatchString(c.cfg.Region) {
		return fmt.Errorf("--region must be a valid AWS region; got %q", c.cfg.Region)
	}
	if c.cfg.Name == "" || !nameRE.MatchString(c.cfg.Name) {
		return fmt.Errorf("--name can only contain letters, numbers, dots, underscores, and hyphens")
	}
	if c.cfg.OutputDir == "" {
		return fmt.Errorf("--output-dir is required")
	}
	return nil
}

func (c buildCommand) render(name string, data any) ([]byte, error) {
	raw, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading template %s: %w", name, err)
	}
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	tmpl, err := template.New(name).Funcs(template.FuncMap{
		"indent": indent,
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
