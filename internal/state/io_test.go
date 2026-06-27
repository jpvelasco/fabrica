package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadStateOrNew_FileMissing_ReturnsNew(t *testing.T) {
	t.Chdir(t.TempDir())

	st, err := ReadStateOrNew("123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("ReadStateOrNew returned error: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
	}
	if st.Account != "123456789012" || st.Region != "us-east-1" {
		t.Errorf("expected account/region from args, got %q/%q", st.Account, st.Region)
	}
	if len(st.Modules) != 0 {
		t.Errorf("expected empty modules, got %d", len(st.Modules))
	}
}

func TestReadStateOrNew_CorruptedJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("{not valid json"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	_, err := ReadStateOrNew("", "")
	if err == nil {
		t.Fatal("expected error for corrupted JSON, got nil")
	}
}

func TestWriteThenReadState_RoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	original := NewState("123456789012", "eu-west-1")
	original.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc123", Properties: map[string]string{"k": "v"}},
	})

	if err := WriteState(original); err != nil {
		t.Fatalf("WriteState returned error: %v", err)
	}

	got, err := ReadStateOrNew("", "")
	if err != nil {
		t.Fatalf("ReadStateOrNew returned error: %v", err)
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

func TestWriteState_CreatesDirAndFileWithMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := WriteState(NewState("acct", "region")); err != nil {
		t.Fatalf("WriteState returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".fabrica")); err != nil {
		t.Errorf("expected .fabrica directory to be created: %v", err)
	}

	// File permission bits are not enforced by Windows; only assert on Unix.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, stateFile))
		if err != nil {
			t.Fatalf("stat state file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected state file mode 0600, got %o", perm)
		}
	}
}

func TestWriteState_DirPathOccupiedByFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Occupy the ".fabrica" name with a regular file so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(dir, ".fabrica"), []byte("x"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	if err := WriteState(NewState("acct", "region")); err == nil {
		t.Fatal("expected error when .fabrica is a file, got nil")
	}
}
