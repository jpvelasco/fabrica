package ami

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Independent copies of the pinned fingerprints: the tests assert the literal
// values so a wrong constant in the generator cannot pass by matching itself.
const (
	testDockerKeyFPR    = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	testMongoDBKeyFPR   = "E58830201F7DD82CD808AA84160D26BB1785BA38"
	testMicrosoftKeyFPR = "BC528686B50D79E339D3721CEB3E94ADBE1229CF"
	testAWSCliKeyFPR    = "FB5DB77FD5C118B80511ADA8A6310ACC4672475C"
)

func renderBakeTemplate(t *testing.T, tmpl, install string) string {
	t.Helper()
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.8.0",
		Install:   install,
		Name:      "test-horde",
		BaseImage: "ami-abc123",
		Region:    "us-east-1",
	}}
	out, err := bc.renderTemplate(tmpl, bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate(%s, %s): %v", tmpl, install, err)
	}
	return string(out)
}

// forEachBakeOutput runs fn against the rendered Image Builder component and
// Packer template for both install modes.
func forEachBakeOutput(t *testing.T, fn func(t *testing.T, tmpl, install, rendered string)) {
	t.Helper()
	for _, tmpl := range []string{"component.yaml.tmpl", "packer.hcl.tmpl"} {
		for _, install := range []string{"docker", "native"} {
			t.Run(tmpl+"/"+install, func(t *testing.T) {
				fn(t, tmpl, install, renderBakeTemplate(t, tmpl, install))
			})
		}
	}
}

func TestBakesHaveNoPipeToShellOrUnverifiedDownloads(t *testing.T) {
	forbidden := []string{
		"| sh",
		"| bash",
		"get.docker.com",
		"packages-microsoft-prod.deb",
		"server-7.0.asc | gpg",
		"--dearmor",
	}
	forEachBakeOutput(t, func(t *testing.T, _, _, rendered string) {
		for _, bad := range forbidden {
			if strings.Contains(rendered, bad) {
				t.Errorf("rendered bake must not contain %q", bad)
			}
		}
	})
}

// A grep on the colon-format fingerprint line is not a trust gate: a file that
// also carries another key still passes it, and signed-by would trust both. The
// bake must instead export only the pinned key.
func TestBakesDoNotUseFingerprintGrepAsTrustGate(t *testing.T) {
	forEachBakeOutput(t, func(t *testing.T, _, _, rendered string) {
		for _, bad := range []string{"grep -x 'fpr", "--show-keys", "signed-by=/etc/apt/keyrings/docker.asc"} {
			if strings.Contains(rendered, bad) {
				t.Errorf("rendered bake must not use fingerprint grep or an armored keyring (found %q)", bad)
			}
		}
	})
}

func TestComponentAWSCliIsPresenceCheckedNotDownloaded(t *testing.T) {
	for _, install := range []string{"docker", "native"} {
		t.Run(install, func(t *testing.T) {
			s := renderBakeTemplate(t, "component.yaml.tmpl", install)
			if !strings.Contains(s, "test -x /usr/local/bin/aws") {
				t.Error("component must fail when the managed aws-cli-version-2-linux component did not install /usr/local/bin/aws")
			}
			for _, bad := range []string{"awscli-exe-linux", "aws/install", "awscliv2.zip"} {
				if strings.Contains(s, bad) {
					t.Errorf("component must not download or install the AWS CLI itself (found %q)", bad)
				}
			}
		})
	}
}

func TestPackerAWSCliSignatureVerifiedBeforeInstall(t *testing.T) {
	for _, install := range []string{"docker", "native"} {
		t.Run(install, func(t *testing.T) {
			s := renderBakeTemplate(t, "packer.hcl.tmpl", install)
			for _, want := range []string{
				"awscli-exe-linux-$(uname -m).zip.sig",
				"gpg --batch --quiet --export " + testAWSCliKeyFPR + " >",
				"gpg --batch --no-default-keyring --keyring",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("packer AWS CLI install missing %q", want)
				}
			}
			verify := strings.Index(s, "--verify")
			installAt := strings.Index(s, "aws/install")
			if verify < 0 || installAt < 0 {
				t.Fatal("packer AWS CLI install must contain gpg --verify and the aws/install step")
			}
			if verify > installAt {
				t.Error("gpg --verify must run before aws/install")
			}
		})
	}
}

func TestBakeAptReposExportPinnedKeyring(t *testing.T) {
	cases := map[string][]string{
		"docker": {
			"gpg --batch --quiet --export " + testDockerKeyFPR + " >",
			"signed-by=/etc/apt/keyrings/docker.gpg",
			"https://download.docker.com/linux/ubuntu",
		},
		"native": {
			"gpg --batch --quiet --export " + testMicrosoftKeyFPR + " >",
			"signed-by=/etc/apt/keyrings/microsoft-prod.gpg",
			"https://packages.microsoft.com/ubuntu/22.04/prod",
			"gpg --batch --quiet --export " + testMongoDBKeyFPR + " >",
			"signed-by=/etc/apt/keyrings/mongodb-server-7.0.gpg",
			"https://repo.mongodb.org/apt/ubuntu jammy/mongodb-org/7.0",
		},
	}
	for install, wants := range cases {
		for _, tmpl := range []string{"component.yaml.tmpl", "packer.hcl.tmpl"} {
			t.Run(install+"/"+tmpl, func(t *testing.T) {
				s := renderBakeTemplate(t, tmpl, install)
				for _, want := range wants {
					if !strings.Contains(s, want) {
						t.Errorf("rendered %s bake missing %q", install, want)
					}
				}
			})
		}
	}
}

func TestBakeInstallScriptsParseAsBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	scripts := map[string]string{
		"dockerInstall":         dockerInstallScript(),
		"dotnetInstall":         dotnetInstallScript(),
		"mongoDBInstall":        mongoDBInstallScript(),
		"awsCliVerifiedInstall": awsCliVerifiedInstallScript(),
		"awsCliPresenceCheck":   awsCliPresenceCheckScript(),
		"dockerBake":            dockerBakeScript("5.8.0"),
	}
	dir := t.TempDir()
	for name, body := range scripts {
		path := filepath.Join(dir, name+".sh")
		if err := os.WriteFile(path, []byte("set -euo pipefail\n"+body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
			t.Errorf("%s does not parse as bash: %v\n%s", name, err, out)
		}
	}
}

// runPinnedKeyring runs the generated export step under bash -euo pipefail and
// returns the keyring path it wrote.
func runPinnedKeyring(t *testing.T, bash, keyFile, fpr string) (keyring string, err error) {
	t.Helper()
	keyring = filepath.Join(t.TempDir(), "pinned.gpg")
	cmd := exec.Command(bash, "-euo", "pipefail", "-c", pinnedKeyringCommands(keyFile, fpr, keyring))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("export step output: %s", out)
	}
	return keyring, err
}

// The export must fail closed: a key file that lacks the pinned fingerprint
// produces an empty keyring and a non-zero exit before anything is trusted.
func TestPinnedKeyringFailsClosed(t *testing.T) {
	bash, _ := requireTools(t)
	keyFile := filepath.Join(t.TempDir(), "aws.asc")
	if err := os.WriteFile(keyFile, []byte(awsCliPublicKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPinnedKeyring(t, bash, keyFile, testAWSCliKeyFPR); err != nil {
		t.Errorf("pinned fingerprint should export a keyring, got: %v", err)
	}
	if _, err := runPinnedKeyring(t, bash, keyFile, testDockerKeyFPR); err == nil {
		t.Error("a key file without the pinned fingerprint must fail the export step")
	}
}

// signed-by trusts every key in the keyring, so a second key in the downloaded
// file must be imported but left out of the exported keyring.
func TestPinnedKeyringExportsOnlyPinnedKey(t *testing.T) {
	bash, gpg := requireTools(t)
	home := newGnupgHome(t)
	gpgEnv := append(os.Environ(), "GNUPGHOME="+home)
	gen := exec.Command(gpg, "--batch", "--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-gen-key", "extra@example.invalid", "ed25519", "default", "never")
	gen.Env = gpgEnv
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not generate a throwaway key: %v\n%s", err, out)
	}
	export := exec.Command(gpg, "--batch", "--armor", "--export", "extra@example.invalid")
	export.Env = gpgEnv
	extra, err := export.Output()
	if err != nil {
		t.Fatalf("export throwaway key: %v", err)
	}
	keyFile := filepath.Join(t.TempDir(), "two.asc")
	if err := os.WriteFile(keyFile, append([]byte(awsCliPublicKey), extra...), 0o600); err != nil {
		t.Fatal(err)
	}
	keyring, err := runPinnedKeyring(t, bash, keyFile, testAWSCliKeyFPR)
	if err != nil {
		t.Fatalf("export of the pinned key from a two-key file failed: %v", err)
	}
	list := exec.Command(gpg, "--batch", "--with-colons", "--show-keys", keyring)
	list.Env = gpgEnv
	out, err := list.Output()
	if err != nil {
		t.Fatalf("list exported keyring: %v", err)
	}
	if n := countColonRecords(string(out), "pub:"); n != 1 {
		t.Errorf("exported keyring should hold exactly one primary key, got %d:\n%s", n, out)
	}
	if !strings.Contains(string(out), "fpr:::::::::"+testAWSCliKeyFPR+":") {
		t.Errorf("exported keyring does not carry the pinned fingerprint:\n%s", out)
	}
}

func TestEmbeddedAWSCliKeyMatchesPinnedFingerprint(t *testing.T) {
	_, gpg := requireTools(t)
	keyFile := filepath.Join(t.TempDir(), "aws.asc")
	if err := os.WriteFile(keyFile, []byte(awsCliPublicKey), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(gpg, "--batch", "--with-colons", "--show-keys", keyFile).Output()
	if err != nil {
		t.Fatalf("gpg --show-keys: %v", err)
	}
	if !strings.Contains(string(out), "fpr:::::::::"+testAWSCliKeyFPR+":") {
		t.Errorf("embedded AWS CLI key does not carry fingerprint %s:\n%s", testAWSCliKeyFPR, out)
	}
}

// countColonRecords counts the lines of gpg --with-colons output that start
// with the given record type, e.g. "pub:".
func countColonRecords(output, record string) int {
	n := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, record) {
			n++
		}
	}
	return n
}

// newGnupgHome returns an empty GNUPGHOME with the 0700 mode gpg insists on.
// t.TempDir subdirectories are not 0700, so gpg would warn about them.
func newGnupgHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "gnupg")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func requireTools(t *testing.T) (bash, gpg string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	gpg, err = exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not available")
	}
	return bash, gpg
}
