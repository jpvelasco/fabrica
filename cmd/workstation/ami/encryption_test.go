package ami

// Tests for the encrypted, tagged bake (#507, #509): the generated recipe
// encrypts the root volume and tags the output AMI, generation fails closed
// on a recipe that would bake an unencrypted root snapshot, the build guide
// documents the encrypted bake paths, and the generated verify-ami.sh gate
// (executed under bash against a fake aws CLI) passes only an AMI whose EBS
// snapshots are all encrypted and tagged ManagedBy=fabrica.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// renderBuild runs the generator into a temp dir and returns it.
func renderBuild(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	c := buildCommand{
		out:       &bytes.Buffer{},
		cfg:       BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "fabrica-workstation-dcv", OutputDir: dir},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return dir
}

func TestBuildRecipeRootVolumeEncryptedAndAMITagged(t *testing.T) {
	dir := renderBuild(t)
	raw, err := os.ReadFile(filepath.Join(dir, "image-builder-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recipe struct {
		BlockDeviceMappings []struct {
			DeviceName string `json:"deviceName"`
			Ebs        struct {
				Encrypted *bool `json:"encrypted"`
			} `json:"ebs"`
		} `json:"blockDeviceMappings"`
		AmiTags map[string]string `json:"amiTags"`
	}
	if err := json.Unmarshal(raw, &recipe); err != nil {
		t.Fatalf("recipe JSON: %v", err)
	}
	if len(recipe.BlockDeviceMappings) != 1 || recipe.BlockDeviceMappings[0].DeviceName != "/dev/sda1" {
		t.Fatalf("blockDeviceMappings = %+v, want the /dev/sda1 root only", recipe.BlockDeviceMappings)
	}
	if enc := recipe.BlockDeviceMappings[0].Ebs.Encrypted; enc == nil || !*enc {
		t.Fatalf("root ebs.encrypted = %v, want true", enc)
	}
	for k, want := range map[string]string{"ManagedBy": "fabrica", "FabricaModule": "workstation", "Name": "fabrica-workstation-dcv"} {
		if got := recipe.AmiTags[k]; got != want {
			t.Errorf("amiTags[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestValidateRecipeEncryption(t *testing.T) {
	for _, tc := range []struct {
		name, recipe, wantErr string
	}{
		{name: "encrypted root", recipe: `{"blockDeviceMappings":[{"deviceName":"/dev/sda1","ebs":{"encrypted":true}}]}`},
		{name: "encrypted false", recipe: `{"blockDeviceMappings":[{"deviceName":"/dev/sda1","ebs":{"encrypted":false}}]}`, wantErr: `"encrypted": true`},
		{name: "encrypted absent", recipe: `{"blockDeviceMappings":[{"deviceName":"/dev/sda1","ebs":{"volumeSize":50}}]}`, wantErr: `"encrypted": true`},
		{name: "no ebs block", recipe: `{"blockDeviceMappings":[{"deviceName":"/dev/sda1"}]}`, wantErr: `"encrypted": true`},
		{name: "only a data volume", recipe: `{"blockDeviceMappings":[{"deviceName":"/dev/sdf","ebs":{"encrypted":true}}]}`, wantErr: "no /dev/sda1"},
		{name: "no mappings", recipe: `{}`, wantErr: "no /dev/sda1"},
		{name: "invalid json", recipe: `{`, wantErr: "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRecipeEncryption([]byte(tc.recipe))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildGuideDocumentsEncryptedTaggedBake(t *testing.T) {
	dir := renderBuild(t)
	raw, err := os.ReadFile(filepath.Join(dir, "build-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	g := string(raw)
	for _, want := range []string{
		`"encrypted": true`,
		"Encrypted=true",
		"DeviceName=/dev/sda1",
		"ResourceType=image,Tags=[{Key=ManagedBy,Value=fabrica}",
		"ResourceType=snapshot,Tags=[{Key=ManagedBy,Value=fabrica}",
		"aws ec2 create-tags --region us-west-2",
		"./verify-ami.sh",
		"Exit 0 is the only pass",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("build guide missing %q", want)
		}
	}
}

func TestBuildWritesExecutableVerifyScript(t *testing.T) {
	dir := renderBuild(t)
	path := filepath.Join(dir, "verify-ami.sh")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("verify-ami.sh mode = %v, want owner-executable", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"set -euo pipefail", `REGION="us-west-2"`, "describe-images", "describe-snapshots", "ManagedBy"} {
		if !strings.Contains(s, want) {
			t.Errorf("verify-ami.sh missing %q", want)
		}
	}
	// The gate is read-only: it never creates, tags, copies, or deletes.
	for _, banned := range []string{"aws ec2 create-", "aws ec2 copy-", "aws ec2 delete-", "aws ec2 deregister-"} {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), banned) {
				t.Errorf("verify-ami.sh runs a mutating command: %q", line)
			}
		}
	}
}

// fakeAWS is a stand-in aws CLI. It answers the four queries verify-ami.sh
// makes from environment variables and fails when FAKE_AWS_FAIL is set.
const fakeAWS = `#!/usr/bin/env bash
if [ -n "${FAKE_AWS_FAIL:-}" ]; then echo "fake aws: AccessDenied" >&2; exit 254; fi
args="$*"
case "$args" in
  *describe-images*Ebs.Encrypted*) printf '%s\n' "$FAKE_IMAGE_ENCRYPTED" ;;
  *describe-images*Ebs.SnapshotId*) printf '%s\n' "$FAKE_SNAPSHOTS" ;;
  *describe-snapshots*Snapshots\[0\].Encrypted*) printf '%s\n' "$FAKE_SNAPSHOT_ENCRYPTED" ;;
  *describe-snapshots*ManagedBy*) printf '%s\n' "$FAKE_SNAPSHOT_MANAGEDBY" ;;
  *) echo "fake aws: unexpected call: $args" >&2; exit 99 ;;
esac
`

func TestVerifyAMIScriptGate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executed gate runs under a POSIX shell; the linux and macOS legs run it")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	dir := renderBuild(t)
	script := filepath.Join(dir, "verify-ami.sh")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(fakeAWS), 0o755); err != nil { // #nosec G306 -- test-only fake CLI must be executable
		t.Fatal(err)
	}

	pass := map[string]string{
		"FAKE_IMAGE_ENCRYPTED":    "True",
		"FAKE_SNAPSHOTS":          "test-root-snapshot",
		"FAKE_SNAPSHOT_ENCRYPTED": "True",
		"FAKE_SNAPSHOT_MANAGEDBY": "fabrica",
	}
	with := func(over map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range pass {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		wantCode int
		wantOut  string
	}{
		{name: "encrypted and tagged passes", args: []string{"test-image"}, env: pass, wantCode: 0, wantOut: "OK: AMI test-image has 1 EBS snapshot(s)"},
		{name: "two snapshots pass", args: []string{"test-image"}, env: with(map[string]string{"FAKE_IMAGE_ENCRYPTED": "True\tTrue", "FAKE_SNAPSHOTS": "root\tdata"}), wantCode: 0, wantOut: "2 EBS snapshot(s)"},
		{name: "unencrypted image mapping fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_IMAGE_ENCRYPTED": "False"}), wantCode: 1, wantOut: "not encrypted (Encrypted=False)"},
		{name: "one of two unencrypted fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_IMAGE_ENCRYPTED": "True\tFalse"}), wantCode: 1, wantOut: "not encrypted"},
		{name: "missing image (None) fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_IMAGE_ENCRYPTED": "None"}), wantCode: 1, wantOut: "Encrypted=None"},
		{name: "no EBS snapshots fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_IMAGE_ENCRYPTED": ""}), wantCode: 1, wantOut: "no EBS snapshots"},
		{name: "unencrypted snapshot fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_SNAPSHOT_ENCRYPTED": "False"}), wantCode: 1, wantOut: "snapshot test-root-snapshot of AMI test-image is not encrypted"},
		{name: "untagged snapshot fails", args: []string{"test-image"}, env: with(map[string]string{"FAKE_SNAPSHOT_MANAGEDBY": "None"}), wantCode: 1, wantOut: "not tagged ManagedBy=fabrica"},
		{name: "aws error fails closed", args: []string{"test-image"}, env: with(map[string]string{"FAKE_AWS_FAIL": "1"}), wantCode: 254, wantOut: "AccessDenied"},
		{name: "missing argument", args: nil, env: pass, wantCode: 2, wantOut: "usage:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bash, append([]string{script}, tc.args...)...) // #nosec G204 -- test runs the generated gate script
			env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			for k, v := range tc.env {
				env = append(env, k+"="+v)
			}
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("running gate: %v", err)
				}
				code = exitErr.ExitCode()
			}
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d; output:\n%s", code, tc.wantCode, out)
			}
			if !strings.Contains(string(out), tc.wantOut) {
				t.Fatalf("output missing %q:\n%s", tc.wantOut, out)
			}
		})
	}
}
