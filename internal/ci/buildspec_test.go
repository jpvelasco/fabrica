package ci

import (
	"encoding/base64"
	"strings"
	"testing"
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
	doc := inlinePolicyDocument(testPlan())
	if !strings.Contains(doc, "/aws/codebuild/"+defaultProjectName) {
		t.Errorf("inline policy not scoped to project log group:\n%s", doc)
	}
}
