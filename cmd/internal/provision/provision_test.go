package provision

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestConfirmPhrase(t *testing.T) {
	if got := ConfirmPhrase("perforce", "123456789012"); got != "create perforce 123456789012" {
		t.Errorf("ConfirmPhrase = %q, want %q", got, "create perforce 123456789012")
	}
	if got := ConfirmPhrase("workstation", "acct"); got != "create workstation acct" {
		t.Errorf("ConfirmPhrase = %q", got)
	}
}

func TestPrintConfirmInstructions(t *testing.T) {
	var out bytes.Buffer
	PrintConfirmInstructions(&out, "create horde acct")
	got := out.String()
	for _, want := range []string{
		"Confirmation required.",
		"Type this exact phrase to continue:",
		"  create horde acct",
		"Any other input cancels.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestReadState_NilConfigDefaultsToEmpty(t *testing.T) {
	// No config and no state file: returns a fresh state with empty account/region.
	t.Chdir(t.TempDir())
	st, err := ReadState(globals.Runtime{})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
	}
	if st.Account != "" || st.Region != "" {
		t.Errorf("expected empty account/region, got %q/%q", st.Account, st.Region)
	}
}

func TestReadState_SeedsAccountRegionFromConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Cloud.AWS.Region = "eu-west-1"

	st, err := ReadState(globals.Runtime{Config: cfg})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Account != "123456789012" || st.Region != "eu-west-1" {
		t.Errorf("account/region = %q/%q, want from config", st.Account, st.Region)
	}
}
