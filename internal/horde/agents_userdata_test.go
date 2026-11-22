package horde

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestAgentUserDataRaw(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	if !strings.Contains(raw, "10.0.1.10") {
		t.Error("user data should contain coordinator IP")
	}
	if !strings.Contains(raw, "5000") {
		t.Error("user data should contain coordinator port")
	}
}

func TestAgentUserDataBase64(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	encoded, err := AgentUserData(cfg)
	if err != nil {
		t.Fatalf("AgentUserData: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	if !strings.Contains(string(decoded), "10.0.1.10") {
		t.Error("decoded user data should contain coordinator IP")
	}
}

func TestAgentUserDataMissingCoordinatorIP(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "",
		CoordinatorPort: 5000,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorIP is empty")
	}
}

func TestAgentUserDataMissingCoordinatorPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 0,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorPort is 0")
	}
}

func TestAgentUserDataBase64Error(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "",
		CoordinatorPort: 0,
	}
	_, err := AgentUserData(cfg)
	if err == nil {
		t.Fatal("expected error from AgentUserData when validation fails")
	}
}

func TestAgentUserDataRawMissingPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: -1,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorPort is negative")
	}
}

func TestAgentUserDataServiceName(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must reference the agent service name, not the full server.
	if !strings.Contains(raw, "horde-agent") {
		t.Error("user data should reference horde-agent service")
	}
	// Must start the agent service specifically.
	if !strings.Contains(raw, "systemctl start horde-agent") {
		t.Error("user data should start horde-agent via systemctl")
	}
}

func TestAgentUserDataConfigPath(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.2.50",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must write to the correct config path.
	if !strings.Contains(raw, "/etc/horde/coordinator.conf") {
		t.Error("user data should write to /etc/horde/coordinator.conf")
	}
	// Must use INI format with [coordinator] section.
	if !strings.Contains(raw, "[coordinator]") {
		t.Error("user data should write INI format with [coordinator] section")
	}
	// Must write host and port keys.
	if !strings.Contains(raw, "host = 10.0.2.50") {
		t.Error("user data should write host key with coordinator IP")
	}
	if !strings.Contains(raw, "port = 5000") {
		t.Error("user data should write port key")
	}
}

func TestAgentUserDataNoSecrets(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// No passwords, tokens, or secrets should appear in the UserData.
	secretWords := []string{"password", "secret", "token", "api_key", "credential"}
	for _, word := range secretWords {
		if strings.Contains(strings.ToLower(raw), word) {
			t.Errorf("user data should not contain %q", word)
		}
	}
}

func TestAgentUserDataEnvVars(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.3.100",
		CoordinatorPort: 5002,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must set environment variables for coordinator discovery.
	if !strings.Contains(raw, "HORDE_COORDINATOR_HOST=10.0.3.100") {
		t.Error("user data should set HORDE_COORDINATOR_HOST env var")
	}
	if !strings.Contains(raw, "HORDE_COORDINATOR_PORT=5002") {
		t.Error("user data should set HORDE_COORDINATOR_PORT env var")
	}
}

func TestAgentUserDataNoFullComposeStack(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must NOT run docker compose up -d without specifying the agent service.
	// The Docker fallback should only start the agent service, not the full stack.
	if strings.Contains(raw, "docker compose up -d\n") {
		t.Error("user data should not run 'docker compose up -d' without specifying agent service")
	}
	// If docker compose is used, it must target the agent service only.
	if strings.Contains(raw, "docker compose") {
		if !strings.Contains(raw, "docker compose up -d agent") {
			t.Error("user data docker compose should only start the agent service")
		}
	}
}

func TestAgentUserDataReadinessSentinel(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must touch the agent-specific readiness sentinel.
	if !strings.Contains(raw, "/var/lib/cloud/instance/horde-agent-ready") {
		t.Error("user data should touch horde-agent-ready sentinel")
	}
}

func TestAgentUserDataGRPCPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:       "10.0.1.10",
		CoordinatorPort:     5000,
		CoordinatorGRPCPort: 5002,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}
	if !strings.Contains(raw, "HORDE_COORDINATOR_GRPC_PORT=5002") {
		t.Error("user data should set HORDE_COORDINATOR_GRPC_PORT env var")
	}
	if !strings.Contains(raw, "grpc_port = 5002") {
		t.Error("user data should write the grpc_port key to the INI config")
	}

	// A zero gRPC port omits both lines entirely.
	cfg.CoordinatorGRPCPort = 0
	raw, err = AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}
	if strings.Contains(raw, "HORDE_COORDINATOR_GRPC_PORT") {
		t.Error("user data should omit the gRPC env var when the port is zero")
	}
	if strings.Contains(raw, "grpc_port") {
		t.Error("user data should omit the grpc_port key when the port is zero")
	}
}

func TestAgentUserDataCustomPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 8080,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Custom port should appear in config and env vars.
	if !strings.Contains(raw, "port = 8080") {
		t.Error("user data should write custom port to config")
	}
	if !strings.Contains(raw, "HORDE_COORDINATOR_PORT=8080") {
		t.Error("user data should set custom port in env var")
	}
}

func tlsAgentConfig(t *testing.T) AgentUserDataConfig {
	t.Helper()
	c, err := ConnectionFromConfig(tlsConfig())
	if err != nil {
		t.Fatalf("ConnectionFromConfig: %v", err)
	}
	return AgentUserDataConfig{Connection: c}
}

func renderTLSAgent(t *testing.T) string {
	t.Helper()
	raw, err := AgentUserDataRaw(tlsAgentConfig(t))
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}
	return raw
}

