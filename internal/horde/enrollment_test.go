package horde

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testTLSConnection = "horde.example.internal"

func httpConnection() Connection {
	return Connection{Version: 1, Port: 5000, GRPCPort: 5002}
}

func tlsConnection() Connection {
	return Connection{Version: 1, TLS: true, ServerName: testTLSConnection, Port: 5000, GRPCPort: 5000,
		CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
}

func mustScript(t *testing.T, c Connection, mode EnrollmentMode, hosts []string) string {
	t.Helper()
	script, err := EnrollmentScript(c, mode, hosts)
	if err != nil {
		t.Fatalf("EnrollmentScript(%s): %v", mode, err)
	}
	return script
}

// heredocs returns the body of every quoted 'PY' heredoc in the script.
func heredocs(script string) []string {
	const open, closing = "<<'PY'\n", "\nPY\n"
	var blocks []string
	rest := script
	for {
		start := strings.Index(rest, open)
		if start < 0 {
			return blocks
		}
		rest = rest[start+len(open):]
		end := strings.Index(rest, closing)
		if end < 0 {
			return blocks
		}
		blocks = append(blocks, rest[:end+1])
		rest = rest[end+len(closing):]
	}
}

func TestEnrollmentScriptTLSLoopbackOnly(t *testing.T) {
	script := mustScript(t, tlsConnection(), EnrollmentList, nil)
	for _, want := range []string{
		"HORDE_URL='https://horde.example.internal:5000'",
		"--proto '=https'",
		"--cacert '/etc/horde/tls/ca.pem'",
		"--resolve 'horde.example.internal:5000:127.0.0.1'",
		`"$HORDE_URL/api/v1/enrollment"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("TLS script missing %q", want)
		}
	}
	if strings.Contains(script, "http://127.0.0.1") {
		t.Error("TLS script must not talk to plain HTTP on loopback")
	}
}

func TestEnrollmentScriptHTTPLoopback(t *testing.T) {
	script := mustScript(t, httpConnection(), EnrollmentList, nil)
	if !strings.Contains(script, "HORDE_URL='http://127.0.0.1:5000'") || !strings.Contains(script, "--proto '=http'") {
		t.Fatalf("HTTP script must target loopback over http:\n%s", script)
	}
	if strings.Contains(script, "--cacert") || strings.Contains(script, "--resolve") {
		t.Error("HTTP loopback needs no trust material")
	}
}

func TestEnrollmentScriptNeverEchoesSecrets(t *testing.T) {
	script := mustScript(t, httpConnection(), EnrollmentApprove, []string{"agent-1"})
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "admin-password") {
			continue
		}
		for _, banned := range []string{"echo", "cat ", "curl", "--data"} {
			if strings.Contains(line, banned) {
				t.Errorf("password path on a line with %q: %s", banned, line)
			}
		}
	}
	if !strings.Contains(script, "trap 'rm -rf \"$WORK\"' EXIT") || !strings.Contains(script, "WORK=$(mktemp -d)") {
		t.Error("script must use a mktemp directory with a cleanup trap")
	}
	if !strings.Contains(script, "os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)") {
		t.Error("request bodies must be written 0600")
	}
}

func TestEnrollmentScriptHostsAreBase64Data(t *testing.T) {
	hosts := []string{"agent-1.example", "agent-2"}
	script := mustScript(t, httpConnection(), EnrollmentApprove, hosts)
	if strings.Contains(script, "agent-1.example") {
		t.Error("hosts must travel base64-encoded, not as raw shell text")
	}
	var b64 string
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(line, "HOSTS_B64="); ok {
			b64 = strings.Trim(v, "'")
		}
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("HOSTS_B64 is not base64: %v", err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil || len(got) != 2 || got[0] != "agent-1.example" {
		t.Fatalf("decoded hosts = %v, %v", got, err)
	}
}

func TestEnrollmentScriptFieldNamesAreDeclaredOnce(t *testing.T) {
	script := mustScript(t, httpConnection(), EnrollmentList, nil)
	for _, want := range []string{"HOST_FIELD='hostName'", "KEY_FIELD='key'", "AGENTS_FIELD='agents'"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %s", want)
		}
	}
}

func TestEnrollmentScriptRejectsUnsafeSelections(t *testing.T) {
	tooMany := make([]string, maxEnrollmentHosts+1)
	for i := range tooMany {
		tooMany[i] = "agent-" + strconv.Itoa(i)
	}
	tests := []struct {
		name  string
		mode  EnrollmentMode
		hosts []string
	}{
		{"quote injection", EnrollmentApprove, []string{"a'; rm -rf /; echo '"}},
		{"command substitution", EnrollmentApprove, []string{"$(id)"}},
		{"leading hyphen", EnrollmentApprove, []string{"-agent"}},
		{"underscore", EnrollmentApprove, []string{"agent_1"}},
		{"empty label", EnrollmentApprove, []string{"agent..1"}},
		{"too long", EnrollmentApprove, []string{strings.Repeat("a", 254)}},
		{"duplicate", EnrollmentApprove, []string{"agent-1", "agent-1"}},
		{"too many", EnrollmentApprove, tooMany},
		{"approve without hosts", EnrollmentApprove, nil},
		{"list with hosts", EnrollmentList, []string{"agent-1"}},
		{"unknown mode", EnrollmentMode("delete"), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EnrollmentScript(httpConnection(), tc.mode, tc.hosts); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestEnrollmentScriptRejectsInvalidConnection(t *testing.T) {
	c := tlsConnection()
	c.ServerName = "http://bad"
	if _, err := EnrollmentScript(c, EnrollmentList, nil); err == nil {
		t.Fatal("expected invalid TLS identity to be rejected before rendering")
	}
}

func TestEnrollmentScriptSyntaxIsValid(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	script := mustScript(t, tlsConnection(), EnrollmentApprove, []string{"agent-1"})
	path := filepath.Join(t.TempDir(), "enroll.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	for i, block := range heredocs(script) {
		cmd := exec.Command(python, "-I", "-c", "import sys; compile(sys.stdin.read(), 'block', 'exec')")
		cmd.Stdin = strings.NewReader(block)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("python block %d does not compile: %v\n%s", i, err, out)
		}
	}
}

// planResult is the outcome of running the rendered plan step.
type planResult struct {
	summary  EnrollmentReport
	approve  string
	stderr   string
	runError error
}

// runPlanStep executes the rendered plan heredoc (the second python block)
// against a canned enrollment response.
func runPlanStep(t *testing.T, mode EnrollmentMode, hosts []string, enrollment string) planResult {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	script := mustScript(t, httpConnection(), mode, hosts)
	blocks := heredocs(script)
	if len(blocks) < 2 {
		t.Fatal("script must contain a login and a plan python block")
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "enrollment.json"), []byte(enrollment), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(hosts)
	if hosts == nil {
		encoded = []byte("[]")
	}
	cmd := exec.Command(python, "-I", "-", work)
	cmd.Stdin = strings.NewReader(blocks[1])
	cmd.Env = append(os.Environ(),
		"MODE="+string(mode),
		"HOSTS_B64="+base64.StdEncoding.EncodeToString(encoded),
		"HOST_FIELD=hostName", "KEY_FIELD=key", "AGENTS_FIELD=agents",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	result := planResult{runError: cmd.Run(), stderr: stderr.String()}
	if raw, err := os.ReadFile(filepath.Join(work, "summary.json")); err == nil {
		if err := json.Unmarshal(raw, &result.summary); err != nil {
			t.Fatalf("summary is not JSON: %v", err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(work, "approve.json")); err == nil {
		result.approve = string(raw)
	}
	return result
}

func TestEnrollmentPlanListsPendingAndCountsUnnamed(t *testing.T) {
	res := runPlanStep(t, EnrollmentList, nil, `[
		{"hostName":"agent-1","key":"KEY-ONE","description":"x"},
		{"hostname":"bad name!","key":"KEY-TWO"},
		{"HOSTNAME":"agent-2","KEY":"KEY-THREE"}
	]`)
	if res.runError != nil {
		t.Fatalf("list failed: %v", res.runError)
	}
	if strings.Join(res.summary.Pending, ",") != "agent-1,agent-2" || res.summary.Unnamed != 1 {
		t.Fatalf("summary = %+v", res.summary)
	}
	if res.approve != "" {
		t.Fatal("list mode must never write an approval body")
	}
	if strings.Contains(res.summary.Pending[0], "KEY") {
		t.Fatal("keys must not appear in the summary")
	}
}

func TestEnrollmentPlanAcceptsWrapperObjects(t *testing.T) {
	for _, doc := range []string{
		`{"agents":[{"hostName":"agent-1","key":"K1"}]}`,
		`{"enrollments":[{"hostName":"agent-1","key":"K1"}]}`,
	} {
		res := runPlanStep(t, EnrollmentList, nil, doc)
		if res.runError != nil || strings.Join(res.summary.Pending, ",") != "agent-1" {
			t.Fatalf("%s: summary=%+v err=%v", doc, res.summary, res.runError)
		}
	}
}

func TestEnrollmentPlanApprovesExactKeysOnly(t *testing.T) {
	doc := `[{"hostName":"agent-1","key":"KEY-ONE"},{"hostName":"agent-2","key":"KEY-TWO"}]`
	res := runPlanStep(t, EnrollmentApprove, []string{"agent-2"}, doc)
	if res.runError != nil {
		t.Fatalf("approve failed: %v", res.runError)
	}
	var body struct {
		Agents []struct {
			Key string `json:"key"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(res.approve), &body); err != nil || len(body.Agents) != 1 || body.Agents[0].Key != "KEY-TWO" {
		t.Fatalf("approval body = %q (%v)", res.approve, err)
	}
	if strings.Join(res.summary.Approved, ",") != "agent-2" {
		t.Fatalf("approved = %v", res.summary.Approved)
	}
}

func TestEnrollmentPlanIsAllOrNothing(t *testing.T) {
	doc := `[{"hostName":"agent-1","key":"KEY-ONE"}]`
	for _, tc := range []struct {
		name  string
		hosts []string
		doc   string
	}{
		{"unknown host", []string{"agent-1", "ghost"}, doc},
		{"duplicate pending host", []string{"agent-1"}, `[{"hostName":"agent-1","key":"A"},{"hostName":"agent-1","key":"B"}]`},
		{"unnamed host requested", []string{"agent-1"}, `[{"hostName":"bad host","key":"A"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runPlanStep(t, EnrollmentApprove, tc.hosts, tc.doc)
			if res.runError == nil || res.approve != "" {
				t.Fatalf("expected failure with no approval body; err=%v body=%q", res.runError, res.approve)
			}
		})
	}
}

// Python's $ also matches before a trailing newline; the coordinator check
// must match Go's RE2 semantics, so such a name counts as unnamed.
func TestEnrollmentPlanTreatsTrailingNewlineHostAsUnnamed(t *testing.T) {
	res := runPlanStep(t, EnrollmentList, nil, `[{"hostName":"agent-1\n","key":"A"}]`)
	if res.runError != nil {
		t.Fatalf("list failed: %v", res.runError)
	}
	if len(res.summary.Pending) != 0 || res.summary.Unnamed != 1 {
		t.Fatalf("summary = %+v, want the newline host counted as unnamed", res.summary)
	}
}

func TestEnrollmentPlanFailsLoudlyOnUnexpectedShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"missing key", `[{"hostName":"agent-1"}]`},
		{"missing hostName", `[{"key":"K1"}]`},
		{"neither field", `[{"other":"x"}]`},
		{"non-object entry", `["agent-1"]`},
		{"object without list", `{"count":1}`},
		{"not JSON", `<html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runPlanStep(t, EnrollmentList, nil, tc.doc)
			if res.runError == nil {
				t.Fatal("expected a loud failure")
			}
			if strings.Contains(res.stderr, "K1") {
				t.Fatal("stderr must not echo enrollment keys")
			}
		})
	}
}

