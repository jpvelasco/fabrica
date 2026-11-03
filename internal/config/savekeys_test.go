package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestSaveKeysRoundTripPreservesFile is the issue #558 test: a short
// hand-written fabrica.yaml keeps its comments, key order, indentation, and
// omitted sections after an in-place update.
func TestSaveKeysRoundTripPreservesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "# studio fabrica config\n" +
		"cloud:\n" +
		"  aws:\n" +
		"    region: us-west-2  # home region\n" +
		"\n" +
		"state:\n" +
		"  table: my-locks\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{
		"cloud.aws.accountId": "111222333444",
		"state.bucket":        "fabrica-state-111222333444",
	})
	if err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"# studio fabrica config",
		"# home region",
		`accountId: "111222333444"`,
		"bucket: fabrica-state-111222333444",
		"table: my-locks",
		"region: us-west-2",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("saved config missing %q:\n%s", want, text)
		}
	}
	// Sections the operator never wrote must not appear.
	for _, absent := range []string{"perforce:", "horde:", "ddc:", "workstation:"} {
		if strings.Contains(text, absent) {
			t.Errorf("saved config gained an unrequested section %q:\n%s", absent, text)
		}
	}
	// The file must still load as a config with the updated values.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after SaveKeys: %v", err)
	}
	if loaded.Cloud.AWS.AccountID != "111222333444" || loaded.State.Bucket != "fabrica-state-111222333444" ||
		loaded.State.Table != "my-locks" || loaded.Cloud.AWS.Region != "us-west-2" {
		t.Errorf("loaded values wrong: account=%q bucket=%q table=%q region=%q",
			loaded.Cloud.AWS.AccountID, loaded.State.Bucket, loaded.State.Table, loaded.Cloud.AWS.Region)
	}
}

// TestSaveKeysOverwritesExistingScalarInPlace replaces a present scalar
// value while keeping its comment, and quotes a numeric-looking string so it
// round-trips as a string.
func TestSaveKeysOverwritesExistingScalarInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "cloud:\n  aws:\n    accountId: 999988887777  # old account\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "111222333444"}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, `accountId: "111222333444"`) {
		t.Errorf("accountId not updated in place with quotes:\n%s", text)
	}
	if !strings.Contains(text, "# old account") {
		t.Errorf("scalar comment lost:\n%s", text)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cloud.AWS.AccountID != "111222333444" {
		t.Errorf("AccountID = %q, want 111222333444", loaded.Cloud.AWS.AccountID)
	}
}

// TestSaveKeysCreatesFileWhenMissing verifies the bootstrap fallback: a
// missing file gets a full save of the current config.
func TestSaveKeysCreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")

	cfg := Defaults()
	if err := cfg.SaveKeys(path, map[string]any{"state.bucket": "my-bucket"}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.State.Bucket != "my-bucket" {
		t.Errorf("State.Bucket = %q, want my-bucket", loaded.State.Bucket)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "cloud:") || !strings.Contains(string(out), "state:") {
		t.Errorf("bootstrap save should write the full config:\n%s", out)
	}
}

// TestSaveKeysCreatesMissingIntermediateMappings verifies an update creates
// mapping levels that are absent from the file (a hand-written config with no
// cloud section at all) and that the untouched section survives.
func TestSaveKeysCreatesMissingIntermediateMappings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, []byte("state:\n  bucket: existing-bucket\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "111222333444"}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Cloud.AWS.AccountID != "111222333444" {
		t.Errorf("AccountID = %q, want 111222333444", loaded.Cloud.AWS.AccountID)
	}
	if loaded.State.Bucket != "existing-bucket" {
		t.Errorf("Bucket = %q, want existing-bucket", loaded.State.Bucket)
	}
}

// TestSaveKeysEmptyFile verifies the empty-file edge: an empty file becomes an
// empty mapping and the requested keys are added.
func TestSaveKeysEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "111222333444"}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Cloud.AWS.AccountID != "111222333444" {
		t.Errorf("AccountID = %q, want 111222333444", loaded.Cloud.AWS.AccountID)
	}
}

// TestSaveKeysFailsClosedOnInvalidYAML refuses to rewrite a file it cannot
// parse and leaves the bytes untouched.
func TestSaveKeysFailsClosedOnInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "cloud: [unclosed\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "1"})
	if err == nil {
		t.Fatal("SaveKeys on invalid YAML must fail")
	}
	if !strings.Contains(err.Error(), "not valid YAML") {
		t.Errorf("error should explain the file is not valid YAML: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != original {
		t.Errorf("file modified despite parse failure:\n%s", out)
	}
}

