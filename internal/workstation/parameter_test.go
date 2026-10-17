package workstation

import (
	"regexp"
	"strings"
	"testing"
)

func TestSessionPasswordParameterARN(t *testing.T) {
	const name = "/fabrica/workstation/abc123def4567890/dcv-password"
	got, err := SessionPasswordParameterARN("us-west-2", "575108928122", name)
	if err != nil {
		t.Fatal(err)
	}
	const want = "arn:aws:ssm:us-west-2:575108928122:parameter/fabrica/workstation/abc123def4567890/dcv-password"
	if got != want {
		t.Fatalf("ARN = %q, want %q", got, want)
	}
	// No region or account wildcard may appear: the read policy must scope
	// to exactly one parameter.
	if strings.Contains(got, "*") {
		t.Fatalf("ARN = %q, want a fully scoped ARN", got)
	}
	if _, err := SessionPasswordParameterARN("", "123", name); err == nil {
		t.Fatal("expected error for empty region")
	}
	if _, err := SessionPasswordParameterARN("us-west-2", "", name); err == nil {
		t.Fatal("expected error for empty account")
	}
}

func TestSessionPasswordParameterARNRejectsBadNames(t *testing.T) {
	for _, name := range []string{
		"",
		"/fabrica/workstation/dcv-password",           // the old shared name
		"/fabrica/workstation//dcv-password",          // empty nonce
		"/fabrica/workstation/a/b/dcv-password",       // nested nonce
		"/fabrica/other/abc/dcv-password",             // wrong prefix
		"/fabrica/workstation/abc",                    // missing leaf
		"/fabrica/workstation/abc/dcv-password/extra", // trailing junk
		"/fabrica/workstation/abc\\cd/dcv-password",   // backslash in nonce
	} {
		if _, err := SessionPasswordParameterARN("us-west-2", "123", name); err == nil {
			t.Errorf("ARN(%q): expected an error", name)
		}
	}
}

func TestSessionPasswordParameterFor(t *testing.T) {
	got, err := SessionPasswordParameterFor("abc123def4567890")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/fabrica/workstation/abc123def4567890/dcv-password"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	// Distinct nonces yield distinct names (concurrent creates never collide).
	other, err := SessionPasswordParameterFor("0987654321fedcba")
	if err != nil {
		t.Fatal(err)
	}
	if got == other {
		t.Fatalf("two nonces produced the same name %q", got)
	}
}

func TestSessionPasswordParameterForRejectsMalformedNonces(t *testing.T) {
	for _, nonce := range []string{
		"",
		"   ",
		"a/b",
		"a\\b",
		"/a",
		"a/",
	} {
		if _, err := SessionPasswordParameterFor(nonce); err == nil {
			t.Errorf("nonce %q: expected an error", nonce)
		}
	}
}

func TestIsSessionPasswordName(t *testing.T) {
	valid := "/fabrica/workstation/abc123def4567890/dcv-password"
	if !IsSessionPasswordName(valid) {
		t.Fatalf("IsSessionPasswordName(%q) = false, want true", valid)
	}
	for _, bad := range []string{
		"",
		"/fabrica/workstation/dcv-password",     // missing nonce segment
		"/fabrica/workstation/a/b/dcv-password", // nested nonce
		"fabrica/workstation/abc123def4567890/dcv-password", // missing leading slash
		"/fabrica/perforce/abc123def4567890/dcv-password",   // wrong prefix
		"/fabrica/workstation/abc123def4567890/dcv-secret",  // wrong leaf
	} {
		if IsSessionPasswordName(bad) {
			t.Errorf("IsSessionPasswordName(%q) = true, want false", bad)
		}
	}
}

var nonceHex = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestNewSessionPasswordNonce(t *testing.T) {
	a, err := NewSessionPasswordNonce()
	if err != nil {
		t.Fatal(err)
	}
	if !nonceHex.MatchString(a) {
		t.Fatalf("nonce %q, want 32 hex chars", a)
	}
	b, err := NewSessionPasswordNonce()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two nonces matched: %q", a)
	}
}
