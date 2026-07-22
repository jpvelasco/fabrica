package perforce

import (
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
	if got := BackupDir("/hxdepots/fabrica-backups", "id1"); got != "/hxdepots/fabrica-backups/id1" {
		t.Errorf("BackupDir = %q", got)
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
		BackupID:      "id1",
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
	if !strings.Contains(script, "s3://my-bucket/p4/id1") {
		t.Error("expected s3 uri")
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
		BackupID: "x", AdminPassword: "p", S3Export: true,
	}); err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestGenerateRestoreScript(t *testing.T) {
	script, err := GenerateRestoreScript(RestoreScriptConfig{
		BackupID:      "id1",
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
	s, err := GenerateDeleteScript("", "id1", "s3://b/p/id1")
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
	s, err := GenerateReadMetaScript(DefaultBackupPath, "id1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "metadata.json") {
		t.Error("expected cat metadata")
	}
}
