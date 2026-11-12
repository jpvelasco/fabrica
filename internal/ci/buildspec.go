package ci

import (
	"encoding/base64"
	"strings"

	"github.com/jpvelasco/fabrica/internal/horde"
)

// buildspecTemplate is the inline CodeBuild buildspec. On each build it submits
// a BuildGraph job to the Horde coordinator at $HORDE_URL, mirroring the request
// that `horde submit` makes (POST /api/v1/jobs with name+target). BUILDGRAPH and
// TARGET are supplied as env overrides by `ci trigger`; HORDE_URL defaults to the
// project's configured value but can also be overridden per build.
//
// A Perforce sync step is intentionally left as a documented placeholder — the
// IAM role can describe the Perforce instance, but active P4 sync is out of
// scope for this milestone (see the design spec).
const buildspecTemplate = `version: 0.2
env:
  shell: bash
phases:
  pre_build:
    commands:
      - 'echo "Fabrica CI build starting"'
      - 'test -n "$HORDE_URL" || { echo "HORDE_URL not set — is Horde provisioned?"; exit 1; }'
      - 'test -n "$TARGET" || { echo "TARGET not set"; exit 1; }'
      # HORDE_TOKEN is a PARAMETER_STORE variable holding the fabrica-ci
      # service-account token; CodeBuild resolves it at build start and masks
      # it in logs. The header is rebuilt in each block that calls Horde.
      # Pre-flight: confirm the coordinator accepts the token and exposes the
      # job-creation API, and report which of the two failed.
      - |
        # FABRICA_CURL_OPTIONS
        # FABRICA_TLS_PREFLIGHT
        HORDE_AUTH=()
        if [ -n "${HORDE_TOKEN:-}" ]; then HORDE_AUTH=(-H "Authorization: ServiceAccount ${HORDE_TOKEN}"); fi
        code=$(curl "${CURL_OPTS[@]}" -o /dev/null -w '%{http_code}' "${HORDE_AUTH[@]}" "$HORDE_URL/api/v1/jobs" || true)
        case "$code" in
          2??) echo "Horde job API check passed" ;;
          401|403)
            echo "Horde rejected the CI credentials (HTTP $code): check the fabrica-ci service-account token parameter and the globals.json ACL." >&2
            exit 1 ;;
          404)
            echo "Horde has no job-creation API: $HORDE_URL/api/v1/jobs returned 404." >&2
            echo "This Horde build does not expose the jobs API surface (see docs/horde-ami.md)." >&2
            exit 1 ;;
          *)
            echo "Horde is not reachable at $HORDE_URL (HTTP $code)." >&2
            exit 1 ;;
        esac
      # Perforce sync placeholder (out of scope for V1):
      # - 'p4 -p "$P4PORT" sync ...'
  build:
    commands:
      - 'echo "Submitting BuildGraph job to Horde at $HORDE_URL"'
      - |
        # FABRICA_CURL_OPTIONS
        HORDE_AUTH=()
        if [ -n "${HORDE_TOKEN:-}" ]; then HORDE_AUTH=(-H "Authorization: ServiceAccount ${HORDE_TOKEN}"); fi
        response=$(mktemp)
        trap 'rm -f "$response"' EXIT
        code=$(curl "${CURL_OPTS[@]}" --fail --max-filesize 65536 -o "$response" -w '%{http_code}' -X POST "$HORDE_URL/api/v1/jobs" "${HORDE_AUTH[@]}" \
          -H "Content-Type: application/json" \
          -d "{\"name\":\"${BUILDGRAPH:-fabrica-ci}\",\"target\":\"$TARGET\"}")
        case "$code" in
          2??) ;;
          *) echo "Horde job submission failed (HTTP $code)" >&2; exit 1 ;;
        esac
        python3 - "$response" <<'PY'
        import json, re, sys
        try:
            with open(sys.argv[1], "rb") as response:
                body = response.read(65537)
            if len(body) > 65536:
                raise ValueError()
            job_id = json.loads(body)["id"]
            if not isinstance(job_id, str) or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", job_id):
                raise ValueError()
        except (ValueError, KeyError, TypeError):
            sys.exit("Horde returned an invalid job identifier; inspect the coordinator job API")
        print("Horde job submitted: " + job_id)
        PY
  post_build:
    commands:
      - 'echo "BuildGraph job submitted"'
`

// Buildspec returns the inline buildspec as a base64-encoded string suitable for
// embedding in the CodeBuild project's Source.BuildSpec field.
func Buildspec(plan *CreatePlan) string {
	return base64.StdEncoding.EncodeToString([]byte(BuildspecRaw(plan)))
}

// BuildspecRaw returns the plain-text buildspec YAML, for test assertions and
// debugging (mirrors the Generate/GenerateRaw convention in other modules).
func BuildspecRaw(plan *CreatePlan) string {
	check := ""
	if plan != nil && plan.HordeConnection.TLS {
		check = strings.TrimSuffix(horde.ListenerCheckScript(plan.HordeConnection), "\n")
		// Preflight must reject 3xx responses too, before authenticated calls.
		check = strings.ReplaceAll(check, `curl "${CURL_OPTS[@]}"`, `curl "${CURL_OPTS[@]}" --fail`)
		check = strings.ReplaceAll(check, "\n", "\n        ")
	}
	result := strings.Replace(buildspecTemplate, "# FABRICA_TLS_PREFLIGHT", check, 1)
	options := `set -e -o pipefail
CURL_OPTS=(--silent --show-error --connect-timeout 5 --max-time 30)
case "$HORDE_URL" in
  https://*) CURL_OPTS+=(--proto '=https') ;;
  http://*) CURL_OPTS+=(--proto '=http') ;;
  *) echo "Invalid Horde URL scheme" >&2; exit 1 ;;
esac
if [ -n "${HORDE_CA_FILE:-}" ]; then
  test -r "$HORDE_CA_FILE" || { echo "HORDE_CA_FILE is not readable" >&2; exit 1; }
  CURL_OPTS+=(--cacert "$HORDE_CA_FILE")
fi`
	options = strings.ReplaceAll(options, "\n", "\n        ")
	return strings.ReplaceAll(result, "# FABRICA_CURL_OPTIONS", options)
}
