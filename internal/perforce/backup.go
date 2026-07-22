package perforce

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultBackupPath is the on-instance directory for Fabrica-managed backups.
	DefaultBackupPath = "/hxdepots/fabrica-backups"
	// DefaultServerRoot is the Helix server root created by cloud-init.
	DefaultServerRoot = "/hxdepots"
	// DefaultS3Prefix is used when S3 export is enabled and no prefix is set.
	DefaultS3Prefix = "perforce-backups/"
	// BackupStatusComplete marks a finished backup inventory entry.
	BackupStatusComplete = "complete"
)

var reBackupName = regexp.MustCompile(`[^a-z0-9-]+`)

// BackupMeta is the metadata.json shape stored beside each backup on EBS.
type BackupMeta struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	CreatedAt    string `json:"createdAt"`
	SizeBytes    int64  `json:"sizeBytes"`
	HelixVersion string `json:"helixVersion"`
	ServerRoot   string `json:"serverRoot"`
	S3URI        string `json:"s3Uri,omitempty"`
	Status       string `json:"status"`
}

// BackupScriptConfig drives GenerateBackupScript.
type BackupScriptConfig struct {
	BackupID     string
	BackupRoot   string // parent of <id>/
	ServerRoot   string
	HelixVersion string
	Name         string
	Description  string
	// AdminPassword is written to a temp file on the instance; scripts must not echo it.
	AdminPassword string
	S3Export      bool
	S3Bucket      string
	S3Prefix      string
}

// RestoreScriptConfig drives GenerateRestoreScript.
type RestoreScriptConfig struct {
	BackupID      string
	BackupRoot    string
	ServerRoot    string
	AdminPassword string
}

// ResolveBackupPath returns cfg path or the default.
func ResolveBackupPath(cfgPath string) string {
	if strings.TrimSpace(cfgPath) == "" {
		return DefaultBackupPath
	}
	return strings.TrimRight(cfgPath, "/")
}

// ResolveS3Prefix returns cfg prefix or the default (always trailing slash).
func ResolveS3Prefix(prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		return DefaultS3Prefix
	}
	if !strings.HasSuffix(prefix, "/") {
		return prefix + "/"
	}
	return prefix
}

// SanitizeBackupName lowercases name and keeps [a-z0-9-], max 32 chars.
func SanitizeBackupName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = reBackupName.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// NewBackupID builds a UTC backup id: YYYYMMDD-HHMMSS or with optional name suffix.
func NewBackupID(now time.Time, name string) string {
	base := now.UTC().Format("20060102-150405")
	if n := SanitizeBackupName(name); n != "" {
		return base + "-" + n
	}
	return base
}

// unixJoin joins path elements with forward slashes for remote Linux paths
// (SSM bash scripts on Helix EC2). Do not use path/filepath: that uses the
// host OS separator and would embed backslashes when Fabrica runs on Windows.
func unixJoin(elem ...string) string {
	return path.Join(elem...)
}

// BackupDir returns root/id for a backup (Unix path for remote Linux instance).
func BackupDir(root, id string) string {
	return unixJoin(ResolveBackupPath(root), id)
}

// MarshalBackupMeta encodes metadata.json.
func MarshalBackupMeta(m BackupMeta) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// ParseBackupMeta decodes metadata.json.
func ParseBackupMeta(data []byte) (BackupMeta, error) {
	var m BackupMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return BackupMeta{}, fmt.Errorf("parsing backup metadata: %w", err)
	}
	if m.ID == "" {
		return BackupMeta{}, fmt.Errorf("backup metadata missing id")
	}
	return m, nil
}

