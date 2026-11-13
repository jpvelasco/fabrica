package state

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/jpvelasco/fabrica/internal/oplog"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestParseStateOwnershipBinding(t *testing.T) {
	for _, doc := range []string{`{}`, `{"modules":[]}`, `{"modules":[{"name":"horde"}]}`} {
		st, err := ParseState([]byte(doc), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(EncodeState(st), []byte(`"managedBy": "fabrica"`)) {
			t.Fatal("decoded legacy state must be default-bound, including empty inventory")
		}
	}
	if bytes.Contains(EncodeState(NewState("", "")), []byte(`"managedBy"`)) {
		t.Fatal("fresh state must remain unbound")
	}
	for _, owner := range []string{`"fabrica"`, `"Studio-Tools"`, `"7"`} {
		st, err := ParseState([]byte(`{"managedBy":`+owner+`}`), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(EncodeState(st), []byte(`"managedBy": `+owner)) {
			t.Fatalf("decoded owner %s was discarded or changed", owner)
		}
	}
	for _, owner := range []string{`""`, `null`, `true`, `7`, `[]`, `{}`, `" studio"`, `"studio\n"`, `"studio/tools"`, `"` + strings.Repeat("a", 64) + `"`} {
		if _, err := ParseState([]byte(`{"managedBy":`+owner+`}`), "", ""); err == nil || !strings.Contains(err.Error(), "managedBy") {
			t.Fatalf("state accepted invalid binding %s: %v", owner, err)
		}
	}
}

func TestParseStateRequiresDocumentObject(t *testing.T) {
	for _, doc := range []string{"null", " \nnull\t", "[]", `"state"`, "true", "7"} {
		t.Run(doc, func(t *testing.T) {
			st, err := ParseState([]byte(doc), "", "")
			if st != nil || err == nil || !strings.Contains(err.Error(), "parsing state document") {
				t.Fatalf("non-object document became ownership proof: state=%+v, err=%v", st, err)
			}
		})
	}
	for _, doc := range []string{`{}`, `{"modules":[]}`, `{"managedBy":"fabrica"}`} {
		st, err := ParseState([]byte(doc), "", "")
		if err != nil || st == nil || st.ManagedBy != tags.ManagedByValue {
			t.Fatalf("valid object %s rejected: state=%+v, err=%v", doc, st, err)
		}
	}
}

func TestStateOwnershipValidationAndBinding(t *testing.T) {
	custom, err := tags.ResolveIdentity("Studio-Tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []string{`{}`, `{"managedBy":"fabrica"}`, `{"managedBy":"Studio-Tools"}`} {
		st, err := ParseState([]byte(doc), "", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, identity := range []tags.Identity{{}, custom} {
			err := st.ValidateOwnership(identity)
			if match := st.ManagedBy == identity.ManagedBy(); (err == nil) != match {
				t.Fatalf("recorded %q vs selected %q: %v", st.ManagedBy, identity.ManagedBy(), err)
			}
		}
	}
	fresh := NewState("", "")
	if err := fresh.ValidateOwnership(custom); err != nil || fresh.ManagedBy != "" {
		t.Fatalf("read validation bound fresh state: %v", err)
	}
	if err := fresh.BindOwnership(tags.Identity{}); err != nil || fresh.ManagedBy != "fabrica" {
		t.Fatalf("default binding failed: %v", err)
	}
	if err := fresh.BindOwnership(custom); err == nil || fresh.ManagedBy != "fabrica" {
		t.Fatal("prepared state was rebound")
	}
	fresh.ManagedBy = ""
	if err := fresh.BindOwnership(tags.Identity{}); err == nil {
		t.Fatal("prepared binding could be cleared")
	}
	invalid := NewState("", "")
	invalid.ManagedBy = "invalid owner"
	if err := invalid.BindOwnership(tags.Identity{}); err == nil {
		t.Fatal("invalid in-memory binding accepted")
	}
	if err := (*State)(nil).BindOwnership(tags.Identity{}); err == nil {
		t.Fatal("nil state accepted for writing")
	}
}

func TestReadStateFile_FileMissing_ReturnsNew(t *testing.T) {
	t.Chdir(t.TempDir())

	st, err := ReadStateFile(DefaultStateFile, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("ReadStateFile returned error: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
		return
	}
	if st.Account != "123456789012" || st.Region != "us-east-1" {
		t.Errorf("expected account/region from args, got %q/%q", st.Account, st.Region)
	}
	if len(st.Modules) != 0 {
		t.Errorf("expected empty modules, got %d", len(st.Modules))
	}
}

func TestReadStateFile_CorruptedJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(DefaultStateFile, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := ReadStateFile(DefaultStateFile, "", "")
	if err == nil {
		t.Fatal("expected error for corrupted JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parsing state document") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestReadStateFile_Mismatch_ReturnsError(t *testing.T) {
	cases := []struct {
		name      string
		account   string
		region    string
		wantParts []string
	}{
		{
			name: "account", account: "222222222222", region: "us-east-1",
			wantParts: []string{"recorded account 111111111111 vs config 222222222222"},
		},
		{
			name: "region", account: "111111111111", region: "eu-west-1",
			wantParts: []string{"recorded home region us-east-1 vs config eu-west-1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			st := NewState("111111111111", "us-east-1")
			if err := WriteStateTo(st, DefaultStateFile); err != nil {
				t.Fatalf("WriteStateTo returned error: %v", err)
			}

			_, err := ReadStateFile(DefaultStateFile, tc.account, tc.region)
			if err == nil {
				t.Fatal("expected mismatch error, got nil")
			}
			for _, want := range append([]string{"state target mismatch"}, tc.wantParts...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}

			// The file is untouched — rejection happens before any caller mutates it.
			got, err := ReadStateFile(DefaultStateFile, "111111111111", "us-east-1")
			if err != nil {
				t.Fatalf("original target must still read cleanly: %v", err)
			}
			if got.Account != "111111111111" || got.Region != "us-east-1" {
				t.Errorf("state file was modified by the failed read: %q/%q", got.Account, got.Region)
			}
		})
	}
}

// TestReadStateFile_DDCEdge_HomeIdentityPreserved locks in the DDC edge
// behaviour: edges run in a foreign region but are bound to the home account
// and home region, so the home identity must validate cleanly.
func TestReadStateFile_DDCEdge_HomeIdentityPreserved(t *testing.T) {
	t.Chdir(t.TempDir())

	st := NewState("111111111111", "us-east-1")
	st.UpsertModule("ddc", "v1", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-edge-1", Properties: map[string]string{"region": "eu-west-1"}},
	})
	if err := WriteStateTo(st, DefaultStateFile); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	// An edge command still reads with the home identity (the edge region is
	// a command argument, never the state's recorded region).
	got, err := ReadStateFile(DefaultStateFile, "111111111111", "us-east-1")
	if err != nil {
		t.Fatalf("home identity must be accepted: %v", err)
	}
	if len(got.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(got.Modules))
	}
}

func TestValidateIdentity(t *testing.T) {
	cases := []struct {
		name      string
		st        *State
		account   string
		region    string
		wantErr   bool
		wantParts []string
	}{
		{name: "nil state accepted", st: nil, account: "111", region: "us-east-1"},
		{name: "exact match accepted", st: NewState("111111111111", "us-east-1"), account: "111111111111", region: "us-east-1"},
		{
			name: "account mismatch rejected", st: NewState("111111111111", "us-east-1"), account: "222222222222", region: "us-east-1",
			wantErr:   true,
			wantParts: []string{"recorded account 111111111111 vs config 222222222222", "state target mismatch"},
		},
		{
			name: "region mismatch rejected", st: NewState("111111111111", "us-east-1"), account: "111111111111", region: "eu-west-1",
			wantErr:   true,
			wantParts: []string{"recorded home region us-east-1 vs config eu-west-1", "state target mismatch"},
		},
		{
			name: "both mismatch rejected with both parts", st: NewState("111111111111", "us-east-1"), account: "222222222222", region: "eu-west-1",
			wantErr:   true,
			wantParts: []string{"recorded account 111111111111", "recorded home region us-east-1"},
		},
		{name: "empty recorded account accepted", st: NewState("", "us-east-1"), account: "111111111111", region: "us-east-1"},
		{name: "empty config region accepted", st: NewState("111111111111", "us-east-1"), account: "111111111111", region: ""},
		{name: "both sides empty accepted", st: NewState("111111111111", "us-east-1"), account: "", region: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIdentity(tc.st, tc.account, tc.region)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				for _, part := range tc.wantParts {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("error missing %q: %v", part, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil, got: %v", err)
			}
		})
	}
}

func TestStateFileFor(t *testing.T) {
	cases := []struct {
		configFile string
		want       string
	}{
		{"", ".fabrica/state.json"},
		{"fabrica.yaml", ".fabrica/state.json"},
		{"fabrica.yml", ".fabrica/state.json"},
		{filepath.Join("dir", "fabrica.yaml"), ".fabrica/state.json"},
		{"fabrica-prod.yaml", filepath.Join(".fabrica", "state-prod.json")},
		{"fabrica-prod.yml", filepath.Join(".fabrica", "state-prod.json")},
		{"prod.yaml", filepath.Join(".fabrica", "state-prod.json")},
		{filepath.Join("abs", "path", "fabrica-prod.yaml"), filepath.Join(".fabrica", "state-prod.json")},
		{"custom.yml", filepath.Join(".fabrica", "state-custom.json")},
		{filepath.Join("dir", "my config.yaml"), filepath.Join(".fabrica", "state-my_config.json")},
	}
	for _, tc := range cases {
		t.Run(tc.configFile, func(t *testing.T) {
			if got := StateFileFor(tc.configFile); got != tc.want {
				t.Errorf("StateFileFor(%q) = %q, want %q", tc.configFile, got, tc.want)
			}
		})
	}
}

func TestStateFileFor_CollidingNames_ShareFile(t *testing.T) {
	// Two configs that resolve to the same name share one state file on
	// purpose; a target conflict between them surfaces as a read error.
	t.Chdir(t.TempDir())

	if err := WriteStateTo(NewState("111111111111", "us-east-1"), StateFileFor("fabrica-prod.yaml")); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	if err := WriteStateTo(NewState("222222222222", "eu-west-1"), StateFileFor("prod.yaml")); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	// prod.yaml (same file) now records the second account, so reading it
	// back with the first target must be rejected.
	_, err := ReadStateFile(StateFileFor("fabrica-prod.yaml"), "111111111111", "us-east-1")
	if err == nil {
		t.Fatal("expected mismatch error when a colliding name changes targets, got nil")
	}
}

// TestConcurrentDifferentAccounts locks the issue's concurrency requirement:
// commands for different accounts running in the same working directory must
// each read and write only their own profile file.
func TestConcurrentDifferentAccounts(t *testing.T) {
	t.Chdir(t.TempDir())

	profiles := []struct {
		config  string
		account string
		region  string
	}{
		{"fabrica.yaml", "111111111111", "us-east-1"},
		{"fabrica-prod.yaml", "222222222222", "eu-west-1"},
		{"fabrica-staging.yaml", "333333333333", "ap-southeast-2"},
		{"fabrica-dev.yml", "444444444444", "us-west-2"},
		{"fabrica-qa.yaml", "555555555555", "eu-central-1"},
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(profiles)*10)
	for _, p := range profiles {
		wg.Add(1)
		go func(cfg, account, region string) {
			defer wg.Done()
			path := StateFileFor(cfg)
			for i := 0; i < 5; i++ {
				st := NewState(account, region)
				st.UpsertModule("perforce", fmt.Sprintf("v%d", i), "ready", []ModuleResource{
					{TypeName: "AWS::EC2::Instance", Identifier: fmt.Sprintf("i-%s-%d", account, i)},
				})
				if err := WriteStateTo(st, path); err != nil {
					errs <- fmt.Errorf("%s write: %w", cfg, err)
					return
				}
				got, err := ReadStateFile(path, account, region)
				if err != nil {
					errs <- fmt.Errorf("%s read: %w", cfg, err)
					return
				}
				if got.Account != account || got.Region != region {
					errs <- fmt.Errorf("%s cross-profile read: got %q/%q", cfg, got.Account, got.Region)
					return
				}
			}
		}(p.config, p.account, p.region)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Every file must carry exactly its own account.
	for _, p := range profiles {
		got, err := ReadStateFile(StateFileFor(p.config), p.account, p.region)
		if err != nil {
			t.Errorf("%s: %v", p.config, err)
			continue
		}
		if got.Account != p.account || got.Region != p.region {
			t.Errorf("%s: got %q/%q, want %q/%q", p.config, got.Account, got.Region, p.account, p.region)
		}
	}
}

func TestWriteState_RoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	original := NewState("123456789012", "eu-west-1")
	original.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc123", Properties: map[string]string{"k": "v"}},
	})

	if err := WriteState(original); err != nil {
		t.Fatalf("WriteState returned error: %v", err)
	}

	got, err := ReadStateFile(DefaultStateFile, "", "")
	if err != nil {
		t.Fatalf("ReadStateFile returned error: %v", err)
	}
	if got.Account != original.Account || got.Region != original.Region {
		t.Errorf("account/region not preserved: got %q/%q", got.Account, got.Region)
	}
	if len(got.Modules) != 1 || got.Modules[0].Name != "perforce" {
		t.Fatalf("module not preserved: %+v", got.Modules)
	}
	res := got.Modules[0].Resources
	if len(res) != 1 || res[0].Identifier != "i-abc123" {
		t.Errorf("resource not preserved: %+v", res)
	}
}

func TestWriteStateTo_CreatesDirAndFileWithMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	path := StateFileFor("fabrica-prod.yaml")
	if err := WriteStateTo(NewState("acct", "region"), path); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".fabrica")); err != nil {
		t.Errorf("expected .fabrica directory to be created: %v", err)
	}

	// File permission bits are not enforced by Windows; only assert on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			t.Fatalf("stat state file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected state file mode 0600, got %o", perm)
		}
	}
}

