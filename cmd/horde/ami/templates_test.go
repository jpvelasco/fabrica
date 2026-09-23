package ami

import (
	"encoding/json"
	"strings"
	"testing"
)

// requireDockerBake asserts that rendered docker-install output contains the
// shared bake markers (compose + image bake) and the full-path aws pull.
// Both the Image Builder component and the Packer template must carry them.
func requireDockerBake(t *testing.T, rendered string) {
	t.Helper()
	for _, marker := range dockerComponentMarkers {
		if !strings.Contains(rendered, marker) {
			t.Errorf("rendered docker output is missing required bake marker %q", marker)
		}
	}
	if !strings.Contains(rendered, "/usr/local/bin/aws ecr get-login-password") {
		t.Error("docker bake should pull via full-path /usr/local/bin/aws (not on SSM PATH)")
	}
	if strings.Contains(rendered, "get-authorization-token") {
		t.Error("docker bake must use get-login-password (the raw authorization token is base64 AWS:password and cannot feed docker login)")
	}
	// The region fallback must be guarded: a bare
	// `ECR_REGION=$(aws configure get region)` aborts the whole bake under
	// `set -e` on a stock instance (no local AWS config) with no message.
	if strings.Contains(rendered, "ECR_REGION=$(/usr/local/bin/aws configure get region)") {
		t.Error("region fallback must guard the non-zero exit of `aws configure get region`")
	}
}

func TestRenderImageBuilderTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-abc123",
		Region:    "us-east-1",
		Name:      "fabrica-horde-5.5.0",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("rendered template is not valid JSON: %v\n%s", err, string(out))
	}

	if raw["parentImage"] != "ami-abc123" {
		t.Errorf("parentImage = %v, want ami-abc123", raw["parentImage"])
	}

	s := string(out)
	if !strings.Contains(s, "arn:aws:imagebuilder:us-east-1:aws:component/update-linux/") {
		t.Error("recipe should contain real update-linux ARN")
	}
	if !strings.Contains(s, "REPLACE_WITH_CUSTOM_COMPONENT_ARN") {
		t.Error("recipe should contain placeholder for custom component ARN")
	}
	if strings.Contains(s, "_comment") {
		t.Error("recipe must not contain _comment (not part of Image Builder schema)")
	}
}

func TestRenderImageBuilderTemplate_Region(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-abc123",
		Region:    "eu-west-1",
		Name:      "test",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "eu-west-1") {
		t.Error("recipe should contain the configured region in component ARNs")
	}
	if strings.Contains(s, "us-east-1") {
		t.Error("recipe should not hardcode us-east-1 when region is eu-west-1")
	}
}

// TestRenderedDockerComponentPassesValidator is the end-to-end guarantee for
// #459: the component the generator actually emits for a docker install must
// satisfy the same validator that refuses to write it if the bake steps are
// missing. This keeps the template, the validator, and the generated AMI
// contract in lockstep.
func TestRenderedDockerComponentPassesValidator(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		Name:      "test-horde",
		BaseImage: "ami-abc123",
		Region:    "us-east-1",
	}}
	out, err := bc.renderTemplate("component.yaml.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}
	if err := validateComponentYAML(out, "docker"); err != nil {
		t.Fatalf("rendered docker component should pass validateComponentYAML, got: %v", err)
	}
	// The native render is irrelevant to the docker bake, but must still pass
	// the top-level field checks.
	nb := buildCommand{cfg: BuildConfig{
		Version:   "5.4.0",
		Install:   "native",
		Name:      "test-horde",
		BaseImage: "ami-abc123",
		Region:    "us-east-1",
	}}
	nout, err := nb.renderTemplate("component.yaml.tmpl", nb.cfg)
	if err != nil {
		t.Fatalf("renderTemplate native error: %v", err)
	}
	if err := validateComponentYAML(nout, "native"); err != nil {
		t.Fatalf("rendered native component should pass validateComponentYAML, got: %v", err)
	}
}

func TestRenderComponentTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version: "5.5.0",
		Install: "docker",
		Name:    "test-horde",
	}}

	out, err := bc.renderTemplate("component.yaml.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "schemaVersion: 1.0") {
		t.Error("component should have schemaVersion: 1.0")
	}
	if !strings.Contains(s, "InstallDocker") {
		t.Error("docker component should have InstallDocker step")
	}
	if !strings.Contains(s, "InstallHordeSystemdUnit") {
		t.Error("docker component should have InstallHordeSystemdUnit step")
	}
	if !strings.Contains(s, "BakeHordeStack") {
		t.Error("docker component should have a BakeHordeStack step")
	}
	if !strings.Contains(s, "InstallAwsCliV2") {
		t.Error("docker component should install the AWS CLI v2 (stock jammy has none)")
	}
	if strings.Contains(s, "InstallDotNet") {
		t.Error("docker component should not have InstallDotNet step")
	}
	if !strings.Contains(s, "EnsureSSMAgent") {
		t.Error("component should fail-closed enable SSM agent")
	}
	if !strings.Contains(s, "snap install amazon-ssm-agent --classic") {
		t.Error("component should install snap SSM agent when neither unit exists")
	}
	if strings.Contains(s, "|| true") {
		t.Error("component must not soft-fail SSM enable with || true")
	}
	// The docker bake must write the compose stack + configs to the path the
	// horde unit and cloud-init use, pull + tag the server image, and fail
	// closed if the compose file is missing (the #459 gap). These are exactly
	// the markers validateComponentYAML enforces, so the rendered output and
	// the validator stay in lockstep.
	requireDockerBake(t, s)
	// The unit starts compose from /etc/horde, not the old /opt/horde.
	if !strings.Contains(s, "WorkingDirectory=/etc/horde") {
		t.Error("horde unit should use WorkingDirectory=/etc/horde")
	}
	if strings.Contains(s, "WorkingDirectory=/opt/horde") {
		t.Error("horde unit must not use the old /opt/horde working directory")
	}
}

func TestRenderComponentTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version: "5.4.0",
		Install: "native",
		Name:    "test-horde-native",
	}}

	out, err := bc.renderTemplate("component.yaml.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "InstallDotNet") {
		t.Error("native component should have InstallDotNet step")
	}
	if !strings.Contains(s, "InstallMongoDB") {
		t.Error("native component should have InstallMongoDB step")
	}
	if !strings.Contains(s, "InstallRedis") {
		t.Error("native component should have InstallRedis step")
	}
	if !strings.Contains(s, "InstallHordeBinary") {
		t.Error("native component should have InstallHordeBinary step")
	}
	if !strings.Contains(s, "InstallAwsCliV2") {
		t.Error("native component should install the AWS CLI v2 (stock jammy has none; aws s3 sync needs it)")
	}
	if strings.Contains(s, "InstallDocker") {
		t.Error("native component should not have InstallDocker step")
	}
	if strings.Contains(s, "BakeHordeStack") {
		t.Error("native component should not have the docker BakeHordeStack step")
	}
	// The native binary is synced to /opt/horde, so its unit keeps
	// WorkingDirectory=/opt/horde (the docker path is the one that moved to
	// /etc/horde).
	if !strings.Contains(s, "WorkingDirectory=/opt/horde") {
		t.Error("native horde unit should use WorkingDirectory=/opt/horde")
	}
}

func TestRenderPackerTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-packer123",
		Region:    "us-west-2",
		Name:      "fabrica-horde-packer",
	}}

	out, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "ami-packer123") {
		t.Error("packer template should contain base image ID")
	}
	if !strings.Contains(s, "5.5.0") {
		t.Error("packer template should contain horde version")
	}
	if !strings.Contains(s, "amazon-ebs") {
		t.Error("packer template should use amazon-ebs builder")
	}
	if !strings.Contains(s, "us-west-2") {
		t.Error("packer template should contain the configured region")
	}
	// The Packer docker bake is the same shared script as the Image Builder
	// component, so it must carry the same bake markers (the #459 gap: the
	// old Packer path did not write the compose file or bake the image).
	requireDockerBake(t, s)
	if strings.Contains(s, "GITHUB_PAT") {
		t.Error("packer template should not reference GITHUB_PAT")
	}
	if !strings.Contains(s, "snap install amazon-ssm-agent --classic") {
		t.Error("packer template should fail-closed enable SSM agent")
	}
	// Bare # comments inside inline = [...] list literals are invalid HCL.
	// Heredoc bodies (<<-EOF ... EOF) are strings, so # there is fine.
	inInlineList := false
	inHeredoc := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<<-EOF") || strings.Contains(trimmed, "<<EOF") {
			inHeredoc = true
			inInlineList = true
			continue
		}
		if inHeredoc && trimmed == "EOF" {
			inHeredoc = false
			continue
		}
		if inHeredoc {
			continue
		}
		if strings.Contains(trimmed, "inline = [") {
			inInlineList = true
		}
		if inInlineList && trimmed == "]" {
			inInlineList = false
		}
		if inInlineList && strings.HasPrefix(trimmed, "#") {
			t.Errorf("packer template must not contain comment lines inside inline lists (invalid HCL): %q", trimmed)
		}
	}
}

func TestRenderPackerTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "native",
		BaseImage: "ami-native456",
		Region:    "us-east-1",
		Name:      "fabrica-horde-native",
	}}

	out, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "dotnet-sdk-8.0") {
		t.Error("native packer template should install .NET 8")
	}
	if !strings.Contains(s, "horde_source_dir") {
		t.Error("native packer template should declare horde_source_dir variable")
	}
	if strings.Contains(s, "GITHUB_PAT") {
		t.Error("native packer template should not reference GITHUB_PAT")
	}
}

func TestRenderBuildGuideTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     "ami-guide",
		Region:        "us-east-1",
		Name:          "test-horde",
		IncludePacker: true,
	}}

	out, err := bc.renderTemplate("build-guide.md.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "docker install") {
		t.Error("docker build guide should mention docker install method")
	}
	if !strings.Contains(s, "packer.pkr.hcl") {
		t.Error("build guide with IncludePacker should mention packer.pkr.hcl")
	}
	if !strings.Contains(s, "component.yaml") {
		t.Error("build guide should mention component.yaml")
	}
	if !strings.Contains(s, "aws imagebuilder create-component") {
		t.Error("build guide should include create-component command")
	}
}

func TestRenderBuildGuideTemplate_NativeNoPackerFile(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "native",
		BaseImage:     "ami-guide",
		Region:        "us-east-1",
		Name:          "test-horde",
		IncludePacker: false,
	}}

	out, err := bc.renderTemplate("build-guide.md.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if strings.Contains(s, "packer.pkr.hcl") {
		t.Error("build guide without IncludePacker should not mention packer.pkr.hcl")
	}
	if !strings.Contains(s, "native install") {
		t.Error("native build guide should mention native install method")
	}
}
