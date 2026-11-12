package horde

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// RuntimeUnit is the shared bake/migration unit. The bootstrap gate prevents
// publication after a partial first boot. Reload never pulls a new image.
const RuntimeUnit = `[Unit]
Description=Horde Server (docker compose)
After=network-online.target docker.service
Requires=docker.service
ConditionPathExists=/etc/horde/bootstrap-complete

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/etc/horde
ExecStartPre=/usr/local/sbin/fabrica-horde-validate
ExecStart=/usr/bin/docker compose -f /etc/horde/docker-compose.yml -f /etc/horde/fabrica-runtime.json up -d --pull never
ExecStop=/usr/bin/docker compose -f /etc/horde/docker-compose.yml -f /etc/horde/fabrica-runtime.json down
ExecReload=/usr/local/sbin/fabrica-horde-validate
ExecReload=/usr/bin/docker compose -f /etc/horde/docker-compose.yml -f /etc/horde/fabrica-runtime.json restart --no-deps horde

[Install]
WantedBy=multi-user.target
`

// RuntimeOverlay builds the specific supported Compose transport overlay.
func RuntimeOverlay(c Connection) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	env := map[string]string{"Horde__HttpPort": fmt.Sprint(c.Port), "Horde__HttpsPort": "0", "Horde__Http2Port": fmt.Sprint(c.GRPCPort)}
	service := map[string]any{"environment": env}
	ports := []string{}
	for _, port := range c.ListenerPorts() {
		ports = append(ports, fmt.Sprintf("${HORDE_BIND_ADDRESS:-127.0.0.1}:%d:%d", port, port))
	}
	service["ports"] = ports
	if c.TLS {
		env["Horde__HttpPort"], env["Horde__HttpsPort"], env["Horde__Http2Port"] = "0", fmt.Sprint(c.Port), "0"
		env["Horde__ServerUrl"] = c.URL("")
		env["Kestrel__Certificates__Default__Path"] = "/fabrica-tls/" + path.Base(c.CertPath)
		env["Kestrel__Certificates__Default__KeyPath"] = "/fabrica-tls/" + path.Base(c.KeyPath)
		env["SSL_CERT_FILE"] = "/fabrica-tls/" + path.Base(c.CAPath)
		service["volumes"] = []map[string]any{{"type": "bind", "source": path.Dir(c.CertPath), "target": "/fabrica-tls", "read_only": true}}
		service["extra_hosts"] = []string{c.ServerName + ":127.0.0.1"}
	} else {
		env["Horde__ServerUrl"] = "${HORDE_SERVER_URL:?HORDE_SERVER_URL must be set}"
	}
	body, err := json.MarshalIndent(map[string]any{"services": map[string]any{"horde": service}}, "", "  ")
	return string(body) + "\n", err
}

// RuntimeValidation checks the base contract without printing configs or
// environments. Certificate checks are shared by bootstrap, unit and adoption.
func RuntimeValidation(c Connection) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	header := "#!/bin/bash\nset -euo pipefail\n"
	overlay, err := RuntimeOverlay(c)
	if err != nil {
		return "", err
	}
	expected := base64.StdEncoding.EncodeToString([]byte(overlay))
	header += "test \"$(cat /etc/horde/fabrica-runtime-contract)\" = '1'\n"
	header += `python3 - <<'PY'
import base64, json, subprocess, sys
with open("/etc/horde/fabrica-runtime.json") as f:
    actual = json.load(f)["services"]["horde"]
# FABRICA_EXPECTED_RUNTIME
if actual != expected:
    sys.exit("Horde runtime overlay differs from its configured transport contract")
# Read the base independently: Compose appends port lists, which is unsafe.
result = subprocess.run(["docker", "compose", "-f", "/etc/horde/docker-compose.yml", "config", "--no-interpolate", "--format", "json"], capture_output=True)
if result.returncode:
    sys.exit("Horde base Compose validation failed")
try:
    base = json.loads(result.stdout)["services"]["horde"]
except (ValueError, KeyError):
    sys.exit("Invalid Horde base Compose contract")
transport_keys = {"aspnetcore_urls", "horde__serverurl", "horde__serverprivatecert", "horde__httpport", "horde__httpsport", "horde__http2port", "kestrel__certificates__default__path", "kestrel__certificates__default__keypath", "ssl_cert_file"}
if base.get("ports") or base.get("restart", "no") != "no" or any(k.lower() in transport_keys for k in base.get("environment", {})):
    sys.exit("Remove base listener publication and conflicting URL/certificate environment before TLS bootstrap")
PY
`
	header = strings.Replace(header, "# FABRICA_EXPECTED_RUNTIME", "expected = json.loads(base64.b64decode('"+expected+"'))[\"services\"][\"horde\"]", 1)
	if !c.TLS {
		return header, nil
	}
	// Shell arguments use the literal quotation owner. No certificate contents
	// enter the rendered script.
	header += fmt.Sprintf(`cert=%s
key=%s
ca=%s
name=%s
test -r "$cert" && test -r "$key" && test -r "$ca"
test -f "$cert" && test -f "$key" && test -f "$ca"
python3 - "$key" <<'PY'
import os, stat, sys
mode = stat.S_IMODE(os.stat(sys.argv[1]).st_mode)
if mode & 0o037:
    sys.exit("Horde TLS private key must deny group write/execute and all other-user access")
PY
cmp -s <(openssl pkey -in "$key" -passin pass: -pubout 2>/dev/null) <(openssl x509 -in "$cert" -pubkey -noout)
openssl x509 -in "$cert" -ext subjectAltName -noout | grep -q 'DNS:'
openssl verify -CAfile "$ca" -untrusted "$cert" -purpose sslserver -verify_hostname "$name" "$cert" > /dev/null
openssl x509 -in "$cert" -checkend 0 -noout > /dev/null
`, ShellQuote(c.CertPath), ShellQuote(c.KeyPath), ShellQuote(c.CAPath), ShellQuote(c.ServerName))
	return header, nil
}