// ParseBackupMetaList parses NDJSON (one metadata object per line) from list script stdout.
func ParseBackupMetaList(stdout string) ([]BackupMeta, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	out := make([]BackupMeta, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m, err := ParseBackupMeta([]byte(line))
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// GenerateBackupScript returns a bash script that checkpoints Helix and packages a backup.
func GenerateBackupScript(cfg BackupScriptConfig) (string, error) {
	if cfg.BackupID == "" {
		return "", fmt.Errorf("BackupID must not be empty")
	}
	if cfg.AdminPassword == "" {
		return "", fmt.Errorf("AdminPassword must not be empty")
	}
	root := ResolveBackupPath(cfg.BackupRoot)
	serverRoot := cfg.ServerRoot
	if serverRoot == "" {
		serverRoot = DefaultServerRoot
	}
	dest := unixJoin(root, cfg.BackupID)
	s3URI := ""
	if cfg.S3Export {
		if cfg.S3Bucket == "" {
			return "", fmt.Errorf("S3Bucket required when S3Export is true")
		}
		s3URI = fmt.Sprintf("s3://%s/%s%s", cfg.S3Bucket, ResolveS3Prefix(cfg.S3Prefix), cfg.BackupID)
	}

	pass := shellSingleQuote(cfg.AdminPassword)

	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "DEST=%s\n", shellSingleQuote(dest))
	fmt.Fprintf(&b, "SERVER_ROOT=%s\n", shellSingleQuote(serverRoot))
	fmt.Fprintf(&b, "BACKUP_ID=%s\n", shellSingleQuote(cfg.BackupID))
	b.WriteString("mkdir -p \"$DEST\"\n")
	b.WriteString("PASSFILE=$(mktemp)\n")
	b.WriteString("chmod 600 \"$PASSFILE\"\n")
	fmt.Fprintf(&b, "printf '%%s' %s > \"$PASSFILE\"\n", pass)
	b.WriteString("trap 'rm -f \"$PASSFILE\"' EXIT\n")
	b.WriteString("export P4PORT=localhost:1666 P4USER=admin\n")
	b.WriteString("export P4PASSWD=$(cat \"$PASSFILE\")\n")
	b.WriteString("p4 trust -y >/dev/null 2>&1 || true\n")
	// Checkpoint (+ journal) into the backup directory using a stable prefix.
	b.WriteString("p4 admin checkpoint -z \"$DEST/checkpoint\"\n")
	b.WriteString("p4 admin journal -z \"$DEST/journal\" || true\n")
	b.WriteString("SIZE=$(du -sb \"$DEST\" | awk '{print $1}')\n")
	b.WriteString("CREATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)\n")
	if s3URI != "" {
		fmt.Fprintf(&b, "S3_URI=%s\n", shellSingleQuote(s3URI))
		b.WriteString("aws s3 sync \"$DEST\" \"$S3_URI/\" --only-show-errors\n")
	}
	// metadata.json: static fields as JSON strings; SIZE/CREATED expanded by shell.
	b.WriteString("cat > \"$DEST/metadata.json\" <<EOF\n")
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"id\": %s,\n", jsonString(cfg.BackupID))
	fmt.Fprintf(&b, "  \"name\": %s,\n", jsonString(cfg.Name))
	fmt.Fprintf(&b, "  \"description\": %s,\n", jsonString(cfg.Description))
	b.WriteString("  \"createdAt\": \"$CREATED\",\n")
	b.WriteString("  \"sizeBytes\": $SIZE,\n")
	fmt.Fprintf(&b, "  \"helixVersion\": %s,\n", jsonString(cfg.HelixVersion))
	fmt.Fprintf(&b, "  \"serverRoot\": %s,\n", jsonString(serverRoot))
	if s3URI != "" {
		fmt.Fprintf(&b, "  \"s3Uri\": %s,\n", jsonString(s3URI))
	} else {
		b.WriteString("  \"s3Uri\": \"\",\n")
	}
	fmt.Fprintf(&b, "  \"status\": %s\n", jsonString(BackupStatusComplete))
	b.WriteString("}\n")
	b.WriteString("EOF\n")
	b.WriteString("echo \"BACKUP_OK $BACKUP_ID\"\n")
	return b.String(), nil
}

