package perforce

import (
	"strings"
	"testing"
)

func TestParseBackupMeta_InvalidJSON(t *testing.T) {
	if _, err := ParseBackupMeta([]byte(`{`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseBackupMetaList_EmptyLinesAndError(t *testing.T) {
	// blank middle line must be skipped (not trimmed away by outer TrimSpace alone)
	stdout := "{\"id\":\"a\",\"status\":\"complete\",\"createdAt\":\"t\",\"sizeBytes\":1,\"helixVersion\":\"x\",\"serverRoot\":\"/r\"}\n\n{\"id\":\"b\",\"status\":\"complete\",\"createdAt\":\"t\",\"sizeBytes\":2,\"helixVersion\":\"x\",\"serverRoot\":\"/r\"}\n"
	list, err := ParseBackupMetaList(stdout)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if _, err := ParseBackupMetaList("{\"id\":\"a\"}\nbad\n"); err == nil {
		t.Fatal("expected error on second line")
	}
}

func TestGenerateRestoreScript_Errors(t *testing.T) {
	if _, err := GenerateRestoreScript(RestoreScriptConfig{}); err == nil {
		t.Fatal("empty id")
	}
	if _, err := GenerateRestoreScript(RestoreScriptConfig{BackupID: "x"}); err == nil {
		t.Fatal("empty password")
	}
	s, err := GenerateRestoreScript(RestoreScriptConfig{
		BackupID:      "id1",
		AdminPassword: "pw",
		ServerRoot:    "", // default
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, DefaultServerRoot) {
		t.Fatalf("expected default server root in %s", s)
	}
}

func TestGenerateReadMetaScript_EmptyID(t *testing.T) {
	if _, err := GenerateReadMetaScript("/x", ""); err == nil {
		t.Fatal("expected empty id error")
	}
}

func TestShellSingleQuoteEscapes(t *testing.T) {
	got := shellSingleQuote("a'b")
	if !strings.Contains(got, `'"'"'`) {
		t.Fatalf("got %q", got)
	}
}
