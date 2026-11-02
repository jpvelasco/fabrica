package perforce

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSanitizeBackupName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Pre Ship", "pre-ship"},
		{"  Hello_World!! ", "hello-world"},
		{"", ""},
		{strings.Repeat("a", 40), strings.Repeat("a", 32)},
	}
	for _, tc := range tests {
		if got := SanitizeBackupName(tc.in); got != tc.want {
			t.Errorf("SanitizeBackupName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewBackupID(t *testing.T) {
	now := time.Date(2026, 7, 15, 14, 30, 22, 0, time.UTC)
	if got := NewBackupID(now, ""); got != "20260715-143022" {
		t.Errorf("no name: got %q", got)
	}
	if got := NewBackupID(now, "Pre Ship"); got != "20260715-143022-pre-ship" {
		t.Errorf("with name: got %q", got)
	}
}

func TestBackupDirAndResolve(t *testing.T) {
	if got := ResolveBackupPath(""); got != DefaultBackupPath {
		t.Errorf("default path = %q", got)
	}
	if got, err := BackupDir("/hxdepots/fabrica-backups", "20260715-143022"); err != nil || got != "/hxdepots/fabrica-backups/20260715-143022" {
		t.Errorf("BackupDir = %q, err = %v", got, err)
	}
	if _, err := BackupDir("/hxdepots/fabrica-backups", "../outside"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	// Multi-segment join must stay Unix-style (not host filepath.Join).
	if got := unixJoin(DefaultBackupPath, "id1", "metadata.json"); got != "/hxdepots/fabrica-backups/id1/metadata.json" {
		t.Errorf("unixJoin multi = %q", got)
	}
	if got := ResolveS3Prefix(""); got != DefaultS3Prefix {
		t.Errorf("default s3 prefix = %q", got)
	}
	if got := ResolveS3Prefix("foo"); got != "foo/" {
		t.Errorf("s3 prefix slash = %q", got)
	}
}

func TestMarshalParseBackupMeta(t *testing.T) {
	m := BackupMeta{
		ID:           "20260715-143022",
		Name:         "smoke",
		Description:  "test",
		CreatedAt:    "2026-07-15T14:30:22Z",
		SizeBytes:    99,
		HelixVersion: "2024.2",
		ServerRoot:   "/hxdepots",
		Status:       BackupStatusComplete,
	}
	raw, err := MarshalBackupMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseBackupMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != m.ID || got.SizeBytes != m.SizeBytes || got.Status != BackupStatusComplete {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestParseBackupMeta_MissingID(t *testing.T) {
	if _, err := ParseBackupMeta([]byte(`{"status":"complete"}`)); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestParseBackupMetaList(t *testing.T) {
	stdout := `{"id":"a","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}
{"id":"b","status":"complete","createdAt":"t","sizeBytes":2,"helixVersion":"2024.2","serverRoot":"/hxdepots"}
`
	list, err := ParseBackupMetaList(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("got %+v", list)
	}
}

func TestGenerateBackupScript(t *testing.T) {
	script, err := GenerateBackupScript(BackupScriptConfig{
		BackupID:      "20260715-143022-smoke",
		BackupRoot:    DefaultBackupPath,
		HelixVersion:  "2024.2",
		Name:          "smoke",
		Description:   "desc",
		AdminPassword: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"p4 admin checkpoint",
		"p4 admin journal",
		"metadata.json",
		"BACKUP_OK",
		"/hxdepots/fabrica-backups/20260715-143022-smoke",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if strings.Contains(script, "echo s3cret") {
		t.Error("script must not echo password")
	}
}

func TestGenerateBackupScript_S3(t *testing.T) {
	script, err := GenerateBackupScript(BackupScriptConfig{
		BackupID:      "20260715-143022",
		AdminPassword: "pw",
		S3Export:      true,
		S3Bucket:      "my-bucket",
		S3Prefix:      "p4/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "aws s3 sync") {
		t.Error("expected s3 sync")
	}
	if !strings.Contains(script, "s3://my-bucket/p4/20260715-143022") {
		t.Error("expected s3 uri")
	}
}

// TestGenerateBackupScript_ManifestBeforeSync verifies metadata.json is written
// before the S3 upload, so every exported backup carries its own manifest.
func TestGenerateBackupScript_ManifestBeforeSync(t *testing.T) {
	script, err := GenerateBackupScript(BackupScriptConfig{
		BackupID:      "20260715-143022",
		AdminPassword: "pw",
		S3Export:      true,
		S3Bucket:      "my-bucket",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestIdx := strings.Index(script, `} > "$DEST/metadata.json"`)
	syncIdx := strings.Index(script, "aws s3 sync")
	if manifestIdx < 0 || syncIdx < 0 {
		t.Fatalf("script missing manifest or sync:\n%s", script)
	}
	if manifestIdx > syncIdx {
		t.Errorf("metadata.json must be written BEFORE the s3 sync (manifest@%d, sync@%d)", manifestIdx, syncIdx)
	}
}

func TestGenerateBackupScript_Errors(t *testing.T) {
	if _, err := GenerateBackupScript(BackupScriptConfig{}); err == nil {
		t.Fatal("expected error for empty id")
	}
	if _, err := GenerateBackupScript(BackupScriptConfig{BackupID: "x"}); err == nil {
		t.Fatal("expected error for empty password")
	}
	if _, err := GenerateBackupScript(BackupScriptConfig{
		BackupID: "20260715-143022", AdminPassword: "p", S3Export: true,
	}); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestGenerateRestoreScript(t *testing.T) {
	script, err := GenerateRestoreScript(RestoreScriptConfig{
		BackupID:      "20260715-143022",
		AdminPassword: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"systemctl stop helix-p4d", "systemctl start helix-p4d", "RESTORE_OK", "p4d"} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestGenerateListScript(t *testing.T) {
	s := GenerateListScript("")
	if !strings.Contains(s, DefaultBackupPath) || !strings.Contains(s, "metadata.json") {
		t.Errorf("list script unexpected: %s", s)
	}
}

func TestGenerateDeleteScript(t *testing.T) {
	s, err := GenerateDeleteScript("", "20260715-143022", "s3://b/p/20260715-143022")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "rm -rf") || !strings.Contains(s, "aws s3 rm") {
		t.Errorf("delete script: %s", s)
	}
	if _, err := GenerateDeleteScript("", "", ""); err == nil {
		t.Fatal("expected empty id error")
	}
}

func TestGenerateReadMetaScript(t *testing.T) {
	s, err := GenerateReadMetaScript(DefaultBackupPath, "20260715-143022")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "metadata.json") {
		t.Error("expected cat metadata")
	}
}

// TestValidateBackupID pins the generated-id shape every boundary accepts
// and the traversal shapes it must reject.
func TestValidateBackupID(t *testing.T) {
	for _, id := range []string{"20260715-143022", "20260715-143022-pre-ship", "20260715-143022-a-b-c"} {
		if err := ValidateBackupID(id); err != nil {
			t.Errorf("ValidateBackupID(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{
		"",
		"id1",
		".",
		"..",
		"/",
		"../outside",
		"20260715-143022/../x",
		"20260715-143022/evil",
		"20260715-143022.",
		"20260715-143022-UPPER",
		" 20260715-143022",
	} {
		if err := ValidateBackupID(id); err == nil {
			t.Errorf("ValidateBackupID(%q) = nil, want error", id)
		}
	}
}

func TestGeneratorsRejectTraversalIDs(t *testing.T) {
	id := "20260715-143022/../x"
	if _, err := BackupDir(DefaultBackupPath, id); err == nil {
		t.Error("BackupDir: expected traversal error")
	}
	if _, err := GenerateBackupScript(BackupScriptConfig{BackupID: id, AdminPassword: "pw"}); err == nil {
		t.Error("GenerateBackupScript: expected traversal error")
	}
	if _, err := GenerateRestoreScript(RestoreScriptConfig{BackupID: id, AdminPassword: "pw"}); err == nil {
		t.Error("GenerateRestoreScript: expected traversal error")
	}
	if _, err := GenerateDeleteScript(DefaultBackupPath, id, ""); err == nil {
		t.Error("GenerateDeleteScript: expected traversal error")
	}
	if _, err := GenerateReadMetaScript(DefaultBackupPath, id); err == nil {
		t.Error("GenerateReadMetaScript: expected traversal error")
	}
}

// TestGenerateBackupScript_MetadataPayloadsStayLiteral verifies shell-active
// payloads survive only as single-quoted printf arguments: no heredoc, no
// expansion context, and each trusted runtime value expands exactly once.
func TestGenerateBackupScript_MetadataPayloadsStayLiteral(t *testing.T) {
	payload := "$(touch /tmp/fabrica-backup-pwn) `id` $HOME"
	script, err := GenerateBackupScript(BackupScriptConfig{
		BackupID:      "20260715-143022",
		BackupRoot:    DefaultBackupPath,
		Name:          payload,
		Description:   payload,
		AdminPassword: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "<<") {
		t.Fatal("script must not contain a heredoc")
	}
	for _, field := range []string{"name", "description"} {
		want := fmt.Sprintf("  printf '  \"%s\": %%s,\\n' %s", field, shellSingleQuote(jsonString(payload)))
		if !strings.Contains(script, want) {
			t.Errorf("script missing exact single-quoted printf line for %s:\n%s", field, script)
		}
	}
	if got := strings.Count(script, payload); got != 2 {
		t.Errorf("payload appears %d times, want exactly 2 (one per metadata field)", got)
	}
	for _, v := range []string{"$CREATED", "$SIZE"} {
		if got := strings.Count(script, v); got != 1 {
			t.Errorf("%s appears %d times, want 1 (trusted runtime expansion)", v, got)
		}
	}
}

// TestGenerateRestoreScript_FailClosed pins the fail-closed ordering: artifact
// pre-flight before the service stop, explicit artifact selection, cleared
// pre-existing server-root artifacts, and no swallowed failures.
func TestGenerateRestoreScript_FailClosed(t *testing.T) {
	script, err := GenerateRestoreScript(RestoreScriptConfig{
		BackupID:      "20260715-143022",
		AdminPassword: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	stopIdx := strings.Index(script, "systemctl stop helix-p4d")
	if stopIdx < 0 {
		t.Fatal("missing service stop")
	}
	for _, want := range []string{`test -f "$SRC/metadata.json"`, "RESTORE_FAILED", "exit 1"} {
		if idx := strings.Index(script, want); idx < 0 || idx > stopIdx {
			t.Errorf("expected %q before the service stop (stopIdx=%d)", want, stopIdx)
		}
	}
	if strings.Contains(script, "|| true") {
		t.Error("restore script must not swallow failures with '|| true'")
	}
	if strings.Contains(script, "head -n1") {
		t.Error("restore script must select artifacts explicitly, not head -n1")
	}
	for _, want := range []string{
		`rm -f "$SERVER_ROOT"/checkpoint*.gz`,
		`cp -a "$CKP" "$SERVER_ROOT"/`,
		`"$P4D" -r "$SERVER_ROOT" -jr "$CKP"`,
		`"$P4D" -r "$SERVER_ROOT" -j "$JRN"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q:\n%s", want, script)
		}
	}
}