// TestSaveKeysRejectsNonMappingTopLevel refuses a list-toplevel file.
func TestSaveKeysRejectsNonMappingTopLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "- a\n- b\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{"state.bucket": "b"})
	if err == nil {
		t.Fatal("SaveKeys on a list-toplevel file must fail")
	}
	if !strings.Contains(err.Error(), "not a YAML mapping") {
		t.Errorf("error should name the mapping problem: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != original {
		t.Errorf("file modified despite rejection:\n%s", out)
	}
}

// TestSaveKeysRejectsNonMappingIntermediate fails when a dotted key passes
// through a scalar instead of a mapping.
func TestSaveKeysRejectsNonMappingIntermediate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "cloud: just-a-string\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "1"})
	if err == nil {
		t.Fatal("SaveKeys through a scalar intermediate must fail")
	}
	if !strings.Contains(err.Error(), `key "cloud" is not a mapping`) {
		t.Errorf("error should name the offending key: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != original {
		t.Errorf("file modified despite rejection:\n%s", out)
	}
}

// TestSaveKeysReadErrorSurfaces fails closed on a read error that is not a
// missing file (here: the path is a directory) and reports it.
func TestSaveKeysReadErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	err := cfg.SaveKeys(dir, map[string]any{"state.bucket": "b"})
	if err == nil {
		t.Fatal("SaveKeys on an unreadable path must fail")
	}
	if !strings.Contains(err.Error(), "reading config") {
		t.Errorf("error should name the read failure: %v", err)
	}
}

// TestSaveKeysRejectsNonStringOnScalarLeaf refuses to overwrite an existing
// scalar leaf with a non-string value (which would change its type).
func TestSaveKeysRejectsNonStringOnScalarLeaf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "state:\n  bucket: foo\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{"state.bucket": 42})
	if err == nil {
		t.Fatal("SaveKeys replacing a scalar leaf with a non-string must fail")
	}
	if !strings.Contains(err.Error(), "scalar") {
		t.Errorf("error should name the scalar mismatch: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != original {
		t.Errorf("file modified despite rejection:\n%s", out)
	}
}

// TestReplaceMappingChildMissingKey verifies the swap helper errors when the
// key is absent rather than silently doing nothing.
func TestReplaceMappingChildMissingKey(t *testing.T) {
	if err := replaceMappingChild(newYAMLMapping(), "absent", &yaml.Node{}); err == nil {
		t.Fatal("replaceMappingChild on a missing key must fail")
	}
}

// TestSaveKeysEmptyUpdatesIsNoOp verifies an empty updates map leaves the
// file byte-identical.
func TestSaveKeysEmptyUpdatesIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "# keep me\ncloud:\n  provider: aws\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := cfg.SaveKeys(path, nil); err != nil {
		t.Fatalf("nil updates: %v", err)
	}
	if err := cfg.SaveKeys(path, map[string]any{}); err != nil {
		t.Fatalf("empty updates: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != original {
		t.Errorf("file changed on empty updates:\n%s", out)
	}
}

// TestSaveKeysReplacesNonScalarLeaf swaps a list value and keeps the comment
// above the parent key.
func TestSaveKeysReplacesNonScalarLeaf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "# budgets keep the studio under control\n" +
		"cost:\n" +
		"  budgets:\n" +
		"    - scope: total\n" +
		"      monthly: 100\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	err := cfg.SaveKeys(path, map[string]any{
		"cost.budgets": []BudgetThreshold{
			{Scope: "total", Monthly: 42},
			{Scope: "perforce", Monthly: 20, WarnPct: 90},
		},
	})
	if err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "# budgets keep the studio under control") {
		t.Errorf("comment above cost: lost:\n%s", out)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cost.Budgets) != 2 || loaded.Cost.Budgets[0].Monthly != 42 ||
		loaded.Cost.Budgets[1].Scope != "perforce" || loaded.Cost.Budgets[1].WarnPct != 90 {
		t.Errorf("budgets not replaced: %+v", loaded.Cost.Budgets)
	}
}

// TestSaveKeysKeepsFourSpaceIndent preserves the file's indentation width.
func TestSaveKeysKeepsFourSpaceIndent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	original := "cloud:\n    aws:\n        region: us-east-1\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := cfg.SaveKeys(path, map[string]any{"cloud.aws.accountId": "1"}); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "    aws:") {
		t.Errorf("4-space indent lost:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimLeft(line, " ") == "aws:" && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			t.Errorf("found 2-space indented aws: line:\n%s", text)
		}
	}
}

// TestSaveKeysMissingFileWriteFailure verifies the bootstrap fallback
// surfaces a write error instead of swallowing it.
func TestSaveKeysMissingFileWriteFailure(t *testing.T) {
	cfg := Defaults()
	err := cfg.SaveKeys(filepath.Join(t.TempDir(), "no-such-dir", "fabrica.yaml"), map[string]any{"state.bucket": "b"})
	if err == nil {
		t.Fatal("SaveKeys with a missing parent directory must fail")
	}
}
