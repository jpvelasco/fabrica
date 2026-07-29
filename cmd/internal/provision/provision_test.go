package provision

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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
		return
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

func TestExistingResource_Found(t *testing.T) {
	st := &fabricastate.State{Modules: []fabricastate.ModuleState{{
		Name:      "ci",
		Resources: []fabricastate.ModuleResource{{TypeName: "AWS::IAM::Role", Identifier: "my-role"}},
	}}}
	res, ok := ExistingResource(st, "ci", "AWS::IAM::Role")
	if !ok {
		t.Fatal("expected resource found")
	}
	if res.Identifier != "my-role" {
		t.Errorf("Identifier = %q, want my-role", res.Identifier)
	}
}

func TestExistingResource_ModuleNotFound(t *testing.T) {
	st := &fabricastate.State{Modules: []fabricastate.ModuleState{}}
	_, ok := ExistingResource(st, "ci", "AWS::IAM::Role")
	if ok {
		t.Error("expected not found for missing module")
	}
}

func TestExistingResource_TypeNotFound(t *testing.T) {
	st := &fabricastate.State{Modules: []fabricastate.ModuleState{{
		Name:      "ci",
		Resources: []fabricastate.ModuleResource{{TypeName: "AWS::IAM::Role", Identifier: "my-role"}},
	}}}
	_, ok := ExistingResource(st, "ci", "AWS::CodeBuild::Project")
	if ok {
		t.Error("expected not found for missing type")
	}
}

func TestAppendUnique_AddsNew(t *testing.T) {
	resources := []fabricastate.ModuleResource{{TypeName: "AWS::IAM::Role", Identifier: "role"}}
	resources = AppendUnique(resources, fabricastate.ModuleResource{TypeName: "AWS::CodeBuild::Project", Identifier: "project"})
	if len(resources) != 2 {
		t.Errorf("len = %d, want 2", len(resources))
	}
}

func TestAppendUnique_SkipsDuplicate(t *testing.T) {
	resources := []fabricastate.ModuleResource{{TypeName: "AWS::IAM::Role", Identifier: "role"}}
	resources = AppendUnique(resources, fabricastate.ModuleResource{TypeName: "AWS::IAM::Role", Identifier: "other-role"})
	if len(resources) != 1 {
		t.Errorf("len = %d, want 1", len(resources))
	}
	if resources[0].Identifier != "role" {
		t.Errorf("Identifier = %q, want role", resources[0].Identifier)
	}
}

func TestConfirmCreate_AssumeYes(t *testing.T) {
	var out bytes.Buffer
	result := ConfirmCreate(&out, "horde", "123456789012", true, nil)
	if !result {
		t.Error("expected true when assumeYes is set")
	}
	if !strings.Contains(out.String(), "Proceeding without interactive confirmation") {
		t.Errorf("missing bypass message: %s", out.String())
	}
}

func TestConfirmCreate_Confirmed(t *testing.T) {
	var out bytes.Buffer
	confirmCalled := false
	result := ConfirmCreate(&out, "perforce", "123456789012", false, func(_, _ string) bool {
		confirmCalled = true
		return true
	})
	if !result {
		t.Error("expected true when user confirms")
	}
	if !confirmCalled {
		t.Error("confirm should have been called")
	}
	if !strings.Contains(out.String(), "Confirmation accepted") {
		t.Errorf("missing accepted message: %s", out.String())
	}
}

func TestConfirmCreate_Cancelled(t *testing.T) {
	var out bytes.Buffer
	result := ConfirmCreate(&out, "horde", "123456789012", false, func(_, _ string) bool {
		return false
	})
	if result {
		t.Error("expected false when user cancels")
	}
	if !strings.Contains(out.String(), "Cancelled. No AWS calls were made") {
		t.Errorf("missing cancel message: %s", out.String())
	}
}

func TestConfirmSetup_AssumeYesBypassesPrompt(t *testing.T) {
	var out bytes.Buffer
	if !ConfirmSetup(&out, "ignored", true, func(string) bool {
		t.Fatal("confirmation called with assumeYes")
		return false
	}) {
		t.Fatal("ConfirmSetup() = false, want true")
	}
	if got, want := out.String(), "Proceeding without confirmation (--yes set).\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestConfirmSetup_Confirmed(t *testing.T) {
	var out bytes.Buffer
	var prompt string
	if !ConfirmSetup(&out, "Create these resources?", false, func(got string) bool {
		prompt = got
		return true
	}) {
		t.Fatal("ConfirmSetup() = false, want true")
	}
	if prompt != "Create these resources?" {
		t.Fatalf("prompt = %q, want %q", prompt, "Create these resources?")
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want none", out.String())
	}
}

func TestConfirmSetup_Cancelled(t *testing.T) {
	var out bytes.Buffer
	var prompt string
	if ConfirmSetup(&out, "Create these resources?", false, func(got string) bool {
		prompt = got
		return false
	}) {
		t.Fatal("ConfirmSetup() = true, want false")
	}
	if prompt != "Create these resources?" {
		t.Fatalf("prompt = %q, want %q", prompt, "Create these resources?")
	}
	if got, want := out.String(), "Setup cancelled. No AWS resources were created.\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
