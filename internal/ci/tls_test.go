package ci

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"go.yaml.in/yaml/v3"
)

func TestTLSBuildspec(t *testing.T) {
	c, err := horde.ConnectionFromConfig(config.HordeConfig{Port: 8443, TLS: config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/cert.pem", KeyPath: "/etc/horde/tls/key.pem", CAPath: "/etc/horde/tls/ca.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{HordeCAPath: "/etc/horde/tls/ca.pem"}, "", "", c.URL(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	plan.HordeConnection = c
	raw := BuildspecRaw(plan)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal("invalid buildspec", err)
	}
	for _, s := range []string{"--cacert", "--proto '=https'", "--connect-timeout 5", "--max-time 30", "unencryptedHttp2", "p.get(\"https\")==8443", "--http2"} {
		if !strings.Contains(raw, s) {
			t.Fatalf("missing %s", s)
		}
	}
	if strings.Index(raw, "/api/v1/server/ports") > strings.Index(raw, "HORDE_AUTH=()") {
		t.Fatal("listener check after token use")
	}
	if strings.Contains(raw, " --location") || strings.Contains(raw, " -k ") {
		t.Fatal("insecure curl")
	}
	if strings.Count(raw, "CURL_OPTS=(") != 2 {
		t.Fatal("both phases need canonical curl initialization")
	}
	if ProjectSpec(plan, "", "").EnvDefaults["HORDE_CA_FILE"] != "/etc/horde/tls/ca.pem" {
		t.Fatal("lost CI CA path")
	}
	if _, err := NewCreatePlan(context.Background(), config.CIConfig{HordeCAPath: "relative"}, "", "", "", nil); err == nil {
		t.Fatal("relative CI trust accepted")
	}
}

func TestGeneratedSubmissionRejectsRedirectAndAuthFailure(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	var doc struct {
		Phases map[string]struct {
			Commands []string `yaml:"commands"`
		} `yaml:"phases"`
	}
	if err := yaml.Unmarshal([]byte(BuildspecRaw(nil)), &doc); err != nil {
		t.Fatal(err)
	}
	submit := doc.Phases["build"].Commands[1]
	for _, code := range []string{"200", "302", "401"} {
		t.Run(code, func(t *testing.T) {
			cmd := exec.Command(bash, "-c", "set -euo pipefail\ncurl() { printf '%s' \"$TEST_CODE\"; }\n"+submit)
			cmd.Env = append(os.Environ(), "TEST_CODE="+code, "HORDE_URL=https://horde.example.internal:5000",
				"HORDE_CA_FILE=", "HORDE_TOKEN=test-fixture-credential", "BUILDGRAPH=fixture", "TARGET=Build")
			out, err := cmd.CombinedOutput()
			if (err == nil) != (code == "200") || strings.Contains(string(out), "test-fixture-credential") {
				t.Fatalf("submission result: %v %s", err, out)
			}
		})
	}
}