// ShellQuote renders a literal shell argument. Validation alone is not quoting.
func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func systemdQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// ListenerCheckScript validates the token-free endpoint and HTTP/2 negotiation.
// Options are initialized by the caller and also used for authenticated calls.
func ListenerCheckScript(c Connection) string {
	return fmt.Sprintf(`curl "${CURL_OPTS[@]}" -w '\n%%{http_code}' "$HORDE_URL/api/v1/server/ports" | python3 -c 'import json,sys; body,code=sys.stdin.read().rsplit("\n",1); p=json.loads(body); sys.exit(0 if code=="200" and all(type(p.get(k)) is int for k in ("http","https","unencryptedHttp2")) and p.get("http")==0 and p.get("https")==%d and p.get("unencryptedHttp2")==0 else "Horde TLS listener mismatch")'
test "$(curl "${CURL_OPTS[@]}" --http2 -o /dev/null -w '%%{http_code} %%{http_version}' "$HORDE_URL/api/v1/server/ports")" = '200 2'
`, c.Port)
}

// AdoptionVerifier inspects the running container privately and emits only
// allowlisted results. Remote environment/Compose output never becomes an
// error message or the SSM result.
func AdoptionVerifier(c Connection) (string, error) {
	if !c.TLS {
		return "", fmt.Errorf("Horde adoption requires TLS")
	}
	validation, err := RuntimeValidation(c)
	if err != nil {
		return "", err
	}
	overlay, err := RuntimeOverlay(c)
	if err != nil {
		return "", err
	}
	// Encode data crossing language boundaries instead of treating JSON string
	// syntax as Python source quotation.
	validationData := base64.StdEncoding.EncodeToString([]byte(validation))
	overlayData := base64.StdEncoding.EncodeToString([]byte(overlay))
	return fmt.Sprintf(`python3 - <<'FABRICA_VERIFY'
import base64, json, os, subprocess, sys
try:
    check = subprocess.run(["bash", "-c", base64.b64decode('%s').decode("utf-8")], capture_output=True, timeout=45)
    if check.returncode or not os.path.isfile("/etc/horde/bootstrap-complete"):
        raise ValueError()
    expected = json.loads(base64.b64decode('%s'))["services"]["horde"]
    inspected = subprocess.run(["docker", "inspect", "horde-server"], capture_output=True, timeout=15)
    if inspected.returncode:
        raise ValueError()
    container = json.loads(inspected.stdout)[0]
    if not container["State"]["Running"]:
        raise ValueError()
    env = dict(item.split("=", 1) for item in container["Config"]["Env"] if "=" in item)
    for key, value in expected["environment"].items():
        if env.get(key) != value:
            raise ValueError()
    bindings = container["HostConfig"].get("PortBindings") or {}
    if set(bindings) != {%q + "/tcp"} or not bindings[%q + "/tcp"]:
        raise ValueError()
    for binding in bindings[%q + "/tcp"]:
        if binding.get("HostPort") != %q or binding.get("HostIp") not in ("0.0.0.0", "::"):
            raise ValueError()
    mount = expected["volumes"][0]
    if not any(m.get("Source") == mount["source"] and m.get("Destination") == mount["target"] and not m.get("RW") for m in container["Mounts"]):
        raise ValueError()
    if not set(expected["extra_hosts"]).issubset(set(container["HostConfig"].get("ExtraHosts") or [])):
        raise ValueError()
except Exception:
    print('{"verified":false}')
    sys.exit(1)
print('{"verified":true,"contract":1,"port":%d}')
FABRICA_VERIFY
`, validationData, overlayData, fmt.Sprint(c.Port), fmt.Sprint(c.Port), fmt.Sprint(c.Port), fmt.Sprint(c.Port), c.Port), nil
}
