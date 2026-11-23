package ci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/naming"
	"go.yaml.in/yaml/v3"
)

func TestTLSBuildspec(t *testing.T) {
	c, err := horde.ConnectionFromConfig(config.HordeConfig{Port: 8443, TLS: config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/cert.pem", KeyPath: "/etc/horde/tls/key.pem", CAPath: "/etc/horde/tls/ca.pem"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewCreatePlan(context.Background(), naming.Default, config.CIConfig{HordeCAPath: "/etc/horde/tls/ca.pem"}, "", "", c.URL(""), nil)
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
	if _, err := NewCreatePlan(context.Background(), naming.Default, config.CIConfig{HordeCAPath: "relative"}, "", "", "", nil); err == nil {
		t.Fatal("relative CI trust accepted")
	}
}

// fixtureJobRequest is the HORDE_JOB_REQUEST value `ci trigger` sends.
const fixtureJobRequest = `{"streamId":"fixture-stream","templateId":"fixture-template"}`

// curlStub replays a canned response: the -o file gets TEST_RESPONSE, stdout
// gets TEST_CODE, and the exit status is TEST_CURL_EXIT.
const curlStub = `curl() {
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

func TestGeneratedSubmissionSendsJobRequestBody(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	stub := `curl() {
printf '%s\n' "$@" >"$TEST_ARGS"
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then shift; printf '%s' '{"id":"job-1"}' >"$1"; fi
  shift
done
printf '200'
}`
	cmd := exec.Command(bash, "-c", stub+"\n"+buildspecCommand(t, BuildspecRaw(nil), "build", "-X POST"))
	cmd.Env = append(os.Environ(), "TEST_ARGS="+filepath.ToSlash(argsFile), "HORDE_URL=https://horde.example.internal:5000",
		"HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential", "HORDE_JOB_REQUEST="+fixtureJobRequest)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "job-1") {
		t.Fatalf("submit: %v %s", err, out)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if slices.Contains(args, "--fail") {
		t.Fatal("job POST must not pass --fail")
	}
	idx := slices.Index(args, "--data-binary")
	if idx < 0 || idx+1 >= len(args) || args[idx+1] != fixtureJobRequest {
		t.Fatalf("--data-binary must carry HORDE_JOB_REQUEST verbatim, args: %q", args)
	}
}

func TestGeneratedSubmissionSurfacesRejectedBody(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	cmd := exec.Command(bash, "-c", curlStub+"\n"+buildspecCommand(t, BuildspecRaw(nil), "build", "-X POST"))
	cmd.Env = append(os.Environ(), "TEST_CODE=404", "TEST_RESPONSE="+`{"message":"Stream fixture-stream not found"}`, "TEST_CURL_EXIT=0",
		"TMPDIR="+filepath.ToSlash(t.TempDir()), "HORDE_URL=https://horde.example.internal:5000",
		"HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential", "HORDE_JOB_REQUEST="+fixtureJobRequest)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("404 must fail the build, got success: %s", out)
	}
	for _, want := range []string{"HTTP 404", "Stream fixture-stream not found"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "fixture-credential") {
		t.Fatal("token leaked into build output")
	}
}

func TestPreBuildRequiresJobRequest(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	check := buildspecCommand(t, BuildspecRaw(nil), "pre_build", `"$HORDE_JOB_REQUEST"`)
	cmd := exec.Command(bash, "-c", check)
	cmd.Env = withoutEnv(os.Environ(), "HORDE_JOB_REQUEST")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("pre_build must fail without HORDE_JOB_REQUEST")
	}
	if !strings.Contains(string(out), "fabrica ci trigger") {
		t.Fatalf("pre_build error does not say how to fix it:\n%s", out)
	}

	cmd = exec.Command(bash, "-c", check)
	cmd.Env = append(os.Environ(), "HORDE_JOB_REQUEST="+fixtureJobRequest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre_build with HORDE_JOB_REQUEST set: %v %s", err, out)
	}
}

// withoutEnv returns env with every entry for key removed.
func withoutEnv(env []string, key string) []string {
	return slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
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
			cmd := exec.Command(bash, "-c", curlStub+"\n"+submit)
			cmd.Env = append(os.Environ(), "TEST_CODE="+tc.code, "TEST_RESPONSE="+tc.body, "TEST_CURL_EXIT="+tc.curlExit,
				"TMPDIR="+filepath.ToSlash(dir), "HORDE_URL=https://horde.example.internal:5000",
				"HORDE_CA_FILE=", "HORDE_TOKEN=fixture-credential", "HORDE_JOB_REQUEST="+fixtureJobRequest)
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
				"HORDE_JOB_REQUEST="+fixtureJobRequest)
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
