package ci

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/iamrole"
)

func TestBuildspecRawSubmitsToHorde(t *testing.T) {
	spec := BuildspecRaw(testPlan())
	for _, want := range []string{
		"version: 0.2",
		"$HORDE_URL/api/v1/jobs",
		"$TARGET",
		"BUILDGRAPH",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("buildspec missing %q:\n%s", want, spec)
		}
	}
}

func TestBuildspecIsBase64OfRaw(t *testing.T) {
	plan := testPlan()
	encoded := Buildspec(plan)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("Buildspec is not valid base64: %v", err)
	}
	if string(decoded) != BuildspecRaw(plan) {
		t.Errorf("decoded buildspec != raw")
	}
}

func TestInlinePolicyScopedToProjectLogGroup(t *testing.T) {
	plan := testPlan()
	pol := iamrole.CICodeBuildInlinePolicy(plan.Region, plan.Account, plan.ProjectName)
	b, _ := json.Marshal(pol["PolicyDocument"])
	doc := string(b)
	if !strings.Contains(doc, "/aws/codebuild/"+defaultProjectName) {
		t.Errorf("inline policy not scoped to project log group:\n%s", doc)
	}
}

func TestInlinePolicyIncludesVPCDescribePermissions(t *testing.T) {
	plan := testPlan()
	pol := iamrole.CICodeBuildInlinePolicy(plan.Region, plan.Account, plan.ProjectName)
	b, _ := json.Marshal(pol["PolicyDocument"])
	doc := string(b)
	for _, want := range []string{
		"ec2:CreateNetworkInterface",
		"ec2:CreateNetworkInterfacePermission",
		"ec2:DeleteNetworkInterface",
		"ec2:DescribeDhcpOptions",
		"ec2:DescribeInstances",
		"ec2:DescribeNetworkInterfaces",
		"ec2:DescribeSecurityGroups",
		"ec2:DescribeSubnets",
		"ec2:DescribeVpcs",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("inline policy missing %q:\n%s", want, doc)
		}
	}
}

// TestBuildspecRawPreflightsJobAPI verifies the buildspec probes the Horde
// job-creation route before submitting, so a coordinator built without the
// jobs API (404 on /api/v1/jobs) fails with a clear message instead of a
// bare curl error.
func TestBuildspecRawPreflightsJobAPI(t *testing.T) {
	spec := BuildspecRaw(testPlan())
	for _, want := range []string{
		"/api/v1/jobs",
		"no job-creation API",
		"horde-ami",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("buildspec missing pre-flight for %q:\n%s", want, spec)
		}
	}
}

func TestBuildspecRawAuthenticatesWithServiceToken(t *testing.T) {
	spec := BuildspecRaw(testPlan())
	for _, want := range []string{
		"shell: bash",
		`HORDE_AUTH=(-H "Authorization: ServiceAccount ${HORDE_TOKEN}")`,
		`"${HORDE_AUTH[@]}" "$HORDE_URL/api/v1/jobs"`,
		`curl -fsS -X POST "$HORDE_URL/api/v1/jobs" "${HORDE_AUTH[@]}"`,
		"401|403)",
		"Horde rejected the CI credentials",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("buildspec missing %q", want)
		}
	}
	if strings.Count(spec, "HORDE_AUTH=()") != 2 {
		t.Error("each phase that calls Horde must rebuild the auth header (phases may not share a shell)")
	}
	if strings.Contains(spec, "echo \"$HORDE_TOKEN") || strings.Contains(spec, "echo $HORDE_TOKEN") {
		t.Error("the token must never be echoed")
	}
}
