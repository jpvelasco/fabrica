package ami

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/horde"
)

func TestHclInline(t *testing.T) {
	cmd := "line1\nline2"
	want := `"line1\nline2"`
	if got := hclInline(cmd); got != want {
		t.Errorf("hclInline = %q, want %q", got, want)
	}

	// Backslashes must be escaped so HCL does not reinterpret them.
	escaped := hclInline(`c:\path\to\file`)
	if strings.Contains(escaped, `c:\path\to\file`) {
		t.Errorf("hclInline should escape backslashes, got %q", escaped)
	}
	if !strings.Contains(escaped, `\\`) {
		t.Errorf("hclInline should produce double backslashes, got %q", escaped)
	}

	// Double quotes in the command must be escaped for the HCL string literal.
	if got := hclInline(`echo "hi"`); got != `"echo \"hi\""` {
		t.Errorf("hclInline = %q, want \"echo \\\"hi\\\"\"", got)
	}

	// HCL interpolation openers must be escaped so Packer treats them as
	// literals; bare $(...) command substitution is left untouched.
	if got := hclInline(`echo "${ECR_REPO%/*} $(uname -m)"`); got != `"echo \"$${ECR_REPO%/*} $(uname -m)\""` {
		t.Errorf("hclInline = %q, want interpolated-escape output", got)
	}

	// HCL template-directive openers must be escaped the same way.
	if got := hclInline(`printf "%{d}\n" x`); got != `"printf \"%%{d}\\n\" x"` {
		t.Errorf("hclInline = %q, want directive-escape output", got)
	}
}

func TestDockerBakeScriptVersionSubstitution(t *testing.T) {
	got := dockerBakeScript("5.5.0")
	if !strings.Contains(got, "fabrica-horde-server:5.5.0") {
		t.Error("bake script should tag fabrica-horde-server:<version>")
	}
	if !strings.Contains(got, `"${ECR_REPO}:5.5.0"`) {
		t.Error("bake script should pull the configured version from the ECR repo variable")
	}
	if !strings.Contains(got, "ECR_REPO=REPLACE_WITH_ECR_REPOSITORY") {
		t.Error("bake script should use the ECR repository placeholder")
	}
	if strings.Contains(got, hordeVersionPlaceholder) {
		t.Error("bake script must not leak the un-substituted version placeholder")
	}

	latest := dockerBakeScript("latest")
	if !strings.Contains(latest, "fabrica-horde-server:latest") {
		t.Error("bake script should tag fabrica-horde-server:latest for the literal latest version")
	}
}

func TestHordeDockerUnitCommand(t *testing.T) {
	cmd := hordeDockerUnitCommand()
	if !strings.Contains(cmd, "cat >/etc/systemd/system/horde.service <<'UNIT'") {
		t.Error("unit command should write /etc/systemd/system/horde.service via a heredoc")
	}
	if !strings.Contains(cmd, "WorkingDirectory=/etc/horde") {
		t.Error("unit command should set WorkingDirectory=/etc/horde")
	}
	if !strings.Contains(cmd, "\nUNIT") {
		t.Error("unit command should close the heredoc with UNIT")
	}
}

// The baked compose stack must not run MongoDB without authentication or leave
// Horde in its default anonymous mode (every request is an administrator), and
// it must not publish the Horde ports beyond loopback until userdata opens them.
func TestDockerBakeScriptRequiresAuthentication(t *testing.T) {
	got := dockerBakeScript("5.8.0")
	if strings.Contains(got, "--noauth") {
		t.Error("compose stack must not start MongoDB with --noauth")
	}
	for _, want := range []string{
		"mongod --auth",
		"MONGO_INITDB_ROOT_PASSWORD=${MONGO_PASSWORD:?",
		"HORDE__AUTHMETHOD=Horde",
		"mongodb://horde:${MONGO_PASSWORD}@mongodb:27017/horde?authSource=admin",
		"/etc/horde/fabrica-runtime-contract",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("compose stack missing %q", want)
		}
	}
	if strings.Contains(got, "    ports:") || strings.Contains(got, "ASPNETCORE_URLS") || strings.Contains(got, "HORDE__SERVERURL") {
		t.Error("base stack must leave listener publication and server URL to the runtime overlay")
	}
}

// The fabrica-ci service account (created by cloud-init for horde submit and
// ci) is authorized only through this global ACL entry: job creation and
// viewing, nothing administrative.
func TestDockerBakeGlobalsGrantServiceAccountJobsOnly(t *testing.T) {
	got := dockerBakeScript("5.8.0")
	start := strings.Index(got, "cat >/etc/horde/globals.json <<'JSON'\n")
	if start < 0 {
		t.Fatal("globals.json heredoc not found")
	}
	body := got[start+len("cat >/etc/horde/globals.json <<'JSON'\n"):]
	end := strings.Index(body, "\nJSON\n")
	if end < 0 {
		t.Fatal("globals.json heredoc terminator not found")
	}
	body = body[:end]
	var cfg struct {
		ACL struct {
			Entries []struct {
				Claim struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"claim"`
				Actions []string `json:"actions"`
			} `json:"entries"`
		} `json:"acl"`
	}
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("globals.json is not valid JSON: %v", err)
	}
	if len(cfg.ACL.Entries) != 1 {
		t.Fatalf("acl entries = %d, want 1", len(cfg.ACL.Entries))
	}
	e := cfg.ACL.Entries[0]
	if e.Claim.Type != horde.ServiceAccountGroupClaimType || e.Claim.Value != horde.ServiceAccountGroup {
		t.Errorf("acl claim = %+v", e.Claim)
	}
	if strings.Join(e.Actions, ",") != "ViewStream,CreateJob,ViewJob" {
		t.Errorf("acl actions = %v", e.Actions)
	}
}
