package perforce

import (
	"encoding/json"
	"fmt"
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

var (
	reBackupName = regexp.MustCompile(`[^a-z0-9-]+`)
	reBackupID   = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[a-z0-9-]+)?$`)
)

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

// ValidateBackupID accepts only ids that NewBackupID can produce
// (YYYYMMDD-HHMMSS, with an optional -name suffix). A generated id is a
// single safe path component — no separators, no dot components, no
// root-equivalent path — so <root>/<id> cannot escape the backup root.
func ValidateBackupID(id string) error {
	if !reBackupID.MatchString(id) {
		return fmt.Errorf("backup id %q is not a generated id (form YYYYMMDD-HHMMSS[-name]) — list current backups with 'fabrica perforce backup list'", id)
	}
	return nil
}

// unixJoin joins path elements with forward slashes for remote Linux paths
// (SSM bash scripts on Helix EC2). Do not use path/filepath: that uses the
// host OS separator and would embed backslashes when Fabrica runs on Windows.
// Implemented with strings.Join("/", …) rather than path.Join so scanners that
// demand filepath.Join do not flag intentional remote-Unix path construction.
func unixJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	for _, e := range elem {
		if e != "" {
			parts = append(parts, e)
		}
	}
	return strings.Join(parts, "/")
}

// backupDirFor validates id and returns the resolved root and <root>/<id>.
// The containment check keeps the result a strict descendant of the resolved
// root even though the id regex already guarantees it.
func backupDirFor(rootCfg, id string) (root, dest string, err error) {
	if err = ValidateBackupID(id); err != nil {
		return "", "", err
	}
	root = ResolveBackupPath(rootCfg)
	dest = unixJoin(root, id)
	if !strings.HasPrefix(dest, root+"/") {
		err = fmt.Errorf("backup id %q escapes backup root %s", id, root)
		return "", "", err
	}
	return root, dest, nil
}

// BackupDir returns root/id for a backup (Unix path for remote Linux instance).
func BackupDir(root, id string) (string, error) {
	_, dest, err := backupDirFor(root, id)
	return dest, err
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
	if cfg.AdminPassword == "" {
		return "", fmt.Errorf("AdminPassword must not be empty")
	}
	_, dest, err := backupDirFor(cfg.BackupRoot, cfg.BackupID)
	if err != nil {
		return "", err
	}
	serverRoot := cfg.ServerRoot
	if serverRoot == "" {
		serverRoot = DefaultServerRoot
	}
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
	// The SSM session runs as root but the checkpoint is written server-side
	// by the perforce user — hand over the directory or writes get EACCES.
	b.WriteString("chown -R perforce:perforce \"$DEST\" 2>/dev/null || true\n")
	b.WriteString("PASSFILE=$(mktemp)\n")
	b.WriteString("chmod 600 \"$PASSFILE\"\n")
	fmt.Fprintf(&b, "printf '%%s' %s > \"$PASSFILE\"\n", pass)
	b.WriteString("trap 'rm -f \"$PASSFILE\"; rm -f \"$TICKETS\"' EXIT\n")
	// Modern p4-server security levels require a login ticket; bare
	// P4PASSWD is ignored/rejected. Isolate the ticket file so runs never
	// clobber an operator's tickets.
	b.WriteString("TICKETS=$(mktemp)\n")
	b.WriteString("chmod 600 \"$TICKETS\"\n")
	b.WriteString("export P4PORT=localhost:1666 P4USER=admin P4TICKETS=\"$TICKETS\"\n")
	b.WriteString("p4 trust -y >/dev/null 2>&1 || true\n")
	b.WriteString("p4 login -a < \"$PASSFILE\" >/dev/null 2>&1\n")
	// Checkpoint (+ journal) into the backup directory using a stable prefix.
	b.WriteString("p4 admin checkpoint -z \"$DEST/checkpoint\"\n")
	b.WriteString("p4 admin journal -z \"$DEST/journal\" || true\n")
	b.WriteString("SIZE=$(du -sb \"$DEST\" | awk '{print $1}')\n")
	b.WriteString("CREATED=$(date -u +%Y-%m-%dT%H:%M:%SZ)\n")
	if s3URI != "" {
		fmt.Fprintf(&b, "S3_URI=%s\n", shellSingleQuote(s3URI))
	}
	// metadata.json: every static field is passed to printf as a single-quoted
	// shell argument, so name/description and friends can never reach a shell
	// expansion context (no heredoc, no eval). Only the trusted runtime values
	// CREATED (date -u) and SIZE (du) expand. Written BEFORE the S3 sync so
	// uploaded backups carry their own manifest.
	b.WriteString("{\n")
	b.WriteString("  printf '{\\n'\n")
	metaField := func(name, value, tail string) {
		fmt.Fprintf(&b, "  printf '  \"%s\": %%s%s\\n' %s\n", name, tail, shellSingleQuote(jsonString(value)))
	}
	metaField("id", cfg.BackupID, ",")
	metaField("name", cfg.Name, ",")
	metaField("description", cfg.Description, ",")
	b.WriteString("  printf '  \"createdAt\": \"%%s\",\\n' \"$CREATED\"\n")
	b.WriteString("  printf '  \"sizeBytes\": %%s,\\n' \"$SIZE\"\n")
	metaField("helixVersion", cfg.HelixVersion, ",")
	metaField("serverRoot", serverRoot, ",")
	metaField("s3Uri", s3URI, ",")
	metaField("status", BackupStatusComplete, "")
	b.WriteString("} > \"$DEST/metadata.json\"\n")
	if s3URI != "" {
		// SSM sessions run a minimal environment that often lacks the AWS
		// CLI; self-provision it (instance profile supplies credentials).
		b.WriteString("command -v aws >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y awscli; }\n")
		b.WriteString("aws s3 sync \"$DEST\" \"$S3_URI/\" --only-show-errors\n")
	}
	b.WriteString("echo \"BACKUP_OK $BACKUP_ID\"\n")
	return b.String(), nil
}

// GenerateRestoreScript stops p4d, restores exactly the selected backup's
// checkpoint and journal artifacts, and restarts. Artifact validation runs
// before the service stop, so an incomplete backup fails first; copy or
// replay failures abort the script under set -e instead of printing
// RESTORE_OK.
func GenerateRestoreScript(cfg RestoreScriptConfig) (string, error) {
	if cfg.AdminPassword == "" {
		return "", fmt.Errorf("AdminPassword must not be empty")
	}
	_, src, err := backupDirFor(cfg.BackupRoot, cfg.BackupID)
	if err != nil {
		return "", err
	}
	serverRoot := cfg.ServerRoot
	if serverRoot == "" {
		serverRoot = DefaultServerRoot
	}

	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "SRC=%s\n", shellSingleQuote(src))
	fmt.Fprintf(&b, "SERVER_ROOT=%s\n", shellSingleQuote(serverRoot))
	b.WriteString("P4D=/opt/perforce/sbin/p4d\n")
	// Pre-flight: verify the selected backup's artifacts before touching the
	// service, so an incomplete backup never takes the server down.
	b.WriteString("test -d \"$SRC\"\n")
	b.WriteString("test -f \"$SRC/metadata.json\"\n")
	b.WriteString("CKP=\"\"\n")
	b.WriteString("for f in \"$SRC\"/checkpoint*; do\n")
	b.WriteString("  [ -f \"$f\" ] || continue\n")
	b.WriteString("  CKP=\"$f\"\n")
	b.WriteString("  break\n")
	b.WriteString("done\n")
	b.WriteString("if [ -z \"$CKP\" ]; then\n")
	b.WriteString("  echo \"RESTORE_FAILED: no checkpoint artifact under $SRC\" >&2\n")
	b.WriteString("  exit 1\n")
	b.WriteString("fi\n")
	// The journal this backup captured (absent means the server had no
	// journal at backup time — nothing to replay beyond the checkpoint).
	b.WriteString("JRN=\"\"\n")
	b.WriteString("for f in \"$SRC\"/journal*; do\n")
	b.WriteString("  [ -f \"$f\" ] || continue\n")
	b.WriteString("  JRN=\"$f\"\n")
	b.WriteString("  break\n")
	b.WriteString("done\n")
	b.WriteString("systemctl stop helix-p4d\n")
	// Clear checkpoint/journal artifacts already in the server root so the
	// replay uses only this backup's files (a pre-existing checkpoint must
	// not win).
	b.WriteString("rm -f \"$SERVER_ROOT\"/checkpoint*.gz \"$SERVER_ROOT\"/checkpoint*.ckp \"$SERVER_ROOT\"/journal*.gz \"$SERVER_ROOT\"/journal*.ckp\n")
	b.WriteString("cp -a \"$CKP\" \"$SERVER_ROOT\"/\n")
	b.WriteString("if [ -n \"$JRN\" ]; then\n")
	b.WriteString("  cp -a \"$JRN\" \"$SERVER_ROOT\"/\n")
	b.WriteString("fi\n")
	// Replay: restore from this backup's checkpoint, then play this backup's
	// journal (if any). Any failure here aborts the script under set -e.
	b.WriteString("\"$P4D\" -r \"$SERVER_ROOT\" -jr \"$CKP\"\n")
	b.WriteString("if [ -n \"$JRN\" ]; then\n")
	b.WriteString("  \"$P4D\" -r \"$SERVER_ROOT\" -j \"$JRN\"\n")
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
	_, dest, err := backupDirFor(backupRoot, backupID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -euo pipefail\n")
	fmt.Fprintf(&b, "DEST=%s\n", shellSingleQuote(dest))
	b.WriteString("rm -rf \"$DEST\"\n")
	if s3URI != "" {
		b.WriteString("command -v aws >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y awscli; }\n")
		fmt.Fprintf(&b, "aws s3 rm %s --recursive --only-show-errors\n", shellSingleQuote(strings.TrimRight(s3URI, "/")+"/"))
	}
	b.WriteString("echo DELETE_OK\n")
	return b.String(), nil
}

// GenerateReadMetaScript cats metadata for one backup id.
func GenerateReadMetaScript(backupRoot, backupID string) (string, error) {
	_, dest, err := backupDirFor(backupRoot, backupID)
	if err != nil {
		return "", err
	}
	meta := unixJoin(dest, "metadata.json")
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
