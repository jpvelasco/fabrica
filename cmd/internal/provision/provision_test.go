package provision

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	st, err := ReadState(context.Background(), globals.Runtime{})
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

	st, err := ReadState(context.Background(), globals.Runtime{Config: cfg})
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Account != "123456789012" || st.Region != "eu-west-1" {
		t.Errorf("account/region = %q/%q, want from config", st.Account, st.Region)
	}
}

func TestReadState_RejectsMismatchedAccount(t *testing.T) {
	t.Chdir(t.TempDir())

	// Seed the default state file with a different account.
	st := fabricastate.NewState("111111111111", "us-east-1")
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}

	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "222222222222"
	_, err := ReadState(context.Background(), globals.Runtime{Config: cfg})
	if err == nil {
		t.Fatal("expected account mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "state target mismatch") {
		t.Errorf("error = %v, want target mismatch", err)
	}
}

func TestReadState_ProfileUsesNamespacedFile(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "222222222222"
	cfg.Cloud.AWS.Region = "eu-west-1"
	rt := globals.Runtime{Config: cfg, ConfigPath: "fabrica-prod.yaml"}

	st, err := ReadState(context.Background(), rt)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Account != "222222222222" || st.Region != "eu-west-1" {
		t.Errorf("account/region = %q/%q, want from config", st.Account, st.Region)
	}

	if err := StateWriter(context.Background(), rt)(st); err != nil {
		t.Fatalf("StateWriter: %v", err)
	}

	profilePath := filepath.Join(".fabrica", "state-prod.json")
	if _, err := os.Stat(profilePath); err != nil {
		t.Errorf("expected %s to exist: %v", profilePath, err)
	}
	if _, err := os.Stat(fabricastate.DefaultStateFile); !os.IsNotExist(err) {
		t.Errorf("default state file must stay untouched, stat err = %v", err)
	}
}

func TestStatePath(t *testing.T) {
	if got, want := StatePath(globals.Runtime{}), fabricastate.DefaultStateFile; got != want {
		t.Errorf("StatePath(default) = %q, want %q", got, want)
	}
	rt := globals.Runtime{ConfigPath: "fabrica-prod.yaml"}
	if got, want := StatePath(rt), filepath.Join(".fabrica", "state-prod.json"); got != want {
		t.Errorf("StatePath(profile) = %q, want %q", got, want)
	}
}

func TestStateWriter_WritesToProfileFile(t *testing.T) {
	t.Chdir(t.TempDir())

	rt := globals.Runtime{ConfigPath: "fabrica-staging.yaml"}
	writer := StateWriter(context.Background(), rt)
	st := fabricastate.NewState("333333333333", "ap-southeast-2")
	if err := writer(st); err != nil {
		t.Fatalf("StateWriter: %v", err)
	}

	got, err := fabricastate.ReadStateFile(filepath.Join(".fabrica", "state-staging.json"), "", "")
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if got.Account != "333333333333" {
		t.Errorf("profile file account = %q, want 333333333333", got.Account)
	}
	if _, err := os.Stat(fabricastate.DefaultStateFile); !os.IsNotExist(err) {
		t.Errorf("default state file must stay untouched, stat err = %v", err)
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