// GenerateRestoreScript stops p4d, restores checkpoint files, and restarts.
func GenerateRestoreScript(cfg RestoreScriptConfig) (string, error) {
	if cfg.BackupID == "" {
		return "", fmt.Errorf("BackupID must not be empty")
	}
	if cfg.AdminPassword == "" {
		return "", fmt.Errorf("AdminPassword must not be empty")
	}
	root := ResolveBackupPath(cfg.BackupRoot)
	serverRoot := cfg.ServerRoot
	if serverRoot == "" {
		serverRoot = DefaultServerRoot
	}
	src := unixJoin(root, cfg.BackupID)

	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "SRC=%s\n", shellSingleQuote(src))
	fmt.Fprintf(&b, "SERVER_ROOT=%s\n", shellSingleQuote(serverRoot))
	b.WriteString("test -f \"$SRC/metadata.json\"\n")
	b.WriteString("systemctl stop helix-p4d\n")
	// Restore checkpoint artifacts into the server root (Helix expects checkpoint files there).
	b.WriteString("cp -a \"$SRC\"/checkpoint* \"$SERVER_ROOT\"/ 2>/dev/null || true\n")
	b.WriteString("cp -a \"$SRC\"/journal* \"$SERVER_ROOT\"/ 2>/dev/null || true\n")
	// Replay via p4d offline restore if a compressed checkpoint exists.
	b.WriteString("CKP=$(ls -1 \"$SERVER_ROOT\"/checkpoint*.gz \"$SERVER_ROOT\"/checkpoint.ckp.gz 2>/dev/null | head -n1 || true)\n")
	b.WriteString("if [ -n \"$CKP\" ]; then\n")
	b.WriteString("  /opt/perforce/sbin/p4d -r \"$SERVER_ROOT\" -jr \"$CKP\"\n")
	b.WriteString("fi\n")
	b.WriteString("systemctl start helix-p4d\n")
	b.WriteString("echo RESTORE_OK\n")
	return b.String(), nil
}

// GenerateListScript emits one-line JSON metadata per backup directory.
func GenerateListScript(backupRoot string) string {
	root := ResolveBackupPath(backupRoot)
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "ROOT=%s\n", shellSingleQuote(root))
	b.WriteString("if [ ! -d \"$ROOT\" ]; then exit 0; fi\n")
	b.WriteString("for d in \"$ROOT\"/*/; do\n")
	b.WriteString("  [ -d \"$d\" ] || continue\n")
	b.WriteString("  if [ -f \"${d}metadata.json\" ]; then\n")
	b.WriteString("    tr -d '\\n' < \"${d}metadata.json\"\n")
	b.WriteString("    echo\n")
	b.WriteString("  fi\n")
	b.WriteString("done\n")
	return b.String()
}

// GenerateDeleteScript removes a backup directory and optional S3 URI.
func GenerateDeleteScript(backupRoot, backupID, s3URI string) (string, error) {
	if backupID == "" {
		return "", fmt.Errorf("backupID must not be empty")
	}
	root := ResolveBackupPath(backupRoot)
	dest := unixJoin(root, backupID)
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "DEST=%s\n", shellSingleQuote(dest))
	b.WriteString("rm -rf \"$DEST\"\n")
	if s3URI != "" {
		fmt.Fprintf(&b, "aws s3 rm %s --recursive --only-show-errors\n", shellSingleQuote(strings.TrimRight(s3URI, "/")+"/"))
	}
	b.WriteString("echo DELETE_OK\n")
	return b.String(), nil
}

// GenerateReadMetaScript cats metadata for one backup id.
func GenerateReadMetaScript(backupRoot, backupID string) (string, error) {
	if backupID == "" {
		return "", fmt.Errorf("backupID must not be empty")
	}
	meta := unixJoin(ResolveBackupPath(backupRoot), backupID, "metadata.json")
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "cat %s\n", shellSingleQuote(meta))
	return b.String(), nil
}

func shellSingleQuote(s string) string {
	// 'foo'bar'baz' → 'foo'"'"'bar'"'"'baz'
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
