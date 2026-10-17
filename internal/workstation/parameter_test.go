package workstation

import (
	"regexp"
	"strings"
	"testing"
)

func TestSessionPasswordParameterARN(t *testing.T) {
	const nonce = "0123456789abcdef0123456789abcdef"
	const name = "/fabrica/workstation/" + nonce + "/dcv-password"
	got, err := SessionPasswordParameterARN("us-west-2", "575108928122", name)
	if err != nil {
		t.Fatal(err)
	}
	const want = "arn:aws:ssm:us-west-2:575108928122:parameter/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password"
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
	got, err := SessionPasswordParameterFor("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	// Distinct nonces yield distinct names (concurrent creates never collide).
	other, err := SessionPasswordParameterFor("fedcba0987654321fedcba0987654321")
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
	valid := "/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password"
	if !IsSessionPasswordName(valid) {
		t.Fatalf("IsSessionPasswordName(%q) = false, want true", valid)
	}
	for _, bad := range []string{
		"",
		"/fabrica/workstation/dcv-password", // missing nonce segment
		"/fabrica/workstation/0123456789abcdef0123456789abcde/dcv-password",   // 31 hex chars
		"/fabrica/workstation/0123456789abcdef0123456789abcdef0/dcv-password", // 33 hex chars
		"/fabrica/workstation/0123456789ABCDEF0123456789ABCDEF/dcv-password",  // uppercase hex
		"/fabrica/workstation/0123456789abcdef0123456789abcdeg/dcv-password",  // non-hex character
		"/fabrica/workstation/abc123def4567890/dcv-password",                  // pre-#498 16-hex nonce
		"/fabrica/workstation/a/b/dcv-password",                               // nested nonce
		"fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password",   // missing leading slash
		"/fabrica/perforce/0123456789abcdef0123456789abcdef/dcv-password",     // wrong prefix
		"/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-secret",    // wrong leaf
		"/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password/", // trailing slash
	} {
		if IsSessionPasswordName(bad) {
			t.Errorf("IsSessionPasswordName(%q) = true, want false", bad)
		}
	}
}

func TestNewCreateNonce(t *testing.T) {
	a, err := NewCreateNonce()
	if err != nil {
		t.Fatal(err)
	}
	if !nonceHex.MatchString(a) {
		t.Fatalf("nonce %q, want 32 hex chars", a)
	}
	b, err := NewCreateNonce()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two nonces matched: %q", a)
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
