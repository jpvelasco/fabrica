package ami

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"go.yaml.in/yaml/v3"
)

const (
	testAgentArtifact = "s3://my-bucket/horde/HordeAgent.zip"
	// Mixed case on purpose: the digest is accepted case-insensitively and
	// rendered lowercase because sha256sum -c writes lowercase hex.
	testAgentSHA      = "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef"
	testAgentSHALower = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func validAgentConfig() BuildConfig {
	return BuildConfig{
		Version:       "5.8.4",
		Install:       "native",
		Role:          roleAgent,
		BaseImage:     defaultBaseImage,
		Region:        "us-east-1",
		OutputDir:     "x",
		AgentArtifact: testAgentArtifact,
		AgentSHA256:   testAgentSHA,
	}
}

func renderAgent(t *testing.T, tmpl string, includePacker bool) string {
	t.Helper()
	cfg := validAgentConfig()
	cfg.IncludePacker = includePacker
	bc := buildCommand{cfg: cfg}
	out, err := bc.renderTemplate(tmpl, bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate(%s): %v", tmpl, err)
	}
	return string(out)
}

func TestAgentRoleValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*BuildConfig)
		wantErr string
	}{
		{"docker install", func(c *BuildConfig) { c.Install = "docker" }, "--role agent requires --install native"},
		{"unknown role", func(c *BuildConfig) { c.Role = "bogus" }, `--role must be "coordinator" or "agent"`},
		{"missing artifact", func(c *BuildConfig) { c.AgentArtifact = "" }, "--agent-artifact is required"},
		{"https artifact", func(c *BuildConfig) { c.AgentArtifact = "https://example.com/HordeAgent.zip" }, "--agent-artifact must be an s3:// URI"},
		{"tar.gz error names the zip", func(c *BuildConfig) { c.AgentArtifact = "s3://my-bucket/horde/horde-agent-linux-x64.tar.gz" }, "publishes the agent as a zip, not a .tar.gz"},
		{"artifact with shell syntax", func(c *BuildConfig) { c.AgentArtifact = "s3://my-bucket/$(id).zip" }, "must be an s3:// URI ending in .zip"},
		{"missing sha", func(c *BuildConfig) { c.AgentSHA256 = "" }, "--agent-sha256 is required"},
		{"short sha", func(c *BuildConfig) { c.AgentSHA256 = "abc123" }, "must be 64 hexadecimal characters"},
		{"non-hex sha", func(c *BuildConfig) { c.AgentSHA256 = strings.Repeat("g", 64) }, "must be 64 hexadecimal characters"},
		{"coordinator with artifact", func(c *BuildConfig) { c.Role = roleCoordinator; c.Install = "docker" }, "only valid with --role agent"},
		{"coordinator with sha", func(c *BuildConfig) {
			c.Role = roleCoordinator
			c.Install = "docker"
			c.AgentArtifact = ""
		}, "only valid with --role agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validAgentConfig()
			tc.mutate(&cfg)
			bc := &buildCommand{cfg: cfg}
			err := bc.validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAgentRoleValidation_Accepts(t *testing.T) {
	bc := &buildCommand{cfg: validAgentConfig()}
	if err := bc.validate(); err != nil {
		t.Fatalf("valid agent config rejected: %v", err)
	}
	// An empty role is the coordinator, so pre-#701 callers keep validating.
	coord := &buildCommand{cfg: BuildConfig{Version: "5.8.4", Install: "docker", BaseImage: defaultBaseImage, Region: "us-east-1", OutputDir: "x"}}
	if err := coord.validate(); err != nil {
		t.Fatalf("empty role should validate as coordinator: %v", err)
	}
}

func TestAgentBuildDefaultNameAndFiles(t *testing.T) {
	cfg := validAgentConfig()
	cfg.Name = ""
	bc, written, _ := newTestCommand(t, cfg)
	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, name := range []string{"image-builder-recipe.json", "component.yaml", "build-guide.md"} {
		if _, ok := written[name]; !ok {
			t.Errorf("expected %s to be written", name)
		}
	}
	recipe := string(written["image-builder-recipe.json"])
	if !strings.Contains(recipe, `"name": "fabrica-horde-agent-5-8-4"`) {
		t.Errorf("default agent recipe name should be fabrica-horde-agent-5-8-4:\n%s", recipe)
	}
	if !strings.Contains(string(written["component.yaml"]), "name: fabrica-horde-agent-5-8-4-native") {
		t.Error("agent component name should be <sanitized-name>-native")
	}
}

func TestAgentRecipeTagsMarkRole(t *testing.T) {
	agent := validAgentConfig()
	agent.Name = "fabrica-horde-agent-5-8-4"
	if got := agent.RecipeTags()["HordeRole"]; got != roleAgent {
		t.Errorf("agent HordeRole tag = %q, want %q", got, roleAgent)
	}
	coord := BuildConfig{Version: "5.8.4", Install: "native", Name: "fabrica-horde-5-8-4"}
	if _, ok := coord.RecipeTags()["HordeRole"]; ok {
		t.Error("coordinator tags must not carry HordeRole")
	}
}

func TestAgentComponentRendering(t *testing.T) {
	s := renderAgent(t, "agent-component.yaml.tmpl", false)
	if err := validateComponentYAML([]byte(s), "native"); err != nil {
		t.Fatalf("rendered agent component is invalid: %v", err)
	}
	mustContain := []string{
		`sha256sum -c "$work/HordeAgent.sha256"`,
		`printf '%s  %s\n' "` + testAgentSHALower + `" "$work/HordeAgent.zip" > "$work/HordeAgent.sha256"`,
		`/usr/local/bin/aws s3 cp "` + testAgentArtifact + `" "$work/HordeAgent.zip"`,
		"apt-get install -y libicu70 unzip",
		`unzip -q "$work/HordeAgent.zip" -d /opt/horde-agent`,
		"chown -R root:root /opt/horde-agent",
		"chmod -R go-w /opt/horde-agent",
		"useradd --system --home-dir /var/lib/horde-agent --no-create-home --shell /usr/sbin/nologin horde-agent",
		"User=horde-agent",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ReadWritePaths=/opt/horde-agent/Data /var/lib/horde-agent/.local/share",
		"ExecStart=/usr/bin/dotnet /opt/horde-agent/HordeAgent.dll -WorkingDir=/opt/horde-agent/Data",
		"WorkingDirectory=/opt/horde-agent/Data",
		"Environment=DOTNET_NOLOGO=1",
		"Environment=DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"printf '1\\n' > /etc/horde/fabrica-native-agent-contract",
		"systemctl enable horde-agent.service",
		"test -x /usr/local/bin/aws",
		"ssm_unit",
		"runuser -u horde-agent -- test -w /opt/horde-agent/Data",
		"runuser -u horde-agent -- test -w /var/lib/horde-agent/.local/share",
		"! runuser -u horde-agent -- test -w /opt/horde-agent/HordeAgent.dll",
		"test -f /opt/horde-agent/HordeAgent.dll",
		"test -x /usr/bin/dotnet",
		"/usr/bin/dotnet --list-runtimes | grep -q '^Microsoft.NETCore.App 10\\.'",
		"systemctl is-enabled horde-agent.service",
		"! systemctl is-active --quiet horde-agent.service",
		"signed-by=/etc/apt/keyrings/dotnet-backports.gpg",
		"https://ppa.launchpadcontent.net/dotnet/backports/ubuntu ${VERSION_CODENAME} main",
		"apt-get install -y aspnetcore-runtime-10.0",
	}
	for _, want := range mustContain {
		assert.Contains(t, s, want)
	}
	assert.NoneOf(t, s, "systemctl start horde-agent", "systemctl restart horde-agent",
		"docker", "mongod", "redis", "dotnet-sdk", "packages.microsoft.com", "| sh", "| bash",
		"awscli-exe-linux", "REPLACE_WITH", testAgentSHA,
		"tar -xzf", "horde-agent.tar.gz", "/opt/horde-agent/horde-agent")
}

func TestAgentPackerRendering(t *testing.T) {
	s := renderAgent(t, "packer.hcl.tmpl", true)
	for _, want := range []string{
		"awscli-exe-linux-$(uname -m).zip.sig",
		"sha256sum -c",
		"unzip -q",
		"aspnetcore-runtime-10.0",
		"systemctl enable horde-agent.service",
		"runuser -u horde-agent -- test -w /opt/horde-agent/Data",
		"ReadWritePaths=/opt/horde-agent/Data /var/lib/horde-agent/.local/share",
		"ExecStart=/usr/bin/dotnet /opt/horde-agent/HordeAgent.dll",
	} {
		assert.Contains(t, s, want)
	}
	assert.NoneOf(t, s, `provisioner "file"`, "horde_source_dir", "systemctl is-enabled horde\"", "docker", "| sh", "| bash")
	if got := strings.Count(s, "sha256sum -c"); got != 1 {
		t.Errorf("agent Packer template verifies the artifact %d times, want once", got)
	}
}

func TestAgentBuildGuideRendering(t *testing.T) {
	s := renderAgent(t, "build-guide.md.tmpl", true)
	for _, want := range []string{
		"### Prerequisites (agent role)",
		"sha256sum HordeAgent.zip",
		"/api/v1/agentsoftware/default/zip",
		"--agent-sha256 <hash>",
		"s3:GetObject",
		"horde:\n  agents:\n    amiId: ami-<id>",
	} {
		assert.Contains(t, s, want)
	}
	assert.NoneOf(t, s, "### Prerequisites (native install only)", "docker compose", "REPLACE_WITH_ECR_REPOSITORY", "horde:\n  amiId")
}

func TestAgentRecipeDescription(t *testing.T) {
	bc := buildCommand{cfg: validAgentConfig()}
	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("agent recipe is not JSON: %v", err)
	}
	if !strings.Contains(doc["description"].(string), "agent") {
		t.Errorf("agent recipe description should name the agent: %v", doc["description"])
	}
}

// TestCoordinatorRenderHasNoAgentContent guards the coordinator output: the
// empty role and the explicit "coordinator" role must render identically, and
// neither may pick up any agent-only content.
func TestCoordinatorRenderHasNoAgentContent(t *testing.T) {
	for _, tmpl := range []string{"image-builder.json.tmpl", "component.yaml.tmpl", "packer.hcl.tmpl", "build-guide.md.tmpl"} {
		for _, install := range []string{"docker", "native"} {
			t.Run(tmpl+"/"+install, func(t *testing.T) {
				base := BuildConfig{Version: "5.8.0", Install: install, BaseImage: defaultBaseImage, Region: "us-east-1", Name: "fabrica-horde-5-8-0", IncludePacker: true}
				empty := buildCommand{cfg: base}
				explicit := base
				explicit.Role = roleCoordinator
				explicitCmd := buildCommand{cfg: explicit}
				a, err := empty.renderTemplate(tmpl, empty.cfg)
				if err != nil {
					t.Fatal(err)
				}
				b, err := explicitCmd.renderTemplate(tmpl, explicitCmd.cfg)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(a, b) {
					t.Fatal("empty role and explicit coordinator role render differently")
				}
				for _, bad := range []string{"horde-agent", "fabrica-native-agent-contract", "HordeRole", "agent-artifact"} {
					if strings.Contains(string(a), bad) {
						t.Errorf("coordinator output must not contain %q", bad)
					}
				}
			})
		}
	}
}

// TestAgentComponentStepsParseAsBash parses the rendered component as YAML and
// runs bash -n over every step's commands. A YAML indentation slip or an
// unterminated UNIT heredoc fails here even when the substring checks pass.
func TestAgentComponentStepsParseAsBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	var doc struct {
		Phases []struct {
			Name  string `yaml:"name"`
			Steps []struct {
				Name   string `yaml:"name"`
				Inputs struct {
					Commands []string `yaml:"commands"`
				} `yaml:"inputs"`
			} `yaml:"steps"`
		} `yaml:"phases"`
	}
	if err := yaml.Unmarshal([]byte(renderAgent(t, "agent-component.yaml.tmpl", false)), &doc); err != nil {
		t.Fatalf("rendered agent component is not valid YAML: %v", err)
	}
	dir := t.TempDir()
	steps := 0
	for _, phase := range doc.Phases {
		for _, step := range phase.Steps {
			steps++
			path := filepath.Join(dir, phase.Name+"-"+step.Name+".sh")
			body := strings.Join(step.Inputs.Commands, "\n") + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
				t.Errorf("step %s/%s does not parse as bash: %v\n%s", phase.Name, step.Name, err, out)
			}
		}
	}
	if steps != 4 {
		t.Errorf("parsed %d steps, want 4 (VerifyAwsCliV2, InstallHordeAgent, EnsureSSMAgent, VerifyAgentService)", steps)
	}
}
