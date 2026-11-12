package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, tc := range []struct {
		name, code, body, curlExit string
		ok                         bool
	}{
		{"success", "200", `{"id":"fixture-job"}`, "0", true},
		{"redirect", "302", `{"id":"fixture-job"}`, "0", false},
		{"unauthorized", "401", `{"id":"fixture-job"}`, "0", false},
		{"transport", "000", "", "7", false},
		{"malformed", "200", "{fixture-credential", "0", false},
		{"missing", "200", `{}`, "0", false},
		{"wrong type", "200", `{"id":7}`, "0", false},
		{"control characters", "200", `{"id":"fixture\ncredential"}`, "0", false},
		{"oversized", "200", "oversized", "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			curl := `curl() {
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    if [ "$TEST_RESPONSE" = "oversized" ]; then python3 -c 'print(" " * 65537)' >"$1"; else printf '%s' "$TEST_RESPONSE" >"$1"; fi
  fi
  shift
done
printf '%s' "$TEST_CODE"
return "$TEST_CURL_EXIT"
}`
			cmd := exec.Command(bash, "-c", curl+"\n"+submit)
			cmd.Env = append(os.Environ(), "TEST_CODE="+tc.code, "TEST_RESPONSE="+tc.body, "TEST_CURL_EXIT="+tc.curlExit,
				"TMPDIR="+filepath.ToSlash(dir), "HORDE_URL=https://horde.example.internal:5000",
				"HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential", "BUILDGRAPH=fixture", "TARGET=Build")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok || strings.Contains(string(out), "fixture-credential") {
				t.Fatalf("submission result: %v %s", err, out)
			}
			if tc.ok && !strings.Contains(string(out), "fixture-job") {
				t.Fatal("successful job identifier missing from output")
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("response file not cleaned up: %v", err)
			}
		})
	}
}

func TestCodeBuildPreflightStopsBeforeCredentialsWithoutCallerShellFlags(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	c, _ := horde.ConnectionFromConfig(config.HordeConfig{TLS: config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/cert.pem", KeyPath: "/etc/horde/tls/key.pem", CAPath: "/etc/horde/tls/ca.pem"}})
	var doc struct {
		Phases map[string]struct{ Commands []string } `yaml:"phases"`
	}
	if err := yaml.Unmarshal([]byte(BuildspecRaw(&CreatePlan{HordeConnection: c})), &doc); err != nil {
		t.Fatal(err)
	}
	block := doc.Phases["pre_build"].Commands[3]
	for _, tc := range []struct {
		name, body, http2, portsExit string
		ok                           bool
	}{
		{"valid", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "200 2", "0", true},
		{"plaintext", `{"http":5000,"https":5000,"unencryptedHttp2":0}`, "200 2", "0", false},
		{"grpc", `{"http":0,"https":5000,"unencryptedHttp2":5002}`, "200 2", "0", false},
		{"http2", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "200 1.1", "0", false},
		{"redirect", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "302 2", "0", false},
		{"listener transport", `{"http":0,"https":5000,"unencryptedHttp2":0}`, "200 2", "7", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canary := filepath.Join(t.TempDir(), "authenticated-call")
			curl := `curl() {
case "$*" in
  */api/v1/server/ports*) case "$*" in *--http2*) printf '%s' "$TEST_HTTP2" ;; *) printf '%s\n200' "$TEST_BODY"; return "$TEST_PORTS_EXIT" ;; esac ;;
  */api/v1/jobs*) touch "$CANARY"; printf '200' ;;
esac
}
`
			cmd := exec.Command(bash, "-c", curl+block)
			cmd.Env = append(os.Environ(), "TEST_BODY="+tc.body, "TEST_HTTP2="+tc.http2, "TEST_PORTS_EXIT="+tc.portsExit, "CANARY="+filepath.ToSlash(canary),
				"HORDE_URL="+c.URL(""), "HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok || strings.Contains(string(out), "fixture-credential") {
				t.Fatalf("preflight: %v %s", err, out)
			}
			_, statErr := os.Stat(canary)
			if (statErr == nil) != tc.ok {
				t.Fatal("credential-bearing request ran after a failed transport check")
			}
		})
	}
}

func TestTLSBuildspecRejectsPlaintextURLOverride(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	var doc struct {
		Phases map[string]struct{ Commands []string } `yaml:"phases"`
	}
	if err := yaml.Unmarshal([]byte(BuildspecRaw(&CreatePlan{HordeConnection: horde.Connection{TLS: true, Port: 5000}})), &doc); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"pre_build", "build"} {
		t.Run(phase, func(t *testing.T) {
			canary := filepath.Join(t.TempDir(), "network-call")
			curl := `curl() { touch "$CANARY"; printf '{"http":0,"https":5000,"unencryptedHttp2":0}\n200'; }` + "\n"
			commands := doc.Phases[phase].Commands
			cmd := exec.Command(bash, "-c", curl+commands[len(commands)-1])
			cmd.Env = append(os.Environ(), "CANARY="+filepath.ToSlash(canary),
				"HORDE_URL=http://horde.example.internal:5000", "HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential",
				"BUILDGRAPH=fixture", "TARGET=Build")
			out, err := cmd.CombinedOutput()
			if err == nil || strings.Contains(string(out), "fixture-credential") {
				t.Fatalf("plaintext override: %v %s", err, out)
			}
			if _, err := os.Stat(canary); !os.IsNotExist(err) {
				t.Fatal("TLS build made a plaintext request")
			}
		})
	}
}
