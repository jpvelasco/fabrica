package horde

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// EnrollmentMode selects what the coordinator-side enrollment script does.
type EnrollmentMode string

const (
	// EnrollmentList reports pending agent enrollments and changes nothing.
	EnrollmentList EnrollmentMode = "list"
	// EnrollmentApprove approves exactly the pending enrollments named by hosts.
	EnrollmentApprove EnrollmentMode = "approve"
)

// Horde enrollment API field names. They are defined only here.
//
// These target Horde 5.8.4 (GET and POST /api/v1/enrollment) and were taken
// from the issue author's working coordinator; they are NOT yet confirmed.
// Pin them from a live 5.8.4 coordinator before release. The script matches
// them case-insensitively, and an unexpected response shape fails loudly.
const (
	enrollmentHostField   = "hostName"
	enrollmentKeyField    = "key"
	enrollmentAgentsField = "agents"
)

// enrollmentOutputLimit caps the coordinator's summary on the local side.
const enrollmentOutputLimit = 64 << 10

const maxEnrollmentHosts = 100

// enrollmentHostPattern accepts RFC 1123 hostnames: dot-separated labels of
// letters, digits, and interior hyphens. Hosts are rendered into the script
// only after this check, and the on-coordinator script compares them as data.
var enrollmentHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// EnrollmentReport is the allowlisted summary the coordinator script prints:
// hostnames and counts only. Enrollment keys and the Admin password never
// leave the coordinator.
type EnrollmentReport struct {
	Pending  []string `json:"pending"`
	Approved []string `json:"approved"`
	Unnamed  int      `json:"unnamed"`
}

// EnrollmentScript renders the script that runs on the coordinator over SSM.
//
// The script reads the Admin password on the coordinator into a 0600 file in
// a private temp directory, logs in over loopback (see LoopbackTarget), and
// lists pending enrollments. In approve mode it POSTs only the keys of the
// named hosts, all or nothing: every host must match exactly one pending
// enrollment before anything is sent. JSON is handled with python3, not jq:
// the coordinator bootstrap already requires python3 (the Admin account step
// in userdata.go), while jq is not in the AMI requirements.
func EnrollmentScript(c Connection, mode EnrollmentMode, hosts []string) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := validateEnrollmentSelection(mode, hosts); err != nil {
		return "", err
	}
	if hosts == nil {
		hosts = []string{}
	}
	encoded, err := json.Marshal(hosts)
	if err != nil {
		return "", fmt.Errorf("encoding enrollment hosts: %w", err)
	}
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -euo pipefail\numask 077\n")
	fmt.Fprintf(&b, "MODE=%s\nHOSTS_B64=%s\n", ShellQuote(string(mode)), ShellQuote(base64.StdEncoding.EncodeToString(encoded)))
	fmt.Fprintf(&b, "HOST_FIELD=%s\nKEY_FIELD=%s\nAGENTS_FIELD=%s\n", ShellQuote(enrollmentHostField), ShellQuote(enrollmentKeyField), ShellQuote(enrollmentAgentsField))
	b.WriteString("export MODE HOSTS_B64 HOST_FIELD KEY_FIELD AGENTS_FIELD\n")
	b.WriteString(LoopbackTarget(c))
	b.WriteString(enrollmentBody)
	return b.String(), nil
}

// ValidateEnrollmentHosts checks that every host is a unique, valid hostname
// and that the batch size is bounded. The command layer uses it before any SSM call.
func ValidateEnrollmentHosts(hosts []string) error {
	if len(hosts) > maxEnrollmentHosts {
		return fmt.Errorf("approve at most %d hosts per run; split the request", maxEnrollmentHosts)
	}
	seen := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		if !validEnrollmentHost(host) {
			return fmt.Errorf("host %q is not a valid agent hostName; use the name shown by 'fabrica horde agents approve'", host)
		}
		if seen[host] {
			return fmt.Errorf("host %q was given more than once; list each host once", host)
		}
		seen[host] = true
	}
	return nil
}

