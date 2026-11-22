package naming

import (
	"strings"
	"testing"
)

func TestResolveSelectsDefaultForEmptyOrFabrica(t *testing.T) {
	for _, in := range []string{"", DefaultName} {
		got, err := Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", in, err)
		}
		if got != Default {
			t.Fatalf("Resolve(%q) = %+v, want Default", in, got)
		}
	}
}

func TestResolveAcceptsValidPrefixes(t *testing.T) {
	for _, in := range []string{"acme", "a", "studio-tools", "a1-b2", strings.Repeat("a", MaxLen)} {
		got, err := Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", in, err)
		}
		if got.String() != in {
			t.Fatalf("Resolve(%q).String() = %q", in, got.String())
		}
	}
}

func TestResolveRejectsInvalidPrefixes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "Acme", want: "is not valid"},
		{in: "1acme", want: "is not valid"},
		{in: "acme-", want: "is not valid"},
		{in: "-acme", want: "is not valid"},
		{in: "ac me", want: "is not valid"},
		{in: "acme_lab", want: "is not valid"},
		{in: "a.b", want: "is not valid"},
		{in: strings.Repeat("a", MaxLen+1), want: "is not valid"},
		{in: "acme--lab", want: `contains "--"`},
		{in: "sg", want: "reserved"},
		{in: "sg-lab", want: "reserved"},
		{in: "xn-acme", want: "reserved"},
		{in: "sthree-acme", want: "reserved"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			_, err := Resolve(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve(%q) error = %v, want containing %q", tc.in, err, tc.want)
			}
		})
	}
}

func TestMustResolve(t *testing.T) {
	if got := MustResolve("acme").String(); got != "acme" {
		t.Fatalf("MustResolve(acme) = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustResolve(Acme) did not panic")
		}
	}()
	MustResolve("Acme")
}

func TestZeroValueIsDefault(t *testing.T) {
	var zero Prefix
	if zero != Default || zero.String() != DefaultName {
		t.Fatalf("zero prefix = %q, want %q", zero.String(), DefaultName)
	}
}

func TestBuilders(t *testing.T) {
	acme := MustResolve("acme")
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"For", acme.For("horde", "agents", "asg"), "acme-horde-agents-asg"},
		{"For default", Default.For("perforce"), "fabrica-perforce"},
		{"SG", acme.SG("perforce"), "acme-perforce-sg"},
		{"Role", acme.Role("lore"), "acme-lore-role"},
		{"Profile", acme.Profile("ddc"), "acme-ddc-profile"},
		{"Instance", acme.Instance("workstation"), "acme-workstation"},
		{"Bucket", acme.Bucket("lore-store", "123456789012", "us-west-2"), "acme-lore-store-123456789012-us-west-2"},
		{"ParamPath", acme.ParamPath("horde"), "/acme/horde"},
		{"ParamPath default", Default.ParamPath("workstation"), "/fabrica/workstation"},
		{"LogGroup", acme.LogGroup("ci"), "/acme/ci"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
