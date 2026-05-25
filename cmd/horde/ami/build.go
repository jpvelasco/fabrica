package ami

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

//go:embed templates
var templateFS embed.FS

// BuildConfig holds the parameters for AMI file generation.
type BuildConfig struct {
	Version       string
	Install       string // "docker" or "native"
	BaseImage     string
	Name          string
	OutputDir     string
	IncludePacker bool
}

type buildCommand struct {
	out io.Writer
	cfg BuildConfig

	// seam for testing
	writeFile func(path string, data []byte, perm os.FileMode) error
}

func newBuildCmd(out io.Writer) *cobra.Command {
	var cfg BuildConfig

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate files needed to build a Horde AMI",
		Long: `Generate files needed to build a Horde AMI.

By default, outputs an AWS EC2 Image Builder recipe JSON. Use --include-packer
to also generate a Packer HCL template.

The generated files are written to --output-dir (default: ./horde-ami).
A build-guide.md is always included with step-by-step instructions.

Examples:
  fabrica horde ami build
  fabrica horde ami build --install native
  fabrica horde ami build --include-packer --output-dir /tmp/horde-build
  fabrica horde ami build --horde-version 5.5.0`,
		RunE: func(cmd *cobra.Command, args []string) error {
			bc := buildCommand{
				out:       out,
				cfg:       cfg,
				writeFile: os.WriteFile,
			}
			return bc.run()
		},
	}

	cmd.Flags().StringVar(&cfg.Version, "horde-version", "5.5.0", "Horde server version to install")
	cmd.Flags().StringVar(&cfg.Install, "install", "docker", `Installation method: "docker" or "native"`)
	cmd.Flags().StringVar(&cfg.BaseImage, "base-image", "ami-0c7217cdde317cfec", "Base Ubuntu 22.04 LTS AMI ID (us-east-1)")
	cmd.Flags().StringVar(&cfg.Name, "name", "", `AMI name (default: "fabrica-horde-<version>")`)
	cmd.Flags().StringVar(&cfg.OutputDir, "output-dir", "horde-ami", "Directory to write generated files to")
	cmd.Flags().BoolVar(&cfg.IncludePacker, "include-packer", false, "Also generate a Packer HCL template")

	return cmd
}

func (b *buildCommand) run() error {
	if b.cfg.Install != "docker" && b.cfg.Install != "native" {
		return fmt.Errorf("--install must be \"docker\" or \"native\", got %q", b.cfg.Install)
	}
	if b.cfg.Name == "" {
		b.cfg.Name = fmt.Sprintf("fabrica-horde-%s", b.cfg.Version)
	}

	if err := os.MkdirAll(b.cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating output dir %s: %w", b.cfg.OutputDir, err)
	}

	fmt.Fprintf(b.out, "Generating Horde AMI build files\n")
	fmt.Fprintf(b.out, "  Horde version:  %s\n", b.cfg.Version)
	fmt.Fprintf(b.out, "  Install method: %s\n", b.cfg.Install)
	fmt.Fprintf(b.out, "  Base image:     %s\n", b.cfg.BaseImage)
	fmt.Fprintf(b.out, "  AMI name:       %s\n", b.cfg.Name)
	fmt.Fprintf(b.out, "  Output dir:     %s\n", b.cfg.OutputDir)
	fmt.Fprintln(b.out)

	if err := b.writeImageBuilderRecipe(); err != nil {
		return err
	}

	if b.cfg.IncludePacker {
		if err := b.writePackerTemplate(); err != nil {
			return err
		}
	}

	if err := b.writeBuildGuide(); err != nil {
		return err
	}

	fmt.Fprintln(b.out, "Next steps:")
	fmt.Fprintf(b.out, "  See %s for instructions.\n", filepath.Join(b.cfg.OutputDir, "build-guide.md"))
	return nil
}

func (b *buildCommand) renderTemplate(name string, data any) ([]byte, error) {
	tmplData, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("reading template %s: %w", name, err)
	}

	funcMap := template.FuncMap{
		"currentYear": func() int { return time.Now().UTC().Year() },
	}

	tmpl, err := template.New(name).Funcs(funcMap).Parse(string(tmplData))
	if err != nil {
		return nil, fmt.Errorf("parsing template %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func (b *buildCommand) writeImageBuilderRecipe() error {
	out, err := b.renderTemplate("image-builder.json.tmpl", b.cfg)
	if err != nil {
		return err
	}
	if err := validateImageBuilderJSON(out); err != nil {
		return fmt.Errorf("generated recipe is invalid: %w", err)
	}
	path := filepath.Join(b.cfg.OutputDir, "image-builder-recipe.json")
	if err := b.writeFile(path, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(b.out, "  wrote %s\n", path)
	return nil
}

func (b *buildCommand) writePackerTemplate() error {
	out, err := b.renderTemplate("packer.hcl.tmpl", b.cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(b.cfg.OutputDir, "packer.pkr.hcl")
	if err := b.writeFile(path, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(b.out, "  wrote %s\n", path)
	return nil
}

func (b *buildCommand) writeBuildGuide() error {
	out, err := b.renderTemplate("build-guide.md.tmpl", b.cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(b.cfg.OutputDir, "build-guide.md")
	if err := b.writeFile(path, out, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(b.out, "  wrote %s\n", path)
	return nil
}