func requireAll(t *testing.T, script string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(script, want) {
			t.Errorf("agent script missing %q", want)
		}
	}
}

func forbidAll(t *testing.T, script string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(script, bad) {
			t.Errorf("agent script must not contain %q", bad)
		}
	}
}

// requireOrder asserts each marker appears, in the given sequence.
func requireOrder(t *testing.T, script string, markers ...string) {
	t.Helper()
	last := -1
	for _, m := range markers {
		i := strings.Index(script, m)
		if i <= last {
			t.Errorf("agent script: %q must appear after the preceding step (index %d, previous %d)", m, i, last)
			return
		}
		last = i
	}
}

func TestAgentUserDataTLSNativeContract(t *testing.T) {
	raw := renderTLSAgent(t)
	requireAll(t, raw,
		"Horde__Server=fabrica\n",
		"Horde__ServerProfiles__fabrica__Url=https://horde.example.internal:5000\n",
		"EnvironmentFile=/etc/horde/fabrica-agent.env",
		"systemctl restart horde-agent.service",
		`test "$(cat /etc/horde/fabrica-native-agent-contract)" = 1`,
		`test "$(systemctl show horde-agent.service --property=LoadState --value)" = loaded`,
		`test "$(id -u "$agent_user")" != 0`,
		"--proto '=https'",
	)
	forbidAll(t, raw, "--proto '=http'", "coordinator.conf", "HORDE_COORDINATOR_", "horde-agent-ready")
}

func TestAgentUserDataTLSProbeRetryIsBounded(t *testing.T) {
	requireAll(t, renderTLSAgent(t), "probe_coordinator() {", "for attempt in $(seq 1 30); do", "sleep 10")
}

func TestAgentUserDataTLSSentinelFollowsProbe(t *testing.T) {
	// TLS stays fail-closed: nothing is written or started until the probe passes.
	requireOrder(t, renderTLSAgent(t),
		"\nprobe_coordinator\n",
		"cat > /etc/horde/fabrica-agent.env",
		"systemctl restart horde-agent.service",
		"touch /etc/horde/agent-configured",
	)
}

func TestAgentUserDataTLSSyntax(t *testing.T) {
	syntaxCheck(t, renderTLSAgent(t))
}

// agentSandboxRun is the observable outcome of running an agent script against
// fake system commands.
type agentSandboxRun struct {
	err          error
	attempts     int
	sleeps       int
	envWritten   bool
	configured   bool
	combinedText string
}

// runAgentSandbox executes the rendered script with fake systemctl, id, sleep
// and curl. Paths under /etc are redirected into a temp directory, and the fake
// curl fails its first curlFailures calls, so the probe's retry loop is real.
func runAgentSandbox(t *testing.T, script string, curlFailures int) agentSandboxRun {
	t.Helper()
	bash := requireTool(t, "bash")
	requireTool(t, "python3")

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	horde := filepath.Join(dir, "horde")
	systemd := filepath.Join(dir, "systemd")
	for _, d := range []string{bin, horde, systemd} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(horde, "fabrica-native-agent-contract"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, body := range fakeAgentCommands(dir, curlFailures) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}

	redirect := strings.NewReplacer(
		"/etc/horde", horde,
		"/etc/systemd/system", systemd,
		"/var/log/fabrica-horde-agent-init.log", filepath.Join(dir, "init.log"),
	)
	cmd := exec.Command(bash, "-c", redirect.Replace(script))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return agentSandboxRun{
		err:          err,
		attempts:     readCount(t, filepath.Join(dir, "attempts")),
		sleeps:       readCount(t, filepath.Join(dir, "sleeps")),
		envWritten:   fileExists(filepath.Join(horde, "fabrica-agent.env")),
		configured:   fileExists(filepath.Join(horde, "agent-configured")),
		combinedText: string(out),
	}
}

func fakeAgentCommands(dir string, curlFailures int) map[string]string {
	return map[string]string{
		"systemctl": `case "$*" in *LoadState*) echo loaded ;; *User*) echo fixture-agent ;; esac` + "\n",
		"id":        "echo 1001\n",
		"sleep":     "echo x >> '" + dir + "/sleeps'\n",
		"curl": fmt.Sprintf(`n=$(cat '%[1]s/attempts' 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > '%[1]s/attempts'
if [ "$n" -le %[2]d ]; then exit 7; fi
case "$*" in
  *--http2*) echo '200 2' ;;
  *) printf '{"http":0,"https":5000,"unencryptedHttp2":0}\n200' ;;
esac
`, dir, curlFailures),
	}
}

func requireTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not installed on this platform", name)
	}
	return path
}

func readCount(t *testing.T, file string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
		return n
	}
	return strings.Count(string(data), "\n")
}

func fileExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func TestAgentUserDataTLSProbeBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux native service")
	}
	script := renderTLSAgent(t)

	t.Run("retries until the coordinator answers, then records success", func(t *testing.T) {
		run := runAgentSandbox(t, script, 1)
		if run.err != nil || !run.configured || !run.envWritten {
			t.Fatalf("got err=%v configured=%v env=%v: %s", run.err, run.configured, run.envWritten, run.combinedText)
		}
	})
	t.Run("fails closed after 30 attempts without writing agent configuration", func(t *testing.T) {
		run := runAgentSandbox(t, script, 1000)
		if run.err == nil || run.configured || run.envWritten || run.attempts != 30 || run.sleeps != 30 {
			t.Fatalf("got err=%v configured=%v env=%v attempts=%d sleeps=%d: %s", run.err, run.configured, run.envWritten, run.attempts, run.sleeps, run.combinedText)
		}
	})
}
