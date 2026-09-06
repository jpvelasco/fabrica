package aws

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapAWSAuthErrorExpiredSSOIncludesLoginHint(t *testing.T) {
	err := wrapAWSAuthError("calling sts:GetCallerIdentity", "studio", errors.New("the SSO session associated with this profile has expired"))
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	got := err.Error()
	for _, want := range []string{"aws sso login", `profile "studio"`, "calling sts:GetCallerIdentity"} {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q: %s", want, got)
		}
	}
}

func TestWrapAWSAuthErrorOtherErrorKeepsOpOnly(t *testing.T) {
	err := wrapAWSAuthError("loading AWS config", "studio", errors.New("access denied"))
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	if strings.Contains(err.Error(), "aws sso login") {
		t.Errorf("non-SSO error should not hint sso login: %v", err)
	}
}

func TestWrapAWSAuthErrorNil(t *testing.T) {
	if wrapAWSAuthError("op", "p", nil) != nil {
		t.Fatal("nil err must stay nil")
	}
}
