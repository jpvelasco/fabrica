package secureparam

import (
	"strings"
	"testing"
)

var testScope = Scope{Prefix: "/fabrica/perforce", Leaf: "/admin-password"}

const testNonce = "0123456789abcdef0123456789abcdef"

func TestNameFor(t *testing.T) {
	got, err := testScope.NameFor(testNonce)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/fabrica/perforce/" + testNonce + "/admin-password"; got != want {
		t.Fatalf("NameFor = %q, want %q", got, want)
	}
	other, err := testScope.NameFor("fedcba0987654321fedcba0987654321")
	if err != nil {
		t.Fatal(err)
	}
	if got == other {
		t.Fatalf("two nonces produced the same name %q", got)
	}
}

func TestNameForRejectsMalformedNonces(t *testing.T) {
	for _, nonce := range []string{"", "   ", "a/b", "a\\b", "/a", "a/"} {
		if _, err := testScope.NameFor(nonce); err == nil {
			t.Errorf("NameFor(%q): expected an error", nonce)
		}
	}
}

func TestNewNameIsScopedAndUnique(t *testing.T) {
	a, err := testScope.NewName()
	if err != nil {
		t.Fatal(err)
	}
	b, err := testScope.NewName()
	if err != nil {
		t.Fatal(err)
	}
	if !testScope.IsName(a) || !testScope.IsName(b) {
		t.Fatalf("NewName produced names outside the scope: %q, %q", a, b)
	}
	if a == b {
		t.Fatalf("two NewName calls produced the same name %q", a)
	}
}

func TestIsName(t *testing.T) {
	valid := "/fabrica/perforce/" + testNonce + "/admin-password"
	if !testScope.IsName(valid) {
		t.Fatalf("IsName(%q) = false, want true", valid)
	}
	for _, name := range []string{
		"",
		"/fabrica/perforce/admin-password",     // no nonce
		"/fabrica/perforce//admin-password",    // empty nonce
		"/fabrica/perforce/a/b/admin-password", // nested nonce
		"/fabrica/other/" + testNonce + "/admin-password",                     // wrong prefix
		"/fabrica/perforce/" + testNonce,                                      // missing leaf
		valid + "/extra",                                                      // trailing junk
		"/fabrica/perforce/" + testNonce + "/dcv-password",                    // wrong leaf
		"/fabrica/perforce/" + strings.ToUpper(testNonce) + "/admin-password", // uppercase hex
		"/fabrica/perforce/0123456789abcdef/admin-password",                   // short nonce
		"/fabrica/perforce/" + testNonce + "0/admin-password",                 // long nonce
		"/fabrica/perforce/0123456789abcdef0123456789abcdeg/admin-password",   // non-hex
		"/fabrica/perforce/abc\\cd/admin-password",                            // backslash
	} {
		if testScope.IsName(name) {
			t.Errorf("IsName(%q) = true, want false", name)
		}
	}
}

func TestARN(t *testing.T) {
	name := "/fabrica/perforce/" + testNonce + "/admin-password"
	got, err := testScope.ARN("us-west-2", "123456789012", name)
	if err != nil {
		t.Fatal(err)
	}
	const want = "arn:aws:ssm:us-west-2:123456789012:parameter/fabrica/perforce/0123456789abcdef0123456789abcdef/admin-password"
	if got != want {
		t.Fatalf("ARN = %q, want %q", got, want)
	}
	if strings.Contains(got, "*") {
		t.Fatalf("ARN = %q, want a fully scoped ARN with no wildcard", got)
	}
}

func TestARNRequiresRegionAndAccount(t *testing.T) {
	name := "/fabrica/perforce/" + testNonce + "/admin-password"
	if _, err := testScope.ARN("", "123", name); err == nil {
		t.Error("expected an error for an empty region")
	}
	if _, err := testScope.ARN("us-west-2", "", name); err == nil {
		t.Error("expected an error for an empty account")
	}
}

func TestARNRejectsNamesOutsideScope(t *testing.T) {
	if _, err := testScope.ARN("us-west-2", "123", "/fabrica/perforce/admin-password"); err == nil {
		t.Error("ARN must reject a name without a nonce")
	}
}

func TestNewNonce(t *testing.T) {
	a, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if !isNonce(a) || !isNonce(b) {
		t.Fatalf("NewNonce returned a malformed nonce: %q, %q", a, b)
	}
	if a == b {
		t.Fatalf("two NewNonce calls returned %q", a)
	}
}

func TestIsNonce(t *testing.T) {
	if !isNonce(testNonce) {
		t.Error("a 32-char lowercase-hex string must be a nonce")
	}
	if isNonce("0123") || isNonce(strings.Repeat("g", nonceLen)) {
		t.Error("short or non-hex strings must not be nonces")
	}
}
