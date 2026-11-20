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

const testAgentCoordinatorIP = "10.0.1.10"

func httpAgentConfig() AgentUserDataConfig {
	return AgentUserDataConfig{CoordinatorIP: testAgentCoordinatorIP, CoordinatorPort: 5000}
}

func tlsAgentConfig(t *testing.T) AgentUserDataConfig {
	t.Helper()
	c, err := ConnectionFromConfig(tlsConfig())
	if err != nil {
		t.Fatalf("ConnectionFromConfig: %v", err)
	}
	return AgentUserDataConfig{Connection: c}
}

func renderAgent(t *testing.T, cfg AgentUserDataConfig) string {
	t.Helper()
	raw, err := AgentUserDataRaw(cfg)
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

func TestAgentUserDataRaw(t *testing.T) {
	raw := renderAgent(t, httpAgentConfig())
	requireAll(t, raw,
		"Horde__Server=fabrica\n",
		"Horde__ServerProfiles__fabrica__Name=fabrica\n",
		"Horde__ServerProfiles__fabrica__Url=http://10.0.1.10:5000\n",
	)
}

func TestAgentUserDataBase64(t *testing.T) {
	encoded, err := AgentUserData(httpAgentConfig())
	if err != nil {
		t.Fatalf("AgentUserData: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if string(decoded) != renderAgent(t, httpAgentConfig()) {
		t.Error("base64 user data does not decode to the raw script")
	}
}

func TestAgentUserDataRejectsInvalidConfig(t *testing.T) {
	for name, cfg := range map[string]AgentUserDataConfig{
		"empty coordinator IP": {CoordinatorPort: 5000},
		"non-IP coordinator":   {CoordinatorIP: "10.0.1.10\nHorde__Server=evil", CoordinatorPort: 5000},
		"zero port":            {CoordinatorIP: testAgentCoordinatorIP},
		"negative port":        {CoordinatorIP: testAgentCoordinatorIP, CoordinatorPort: -1},
		"relative CA path":     {CoordinatorIP: testAgentCoordinatorIP, CoordinatorPort: 5000, CAPath: "relative.pem"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := AgentUserDataRaw(cfg); err == nil {
				t.Error("AgentUserDataRaw accepted invalid config")
			}
			if _, err := AgentUserData(cfg); err == nil {
				t.Error("AgentUserData accepted invalid config")
			}
		})
	}
}

func TestAgentUserDataServiceName(t *testing.T) {
	raw := renderAgent(t, httpAgentConfig())
	requireAll(t, raw,
		"systemctl restart horde-agent.service",
		"/etc/systemd/system/horde-agent.service.d/fabrica.conf",
		"EnvironmentFile=/etc/horde/fabrica-agent.env",
	)
}

func TestAgentUserDataHasNoLegacyConfiguration(t *testing.T) {
	raw := renderAgent(t, httpAgentConfig())
	forbidAll(t, raw,
		"coordinator.conf",
		"[coordinator]",
		"HORDE_COORDINATOR_",
		"docker",
		"ExecStartPre",
		"horde-agent-ready",
		"|| true",
	)
}

func TestAgentUserDataNoSecrets(t *testing.T) {
	for name, cfg := range map[string]AgentUserDataConfig{"http": httpAgentConfig(), "tls": tlsAgentConfig(t)} {
		t.Run(name, func(t *testing.T) {
			raw := strings.ToLower(renderAgent(t, cfg))
			for _, word := range []string{"password", "secret", "token", "api_key", "credential"} {
				if strings.Contains(raw, word) {
					t.Errorf("user data should not contain %q", word)
				}
			}
		})
	}
}

func TestAgentUserDataProtocolFollowsMode(t *testing.T) {
	httpRaw := renderAgent(t, httpAgentConfig())
	requireAll(t, httpRaw, "--proto '=http'")
	forbidAll(t, httpRaw, "--proto '=https'")

	tlsRaw := renderAgent(t, tlsAgentConfig(t))
	requireAll(t, tlsRaw,
		"--proto '=https'",
		"Horde__ServerProfiles__fabrica__Url=https://horde.example.internal:5000\n",
	)
	forbidAll(t, tlsRaw, "--proto '=http'")
}

func TestAgentUserDataRequiresNativeContract(t *testing.T) {
	for name, cfg := range map[string]AgentUserDataConfig{"http": httpAgentConfig(), "tls": tlsAgentConfig(t)} {
		t.Run(name, func(t *testing.T) {
			requireAll(t, renderAgent(t, cfg),
				`test "$(cat /etc/horde/fabrica-native-agent-contract)" = 1`,
				`test "$(systemctl show horde-agent.service --property=LoadState --value)" = loaded`,
				`test "$(id -u "$agent_user")" != 0`,
			)
		})
	}
}

func TestAgentUserDataProbeRetryIsBounded(t *testing.T) {
	for name, cfg := range map[string]AgentUserDataConfig{"http": httpAgentConfig(), "tls": tlsAgentConfig(t)} {
		t.Run(name, func(t *testing.T) {
			requireAll(t, renderAgent(t, cfg), "probe_coordinator() {", "for attempt in $(seq 1 30); do", "sleep 10")
		})
	}
}

func TestAgentUserDataSentinelFollowsProbe(t *testing.T) {
	const probeCall = "\nprobe_coordinator\n"
	const sentinel = "touch /etc/horde/agent-configured"
	const envWrite = "cat > /etc/horde/fabrica-agent.env"
	const restart = "systemctl restart horde-agent.service"

	requireOrder(t, renderAgent(t, httpAgentConfig()), envWrite, restart, probeCall, sentinel)
	// TLS stays fail-closed: nothing is written or started until the probe passes.
	requireOrder(t, renderAgent(t, tlsAgentConfig(t)), probeCall, envWrite, restart, sentinel)
}

func TestAgentUserDataSyntax(t *testing.T) {
	syntaxCheck(t, renderAgent(t, httpAgentConfig()))
	syntaxCheck(t, renderAgent(t, tlsAgentConfig(t)))
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
  *https://*) printf '{"http":0,"https":5000,"unencryptedHttp2":0}\n200' ;;
  *) printf '{"http":5000}\n200' ;;
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

func TestAgentUserDataProbeBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux native service")
	}
	httpScript := renderAgent(t, httpAgentConfig())
	tlsScript := renderAgent(t, tlsAgentConfig(t))

	t.Run("http retries until the coordinator answers, then records success", func(t *testing.T) {
		run := runAgentSandbox(t, httpScript, 2)
		if run.err != nil || !run.configured || run.attempts != 3 || run.sleeps != 2 {
			t.Fatalf("got err=%v configured=%v attempts=%d sleeps=%d: %s", run.err, run.configured, run.attempts, run.sleeps, run.combinedText)
		}
	})
	t.Run("http keeps the started service but fails without sentinel after 30 attempts", func(t *testing.T) {
		run := runAgentSandbox(t, httpScript, 1000)
		if run.err == nil || run.configured || !run.envWritten || run.attempts != 30 || run.sleeps != 30 {
			t.Fatalf("got err=%v configured=%v env=%v attempts=%d sleeps=%d: %s", run.err, run.configured, run.envWritten, run.attempts, run.sleeps, run.combinedText)
		}
	})
	t.Run("tls retries until the coordinator answers, then records success", func(t *testing.T) {
		run := runAgentSandbox(t, tlsScript, 1)
		if run.err != nil || !run.configured || !run.envWritten {
			t.Fatalf("got err=%v configured=%v env=%v: %s", run.err, run.configured, run.envWritten, run.combinedText)
		}
	})
	t.Run("tls fails closed after 30 attempts without writing agent configuration", func(t *testing.T) {
		run := runAgentSandbox(t, tlsScript, 1000)
		if run.err == nil || run.configured || run.envWritten || run.attempts != 30 {
			t.Fatalf("got err=%v configured=%v env=%v attempts=%d: %s", run.err, run.configured, run.envWritten, run.attempts, run.combinedText)
		}
	})
}
