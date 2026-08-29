package ci

import (
	"encoding/base64"
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/iamrole"
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
phases:
  pre_build:
    commands:
      - 'echo "Fabrica CI build starting"'
      - 'test -n "$HORDE_URL" || { echo "HORDE_URL not set — is Horde provisioned?"; exit 1; }'
      - 'test -n "$TARGET" || { echo "TARGET not set"; exit 1; }'
      # Pre-flight: confirm the coordinator exposes the job-creation API. A Horde
      # build without the jobs/graphs/agents controllers returns 404 on
      # /api/v1/jobs; fail fast with a clear message instead of a bare curl error.
      - |
        if curl -fsS -o /dev/null "$HORDE_URL/api/v1/jobs"; then
          echo "Horde job API check passed"
        else
          echo "Horde has no job-creation API: $HORDE_URL/api/v1/jobs is not reachable." >&2
          echo "This Horde build does not expose the jobs API surface (see docs/horde-ami.md)." >&2
          exit 1
        fi
      # Perforce sync placeholder (out of scope for V1):
      # - 'p4 -p "$P4PORT" sync ...'
  build:
    commands:
      - 'echo "Submitting BuildGraph job to Horde at $HORDE_URL"'
      - |
        curl -fsS -X POST "$HORDE_URL/api/v1/jobs" \
          -H "Content-Type: application/json" \
          -d "{\"name\":\"${BUILDGRAPH:-fabrica-ci}\",\"target\":\"$TARGET\"}"
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
	// The template is currently static; the function takes plan for parity with
	// the other modules' generators and to allow future per-plan customisation.
	_ = plan
	return buildspecTemplate
}

// inlinePolicyDocument returns the least-privilege inline IAM policy granting the
// CodeBuild role CloudWatch Logs write access and EC2 permissions for VPC
// networking (ENI lifecycle + describe) and coordinator address resolution.
// It re-uses the shared iamrole helper so create and export stay byte-equal.
func inlinePolicyDocument(plan *CreatePlan) string {
	pol := iamrole.CICodeBuildInlinePolicy(plan.Region, plan.Account, plan.ProjectName)
	b, _ := json.Marshal(pol["PolicyDocument"])
	return string(b)
}
