package horde

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the Horde cloud-init script.
// MongoPassword is validated (non-empty) but never rendered: the MongoDB and
// Horde Admin passwords are generated on the instance at first boot, so no
// secret travels through user data. Port is used for the health check.
//
// ServiceTokenParameter, when set, makes cloud-init create the fabrica-ci
// service account and publish its token to that per-create SSM SecureString
// in Region. Only the parameter name is rendered, never a token.
type UserDataConfig struct {
	Connection            Connection
	MongoPassword         string
	Port                  int
	ServiceTokenParameter string
	Region                string
}

// AccountName, GroupClaimType, and GroupClaimValue expose the service-account
// constants to the template.
func (UserDataConfig) AccountName() string     { return ServiceAccountName }
func (UserDataConfig) GroupClaimType() string  { return ServiceAccountGroupClaimType }
func (UserDataConfig) GroupClaimValue() string { return ServiceAccountGroup }

var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)

var userDataRenderer = userdata.New(template.Must(template.New("horde-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-init.log) 2>&1

echo "Starting Horde Docker stack..."

# Ensure Docker is running
for i in $(seq 1 12); do
  docker info > /dev/null 2>&1 && break
  [ $i -eq 12 ] && echo "ERROR: Docker did not start within 60s" && exit 1
  sleep 5
done

cd /etc/horde

# Secrets are generated here, on the instance, and never leave it. The compose
# stack reads them from /etc/horde/.env. Horde starts bound to loopback only
# (HORDE_BIND_ADDRESS=127.0.0.1) so nothing on the network can reach it before
# its Admin account exists. Read the Admin password over SSM from
# /etc/horde/admin-password.
umask 077
if [ ! -s /etc/horde/.env ]; then
  IMDS_TOKEN=$(curl -sf -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')
  PRIVATE_IP=$(curl -sf -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/local-ipv4)
  openssl rand -hex 24 > /etc/horde/admin-password
  cat > /etc/horde/.env <<ENV
MONGO_PASSWORD=$(openssl rand -hex 24)
HORDE_BIND_ADDRESS=127.0.0.1
HORDE_SERVER_URL=http://${PRIVATE_IP}:{{ .Port }}
ENV
fi

# horde_up reports whether Horde answers HTTP at all. With built-in accounts
# the root URL redirects to the login page (or returns 401), which is healthy.
horde_up() {
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:{{ .Port }}/ || true)
  [ "$code" != "000" ] && [ "${code:0:1}" != "5" ]
}

# Start the Docker compose stack (MongoDB + Redis + Horde).
# On cold boot the first compose up can exit non-zero: mongo's mongosh
# healthcheck (timeout: 5s) fails for the first ~30s, so the service_healthy
# dependency fails before the stack converges. Retry instead of aborting —
# the stack converges to all-healthy within ~90s.
compose_up() {
  local i
  for i in $(seq 1 8); do
    docker compose up -d && break
    [ $i -eq 8 ] && echo "ERROR: docker compose up -d failed after 8 attempts (~3m)" && exit 1
    sleep 20
  done
}
compose_up

# Wait for Horde to become healthy
for i in $(seq 1 30); do
  horde_up && break
  [ $i -eq 30 ] && echo "ERROR: Horde did not become ready within 5m" && exit 1
  sleep 10
done

# In built-in account mode the anonymous admin-create endpoint is first-caller-
# wins, so create the Admin account now, while the ports are loopback-only.
# 403 means the account already exists (a re-run).
# Request bodies carrying secrets go through 0600 files in a private temp
# directory, never through command-line arguments or the log.
WORK=$(mktemp -d /run/fabrica-horde.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
python3 - /etc/horde/admin-password "$WORK" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    password = f.read().strip()
with open(sys.argv[2] + "/create.json", "w") as f:
    json.dump({"password": password}, f)
with open(sys.argv[2] + "/login.json", "w") as f:
    json.dump({"username": "Admin", "password": password}, f)
PY
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  --data @"$WORK/create.json" \
  http://127.0.0.1:{{ .Port }}/api/v1/accounts/admin/create || true)
case "$code" in
  200|403) ;;
  *) echo "ERROR: Horde Admin account creation failed (HTTP $code)"; exit 1 ;;
esac
{{- if .ServiceTokenParameter }}

# Create the {{ .AccountName }} service account for 'fabrica horde submit' and
# the ci CodeBuild project, still on loopback. Its only claim is the
# {{ .GroupClaimValue }} group, which the baked globals.json ACL limits to
# creating and viewing jobs, so the token is not an administrator. The token
# goes from Horde's response straight into the per-create SSM SecureString.
if [ ! -e /etc/horde/service-token-published ]; then
  if [ ! -s /etc/horde/service-token-pending ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' -c "$WORK/cookies" -X POST -H 'Content-Type: application/json' \
    --data @"$WORK/login.json" http://127.0.0.1:{{ .Port }}/account/login/dashboard || true)
  case "$code" in
    200|302) ;;
    *) echo "ERROR: Horde Admin login failed (HTTP $code)"; exit 1 ;;
  esac
  code=$(curl -s -o "$WORK/account.json" -w '%{http_code}' -b "$WORK/cookies" -X POST -H 'Content-Type: application/json' \
    -d '{"name":"{{ .AccountName }}","description":"fabrica horde submit and ci","claims":[{"type":"{{ .GroupClaimType }}","value":"{{ .GroupClaimValue }}"}],"enabled":true}' \
    http://127.0.0.1:{{ .Port }}/api/v1/serviceaccounts || true)
  if [ "$code" != 200 ]; then
    echo "ERROR: Horde service-account creation failed (HTTP $code)"
    exit 1
  fi
  python3 - "$WORK/account.json" "$WORK/token" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    token = json.load(f).get("secretToken", "")
if not token:
    sys.exit("ERROR: Horde returned no service-account token")
with open(sys.argv[2], "w") as f:
    f.write(token)
PY
    cp "$WORK/token" /etc/horde/service-token-pending
  fi
  /usr/local/bin/aws ssm put-parameter --region {{ .Region }} --name {{ .ServiceTokenParameter }} \
    --type SecureString --overwrite --value "file:///etc/horde/service-token-pending" > /dev/null
  touch /etc/horde/service-token-published
  rm -f /etc/horde/service-token-pending
  echo "Published the Horde service-account token to SSM parameter {{ .ServiceTokenParameter }}"
fi
{{- end }}
rm -rf "$WORK"
trap - EXIT

# Publish Horde on the network (the security group still limits the source CIDR).
sed -i 's/^HORDE_BIND_ADDRESS=.*/HORDE_BIND_ADDRESS=0.0.0.0/' /etc/horde/.env
compose_up
for i in $(seq 1 30); do
  horde_up && break
  [ $i -eq 30 ] && echo "ERROR: Horde did not become ready after publishing within 5m" && exit 1
  sleep 10
done

echo "Horde stack is ready on port {{ .Port }}"
touch /var/lib/cloud/instance/horde-ready
`)))

// validate checks required fields. Returns nil if valid.
func (cfg *UserDataConfig) validate() error {
	if cfg.MongoPassword == "" {
		return fmt.Errorf("MongoPassword must not be empty")
	}
	if cfg.ServiceTokenParameter != "" {
		// Both values are rendered into a root shell script: accept only the
		// exact per-create name shape and a real Region code.
		if !IsServiceTokenName(cfg.ServiceTokenParameter) {
			return fmt.Errorf("ServiceTokenParameter %q is not a per-create %s name", cfg.ServiceTokenParameter, ServiceTokenParameterPrefix)
		}
		if !regionPattern.MatchString(cfg.Region) {
			return fmt.Errorf("Region %q is not a valid AWS Region (required with ServiceTokenParameter)", cfg.Region)
		}
	}
	return nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	script, err := userDataRenderer.Render(cfg)
	if err != nil {
		return "", err
	}
	if cfg.Connection == (Connection{}) {
		return script, nil
	} // custom legacy HTTP AMI
	return runtimeBootstrap(cfg, script)
}

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	raw, err := GenerateRaw(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

func runtimeBootstrap(cfg UserDataConfig, script string) (string, error) {
	c := cfg.Connection
	overlay, err := RuntimeOverlay(c)
	if err != nil {
		return "", err
	}
	validation, err := RuntimeValidation(c)
	if err != nil {
		return "", err
	}
	install := "umask 077\nmkdir -p /etc/horde\n"
	install += "cat > /etc/horde/fabrica-runtime.json <<'FABRICA_RUNTIME'\n" + overlay + "FABRICA_RUNTIME\n"
	install += "cat > /usr/local/sbin/fabrica-horde-validate <<'FABRICA_VALIDATE'\n" + validation + "FABRICA_VALIDATE\nchmod 0750 /usr/local/sbin/fabrica-horde-validate\n"
	install += "cat > /etc/systemd/system/horde.service <<'FABRICA_UNIT'\n" + RuntimeUnit + "FABRICA_UNIT\nsystemctl daemon-reload\n"
	// HTTP remains compatible with old custom AMIs. Only new contract AMIs
	// consume the overlay; TLS requires the contract before any publication.
	var compose string
	if c.TLS {
		compose = "docker compose -f /etc/horde/docker-compose.yml -f /etc/horde/fabrica-runtime.json up -d --pull never"
		script = strings.Replace(script, "echo \"Starting Horde Docker stack...\"", "rm -f /etc/horde/bootstrap-complete\n/usr/local/sbin/fabrica-horde-validate\n\necho \"Starting Horde Docker stack...\"", 1)
	} else {
		compose = `if [ -f /etc/horde/fabrica-runtime-contract ]; then docker compose -f /etc/horde/docker-compose.yml -f /etc/horde/fabrica-runtime.json up -d --pull never; else docker compose up -d; fi`
		install = "if [ -f /etc/horde/fabrica-runtime-contract ]; then\n" + install + "fi\n"
	}
	script = strings.ReplaceAll(script, "docker compose up -d && break", compose+" && break")
	failureGate := `trap 'rm -f /etc/horde/bootstrap-complete; docker stop horde-server >/dev/null 2>&1 || true' ERR
`
	script = strings.Replace(script, "set -euo pipefail", "set -Eeuo pipefail\n"+failureGate, 1)
	// Always reset the bind before a retry. A prior failed final probe must
	// not leave a reboot publishing an incomplete bootstrap.
	reset := "sed -i 's/^HORDE_BIND_ADDRESS=.*/HORDE_BIND_ADDRESS=127.0.0.1/' /etc/horde/.env\n"
	script = strings.Replace(script, "# horde_up reports", reset+"\n# horde_up reports", 1)
	script = strings.Replace(script, "openssl rand -hex 24 > /etc/horde/admin-password", "[ -s /etc/horde/admin-password ] || openssl rand -hex 24 > /etc/horde/admin-password", 1)
	if c.TLS {
		options := "HORDE_URL=" + ShellQuote(c.URL("")) + "\nCURL_OPTS=(--silent --show-error --connect-timeout 5 --max-time 30 --proto '=https' --cacert " + ShellQuote(c.CAPath) + " --resolve " + ShellQuote(fmt.Sprintf("%s:%d:127.0.0.1", c.ServerName, c.Port)) + ")\n"
		script = strings.ReplaceAll(script, "http://127.0.0.1:"+fmt.Sprint(cfg.Port), "$HORDE_URL")
		script = strings.ReplaceAll(script, "curl -s ", `curl "${CURL_OPTS[@]}" `)
		script = strings.Replace(script, "horde_up() {", options+"\nhorde_up() {", 1)
		script = strings.Replace(script, "# Publish Horde on the network", ListenerCheckScript(c)+"\n# Publish Horde on the network", 1)
		script = strings.Replace(script, `echo "Horde stack is ready`, ListenerCheckScript(c)+"\n"+`echo "Horde stack is ready`, 1)
	}
	script += "\nif [ -f /etc/horde/fabrica-runtime-contract ]; then touch /etc/horde/bootstrap-complete; systemctl enable --now horde.service; fi\n"
	// Install before executing so missing certificates are recoverable by
	// rerunning the script after delivery, without rerunning cloud-init.
	return "#!/bin/bash\nset -euo pipefail\n" + install +
		"cat > /usr/local/sbin/fabrica-horde-bootstrap <<'FABRICA_BOOTSTRAP'\n" + script +
		"FABRICA_BOOTSTRAP\nchmod 0700 /usr/local/sbin/fabrica-horde-bootstrap\n/usr/local/sbin/fabrica-horde-bootstrap\n", nil
}
