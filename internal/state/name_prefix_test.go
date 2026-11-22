package state

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/tags"
)

const (
	testAccount = "123456789012"
	testRegion  = "us-west-2"
	legacyDoc   = `{"version":"0.1","account":"123456789012","region":"us-west-2","modules":[],"history":[],"updated":"2026-01-01T00:00:00Z","created":"2026-01-01T00:00:00Z"}`
)

func encodeBound(t *testing.T, prefix naming.Prefix) []byte {
	t.Helper()
	st := NewState(testAccount, testRegion)
	if err := st.BindOwnership(tags.Identity{}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindNamePrefix(prefix); err != nil {
		t.Fatal(err)
	}
	return EncodeState(st)
}

func TestLegacyDocumentResolvesToDefaultPrefix(t *testing.T) {
	st, err := ParseState([]byte(legacyDoc), testAccount, testRegion)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ValidateNamePrefix(naming.Default); err != nil {
		t.Fatalf("legacy document rejected under default prefix: %v", err)
	}
	if err := st.ValidateNamePrefix(naming.MustResolve("acme")); err == nil {
		t.Fatal("legacy document accepted under acme prefix")
	}
}

func TestPrefixMismatchRefusedWithRemedy(t *testing.T) {
	st, err := ParseState(encodeBound(t, naming.MustResolve("acme")), testAccount, testRegion)
	if err != nil {
		t.Fatal(err)
	}
	err = st.ValidateNamePrefix(naming.Default)
	if err == nil {
		t.Fatal("acme state accepted under default prefix")
	}
	for _, want := range []string{`"acme"`, `"fabrica"`, "namePrefix", "fabrica destroy --all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if err := st.ValidateNamePrefix(naming.MustResolve("acme")); err != nil {
		t.Fatalf("matching prefix refused: %v", err)
	}
	if err := st.BindNamePrefix(naming.Default); err == nil {
		t.Fatal("bind replaced a loaded prefix binding")
	}
}

func TestDefaultPrefixDocumentRoundTripsByteIdentical(t *testing.T) {
	data := encodeBound(t, naming.Default)
	if bytes.Contains(data, []byte("namePrefix")) {
		t.Fatalf("default prefix recorded in document:\n%s", data)
	}
	st, err := ParseState(data, testAccount, testRegion)
	if err != nil {
		t.Fatal(err)
	}
	if got := EncodeState(st); !bytes.Equal(got, data) {
		t.Fatalf("round trip changed document:\n%s\nwant:\n%s", got, data)
	}
}

func TestNonDefaultPrefixIsRecordedAndRoundTrips(t *testing.T) {
	data := encodeBound(t, naming.MustResolve("acme"))
	if !bytes.Contains(data, []byte(`"namePrefix": "acme"`)) {
		t.Fatalf("acme prefix not recorded:\n%s", data)
	}
	st, err := ParseState(data, testAccount, testRegion)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ValidateNamePrefix(naming.MustResolve("acme")); err != nil {
		t.Fatal(err)
	}
}

func TestPrefixValidationEdges(t *testing.T) {
	if _, err := ParseState([]byte(`{"namePrefix":"Bad_Name"}`), "", ""); err == nil {
		t.Fatal("invalid recorded prefix parsed")
	}
	if err := (*State)(nil).ValidateNamePrefix(naming.Default); err == nil {
		t.Fatal("nil state validated")
	}
	if err := (*State)(nil).BindNamePrefix(naming.Default); err == nil {
		t.Fatal("nil state bound")
	}
}

func TestBackendNamesFollowPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	got := ResolveBackendNames(acme, nil, testAccount)
	if got.Bucket != "acme-state-"+testAccount || got.Table != "acme-state-lock" {
		t.Fatalf("derived names = %+v", got)
	}
	if id := acme.StateLockID(testAccount); id != "acme-state/"+testAccount {
		t.Fatalf("lock ID = %q", id)
	}

	cfg := config.Defaults()
	cfg.State.Bucket = "team-bucket"
	got = ResolveBackendNames(acme, cfg, testAccount)
	if got.Bucket != "team-bucket" || got.Table != "acme-state-lock" {
		t.Fatalf("explicit bucket did not win: %+v", got)
	}

	plan := NewSetupPlan(acme, cfg, testAccount, testRegion)
	if plan.Prefix != acme || plan.Backend != got {
		t.Fatalf("setup plan = %+v", plan)
	}
}

func TestConfiguredBackendNamesStillGated(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	if _, err := ConfiguredBackendNames(cfg, testAccount); err == nil {
		t.Fatal("custom prefix resolved while the config gate is closed")
	}
}

func TestConfiguredBackendNamesDefaultPrefix(t *testing.T) {
	cfg := config.Defaults()
	names, err := ConfiguredBackendNames(cfg, testAccount)
	if err != nil || names != ResolveBackendNames(naming.Default, cfg, testAccount) {
		t.Fatalf("default names = %+v, %v", names, err)
	}
}