func TestEnrollmentPlanNeverLeaksKeysOnFailure(t *testing.T) {
	res := runPlanStep(t, EnrollmentApprove, []string{"ghost"}, `[{"hostName":"agent-1","key":"SECRET-KEY-VALUE"}]`)
	if res.runError == nil {
		t.Fatal("unknown host must fail")
	}
	if strings.Contains(res.stderr, "SECRET-KEY-VALUE") || strings.Contains(res.stderr, "ghost") {
		t.Fatalf("stderr leaked data: %q", res.stderr)
	}
}

func TestParseEnrollmentOutputAcceptsSummary(t *testing.T) {
	got, err := ParseEnrollmentOutput(`{"pending":["agent-1"],"approved":[],"unnamed":2}` + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Pending) != 1 || got.Pending[0] != "agent-1" || got.Unnamed != 2 || len(got.Approved) != 0 {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestParseEnrollmentOutputRejectsBadSummaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"malformed", `{"pending":`},
		{"unknown field", `{"pending":[],"approved":[],"unnamed":0,"keys":["x"]}`},
		{"missing pending", `{"approved":[],"unnamed":0}`},
		{"missing approved", `{"pending":[],"unnamed":0}`},
		{"missing unnamed", `{"pending":[],"approved":[]}`},
		{"negative unnamed", `{"pending":[],"approved":[],"unnamed":-1}`},
		{"trailing data", `{"pending":[],"approved":[],"unnamed":0}{}`},
		{"invalid name", `{"pending":["bad name\u001b[31m"],"approved":[],"unnamed":0}`},
		{"invalid approved name", `{"pending":[],"approved":["x;rm"],"unnamed":0}`},
		{"oversized", `{"pending":["` + strings.Repeat("a", enrollmentOutputLimit) + `"],"approved":[],"unnamed":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseEnrollmentOutput(tc.output); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestValidateEnrollmentHostsAcceptsValidNames(t *testing.T) {
	if err := ValidateEnrollmentHosts([]string{"agent-1", "ip-10-0-1-2.ec2.internal"}); err != nil {
		t.Fatal(err)
	}
}