func TestWriteStateTo_DirPathOccupiedByFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Occupy the ".fabrica" name with a regular file so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(dir, ".fabrica"), []byte("x"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	if err := WriteStateTo(NewState("acct", "region"), StateFileFor("fabrica-prod.yaml")); err == nil {
		t.Fatal("expected error when .fabrica is a file, got nil")
	}
}

func TestReadStateFile_FileMissing_DebugLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf) // verbose enables debug

	t.Chdir(t.TempDir())

	st, err := ReadStateFile(DefaultStateFile, "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("ReadStateFile returned error: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "state file not found") {
		t.Errorf("expected debug log 'state file not found', got:\n%s", logOutput)
	}
}

func TestReadStateFile_ParseError_ErrorLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	dir := t.TempDir()
	t.Chdir(dir)
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(DefaultStateFile, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := ReadStateFile(DefaultStateFile, "", "")
	if err == nil {
		t.Fatal("expected error for corrupted JSON, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to parse state file") {
		t.Errorf("expected error log 'failed to parse state file', got:\n%s", logOutput)
	}
}

func TestReadStateFile_ReadError_ErrorLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	dir := t.TempDir()
	t.Chdir(dir)
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	// Create a directory at the state file path — ReadFile on a directory
	// returns an error that is NOT IsNotExist, exercising the read-error log.
	if err := os.Mkdir(DefaultStateFile, 0o700); err != nil {
		t.Fatalf("setup mkdir for state file: %v", err)
	}

	_, err := ReadStateFile(DefaultStateFile, "", "")
	if err == nil {
		t.Fatal("expected error when state file is a directory, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to read state file") {
		t.Errorf("expected error log 'failed to read state file', got:\n%s", logOutput)
	}
}

func TestReadStateFile_Mismatch_ErrorLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	t.Chdir(t.TempDir())
	if err := WriteStateTo(NewState("111111111111", "us-east-1"), DefaultStateFile); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	_, err := ReadStateFile(DefaultStateFile, "222222222222", "us-east-1")
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to parse state file") || !strings.Contains(logOutput, "state target mismatch") {
		t.Errorf("expected error log with 'state target mismatch', got:\n%s", logOutput)
	}
}