// ParseEnrollmentOutput decodes the summary printed by the coordinator script.
// It rejects oversized, malformed, unknown-field, and incomplete output, and
// any hostname that the script would not have emitted.
func ParseEnrollmentOutput(stdout string) (EnrollmentReport, error) {
	if len(stdout) > enrollmentOutputLimit {
		return EnrollmentReport{}, fmt.Errorf("Horde enrollment output exceeds %d bytes; expected only hostnames and counts", enrollmentOutputLimit)
	}
	var raw struct {
		Pending  *[]string `json:"pending"`
		Approved *[]string `json:"approved"`
		Unnamed  *int      `json:"unnamed"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return EnrollmentReport{}, fmt.Errorf("Horde enrollment output is not the expected summary; re-run 'fabrica horde agents approve' and check the coordinator's SSM output")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return EnrollmentReport{}, fmt.Errorf("Horde enrollment output has trailing data; re-run 'fabrica horde agents approve'")
	}
	if raw.Pending == nil || raw.Approved == nil || raw.Unnamed == nil || *raw.Unnamed < 0 {
		return EnrollmentReport{}, fmt.Errorf("Horde enrollment output is missing pending, approved, or unnamed; the coordinator script and CLI are out of step")
	}
	if err := checkEnrollmentNames(*raw.Pending); err != nil {
		return EnrollmentReport{}, err
	}
	if err := checkEnrollmentNames(*raw.Approved); err != nil {
		return EnrollmentReport{}, err
	}
	return EnrollmentReport{Pending: *raw.Pending, Approved: *raw.Approved, Unnamed: *raw.Unnamed}, nil
}

func validateEnrollmentSelection(mode EnrollmentMode, hosts []string) error {
	switch mode {
	case EnrollmentList:
		if len(hosts) != 0 {
			return fmt.Errorf("listing pending enrollments takes no hosts")
		}
		return nil
	case EnrollmentApprove:
		if len(hosts) == 0 {
			return fmt.Errorf("approval needs at least one host; pass --host or --all")
		}
		return ValidateEnrollmentHosts(hosts)
	default:
		return fmt.Errorf("unsupported enrollment mode %q", mode)
	}
}

func validEnrollmentHost(host string) bool {
	return len(host) <= 253 && enrollmentHostPattern.MatchString(host)
}

func checkEnrollmentNames(names []string) error {
	for _, name := range names {
		if !validEnrollmentHost(name) {
			return fmt.Errorf("Horde enrollment output contains an invalid hostname; re-run 'fabrica horde agents approve' and report the coordinator's Horde version")
		}
	}
	return nil
}

// enrollmentBody runs after the header (mode, hosts, field names, loopback
// target). It never echoes the password, keys, or raw API responses, and its
// only stdout is the summary.
const enrollmentBody = `
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

if [ ! -s /etc/horde/admin-password ]; then
  echo "ERROR: Horde Admin password file is missing; the coordinator bootstrap has not completed" >&2
  exit 1
fi
python3 -I - "$WORK" <<'PY'
import json, os, sys

def write_private(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(text)

with open("/etc/horde/admin-password") as f:
    password = f.read().strip()
write_private(os.path.join(sys.argv[1], "login.json"), json.dumps({"username": "Admin", "password": password}))
PY

code=$(curl "${CURL_OPTS[@]}" -c "$WORK/cookies" -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' --data @"$WORK/login.json" "$HORDE_URL/account/login/dashboard" || true)
case "$code" in
  200|302) ;;
  *) echo "ERROR: Horde Admin login failed (HTTP $code)" >&2; exit 1 ;;
esac

code=$(curl "${CURL_OPTS[@]}" -b "$WORK/cookies" -o "$WORK/enrollment.json" -w '%{http_code}' \
  "$HORDE_URL/api/v1/enrollment" || true)
if [ "$code" != 200 ]; then
  echo "ERROR: Horde enrollment list failed (HTTP $code)" >&2
  exit 1
fi

python3 -I - "$WORK" <<'PY'
import base64, json, os, re, sys

HOST = re.compile(r"^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$")
HOST_FIELD = os.environ["HOST_FIELD"]
KEY_FIELD = os.environ["KEY_FIELD"]
AGENTS_FIELD = os.environ["AGENTS_FIELD"]
MODE = os.environ["MODE"]
WORK = sys.argv[1]

def fail(message):
    sys.stderr.write("ERROR: " + message + "\n")
    sys.exit(1)

def write_private(path, text):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(text)

def find(entry, name):
    for k, v in entry.items():
        if k.lower() == name.lower():
            return True, v
    return False, None

def entries(doc):
    items = doc
    if isinstance(doc, dict):
        found, items = find(doc, AGENTS_FIELD)
        if not found:
            lists = [v for v in doc.values() if isinstance(v, list)]
            items = lists[0] if len(lists) == 1 else None
    if not isinstance(items, list):
        fail("unrecognized Horde enrollment response shape; the API differs from the pinned 5.8.4 field names")
    return items

def pending_enrollments(doc):
    result = []
    for entry in entries(doc):
        if not isinstance(entry, dict):
            fail("unrecognized Horde enrollment entry; the API differs from the pinned 5.8.4 field names")
        key_found, key = find(entry, KEY_FIELD)
        host_found, host = find(entry, HOST_FIELD)
        if not (key_found and host_found) or not isinstance(key, str) or not key:
            fail("enrollment entry lacks the " + HOST_FIELD + " or " + KEY_FIELD + " field; pin the 5.8.4 field names from a live coordinator")
        valid = isinstance(host, str) and len(host) <= 253 and HOST.match(host) is not None
        result.append((host if valid else None, key))
    return result

try:
    with open(os.path.join(WORK, "enrollment.json")) as f:
        document = json.load(f)
except ValueError:
    fail("Horde enrollment response is not JSON")

pending = pending_enrollments(document)
approved = []
if MODE == "approve":
    hosts = json.loads(base64.b64decode(os.environ["HOSTS_B64"]))
    body = []
    for host in hosts:
        keys = [key for name, key in pending if name == host]
        if len(keys) != 1:
            fail("a requested host is not uniquely pending; nothing was approved. Re-run 'fabrica horde agents approve' to list current enrollments")
        body.append({KEY_FIELD: keys[0]})
    if not body:
        fail("no hosts to approve")
    write_private(os.path.join(WORK, "approve.json"), json.dumps({AGENTS_FIELD: body}))
    approved = hosts
elif MODE != "list":
    fail("unknown enrollment mode")

names = [name for name, _ in pending if name is not None]
summary = {"pending": names, "approved": approved, "unnamed": len(pending) - len(names)}
write_private(os.path.join(WORK, "summary.json"), json.dumps(summary))
PY

if [ -f "$WORK/approve.json" ]; then
  code=$(curl "${CURL_OPTS[@]}" -b "$WORK/cookies" -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' --data @"$WORK/approve.json" "$HORDE_URL/api/v1/enrollment" || true)
  case "$code" in
    200|201|204) ;;
    *) echo "ERROR: Horde enrollment approval failed (HTTP $code); re-run 'fabrica horde agents approve' to see current enrollments" >&2; exit 1 ;;
  esac
fi
cat "$WORK/summary.json"
`
