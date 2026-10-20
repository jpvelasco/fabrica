package ami

import (
	"bytes"
	"encoding/json"
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
	"github.com/jpvelasco/fabrica/internal/workstation"
	"github.com/spf13/cobra"
)

const (
	defaultBaseImage = "ami-0c7217cdde317cfec"
	defaultRegion    = "us-east-1"
	defaultOutputDir = "workstation-ami"
	defaultName      = "fabrica-workstation-dcv"
	// defaultRootDevice is the root device of Canonical's Ubuntu 22.04 server
	// AMIs, the documented base image. Pass --root-device when the base image
	// reports another RootDeviceName; verify-ami.sh fails the bake if the
	// recipe's mapping missed the real root (#507).
	defaultRootDevice = "/dev/sda1"
)

var (
	amiRE    = regexp.MustCompile(`^ami-[0-9a-f]{8,}$`)
	regionRE = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]$`)
	nameRE   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type BuildConfig struct {
	BaseImage  string
	Region     string
	Name       string
	OutputDir  string
	RootDevice string
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

The recipe bakes on an encrypted root volume, so the AMI's root snapshot is
encrypted. --root-device must be the base image's RootDeviceName (the
default, /dev/sda1, is Canonical Ubuntu's). Run the generated verify-ami.sh against the baked AMI before you
record it: it fails unless every EBS snapshot is encrypted and tagged
ManagedBy=fabrica.

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
	cmd.Flags().StringVar(&cfg.RootDevice, "root-device", defaultRootDevice, "Root device name of --base-image (its RootDeviceName); the recipe encrypts this device")
	return cmd
}

func (c buildCommand) run() error {
	if err := c.validate(); err != nil {
		return err
	}
	files := []string{"component.yaml", "image-builder-recipe.json", "build-guide.md", "verify-ami.sh"}
	fmt.Fprintf(c.out, "Generating workstation DCV AMI build files\n")
	fmt.Fprintf(c.out, "  Base image: %s\n  Region:     %s\n  Name:       %s\n  Root:       %s (encrypted)\n  Output dir: %s\n\n", c.cfg.BaseImage, c.cfg.Region, c.cfg.Name, c.cfg.RootDevice, c.cfg.OutputDir)
	if err := c.mkdirAll(c.cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating --output-dir %q: %w", c.cfg.OutputDir, err)
	}
	data := struct {
		BuildConfig
		EnsureSSM  string
		RequireSSM string
	}{BuildConfig: c.cfg, EnsureSSM: amissm.EnsureScript(), RequireSSM: amissm.RequireEnabledScript(false)}
	for _, pair := range []struct {
		tmpl, out string
		perm      os.FileMode
	}{
		{"component.yaml.tmpl", "component.yaml", 0644},
		{"image-builder.json.tmpl", "image-builder-recipe.json", 0644},
		{"build-guide.md.tmpl", "build-guide.md", 0644},
		{"verify-ami.sh.tmpl", "verify-ami.sh", 0755},
	} {
		body, err := c.render(pair.tmpl, data)
		if err != nil {
			return err
		}
		if pair.out == "image-builder-recipe.json" {
			if err := validateRecipeEncryption(body, c.cfg.RootDevice); err != nil {
				return err
			}
		}
		path := filepath.Join(c.cfg.OutputDir, pair.out)
		if err := c.writeFile(path, body, pair.perm); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		fmt.Fprintf(c.out, "  wrote %s\n", path)
	}
	fmt.Fprintf(c.out, "\nGenerated %d files in %s/. See %s.\n", len(files), c.cfg.OutputDir, filepath.Join(c.cfg.OutputDir, "build-guide.md"))
	return nil
}

// validateRecipeEncryption fails closed unless the rendered Image Builder
// recipe maps rootDevice with "encrypted": true (#507). Image Builder
// applies the recipe's block device mappings to the build instance and the
// output AMI, so this flag is what makes the baked snapshot encrypted when
// the parent image's snapshot is not and account EBS default encryption is
// off. Generation refuses to write a recipe that would bake an unencrypted
// root snapshot.
func validateRecipeEncryption(recipe []byte, rootDevice string) error {
	if !workstation.ValidRootDeviceName(rootDevice) {
		return fmt.Errorf("root device %q is not an EC2 device name; refusing to write a recipe that cannot encrypt the root", rootDevice)
	}
	var doc struct {
		BlockDeviceMappings []struct {
			DeviceName string `json:"deviceName"`
			Ebs        *struct {
				Encrypted *bool `json:"encrypted"`
			} `json:"ebs"`
		} `json:"blockDeviceMappings"`
	}
	if err := json.Unmarshal(recipe, &doc); err != nil {
		return fmt.Errorf("image-builder-recipe.json is not valid JSON: %w", err)
	}
	for _, m := range doc.BlockDeviceMappings {
		if m.DeviceName != rootDevice {
			continue
		}
		if m.Ebs == nil || m.Ebs.Encrypted == nil || !*m.Ebs.Encrypted {
			return fmt.Errorf("image-builder-recipe.json must map %s with \"encrypted\": true so the baked root snapshot is encrypted", rootDevice)
		}
		return nil
	}
	return fmt.Errorf("image-builder-recipe.json has no %s block device mapping; the root volume must be mapped with \"encrypted\": true", rootDevice)
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
	if !workstation.ValidRootDeviceName(c.cfg.RootDevice) {
		return fmt.Errorf("--root-device must be the base image's root device name (for example /dev/sda1 or /dev/xvda); got %q", c.cfg.RootDevice)
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
