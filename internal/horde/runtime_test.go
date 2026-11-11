package horde

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestRuntimeArtifacts(t *testing.T) {
	for _, cfg := range []config.HordeConfig{{}, tlsConfig()} {
		c, _ := ConnectionFromConfig(cfg)
		raw, err := RuntimeOverlay(c)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct {
				Ports       []string
				Environment map[string]string
				Volumes     []map[string]any
				ExtraHosts  []string `json:"extra_hosts"`
			}
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		service := doc.Services["horde"]
		if len(service.Ports) != len(c.ListenerPorts()) {
			t.Fatal(raw)
		}
		if c.TLS && (service.Environment["Horde__HttpPort"] != "0" || service.Environment["Horde__Http2Port"] != "0" || len(service.Volumes) != 1 || len(service.ExtraHosts) != 1) {
			t.Fatal(raw)
		}
		validation, err := RuntimeValidation(c)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(validation, "base.get(\"ports\")") {
			t.Fatal("base port gate absent")
		}
		syntaxCheck(t, validation)
	}
	bad := Connection{}
	if _, err := RuntimeOverlay(bad); err == nil {
		t.Fatal("invalid connection accepted")
	}
	if _, err := RuntimeValidation(bad); err == nil {
		t.Fatal("invalid connection accepted")
	}
	if ShellQuote("a'b") != "'a'\"'\"'b'" {
		t.Fatal("shell quotation")
	}
}

func syntaxCheck(t *testing.T, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed on this platform")
	}
	file := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bash, "-n", file).CombinedOutput(); err != nil {
		t.Fatalf("syntax: %s %v", out, err)
	}
}

func TestTLSBootstrapAndAgent(t *testing.T) {
	c, _ := ConnectionFromConfig(tlsConfig())
	name, err := ServiceTokenParameterFor("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	script, err := GenerateRaw(UserDataConfig{MongoPassword: "not-rendered", Port: c.Port, Connection: c, ServiceTokenParameter: name, Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fabrica-horde-bootstrap", "--pull never", "--cacert", "--resolve", "service-token-pending", "bootstrap-complete", "unencryptedHttp2"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(script, "not-rendered") || strings.Contains(script, " -k ") || strings.Contains(script, "http://127.0.0.1:5000") {
		t.Fatal("secret/insecure local call")
	}
	// The installed retry script must exist before it runs certificate checks.
	if strings.Index(script, "cat > /usr/local/sbin/fabrica-horde-bootstrap") > strings.Index(script, "\n/usr/local/sbin/fabrica-horde-validate\n") {
		t.Fatal("retry script not installed first")
	}
	syntaxCheck(t, script)
	agent, err := AgentUserDataRaw(AgentUserDataConfig{Connection: c, CAPath: "/etc/horde/tls/ca'file.pem"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Horde__Server=fabrica", "EnvironmentFile=", "systemctl restart horde-agent.service", "--http2", "agent-configured", "SSL_CERT_FILE=", `test "$(id -u "$agent_user")" != 0`} {
		if !strings.Contains(agent, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(agent, "|| true") || strings.Contains(agent, "horde-agent-ready") {
		t.Fatal("false readiness")
	}
	syntaxCheck(t, agent)
	agent, err = AgentUserDataRaw(AgentUserDataConfig{Connection: c})
	if err != nil || strings.Contains(agent, "SSL_CERT_FILE") {
		t.Fatal("system trust mode", err)
	}
	if _, err := AgentUserDataRaw(AgentUserDataConfig{Connection: c, CAPath: "relative"}); err == nil {
		t.Fatal("bad trust path")
	}
	c.Version = 0
	if _, err := GenerateRaw(UserDataConfig{MongoPassword: "fixture-password", Port: c.Port, Connection: c}); err == nil || !strings.Contains(err.Error(), "unsupported Horde connection version") {
		t.Fatal("unversioned TLS bootstrap silently became HTTP", err)
	}
}

func TestDependentContract(t *testing.T) {
	c, _ := ConnectionFromConfig(tlsConfig())
	r := state.ModuleResource{Properties: map[string]string{ConnectionProperty: c.Record(), "hordeCIContract": "1"}}
	if err := ValidateDependent(c, r, "hordeCIContract"); err != nil {
		t.Fatal(err)
	}
	delete(r.Properties, "hordeCIContract")
	if err := ValidateDependent(c, r, "hordeCIContract"); err == nil {
		t.Fatal("accepted legacy dependent")
	}
	c.TLS = false
	if err := ValidateDependent(c, r, "hordeCIContract"); err != nil {
		t.Fatal(err)
	}
}