func TestWriteStateTo_MkdirAllError_ErrorLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	dir := t.TempDir()
	t.Chdir(dir)
	// Occupy ".fabrica" with a regular file so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(dir, ".fabrica"), []byte("x"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	err := WriteStateTo(NewState("acct", "region"), StateFileFor("fabrica-prod.yaml"))
	if err == nil {
		t.Fatal("expected error when .fabrica is a file, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to create .fabrica directory") {
		t.Errorf("expected error log 'failed to create .fabrica directory', got:\n%s", logOutput)
	}
}

func TestWriteStateTo_WriteFileError_ErrorLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	dir := t.TempDir()
	t.Chdir(dir)
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	// Pre-create the state file as read-only so WriteFile fails.
	if err := os.WriteFile(DefaultStateFile, []byte("existing"), 0444); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	err := WriteStateTo(NewState("acct", "region"), DefaultStateFile)
	if err == nil {
		t.Fatal("expected error when state file is read-only, got nil")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to write state file") {
		t.Errorf("expected error log 'failed to write state file', got:\n%s", logOutput)
	}
}

func TestWriteStateTo_Success_DebugLog(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(slog.LevelInfo, true, &buf) // verbose enables debug

	t.Chdir(t.TempDir())

	if err := WriteStateTo(NewState("acct", "region"), DefaultStateFile); err != nil {
		t.Fatalf("WriteStateTo returned error: %v", err)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "state written") {
		t.Errorf("expected debug log 'state written', got:\n%s", logOutput)
	}
}
